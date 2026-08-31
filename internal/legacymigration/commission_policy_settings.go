package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const legacyCommissionPolicySettingsKeys = `'invite_commission','commission_first_time_enable','commission_auto_check_enable',
	'withdraw_close_enable','commission_distribution_enable','commission_distribution_l1',
	'commission_distribution_l2','commission_distribution_l3'`

type CommissionPolicySettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyCommissionPolicySettings
	Checksum string
}

func ReadCommissionPolicySettingsSnapshot(ctx context.Context, sourcePath string) (CommissionPolicySettingsSnapshot, error) {
	settings := store.DefaultLegacyCommissionPolicySettings()
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		budgetQuery := `SELECT COUNT(*),COALESCE(SUM(length(CAST(name AS BLOB))+COALESCE(length(CAST(value AS BLOB)),0)),0)
			FROM v2_settings WHERE name IN (` + legacyCommissionPolicySettingsKeys + `)`
		if err := validateLegacyQueryBudget(ctx, database, budgetQuery, 8, 16<<10, "legacy commission policy settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,CAST(value AS BLOB),value IS NULL FROM v2_settings WHERE name IN (`+legacyCommissionPolicySettingsKeys+`) ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy commission policy settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 8)
		for rows.Next() {
			var name string
			var raw []byte
			var isNull bool
			if err := rows.Scan(&name, &raw, &isNull); err != nil {
				return fmt.Errorf("scan legacy commission policy setting: %w", err)
			}
			if !isNull && raw == nil {
				raw = []byte{}
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy commission policy settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			if err := applyLegacyCommissionPolicySetting(name, raw, &settings); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy commission policy settings: %w", err)
		}
		settings, err = store.NormalizeLegacyCommissionPolicySettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy commission policy settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return CommissionPolicySettingsSnapshot{}, err
	}
	return CommissionPolicySettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyCommissionPolicySettingsChecksum(settings),
	}, nil
}

func applyLegacyCommissionPolicySetting(name string, raw []byte, settings *store.LegacyCommissionPolicySettings) error {
	if settings == nil {
		return errors.New("legacy commission policy settings destination is unavailable")
	}
	var err error
	switch name {
	case "invite_commission":
		settings.InviteCommission, err = parseLegacyPolicyInteger(raw, settings.InviteCommission)
	case "commission_first_time_enable":
		settings.FirstTimeEnabled, err = parseLegacyPolicyBoolean(raw, settings.FirstTimeEnabled)
	case "commission_auto_check_enable":
		settings.AutoCheckEnabled, err = parseLegacyPolicyBoolean(raw, settings.AutoCheckEnabled)
	case "withdraw_close_enable":
		settings.WithdrawClosed, err = parseLegacyPolicyBoolean(raw, settings.WithdrawClosed)
	case "commission_distribution_enable":
		settings.DistributionEnabled, err = parseLegacyPolicyBoolean(raw, settings.DistributionEnabled)
	case "commission_distribution_l1":
		settings.DistributionL1, err = parseLegacyPolicyInteger(raw, settings.DistributionL1)
	case "commission_distribution_l2":
		settings.DistributionL2, err = parseLegacyPolicyInteger(raw, settings.DistributionL2)
	case "commission_distribution_l3":
		settings.DistributionL3, err = parseLegacyPolicyInteger(raw, settings.DistributionL3)
	default:
		return fmt.Errorf("unsupported legacy commission policy setting %q", name)
	}
	if err != nil {
		return fmt.Errorf("validate legacy %s: %w", name, err)
	}
	return nil
}
