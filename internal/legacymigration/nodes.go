package legacymigration

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyNodeDomainRows = 2_000_000
	maxLegacyNodeDataBytes  = int64(512 << 20)
)

type NodesSnapshot struct {
	Path        string
	Size        int64
	SHA256      string
	Machines    []store.LegacyMachine
	Credentials []store.LegacyMachineCredential
	Enrollments []store.LegacyMachineEnrollment
	LoadHistory []store.LegacyMachineLoad
	Nodes       []store.LegacyNode
	Schedules   []store.LegacyActivationSchedule
	Traffic     []store.LegacyNodeTrafficStat
	Checksums   store.LegacyNodesChecksums
}

func ReadNodesSnapshot(ctx context.Context, sourcePath string) (NodesSnapshot, error) {
	var snapshot NodesSnapshot
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		required := map[string][]string{
			"v2_server_machine":              {"id", "name", "token", "notes", "is_active", "last_seen_at", "load_status", "created_at", "updated_at"},
			"v2_server_machine_credential":   {"id", "machine_id", "token_hash", "token_prefix", "last_used_at", "revoked_at", "created_at"},
			"v2_server_machine_enrollment":   {"id", "machine_id", "code_hash", "revoke_existing", "expires_at", "consumed_at", "created_at"},
			"v2_server_machine_load_history": {"id", "machine_id", "cpu", "mem_total", "mem_used", "disk_total", "disk_used", "net_in_speed", "net_out_speed", "recorded_at"},
			"v2_server":                      {"id", "type", "code", "parent_id", "group_ids", "route_ids", "name", "rate", "tags", "host", "port", "server_port", "protocol_settings", "show", "sort", "created_at", "updated_at", "rate_time_enable", "rate_time_ranges", "custom_outbounds", "custom_routes", "cert_config", "transfer_enable", "u", "d", "machine_id", "enabled"},
			"v2_server_activation_schedule":  {"server_id", "schedule_type", "timezone", "enable_second", "disable_second", "enable_at", "disable_at", "revision", "next_transition_at", "next_target_enabled", "enabled_applied_at", "disabled_applied_at", "created_at", "updated_at"},
			"v2_stat_server":                 {"server_id", "server_type", "u", "d", "record_type", "record_at", "created_at", "updated_at"},
			"v2_server_report_receipt":       {"id"},
		}
		for table, columns := range required {
			if err := requireRealTable(ctx, database, table, columns); err != nil {
				return err
			}
		}
		var receipts int64
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM v2_server_report_receipt`).Scan(&receipts); err != nil {
			return fmt.Errorf("count legacy server report receipts: %w", err)
		}
		if receipts != 0 {
			return errors.New("legacy server report receipts are transient in-flight state and must be drained before offline migration")
		}
		if err := validateLegacyNodeBudgets(ctx, database); err != nil {
			return err
		}
		var readErr error
		snapshot.Credentials, readErr = readLegacyMachineCredentials(ctx, database)
		if readErr != nil {
			return readErr
		}
		snapshot.Machines, snapshot.Credentials, readErr = readLegacyMachines(ctx, database, snapshot.Credentials)
		if readErr != nil {
			return readErr
		}
		snapshot.Enrollments, readErr = readLegacyMachineEnrollments(ctx, database)
		if readErr != nil {
			return readErr
		}
		snapshot.LoadHistory, readErr = readLegacyMachineLoads(ctx, database)
		if readErr != nil {
			return readErr
		}
		snapshot.Nodes, readErr = readLegacyNodes(ctx, database)
		if readErr != nil {
			return readErr
		}
		snapshot.Schedules, readErr = readLegacySchedules(ctx, database)
		if readErr != nil {
			return readErr
		}
		snapshot.Traffic, readErr = readLegacyNodeTraffic(ctx, database, snapshot.Nodes)
		if readErr != nil {
			return readErr
		}
		return store.ValidateLegacyNodesData(store.LegacyNodesImport{
			Machines: snapshot.Machines, Credentials: snapshot.Credentials, Enrollments: snapshot.Enrollments,
			LoadHistory: snapshot.LoadHistory, Nodes: snapshot.Nodes, Schedules: snapshot.Schedules, Traffic: snapshot.Traffic,
		})
	})
	if err != nil {
		return NodesSnapshot{}, err
	}
	snapshot.Path, snapshot.Size, snapshot.SHA256 = identity.Path, identity.Size, identity.SHA256
	snapshot.Checksums = store.LegacyNodesChecksums{
		Machines: store.LegacyMachinesChecksum(snapshot.Machines, snapshot.Credentials, snapshot.Enrollments, snapshot.LoadHistory),
		Nodes:    store.LegacyNodesChecksum(snapshot.Nodes), Schedules: store.LegacySchedulesChecksum(snapshot.Schedules), Traffic: store.LegacyNodeTrafficChecksum(snapshot.Traffic),
	}
	return snapshot, nil
}

func validateLegacyNodeBudgets(ctx context.Context, database *sql.DB) error {
	queries := []struct{ table, expression, label string }{
		{"v2_server_machine", `length(CAST(name AS BLOB))+length(CAST(token AS BLOB))+COALESCE(length(CAST(notes AS BLOB)),0)+COALESCE(length(CAST(load_status AS BLOB)),0)`, "legacy machines"},
		{"v2_server_machine_credential", `length(CAST(token_hash AS BLOB))+length(CAST(token_prefix AS BLOB))`, "legacy machine credentials"},
		{"v2_server_machine_enrollment", `length(CAST(code_hash AS BLOB))`, "legacy machine enrollments"},
		{"v2_server_machine_load_history", `64`, "legacy machine load history"},
		{"v2_server", `length(CAST(name AS BLOB))+length(CAST(host AS BLOB))+length(CAST(port AS BLOB))+COALESCE(length(CAST(protocol_settings AS BLOB)),0)+COALESCE(length(CAST(group_ids AS BLOB)),0)+COALESCE(length(CAST(route_ids AS BLOB)),0)+COALESCE(length(CAST(tags AS BLOB)),0)+COALESCE(length(CAST(rate_time_ranges AS BLOB)),0)+COALESCE(length(CAST(custom_outbounds AS BLOB)),0)+COALESCE(length(CAST(custom_routes AS BLOB)),0)+COALESCE(length(CAST(cert_config AS BLOB)),0)`, "legacy nodes"},
		{"v2_server_activation_schedule", `length(CAST(revision AS BLOB))+COALESCE(length(CAST(timezone AS BLOB)),0)`, "legacy activation schedules"},
		{"v2_stat_server", `64`, "legacy node traffic statistics"},
	}
	var total int64
	for _, item := range queries {
		var count, bytes int64
		query := `SELECT COUNT(*),COALESCE(SUM(` + item.expression + `),0) FROM ` + item.table
		if err := database.QueryRowContext(ctx, query).Scan(&count, &bytes); err != nil {
			return fmt.Errorf("measure %s: %w", item.label, err)
		}
		if count < 0 || count > maxLegacyNodeDomainRows {
			return fmt.Errorf("%s exceed the %d-row migration limit", item.label, maxLegacyNodeDomainRows)
		}
		if bytes < 0 || total > maxLegacyNodeDataBytes-bytes {
			return errors.New("legacy node data exceed the migration data limit")
		}
		total += bytes
	}
	return nil
}

func readLegacyMachineCredentials(ctx context.Context, database *sql.DB) ([]store.LegacyMachineCredential, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,machine_id,token_hash,token_prefix,last_used_at,revoked_at,`+legacyUnixExpression("created_at")+` FROM v2_server_machine_credential ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyMachineCredential{}
	for rows.Next() {
		var item store.LegacyMachineCredential
		if err := rows.Scan(&item.ID, &item.MachineID, &item.TokenHash, &item.TokenPrefix, &item.LastUsedAt, &item.RevokedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacyMachines(ctx context.Context, database *sql.DB, credentials []store.LegacyMachineCredential) ([]store.LegacyMachine, []store.LegacyMachineCredential, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,name,token,COALESCE(notes,''),is_active,last_seen_at,COALESCE(load_status,'null'),`+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+` FROM v2_server_machine ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	credentialCount := make(map[int64]int, len(credentials))
	maxID := int64(0)
	for _, item := range credentials {
		credentialCount[item.MachineID]++
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	result := []store.LegacyMachine{}
	for rows.Next() {
		var item store.LegacyMachine
		var token string
		var active int
		var load string
		if err := rows.Scan(&item.ID, &item.Name, &token, &item.Notes, &active, &item.LastSeenAt, &load, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, nil, err
		}
		if active != 0 && active != 1 {
			return nil, nil, fmt.Errorf("legacy machine id %d has invalid active state", item.ID)
		}
		item.IsActive = active == 1
		canonical, err := canonicalLegacyJSON(load, "container-or-null")
		if err != nil {
			return nil, nil, fmt.Errorf("decode legacy machine id %d load status: %w", item.ID, err)
		}
		item.LoadStatus = canonical
		result = append(result, item)
		if credentialCount[item.ID] == 0 && token != "" {
			if maxID == math.MaxInt64 {
				return nil, nil, errors.New("legacy credential identity overflow")
			}
			maxID++
			prefix := token[:min(12, len(token))]
			credentials = append(credentials, store.LegacyMachineCredential{ID: maxID, MachineID: item.ID, TokenHash: security.DigestToken(token), TokenPrefix: prefix, CreatedAt: item.CreatedAt})
			credentialCount[item.ID]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	return result, credentials, nil
}

func readLegacyMachineEnrollments(ctx context.Context, database *sql.DB) ([]store.LegacyMachineEnrollment, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,machine_id,code_hash,revoke_existing,expires_at,consumed_at,`+legacyUnixExpression("created_at")+` FROM v2_server_machine_enrollment ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyMachineEnrollment{}
	for rows.Next() {
		var item store.LegacyMachineEnrollment
		var revoke int
		if err := rows.Scan(&item.ID, &item.MachineID, &item.CodeHash, &revoke, &item.ExpiresAt, &item.ConsumedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		if revoke != 0 && revoke != 1 {
			return nil, fmt.Errorf("legacy enrollment id %d has invalid revoke state", item.ID)
		}
		item.RevokeExisting = revoke == 1
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacyMachineLoads(ctx context.Context, database *sql.DB) ([]store.LegacyMachineLoad, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,machine_id,cpu,mem_total,mem_used,disk_total,disk_used,COALESCE(net_in_speed,0),COALESCE(net_out_speed,0),recorded_at FROM v2_server_machine_load_history ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyMachineLoad{}
	for rows.Next() {
		var item store.LegacyMachineLoad
		if err := rows.Scan(&item.ID, &item.MachineID, &item.CPU, &item.MemTotal, &item.MemUsed, &item.DiskTotal, &item.DiskUsed, &item.NetInSpeed, &item.NetOutSpeed, &item.RecordedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacyNodes(ctx context.Context, database *sql.DB) ([]store.LegacyNode, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,type,COALESCE(code,''),parent_id,COALESCE(group_ids,'[]'),COALESCE(route_ids,'[]'),name,CAST(rate AS TEXT),COALESCE(tags,'[]'),host,CAST(port AS TEXT),server_port,COALESCE(protocol_settings,'{}'),show,COALESCE(sort,0),`+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+`,rate_time_enable,COALESCE(rate_time_ranges,'[]'),COALESCE(custom_outbounds,'[]'),COALESCE(custom_routes,'[]'),COALESCE(cert_config,'{}'),COALESCE(transfer_enable,0),u,d,machine_id,COALESCE(enabled,1) FROM v2_server ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyNode{}
	for rows.Next() {
		var item store.LegacyNode
		var groupRaw, routeRaw, rateRaw, tagsRaw, protocolRaw, rangesRaw, outboundsRaw, routesRaw, certRaw string
		var show, rateEnabled, enabled int
		if err := rows.Scan(&item.ID, &item.Type, &item.ExternalCode, &item.ParentID, &groupRaw, &routeRaw, &item.Name, &rateRaw, &tagsRaw, &item.Host, &item.Port, &item.ServerPort, &protocolRaw, &show, &item.Sort, &item.CreatedAt, &item.UpdatedAt, &rateEnabled, &rangesRaw, &outboundsRaw, &routesRaw, &certRaw, &item.TransferEnable, &item.TrafficUpload, &item.TrafficDownload, &item.MachineID, &enabled); err != nil {
			return nil, err
		}
		item.Type = normalizeLegacyNodeType(item.Type)
		if show < 0 || show > 1 || rateEnabled < 0 || rateEnabled > 1 || enabled < 0 || enabled > 1 {
			return nil, fmt.Errorf("legacy node id %d has invalid boolean", item.ID)
		}
		item.Show = show == 1
		item.RateTimeEnabled = rateEnabled == 1
		item.Enabled = enabled == 1
		item.RateMicros, err = parseLegacyRate(rateRaw)
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d rate: %w", item.ID, err)
		}
		item.GroupIDs, err = decodeLegacyIDs(groupRaw)
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d groups: %w", item.ID, err)
		}
		item.RouteIDs, err = decodeLegacyIDs(routeRaw)
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d routes: %w", item.ID, err)
		}
		item.Tags, err = decodeLegacyStrings(tagsRaw)
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d tags: %w", item.ID, err)
		}
		item.ProtocolSettings, err = canonicalLegacyJSON(protocolRaw, "object")
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d protocol settings: %w", item.ID, err)
		}
		item.RateTimeRanges, err = canonicalLegacyJSON(rangesRaw, "array")
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d rate ranges: %w", item.ID, err)
		}
		item.CustomOutbounds, err = canonicalLegacyJSON(outboundsRaw, "array")
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d custom outbounds: %w", item.ID, err)
		}
		item.CustomRoutes, err = canonicalLegacyJSON(routesRaw, "array")
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d custom routes: %w", item.ID, err)
		}
		item.CertConfig, err = canonicalLegacyJSON(certRaw, "object-empty-array")
		if err != nil {
			return nil, fmt.Errorf("legacy node id %d certificate config: %w", item.ID, err)
		}
		item.RuntimeConfig, err = buildLegacyRuntimeConfig(item)
		if err != nil {
			return nil, fmt.Errorf("build legacy node id %d runtime config: %w", item.ID, err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacySchedules(ctx context.Context, database *sql.DB) ([]store.LegacyActivationSchedule, error) {
	rows, err := database.QueryContext(ctx, `SELECT server_id,schedule_type,COALESCE(timezone,''),enable_second,disable_second,enable_at,disable_at,revision,next_transition_at,next_target_enabled,enabled_applied_at,disabled_applied_at,`+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+` FROM v2_server_activation_schedule ORDER BY server_id,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyActivationSchedule{}
	for rows.Next() {
		var item store.LegacyActivationSchedule
		if err := rows.Scan(&item.NodeID, &item.ScheduleType, &item.Timezone, &item.EnableSecond, &item.DisableSecond, &item.EnableAt, &item.DisableAt, &item.Revision, &item.NextTransitionAt, &item.NextTargetEnabled, &item.EnabledAppliedAt, &item.DisabledAppliedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacyNodeTraffic(ctx context.Context, database *sql.DB, nodes []store.LegacyNode) ([]store.LegacyNodeTrafficStat, error) {
	rows, err := database.QueryContext(ctx, `SELECT server_id,record_at,record_type,MIN(server_type),COUNT(DISTINCT server_type),SUM(u),SUM(d),MIN(created_at),MAX(updated_at) FROM v2_stat_server GROUP BY server_id,record_at,record_type ORDER BY server_id,record_at,record_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []store.LegacyNodeTrafficStat{}
	nodeTypes := make(map[int64]string, len(nodes))
	for _, node := range nodes {
		nodeTypes[node.ID] = node.Type
	}
	for rows.Next() {
		var item store.LegacyNodeTrafficStat
		var serverType string
		var typeCount int
		if err := rows.Scan(&item.NodeID, &item.RecordAt, &item.RecordType, &serverType, &typeCount, &item.Upload, &item.Download, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if expected, exists := nodeTypes[item.NodeID]; !exists || typeCount != 1 || normalizeLegacyNodeType(serverType) != expected {
			return nil, fmt.Errorf("legacy traffic for node id %d has an inconsistent server type", item.NodeID)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func legacyUnixExpression(column string) string {
	return `CASE WHEN typeof(` + column + `) IN ('integer','real') THEN CAST(` + column + ` AS INTEGER) ELSE unixepoch(` + column + `) END`
}

func normalizeLegacyNodeType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "v2ray":
		return "vmess"
	case "hysteria2":
		return "hysteria"
	default:
		return value
	}
}

func parseLegacyRate(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, errors.New("missing rate")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 {
		return 0, errors.New("invalid decimal")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 || whole > 1000 {
		return 0, errors.New("rate must be between 0 and 1000")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) > 6 {
			return 0, errors.New("rate has more than six decimal places")
		}
		for _, r := range fraction {
			if r < '0' || r > '9' {
				return 0, errors.New("invalid rate decimal")
			}
		}
	}
	fraction += strings.Repeat("0", 6-len(fraction))
	fractionValue := int64(0)
	if fraction != "" {
		fractionValue, _ = strconv.ParseInt(fraction, 10, 64)
	}
	if whole == 1000 && fractionValue != 0 {
		return 0, errors.New("rate exceeds 1000")
	}
	return whole*1_000_000 + fractionValue, nil
}

func decodeLegacyIDs(encoded string) ([]int64, error) {
	var raw []any
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	result := make([]int64, 0, len(raw))
	for _, value := range raw {
		var id int64
		switch typed := value.(type) {
		case json.Number:
			parsed, err := strconv.ParseInt(string(typed), 10, 64)
			if err != nil {
				id = 0
			} else {
				id = parsed
			}
		case string:
			parsed, err := strconv.ParseInt(typed, 10, 64)
			if err != nil {
				id = 0
			} else {
				id = parsed
			}
		default:
			id = 0
		}
		if id < 1 {
			return nil, errors.New("membership ids must be positive integers")
		}
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, errors.New("duplicate membership id")
		}
	}
	return result, nil
}

func decodeLegacyStrings(encoded string) ([]string, error) {
	var result []string
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return nil, err
	}
	if result == nil {
		return []string{}, nil
	}
	return result, nil
}

func canonicalLegacyJSON(encoded, kind string) (json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("trailing JSON data")
	}
	switch kind {
	case "object":
		if value == nil {
			value = map[string]any{}
		}
		if _, ok := value.(map[string]any); !ok {
			return nil, errors.New("must be a JSON object")
		}
	case "container-or-null":
		if value != nil {
			switch value.(type) {
			case map[string]any, []any:
			default:
				return nil, errors.New("must be a JSON object, array, or null")
			}
		}
	case "object-empty-array":
		if value == nil {
			value = map[string]any{}
		}
		if array, ok := value.([]any); ok {
			if len(array) != 0 {
				return nil, errors.New("must be a JSON object or empty array")
			}
			value = map[string]any{}
		}
		if _, ok := value.(map[string]any); !ok {
			return nil, errors.New("must be a JSON object")
		}
	case "array":
		if value == nil {
			value = []any{}
		}
		if _, ok := value.([]any); !ok {
			return nil, errors.New("must be a JSON array")
		}
	}
	encodedValue, err := json.Marshal(value)
	return encodedValue, err
}

func buildLegacyRuntimeConfig(node store.LegacyNode) (json.RawMessage, error) {
	var rawSettings map[string]any
	if err := json.Unmarshal(node.ProtocolSettings, &rawSettings); err != nil {
		return nil, err
	}
	settings := normalizeLegacyProtocolSettings(node.Type, rawSettings)
	base := map[string]any{"protocol": node.Type, "listen_ip": "0.0.0.0", "server_port": node.ServerPort, "network": settings["network"], "networkSettings": settings["network_settings"]}
	if !legacyPHPTruthy(base["networkSettings"]) {
		base["networkSettings"] = nil
	}
	config := base
	switch node.Type {
	case "shadowsocks":
		config["cipher"] = settings["cipher"]
		config["plugin"] = settings["plugin"]
		config["plugin_opts"] = settings["plugin_opts"]
		cipher, _ := settings["cipher"].(string)
		switch cipher {
		case "2022-blake3-aes-128-gcm":
			config["server_key"] = legacyServerKey(node.CreatedAt, 16)
		case "2022-blake3-aes-256-gcm":
			config["server_key"] = legacyServerKey(node.CreatedAt, 32)
		default:
			config["server_key"] = nil
		}
	case "vmess":
		config["tls"] = legacyInt(settings["tls"])
		config["tls_settings"] = settings["tls_settings"]
		config["multiplex"] = settings["multiplex"]
	case "trojan":
		config["host"] = node.Host
		config["server_name"] = legacyNested(settings, "tls_settings", "server_name")
		config["multiplex"] = settings["multiplex"]
		tls := legacyInt(settings["tls"])
		config["tls"] = tls
		if tls == 2 {
			config["tls_settings"] = settings["reality_settings"]
		} else {
			config["tls_settings"] = settings["tls_settings"]
		}
	case "vless":
		tls := legacyInt(settings["tls"])
		config["tls"] = tls
		config["flow"] = settings["flow"]
		if enabled, ok := legacyNested(settings, "encryption", "enabled").(bool); ok && enabled {
			config["decryption"] = legacyNested(settings, "encryption", "decryption")
		} else {
			config["decryption"] = nil
		}
		if tls == 2 {
			config["tls_settings"] = settings["reality_settings"]
		} else {
			config["tls_settings"] = settings["tls_settings"]
		}
		config["multiplex"] = settings["multiplex"]
	case "hysteria":
		version := legacyInt(settings["version"])
		config["version"] = version
		config["host"] = node.Host
		config["server_name"] = legacyNested(settings, "tls", "server_name")
		config["tls_settings"] = settings["tls"]
		config["up_mbps"] = legacyInt(legacyNested(settings, "bandwidth", "up"))
		config["down_mbps"] = legacyInt(legacyNested(settings, "bandwidth", "down"))
		if version == 1 {
			config["obfs"] = legacyNested(settings, "obfs", "password")
		} else if version == 2 {
			if open, ok := legacyNested(settings, "obfs", "open").(bool); ok && open {
				config["obfs"] = legacyNested(settings, "obfs", "type")
			} else {
				config["obfs"] = nil
			}
			config["obfs-password"] = legacyNested(settings, "obfs", "password")
		}
	case "tuic":
		config["version"] = legacyInt(settings["version"])
		config["server_name"] = legacyNested(settings, "tls", "server_name")
		config["congestion_control"] = settings["congestion_control"]
		config["tls_settings"] = settings["tls"]
		config["auth_timeout"] = "3s"
		config["zero_rtt_handshake"] = false
		config["heartbeat"] = "3s"
	case "anytls":
		config["server_name"] = legacyNested(settings, "tls", "server_name")
		config["tls_settings"] = settings["tls"]
		config["padding_scheme"] = settings["padding_scheme"]
	case "socks":
		config["tls"] = legacyInt(settings["tls"])
		config["tls_settings"] = settings["tls_settings"]
	case "naive", "http":
		config["tls"] = legacyInt(settings["tls"])
		config["tls_settings"] = settings["tls_settings"]
	case "mieru":
		transport := settings["transport"]
		if transport == nil {
			transport = "TCP"
		}
		config["transport"] = transport
		config["traffic_pattern"] = settings["traffic_pattern"]
	default:
		return nil, fmt.Errorf("unsupported protocol %q", node.Type)
	}
	var custom []any
	if err := json.Unmarshal(node.CustomOutbounds, &custom); err != nil {
		return nil, err
	}
	if len(custom) > 0 {
		config["custom_outbounds"] = custom
	}
	if err := json.Unmarshal(node.CustomRoutes, &custom); err != nil {
		return nil, err
	}
	if len(custom) > 0 {
		config["custom_routes"] = custom
	}
	var cert map[string]any
	if err := json.Unmarshal(node.CertConfig, &cert); err != nil {
		return nil, err
	}
	if mode, ok := cert["mode"]; ok {
		if _, exists := cert["cert_mode"]; !exists {
			cert["cert_mode"] = mode
		}
		delete(cert, "mode")
	}
	if len(cert) > 0 && cert["cert_mode"] != "none" {
		config["cert_config"] = cert
	}
	return json.Marshal(config)
}

func normalizeLegacyProtocolSettings(protocol string, raw map[string]any) map[string]any {
	scalar := func(key, kind string, fallback any) any {
		value, exists := raw[key]
		if !exists || value == nil {
			return fallback
		}
		switch kind {
		case "integer":
			return legacyInt(value)
		case "boolean":
			return legacyPHPTruthy(value)
		case "string":
			return legacyString(value)
		case "array":
			return legacyArray(value)
		default:
			return value
		}
	}
	object := func(key string, normalize func(map[string]any) map[string]any) any {
		value, exists := raw[key]
		if !exists || value == nil {
			return nil
		}
		if input, ok := legacyObjectInput(value); ok {
			return normalize(input)
		}
		return nil
	}
	tlsSettings := func(input map[string]any) map[string]any { return normalizeLegacyTLS(input) }
	multiplex := func(input map[string]any) map[string]any { return normalizeLegacyMultiplex(input) }
	reality := func(input map[string]any) map[string]any { return normalizeLegacyReality(input) }
	utls := func(input map[string]any) map[string]any { return normalizeLegacyUTLS(input) }

	switch protocol {
	case "trojan":
		return map[string]any{
			"tls": scalar("tls", "integer", int64(1)), "network": scalar("network", "string", nil),
			"network_settings": scalar("network_settings", "array", nil), "server_name": scalar("server_name", "string", nil),
			"allow_insecure": scalar("allow_insecure", "boolean", false), "tls_settings": object("tls_settings", tlsSettings),
			"reality_settings": object("reality_settings", reality), "multiplex": object("multiplex", multiplex), "utls": object("utls", utls),
		}
	case "vmess":
		return map[string]any{
			"tls": scalar("tls", "integer", int64(0)), "network": scalar("network", "string", nil),
			"rules": scalar("rules", "array", nil), "network_settings": scalar("network_settings", "array", nil),
			"tls_settings": object("tls_settings", tlsSettings), "multiplex": object("multiplex", multiplex), "utls": object("utls", utls),
		}
	case "vless":
		return map[string]any{
			"tls": scalar("tls", "integer", int64(0)), "tls_settings": object("tls_settings", tlsSettings),
			"flow": scalar("flow", "string", nil), "encryption": object("encryption", normalizeLegacyEncryption),
			"network": scalar("network", "string", nil), "network_settings": scalar("network_settings", "array", nil),
			"reality_settings": object("reality_settings", reality), "multiplex": object("multiplex", multiplex), "utls": object("utls", utls),
		}
	case "shadowsocks":
		return map[string]any{
			"cipher": scalar("cipher", "string", nil), "obfs": scalar("obfs", "string", nil),
			"obfs_settings": scalar("obfs_settings", "array", nil), "plugin": scalar("plugin", "string", nil),
			"plugin_opts": scalar("plugin_opts", "string", nil),
		}
	case "hysteria":
		return map[string]any{
			"version": scalar("version", "integer", int64(2)), "bandwidth": object("bandwidth", normalizeLegacyBandwidth),
			"obfs": object("obfs", normalizeLegacyObfs), "tls": object("tls", tlsSettings),
			"hop_interval": scalar("hop_interval", "integer", nil),
		}
	case "tuic":
		return map[string]any{
			"version": scalar("version", "integer", int64(5)), "congestion_control": scalar("congestion_control", "string", "cubic"),
			"alpn": scalar("alpn", "array", []any{"h3"}), "udp_relay_mode": scalar("udp_relay_mode", "string", "native"),
			"tls": object("tls", tlsSettings),
		}
	case "anytls":
		return map[string]any{
			"padding_scheme": scalar("padding_scheme", "array", []any{"stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000", "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"}),
			"tls":            object("tls", tlsSettings),
		}
	case "socks", "naive", "http":
		return map[string]any{"tls": scalar("tls", "integer", int64(0)), "tls_settings": object("tls_settings", tlsSettings)}
	case "mieru":
		return map[string]any{
			"transport": scalar("transport", "string", "TCP"), "traffic_pattern": scalar("traffic_pattern", "string", ""),
			"multiplex": object("multiplex", multiplex),
		}
	default:
		return raw
	}
}

func normalizeLegacyTLS(input map[string]any) map[string]any {
	return map[string]any{
		"server_name":    legacyNullableString(input["server_name"]),
		"allow_insecure": legacyPHPTruthy(input["allow_insecure"]),
		"ech":            normalizeLegacyOptionalObject(input["ech"], normalizeLegacyECH),
	}
}

func normalizeLegacyECH(input map[string]any) map[string]any {
	return map[string]any{
		"enabled": legacyPHPTruthy(input["enabled"]), "config": legacyNullableString(input["config"]),
		"query_server_name": legacyNullableString(input["query_server_name"]), "key": legacyNullableString(input["key"]),
		"key_path": legacyNullableString(input["key_path"]), "config_path": legacyNullableString(input["config_path"]),
	}
}

func normalizeLegacyReality(input map[string]any) map[string]any {
	return map[string]any{
		"server_name": legacyNullableString(input["server_name"]), "server_port": legacyNullableString(input["server_port"]),
		"public_key": legacyNullableString(input["public_key"]), "private_key": legacyNullableString(input["private_key"]),
		"short_id": legacyNullableString(input["short_id"]), "allow_insecure": legacyPHPTruthy(input["allow_insecure"]),
	}
}

func normalizeLegacyMultiplex(input map[string]any) map[string]any {
	protocol := "yamux"
	if input["protocol"] != nil {
		protocol = legacyString(input["protocol"])
	}
	return map[string]any{
		"enabled": legacyPHPTruthy(input["enabled"]), "protocol": protocol,
		"max_connections": legacyNullableInt(input["max_connections"]), "padding": legacyPHPTruthy(input["padding"]),
		"brutal": normalizeLegacyOptionalObject(input["brutal"], func(value map[string]any) map[string]any {
			return map[string]any{"enabled": legacyPHPTruthy(value["enabled"]), "up_mbps": legacyNullableInt(value["up_mbps"]), "down_mbps": legacyNullableInt(value["down_mbps"])}
		}),
	}
}

func normalizeLegacyUTLS(input map[string]any) map[string]any {
	fingerprint := "chrome"
	if input["fingerprint"] != nil {
		fingerprint = legacyString(input["fingerprint"])
	}
	return map[string]any{"enabled": legacyPHPTruthy(input["enabled"]), "fingerprint": fingerprint}
}

func normalizeLegacyEncryption(input map[string]any) map[string]any {
	return map[string]any{
		"enabled": legacyPHPTruthy(input["enabled"]), "encryption": legacyNullableString(input["encryption"]), "decryption": legacyNullableString(input["decryption"]),
	}
}

func normalizeLegacyBandwidth(input map[string]any) map[string]any {
	return map[string]any{"up": legacyNullableInt(input["up"]), "down": legacyNullableInt(input["down"])}
}

func normalizeLegacyObfs(input map[string]any) map[string]any {
	typeValue := "salamander"
	if input["type"] != nil {
		typeValue = legacyString(input["type"])
	}
	return map[string]any{"open": legacyPHPTruthy(input["open"]), "type": typeValue, "password": legacyNullableString(input["password"])}
}

func normalizeLegacyOptionalObject(value any, normalize func(map[string]any) map[string]any) any {
	input, ok := legacyObjectInput(value)
	if !ok {
		return nil
	}
	return normalize(input)
}

func legacyObjectInput(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case []any:
		return map[string]any{}, true
	default:
		return nil, false
	}
}

func legacyNullableString(value any) any {
	if value == nil {
		return nil
	}
	return legacyString(value)
}

func legacyNullableInt(value any) any {
	if value == nil {
		return nil
	}
	return legacyInt(value)
}

func legacyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "1"
		}
		return ""
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return string(typed)
	default:
		return ""
	}
}

func legacyArray(value any) any {
	switch typed := value.(type) {
	case []any, map[string]any:
		return typed
	default:
		return []any{typed}
	}
}

func legacyNested(value map[string]any, keys ...string) any {
	var current any = value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}
func legacyInt(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		if typed > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		return 0
	}
}

func legacyPHPTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != "" && typed != "0"
	case float64:
		return typed != 0
	case []any:
		return len(typed) != 0
	case map[string]any:
		return len(typed) != 0
	default:
		return true
	}
}
func legacyServerKey(timestamp int64, length int) string {
	sum := md5.Sum([]byte(strconv.FormatInt(timestamp, 10)))
	hexed := hex.EncodeToString(sum[:])
	return base64.StdEncoding.EncodeToString([]byte(hexed[:min(length, len(hexed))]))
}
