package operations

import (
	"testing"
	"time"
)

func TestTrackerReportsFreshAndStaleWorkerHeartbeats(t *testing.T) {
	started := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	tracker := NewTracker(started)
	initial := tracker.Snapshot(started.Add(time.Second), 2*time.Minute)
	if initial.Scheduler.Healthy || initial.MailWorker.Healthy || initial.Uptime != time.Second {
		t.Fatalf("initial snapshot = %#v", initial)
	}
	tracker.MarkSchedulerRun(started.Add(2 * time.Second))
	tracker.MarkMailRun(started.Add(3 * time.Second))
	fresh := tracker.Snapshot(started.Add(time.Minute), 2*time.Minute)
	if !fresh.Scheduler.Healthy || !fresh.MailWorker.Healthy || fresh.Scheduler.LastRunAt == nil || fresh.MailWorker.LastRunAt == nil {
		t.Fatalf("fresh snapshot = %#v", fresh)
	}
	stale := tracker.Snapshot(started.Add(4*time.Minute), 2*time.Minute)
	if stale.Scheduler.Healthy || stale.MailWorker.Healthy {
		t.Fatalf("stale snapshot = %#v", stale)
	}
}
