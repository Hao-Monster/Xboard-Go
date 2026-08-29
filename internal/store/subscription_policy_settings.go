package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type subscriptionPolicySettingsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) GetSubscriptionPolicySettings(ctx context.Context) (SubscriptionPolicySettings, error) {
	settings, err := readSubscriptionPolicySettings(ctx, s.db)
	if err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("get subscription policy settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateSubscriptionPolicySettings(ctx context.Context, administratorID, revision int64, input SaveSubscriptionPolicySettingsInput, now time.Time) (SubscriptionPolicySettings, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 || !validSubscriptionPolicySettings(input) {
		return SubscriptionPolicySettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("begin subscription policy settings update: %w", err)
	}
	defer tx.Rollback()
	var currentResetTrafficMethod int
	if err := tx.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&currentResetTrafficMethod); err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("read current subscription policy settings: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET plan_change_enable = ?, traffic_reset_method = ?, surplus_enable = ?,
		    new_order_event_id = ?, renew_order_event_id = ?, change_order_event_id = ?,
		    default_remind_expire = ?, default_remind_traffic = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, input.PlanChangeEnabled, input.ResetTrafficMethod, input.SurplusEnabled,
		input.NewOrderEventID, input.RenewOrderEventID, input.ChangeOrderEventID,
		input.DefaultRemindExpire, input.DefaultRemindTraffic, administratorID, now.Unix(), revision)
	if err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("update subscription policy settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("count subscription policy settings update: %w", err)
	}
	if updated != 1 {
		return SubscriptionPolicySettings{}, ErrConflict
	}
	if input.ResetTrafficMethod != currentResetTrafficMethod {
		if err := rescheduleSystemTrafficResetUsers(ctx, tx, input.ResetTrafficMethod, now); err != nil {
			return SubscriptionPolicySettings{}, err
		}
	}
	settings, err := readSubscriptionPolicySettings(ctx, tx)
	if err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("read updated subscription policy settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SubscriptionPolicySettings{}, fmt.Errorf("commit subscription policy settings update: %w", err)
	}
	return settings, nil
}

func (s *Store) GetLegacyAdminSubscriptionConfig(ctx context.Context) (LegacyAdminSubscriptionConfig, error) {
	config, err := readLegacyAdminSubscriptionConfig(ctx, s.db)
	if err != nil {
		return LegacyAdminSubscriptionConfig{}, fmt.Errorf("get legacy subscription config: %w", err)
	}
	return config, nil
}

// UpdateLegacyAdminSubscriptionConfig accepts the old partial config contract but
// applies every touched settings row in one SQLite transaction. Untouched
// fields are merged from the same snapshot so an old client cannot erase a
// setting introduced by a newer client.
func (s *Store) UpdateLegacyAdminSubscriptionConfig(ctx context.Context, administratorID int64, input SaveLegacyAdminSubscriptionConfigInput, now time.Time) (LegacyAdminSubscriptionConfig, error) {
	policyTouched := input.PlanChangeEnabled != nil || input.ResetTrafficMethod != nil || input.SurplusEnabled != nil ||
		input.NewOrderEventID != nil || input.RenewOrderEventID != nil || input.ChangeOrderEventID != nil ||
		input.DefaultRemindExpire != nil || input.DefaultRemindTraffic != nil
	outputTouched := input.ShowInfo != nil || input.ShowProtocol != nil || input.Path != nil
	if administratorID < 1 || now.Unix() < 0 || (!policyTouched && !outputTouched) {
		return LegacyAdminSubscriptionConfig{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyAdminSubscriptionConfig{}, fmt.Errorf("begin legacy subscription config update: %w", err)
	}
	defer tx.Rollback()
	current, err := readLegacyAdminSubscriptionConfig(ctx, tx)
	if err != nil {
		return LegacyAdminSubscriptionConfig{}, fmt.Errorf("read legacy subscription config: %w", err)
	}
	merged := mergeLegacySubscriptionConfig(current, input)
	if !validLegacySubscriptionConfig(merged) {
		return LegacyAdminSubscriptionConfig{}, ErrInvalidInput
	}
	if policyTouched {
		result, err := tx.ExecContext(ctx, `
			UPDATE app_settings
			SET plan_change_enable = ?, traffic_reset_method = ?, surplus_enable = ?,
			    new_order_event_id = ?, renew_order_event_id = ?, change_order_event_id = ?,
			    default_remind_expire = ?, default_remind_traffic = ?,
			    updated_by = ?, updated_at = ?, revision = revision + 1
			WHERE id = 1
		`, merged.PlanChangeEnabled, merged.ResetTrafficMethod, merged.SurplusEnabled,
			merged.NewOrderEventID, merged.RenewOrderEventID, merged.ChangeOrderEventID,
			merged.DefaultRemindExpire, merged.DefaultRemindTraffic, administratorID, now.Unix())
		if err != nil {
			return LegacyAdminSubscriptionConfig{}, fmt.Errorf("update legacy subscription policy config: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return LegacyAdminSubscriptionConfig{}, fmt.Errorf("count legacy subscription policy update: %w", err)
		}
		if count != 1 {
			return LegacyAdminSubscriptionConfig{}, ErrConflict
		}
		if merged.ResetTrafficMethod != current.ResetTrafficMethod {
			if err := rescheduleSystemTrafficResetUsers(ctx, tx, merged.ResetTrafficMethod, now); err != nil {
				return LegacyAdminSubscriptionConfig{}, err
			}
		}
	}
	if outputTouched {
		result, err := tx.ExecContext(ctx, `
			UPDATE subscription_settings
			SET path = ?, show_info = ?, show_protocol = ?, updated_by = ?, updated_at = ?, revision = revision + 1
			WHERE id = 1
		`, merged.Path, merged.ShowInfo, merged.ShowProtocol, administratorID, now.Unix())
		if err != nil {
			return LegacyAdminSubscriptionConfig{}, fmt.Errorf("update legacy subscription output config: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return LegacyAdminSubscriptionConfig{}, fmt.Errorf("count legacy subscription output update: %w", err)
		}
		if count != 1 {
			return LegacyAdminSubscriptionConfig{}, ErrConflict
		}
	}
	updated, err := readLegacyAdminSubscriptionConfig(ctx, tx)
	if err != nil {
		return LegacyAdminSubscriptionConfig{}, fmt.Errorf("read updated legacy subscription config: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyAdminSubscriptionConfig{}, fmt.Errorf("commit legacy subscription config update: %w", err)
	}
	return updated, nil
}

func readSubscriptionPolicySettings(ctx context.Context, query subscriptionPolicySettingsQuery) (SubscriptionPolicySettings, error) {
	var settings SubscriptionPolicySettings
	var updatedAt int64
	err := query.QueryRowContext(ctx, `
		SELECT revision, plan_change_enable, traffic_reset_method, surplus_enable,
		       new_order_event_id, renew_order_event_id, change_order_event_id,
		       default_remind_expire, default_remind_traffic, updated_at
		FROM app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.PlanChangeEnabled, &settings.ResetTrafficMethod, &settings.SurplusEnabled,
		&settings.NewOrderEventID, &settings.RenewOrderEventID, &settings.ChangeOrderEventID,
		&settings.DefaultRemindExpire, &settings.DefaultRemindTraffic, &updatedAt)
	if err != nil {
		return SubscriptionPolicySettings{}, err
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func readLegacyAdminSubscriptionConfig(ctx context.Context, query subscriptionPolicySettingsQuery) (LegacyAdminSubscriptionConfig, error) {
	var config LegacyAdminSubscriptionConfig
	err := query.QueryRowContext(ctx, `
		SELECT a.plan_change_enable, a.traffic_reset_method, a.surplus_enable,
		       a.new_order_event_id, a.renew_order_event_id, a.change_order_event_id,
		       s.show_info, s.show_protocol, a.default_remind_expire, a.default_remind_traffic, s.path
		FROM app_settings a CROSS JOIN subscription_settings s
		WHERE a.id = 1 AND s.id = 1
	`).Scan(&config.PlanChangeEnabled, &config.ResetTrafficMethod, &config.SurplusEnabled,
		&config.NewOrderEventID, &config.RenewOrderEventID, &config.ChangeOrderEventID,
		&config.ShowInfo, &config.ShowProtocol, &config.DefaultRemindExpire, &config.DefaultRemindTraffic, &config.Path)
	if err != nil {
		return LegacyAdminSubscriptionConfig{}, err
	}
	return config, nil
}

func validSubscriptionPolicySettings(input SaveSubscriptionPolicySettingsInput) bool {
	return input.ResetTrafficMethod >= 0 && input.ResetTrafficMethod <= 4 &&
		validSubscriptionEventID(input.NewOrderEventID) && validSubscriptionEventID(input.RenewOrderEventID) &&
		validSubscriptionEventID(input.ChangeOrderEventID)
}

func validSubscriptionEventID(value int) bool { return value == 0 || value == 1 }

func validLegacySubscriptionConfig(config LegacyAdminSubscriptionConfig) bool {
	return validSubscriptionPolicySettings(SaveSubscriptionPolicySettingsInput{
		PlanChangeEnabled: config.PlanChangeEnabled, ResetTrafficMethod: config.ResetTrafficMethod,
		SurplusEnabled: config.SurplusEnabled, NewOrderEventID: config.NewOrderEventID,
		RenewOrderEventID: config.RenewOrderEventID, ChangeOrderEventID: config.ChangeOrderEventID,
		DefaultRemindExpire: config.DefaultRemindExpire, DefaultRemindTraffic: config.DefaultRemindTraffic,
	}) && subscriptionPathPattern.MatchString(config.Path)
}

func mergeLegacySubscriptionConfig(current LegacyAdminSubscriptionConfig, input SaveLegacyAdminSubscriptionConfigInput) LegacyAdminSubscriptionConfig {
	if input.PlanChangeEnabled != nil {
		current.PlanChangeEnabled = *input.PlanChangeEnabled
	}
	if input.ResetTrafficMethod != nil {
		current.ResetTrafficMethod = *input.ResetTrafficMethod
	}
	if input.SurplusEnabled != nil {
		current.SurplusEnabled = *input.SurplusEnabled
	}
	if input.NewOrderEventID != nil {
		current.NewOrderEventID = *input.NewOrderEventID
	}
	if input.RenewOrderEventID != nil {
		current.RenewOrderEventID = *input.RenewOrderEventID
	}
	if input.ChangeOrderEventID != nil {
		current.ChangeOrderEventID = *input.ChangeOrderEventID
	}
	if input.ShowInfo != nil {
		current.ShowInfo = *input.ShowInfo
	}
	if input.ShowProtocol != nil {
		current.ShowProtocol = *input.ShowProtocol
	}
	if input.DefaultRemindExpire != nil {
		current.DefaultRemindExpire = *input.DefaultRemindExpire
	}
	if input.DefaultRemindTraffic != nil {
		current.DefaultRemindTraffic = *input.DefaultRemindTraffic
	}
	if input.Path != nil {
		current.Path = strings.TrimSpace(*input.Path)
	}
	return current
}
