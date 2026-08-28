package legacymigration

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type RegistrationTrialSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyRegistrationTrialSettings
	Checksum string
}

func ReadRegistrationTrialSettingsSnapshot(ctx context.Context, sourcePath string) (RegistrationTrialSettingsSnapshot, error) {
	settings := store.LegacyRegistrationTrialSettings{Hours: 1}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('try_out_plan_id','try_out_hour')
		`, 2, 1024, "legacy registration trial settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,value FROM v2_settings WHERE name IN ('try_out_plan_id','try_out_hour') ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy registration trial settings: %w", err)
		}
		defer rows.Close()
		values := make(map[string]sql.NullString, 2)
		for rows.Next() {
			var name string
			var value sql.NullString
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy registration trial setting: %w", err)
			}
			if _, exists := values[name]; exists {
				return fmt.Errorf("legacy registration trial settings contain duplicate %q rows", name)
			}
			values[name] = value
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy registration trial settings: %w", err)
		}
		if value, exists := values["try_out_plan_id"]; exists && value.Valid {
			settings.PlanID, err = strconv.ParseInt(value.String, 10, 64)
			if err != nil {
				return fmt.Errorf("legacy registration trial plan id must be an integer")
			}
		}
		if settings.PlanID > 0 {
			if value, exists := values["try_out_hour"]; exists && value.Valid {
				parsed, parseErr := strconv.ParseInt(value.String, 10, 32)
				if parseErr != nil {
					return fmt.Errorf("legacy registration trial duration must be an integer")
				}
				settings.Hours = int(parsed)
			}
			if settings.Hours < 1 || settings.Hours > 8760 {
				return fmt.Errorf("legacy registration trial duration must be between 1 and 8760 hours")
			}
		} else {
			// A disabled legacy trial has no effective duration. Normalize it to
			// the new safe default instead of preserving zero/fractional values.
			settings.Hours = 1
		}
		if err := store.ValidateLegacyRegistrationTrialSettingsData(settings); err != nil {
			return fmt.Errorf("validate legacy registration trial settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return RegistrationTrialSettingsSnapshot{}, err
	}
	return RegistrationTrialSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyRegistrationTrialSettingsChecksum(settings),
	}, nil
}
