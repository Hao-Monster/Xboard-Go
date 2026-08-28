package store

import (
	"bytes"
	"context"
	"crypto/md5" // Compatibility key derivation shared with existing Xboard subscriptions.
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxAdminNodeDefinitionJSON = 1 << 20
	maxAdminNodeTags           = 1_000
	maxAdminNodeJSONDepth      = 32
	maxAdminNodeJSONValues     = 50_000
)

type normalizedAdminNodeDefinition struct {
	SaveAdminNodeDefinitionInput
	Tags              []string
	GroupIDs          []int64
	RouteIDs          []int64
	ProtocolSettings  json.RawMessage
	RateTimeRanges    json.RawMessage
	CustomOutbounds   json.RawMessage
	CustomRoutes      json.RawMessage
	CertificateConfig json.RawMessage
	settings          map[string]any
	certificate       map[string]any
}

var basicAdminNodeProtocolSettings = map[string]string{
	"shadowsocks": `{"cipher":"aes-128-gcm","plugin":"","plugin_opts":""}`,
	"vmess":       `{"tls":0,"network":"tcp","network_settings":{},"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`,
	"trojan":      `{"tls":1,"network":"tcp","network_settings":{},"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"","server_port":443,"public_key":"","private_key":"","short_id":"","allow_insecure":false},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`,
	"hysteria":    `{"version":2,"alpn":"h2","obfs":{"open":false,"type":"salamander","password":""},"tls":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"bandwidth":{"up":0,"down":0}}`,
	"vless":       `{"tls":0,"network":"tcp","network_settings":{},"flow":"","encryption":{"enabled":false,"encryption":"","decryption":""},"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}},"reality_settings":{"server_name":"","server_port":443,"public_key":"","private_key":"","short_id":"","allow_insecure":false},"utls":{"enabled":false,"fingerprint":"chrome"},"multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`,
	"tuic":        `{"version":5,"congestion_control":"bbr","alpn":["h3"],"udp_relay_mode":"native","tls":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`,
	"socks":       `{"tls":0,"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`,
	"naive":       `{"tls":0,"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`,
	"http":        `{"tls":0,"tls_settings":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`,
	"mieru":       `{"transport":"TCP","traffic_pattern":"","multiplex":{"enabled":false,"protocol":"smux","max_connections":4,"padding":false,"brutal":{"enabled":false,"up_mbps":100,"down_mbps":100}}}`,
	"anytls":      `{"alpn":"","padding_scheme":[],"tls":{"server_name":"","allow_insecure":false,"ech":{"enabled":false,"config":"","query_server_name":"","key":""}}}`,
}

// NewBasicAdminNodeDefinitionInput upgrades the legacy compact create payload to
// a complete editable definition without changing its observable node defaults.
func NewBasicAdminNodeDefinitionInput(input CreateNodeInput) (SaveAdminNodeDefinitionInput, error) {
	nodeType := strings.ToLower(strings.TrimSpace(input.Type))
	settings, ok := basicAdminNodeProtocolSettings[nodeType]
	portParts := nodePortPattern.FindStringSubmatch(strings.TrimSpace(input.Port))
	if !ok || portParts == nil {
		return SaveAdminNodeDefinitionInput{}, fmt.Errorf("%w: invalid basic administrator node definition", ErrInvalidInput)
	}
	serverPort, err := strconv.Atoi(portParts[1])
	if err != nil {
		return SaveAdminNodeDefinitionInput{}, fmt.Errorf("%w: invalid basic administrator node port", ErrInvalidInput)
	}
	return SaveAdminNodeDefinitionInput{
		Type: nodeType, Name: input.Name, RateMicros: trafficRateScale, Tags: []string{}, Host: input.Host,
		Port: input.Port, ServerPort: serverPort, ListenAddress: "0.0.0.0", ProtocolSettings: json.RawMessage(settings),
		Show: input.Show, Enabled: input.Enabled, Sort: input.Sort, MachineID: input.MachineID,
		GroupIDs: []int64{}, RouteIDs: []int64{}, RateTimeRanges: json.RawMessage(`[]`),
		CustomOutbounds: json.RawMessage(`[]`), CustomRoutes: json.RawMessage(`[]`),
		CertificateConfig: json.RawMessage(`{"cert_mode":"none"}`),
	}, nil
}

func (s *Store) CreateAdminNodeDefinition(ctx context.Context, input SaveAdminNodeDefinitionInput, now time.Time) (AdminNodeDefinition, AdminNodeMutation, error) {
	normalized, err := normalizeAdminNodeDefinition(input, false)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("begin administrator node definition create: %w", err)
	}
	defer tx.Rollback()
	if err := validateAdminNodeDefinitionReferences(ctx, tx, 0, normalized); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	parentCreatedAt, err := adminNodeParentCreatedAt(ctx, tx, normalized.ParentID)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	runtime, err := buildAdminNodeRuntime(normalized, now, parentCreatedAt)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO nodes (
			name, type, host, port, show, enabled, sort, machine_id, rate_micros, runtime_config,
			traffic_u, traffic_d, created_at, updated_at, admin_revision
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, 1)
	`, normalized.Name, normalized.Type, normalized.Host, normalized.Port, normalized.Show, normalized.Enabled,
		normalized.Sort, normalized.MachineID, normalized.RateMicros, runtime, now.Unix(), now.Unix())
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("create administrator node: %w", err)
	}
	nodeID, err := result.LastInsertId()
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("read administrator node id: %w", err)
	}
	if err := insertAdminNodeDefinition(ctx, tx, nodeID, normalized); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	if err := replaceAdminNodeMemberships(ctx, tx, nodeID, normalized.GroupIDs, normalized.RouteIDs); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("commit administrator node definition create: %w", err)
	}
	created, err := s.GetAdminNodeDefinition(ctx, nodeID)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	return created, mutationForNodes(nil, []Node{created.Node}), nil
}

func (s *Store) UpdateAdminNodeDefinition(ctx context.Context, nodeID int64, input SaveAdminNodeDefinitionInput, now time.Time) (AdminNodeDefinition, AdminNodeMutation, error) {
	if nodeID < 1 {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("%w: invalid administrator node id", ErrInvalidInput)
	}
	normalized, err := normalizeAdminNodeDefinition(input, true)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("begin administrator node definition update: %w", err)
	}
	defer tx.Rollback()
	current, err := loadAdminNodeTarget(ctx, tx, nodeID)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	if current.Revision != normalized.Revision {
		return AdminNodeDefinition{}, AdminNodeMutation{}, ErrConflict
	}
	if err := validateAdminNodeDefinitionReferences(ctx, tx, nodeID, normalized); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM nodes WHERE id = ?`, nodeID).Scan(&createdAt); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("read administrator node created time: %w", err)
	}
	parentCreatedAt, err := adminNodeParentCreatedAt(ctx, tx, normalized.ParentID)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	runtime, err := buildAdminNodeRuntime(normalized, time.Unix(createdAt, 0), parentCreatedAt)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes SET name = ?, type = ?, host = ?, port = ?, show = ?, enabled = ?, sort = ?,
			machine_id = ?, rate_micros = ?, runtime_config = ?, admin_revision = admin_revision + 1, updated_at = ?
		WHERE id = ? AND admin_revision = ?
	`, normalized.Name, normalized.Type, normalized.Host, normalized.Port, normalized.Show, normalized.Enabled,
		normalized.Sort, normalized.MachineID, normalized.RateMicros, runtime, now.Unix(), nodeID, normalized.Revision)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("update administrator node definition: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return AdminNodeDefinition{}, AdminNodeMutation{}, ErrConflict
	}
	if err := updateAdminNodeDefinition(ctx, tx, nodeID, normalized); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	if err := replaceAdminNodeMemberships(ctx, tx, nodeID, normalized.GroupIDs, normalized.RouteIDs); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, fmt.Errorf("commit administrator node definition update: %w", err)
	}
	updated, err := s.GetAdminNodeDefinition(ctx, nodeID)
	if err != nil {
		return AdminNodeDefinition{}, AdminNodeMutation{}, err
	}
	mutation := mutationForNodes([]adminNodeTarget{current}, []Node{updated.Node})
	listChanged := current.Name != updated.Name || current.Type != updated.Type || current.Enabled != updated.Enabled ||
		current.Sort != updated.Sort || !sameOptionalInt64(current.MachineID, updated.MachineID)
	if !listChanged {
		mutation.MachineIDs = nil
	}
	if current.Enabled && updated.Enabled && sameOptionalInt64(current.MachineID, updated.MachineID) && updated.MachineID != nil {
		mutation.FullSyncs = []AdminNodeFullSync{{MachineID: *updated.MachineID, NodeID: nodeID}}
	}
	if (current.Enabled && !updated.Enabled) || !sameOptionalInt64(current.MachineID, updated.MachineID) {
		mutation.ClearNodeIDs = []int64{nodeID}
	}
	return updated, mutation, nil
}

func (s *Store) GetAdminNodeDefinition(ctx context.Context, nodeID int64) (AdminNodeDefinition, error) {
	if nodeID < 1 {
		return AdminNodeDefinition{}, fmt.Errorf("%w: invalid administrator node id", ErrInvalidInput)
	}
	var item AdminNodeDefinition
	var externalCode sql.NullString
	var parentID, machineID, lastCheckAt, lastPushAt sql.NullInt64
	var rateMicros, createdAt, updatedAt int64
	var tags, settings, rateRanges, outbounds, routes, certificate string
	err := s.db.QueryRowContext(ctx, `
		SELECT n.id, n.admin_revision, n.name, n.type, n.host, n.port, n.show, n.enabled, n.sort,
		       d.configured_rate_micros, n.traffic_u, n.traffic_d, n.runtime_config IS NOT NULL,
		       n.last_check_at, n.last_push_at, n.machine_id, n.created_at, n.updated_at,
		       d.external_code, d.parent_id, d.server_port, d.listen_address, d.tags_json,
		       d.protocol_settings_json, d.rate_time_enabled, d.rate_time_ranges_json,
		       d.custom_outbounds_json, d.custom_routes_json, d.cert_config_json, d.transfer_enable
		FROM nodes n JOIN node_protocol_definitions d ON d.node_id = n.id WHERE n.id = ?
	`, nodeID).Scan(
		&item.ID, &item.Revision, &item.Name, &item.Type, &item.Host, &item.Port, &item.Show, &item.Enabled, &item.Sort,
		&rateMicros, &item.TrafficUpload, &item.TrafficDownload, &item.RuntimeConfigured,
		&lastCheckAt, &lastPushAt, &machineID, &createdAt, &updatedAt,
		&externalCode, &parentID, &item.ServerPort, &item.ListenAddress, &tags, &settings,
		&item.RateTimeEnabled, &rateRanges, &outbounds, &routes, &certificate, &item.TransferEnable,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminNodeDefinition{}, ErrNotFound
	}
	if err != nil {
		return AdminNodeDefinition{}, fmt.Errorf("read administrator node definition: %w", err)
	}
	item.Rate = float64(rateMicros) / float64(trafficRateScale)
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	if externalCode.Valid {
		item.ExternalCode = externalCode.String
	}
	if parentID.Valid {
		item.ParentID = &parentID.Int64
	}
	if machineID.Valid {
		item.MachineID = &machineID.Int64
	}
	if lastCheckAt.Valid {
		value := time.Unix(lastCheckAt.Int64, 0).UTC()
		item.LastCheckAt = &value
	}
	if lastPushAt.Valid {
		value := time.Unix(lastPushAt.Int64, 0).UTC()
		item.LastPushAt = &value
	}
	if err := json.Unmarshal([]byte(tags), &item.Tags); err != nil {
		return AdminNodeDefinition{}, fmt.Errorf("decode administrator node tags: %w", err)
	}
	item.ProtocolSettings = json.RawMessage(settings)
	item.RateTimeRanges = json.RawMessage(rateRanges)
	item.CustomOutbounds = json.RawMessage(outbounds)
	item.CustomRoutes = json.RawMessage(routes)
	item.CertificateConfig = json.RawMessage(certificate)
	item.GroupIDs, err = listAdminNodeMembershipIDs(ctx, s.db, "node_group_memberships", "group_id", nodeID)
	if err != nil {
		return AdminNodeDefinition{}, err
	}
	item.RouteIDs, err = listAdminNodeMembershipIDs(ctx, s.db, "node_route_memberships", "route_id", nodeID)
	if err != nil {
		return AdminNodeDefinition{}, err
	}
	return item, nil
}

func normalizeAdminNodeDefinition(input SaveAdminNodeDefinitionInput, updating bool) (normalizedAdminNodeDefinition, error) {
	name, host, err := normalizeAdminNodeFields(input.Name, input.Host, input.Port, input.Sort)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	input.Name, input.Host = name, host
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.ExternalCode = strings.TrimSpace(input.ExternalCode)
	if _, ok := supportedNodeTypes[input.Type]; !ok || (updating && input.Revision < 1) || (!updating && input.Revision != 0) ||
		input.RateMicros < 1 || input.RateMicros > 1_000_000_000 || input.ServerPort < 1 || input.ServerPort > 65535 ||
		input.TransferEnable < 0 || len(input.ExternalCode) > 255 || !validAdminNodeText(input.ExternalCode, true) {
		return normalizedAdminNodeDefinition{}, fmt.Errorf("%w: invalid administrator node definition", ErrInvalidInput)
	}
	address, err := netip.ParseAddr(strings.TrimSpace(input.ListenAddress))
	if err != nil || address.Zone() != "" {
		return normalizedAdminNodeDefinition{}, fmt.Errorf("%w: listen_address must be an IP address without a port or zone", ErrInvalidInput)
	}
	input.ListenAddress = address.Unmap().String()
	if input.ParentID != nil && *input.ParentID < 1 {
		return normalizedAdminNodeDefinition{}, fmt.Errorf("%w: invalid parent node", ErrInvalidInput)
	}
	tags, err := normalizeAdminNodeTags(input.Tags)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	groups, err := normalizeGroupIDs(input.GroupIDs)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	routes, err := normalizeRouteIDs(input.RouteIDs)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	settingsJSON, settingsValue, err := normalizeAdminNodeJSONObject(input.ProtocolSettings, "protocol_settings", false)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	settings := settingsValue.(map[string]any)
	if err := validateAdminNodeProtocolSettings(input.Type, settings); err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	rateRanges, _, err := normalizeAdminNodeJSONArray(input.RateTimeRanges, "rate_time_ranges", true)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	if err := validateLegacyRateRanges(input.RateTimeEnabled, rateRanges); err != nil {
		return normalizedAdminNodeDefinition{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	outbounds, outboundsValue, err := normalizeAdminNodeJSONArray(input.CustomOutbounds, "custom_outbounds", true)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	if err := validateAdminNodeCustomOutbounds(outboundsValue); err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	customRoutes, _, err := normalizeAdminNodeJSONArray(input.CustomRoutes, "custom_routes", true)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	certificateJSON, certificateValue, err := normalizeAdminNodeJSONObject(input.CertificateConfig, "certificate_config", true)
	if err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	certificate := certificateValue.(map[string]any)
	if err := validateAdminNodeCertificate(certificate); err != nil {
		return normalizedAdminNodeDefinition{}, err
	}
	input.Tags, input.GroupIDs, input.RouteIDs = tags, groups, routes
	input.ProtocolSettings, input.RateTimeRanges = settingsJSON, rateRanges
	input.CustomOutbounds, input.CustomRoutes, input.CertificateConfig = outbounds, customRoutes, certificateJSON
	return normalizedAdminNodeDefinition{
		SaveAdminNodeDefinitionInput: input, Tags: tags, GroupIDs: groups, RouteIDs: routes,
		ProtocolSettings: settingsJSON, RateTimeRanges: rateRanges, CustomOutbounds: outbounds,
		CustomRoutes: customRoutes, CertificateConfig: certificateJSON, settings: settings, certificate: certificate,
	}, nil
}

func normalizeAdminNodeTags(values []string) ([]string, error) {
	if values == nil || len(values) > maxAdminNodeTags {
		return nil, fmt.Errorf("%w: tags must contain at most %d values", ErrInvalidInput, maxAdminNodeTags)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 255 || !validAdminNodeText(value, false) {
			return nil, fmt.Errorf("%w: invalid administrator node tag", ErrInvalidInput)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func validAdminNodeText(value string, emptyAllowed bool) bool {
	if value == "" {
		return emptyAllowed
	}
	return utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func normalizeAdminNodeJSONObject(raw json.RawMessage, label string, emptyAllowed bool) (json.RawMessage, any, error) {
	if len(raw) == 0 && emptyAllowed {
		raw = json.RawMessage(`{}`)
	}
	return normalizeAdminNodeJSON(raw, label, "object")
}

func normalizeAdminNodeJSONArray(raw json.RawMessage, label string, emptyAllowed bool) (json.RawMessage, any, error) {
	if len(raw) == 0 && emptyAllowed {
		raw = json.RawMessage(`[]`)
	}
	return normalizeAdminNodeJSON(raw, label, "array")
}

func normalizeAdminNodeJSON(raw json.RawMessage, label, kind string) (json.RawMessage, any, error) {
	if len(raw) == 0 || len(raw) > maxAdminNodeDefinitionJSON {
		return nil, nil, fmt.Errorf("%w: %s is missing or too large", ErrInvalidInput, label)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, fmt.Errorf("%w: %s must be valid JSON", ErrInvalidInput, label)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, nil, fmt.Errorf("%w: %s contains trailing JSON", ErrInvalidInput, label)
	}
	if kind == "object" {
		if _, ok := value.(map[string]any); !ok {
			return nil, nil, fmt.Errorf("%w: %s must be a JSON object", ErrInvalidInput, label)
		}
	} else if _, ok := value.([]any); !ok {
		return nil, nil, fmt.Errorf("%w: %s must be a JSON array", ErrInvalidInput, label)
	}
	count := 0
	if err := validateAdminNodeJSONValue(value, 0, &count); err != nil {
		return nil, nil, fmt.Errorf("%w: %s %v", ErrInvalidInput, label, err)
	}
	var compacted bytes.Buffer
	compacted.Grow(len(raw))
	if err := json.Compact(&compacted, raw); err != nil {
		return nil, nil, fmt.Errorf("%w: %s must be valid JSON", ErrInvalidInput, label)
	}
	return json.RawMessage(compacted.Bytes()), value, nil
}

func validateAdminNodeJSONValue(value any, depth int, count *int) error {
	*count = *count + 1
	if depth > maxAdminNodeJSONDepth || *count > maxAdminNodeJSONValues {
		return errors.New("exceeds structural limits")
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "" || len(key) > 255 || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 {
				return errors.New("contains an invalid object key")
			}
			if err := validateAdminNodeJSONValue(child, depth+1, count); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateAdminNodeJSONValue(child, depth+1, count); err != nil {
				return err
			}
		}
	case string:
		if len(typed) > 262_144 || !utf8.ValidString(typed) {
			return errors.New("contains an invalid or oversized string")
		}
	case json.Number:
		value, err := strconv.ParseFloat(string(typed), 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("contains a non-finite number")
		}
	case nil, bool:
	default:
		return errors.New("contains an unsupported value")
	}
	return nil
}

func validateAdminNodeProtocolSettings(protocol string, settings map[string]any) error {
	allowed := map[string][]string{
		"shadowsocks": {"cipher", "obfs", "obfs_settings", "plugin", "plugin_opts"},
		"vmess":       {"tls", "network", "rules", "network_settings", "tls_settings", "multiplex", "utls"},
		"trojan":      {"tls", "network", "network_settings", "server_name", "allow_insecure", "tls_settings", "reality_settings", "multiplex", "utls"},
		"vless":       {"tls", "tls_settings", "flow", "encryption", "network", "network_settings", "reality_settings", "multiplex", "utls"},
		"hysteria":    {"version", "alpn", "bandwidth", "obfs", "tls", "hop_interval"},
		"tuic":        {"version", "congestion_control", "alpn", "udp_relay_mode", "tls"},
		"anytls":      {"alpn", "padding_scheme", "tls"},
		"socks":       {"tls", "tls_settings"},
		"naive":       {"tls", "tls_settings"},
		"http":        {"tls", "tls_settings"},
		"mieru":       {"transport", "traffic_pattern", "multiplex"},
	}[protocol]
	if err := requireAllowedAdminNodeKeys(settings, allowed, "protocol_settings"); err != nil {
		return err
	}
	switch protocol {
	case "shadowsocks":
		if !adminNodeStringSet(settings["cipher"], []string{
			"aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305",
			"2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305",
		}) || !adminNodeOptionalStringSet(settings, "plugin", []string{"", "obfs", "v2ray-plugin", "gost-plugin", "shadow-tls", "restls", "kcptun"}) ||
			!adminNodeOptionalString(settings, "plugin_opts", 4_096) {
			return fmt.Errorf("%w: invalid Shadowsocks settings", ErrInvalidInput)
		}
	case "vmess", "trojan", "vless":
		network, ok := settings["network"].(string)
		if !ok || !adminNodeAllowed(network, []string{"tcp", "ws", "grpc", "h2", "httpupgrade", "xhttp", "kcp"}) ||
			(protocol != "vless" && network == "kcp") || !adminNodeObject(settings["network_settings"]) {
			return fmt.Errorf("%w: invalid %s network settings", ErrInvalidInput, protocol)
		}
		tlsMin, tlsMax := int64(0), int64(1)
		if protocol != "vmess" {
			tlsMax = 2
		}
		if protocol == "trojan" {
			tlsMin = 1
		}
		if !adminNodeInteger(settings["tls"], tlsMin, tlsMax) || validateAdminNodeTLSObject(settings["tls_settings"], "tls_settings") != nil ||
			validateAdminNodeOptionalMultiplex(settings["multiplex"]) != nil || validateAdminNodeOptionalUTLS(settings["utls"]) != nil {
			return fmt.Errorf("%w: invalid %s transport security settings", ErrInvalidInput, protocol)
		}
		tlsMode := adminNodeInt(settings["tls"])
		if tlsMode == 0 {
			if enabled, _ := adminNodeNested(settings["utls"], "enabled").(bool); enabled {
				return fmt.Errorf("%w: uTLS requires TLS or Reality", ErrInvalidInput)
			}
		}
		if protocol != "vmess" && validateAdminNodeOptionalReality(settings["reality_settings"]) != nil {
			return fmt.Errorf("%w: invalid %s Reality settings", ErrInvalidInput, protocol)
		}
		if tlsMode == 2 {
			reality, _ := settings["reality_settings"].(map[string]any)
			if !adminNodeRequiredSingleLine(reality, "server_name") || !adminNodeRequiredSingleLine(reality, "public_key") || !adminNodeRequiredSingleLine(reality, "private_key") {
				return fmt.Errorf("%w: Reality requires server_name, public_key, and private_key", ErrInvalidInput)
			}
		}
		if protocol == "vless" {
			if !adminNodeOptionalStringSet(settings, "flow", []string{"", "xtls-rprx-direct", "xtls-rprx-splice", "xtls-rprx-vision"}) ||
				validateAdminNodeOptionalEncryption(settings["encryption"]) != nil {
				return fmt.Errorf("%w: invalid VLESS settings", ErrInvalidInput)
			}
		}
	case "hysteria":
		if !adminNodeInteger(settings["version"], 1, 2) || !adminNodeOptionalStringSet(settings, "alpn", []string{"hysteria", "http/1.1", "h2", "h3"}) ||
			validateAdminNodeTLSObject(settings["tls"], "tls") != nil || validateAdminNodeBandwidth(settings["bandwidth"]) != nil ||
			validateAdminNodeObfs(settings["obfs"]) != nil {
			return fmt.Errorf("%w: invalid Hysteria settings", ErrInvalidInput)
		}
		if value, exists := settings["hop_interval"]; exists && value != nil && !adminNodeInteger(value, 1, 86_400) {
			return fmt.Errorf("%w: invalid Hysteria hop interval", ErrInvalidInput)
		}
	case "tuic":
		if !adminNodeIntegerSet(settings["version"], []int64{4, 5}) || !adminNodeStringSet(settings["congestion_control"], []string{"bbr", "cubic", "new_reno"}) ||
			!adminNodeStringArraySet(settings["alpn"], 1, 8, []string{"h3", "h2", "http/1.1"}) || !adminNodeStringSet(settings["udp_relay_mode"], []string{"native", "quic"}) ||
			validateAdminNodeTLSObject(settings["tls"], "tls") != nil {
			return fmt.Errorf("%w: invalid TUIC settings", ErrInvalidInput)
		}
	case "anytls":
		if !adminNodeOptionalString(settings, "alpn", 64) || !adminNodeStringArray(settings["padding_scheme"], 0, 64, 1_024) ||
			validateAdminNodeTLSObject(settings["tls"], "tls") != nil {
			return fmt.Errorf("%w: invalid AnyTLS settings", ErrInvalidInput)
		}
	case "socks", "naive", "http":
		if !adminNodeInteger(settings["tls"], 0, 1) || validateAdminNodeTLSObject(settings["tls_settings"], "tls_settings") != nil {
			return fmt.Errorf("%w: invalid %s TLS settings", ErrInvalidInput, protocol)
		}
	case "mieru":
		if !adminNodeStringSet(settings["transport"], []string{"TCP", "UDP"}) || !adminNodeOptionalString(settings, "traffic_pattern", 4_096) ||
			validateAdminNodeOptionalMultiplex(settings["multiplex"]) != nil {
			return fmt.Errorf("%w: invalid Mieru settings", ErrInvalidInput)
		}
	}
	return nil
}

func requireAllowedAdminNodeKeys(object map[string]any, allowed []string, label string) error {
	set := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range object {
		if _, ok := set[key]; !ok {
			return fmt.Errorf("%w: %s contains unsupported field %q", ErrInvalidInput, label, key)
		}
	}
	return nil
}

func validateAdminNodeTLSObject(value any, label string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New(label + " must be an object")
	}
	if err := requireAllowedAdminNodeKeys(object, []string{"server_name", "allow_insecure", "ech", "alpn"}, label); err != nil {
		return err
	}
	if !adminNodeOptionalSingleLine(object, "server_name", 255) || !adminNodeOptionalBool(object, "allow_insecure") || !adminNodeOptionalSingleLine(object, "alpn", 64) {
		return errors.New("invalid " + label)
	}
	if ech, exists := object["ech"]; exists && ech != nil {
		entry, ok := ech.(map[string]any)
		if !ok || requireAllowedAdminNodeKeys(entry, []string{"enabled", "config", "query_server_name", "key", "key_path", "config_path"}, label+".ech") != nil ||
			!adminNodeOptionalBool(entry, "enabled") || !adminNodeOptionalString(entry, "config", 262_144) ||
			!adminNodeOptionalSingleLine(entry, "query_server_name", 255) || !adminNodeOptionalString(entry, "key", 262_144) ||
			!adminNodeOptionalSingleLine(entry, "key_path", 4_096) || !adminNodeOptionalSingleLine(entry, "config_path", 4_096) {
			return errors.New("invalid " + label + ".ech")
		}
		if enabled, _ := entry["enabled"].(bool); enabled {
			config := strings.TrimSpace(adminNodeStringValue(entry["config"]))
			configPath := strings.TrimSpace(adminNodeStringValue(entry["config_path"]))
			key := strings.TrimSpace(adminNodeStringValue(entry["key"]))
			keyPath := strings.TrimSpace(adminNodeStringValue(entry["key_path"]))
			if (config == "" && configPath == "") || (key == "" && keyPath == "") {
				return errors.New(label + ".ech requires config and key material when enabled")
			}
		}
	}
	return nil
}

func validateAdminNodeOptionalReality(value any) error {
	if value == nil {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"server_name", "server_port", "public_key", "private_key", "short_id", "allow_insecure"}, "reality_settings") != nil ||
		!adminNodeOptionalSingleLine(object, "server_name", 255) || !adminNodeOptionalInteger(object, "server_port", 1, 65_535) ||
		!adminNodeOptionalSingleLine(object, "public_key", 4_096) || !adminNodeOptionalSingleLine(object, "private_key", 4_096) ||
		!adminNodeOptionalSingleLine(object, "short_id", 64) || !adminNodeOptionalBool(object, "allow_insecure") {
		return errors.New("invalid reality settings")
	}
	return nil
}

func validateAdminNodeOptionalMultiplex(value any) error {
	if value == nil {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"enabled", "protocol", "max_connections", "padding", "brutal"}, "multiplex") != nil ||
		!adminNodeOptionalBool(object, "enabled") || !adminNodeOptionalStringSet(object, "protocol", []string{"smux", "yamux", "h2mux"}) || !adminNodeOptionalBool(object, "padding") {
		return errors.New("invalid multiplex settings")
	}
	if max, exists := object["max_connections"]; exists && max != nil && !adminNodeInteger(max, 1, 65_535) {
		return errors.New("invalid multiplex max_connections")
	}
	if brutal, exists := object["brutal"]; exists && brutal != nil {
		entry, ok := brutal.(map[string]any)
		if !ok || requireAllowedAdminNodeKeys(entry, []string{"enabled", "up_mbps", "down_mbps"}, "multiplex.brutal") != nil ||
			!adminNodeOptionalBool(entry, "enabled") || !adminNodeOptionalInteger(entry, "up_mbps", 1, 1_000_000) || !adminNodeOptionalInteger(entry, "down_mbps", 1, 1_000_000) {
			return errors.New("invalid multiplex brutal settings")
		}
	}
	return nil
}

func validateAdminNodeOptionalUTLS(value any) error {
	if value == nil {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"enabled", "fingerprint"}, "utls") != nil ||
		!adminNodeOptionalBool(object, "enabled") || !adminNodeOptionalStringSet(object, "fingerprint", []string{"chrome", "firefox", "safari", "ios", "edge", "random"}) {
		return errors.New("invalid utls settings")
	}
	return nil
}

func validateAdminNodeOptionalEncryption(value any) error {
	if value == nil {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"enabled", "encryption", "decryption"}, "encryption") != nil ||
		!adminNodeOptionalBool(object, "enabled") || !adminNodeOptionalString(object, "encryption", 8_192) || !adminNodeOptionalString(object, "decryption", 8_192) {
		return errors.New("invalid VLESS encryption")
	}
	return nil
}

func validateAdminNodeBandwidth(value any) error {
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"up", "down"}, "bandwidth") != nil ||
		!adminNodeOptionalInteger(object, "up", 0, 1_000_000) || !adminNodeOptionalInteger(object, "down", 0, 1_000_000) {
		return errors.New("invalid bandwidth")
	}
	return nil
}

func validateAdminNodeObfs(value any) error {
	object, ok := value.(map[string]any)
	if !ok || requireAllowedAdminNodeKeys(object, []string{"open", "type", "password"}, "obfs") != nil ||
		!adminNodeOptionalBool(object, "open") || !adminNodeOptionalStringSet(object, "type", []string{"", "salamander"}) || !adminNodeOptionalString(object, "password", 4_096) {
		return errors.New("invalid obfs")
	}
	return nil
}

func validateAdminNodeCertificate(certificate map[string]any) error {
	if err := requireAllowedAdminNodeKeys(certificate, []string{"cert_mode", "mode", "domain", "email", "dns_provider", "dns_env", "http_port", "cert_file", "key_file", "cert_content", "key_content"}, "certificate_config"); err != nil {
		return err
	}
	mode := adminNodeStringValue(certificate["cert_mode"])
	if mode == "" {
		mode = adminNodeStringValue(certificate["mode"])
	}
	if mode != "" && !adminNodeAllowed(mode, []string{"none", "dns", "http", "self", "file", "content"}) {
		return fmt.Errorf("%w: invalid certificate mode", ErrInvalidInput)
	}
	for _, field := range []string{"domain", "email", "dns_provider", "cert_file", "key_file", "cert_content", "key_content"} {
		limit := 4_096
		if field == "cert_content" || field == "key_content" {
			limit = 262_144
		}
		if !adminNodeOptionalString(certificate, field, limit) {
			return fmt.Errorf("%w: invalid certificate field %s", ErrInvalidInput, field)
		}
	}
	if port, exists := certificate["http_port"]; exists && port != nil && !adminNodeInteger(port, 1, 65_535) {
		return fmt.Errorf("%w: invalid certificate HTTP port", ErrInvalidInput)
	}
	if environment, exists := certificate["dns_env"]; exists && environment != nil {
		values, ok := environment.(map[string]any)
		if !ok || len(values) > 128 {
			return fmt.Errorf("%w: invalid certificate DNS environment", ErrInvalidInput)
		}
		for key, value := range values {
			if !validAdminNodeEnvKey(key) || !adminNodeString(value, 0, 8_192) {
				return fmt.Errorf("%w: invalid certificate DNS environment", ErrInvalidInput)
			}
		}
	}
	switch mode {
	case "http", "dns":
		if !adminNodeRequiredSingleLine(certificate, "domain") {
			return fmt.Errorf("%w: certificate domain is required for %s mode", ErrInvalidInput, mode)
		}
	case "file":
		if !adminNodeRequiredSingleLine(certificate, "cert_file") || !adminNodeRequiredSingleLine(certificate, "key_file") {
			return fmt.Errorf("%w: file certificate mode requires cert_file and key_file", ErrInvalidInput)
		}
	case "content":
		certContent, certOK := certificate["cert_content"].(string)
		keyContent, keyOK := certificate["key_content"].(string)
		if !certOK || !keyOK || certContent == "" || keyContent == "" {
			return fmt.Errorf("%w: content certificate mode requires cert_content and key_content", ErrInvalidInput)
		}
		if _, err := tls.X509KeyPair([]byte(certContent), []byte(keyContent)); err != nil {
			return fmt.Errorf("%w: certificate content and private key are not a valid pair", ErrInvalidInput)
		}
	}
	if mode == "dns" {
		if !adminNodeRequiredSingleLine(certificate, "dns_provider") {
			return fmt.Errorf("%w: DNS certificate mode requires dns_provider", ErrInvalidInput)
		}
		environment, _ := certificate["dns_env"].(map[string]any)
		hasCredential := false
		for _, value := range environment {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				hasCredential = true
				break
			}
		}
		if !hasCredential {
			return fmt.Errorf("%w: DNS certificate mode requires at least one credential", ErrInvalidInput)
		}
	}
	return nil
}

func validateAdminNodeCustomOutbounds(value any) error {
	values, ok := value.([]any)
	if !ok || len(values) > 256 {
		return fmt.Errorf("%w: custom_outbounds must contain at most 256 entries", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(values))
	available := map[string]struct{}{"direct": {}, "block": {}}
	entries := make([]map[string]any, 0, len(values))
	for index, value := range values {
		entry, ok := value.(map[string]any)
		if !ok || requireAllowedAdminNodeKeys(entry, []string{"tag", "protocol", "settings", "proxy_tag"}, "custom_outbounds") != nil {
			return fmt.Errorf("%w: custom_outbounds[%d] must be a supported object", ErrInvalidInput, index)
		}
		tag, tagOK := entry["tag"].(string)
		protocol, protocolOK := entry["protocol"].(string)
		tag = strings.TrimSpace(tag)
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if !tagOK || !protocolOK || tag == "" || len(tag) > 255 || !validAdminNodeText(tag, false) ||
			!adminNodeAllowed(protocol, []string{"vmess", "vless", "trojan", "shadowsocks", "socks", "http", "wireguard", "tuic", "hysteria2", "anytls", "naive", "mieru"}) {
			return fmt.Errorf("%w: custom_outbounds[%d] has an invalid tag or protocol", ErrInvalidInput, index)
		}
		key := strings.ToLower(tag)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: custom_outbounds[%d] has a duplicate tag", ErrInvalidInput, index)
		}
		settings, ok := entry["settings"].(map[string]any)
		if !ok || len(settings) == 0 {
			return fmt.Errorf("%w: custom_outbounds[%d].settings is required", ErrInvalidInput, index)
		}
		for _, reserved := range []string{"tag", "protocol", "proxy_tag", "proxyTag"} {
			if _, exists := settings[reserved]; exists {
				return fmt.Errorf("%w: custom_outbounds[%d].settings contains a reserved field", ErrInvalidInput, index)
			}
		}
		for _, field := range []string{"server", "uuid", "password", "method", "cipher", "private_key", "secret_key", "privateKey", "secretKey"} {
			if fieldValue, exists := settings[field]; exists {
				text, ok := fieldValue.(string)
				if !ok || strings.TrimSpace(text) == "" || !validAdminNodeText(text, false) {
					return fmt.Errorf("%w: custom_outbounds[%d].settings.%s must be a non-empty string", ErrInvalidInput, index, field)
				}
			}
		}
		for _, field := range []string{"server_port", "serverPort"} {
			if fieldValue, exists := settings[field]; exists && !adminNodePortValue(fieldValue) {
				return fmt.Errorf("%w: custom_outbounds[%d].settings.%s must be a valid port", ErrInvalidInput, index, field)
			}
		}
		seen[key] = struct{}{}
		available[key] = struct{}{}
		entries = append(entries, entry)
	}
	for index, entry := range entries {
		value, exists := entry["proxy_tag"]
		if !exists || value == nil {
			continue
		}
		proxyTag, ok := value.(string)
		proxyTag = strings.ToLower(strings.TrimSpace(proxyTag))
		ownTag := strings.ToLower(strings.TrimSpace(adminNodeStringValue(entry["tag"])))
		if !ok || proxyTag == "" || proxyTag == ownTag {
			return fmt.Errorf("%w: custom_outbounds[%d].proxy_tag is invalid", ErrInvalidInput, index)
		}
		if _, exists := available[proxyTag]; !exists {
			return fmt.Errorf("%w: custom_outbounds[%d].proxy_tag references an unknown outbound", ErrInvalidInput, index)
		}
	}
	return nil
}

func adminNodePortValue(value any) bool {
	if number, ok := value.(json.Number); ok {
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		return err == nil && parsed >= 1 && parsed <= 65_535
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(text))
	return err == nil && parsed >= 1 && parsed <= 65_535
}

func adminNodeRequiredSingleLine(object map[string]any, key string) bool {
	value, ok := object[key].(string)
	return ok && value != "" && value == strings.TrimSpace(value) && validAdminNodeText(value, false)
}

func validAdminNodeEnvKey(value string) bool {
	if value == "" || len(value) > 128 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func adminNodeString(value any, minimum, maximum int) bool {
	text, ok := value.(string)
	return ok && len(text) >= minimum && len(text) <= maximum && utf8.ValidString(text)
}

func adminNodeOptionalString(object map[string]any, key string, maximum int) bool {
	value, exists := object[key]
	return !exists || value == nil || adminNodeString(value, 0, maximum)
}

func adminNodeOptionalSingleLine(object map[string]any, key string, maximum int) bool {
	value, exists := object[key]
	if !exists || value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && len(text) <= maximum && validAdminNodeText(text, true)
}

func adminNodeOptionalStringSet(object map[string]any, key string, allowed []string) bool {
	value, exists := object[key]
	return !exists || value == nil || adminNodeStringSet(value, allowed)
}

func adminNodeOptionalBool(object map[string]any, key string) bool {
	value, exists := object[key]
	if !exists || value == nil {
		return true
	}
	_, ok := value.(bool)
	return ok
}

func adminNodeInteger(value any, minimum, maximum int64) bool {
	number, ok := value.(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	return err == nil && parsed >= minimum && parsed <= maximum
}

func adminNodeOptionalInteger(object map[string]any, key string, minimum, maximum int64) bool {
	value, exists := object[key]
	return !exists || value == nil || adminNodeInteger(value, minimum, maximum)
}

func adminNodeIntegerSet(value any, allowed []int64) bool {
	for _, candidate := range allowed {
		if adminNodeInteger(value, candidate, candidate) {
			return true
		}
	}
	return false
}

func adminNodeStringSet(value any, allowed []string) bool {
	text, ok := value.(string)
	return ok && adminNodeAllowed(text, allowed)
}

func adminNodeAllowed(value string, allowed []string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func adminNodeStringArray(value any, minimum, maximum, stringMaximum int) bool {
	values, ok := value.([]any)
	if !ok || len(values) < minimum || len(values) > maximum {
		return false
	}
	for _, entry := range values {
		if !adminNodeString(entry, 0, stringMaximum) {
			return false
		}
	}
	return true
}

func adminNodeStringArraySet(value any, minimum, maximum int, allowed []string) bool {
	values, ok := value.([]any)
	if !ok || len(values) < minimum || len(values) > maximum {
		return false
	}
	for _, entry := range values {
		if !adminNodeStringSet(entry, allowed) {
			return false
		}
	}
	return true
}

func adminNodeObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func adminNodeStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func validateAdminNodeDefinitionReferences(ctx context.Context, tx *sql.Tx, nodeID int64, input normalizedAdminNodeDefinition) error {
	if err := requireAdminNodeMachine(ctx, tx, input.MachineID); err != nil {
		return err
	}
	if err := requireReferencedIDs(ctx, tx, "server_groups", input.GroupIDs, "node groups"); err != nil {
		return err
	}
	if err := requireReferencedIDs(ctx, tx, "routing_rules", input.RouteIDs, "node routes"); err != nil {
		return err
	}
	if input.ParentID != nil {
		if *input.ParentID == nodeID {
			return fmt.Errorf("%w: node cannot be its own parent", ErrInvalidInput)
		}
		var parentType string
		if err := tx.QueryRowContext(ctx, `SELECT type FROM nodes WHERE id = ?`, *input.ParentID).Scan(&parentType); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: parent node does not exist", ErrInvalidInput)
		} else if err != nil {
			return fmt.Errorf("validate administrator parent node: %w", err)
		}
		if parentType != input.Type {
			return fmt.Errorf("%w: parent node protocol must match", ErrInvalidInput)
		}
		if nodeID > 0 {
			var cyclic bool
			if err := tx.QueryRowContext(ctx, `
				WITH RECURSIVE ancestors(id) AS (
					SELECT ?
					UNION
					SELECT d.parent_id FROM node_protocol_definitions d JOIN ancestors a ON d.node_id = a.id
					WHERE d.parent_id IS NOT NULL
				) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id = ?)
			`, *input.ParentID, nodeID).Scan(&cyclic); err != nil {
				return fmt.Errorf("validate administrator node parent cycle: %w", err)
			}
			if cyclic {
				return fmt.Errorf("%w: parent node would create a cycle", ErrInvalidInput)
			}
		}
	}
	if nodeID > 0 {
		var mismatchedChild bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM node_protocol_definitions d JOIN nodes n ON n.id = d.node_id
				WHERE d.parent_id = ? AND n.type <> ?
			)
		`, nodeID, input.Type).Scan(&mismatchedChild); err != nil {
			return fmt.Errorf("validate administrator node children: %w", err)
		}
		if mismatchedChild {
			return fmt.Errorf("%w: node protocol must match its children", ErrInvalidInput)
		}
	}
	return nil
}

func adminNodeParentCreatedAt(ctx context.Context, tx *sql.Tx, parentID *int64) (*time.Time, error) {
	if parentID == nil {
		return nil, nil
	}
	var createdAt int64
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM nodes WHERE id = ?`, *parentID).Scan(&createdAt); err != nil {
		return nil, fmt.Errorf("read administrator parent node created time: %w", err)
	}
	value := time.Unix(createdAt, 0)
	return &value, nil
}

func buildAdminNodeRuntime(input normalizedAdminNodeDefinition, createdAt time.Time, parentCreatedAt *time.Time) (json.RawMessage, error) {
	settings := input.settings
	network := settings["network"]
	networkSettings := settings["network_settings"]
	if object, ok := networkSettings.(map[string]any); ok && len(object) == 0 {
		networkSettings = nil
	}
	if array, ok := networkSettings.([]any); ok && len(array) == 0 {
		networkSettings = nil
	}
	config := map[string]any{
		"protocol": input.Type, "listen_ip": input.ListenAddress, "server_port": input.ServerPort,
		"network": network, "networkSettings": networkSettings,
	}
	switch input.Type {
	case "shadowsocks":
		config["cipher"], config["plugin"], config["plugin_opts"] = settings["cipher"], settings["plugin"], settings["plugin_opts"]
		keyTime := createdAt
		if parentCreatedAt != nil {
			keyTime = *parentCreatedAt
		}
		switch settings["cipher"] {
		case "2022-blake3-aes-128-gcm":
			config["server_key"] = adminNodeLegacyServerKey(keyTime, 16)
		case "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305":
			config["server_key"] = adminNodeLegacyServerKey(keyTime, 32)
		}
	case "vmess":
		config["tls"], config["tls_settings"], config["multiplex"], config["utls"] = adminNodeInt(settings["tls"]), settings["tls_settings"], settings["multiplex"], settings["utls"]
	case "trojan":
		tls := adminNodeInt(settings["tls"])
		config["host"], config["tls"], config["multiplex"], config["utls"] = input.Host, tls, settings["multiplex"], settings["utls"]
		config["server_name"] = adminNodeNested(settings["tls_settings"], "server_name")
		if tls == 2 {
			config["tls_settings"] = settings["reality_settings"]
		} else {
			config["tls_settings"] = settings["tls_settings"]
		}
	case "vless":
		tls := adminNodeInt(settings["tls"])
		config["tls"], config["flow"], config["multiplex"], config["utls"] = tls, settings["flow"], settings["multiplex"], settings["utls"]
		if enabled, _ := adminNodeNested(settings["encryption"], "enabled").(bool); enabled {
			config["decryption"] = adminNodeNested(settings["encryption"], "decryption")
		}
		if tls == 2 {
			config["tls_settings"] = settings["reality_settings"]
		} else {
			config["tls_settings"] = settings["tls_settings"]
		}
	case "hysteria":
		version := adminNodeInt(settings["version"])
		tls := cloneAdminNodeMap(settings["tls"])
		if alpn := adminNodeStringValue(settings["alpn"]); alpn != "" {
			tls["alpn"] = []string{alpn}
		}
		config["version"], config["host"], config["tls_settings"] = version, input.Host, tls
		config["server_name"] = tls["server_name"]
		config["up_mbps"], config["down_mbps"] = adminNodeInt(adminNodeNested(settings["bandwidth"], "up")), adminNodeInt(adminNodeNested(settings["bandwidth"], "down"))
		if version == 1 {
			config["obfs"] = adminNodeNested(settings["obfs"], "password")
		} else {
			if enabled, _ := adminNodeNested(settings["obfs"], "open").(bool); enabled {
				config["obfs"] = adminNodeNested(settings["obfs"], "type")
			}
			config["obfs-password"] = adminNodeNested(settings["obfs"], "password")
		}
		if settings["hop_interval"] != nil {
			config["hop_interval"] = adminNodeInt(settings["hop_interval"])
		}
	case "tuic":
		tls := cloneAdminNodeMap(settings["tls"])
		if alpn, ok := settings["alpn"].([]any); ok {
			tls["alpn"] = alpn
		}
		config["version"], config["server_name"] = adminNodeInt(settings["version"]), tls["server_name"]
		config["congestion_control"], config["udp_relay_mode"], config["tls_settings"] = settings["congestion_control"], settings["udp_relay_mode"], tls
		config["auth_timeout"], config["zero_rtt_handshake"], config["heartbeat"] = "3s", false, "3s"
	case "anytls":
		tls := cloneAdminNodeMap(settings["tls"])
		if alpn := adminNodeStringValue(settings["alpn"]); alpn != "" {
			tls["alpn"] = []string{alpn}
		}
		config["server_name"], config["tls_settings"], config["padding_scheme"] = tls["server_name"], tls, settings["padding_scheme"]
	case "socks", "naive", "http":
		config["tls"], config["tls_settings"] = adminNodeInt(settings["tls"]), settings["tls_settings"]
	case "mieru":
		config["transport"], config["traffic_pattern"] = settings["transport"], settings["traffic_pattern"]
		if settings["multiplex"] != nil {
			config["multiplex"] = settings["multiplex"]
		}
	}
	if err := appendAdminNodeJSONArray(config, "custom_outbounds", input.CustomOutbounds); err != nil {
		return nil, err
	}
	if err := appendAdminNodeJSONArray(config, "custom_routes", input.CustomRoutes); err != nil {
		return nil, err
	}
	if len(input.certificate) > 0 {
		certificate := cloneAdminNodeMap(input.certificate)
		if mode := adminNodeStringValue(certificate["mode"]); certificate["cert_mode"] == nil && mode != "" {
			certificate["cert_mode"] = mode
		}
		delete(certificate, "mode")
		if certificate["cert_mode"] != "none" {
			config["cert_config"] = certificate
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil || len(encoded) > maxRuntimeConfigBytes {
		return nil, fmt.Errorf("%w: generated node runtime is invalid or too large", ErrInvalidInput)
	}
	return json.RawMessage(encoded), nil
}

func adminNodeLegacyServerKey(createdAt time.Time, size int) string {
	digest := md5.Sum([]byte(strconv.FormatInt(createdAt.Unix(), 10)))
	hexDigest := hex.EncodeToString(digest[:])
	return base64.StdEncoding.EncodeToString([]byte(hexDigest[:size]))
}

func adminNodeInt(value any) int64 {
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	parsed, _ := strconv.ParseInt(string(number), 10, 64)
	return parsed
}

func adminNodeNested(value any, key string) any {
	object, _ := value.(map[string]any)
	return object[key]
}

func cloneAdminNodeMap(value any) map[string]any {
	object, _ := value.(map[string]any)
	result := make(map[string]any, len(object))
	for key, entry := range object {
		result[key] = entry
	}
	return result
}

func appendAdminNodeJSONArray(config map[string]any, key string, raw json.RawMessage) error {
	var values []any
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("%w: invalid %s", ErrInvalidInput, key)
	}
	if len(values) > 0 {
		config[key] = values
	}
	return nil
}

func insertAdminNodeDefinition(ctx context.Context, tx *sql.Tx, nodeID int64, input normalizedAdminNodeDefinition) error {
	var externalCode any
	if input.ExternalCode != "" {
		externalCode = input.ExternalCode
	}
	tags, err := json.Marshal(input.Tags)
	if err != nil {
		return fmt.Errorf("encode administrator node tags: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO node_protocol_definitions (
			node_id, external_code, parent_id, server_port, listen_address, tags_json, protocol_settings_json,
			rate_time_enabled, rate_time_ranges_json, custom_outbounds_json, custom_routes_json,
			cert_config_json, transfer_enable, configured_rate_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, nodeID, externalCode, input.ParentID, input.ServerPort, input.ListenAddress, tags, input.ProtocolSettings,
		input.RateTimeEnabled, input.RateTimeRanges, input.CustomOutbounds, input.CustomRoutes,
		input.CertificateConfig, input.TransferEnable, input.RateMicros)
	if err != nil {
		return fmt.Errorf("create administrator node protocol definition: %w", err)
	}
	return nil
}

func updateAdminNodeDefinition(ctx context.Context, tx *sql.Tx, nodeID int64, input normalizedAdminNodeDefinition) error {
	var externalCode any
	if input.ExternalCode != "" {
		externalCode = input.ExternalCode
	}
	tags, err := json.Marshal(input.Tags)
	if err != nil {
		return fmt.Errorf("encode administrator node tags: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE node_protocol_definitions SET external_code = ?, parent_id = ?, server_port = ?, listen_address = ?,
			tags_json = ?, protocol_settings_json = ?, rate_time_enabled = ?, rate_time_ranges_json = ?,
			custom_outbounds_json = ?, custom_routes_json = ?, cert_config_json = ?, transfer_enable = ?,
			configured_rate_micros = ? WHERE node_id = ?
	`, externalCode, input.ParentID, input.ServerPort, input.ListenAddress, tags, input.ProtocolSettings,
		input.RateTimeEnabled, input.RateTimeRanges, input.CustomOutbounds, input.CustomRoutes,
		input.CertificateConfig, input.TransferEnable, input.RateMicros, nodeID)
	if err != nil {
		return fmt.Errorf("update administrator node protocol definition: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrNotFound
	}
	return nil
}

func replaceAdminNodeMemberships(ctx context.Context, tx *sql.Tx, nodeID int64, groups, routes []int64) error {
	for _, target := range []struct {
		table  string
		column string
		values []int64
	}{
		{"node_group_memberships", "group_id", groups},
		{"node_route_memberships", "route_id", routes},
	} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+target.table+` WHERE node_id = ?`, nodeID); err != nil {
			return fmt.Errorf("clear administrator node memberships: %w", err)
		}
		if len(target.values) == 0 {
			continue
		}
		arguments := make([]any, 0, len(target.values)*2)
		valuesSQL := make([]string, len(target.values))
		for index, value := range target.values {
			valuesSQL[index] = "(?, ?)"
			arguments = append(arguments, nodeID, value)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+target.table+` (node_id, `+target.column+`) VALUES `+strings.Join(valuesSQL, ","), arguments...); err != nil {
			return fmt.Errorf("insert administrator node memberships: %w", err)
		}
	}
	return nil
}

type adminNodeMembershipQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listAdminNodeMembershipIDs(ctx context.Context, database adminNodeMembershipQueryer, table, column string, nodeID int64) ([]int64, error) {
	if (table != "node_group_memberships" || column != "group_id") && (table != "node_route_memberships" || column != "route_id") {
		return nil, errors.New("unsupported administrator node membership")
	}
	rows, err := database.QueryContext(ctx, `SELECT `+column+` FROM `+table+` WHERE node_id = ? ORDER BY `+column, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list administrator node memberships: %w", err)
	}
	defer rows.Close()
	result := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan administrator node membership: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list administrator node memberships: %w", err)
	}
	return result, nil
}
