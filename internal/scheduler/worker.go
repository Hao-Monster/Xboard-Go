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
