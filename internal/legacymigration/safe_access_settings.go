package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type SafeAccessSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacySafeAccessSettings
	Checksum string
}

func ReadSafeAccessSettingsSnapshot(ctx context.Context, sourcePath string, effectiveSecurePaths ...string) (SafeAccessSettingsSnapshot, error) {
	if len(effectiveSecurePaths) > 1 {
		return SafeAccessSettingsSnapshot{}, fmt.Errorf("legacy safe access settings accept at most one effective secure path override")
	}
	effectiveSecurePath := ""
	if len(effectiveSecurePaths) == 1 {
		effectiveSecurePath = effectiveSecurePaths[0]
	}
	var settings store.LegacySafeAccessSettings
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('safe_mode_enable','secure_path','frontend_admin_path')
		`, 3, 1536, "legacy safe access settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,COALESCE(CAST(value AS TEXT),'') FROM v2_settings
			WHERE name IN ('safe_mode_enable','secure_path','frontend_admin_path') ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy safe access settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 3)
		var securePath, frontendAdminPath string
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy safe access setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy safe access settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			switch name {
			case "frontend_admin_path":
				frontendAdminPath = value
			case "safe_mode_enable":
				settings.SafeModeEnabled, err = parseLegacyPublicOriginBoolean(value)
				if err != nil {
					return fmt.Errorf("validate legacy safe_mode_enable: %w", err)
				}
			case "secure_path":
				securePath = value
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy safe access settings: %w", err)
		}
		if _, exists := seen["secure_path"]; exists {
			if effectiveSecurePath != "" {
				return fmt.Errorf("legacy safe access settings already contain secure_path; an effective path override is not allowed")
			}
			settings.SecurePath = securePath
		} else if _, exists := seen["frontend_admin_path"]; exists {
			if effectiveSecurePath != "" {
				return fmt.Errorf("legacy safe access settings already contain frontend_admin_path; an effective path override is not allowed")
			}
			settings.SecurePath = frontendAdminPath
		} else {
			if effectiveSecurePath == "" {
				return fmt.Errorf("legacy safe access settings are missing secure_path and frontend_admin_path; provide the old effective path explicitly")
			}
			settings.SecurePath = effectiveSecurePath
		}
		settings, err = store.NormalizeLegacySafeAccessSettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy safe access settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return SafeAccessSettingsSnapshot{}, err
	}
	return SafeAccessSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacySafeAccessSettingsChecksum(settings),
	}, nil
}
