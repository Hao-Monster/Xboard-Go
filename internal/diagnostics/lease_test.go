package diagnostics_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
)

func TestStartSessionLifecycle(t *testing.T) {
	service := diagnostics.NewMemoryLeaseService()
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	input := diagnostics.StartSessionInput{
		DiagnosticID:   "111111111111111111111111",
		OperatorID:     1,
		IdempotencyKey: "key-1",
		TargetNodeIDs:  []int64{101, 102},
		Now:            now,
	}

	// 1. Initial Start
	sess, isNew, err := service.StartSession(ctx, input)
	if err != nil {
		t.Fatalf("start session failed: %v", err)
	}
	if !isNew {
		t.Error("expected isNew=true")
	}
	if sess.State != diagnostics.SessionStatePending {
		t.Errorf("expected pending state, got %s", sess.State)
	}
	if sess.Revision != 1 {
		t.Errorf("expected revision 1, got %d", sess.Revision)
	}
	if !sess.ExpiresAt.Equal(now.Add(diagnostics.MaxSessionDuration)) {
		t.Errorf("expected expires_at to be exactly +15m, got %v", sess.ExpiresAt)
	}

	// 2. Repeat Start for same customer (should return existing session without extending expires_at)
	input2 := input
	input2.Now = now.Add(5 * time.Minute)
	input2.IdempotencyKey = "key-2" // Different idempotency key, same customer
	sess2, isNew2, err := service.StartSession(ctx, input2)
	if err != nil {
		t.Fatalf("repeat start failed: %v", err)
	}
	if isNew2 {
		t.Error("expected isNew=false for duplicate customer session")
	}
	if sess2.SessionID != sess.SessionID {
		t.Errorf("expected same session ID %s, got %s", sess.SessionID, sess2.SessionID)
	}
	if !sess2.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("expires_at must not be extended: %v vs %v", sess2.ExpiresAt, sess.ExpiresAt)
	}

	// 3. Idempotency Key Conflict: same operator + idempotency key, but different diagnostic_id
	inputConflict := input
	inputConflict.DiagnosticID = "222222222222222222222222"
	_, _, errConflict := service.StartSession(ctx, inputConflict)
	if errConflict == nil || errConflict != diagnostics.ErrIdempotencyConflict {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", errConflict)
	}

	// 4. Concurrency Limit: max 10 concurrent active sessions
	for i := 2; i <= 10; i++ {
		_, _, err := service.StartSession(ctx, diagnostics.StartSessionInput{
			DiagnosticID:  fmt.Sprintf("cust_%020d", i),
			OperatorID:    1,
			TargetNodeIDs: []int64{101},
			Now:           now,
		})
		if err != nil {
			t.Fatalf("failed starting session %d: %v", i, err)
		}
	}

	// 11th session should fail with ErrLeaseLimitReached
	_, _, errLimit := service.StartSession(ctx, diagnostics.StartSessionInput{
		DiagnosticID:  "cust_00000000000000000011",
		OperatorID:    1,
		TargetNodeIDs: []int64{101},
		Now:           now,
	})
	if errLimit == nil || errLimit != diagnostics.ErrLeaseLimitReached {
		t.Fatalf("expected ErrLeaseLimitReached, got %v", errLimit)
	}
}

func TestBuildNodeSnapshotAndAck(t *testing.T) {
	service := diagnostics.NewMemoryLeaseService()
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	scope := "00000000-0000-4000-8000-000000000001"

	sess, _, err := service.StartSession(ctx, diagnostics.StartSessionInput{
		DiagnosticID:  "111111111111111111111111",
		OperatorID:    1,
		TargetNodeIDs: []int64{101},
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}

	requestID := "req00000000000000000000000000001"
	snap, err := service.BuildNodeSnapshot(ctx, scope, 101, "rt1", requestID, now)
	if err != nil {
		t.Fatalf("build snapshot failed: %v", err)
	}

	if len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != sess.SessionID {
		t.Fatalf("snapshot should contain session %s, got: %+v", sess.SessionID, snap.Sessions)
	}
	if snap.ControlEpoch != service.ControlEpoch() {
		t.Errorf("mismatched control epoch")
	}

	t.Run("valid_ack", func(t *testing.T) {
		ackPayload := diagnostics.NodeAckPayload{
			RequestID:      requestID,
			RuntimeID:      "rt1",
			ControlEpoch:   snap.ControlEpoch,
			ControlVersion: snap.ControlVersion,
			Sessions: []diagnostics.NodeAckSessionPayload{
				{
					SessionID:       sess.SessionID,
					Revision:        sess.Revision,
					Effective:       true,
					LocalDeadlineMS: 900000,
				},
			},
		}

		if err := service.ProcessNodeAck(ctx, 101, "rt1", ackPayload, now.Add(5*time.Second)); err != nil {
			t.Fatalf("valid ack failed: %v", err)
		}

		updated, ok, _ := service.GetSession(ctx, sess.DiagnosticID)
		if !ok || updated.State != diagnostics.SessionStateActive {
			t.Fatalf("expected state active after ACK, got %+v", updated)
		}
	})

	t.Run("forged_ack_with_unauthorized_session", func(t *testing.T) {
		forgedPayload := diagnostics.NodeAckPayload{
			RequestID:      requestID,
			RuntimeID:      "rt1",
			ControlEpoch:   snap.ControlEpoch,
			ControlVersion: snap.ControlVersion,
			Sessions: []diagnostics.NodeAckSessionPayload{
				{
					SessionID: "forged-session-id-not-in-snapshot",
					Revision:  1,
					Effective: true,
				},
			},
		}

		err := service.ProcessNodeAck(ctx, 101, "rt1", forgedPayload, now.Add(10*time.Second))
		if err == nil {
			t.Fatal("expected error for forged ACK, got nil")
		}
	})

	t.Run("expired_snapshot_binding", func(t *testing.T) {
		ackPayload := diagnostics.NodeAckPayload{
			RequestID:      requestID,
			RuntimeID:      "rt1",
			ControlEpoch:   snap.ControlEpoch,
			ControlVersion: snap.ControlVersion,
			Sessions: []diagnostics.NodeAckSessionPayload{
				{
					SessionID: sess.SessionID,
					Revision:  sess.Revision,
					Effective: true,
				},
			},
		}

		// 65 seconds later (> 60s binding TTL)
		err := service.ProcessNodeAck(ctx, 101, "rt1", ackPayload, now.Add(65*time.Second))
		if err == nil {
			t.Fatal("expected error for expired snapshot binding, got nil")
		}
	})
}

func TestStopSession(t *testing.T) {
	service := diagnostics.NewMemoryLeaseService()
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	scope := "00000000-0000-4000-8000-000000000001"

	sess, _, err := service.StartSession(ctx, diagnostics.StartSessionInput{
		DiagnosticID:  "111111111111111111111111",
		OperatorID:    1,
		TargetNodeIDs: []int64{101},
		Now:           now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Stop the session
	stoppedSess, err := service.StopSession(ctx, diagnostics.StopSessionInput{
		DiagnosticID: sess.DiagnosticID,
		SessionID:    sess.SessionID,
		OperatorID:   1,
		Now:          now.Add(1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("stop session failed: %v", err)
	}

	if stoppedSess.Enabled {
		t.Error("session should be disabled after stop")
	}
	if stoppedSess.Revision != 2 {
		t.Errorf("expected revision 2 after stop, got %d", stoppedSess.Revision)
	}

	// Snapshot should now be empty of this session
	snap, err := service.BuildNodeSnapshot(ctx, scope, 101, "rt1", "req-after-stop", now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 0 {
		t.Errorf("snapshot should not contain stopped session, got: %+v", snap.Sessions)
	}
}
