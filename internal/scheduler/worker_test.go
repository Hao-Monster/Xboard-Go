package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestWorkerAppliesPersistedDueSchedule(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	location, _ := time.LoadLocation("Asia/Singapore")
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, location)
	machine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "worker-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{Name: "worker-node", Type: "vless", Host: "worker.example.test", Port: "443", Show: true, MachineID: &machine.ID}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	saved, err := database.SaveDailySchedule(ctx, node.ID, "Asia/Singapore", "19:00", "01:00", now)
	if err != nil {
		t.Fatalf("SaveDailySchedule() error = %v", err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return saved.NextTransitionAt }
	worker.applyDue(ctx)

	updated, err := database.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if !updated.Enabled {
		t.Fatal("due worker did not enable the node")
	}
	advanced, err := database.GetActivationSchedule(ctx, node.ID)
	if err != nil {
		t.Fatalf("GetActivationSchedule() error = %v", err)
	}
	if !advanced.NextTransitionAt.After(saved.NextTransitionAt) {
		t.Fatalf("next transition = %s, want after %s", advanced.NextTransitionAt, saved.NextTransitionAt)
	}
}
