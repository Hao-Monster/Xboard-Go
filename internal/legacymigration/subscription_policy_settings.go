package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const legacySubscriptionPolicySettingsKeys = `'plan_change_enable','surplus_enable','new_order_event_id','renew_order_event_id',
	'change_order_event_id','default_remind_expire','default_remind_traffic'`

type SubscriptionPolicySettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacySubscriptionPolicySettings
	Checksum string
}

func ReadSubscriptionPolicySettingsSnapshot(ctx context.Context, sourcePath string) (SubscriptionPolicySettingsSnapshot, error) {
	settings := store.DefaultLegacySubscriptionPolicySettings()
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		budgetQuery := `SELECT COUNT(*),COALESCE(SUM(length(CAST(name AS BLOB))+COALESCE(length(CAST(value AS BLOB)),0)),0)
			FROM v2_settings WHERE name IN (` + legacySubscriptionPolicySettingsKeys + `)`
		if err := validateLegacyQueryBudget(ctx, database, budgetQuery, 7, 16<<10, "legacy subscription policy settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,CAST(value AS BLOB),value IS NULL FROM v2_settings WHERE name IN (`+legacySubscriptionPolicySettingsKeys+`) ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy subscription policy settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 7)
		for rows.Next() {
			var name string
			var raw []byte
			var isNull bool
			if err := rows.Scan(&name, &raw, &isNull); err != nil {
				return fmt.Errorf("scan legacy subscription policy setting: %w", err)
			}
			if !isNull && raw == nil {
				raw = []byte{}
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy subscription policy settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			if err := applyLegacySubscriptionPolicySetting(name, raw, &settings); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy subscription policy settings: %w", err)
		}
		settings, err = store.NormalizeLegacySubscriptionPolicySettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy subscription policy settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return SubscriptionPolicySettingsSnapshot{}, err
	}
	return SubscriptionPolicySettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacySubscriptionPolicySettingsChecksum(settings),
	}, nil
}

func applyLegacySubscriptionPolicySetting(name string, raw []byte, settings *store.LegacySubscriptionPolicySettings) error {
	if settings == nil {
		return errors.New("legacy subscription policy settings destination is unavailable")
	}
	var err error
	switch name {
	case "plan_change_enable":
		settings.PlanChangeEnabled, err = parseLegacyPolicyBoolean(raw, settings.PlanChangeEnabled)
	case "surplus_enable":
		settings.SurplusEnabled, err = parseLegacyPolicyBoolean(raw, settings.SurplusEnabled)
	case "new_order_event_id":
		settings.NewOrderEventID, err = parseLegacyPolicyInteger(raw, settings.NewOrderEventID)
	case "renew_order_event_id":
		settings.RenewOrderEventID, err = parseLegacyPolicyInteger(raw, settings.RenewOrderEventID)
	case "change_order_event_id":
		settings.ChangeOrderEventID, err = parseLegacyPolicyInteger(raw, settings.ChangeOrderEventID)
	case "default_remind_expire":
		settings.DefaultRemindExpire, err = parseLegacyPolicyBoolean(raw, settings.DefaultRemindExpire)
	case "default_remind_traffic":
		settings.DefaultRemindTraffic, err = parseLegacyPolicyBoolean(raw, settings.DefaultRemindTraffic)
	default:
		return fmt.Errorf("unsupported legacy subscription policy setting %q", name)
	}
	if err != nil {
		return fmt.Errorf("validate legacy %s: %w", name, err)
	}
	return nil
}
