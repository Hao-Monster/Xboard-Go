package operations

import (
	"sync/atomic"
	"time"
)

type ComponentStatus struct {
	Healthy   bool       `json:"healthy"`
	LastRunAt *time.Time `json:"last_run_at"`
}

type SubscriptionLoad struct {
	InFlight     int64  `json:"in_flight"`
	PeakInFlight int64  `json:"peak_in_flight"`
	RateLimited  uint64 `json:"rate_limited"`
	Busy         uint64 `json:"busy"`
}

type Snapshot struct {
	StartedAt    time.Time        `json:"started_at"`
	Uptime       time.Duration    `json:"-"`
	Scheduler    ComponentStatus  `json:"scheduler"`
	MailWorker   ComponentStatus  `json:"mail_worker"`
	Subscription SubscriptionLoad `json:"subscription"`
}

type Tracker struct {
	startedAt                time.Time
	schedulerLastRun         atomic.Int64
	mailLastRun              atomic.Int64
	subscriptionInFlight     atomic.Int64
	subscriptionPeakInFlight atomic.Int64
	subscriptionRateLimited  atomic.Uint64
	subscriptionBusy         atomic.Uint64
}

func (t *Tracker) BeginSubscriptionRender() {
	if t == nil {
		return
	}
	current := t.subscriptionInFlight.Add(1)
	for peak := t.subscriptionPeakInFlight.Load(); current > peak; peak = t.subscriptionPeakInFlight.Load() {
		if t.subscriptionPeakInFlight.CompareAndSwap(peak, current) {
			break
		}
	}
}

func (t *Tracker) EndSubscriptionRender() {
	if t != nil {
		t.subscriptionInFlight.Add(-1)
	}
}

func (t *Tracker) MarkSubscriptionRateLimited() {
	if t != nil {
		t.subscriptionRateLimited.Add(1)
	}
}

func (t *Tracker) MarkSubscriptionBusy() {
	if t != nil {
		t.subscriptionBusy.Add(1)
	}
}

func NewTracker(startedAt time.Time) *Tracker {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	return &Tracker{startedAt: startedAt.UTC()}
}

func (t *Tracker) MarkSchedulerRun(now time.Time) {
	if t != nil && !now.IsZero() {
		t.schedulerLastRun.Store(now.UnixNano())
	}
}

func (t *Tracker) MarkMailRun(now time.Time) {
	if t != nil && !now.IsZero() {
		t.mailLastRun.Store(now.UnixNano())
	}
}

func (t *Tracker) Snapshot(now time.Time, healthyWithin time.Duration) Snapshot {
	if t == nil {
		return Snapshot{}
	}
	if now.IsZero() {
		now = time.Now()
	}
	if healthyWithin <= 0 {
		healthyWithin = 2 * time.Minute
	}
	now = now.UTC()
	uptime := now.Sub(t.startedAt)
	if uptime < 0 {
		uptime = 0
	}
	return Snapshot{
		StartedAt:  t.startedAt,
		Uptime:     uptime,
		Scheduler:  componentSnapshot(t.schedulerLastRun.Load(), now, healthyWithin),
		MailWorker: componentSnapshot(t.mailLastRun.Load(), now, healthyWithin),
		Subscription: SubscriptionLoad{
			InFlight: t.subscriptionInFlight.Load(), PeakInFlight: t.subscriptionPeakInFlight.Load(),
			RateLimited: t.subscriptionRateLimited.Load(), Busy: t.subscriptionBusy.Load(),
		},
	}
}

func componentSnapshot(timestamp int64, now time.Time, healthyWithin time.Duration) ComponentStatus {
	if timestamp == 0 {
		return ComponentStatus{}
	}
	lastRun := time.Unix(0, timestamp).UTC()
	age := now.Sub(lastRun)
	return ComponentStatus{Healthy: age >= 0 && age <= healthyWithin, LastRunAt: &lastRun}
}
