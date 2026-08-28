package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type userPlanEntitlement struct {
	planID         int64
	groupID        *int64
	transferEnable int64
	speedLimit     int
	deviceLimit    int
	resetMethod    *int
}

type trialPlanQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readUserPlanEntitlement(ctx context.Context, query trialPlanQueryer, planID int64) (userPlanEntitlement, bool, error) {
	var planGroup, planSpeed, planDevices, planReset sql.NullInt64
	var planTransferGiB int64
	err := query.QueryRowContext(ctx, `
		SELECT group_id, transfer_enable_gib, speed_limit, device_limit, reset_traffic_method
		FROM plans WHERE id = ?
	`, planID).Scan(&planGroup, &planTransferGiB, &planSpeed, &planDevices, &planReset)
	if errors.Is(err, sql.ErrNoRows) {
		return userPlanEntitlement{}, false, nil
	}
	if err != nil {
		return userPlanEntitlement{}, false, fmt.Errorf("read plan entitlement: %w", err)
	}
	return userPlanEntitlement{
		planID: planID, groupID: nullableInt64Pointer(planGroup), transferEnable: planTransferGiB * bytesPerGiB,
		speedLimit: optionalAdminUserLimit(planSpeed), deviceLimit: optionalAdminUserLimit(planDevices),
		resetMethod: nullableIntPointer(planReset),
	}, true, nil
}

type registrationTrial struct {
	entitlement userPlanEntitlement
	expiredAt   time.Time
	nextResetAt *time.Time
}

func resolveRegistrationTrial(ctx context.Context, query trialPlanQueryer, planID int64, hours, systemResetMethod int, now time.Time) (*registrationTrial, error) {
	if planID == 0 {
		return nil, nil
	}
	entitlement, exists, err := readUserPlanEntitlement(ctx, query, planID)
	if err != nil {
		return nil, err
	}
	if !exists {
		// Legacy runtime semantics treated a dangling configured plan as disabled.
		// New writes and plan deletion prevent this state from being introduced.
		return nil, nil
	}
	expiredAt := now.Add(time.Duration(hours) * time.Hour)
	return &registrationTrial{
		entitlement: entitlement,
		expiredAt:   expiredAt,
		nextResetAt: CalculateNextTrafficReset(entitlement.resetMethod, systemResetMethod, &expiredAt, now),
	}, nil
}
