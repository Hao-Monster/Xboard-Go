package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type commissionSettingsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) GetCommissionSettings(ctx context.Context) (CommissionSettings, error) {
	settings, err := readCommissionSettings(ctx, s.db)
	if err != nil {
		return CommissionSettings{}, fmt.Errorf("get commission settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateCommissionSettings(ctx context.Context, administratorID, revision int64, input SaveCommissionSettingsInput, now time.Time) (CommissionSettings, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 || !validCommissionSettings(input) {
		return CommissionSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommissionSettings{}, fmt.Errorf("begin commission settings update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings
		SET invite_commission = ?, commission_first_time_enable = ?, commission_auto_check_enable = ?,
		    withdraw_close_enable = ?, commission_distribution_enable = ?, commission_distribution_l1 = ?,
		    commission_distribution_l2 = ?, commission_distribution_l3 = ?, updated_by = ?, updated_at = ?,
		    revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, input.InviteCommission, input.FirstTimeEnabled, input.AutoCheckEnabled,
		input.WithdrawClosed, input.DistributionEnabled, input.DistributionL1,
		input.DistributionL2, input.DistributionL3, administratorID, now.Unix(), revision)
	if err != nil {
		return CommissionSettings{}, fmt.Errorf("update commission settings: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return CommissionSettings{}, fmt.Errorf("count commission settings update: %w", err)
	}
	if updated != 1 {
		return CommissionSettings{}, ErrConflict
	}
	settings, err := readCommissionSettings(ctx, tx)
	if err != nil {
		return CommissionSettings{}, fmt.Errorf("read updated commission settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommissionSettings{}, fmt.Errorf("commit commission settings update: %w", err)
	}
	return settings, nil
}

func validCommissionSettings(input SaveCommissionSettingsInput) bool {
	percentages := [...]int{
		input.InviteCommission,
		input.DistributionL1,
		input.DistributionL2,
		input.DistributionL3,
	}
	for _, percentage := range percentages {
		if percentage < 0 || percentage > 100 {
			return false
		}
	}
	return input.DistributionL1+input.DistributionL2+input.DistributionL3 <= 100
}

func readCommissionSettings(ctx context.Context, query commissionSettingsQuery) (CommissionSettings, error) {
	var settings CommissionSettings
	var updatedAt int64
	err := query.QueryRowContext(ctx, `
		SELECT revision, invite_commission, commission_first_time_enable, commission_auto_check_enable,
		       withdraw_close_enable, commission_distribution_enable, commission_distribution_l1,
		       commission_distribution_l2, commission_distribution_l3, updated_at
		FROM app_settings WHERE id = 1
	`).Scan(
		&settings.Revision, &settings.InviteCommission, &settings.FirstTimeEnabled, &settings.AutoCheckEnabled,
		&settings.WithdrawClosed, &settings.DistributionEnabled, &settings.DistributionL1,
		&settings.DistributionL2, &settings.DistributionL3, &updatedAt,
	)
	if err != nil {
		return CommissionSettings{}, err
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}
