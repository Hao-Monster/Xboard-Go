package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	DefaultCleanupLimit = 1_000
	MaxCleanupLimit     = 1_000
	staleTicketAge      = 24 * time.Hour
)

type CleanupResult struct {
	StaleTicketsClosed                   int64 `json:"stale_tickets_closed"`
	RegistrationIPLimitsPruned           int64 `json:"registration_ip_limits_pruned"`
	PasswordResetsPruned                 int64 `json:"password_resets_pruned"`
	RegistrationEmailVerificationsPruned int64 `json:"registration_email_verifications_pruned"`
	LoginLinksPruned                     int64 `json:"login_links_pruned"`
	LoginFailureLimitsPruned             int64 `json:"login_failure_limits_pruned"`
}

type cleanupStore interface {
	CloseStaleAnsweredTickets(context.Context, time.Time, time.Time, int) (int64, error)
	PruneExpiredRegistrationIPLimits(context.Context, time.Time, int) (int64, error)
	PruneExpiredPasswordResets(context.Context, time.Time, int) (int64, error)
	PruneExpiredRegistrationEmailVerifications(context.Context, time.Time, int) (int64, error)
	PruneExpiredLoginLinks(context.Context, time.Time, int) (int64, error)
	PruneExpiredLoginFailureLimits(context.Context, time.Time, int) (int64, error)
}

func CleanupExpired(ctx context.Context, database cleanupStore, now time.Time, limit int) (CleanupResult, error) {
	if err := ctx.Err(); err != nil {
		return CleanupResult{}, err
	}
	if database == nil || now.IsZero() || limit < 1 || limit > MaxCleanupLimit {
		return CleanupResult{}, errors.New("cleanup requires a database, current time, and limit between 1 and 1000")
	}

	var result CleanupResult
	var err error
	result.StaleTicketsClosed, err = database.CloseStaleAnsweredTickets(ctx, now.Add(-staleTicketAge), now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("close stale answered tickets: %w", err)
	}
	result.RegistrationIPLimitsPruned, err = database.PruneExpiredRegistrationIPLimits(ctx, now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired registration IP limits: %w", err)
	}
	result.PasswordResetsPruned, err = database.PruneExpiredPasswordResets(ctx, now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired password resets: %w", err)
	}
	result.RegistrationEmailVerificationsPruned, err = database.PruneExpiredRegistrationEmailVerifications(ctx, now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired registration email verifications: %w", err)
	}
	result.LoginLinksPruned, err = database.PruneExpiredLoginLinks(ctx, now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired login links: %w", err)
	}
	result.LoginFailureLimitsPruned, err = database.PruneExpiredLoginFailureLimits(ctx, now, limit)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired login failure limits: %w", err)
	}
	return result, nil
}
