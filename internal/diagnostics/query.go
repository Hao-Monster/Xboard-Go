package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	MaxQueryReadLines       = 10000
	MaxQueryReadBytes       = 16 << 20 // 16 MiB
	MaxSnapshotItems        = 1000
	MaxSnapshotBytes        = 2 << 20 // 2 MiB
	DefaultQueryPageSize    = 100
	MaxQueryPageSize        = 200
	MaxDNSOperationsPerConn = 32
	CursorTTL               = 5 * time.Minute
	MaxSnapshotsPerAdmin    = 3
	MaxDomainLength         = 253
)

var (
	ErrStorageNotConfigured = errors.New("diagnostics storage not configured")
	ErrStorageUnavailable   = ErrBackendUnavailable
	ErrStorageTimeout       = ErrBackendTimeout
	ErrInvalidQueryFilters  = errors.New("invalid diagnostics query filters")

	traceIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// QueryFilters contains the validated parameters for filtering connection diagnostics.
type QueryFilters struct {
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	NodeID      *int64    `json:"node_id,omitempty"`
	Domain      *string   `json:"domain,omitempty"`
	DomainMatch string    `json:"domain_match,omitempty"` // exact, suffix, contains (default)
	TraceID     *string   `json:"trace_id,omitempty"`     // 32-hex
	HasError    *bool     `json:"has_error,omitempty"`
	ReasonCode  *string   `json:"reason_code,omitempty"`
}

// Normalize validates and fills defaults for QueryFilters.
func (f *QueryFilters) Normalize(now time.Time) error {
	if f.From.IsZero() {
		f.From = now.Add(-15 * time.Minute)
	}
	if f.To.IsZero() {
		f.To = now
	}
	if f.From.After(f.To) || f.From.Equal(f.To) {
		return fmt.Errorf("%w: from must be before to", ErrInvalidQueryFilters)
	}
	if f.To.After(now.Add(10 * time.Second)) {
		return fmt.Errorf("%w: to cannot exceed server time", ErrInvalidQueryFilters)
	}
	if f.To.Sub(f.From) > 72*time.Hour {
		return fmt.Errorf("%w: query window cannot exceed 72 hours", ErrInvalidQueryFilters)
	}

	if f.NodeID != nil && *f.NodeID <= 0 {
		return fmt.Errorf("%w: node_id must be a positive integer", ErrInvalidQueryFilters)
	}

	if f.Domain != nil {
		clean := strings.TrimSpace(strings.ToLower(*f.Domain))
		if len(clean) == 0 || len(clean) > MaxDomainLength {
			return fmt.Errorf("%w: domain must be 1-%d characters", ErrInvalidQueryFilters, MaxDomainLength)
		}
		if strings.ContainsAny(clean, " \t\r\n\"'\\;()") {
			return fmt.Errorf("%w: domain contains forbidden characters", ErrInvalidQueryFilters)
		}
		f.Domain = &clean
		switch f.DomainMatch {
		case "exact", "suffix", "contains":
		case "":
			f.DomainMatch = "contains"
		default:
			return fmt.Errorf("%w: invalid domain_match mode", ErrInvalidQueryFilters)
		}
	}

	if f.TraceID != nil {
		traceID := strings.TrimSpace(strings.ToLower(*f.TraceID))
		if !traceIDRegex.MatchString(traceID) {
			return fmt.Errorf("%w: trace_id must be 32 lowercase hex characters", ErrInvalidQueryFilters)
		}
		f.TraceID = &traceID
	}

	if f.ReasonCode != nil {
		reason := strings.TrimSpace(strings.ToLower(*f.ReasonCode))
		if len(reason) == 0 || len(reason) > 64 {
			return fmt.Errorf("%w: reason_code must be 1-64 characters", ErrInvalidQueryFilters)
		}
		f.ReasonCode = &reason
	}

	return nil
}

// EncodeLogsQL constructs a safe LogsQL expression pushing down scope, customer identity, and primary filters.
func EncodeLogsQL(panelScope, diagnosticID string, f QueryFilters) (string, error) {
	if len(panelScope) != 36 || len(diagnosticID) != 24 {
		return "", ErrInvalidQueryFilters
	}

	var parts []string
	parts = append(parts, fmt.Sprintf(`panel_scope:%q`, panelScope))
	parts = append(parts, fmt.Sprintf(`diagnostic_id:%q`, diagnosticID))

	// Format time window in RFC3339
	fromStr := f.From.UTC().Format(time.RFC3339Nano)
	toStr := f.To.UTC().Format(time.RFC3339Nano)
	parts = append(parts, fmt.Sprintf(`_time:[%s, %s]`, fromStr, toStr))

	if f.NodeID != nil {
		parts = append(parts, fmt.Sprintf(`node_id:%d`, *f.NodeID))
	}
	if f.TraceID != nil {
		// Connection trace_id equals connection_id
		parts = append(parts, fmt.Sprintf(`trace_id:%q`, *f.TraceID))
	}

	query := strings.Join(parts, " AND ")
	// Order by event_time descending, limit to MaxQueryReadLines + 1 to detect read truncation
	query += fmt.Sprintf(" | sort by (_time, seq) desc | limit %d", MaxQueryReadLines+1)
	return query, nil
}

// ConnectionSummary represents an aggregated target connection view.
type ConnectionSummary struct {
	NodeID                   int64     `json:"node_id"`
	RuntimeID                string    `json:"runtime_id"`
	ConnectionID             string    `json:"connection_id"`
	TargetID                 int64     `json:"target_id"`
	DiagnosticID             string    `json:"diagnostic_id"`
	InboundProtocol          string    `json:"inbound_protocol"`
	Network                  string    `json:"network"`
	DestinationHost          string    `json:"destination_host"`
	DestinationPort          int       `json:"destination_port"`
	OutboundTag              string    `json:"outbound_tag"`
	OpenedAt                 time.Time `json:"opened_at"`
	ClosedAt                 *time.Time `json:"closed_at,omitempty"`
	DurationMS               int64     `json:"duration_ms"`
	HasError                 bool      `json:"has_error"`
	ErrorStages              []string  `json:"error_stages"`
	ReasonCodes              []string  `json:"reason_codes"`
	LastErrorStage           string    `json:"last_error_stage,omitempty"`
	LastReasonCode           string    `json:"last_reason_code,omitempty"`
	TransportState           string    `json:"transport_state"`
	UploadBytes              int64     `json:"upload_bytes"`
	DownloadBytes            int64     `json:"download_bytes"`
	EventsCount              int       `json:"events_count"`
	DNSResolverOperationIDs  []string  `json:"dns_resolver_operation_ids"`
}

// ConnectionAggregateResult is the output of raw log event stream processing.
type ConnectionAggregateResult struct {
	Summaries     []ConnectionSummary
	ScanTruncated bool
}

type rawEventItem struct {
	EventTime            string          `json:"event_time"`
	EventID              string          `json:"event_id"`
	EventFamily          string          `json:"event_family"`
	PanelScope           string          `json:"panel_scope"`
	NodeID               int64           `json:"node_id"`
	RuntimeID            string          `json:"runtime_id"`
	Event                string          `json:"event"`
	DiagnosticID         string          `json:"diagnostic_id"`
	ConnectionID         string          `json:"connection_id"`
	TargetID             int64           `json:"target_id"`
	Seq                  int64           `json:"seq"`
	InboundProtocol      string          `json:"inbound_protocol"`
	Network              string          `json:"network"`
	DestinationHost      string          `json:"destination_host"`
	DestinationPort      int             `json:"destination_port"`
	OutboundTag          string          `json:"outbound_tag"`
	HasError             bool            `json:"has_error"`
	ErrorStages          []string        `json:"error_stages"`
	ReasonCodes          []string        `json:"reason_codes"`
	LastErrorStage       string          `json:"last_error_stage"`
	LastReasonCode       string          `json:"last_reason_code"`
	TransportState       string          `json:"transport_state"`
	UploadBytes          int64           `json:"upload_bytes"`
	DownloadBytes        int64           `json:"download_bytes"`
	ResolverOperationID  string          `json:"resolver_operation_id"`
	SpanLinks            []string        `json:"span_links"`
	TargetSummaryReason  string          `json:"target_summary_reason"`
}

// AggregateConnectionEvents reads raw JSON stream lines from VictoriaLogs and aggregates target connections.
func AggregateConnectionEvents(r io.Reader, filters QueryFilters) (ConnectionAggregateResult, error) {
	scanner := bufio.NewScanner(r)
	// Allow up to 128 KiB per JSON line
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	type connKey struct {
		nodeID       int64
		runtimeID    string
		connectionID string
		targetID     int64
	}

	type connAccumulator struct {
		summary     ConnectionSummary
		seenSeq     map[int64]struct{}
		maxSeq      int64
		dnsOpIDs    map[string]struct{}
		errorStages map[string]struct{}
		reasonCodes map[string]struct{}
		firstTime   time.Time
		lastTime    time.Time
	}

	accumulators := make(map[connKey]*connAccumulator)
	order := make([]connKey, 0)
	seenEventIDs := make(map[string]struct{})

	lineCount := 0
	scanTruncated := false

	for scanner.Scan() {
		lineCount++
		if lineCount > MaxQueryReadLines {
			scanTruncated = true
			break
		}

		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var ev rawEventItem
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}

		if ev.EventFamily != "connection" || ev.ConnectionID == "" {
			continue
		}

		if _, exists := seenEventIDs[ev.EventID]; exists {
			continue
		}
		seenEventIDs[ev.EventID] = struct{}{}

		parsedTime, _ := time.Parse(time.RFC3339Nano, ev.EventTime)

		key := connKey{
			nodeID:       ev.NodeID,
			runtimeID:    ev.RuntimeID,
			connectionID: ev.ConnectionID,
			targetID:     ev.TargetID,
		}

		acc, exists := accumulators[key]
		if !exists {
			acc = &connAccumulator{
				summary: ConnectionSummary{
					NodeID:          ev.NodeID,
					RuntimeID:       ev.RuntimeID,
					ConnectionID:    ev.ConnectionID,
					TargetID:        ev.TargetID,
					DiagnosticID:    ev.DiagnosticID,
					InboundProtocol: ev.InboundProtocol,
					Network:         ev.Network,
					DestinationHost: ev.DestinationHost,
					DestinationPort: ev.DestinationPort,
					OutboundTag:     ev.OutboundTag,
					TransportState:  ev.TransportState,
				},
				seenSeq:     make(map[int64]struct{}),
				dnsOpIDs:    make(map[string]struct{}),
				errorStages: make(map[string]struct{}),
				reasonCodes: make(map[string]struct{}),
				firstTime:   parsedTime,
				lastTime:    parsedTime,
			}
			accumulators[key] = acc
			order = append(order, key)
		}

		acc.summary.EventsCount++
		if !parsedTime.IsZero() {
			if acc.firstTime.IsZero() || parsedTime.Before(acc.firstTime) {
				acc.firstTime = parsedTime
			}
			if parsedTime.After(acc.lastTime) {
				acc.lastTime = parsedTime
			}
		}

		// Track DNS operations
		if ev.ResolverOperationID != "" {
			acc.dnsOpIDs[ev.ResolverOperationID] = struct{}{}
		}
		for _, link := range ev.SpanLinks {
			if strings.HasPrefix(link, "dns.exchange:") {
				opID := strings.TrimPrefix(link, "dns.exchange:")
				if opID != "" {
					acc.dnsOpIDs[opID] = struct{}{}
				}
			}
		}

		// Cumulative errors
		if ev.HasError {
			acc.summary.HasError = true
		}
		for _, st := range ev.ErrorStages {
			acc.errorStages[st] = struct{}{}
		}
		for _, rc := range ev.ReasonCodes {
			acc.reasonCodes[rc] = struct{}{}
		}
		if ev.LastErrorStage != "" {
			acc.summary.LastErrorStage = ev.LastErrorStage
		}
		if ev.LastReasonCode != "" {
			acc.summary.LastReasonCode = ev.LastReasonCode
		}

		// Keep highest sequence event metadata
		if ev.Seq >= acc.maxSeq {
			acc.maxSeq = ev.Seq
			if ev.InboundProtocol != "" {
				acc.summary.InboundProtocol = ev.InboundProtocol
			}
			if ev.Network != "" {
				acc.summary.Network = ev.Network
			}
			if ev.DestinationHost != "" {
				acc.summary.DestinationHost = ev.DestinationHost
			}
			if ev.DestinationPort > 0 {
				acc.summary.DestinationPort = ev.DestinationPort
			}
			if ev.OutboundTag != "" {
				acc.summary.OutboundTag = ev.OutboundTag
			}
			if ev.TransportState != "" {
				acc.summary.TransportState = ev.TransportState
			}
		}

		if ev.UploadBytes > acc.summary.UploadBytes {
			acc.summary.UploadBytes = ev.UploadBytes
		}
		if ev.DownloadBytes > acc.summary.DownloadBytes {
			acc.summary.DownloadBytes = ev.DownloadBytes
		}
		if ev.Event == "diag.connection.close" {
			t := parsedTime
			acc.summary.ClosedAt = &t
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return ConnectionAggregateResult{}, fmt.Errorf("scan log events: %w", err)
	}

	// Finalize accumulators and apply post-aggregation filters
	results := make([]ConnectionSummary, 0, len(order))
	for _, key := range order {
		acc := accumulators[key]
		acc.summary.OpenedAt = acc.firstTime
		if !acc.lastTime.IsZero() && !acc.firstTime.IsZero() && acc.lastTime.After(acc.firstTime) {
			acc.summary.DurationMS = acc.lastTime.Sub(acc.firstTime).Milliseconds()
		}

		// Flatten error stages
		if len(acc.errorStages) > 0 {
			stages := make([]string, 0, len(acc.errorStages))
			for s := range acc.errorStages {
				stages = append(stages, s)
			}
			sort.Strings(stages)
			acc.summary.ErrorStages = stages
		}

		// Flatten reason codes
		if len(acc.reasonCodes) > 0 {
			reasons := make([]string, 0, len(acc.reasonCodes))
			for r := range acc.reasonCodes {
				reasons = append(reasons, r)
			}
			sort.Strings(reasons)
			acc.summary.ReasonCodes = reasons
		}

		// Flatten DNS operation IDs
		if len(acc.dnsOpIDs) > 0 {
			ids := make([]string, 0, len(acc.dnsOpIDs))
			for id := range acc.dnsOpIDs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			acc.summary.DNSResolverOperationIDs = ids
		}

		// Apply Domain filter
		if filters.Domain != nil {
			host := strings.ToLower(acc.summary.DestinationHost)
			filterDomain := *filters.Domain
			match := false
			switch filters.DomainMatch {
			case "exact":
				match = host == filterDomain
			case "suffix":
				match = strings.HasSuffix(host, "."+filterDomain) || host == filterDomain
			default: // contains
				match = strings.Contains(host, filterDomain)
			}
			if !match {
				continue
			}
		}

		// Apply HasError filter
		if filters.HasError != nil {
			if acc.summary.HasError != *filters.HasError {
				continue
			}
		}

		// Apply ReasonCode filter
		if filters.ReasonCode != nil {
			matchedReason := false
			for _, rc := range acc.summary.ReasonCodes {
				if rc == *filters.ReasonCode {
					matchedReason = true
					break
				}
			}
			if !matchedReason && acc.summary.LastReasonCode != *filters.ReasonCode {
				continue
			}
		}

		results = append(results, acc.summary)
	}

	// Sort summaries by OpenedAt descending, connection_id descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].OpenedAt.Equal(results[j].OpenedAt) {
			return results[i].ConnectionID > results[j].ConnectionID
		}
		return results[i].OpenedAt.After(results[j].OpenedAt)
	})

	return ConnectionAggregateResult{
		Summaries:     results,
		ScanTruncated: scanTruncated,
	}, nil
}

// CursorPayload represents the state encoded in a signed pagination cursor.
type CursorPayload struct {
	AdminID           int64     `json:"admin_id"`
	OrderID           *int64    `json:"order_id,omitempty"`
	DiagnosticID      string    `json:"diagnostic_id"`
	PanelScope        string    `json:"panel_scope"`
	SnapshotID        string    `json:"snapshot_id"`
	StorageGeneration int64     `json:"storage_generation"`
	Offset            int       `json:"offset"`
	CreatedAt         time.Time `json:"created_at"`
}

// GenerateCursor produces a signed base64url cursor string.
func GenerateCursor(secret []byte, p CursorPayload) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("cursor signing secret cannot be empty")
	}
	payloadBytes, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payloadBytes)
	sig := mac.Sum(nil)

	wrapped := struct {
		Payload   []byte `json:"p"`
		Signature []byte `json:"s"`
	}{
		Payload:   payloadBytes,
		Signature: sig,
	}

	raw, err := json.Marshal(wrapped)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// ParseAndVerifyCursor validates the signature, generation, and TTL of a cursor string.
func ParseAndVerifyCursor(secret []byte, cursorStr string, expectedAdminID int64, expectedScope, expectedDiagID string, expectedGeneration int64, now time.Time) (CursorPayload, error) {
	if len(secret) == 0 {
		return CursorPayload{}, ErrCursorInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursorStr)
	if err != nil {
		return CursorPayload{}, ErrCursorInvalid
	}

	var wrapped struct {
		Payload   []byte `json:"p"`
		Signature []byte `json:"s"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return CursorPayload{}, ErrCursorInvalid
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(wrapped.Payload)
	expectedSig := mac.Sum(nil)
	if !hmac.Equal(wrapped.Signature, expectedSig) {
		return CursorPayload{}, ErrCursorInvalid
	}

	var payload CursorPayload
	if err := json.Unmarshal(wrapped.Payload, &payload); err != nil {
		return CursorPayload{}, ErrCursorInvalid
	}

	if now.Sub(payload.CreatedAt) > CursorTTL {
		return CursorPayload{}, ErrCursorExpired
	}
	if payload.AdminID != expectedAdminID || payload.PanelScope != expectedScope || payload.DiagnosticID != expectedDiagID || payload.StorageGeneration != expectedGeneration {
		return CursorPayload{}, ErrCursorInvalid
	}

	return payload, nil
}

// SnapshotCache stores recent in-memory query snapshots with a strict 5-minute TTL and per-admin bounds.
type SnapshotCache struct {
	mu        sync.Mutex
	snapshots map[string]*cachedSnapshot
	adminKeys map[int64][]string
}

type cachedSnapshot struct {
	id        string
	adminID   int64
	items     []ConnectionSummary
	truncated bool
	createdAt time.Time
}

func NewSnapshotCache() *SnapshotCache {
	return &SnapshotCache{
		snapshots: make(map[string]*cachedSnapshot),
		adminKeys: make(map[int64][]string),
	}
}

func (c *SnapshotCache) Put(adminID int64, id string, items []ConnectionSummary, truncated bool, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cleanupExpired(now)

	// Enforce max snapshots per admin
	keys := c.adminKeys[adminID]
	if len(keys) >= MaxSnapshotsPerAdmin {
		oldest := keys[0]
		delete(c.snapshots, oldest)
		keys = keys[1:]
	}

	c.snapshots[id] = &cachedSnapshot{
		id:        id,
		adminID:   adminID,
		items:     items,
		truncated: truncated,
		createdAt: now,
	}
	c.adminKeys[adminID] = append(keys, id)
}

func (c *SnapshotCache) Get(adminID int64, id string, now time.Time) ([]ConnectionSummary, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	snap, exists := c.snapshots[id]
	if !exists {
		return nil, false, false
	}
	if snap.adminID != adminID || now.Sub(snap.createdAt) > CursorTTL {
		delete(c.snapshots, id)
		return nil, false, false
	}
	return snap.items, snap.truncated, true
}

func (c *SnapshotCache) cleanupExpired(now time.Time) {
	for id, s := range c.snapshots {
		if now.Sub(s.createdAt) > CursorTTL {
			delete(c.snapshots, id)
		}
	}
	for adminID, keys := range c.adminKeys {
		valid := keys[:0]
		for _, k := range keys {
			if _, exists := c.snapshots[k]; exists {
				valid = append(valid, k)
			}
		}
		if len(valid) == 0 {
			delete(c.adminKeys, adminID)
		} else {
			c.adminKeys[adminID] = valid
		}
	}
}

// StorageClient provides transport to VictoriaLogs /select/logsql/query.
type StorageClient struct {
	httpClient *http.Client
}

func NewStorageClient() *StorageClient {
	return &StorageClient{
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 3 * time.Second,
			},
		},
	}
}

// Query executes a LogsQL query against VictoriaLogs and returns the raw response reader.
func (c *StorageClient) Query(ctx context.Context, endpoint, username, password, logsql string) (io.ReadCloser, error) {
	if endpoint == "" {
		return nil, ErrStorageNotConfigured
	}

	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("%w: invalid storage endpoint URL", ErrStorageNotConfigured)
	}

	// Target /select/logsql/query
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/select/logsql/query"
	queryURL := parsed.String()

	form := url.Values{}
	form.Set("query", logsql)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, queryURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || osIsTimeout(err) {
			return nil, ErrStorageTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrStorageUnavailable, err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if resp.StatusCode == http.StatusGatewayTimeout || resp.StatusCode == http.StatusRequestTimeout {
			return nil, ErrStorageTimeout
		}
		return nil, fmt.Errorf("%w: upstream status %d: %s", ErrStorageUnavailable, resp.StatusCode, string(body))
	}

	return resp.Body, nil
}

func osIsTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) {
		return timeoutErr.Timeout()
	}
	return false
}

// StorageHealthResponse records the health status reported by VictoriaLogs/guard.
type StorageHealthResponse struct {
	SchemaVersion      int      `json:"schema_version"`
	PanelScope         string   `json:"panel_scope"`
	StorageID          string   `json:"storage_id"`
	State              string   `json:"state"`
	ReasonCodes        []string `json:"reason_codes"`
	CheckedAtUTC       string   `json:"checked_at_utc"`
	CheckAgeSeconds    int64    `json:"check_age_seconds"`
	Latched            bool     `json:"latched,omitempty"`
	DataUsedBytes      int64    `json:"data_used_bytes,omitempty"`
	DataAvailableBytes int64    `json:"data_available_bytes,omitempty"`
	RootAvailableBytes int64    `json:"root_available_bytes,omitempty"`
}

// GetHealth fetches the detailed storage health status from the guard endpoint.
func (c *StorageClient) GetHealth(ctx context.Context, endpoint, username, password string) (StorageHealthResponse, error) {
	if endpoint == "" {
		return StorageHealthResponse{}, ErrStorageNotConfigured
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return StorageHealthResponse{}, fmt.Errorf("%w: invalid storage endpoint URL", ErrStorageNotConfigured)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/diagnostics/health"
	healthURL := parsed.String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
	if err != nil {
		return StorageHealthResponse{}, err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StorageHealthResponse{}, fmt.Errorf("%w: %v", ErrDiagnosticsStorageNotReady, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		return StorageHealthResponse{}, fmt.Errorf("%w: upstream status %d", ErrDiagnosticsStorageNotReady, resp.StatusCode)
	}

	var health StorageHealthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&health); err != nil {
		return StorageHealthResponse{}, fmt.Errorf("%w: invalid health json", ErrDiagnosticsStorageNotReady)
	}

	return health, nil
}

// CheckHealth queries the storage health endpoint and validates readiness.
func (c *StorageClient) CheckHealth(ctx context.Context, endpoint, username, password, expectedScope string) error {
	health, err := c.GetHealth(ctx, endpoint, username, password)
	if err != nil {
		return err
	}

	if health.State != "ready" || (expectedScope != "" && health.PanelScope != expectedScope) || health.CheckAgeSeconds > 60 || health.Latched {
		return ErrDiagnosticsStorageNotReady
	}
	return nil
}
