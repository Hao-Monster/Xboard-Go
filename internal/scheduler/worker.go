package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type Worker struct {
	store           *store.Store
	interval        time.Duration
	now             func() time.Time
	logger          *slog.Logger
	lastTicketSweep time.Time
	tracker         *operations.Tracker
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
