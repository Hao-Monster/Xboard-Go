package legacymigration

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type NodeAgentSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyNodeAgentSettings
	Checksum string
}

func ReadNodeAgentSettingsSnapshot(ctx context.Context, sourcePath string) (NodeAgentSettingsSnapshot, error) {
	settings := store.LegacyNodeAgentSettings{
		PullInterval: 60, PushInterval: 60, WebSocketEnabled: true,
	}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN (
				'server_token','server_pull_interval','server_push_interval',
				'device_limit_mode','server_ws_enable','server_ws_url'
			)
		`, 6, 8192, "legacy node agent settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,value FROM v2_settings WHERE name IN (
				'server_token','server_pull_interval','server_push_interval',
				'device_limit_mode','server_ws_enable','server_ws_url'
			) ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy node agent settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 6)
		for rows.Next() {
			var name string
			var value sql.NullString
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy node agent setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy node agent settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			raw := ""
			if value.Valid {
				raw = value.String
			}
			switch name {
			case "server_token":
				if raw != "" {
					if !validLegacyNodeAgentToken(raw) {
						return fmt.Errorf("legacy node agent setting %q must contain 16-256 printable ASCII characters without whitespace", name)
					}
					settings.ServerTokenHash = security.DigestToken(raw)
					settings.ServerTokenPrefix = raw[:min(8, len(raw))]
				}
			case "server_pull_interval":
				settings.PullInterval, err = strconv.Atoi(raw)
			case "server_push_interval":
				settings.PushInterval, err = strconv.Atoi(raw)
			case "device_limit_mode":
				settings.DeviceLimitMode, err = strconv.Atoi(raw)
			case "server_ws_enable":
				settings.WebSocketEnabled = legacyPHPBool(value)
			case "server_ws_url":
				settings.WebSocketURL = raw
			}
			if err != nil {
				return fmt.Errorf("legacy node agent setting %q is not an integer", name)
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy node agent settings: %w", err)
		}
		if err := store.ValidateLegacyNodeAgentSettingsData(settings); err != nil {
			return fmt.Errorf("validate legacy node agent settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return NodeAgentSettingsSnapshot{}, err
	}
	return NodeAgentSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyNodeAgentSettingsChecksum(settings),
	}, nil
}

func validLegacyNodeAgentToken(token string) bool {
	if len(token) < 16 || len(token) > 256 {
		return false
	}
	for _, value := range []byte(token) {
		if value < 0x21 || value > 0x7e {
			return false
		}
	}
	return true
}
