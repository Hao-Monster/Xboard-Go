package legacymigration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type ConfigurationCompatibilitySettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyConfigurationCompatibilitySettings
	Checksum string
}

func ReadConfigurationCompatibilitySettingsSnapshot(ctx context.Context, sourcePath string) (ConfigurationCompatibilitySettingsSnapshot, error) {
	settings := store.LegacyConfigurationCompatibilitySettings{
		CommissionWithdrawLimit: 10_000, CommissionWithdrawMethod: []string{"支付宝", "USDT", "Paypal"},
		SidebarStyle: "light", HeaderStyle: "dark",
	}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN (
				'commission_withdraw_limit','commission_withdraw_method',
				'frontend_theme_sidebar','frontend_theme_header'
			)
		`, 4, 8_192, "legacy configuration compatibility settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name, value FROM v2_settings WHERE name IN (
				'commission_withdraw_limit','commission_withdraw_method',
				'frontend_theme_sidebar','frontend_theme_header'
			) ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy configuration compatibility settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 4)
		for rows.Next() {
			var name string
			var value sql.NullString
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy configuration compatibility setting: %w", err)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("legacy configuration compatibility settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			if !value.Valid || strings.TrimSpace(value.String) == "" {
				continue
			}
			raw := strings.TrimSpace(value.String)
			switch name {
			case "commission_withdraw_limit":
				settings.CommissionWithdrawLimit, err = store.ParseCurrencyAmount(raw)
				if err != nil {
					return errors.New("legacy commission withdrawal limit is not a non-negative amount with at most two decimal places")
				}
			case "commission_withdraw_method":
				decoder := json.NewDecoder(bytes.NewBufferString(raw))
				if err := decoder.Decode(&settings.CommissionWithdrawMethod); err != nil {
					return errors.New("legacy commission withdrawal methods are not a JSON string array")
				}
				if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
					return errors.New("legacy commission withdrawal methods contain trailing data")
				}
			case "frontend_theme_sidebar":
				settings.SidebarStyle = raw
			case "frontend_theme_header":
				settings.HeaderStyle = raw
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy configuration compatibility settings: %w", err)
		}
		settings, err = store.NormalizeLegacyConfigurationCompatibilitySettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy configuration compatibility settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return ConfigurationCompatibilitySettingsSnapshot{}, err
	}
	return ConfigurationCompatibilitySettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyConfigurationCompatibilitySettingsChecksum(settings),
	}, nil
}
