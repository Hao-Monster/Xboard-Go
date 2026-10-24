package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion = 1
	Revision      = "2026-10-18-review1"

	MaxEventBytes                 = 4096
	MaxStackBytes                 = 2048
	MaxStackFrames                = 12
	MaxStackFrameBytes            = 192
	MaxSafeMessageChars           = 256
	MaxRequestedHostChars         = 253
	MaxOutboundTagChars           = 128
	MaxDNSAnswers                 = 8
	MaxSpanLinks                  = 4
	MaxUDPTargetsPerSession       = 64
	MaxActiveLeasesPerPanel       = 10
	LeaseTTLSeconds               = 900
	LeaseMaxRTTMs                 = 5000
	NodeQueueMaxEvents            = 2048
	NodeQueueMaxBytes             = 8388608
	NodeQueueReservedBaselineRatio = 0.25

	DiagnosticIDHexLen       = 24
	RuntimeIDHexLen          = 32
	ConnectionIDHexLen       = 32
	ResolverOperationIDHexLen = 32
	SessionIDHexLen          = 32
	TraceIDHexLen            = 32
	SpanIDHexLen             = 16
	EventIDHexLen            = 64
)

// Standard Error Codes
var (
	ErrDiagnosticsNotInitialized = errors.New("diagnostics_not_initialized")
	ErrInvalidPanelScope         = errors.New("invalid_panel_scope")
	ErrSubscriptionUserMissing   = errors.New("subscription_user_missing")
	ErrInvalidLookupInput        = errors.New("invalid_lookup_input")
	ErrOrderNotFound             = errors.New("order_not_found")
	ErrDiagnosticsStorageNotReady = errors.New("diagnostics_storage_not_ready")
	ErrIdempotencyConflict       = errors.New("idempotency_conflict")
	ErrLeaseLimitReached         = errors.New("lease_limit_reached")
	ErrSnapshotConflict          = errors.New("snapshot_conflict")
	ErrCursorExpired             = errors.New("cursor_expired")
	ErrCursorInvalid             = errors.New("cursor_invalid")
	ErrBackendUnavailable        = errors.New("backend_unavailable")
	ErrBackendTimeout            = errors.New("backend_timeout")
)

var (
	uuidRegex       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	hex24Regex      = regexp.MustCompile(`^[0-9a-f]{24}$`)
	hex32Regex      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex16Regex      = regexp.MustCompile(`^[0-9a-f]{16}$`)
	hex64Regex      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	urlRegex        = regexp.MustCompile(`https?://[^\s]+`)
	emailRegex      = regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	uuidStrRegex    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	ipv4Regex       = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	ipv6Regex       = regexp.MustCompile(`(?:\[[0-9a-fA-F:]+:[0-9a-fA-F:]*\]|\b[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){1,7}\b|::1\b)`)
	secTokenRegex   = regexp.MustCompile(`(?i)(token=|sec_)[a-zA-Z0-9_]+`)
	funcArgsRegex   = regexp.MustCompile(`\([^)]*0x[0-9a-fA-F]+[^)]*\)$`)
)

// Allowed fields per family
var (
	eventToFamily = map[string]string{
		"diag.connection.open":       "connection",
		"diag.admission.rejected":    "connection",
		"diag.route.result":          "connection",
		"diag.route.step":            "connection",
		"diag.dns.result":            "connection",
		"diag.dial.result":           "connection",
		"diag.dial.attempt":          "connection",
		"diag.relay.result":          "connection",
		"diag.target.summary":        "connection",
		"diag.connection.close":      "connection",
		"diag.dns.exchange.result":   "resolver_operation",
		"diag.dns.attempt":           "resolver_operation",
		"diag.observer.health":       "node",
	}

	connectionAllowedFields = map[string]bool{
		"schema_version": true, "event_time": true, "event_id": true, "event_family": true,
		"panel_scope": true, "node_id": true, "runtime_id": true, "diagnostic_id": true,
		"connection_id": true, "target_id": true, "seq": true, "event": true,
		"stage": true, "observation_mode": true, "session_id": true, "build_revision": true,
		"payload_truncated": true, "trace_id": true, "span_id": true, "parent_span_id": true,
		"span_name": true, "span_start_time": true, "span_duration_ms": true, "span_status": true,
		"span_links": true, "network": true, "protocol": true, "requested_host": true,
		"requested_port": true, "observed_domain": true, "domain_source": true, "outbound_tag": true,
		"rule_id": true, "selected_target_address": true, "socket_peer": true, "has_error": true,
		"last_error_stage": true, "last_reason_code": true, "error_stages": true, "reason_codes": true,
		"transport_state": true, "summary_reason": true, "targets_evicted_total": true,
		"resolver_operation_id": true, "node_resolution": true, "dns_rcode": true,
		"dns_answers": true, "dns_answers_total": true, "dns_answers_truncated": true,
		"dns_cache_hit": true, "dns_query_type": true, "device_limit": true, "active_ip_count": true,
		"attempt_id": true, "duration_ms": true, "bytes_up": true, "bytes_down": true,
		"close_reason": true, "reason_code": true, "error_type": true, "timeout": true,
		"errno": true, "safe_message": true, "exception": true,
	}

	connectionRequiredFields = []string{
		"schema_version", "event_time", "event_id", "event_family",
		"panel_scope", "node_id", "runtime_id", "diagnostic_id",
		"connection_id", "seq", "event", "stage", "network",
		"observation_mode", "trace_id", "span_id", "span_name",
		"span_start_time", "span_duration_ms", "span_status",
		"has_error", "error_stages", "reason_codes", "transport_state",
	}

	connectionForbiddenFields = map[string]bool{
		"user_id": true, "uuid": true, "token": true, "subscribe_url": true,
		"email": true, "trade_no": true, "order_id": true, "client_ip": true,
		"source_ip": true, "dest_ip": true, "resolved_ip": true, "ecs_subnet": true,
		"raw_url": true, "sni": true, "ech": true, "alpn": true,
		"stack_raw": true, "headers": true, "waiting_customers": true, "customer_ids": true,
	}

	resolverOperationAllowedFields = map[string]bool{
		"schema_version": true, "event_time": true, "event_id": true, "event_family": true,
		"panel_scope": true, "node_id": true, "runtime_id": true, "resolver_operation_id": true,
		"operation_seq": true, "event": true, "stage": true, "observation_mode": true,
		"build_revision": true, "payload_truncated": true, "trace_id": true, "span_id": true,
		"parent_span_id": true, "span_name": true, "span_start_time": true, "span_duration_ms": true,
		"span_status": true, "observed_domain": true, "dns_server": true, "dns_transport": true,
		"dns_query_type": true, "attempt_id": true, "dns_rcode": true, "dns_answers": true,
		"dns_answers_total": true, "dns_answers_truncated": true, "dns_cache_hit": true,
		"detail_started_mid_operation": true, "reason_code": true, "error_type": true,
		"timeout": true, "errno": true, "safe_message": true, "exception": true,
	}

	resolverOperationRequiredFields = []string{
		"schema_version", "event_time", "event_id", "event_family",
		"panel_scope", "node_id", "runtime_id", "resolver_operation_id",
		"operation_seq", "event", "stage", "observation_mode",
		"trace_id", "span_id", "span_name", "span_start_time",
		"span_duration_ms", "span_status", "observed_domain",
	}

	resolverOperationForbiddenFields = map[string]bool{
		"diagnostic_id": true, "connection_id": true, "session_id": true,
		"user_id": true, "uuid": true, "token": true, "subscribe_url": true,
		"email": true, "trade_no": true, "order_id": true, "client_ip": true,
		"source_ip": true, "dest_ip": true, "resolved_ip": true, "ecs_subnet": true,
		"raw_url": true, "sni": true, "ech": true, "alpn": true,
		"stack_raw": true, "headers": true, "waiting_customers": true, "customer_ids": true,
	}

	nodeAllowedFields = map[string]bool{
		"schema_version": true, "event_time": true, "event_id": true, "event_family": true,
		"panel_scope": true, "node_id": true, "runtime_id": true, "runtime_seq": true,
		"event": true, "observation_mode": true, "build_revision": true, "payload_truncated": true,
		"observer_state": true, "control_epoch": true, "control_version": true,
		"active_leases": true, "queue_depth": true, "queue_bytes": true, "dropped_total": true,
		"dropped_detail": true, "dropped_baseline": true, "evicted_targets_total": true,
		"stage": true, "reason_code": true, "error_type": true, "safe_message": true,
	}

	nodeRequiredFields = []string{
		"schema_version", "event_time", "event_id", "event_family",
		"panel_scope", "node_id", "runtime_id", "runtime_seq", "event",
	}

	nodeForbiddenFields = map[string]bool{
		"diagnostic_id": true, "connection_id": true, "session_id": true, "trace_id": true,
		"span_id": true, "user_id": true, "uuid": true, "token": true, "subscribe_url": true,
		"email": true, "trade_no": true, "order_id": true, "client_ip": true,
		"source_ip": true, "dest_ip": true, "resolved_ip": true, "ecs_subnet": true,
		"raw_url": true, "sni": true, "ech": true, "alpn": true,
		"stack_raw": true, "headers": true, "waiting_customers": true, "customer_ids": true,
	}
)

// RFC 3492 Punycode parameters
const (
	punycodeBase        = 36
	punycodeTmin        = 1
	punycodeTmax        = 26
	punycodeSkew        = 38
	punycodeDamp        = 700
	punycodeInitialBias = 72
	punycodeInitialN    = 128
)

func adapt(delta int32, numpoints int32, firsttime bool) int32 {
	if firsttime {
		delta /= punycodeDamp
	} else {
		delta /= 2
	}
	delta += delta / numpoints
	var k int32 = 0
	for delta > ((punycodeBase-punycodeTmin)*punycodeTmax)/2 {
		delta /= punycodeBase - punycodeTmin
		k += punycodeBase
	}
	return k + (((punycodeBase - punycodeTmin + 1) * delta) / (delta + punycodeSkew))
}

func encodeDigit(d int32) byte {
	if d < 26 {
		return byte(d + 'a')
	}
	return byte(d - 26 + '0')
}

func punycodeEncode(s string) string {
	runes := []rune(s)
	var output []byte
	for _, r := range runes {
		if r < 128 {
			output = append(output, byte(r))
		}
	}
	b := int32(len(output))
	h := b
	if b > 0 {
		output = append(output, '-')
	}

	n := int32(punycodeInitialN)
	delta := int32(0)
	bias := int32(punycodeInitialBias)

	for h < int32(len(runes)) {
		var m int32 = 0x7fffffff
		for _, r := range runes {
			if r >= n && r < m {
				m = r
			}
		}

		delta += (m - n) * (h + 1)
		n = m

		for _, r := range runes {
			if r < n {
				delta++
			}
			if r == n {
				q := delta
				for k := int32(punycodeBase); ; k += punycodeBase {
					t := k - bias
					if t < punycodeTmin {
						t = punycodeTmin
					} else if t > punycodeTmax {
						t = punycodeTmax
					}
					if q < t {
						break
					}
					output = append(output, encodeDigit(t+((q-t)%(punycodeBase-t))))
					q = (q - t) / (punycodeBase - t)
				}
				output = append(output, encodeDigit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(output)
}

// StackFrame represents a frame in an exception stack trace.
type StackFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// SanitizedStack is the output of stack sanitization.
type SanitizedStack struct {
	FormattedFrames   []string `json:"formatted_frames"`
	FramesTotal       int      `json:"frames_total"`
	FramesKept        int      `json:"frames_kept"`
	Truncated         bool     `json:"truncated"`
	SerializedPayload string   `json:"serialized_payload"`
}

// LeaseSession is an individual lease session inside a snapshot.
type LeaseSession struct {
	SessionID    string `json:"session_id"`
	DiagnosticID string `json:"diagnostic_id"`
	NodeID       int64  `json:"node_id"`
	Revision     int64  `json:"revision"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    string `json:"created_at"`
	ExpiresAt    string `json:"expires_at"`
}

// LeaseSnapshot represents a full snapshot received from or issued by the panel.
type LeaseSnapshot struct {
	SchemaVersion  int            `json:"schema_version"`
	RequestID      string         `json:"request_id"`
	PanelScope     string         `json:"panel_scope"`
	NodeID         int64          `json:"node_id"`
	RuntimeID      string         `json:"runtime_id"`
	ControlEpoch   string         `json:"control_epoch"`
	ControlVersion int64          `json:"control_version"`
	ServerTime     string         `json:"server_time"`
	Sessions       []LeaseSession `json:"sessions"`
}

// LeaseEvaluationResult holds the evaluated output of a snapshot.
type LeaseEvaluationResult struct {
	Accepted                  bool             `json:"accepted"`
	Reason                    string           `json:"reason,omitempty"`
	ActiveDiagnosticIDs       []string         `json:"active_diagnostic_ids"`
	DeadlinesMonotonicMS      map[string]int64 `json:"deadlines_monotonic_ms"`
	ExpectedDeadlineMonotonic int64            `json:"expected_deadline_monotonic_ms,omitempty"`
}

// ComputeDiagnosticID calculates the stable 24-hex customer diagnostic ID.
func ComputeDiagnosticID(panelScope string, subscriberUserID int64) (string, error) {
	trimmed := strings.TrimSpace(panelScope)
	if trimmed == "" {
		return "", ErrDiagnosticsNotInitialized
	}
	if !uuidRegex.MatchString(panelScope) {
		return "", ErrInvalidPanelScope
	}
	if subscriberUserID <= 0 {
		return "", ErrSubscriptionUserMissing
	}

	raw := "xboard-diag:v1" + "\x00" + panelScope + "\x00" + strconv.FormatInt(subscriberUserID, 10)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])[:DiagnosticIDHexLen], nil
}

// ComputeEventID calculates the canonical 64-hex SHA256 event ID.
func ComputeEventID(panelScope string, nodeID int64, runtimeID, eventFamily, ownerID string, sequence int64) string {
	raw := fmt.Sprintf("%s/%d/%s/%s/%s/%d", panelScope, nodeID, runtimeID, eventFamily, ownerID, sequence)
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// NormalizeDomain performs IDNA ASCII normalization, lowercase, trailing dot removal, and strict validation.
func NormalizeDomain(domain string) (string, bool) {
	if domain == "" || len(domain) > MaxRequestedHostChars {
		return "", false
	}
	// Check for whitespace, scheme, path, query, port
	if strings.ContainsAny(domain, " \t\r\n/?:") {
		return "", false
	}
	// Remove trailing dot
	clean := strings.TrimSuffix(domain, ".")
	if clean == "" {
		return "", false
	}

	// Rejects IPv4 / IPv6 addresses
	if net.ParseIP(clean) != nil {
		return "", false
	}
	// Rejects strings that look like four dot-separated octets (e.g. 999.999.999.999)
	parts := strings.Split(clean, ".")
	if len(parts) == 4 {
		allNumeric := true
		for _, p := range parts {
			if _, err := strconv.Atoi(p); err != nil {
				allNumeric = false
				break
			}
		}
		if allNumeric {
			return "", false
		}
	}

	// Must have at least one dot (localhost is rejected)
	if len(parts) < 2 {
		return "", false
	}

	// Check labels and convert IDNA
	var asciiParts []string
	for _, p := range parts {
		if p == "" || strings.HasPrefix(p, "-") || strings.HasSuffix(p, "-") {
			return "", false
		}

		isASCII := true
		for _, r := range p {
			if r >= 128 {
				isASCII = false
				break
			}
		}

		var label string
		if isASCII {
			label = strings.ToLower(p)
		} else {
			encoded := punycodeEncode(strings.ToLower(p))
			if encoded == "" {
				return "", false
			}
			label = "xn--" + encoded
		}

		if label == "" || label == "xn--" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", false
		}
		// Validate label characters: [a-z0-9-]
		for _, c := range label {
			if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
				return "", false
			}
		}
		asciiParts = append(asciiParts, label)
	}

	result := strings.Join(asciiParts, ".")
	if len(result) > MaxRequestedHostChars {
		return "", false
	}
	return result, true
}

// SanitizeStack strips absolute paths, call arguments, caps at 12 frames, and formats frames.
func SanitizeStack(rawFrames []StackFrame) SanitizedStack {
	total := len(rawFrames)
	keptCount := total
	truncated := false
	if keptCount > MaxStackFrames {
		keptCount = MaxStackFrames
		truncated = true
	}

	formatted := make([]string, 0, keptCount)
	for i := 0; i < keptCount; i++ {
		f := rawFrames[i]

		// 1. Sanitize function: strip call arguments like (0x...)
		fn := f.Function
		if loc := funcArgsRegex.FindStringIndex(fn); loc != nil {
			fn = fn[:loc[0]]
		}

		// 2. Sanitize file path: strip absolute prefixes
		filePath := strings.ReplaceAll(f.File, "\\", "/")
		relFile := cleanFilePath(filePath)

		formatted = append(formatted, fmt.Sprintf("%s (%s:%d)", fn, relFile, f.Line))
	}

	return SanitizedStack{
		FormattedFrames:   formatted,
		FramesTotal:       total,
		FramesKept:        keptCount,
		Truncated:         truncated,
		SerializedPayload: strings.Join(formatted, "\n"),
	}
}

func cleanFilePath(p string) string {
	// gomodcache pattern e.g. /home/bingo/.gomodcache/github.com/miekg/dns@v1.1.58/client.go -> dns@v1.1.58/client.go
	if idx := strings.Index(p, ".gomodcache/"); idx != -1 {
		sub := p[idx+len(".gomodcache/"):]
		parts := strings.Split(sub, "/")
		if len(parts) >= 3 {
			return strings.Join(parts[2:], "/")
		}
	}

	// Go standard library e.g. /usr/local/go/src/net/dial.go -> src/net/dial.go
	if idx := strings.Index(p, "/go/src/"); idx != -1 {
		return p[idx+len("/go/"):]
	}

	// Check /deps/ before /internal/ so /home/internal/.../deps/... matches deps/
	for _, marker := range []string{"/deps/", "/internal/", "/pkg/", "/cmd/"} {
		if idx := strings.Index(p, marker); idx != -1 {
			return p[idx+1:]
		}
	}

	// General fallback: keep last two path elements if present
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}

// SanitizeMessage redacts credentials, emails, UUIDs, IPs, URLs, ANSI codes and truncates to maxChars.
func SanitizeMessage(rawMessage string, maxChars int) string {
	msg := ansiEscapeRegex.ReplaceAllString(rawMessage, "")
	msg = urlRegex.ReplaceAllString(msg, "[REDACTED_URL]")
	msg = secTokenRegex.ReplaceAllString(msg, "[REDACTED]")
	msg = emailRegex.ReplaceAllString(msg, "[REDACTED_EMAIL]")
	msg = uuidStrRegex.ReplaceAllString(msg, "[REDACTED_UUID]")
	msg = ipv6Regex.ReplaceAllString(msg, "[REDACTED_IP]")
	msg = ipv4Regex.ReplaceAllString(msg, "[REDACTED_IP]")

	runes := []rune(msg)
	if len(runes) > maxChars {
		runes = runes[:maxChars]
	}
	return string(runes)
}

// ValidateEventJSON checks a single event JSON for schema conformance, field whitelisting, and constraints.
func ValidateEventJSON(rawJSON []byte) error {
	if len(rawJSON) > MaxEventBytes {
		return fmt.Errorf("event_bytes_exceeded: len=%d max=%d", len(rawJSON), MaxEventBytes)
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err != nil {
		return fmt.Errorf("json_syntax_error: %w", err)
	}

	// 1. schema_version check
	schemaVerRaw, ok := rawMap["schema_version"]
	if !ok {
		return errors.New("missing_schema_version")
	}
	var schemaVer int
	if err := json.Unmarshal(schemaVerRaw, &schemaVer); err != nil || schemaVer != SchemaVersion {
		return errors.New("invalid_schema_version")
	}

	// 2. event and event_family
	eventRaw, ok := rawMap["event"]
	if !ok {
		return errors.New("missing_event")
	}
	var eventName string
	if err := json.Unmarshal(eventRaw, &eventName); err != nil {
		return errors.New("invalid_event_format")
	}

	familyRaw, ok := rawMap["event_family"]
	if !ok {
		return errors.New("missing_event_family")
	}
	var eventFamily string
	if err := json.Unmarshal(familyRaw, &eventFamily); err != nil {
		return errors.New("invalid_event_family_format")
	}

	expectedFamily, ok := eventToFamily[eventName]
	if !ok {
		return fmt.Errorf("unknown_event: %s", eventName)
	}
	if expectedFamily != eventFamily {
		return errors.New("event_family_mismatch")
	}

	// 3. Family-specific checks
	switch eventFamily {
	case "connection":
		for _, f := range connectionRequiredFields {
			if _, exists := rawMap[f]; !exists {
				return fmt.Errorf("missing_required_field: %s", f)
			}
		}
		for k := range rawMap {
			if connectionForbiddenFields[k] {
				return errors.New("forbidden_field")
			}
			if !connectionAllowedFields[k] {
				return fmt.Errorf("unknown_field_in_connection: %s", k)
			}
		}

		// trace_id must equal connection_id
		var connID, traceID string
		_ = json.Unmarshal(rawMap["connection_id"], &connID)
		_ = json.Unmarshal(rawMap["trace_id"], &traceID)
		if connID != traceID {
			return errors.New("connection_trace_id_must_equal_connection_id")
		}

	case "resolver_operation":
		for _, f := range resolverOperationRequiredFields {
			if _, exists := rawMap[f]; !exists {
				return fmt.Errorf("missing_required_field: %s", f)
			}
		}
		for k := range rawMap {
			if resolverOperationForbiddenFields[k] {
				return errors.New("forbidden_field_in_resolver_operation")
			}
			if !resolverOperationAllowedFields[k] {
				return fmt.Errorf("unknown_field_in_resolver_operation: %s", k)
			}
		}

	case "node":
		for _, f := range nodeRequiredFields {
			if _, exists := rawMap[f]; !exists {
				return fmt.Errorf("missing_required_field: %s", f)
			}
		}
		for k := range rawMap {
			if nodeForbiddenFields[k] {
				return errors.New("forbidden_field_in_node")
			}
			if !nodeAllowedFields[k] {
				return fmt.Errorf("unknown_field_in_node: %s", k)
			}
		}

	default:
		return fmt.Errorf("unsupported_event_family: %s", eventFamily)
	}

	return nil
}

// EvaluateLeaseSnapshot applies snapshot logic, RTT check, and calculates monotonic deadlines.
func EvaluateLeaseSnapshot(
	currentEpoch string,
	currentVersion int64,
	rttMs int64,
	monotonicNowMs int64,
	snapshot LeaseSnapshot,
	existingDeadlines map[string]int64,
) LeaseEvaluationResult {
	// RTT Check
	if rttMs > LeaseMaxRTTMs {
		return LeaseEvaluationResult{
			Accepted: false,
			Reason:   "rtt_exceeds_limit",
		}
	}

	// Check epoch and version
	isNewEpoch := snapshot.ControlEpoch != currentEpoch
	if !isNewEpoch && snapshot.ControlVersion <= currentVersion {
		return LeaseEvaluationResult{
			Accepted: false,
			Reason:   "stale_control_version",
		}
	}

	// Validate sessions inside snapshot
	for _, sess := range snapshot.Sessions {
		if !hex24Regex.MatchString(sess.DiagnosticID) {
			return LeaseEvaluationResult{
				Accepted: false,
				Reason:   "invalid_session_in_snapshot",
			}
		}
	}

	serverTime, err := time.Parse(time.RFC3339, snapshot.ServerTime)
	if err != nil {
		serverTime, err = time.Parse(time.RFC3339Nano, snapshot.ServerTime)
		if err != nil {
			return LeaseEvaluationResult{
				Accepted: false,
				Reason:   "invalid_server_time",
			}
		}
	}

	deadlines := make(map[string]int64)
	activeIDs := make([]string, 0, len(snapshot.Sessions))
	var lastDeadline int64

	for _, sess := range snapshot.Sessions {
		if !sess.Enabled {
			continue
		}

		expTime, err := time.Parse(time.RFC3339, sess.ExpiresAt)
		if err != nil {
			expTime, err = time.Parse(time.RFC3339Nano, sess.ExpiresAt)
			if err != nil {
				return LeaseEvaluationResult{
					Accepted: false,
					Reason:   "invalid_session_expires_at",
				}
			}
		}

		diffMs := expTime.Sub(serverTime).Milliseconds()
		remaining := diffMs - rttMs
		if remaining < 0 {
			remaining = 0
		}
		// Allow up to TTL + 60s clock skew leeway before clamping
		if remaining > (LeaseTTLSeconds+60)*1000 {
			remaining = (LeaseTTLSeconds + 60) * 1000
		}

		localDeadline := monotonicNowMs + remaining
		if !isNewEpoch && existingDeadlines != nil {
			if old, exists := existingDeadlines[sess.DiagnosticID]; exists {
				if old < localDeadline {
					localDeadline = old
				}
			}
		}
		deadlines[sess.DiagnosticID] = localDeadline
		activeIDs = append(activeIDs, sess.DiagnosticID)
		lastDeadline = localDeadline
	}

	return LeaseEvaluationResult{
		Accepted:                  true,
		ActiveDiagnosticIDs:       activeIDs,
		DeadlinesMonotonicMS:      deadlines,
		ExpectedDeadlineMonotonic: lastDeadline,
	}
}

// UTF8ByteBudgetValidator confirms string serialization conforms to max byte and rune constraints.
func UTF8ByteBudgetValidator(s string, maxBytes int, maxRunes int) bool {
	if len(s) > maxBytes {
		return false
	}
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		return false
	}
	return true
}
