package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type Worker struct {
	store                      *store.Store
	interval                   time.Duration
	now                        func() time.Time
	logger                     *slog.Logger
	lastTicketSweep            time.Time
	lastIPSweep                time.Time
	lastPasswordResetSweep     time.Time
	lastRegistrationEmailSweep time.Time
	lastLoginLinkSweep         time.Time
	lastLoginFailureSweep      time.Time
	lastTrafficResetSweep      time.Time
	lastOrderSweep             time.Time
	lastCommissionSweep        time.Time
	tracker                    *operations.Tracker
}

func NewWorker(database *store.Store, interval time.Duration, logger *slog.Logger, trackers ...*operations.Tracker) *Worker {
	if interval <= 0 {
		interval = time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	var tracker *operations.Tracker
	if len(trackers) > 0 {
		tracker = trackers[0]
	}
	return &Worker{store: database, interval: interval, now: time.Now, logger: logger, tracker: tracker}
}

func (w *Worker) Run(ctx context.Context) {
	w.applyDue(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.applyDue(ctx)
		}
	}
}

func (w *Worker) applyDue(ctx context.Context) {
	now := w.now()
	if w.tracker != nil {
		defer func() { w.tracker.MarkSchedulerRun(w.now()) }()
	}
	w.closeStaleTickets(ctx, now)
	w.pruneRegistrationIPLimits(ctx, now)
	w.prunePasswordResets(ctx, now)
	w.pruneRegistrationEmailVerifications(ctx, now)
	w.pruneLoginLinks(ctx, now)
	w.pruneLoginFailures(ctx, now)
	w.resetDueTraffic(ctx, now)
	w.processOrders(ctx, now)
	w.processCommissions(ctx, now)
	due, err := w.store.ListDueSchedules(ctx, now, 100)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("list due activation schedules", "error", err)
		}
		return
	}
	for _, item := range due {
		applied, err := w.store.ApplyDueSchedule(ctx, item, now)
		if err != nil {
			w.logger.Error("apply activation schedule", "node_id", item.NodeID, "revision", item.Revision, "error", err)
			continue
		}
		if applied {
			w.logger.Info("activation schedule applied", "node_id", item.NodeID, "revision", item.Revision)
		}
	}
}

func (w *Worker) processCommissions(ctx context.Context, now time.Time) {
	// The legacy check:commission command runs every minute. Keep the same
	// cadence while the store transaction guarantees exactly-once payout.
	if !w.lastCommissionSweep.IsZero() && now.Sub(w.lastCommissionSweep) < time.Minute {
		return
	}
	w.lastCommissionSweep = now
	result, err := w.store.ProcessCommissions(ctx, now, 200)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("process invitation commissions", "error", err)
		}
		return
	}
	if result.Checked > 0 || result.Paid > 0 {
		w.logger.Info("invitation commissions processed", "checked", result.Checked, "paid", result.Paid, "remaining", result.Remaining)
	}
}

func (w *Worker) processOrders(ctx context.Context, now time.Time) {
	if !w.lastOrderSweep.IsZero() && now.Sub(w.lastOrderSweep) < time.Minute {
		return
	}
	w.lastOrderSweep = now
	result, err := w.store.ProcessStaleOrders(ctx, now, 200)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("process pending orders", "error", err)
		}
		return
	}
	if result.Cancelled > 0 || result.Completed > 0 {
		w.logger.Info("orders reconciled", "cancelled", result.Cancelled, "completed", result.Completed, "remaining", result.Remaining)
	}
}

func (w *Worker) resetDueTraffic(ctx context.Context, now time.Time) {
	// The legacy reset:traffic command runs every minute. Preserve that cadence
	// while keeping the scheduler's second-level activation transitions.
	if !w.lastTrafficResetSweep.IsZero() && now.Sub(w.lastTrafficResetSweep) < time.Minute {
		return
	}
	w.lastTrafficResetSweep = now
	result, err := w.store.ProcessDueTrafficResets(ctx, now, 100)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("reset due user traffic", "error", err)
		}
		return
	}
	if result.Processed > 0 {
		w.logger.Info("due user traffic reset", "processed", result.Processed, "remaining", result.Remaining)
	}
}

func (w *Worker) pruneLoginFailures(ctx context.Context, now time.Time) {
	if !w.lastLoginFailureSweep.IsZero() && now.Sub(w.lastLoginFailureSweep) < time.Minute {
		return
	}
	w.lastLoginFailureSweep = now
	removed, err := w.store.PruneExpiredLoginFailureLimits(ctx, now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("prune expired login failures", "error", err)
		}
		return
	}
	if removed > 0 {
		w.logger.Info("expired login failures pruned", "count", removed)
	}
}

func (w *Worker) pruneLoginLinks(ctx context.Context, now time.Time) {
	if !w.lastLoginLinkSweep.IsZero() && now.Sub(w.lastLoginLinkSweep) < time.Minute {
		return
	}
	w.lastLoginLinkSweep = now
	removed, err := w.store.PruneExpiredLoginLinks(ctx, now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("prune expired login links", "error", err)
		}
		return
	}
	if removed > 0 {
		w.logger.Info("expired login links pruned", "count", removed)
	}
}

func (w *Worker) pruneRegistrationEmailVerifications(ctx context.Context, now time.Time) {
	if !w.lastRegistrationEmailSweep.IsZero() && now.Sub(w.lastRegistrationEmailSweep) < time.Minute {
		return
	}
	w.lastRegistrationEmailSweep = now
	removed, err := w.store.PruneExpiredRegistrationEmailVerifications(ctx, now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("prune expired registration email verifications", "error", err)
		}
		return
	}
	if removed > 0 {
		w.logger.Info("expired registration email verifications pruned", "count", removed)
	}
}

func (w *Worker) prunePasswordResets(ctx context.Context, now time.Time) {
	if !w.lastPasswordResetSweep.IsZero() && now.Sub(w.lastPasswordResetSweep) < time.Minute {
		return
	}
	w.lastPasswordResetSweep = now
	removed, err := w.store.PruneExpiredPasswordResets(ctx, now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("prune expired password resets", "error", err)
		}
		return
	}
	if removed > 0 {
		w.logger.Info("expired password resets pruned", "count", removed)
	}
}

func (w *Worker) pruneRegistrationIPLimits(ctx context.Context, now time.Time) {
	if !w.lastIPSweep.IsZero() && now.Sub(w.lastIPSweep) < time.Minute {
		return
	}
	w.lastIPSweep = now
	removed, err := w.store.PruneExpiredRegistrationIPLimits(ctx, now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("prune expired registration IP limits", "error", err)
		}
		return
	}
	if removed > 0 {
		w.logger.Info("expired registration IP limits pruned", "count", removed)
	}
}

func (w *Worker) closeStaleTickets(ctx context.Context, now time.Time) {
	if !w.lastTicketSweep.IsZero() && now.Sub(w.lastTicketSweep) < time.Minute {
		return
	}
	w.lastTicketSweep = now
	closed, err := w.store.CloseStaleAnsweredTickets(ctx, now.Add(-24*time.Hour), now, 1_000)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("close stale answered tickets", "error", err)
		}
		return
	}
	if closed > 0 {
		w.logger.Info("stale answered tickets closed", "count", closed)
	}
}
