package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxLegacyTrustedPluginBytes = int64(64 << 10)

type TrustedPluginsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Plugins  []store.LegacyTrustedPlugin
	Checksum string
}

type legacyTrustedPluginIdentity struct {
	name, pluginType, version string
}

var legacyTrustedPluginIdentities = map[string]legacyTrustedPluginIdentity{
	store.TrustedPluginTelegram:     {name: "Telegram Bot 集成", pluginType: "feature", version: "1.0.1"},
	store.TrustedPluginAlipayF2F:    {name: "AlipayF2F", pluginType: "payment", version: "1.0.0"},
	store.TrustedPluginBTCPay:       {name: "BTCPay", pluginType: "payment", version: "1.0.0"},
	store.TrustedPluginCoinPayments: {name: "CoinPayments", pluginType: "payment", version: "1.0.0"},
	store.TrustedPluginCoinbase:     {name: "Coinbase", pluginType: "payment", version: "1.0.0"},
	store.TrustedPluginEPay:         {name: "EPay", pluginType: "payment", version: "1.0.0"},
	store.TrustedPluginMGate:        {name: "MGate", pluginType: "payment", version: "1.0.0"},
}

func ReadTrustedPluginsSnapshot(ctx context.Context, sourcePath string) (TrustedPluginsSnapshot, error) {
	var plugins []store.LegacyTrustedPlugin
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_plugins", []string{"id", "name", "code", "version", "is_enabled", "config", "type"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				COALESCE(length(CAST(name AS BLOB)),0) + COALESCE(length(CAST(code AS BLOB)),0) +
				COALESCE(length(CAST(version AS BLOB)),0) + COALESCE(length(CAST(config AS BLOB)),0) +
				COALESCE(length(CAST(type AS BLOB)),0) + 8
			),0) FROM v2_plugins
		`, len(legacyTrustedPluginIdentities), maxLegacyTrustedPluginBytes, "legacy trusted plugins"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,code,version,is_enabled,config,type FROM v2_plugins ORDER BY code,id
		`)
		if err != nil {
			return fmt.Errorf("read legacy trusted plugins: %w", err)
		}
		defer rows.Close()
		plugins = make([]store.LegacyTrustedPlugin, 0, len(legacyTrustedPluginIdentities))
		for rows.Next() {
			var name, code, version, rawConfig, pluginType string
			var enabled int64
			if err := rows.Scan(&name, &code, &version, &enabled, &rawConfig, &pluginType); err != nil {
				return fmt.Errorf("scan legacy trusted plugin: %w", err)
			}
			definition, trusted := legacyTrustedPluginIdentities[code]
			if !trusted || name != definition.name || version != definition.version || pluginType != definition.pluginType {
				return fmt.Errorf("legacy snapshot contains untrusted plugin identity %q", code)
			}
			if enabled != 0 && enabled != 1 {
				return fmt.Errorf("legacy plugin %q has invalid enabled state", code)
			}
			if !utf8.ValidString(rawConfig) {
				return fmt.Errorf("legacy plugin %q config is not valid UTF-8", code)
			}
			config, err := normalizeLegacyTrustedPluginConfig(code, rawConfig)
			if err != nil {
				return err
			}
			plugins = append(plugins, store.LegacyTrustedPlugin{
				Code: code, Type: pluginType, Version: version, Enabled: enabled == 1, Config: config,
			})
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy trusted plugins: %w", err)
		}
		if err := store.ValidateLegacyTrustedPluginsData(plugins); err != nil {
			return fmt.Errorf("validate legacy trusted plugins: %w", err)
		}
		return nil
	})
	if err != nil {
		return TrustedPluginsSnapshot{}, err
	}
	return TrustedPluginsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Plugins: plugins, Checksum: store.LegacyTrustedPluginsChecksum(plugins),
	}, nil
}

func normalizeLegacyTrustedPluginConfig(code, raw string) (map[string]any, error) {
	if code == store.TrustedPluginTelegram {
		config, err := decodeLegacyTrustedPluginObject(raw)
		if err != nil {
			return nil, fmt.Errorf("decode legacy plugin %q config: %w", code, err)
		}
		return config, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("decode legacy plugin %q config: %w", code, err)
	}
	switch config := decoded.(type) {
	case []any:
		if len(config) != 0 {
			return nil, fmt.Errorf("legacy payment plugin %q config must be empty", code)
		}
	case map[string]any:
		if len(config) != 0 {
			return nil, fmt.Errorf("legacy payment plugin %q config must be empty", code)
		}
	default:
		return nil, fmt.Errorf("legacy payment plugin %q config must be an empty array or object", code)
	}
	return map[string]any{}, nil
}

func decodeLegacyTrustedPluginObject(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	start, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := start.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("config must be an object")
	}
	config := make(map[string]any, 9)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("config contains a non-string key")
		}
		if _, duplicate := config[key]; duplicate {
			return nil, fmt.Errorf("config contains duplicate key %q", key)
		}
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		config[key] = value
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != '}' {
		return nil, errors.New("config object is not terminated")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("config contains multiple JSON values")
		}
		return nil, err
	}
	return config, nil
}
