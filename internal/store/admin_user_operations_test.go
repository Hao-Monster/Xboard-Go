package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResetAdminUserTrafficIsAtomicAuditedAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	resetMethod := 1
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: pointerTo(int64(7)), TransferEnableGiB: 64, Name: "U4 reset plan",
		ResetTrafficMethod: &resetMethod, Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: strings.Repeat("a", 280) + "@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.AddDate(0, 2, 0)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-reset@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)),
		PlanID: &plan.ID, TransferEnable: 64 << 30, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET traffic_u = 1234, traffic_d = 5678 WHERE id = ?
	`, account.ID); err != nil {
		t.Fatal(err)
	}
	reason := strings.Repeat("界", 255)

	first, err := database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
		UserID: account.ID, AdministratorID: administrator.ID,
		Reason: reason, IdempotencyKey: "u4-reset-request-0001",
	}, now)
	if err != nil {
		t.Fatalf("ResetAdminUserTraffic() error = %v", err)
	}
	if first.Idempotent || first.UploadBefore != 1234 || first.DownloadBefore != 5678 ||
		first.UploadAfter != 0 || first.DownloadAfter != 0 || first.ResetCount != 1 || first.ResetAt != now {
		t.Fatalf("unexpected first reset: %#v", first)
	}
	if first.UUID == "" || first.GroupID == nil || *first.GroupID != 7 || first.NextResetAt == nil {
		t.Fatalf("reset omitted runtime scheduling state: %#v", first)
	}

	retry, err := database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
		UserID: account.ID, AdministratorID: administrator.ID,
		Reason: reason, IdempotencyKey: "u4-reset-request-0001",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("idempotent retry error = %v", err)
	}
	if !retry.Idempotent || retry.ResetAt != now || retry.ResetCount != 1 {
		t.Fatalf("unexpected retry result: %#v", retry)
	}

	if _, err := database.db.ExecContext(ctx, `UPDATE users SET traffic_u = 99 WHERE id = ?`, account.ID); err != nil {
		t.Fatal(err)
	}
	_, err = database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
		UserID: account.ID, AdministratorID: administrator.ID,
		Reason: "different request", IdempotencyKey: "u4-reset-request-0001",
	}, now.Add(2*time.Minute))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("reused idempotency key error = %v, want ErrConflict", err)
	}
	var upload, download int64
	var resetCount, logCount int
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d, reset_count FROM users WHERE id = ?`, account.ID).Scan(&upload, &download, &resetCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_reset_logs WHERE user_id = ?`, account.ID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if upload != 99 || download != 0 || resetCount != 1 || logCount != 1 {
		t.Fatalf("idempotency mismatch mutated state: upload=%d download=%d count=%d logs=%d", upload, download, resetCount, logCount)
	}

	history, err := database.ListAdminUserTrafficResets(ctx, account.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if history.Total != 1 || len(history.Items) != 1 || history.Items[0].Reason != reason ||
		history.Items[0].AdministratorID == nil || *history.Items[0].AdministratorID != administrator.ID {
		t.Fatalf("unexpected reset history: %#v", history)
	}
}

func TestResetAdminUserTrafficConcurrentRetryMutatesOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 2, 30, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: pointerTo(int64(7)), TransferEnableGiB: 64, Name: "U4 concurrent reset plan",
		Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-concurrent-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.AddDate(0, 2, 0)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-concurrent-reset@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)),
		PlanID: &plan.ID, TransferEnable: 64 << 30, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET traffic_u = 13, traffic_d = 17 WHERE id = ?`, account.ID); err != nil {
		t.Fatal(err)
	}

	type outcome struct {
		result AdminUserTrafficResetResult
		err    error
	}
	const attempts = 2
	ready := make(chan struct{})
	outcomes := make(chan outcome, attempts)
	var workers sync.WaitGroup
	for range attempts {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-ready
			result, err := database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
				UserID: account.ID, AdministratorID: administrator.ID,
				Reason: "simultaneous administrator retry", IdempotencyKey: "u4-concurrent-reset-request",
			}, now)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(ready)
	workers.Wait()
	close(outcomes)

	idempotent := 0
	for item := range outcomes {
		if item.err != nil {
			t.Fatalf("concurrent ResetAdminUserTraffic() error = %v", item.err)
		}
		if item.result.Idempotent {
			idempotent++
		}
		if item.result.UploadBefore != 13 || item.result.DownloadBefore != 17 || item.result.ResetCount != 1 {
			t.Fatalf("concurrent reset returned inconsistent result: %#v", item.result)
		}
	}
	if idempotent != attempts-1 {
		t.Fatalf("idempotent results = %d, want %d", idempotent, attempts-1)
	}
	var upload, download int64
	var resetCount, logCount int
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d, reset_count FROM users WHERE id = ?`, account.ID).Scan(&upload, &download, &resetCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_reset_logs WHERE user_id = ?`, account.ID).Scan(&logCount); err != nil {
		t.Fatal(err)
	}
	if upload != 0 || download != 0 || resetCount != 1 || logCount != 1 {
		t.Fatalf("concurrent retry mutated more than once: upload=%d download=%d count=%d logs=%d", upload, download, resetCount, logCount)
	}
}

func TestResetAdminUserTrafficRejectsIneligibleAccounts(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 3, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-guard-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	noPlan, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-no-plan@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
		UserID: noPlan.ID, AdministratorID: administrator.ID, IdempotencyKey: "u4-ineligible-request",
	}, now)
	if !errors.Is(err, ErrTrafficResetUnavailable) || !errors.Is(err, ErrConflict) {
		t.Fatalf("no-plan reset error = %v, want ErrTrafficResetUnavailable and ErrConflict", err)
	}

	var internalID int64
	if err := database.db.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, account_kind, uuid, transfer_enable, subscription_token, created_at, updated_at)
		VALUES ('u4-internal@example.test', 'hash', 'internal_subscription', '6c7f599b-b133-4986-8f3a-709122ce0cc1', 1, ?, ?, ?)
		RETURNING id
	`, testSubscriptionToken(t), now.Unix(), now.Unix()).Scan(&internalID); err != nil {
		t.Fatal(err)
	}
	_, err = database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
		UserID: internalID, AdministratorID: administrator.ID, IdempotencyKey: "u4-internal-request",
	}, now)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("internal reset error = %v, want ErrNotFound", err)
	}
}

func TestAdminUserTrafficResetReasonUsesLegacyCharacterLimit(t *testing.T) {
	input := AdminUserTrafficResetInput{
		UserID: 1, AdministratorID: 2, IdempotencyKey: "u4-reason-boundary", // gitleaks:allow -- deterministic idempotency fixture
		Reason: strings.Repeat("界", 255),
	}
	if _, err := normalizeAdminUserTrafficResetInput(input); err != nil {
		t.Fatalf("255-character reason rejected: %v", err)
	}
	input.Reason += "界"
	if _, err := normalizeAdminUserTrafficResetInput(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("256-character reason error = %v, want ErrInvalidInput", err)
	}
	input.Reason = "invalid\x00reason"
	if _, err := normalizeAdminUserTrafficResetInput(input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NUL reason error = %v, want ErrInvalidInput", err)
	}
}

func TestAdminUserRelatedViewsAreServerScopedAndSecretsStayExplicit(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 4, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: pointerTo(int64(7)), TransferEnableGiB: 16, Name: "U4 related plan",
		Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-owner@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-other@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	invited, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-invited@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.UpdateAdminUser(ctx, invited.ID, UpdateAdminUserInput{
		Revision: invited.Revision, Email: invited.Email, GroupID: invited.GroupID,
		InviteUserEmailSet: true, InviteUserEmail: &owner.Email,
		TransferEnable: invited.TransferEnable, ExpiredAt: invited.ExpiredAt,
		SpeedLimit: invited.SpeedLimit, DeviceLimit: invited.DeviceLimit, Banned: invited.Banned,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	ownerOrder, err := database.AssignOrder(ctx, AssignOrderInput{Email: owner.Email, PlanID: plan.ID, Period: "monthly", TotalAmount: 1234}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignOrder(ctx, AssignOrderInput{Email: other.Email, PlanID: plan.ID, Period: "monthly", TotalAmount: 4321}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO user_traffic_stats (user_id, rate_micros, record_at, record_type, upload, download, created_at, updated_at)
		VALUES (?, 1000000, ?, 'd', 111, 222, ?, ?), (?, 1000000, ?, 'd', 999, 999, ?, ?)
	`, owner.ID, now.Unix(), now.Unix(), now.Unix(), other.ID, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	orders, err := database.ListAdminOrders(ctx, AdminOrderFilter{Page: 1, PageSize: 20, UserID: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if orders.Total != 1 || len(orders.Items) != 1 || orders.Items[0].ID != ownerOrder.ID {
		t.Fatalf("scoped orders = %#v", orders)
	}
	invitations, err := database.ListAdminUserInvitations(ctx, owner.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if invitations.Total != 1 || len(invitations.Items) != 1 || invitations.Items[0].ID != invited.ID {
		t.Fatalf("scoped invitations = %#v", invitations)
	}
	traffic, err := database.ListAdminUserTrafficStats(ctx, owner.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if traffic.Total != 1 || len(traffic.Items) != 1 || traffic.Items[0].Upload != 111 || traffic.Items[0].Download != 222 {
		t.Fatalf("scoped traffic = %#v", traffic)
	}

	token, err := database.GetAdminUserSubscriptionToken(ctx, owner.ID)
	if err != nil || token == "" {
		t.Fatalf("GetAdminUserSubscriptionToken() = (%q, %v)", token, err)
	}
	encoded, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsJSONSecret(encoded, token) {
		t.Fatalf("ordinary AdminUser JSON leaked subscription token: %s", encoded)
	}
}

func TestAdminUserRelatedQueriesUseScopedIndexes(t *testing.T) {
	database := newTestStore(t)
	for name, testCase := range map[string]struct {
		query string
		index string
	}{
		"orders": {
			query: `SELECT id FROM orders WHERE user_id = 1 ORDER BY created_at DESC, id DESC LIMIT 20`,
			index: "idx_orders_user_created",
		},
		"invitations": {
			query: `SELECT id FROM users WHERE account_kind = 'human' AND invite_user_id = 1 ORDER BY id DESC LIMIT 20`,
			index: "idx_users_invite_user_id",
		},
		"traffic": {
			query: `SELECT user_id FROM user_traffic_stats WHERE user_id = 1 ORDER BY record_at DESC, record_type DESC, rate_micros DESC LIMIT 20`,
			index: "idx_user_traffic_stats_user_record",
		},
		"traffic resets": {
			query: `SELECT id FROM traffic_reset_logs WHERE user_id = 1 ORDER BY reset_at DESC, id DESC LIMIT 20`,
			index: "idx_traffic_reset_logs_user",
		},
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := database.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+testCase.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan strings.Builder
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail)
				plan.WriteByte('\n')
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.String(), testCase.index) {
				t.Fatalf("query plan did not use %s:\n%s", testCase.index, plan.String())
			}
		})
	}
}

func TestAssignOrderByUserIDCannotRetargetReusedEmail(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 4, 30, 0, 0, time.UTC)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "U4 scoped assignment plan", TransferEnableGiB: 8, Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-assignment-original@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET email = ? WHERE id = ?`, "u4-assignment-renamed@example.test", target.ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-assignment-original@example.test", PasswordHash: "hash",
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignOrder(ctx, AssignOrderInput{
		UserID: &target.ID, Email: replacement.Email, PlanID: plan.ID, Period: "monthly", TotalAmount: 100,
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ambiguous assignment selector error = %v, want ErrInvalidInput", err)
	}
	order, err := database.AssignOrder(ctx, AssignOrderInput{
		UserID: &target.ID, PlanID: plan.ID, Period: "monthly", TotalAmount: 100,
	}, now)
	if err != nil {
		t.Fatalf("AssignOrder(user ID) error = %v", err)
	}
	if order.UserID != target.ID || order.UserID == replacement.ID {
		t.Fatalf("scoped assignment targeted user %d, want %d and not replacement %d", order.UserID, target.ID, replacement.ID)
	}
}

func TestSchemaV38PreservesScheduledTrafficResetHistory(t *testing.T) {
	database := newTestStore(t)
	removeSchemaV39ForMigrationTest(t, database)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 5, 0, 0, 0, time.UTC)
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "U4 migration plan", TransferEnableGiB: 1, Prices: PlanPrices{}, Tags: []string{}}, now)
	if err != nil {
		t.Fatal(err)
	}
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "u4-migration@example.test", PasswordHash: "hash", PlanID: &plan.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	scheduledFor := now.Add(-time.Hour).Unix()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO traffic_reset_logs (user_id, plan_id, scheduled_for, reset_at, upload_before, download_before, reset_count)
		VALUES (?, ?, ?, ?, 17, 29, 3)
	`, account.ID, plan.ID, scheduledFor, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP INDEX idx_traffic_reset_logs_user;
		DROP INDEX idx_traffic_reset_logs_scheduled;
		DROP INDEX idx_traffic_reset_logs_manual_idempotency;
		DROP INDEX idx_user_traffic_stats_user_record;
		ALTER TABLE traffic_reset_logs RENAME TO traffic_reset_logs_v38;
		CREATE TABLE traffic_reset_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL,
			scheduled_for INTEGER NOT NULL CHECK (scheduled_for >= 0),
			reset_at INTEGER NOT NULL CHECK (reset_at >= scheduled_for),
			upload_before INTEGER NOT NULL CHECK (upload_before >= 0),
			download_before INTEGER NOT NULL CHECK (download_before >= 0),
			reset_count INTEGER NOT NULL CHECK (reset_count > 0),
			UNIQUE (user_id, scheduled_for)
		);
		INSERT INTO traffic_reset_logs (id, user_id, plan_id, scheduled_for, reset_at, upload_before, download_before, reset_count)
		SELECT id, user_id, plan_id, scheduled_for, reset_at, upload_before, download_before, reset_count FROM traffic_reset_logs_v38;
		DROP TABLE traffic_reset_logs_v38;
		CREATE INDEX idx_traffic_reset_logs_user ON traffic_reset_logs(user_id, reset_at DESC, id DESC);
		PRAGMA user_version = 37;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v37 to v38) error = %v", err)
	}
	history, err := database.ListAdminUserTrafficResets(ctx, account.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 1 || history.Items[0].TriggerSource != "scheduled" ||
		history.Items[0].ScheduledFor == nil || history.Items[0].ScheduledFor.Unix() != scheduledFor ||
		history.Items[0].UploadBefore != 17 || history.Items[0].DownloadBefore != 29 ||
		history.Items[0].UploadAfter != 0 || history.Items[0].DownloadAfter != 0 {
		t.Fatalf("migrated history = %#v", history)
	}
}

func containsJSONSecret(document []byte, secret string) bool {
	var value map[string]any
	if json.Unmarshal(document, &value) != nil {
		return true
	}
	for _, item := range value {
		if text, ok := item.(string); ok && text == secret {
			return true
		}
	}
	return false
}

func BenchmarkResetAdminUserTraffic(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 6, 0, 0, 0, time.UTC)
	seedAdminUserGroups(b, database, now)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: pointerTo(int64(7)), TransferEnableGiB: 64, Name: "U4 reset benchmark plan",
		Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-reset-benchmark-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	expiresAt := now.AddDate(1, 0, 0)
	account, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u4-reset-benchmark@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)),
		PlanID: &plan.ID, TransferEnable: 64 << 30, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		result, err := database.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{
			UserID: account.ID, AdministratorID: administrator.ID,
			Reason: "benchmark", IdempotencyKey: "u4-benchmark-reset-" + strconv.Itoa(index),
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			b.Fatal(err)
		}
		if result.Idempotent || result.ResetCount != index+1 {
			b.Fatalf("unexpected reset result at %d: %#v", index, result)
		}
	}
}
