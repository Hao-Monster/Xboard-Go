package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type CurrencySettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyCurrencySettings
	Checksum string
}

func ReadCurrencySettingsSnapshot(ctx context.Context, sourcePath string) (CurrencySettingsSnapshot, error) {
	settings := store.LegacyCurrencySettings{Currency: "CNY", CurrencySymbol: "¥"}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('currency','currency_symbol')
		`, 2, 128, "legacy currency settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,COALESCE(CAST(value AS TEXT),'') FROM v2_settings
			WHERE name IN ('currency','currency_symbol') ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy currency settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 2)
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy currency setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy currency settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			switch name {
			case "currency":
				settings.Currency = value
			case "currency_symbol":
				settings.CurrencySymbol = value
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy currency settings: %w", err)
		}
		settings, err = store.NormalizeLegacyCurrencySettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy currency settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return CurrencySettingsSnapshot{}, err
	}
	return CurrencySettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyCurrencySettingsChecksum(settings),
	}, nil
}
