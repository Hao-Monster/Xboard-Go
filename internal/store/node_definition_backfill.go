package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type legacyNodeDefinition struct {
	id            int64
	name          string
	nodeType      string
	host          string
	port          string
	show          bool
	enabled       bool
	sort          int
	machineID     sql.NullInt64
	rateMicros    int64
	runtimeConfig sql.NullString
	createdAt     int64
}

// applySchemaV42 closes the compatibility gap left by the compact node API:
// versions 26-41 allowed a node to exist without its editable protocol
// definition. Existing runtime JSON is never replaced during the backfill.
func applySchemaV42(ctx context.Context, tx *sql.Tx) error {
	const batchSize = 256
	var lastID int64
	for {
		missing, err := listLegacyNodesMissingDefinitions(ctx, tx, lastID, batchSize)
		if err != nil {
			return err
		}
		if len(missing) == 0 {
			return nil
		}
		for _, item := range missing {
			var machineID *int64
			if item.machineID.Valid {
				value := item.machineID.Int64
				machineID = &value
			}
			input, err := NewBasicAdminNodeDefinitionInput(CreateNodeInput{
				Name: item.name, Type: item.nodeType, Host: item.host, Port: item.port,
				Show: item.show, Enabled: item.enabled, Sort: item.sort, MachineID: machineID,
			})
			if err != nil {
				return fmt.Errorf("prepare protocol definition for node %d: %w", item.id, err)
			}
			input.RateMicros = item.rateMicros
			if item.runtimeConfig.Valid && strings.TrimSpace(item.runtimeConfig.String) != "" {
				input = hydrateLegacyNodeDefinition(input, json.RawMessage(item.runtimeConfig.String))
			}
			normalized, err := normalizeAdminNodeDefinition(input, false)
			if err != nil {
				return fmt.Errorf("normalize protocol definition for node %d: %w", item.id, err)
			}
			if err := insertAdminNodeDefinition(ctx, tx, item.id, normalized); err != nil {
				return fmt.Errorf("backfill protocol definition for node %d: %w", item.id, err)
			}
			if !item.runtimeConfig.Valid || strings.TrimSpace(item.runtimeConfig.String) == "" {
				runtime, err := buildAdminNodeRuntime(normalized, time.Unix(item.createdAt, 0), nil)
				if err != nil {
					return fmt.Errorf("build runtime for node %d: %w", item.id, err)
				}
				if _, err := tx.ExecContext(ctx, `UPDATE nodes SET runtime_config = ? WHERE id = ?`, runtime, item.id); err != nil {
					return fmt.Errorf("backfill runtime for node %d: %w", item.id, err)
				}
			}
			lastID = item.id
		}
	}
}

func listLegacyNodesMissingDefinitions(ctx context.Context, tx *sql.Tx, afterID int64, limit int) ([]legacyNodeDefinition, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.name, n.type, n.host, n.port, n.show, n.enabled, n.sort,
		       n.machine_id, n.rate_micros, n.runtime_config, n.created_at
		FROM nodes n
		LEFT JOIN node_protocol_definitions d ON d.node_id = n.id
		WHERE n.id > ? AND d.node_id IS NULL
		ORDER BY n.id
		LIMIT ?
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list nodes missing protocol definitions: %w", err)
	}
	missing := make([]legacyNodeDefinition, 0, limit)
	for rows.Next() {
		var item legacyNodeDefinition
		if err := rows.Scan(
			&item.id, &item.name, &item.nodeType, &item.host, &item.port, &item.show, &item.enabled,
			&item.sort, &item.machineID, &item.rateMicros, &item.runtimeConfig, &item.createdAt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan node missing protocol definition: %w", err)
		}
		missing = append(missing, item)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close nodes missing protocol definitions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes missing protocol definitions: %w", err)
	}
	return missing, nil
}

// hydrateLegacyNodeDefinition recovers fields represented by the legacy raw
// runtime API. If a hand-written runtime cannot satisfy the stricter editable
// definition contract, the runtime remains authoritative and untouched while
// the safest valid definition (common fields first, then defaults) is stored.
func hydrateLegacyNodeDefinition(base SaveAdminNodeDefinitionInput, raw json.RawMessage) SaveAdminNodeDefinitionInput {
	_, value, err := normalizeAdminNodeJSONObject(raw, "runtime_config", false)
	if err != nil {
		return base
	}
	runtime := value.(map[string]any)
	protocol, _ := runtime["protocol"].(string)
	if !strings.EqualFold(strings.TrimSpace(protocol), base.Type) {
		return base
	}

	common := base
	if listenAddress, ok := runtime["listen_ip"].(string); ok {
		common.ListenAddress = listenAddress
	}
	if adminNodeInteger(runtime["server_port"], 1, 65_535) {
		common.ServerPort = int(adminNodeInt(runtime["server_port"]))
	}
	full := common
	settings := decodeLegacyDefinitionObject(base.ProtocolSettings)
	hydrateLegacyProtocolSettings(base.Type, settings, runtime)
	if encoded, err := json.Marshal(settings); err == nil {
		full.ProtocolSettings = encoded
	}
	for source, target := range map[string]*json.RawMessage{
		"custom_outbounds": &full.CustomOutbounds,
		"custom_routes":    &full.CustomRoutes,
		"cert_config":      &full.CertificateConfig,
	} {
		if entry, exists := runtime[source]; exists {
			if encoded, err := json.Marshal(entry); err == nil {
				*target = encoded
			}
		}
	}
	if _, err := normalizeAdminNodeDefinition(full, false); err == nil {
		return full
	}
	if _, err := normalizeAdminNodeDefinition(common, false); err == nil {
		return common
	}
	return base
}

func decodeLegacyDefinitionObject(raw json.RawMessage) map[string]any {
	_, value, err := normalizeAdminNodeJSONObject(raw, "protocol_settings", false)
	if err != nil {
		return map[string]any{}
	}
	return value.(map[string]any)
}

func copyLegacyRuntimeFields(target, source map[string]any, fields ...string) {
	for _, field := range fields {
		if value, exists := source[field]; exists {
			target[field] = value
		}
	}
}

func legacyTLSSettings(runtime map[string]any) (map[string]any, []any) {
	value, _ := runtime["tls_settings"].(map[string]any)
	tls := cloneAdminNodeMap(value)
	var alpn []any
	if values, ok := tls["alpn"].([]any); ok {
		alpn = values
	} else if text, ok := tls["alpn"].(string); ok && text != "" {
		alpn = []any{text}
	}
	delete(tls, "alpn")
	return tls, alpn
}

func hydrateLegacyProtocolSettings(protocol string, settings, runtime map[string]any) {
	switch protocol {
	case "shadowsocks":
		copyLegacyRuntimeFields(settings, runtime, "cipher", "plugin", "plugin_opts")
	case "vmess":
		copyLegacyRuntimeFields(settings, runtime, "tls", "network", "tls_settings", "multiplex", "utls")
		if value, exists := runtime["networkSettings"]; exists {
			if value == nil {
				value = map[string]any{}
			}
			settings["network_settings"] = value
		}
	case "trojan", "vless":
		copyLegacyRuntimeFields(settings, runtime, "tls", "network", "multiplex", "utls")
		if value, exists := runtime["networkSettings"]; exists {
			if value == nil {
				value = map[string]any{}
			}
			settings["network_settings"] = value
		}
		tlsMode := adminNodeInt(runtime["tls"])
		if value, exists := runtime["tls_settings"]; exists {
			if tlsMode == 2 {
				settings["reality_settings"] = value
				if protocol == "trojan" {
					tlsSettings := cloneAdminNodeMap(settings["tls_settings"])
					if serverName, ok := runtime["server_name"].(string); ok {
						tlsSettings["server_name"] = serverName
					}
					settings["tls_settings"] = tlsSettings
				}
			} else {
				settings["tls_settings"] = value
			}
		}
		if protocol == "vless" {
			copyLegacyRuntimeFields(settings, runtime, "flow")
			if decryption, ok := runtime["decryption"].(string); ok && decryption != "" {
				settings["encryption"] = map[string]any{"enabled": true, "encryption": "", "decryption": decryption}
			}
		}
	case "hysteria":
		copyLegacyRuntimeFields(settings, runtime, "version", "hop_interval")
		tls, alpn := legacyTLSSettings(runtime)
		if len(tls) > 0 {
			settings["tls"] = tls
		}
		if len(alpn) > 0 {
			settings["alpn"] = alpn[0]
		}
		settings["bandwidth"] = map[string]any{"up": runtime["up_mbps"], "down": runtime["down_mbps"]}
		version := adminNodeInt(runtime["version"])
		if version == 1 {
			password, _ := runtime["obfs"].(string)
			settings["obfs"] = map[string]any{"open": password != "", "type": "", "password": password}
		} else {
			obfsType, _ := runtime["obfs"].(string)
			settings["obfs"] = map[string]any{"open": obfsType != "", "type": obfsType, "password": runtime["obfs-password"]}
		}
	case "tuic":
		copyLegacyRuntimeFields(settings, runtime, "version", "congestion_control", "udp_relay_mode")
		tls, alpn := legacyTLSSettings(runtime)
		if len(tls) > 0 {
			settings["tls"] = tls
		}
		if len(alpn) > 0 {
			settings["alpn"] = alpn
		}
	case "socks", "naive", "http":
		copyLegacyRuntimeFields(settings, runtime, "tls", "tls_settings")
	case "mieru":
		copyLegacyRuntimeFields(settings, runtime, "transport", "traffic_pattern", "multiplex")
	case "anytls":
		copyLegacyRuntimeFields(settings, runtime, "padding_scheme")
		tls, alpn := legacyTLSSettings(runtime)
		if len(tls) > 0 {
			settings["tls"] = tls
		}
		if len(alpn) > 0 {
			if value, ok := alpn[0].(string); ok {
				settings["alpn"] = value
			}
		}
	}
}
