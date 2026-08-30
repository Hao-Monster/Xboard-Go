package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type PublicOriginSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyPublicOriginSettings
	Checksum string
}

func ReadPublicOriginSettingsSnapshot(ctx context.Context, sourcePath string) (PublicOriginSettingsSnapshot, error) {
	var settings store.LegacyPublicOriginSettings
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('force_https','subscribe_url')
		`, 2, 16<<10, "legacy public origin settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,COALESCE(CAST(value AS TEXT),'') FROM v2_settings
			WHERE name IN ('force_https','subscribe_url') ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy public origin settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 2)
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy public origin setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy public origin settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			switch name {
			case "force_https":
				settings.ForceHTTPS, err = parseLegacyPublicOriginBoolean(value)
				if err != nil {
					return fmt.Errorf("validate legacy force_https: %w", err)
				}
			case "subscribe_url":
				settings.SubscribeURL = value
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy public origin settings: %w", err)
		}
		settings, err = store.NormalizeLegacyPublicOriginSettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy public origin settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return PublicOriginSettingsSnapshot{}, err
	}
	return PublicOriginSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyPublicOriginSettingsChecksum(settings),
	}, nil
}

func parseLegacyPublicOriginBoolean(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true":
		return true, nil
	case "0", "false", "":
		return false, nil
	default:
		return false, errors.New("must be 0, 1, true, or false")
	}
}
