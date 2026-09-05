package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestNodeAuthTelemetryIsBoundedPersistentAndAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	observedSince := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if err := database.EnsureNodeAuthTelemetry(ctx, observedSince); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureNodeAuthTelemetry(ctx, observedSince.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	legacyUsedAt := observedSince.Add(10 * time.Minute)
	machineUsedAt := observedSince.Add(20 * time.Minute)
	if err := database.AddNodeAuthUsage(ctx, []NodeAuthUsageIncrement{
		{AuthKind: NodeAuthKindLegacyGlobalToken, Transport: NodeAuthTransportHTTP, SuccessCount: 3, LastUsedAt: legacyUsedAt},
		{AuthKind: NodeAuthKindLegacyGlobalToken, Transport: NodeAuthTransportWebSocket, SuccessCount: 2, LastUsedAt: legacyUsedAt.Add(-time.Minute)},
		{AuthKind: NodeAuthKindMachineCredential, Transport: NodeAuthTransportHTTP, SuccessCount: 7, LastUsedAt: machineUsedAt.Add(-time.Minute)},
		{AuthKind: NodeAuthKindMachineCredential, Transport: NodeAuthTransportWebSocket, SuccessCount: 5, LastUsedAt: machineUsedAt},
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := database.GetNodeAuthTelemetry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.ObservedSince.Equal(observedSince) || snapshot.LegacyGlobalToken.HTTPAuthSuccess != 3 ||
		snapshot.LegacyGlobalToken.WebSocketAuthSuccess != 2 || snapshot.LegacyGlobalToken.LastUsedAt == nil ||
		!snapshot.LegacyGlobalToken.LastUsedAt.Equal(legacyUsedAt) || snapshot.MachineCredential.HTTPAuthSuccess != 7 ||
		snapshot.MachineCredential.WebSocketAuthSuccess != 5 || snapshot.MachineCredential.LastUsedAt == nil ||
		!snapshot.MachineCredential.LastUsedAt.Equal(machineUsedAt) {
		t.Fatalf("unexpected telemetry snapshot: %#v", snapshot)
	}
	var rows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_auth_telemetry`).Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("telemetry rows=%d err=%v, want exactly four bounded aggregates", rows, err)
	}

	if _, err := database.db.ExecContext(ctx, `
		UPDATE node_auth_telemetry SET success_count = ?
		WHERE auth_kind = ? AND transport = ?
	`, int64(math.MaxInt64), NodeAuthKindLegacyGlobalToken, NodeAuthTransportHTTP); err != nil {
		t.Fatal(err)
	}
	err = database.AddNodeAuthUsage(ctx, []NodeAuthUsageIncrement{
		{AuthKind: NodeAuthKindMachineCredential, Transport: NodeAuthTransportHTTP, SuccessCount: 1, LastUsedAt: machineUsedAt.Add(time.Minute)},
		{AuthKind: NodeAuthKindLegacyGlobalToken, Transport: NodeAuthTransportHTTP, SuccessCount: 1, LastUsedAt: legacyUsedAt.Add(time.Minute)},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("overflow error=%v, want ErrInvalidInput", err)
	}
	rolledBack, err := database.GetNodeAuthTelemetry(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.MachineCredential.HTTPAuthSuccess != 7 {
		t.Fatalf("partial batch survived overflow rollback: %#v", rolledBack.MachineCredential)
	}

	for _, invalid := range []NodeAuthUsageIncrement{
		{AuthKind: "attacker", Transport: NodeAuthTransportHTTP, SuccessCount: 1, LastUsedAt: observedSince},
		{AuthKind: NodeAuthKindLegacyGlobalToken, Transport: "raw-token", SuccessCount: 1, LastUsedAt: observedSince},
		{AuthKind: NodeAuthKindLegacyGlobalToken, Transport: NodeAuthTransportHTTP, SuccessCount: 0, LastUsedAt: observedSince},
	} {
		if err := database.AddNodeAuthUsage(ctx, []NodeAuthUsageIncrement{invalid}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid increment %#v error=%v, want ErrInvalidInput", invalid, err)
		}
	}
	duplicate := NodeAuthUsageIncrement{AuthKind: NodeAuthKindMachineCredential, Transport: NodeAuthTransportHTTP, SuccessCount: 1, LastUsedAt: observedSince}
	if err := database.AddNodeAuthUsage(ctx, []NodeAuthUsageIncrement{duplicate, duplicate}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate aggregate error=%v, want ErrInvalidInput", err)
	}
}

func TestSchemaV60MigratesAndValidatesNodeAuthTelemetry(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE node_auth_telemetry;
		DROP TABLE node_auth_telemetry_state;
		PRAGMA user_version = 59;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureNodeAuthTelemetry(ctx, time.Unix(1_800_000_000, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := database.GetNodeAuthTelemetry(ctx); err != nil || snapshot.ObservedSince.Unix() != 1_800_000_000 {
		t.Fatalf("migrated telemetry=%#v err=%v", snapshot, err)
	}
}

func TestSchemaV60RejectsLooseNodeAuthTelemetryTable(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		ALTER TABLE node_auth_telemetry RENAME TO valid_node_auth_telemetry;
		CREATE TABLE node_auth_telemetry (
			auth_kind TEXT, transport TEXT, success_count INTEGER, last_used_at INTEGER
		);
		DROP TABLE valid_node_auth_telemetry;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err == nil {
		t.Fatal("Migrate() accepted a loose high-cardinality node authentication telemetry table")
	}
}
