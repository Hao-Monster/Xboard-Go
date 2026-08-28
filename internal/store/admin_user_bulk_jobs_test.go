package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSchemaV39PreservesV38UsersAndAddsAdministratorBulkJobs(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 7, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-migration-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	removeSchemaV39ForMigrationTest(t, database)
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 38`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v38 to v39) error = %v", err)
	}
	preserved, err := database.GetAdminUser(ctx, administrator.ID)
	if err != nil || preserved.Email != administrator.Email || !preserved.IsAdmin {
		t.Fatalf("preserved administrator = %#v, %v", preserved, err)
	}
	var version, tables, indexes int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('admin_user_bulk_jobs','admin_user_bulk_targets')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='index' AND name IN ('idx_admin_user_bulk_jobs_list','idx_admin_user_bulk_jobs_claim','idx_admin_user_bulk_targets_claim','idx_admin_user_bulk_targets_job_status')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() || tables != 2 || indexes != 4 {
		t.Fatalf("schema version=%d tables=%d indexes=%d", version, tables, indexes)
	}
	job, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll},
	}, now.Add(time.Second))
	if err != nil || job.TotalCount != 1 {
		t.Fatalf("post-migration bulk job = %#v, %v", job, err)
	}
}

func TestAdminUserBulkJobSnapshotsSelectedFilteredAndAllScopes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	active, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-active@example.test", PasswordHash: "hash", TransferEnable: 10 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	banned, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-banned@example.test", PasswordHash: "hash", Banned: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (
			email,password_hash,is_admin,banned,account_kind,uuid,subscription_token,
			transfer_enable,created_at,updated_at
		) VALUES ('internal-u5@internal.invalid','!',0,0,'internal_subscription',
			'00000000-0000-4000-8000-000000000005','eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',0,?,?)
	`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	selected, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{banned.ID, active.ID, active.ID}},
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUserBulkJob(selected) error = %v", err)
	}
	selectedTargets, err := database.ListAdminUserBulkTargets(ctx, selected.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if selected.TotalCount != 2 || len(selectedTargets) != 2 ||
		selectedTargets[0].UserID != active.ID || selectedTargets[1].UserID != banned.ID {
		t.Fatalf("selected job=%#v targets=%#v", selected, selectedTargets)
	}

	filtered, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeFiltered, Filter: AdminUserFilter{
			Rules: []AdminUserFilterRule{{Field: AdminUserFieldBanned, Operator: AdminUserOperatorEqual, Values: []string{"false"}}},
		}},
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("CreateAdminUserBulkJob(filtered) error = %v", err)
	}
	if filtered.TotalCount != 2 { // administrator and active user; internal account is excluded.
		t.Fatalf("filtered total = %d, want 2", filtered.TotalCount)
	}

	all, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll, Filter: AdminUserFilter{
			Rules: []AdminUserFilterRule{{Field: AdminUserFieldID, Operator: AdminUserOperatorEqual, Values: []string{fmt.Sprint(active.ID)}}},
		}},
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("CreateAdminUserBulkJob(all) error = %v", err)
	}
	if all.TotalCount != 3 {
		t.Fatalf("all total = %d, want 3 human users", all.TotalCount)
	}

	if _, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeFiltered},
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty filtered scope error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: make([]int64, 501)},
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized selected scope error = %v, want ErrInvalidInput", err)
	}
}

func TestAdminUserBulkJobEnforcesTenThousandTargetLimitWithoutPartialJob(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 8, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-limit-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	seedAdministratorBulkUsers(t, database, 10_000, now)
	if _, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll},
	}, now.Add(time.Second)); !errors.Is(err, ErrAdminUserBulkLimit) {
		t.Fatalf("10,001-target job error = %v, want ErrAdminUserBulkLimit", err)
	}
	page, err := database.ListAdminUserBulkJobs(ctx, 1, 20)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("limit failure left partial jobs = %#v, %v", page, err)
	}
}

func BenchmarkAdminUserBulkJobTenThousandTargets(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 8, 45, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-benchmark-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	seedAdministratorBulkUsers(b, database, 9_999, now)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
			Kind: AdminUserBulkKindCSV, AdministratorID: administrator.ID,
			Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll},
		}, now.Add(time.Duration(index+1)*time.Second)); err != nil {
			b.Fatal(err)
		}
	}
}

func seedAdministratorBulkUsers(tb testing.TB, database *Store, count int, now time.Time) {
	tb.Helper()
	tx, err := database.db.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	statement, err := tx.Prepare(`
		INSERT INTO users (email,password_hash,is_admin,banned,account_kind,subscription_token,created_at,updated_at)
		VALUES (?, 'hash', 0, 0, 'human', ?, ?, ?)
	`)
	if err != nil {
		tb.Fatal(err)
	}
	defer statement.Close()
	for index := 0; index < count; index++ {
		if _, err := statement.Exec(fmt.Sprintf("u5-bulk-%05d@example.test", index), fmt.Sprintf("%032x", index+1), now.Unix(), now.Unix()); err != nil {
			tb.Fatalf("seed bulk user %d: %v", index, err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func TestAdminUserBulkMailClaimRetryCancelAndTemplateSnapshot(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-mail-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-mail-target@example.test", PasswordHash: "hash", TransferEnable: 20 << 30,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name='U5 Board', app_url='https://panel.example.test',
			smtp_enabled=1, smtp_host='smtp.example.test', smtp_port=587,
			smtp_encryption='starttls', smtp_from_address='no-reply@example.test'
		WHERE id=1
	`); err != nil {
		t.Fatal(err)
	}
	job, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindMail, AdministratorID: administrator.ID,
		Scope:   AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "通知 {{user.email}}", Content: "{{app.name}} {{missing|默认值}} {{unknown}}",
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUserBulkJob(mail) error = %v", err)
	}
	if job.Subject != "通知 {{user.email}}" || job.AppName != "U5 Board" || job.AppURL != "https://panel.example.test" {
		t.Fatalf("mail snapshot = %#v", job)
	}

	claimed, ok, err := database.ClaimAdminUserBulkMail(ctx, "claim-0001", now, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("ClaimAdminUserBulkMail() = %#v, %v, %v", claimed, ok, err)
	}
	if claimed.JobID != job.ID || claimed.UserID != target.ID || claimed.Attempt != 1 || claimed.Email != target.Email {
		t.Fatalf("claimed mail = %#v", claimed)
	}
	if err := database.FailAdminUserBulkMail(ctx, claimed.JobID, claimed.Sequence, "claim-0001", "temporary SMTP failure", now.Add(time.Second), now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := database.ClaimAdminUserBulkMail(ctx, "claim-too-early", now, 30*time.Second); err != nil || ok {
		t.Fatalf("early retry claimed=%v error=%v", ok, err)
	}
	retry, ok, err := database.ClaimAdminUserBulkMail(ctx, "claim-0002", now.Add(time.Second), 30*time.Second)
	if err != nil || !ok || retry.Attempt != 2 {
		t.Fatalf("retry = %#v, %v, %v", retry, ok, err)
	}
	if _, err := database.CancelAdminUserBulkJob(ctx, job.ID, administrator.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := database.CompleteAdminUserBulkMail(ctx, retry.JobID, retry.Sequence, "claim-0002", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	finished, err := database.GetAdminUserBulkJob(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != AdminUserBulkStatusCancelled || finished.ProcessedCount != 1 || finished.SuccessCount != 1 || finished.FailureCount != 0 {
		t.Fatalf("cancelled in-flight job = %#v", finished)
	}
}

func TestBanAdminUsersIsIdempotentAndProtectsAdministrator(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-ban-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	active, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-ban-active@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	already, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "u5-ban-already@example.test", PasswordHash: "hash", Banned: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: active.ID, Name: "U5 token", TokenHash: strings.Repeat("d", 64),
	}, now); err != nil {
		t.Fatal(err)
	}
	result, err := database.BanAdminUsers(ctx, BanAdminUsersInput{
		AdministratorID: administrator.ID, IdempotencyKey: "u5-ban-request-0001",
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll},
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("BanAdminUsers() error = %v", err)
	}
	if result.SuccessCount != 1 || result.SkippedCount != 2 || result.FailureCount != 0 || result.Status != AdminUserBulkStatusSucceeded {
		t.Fatalf("ban result = %#v", result)
	}
	var adminBanned, activeBanned bool
	var activeTokens int
	if err := database.db.QueryRowContext(ctx, `SELECT banned FROM users WHERE id=?`, administrator.ID).Scan(&adminBanned); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT banned FROM users WHERE id=?`, active.ID).Scan(&activeBanned); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM access_tokens WHERE user_id=? AND revoked_at IS NULL`, active.ID).Scan(&activeTokens); err != nil {
		t.Fatal(err)
	}
	if adminBanned || !activeBanned || activeTokens != 0 {
		t.Fatalf("ban state admin=%v active=%v tokens=%d", adminBanned, activeBanned, activeTokens)
	}
	targets, err := database.ListAdminUserBulkTargets(ctx, result.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0].Status != AdminUserBulkTargetSkipped || targets[1].Status != AdminUserBulkTargetSucceeded || targets[2].Status != AdminUserBulkTargetSkipped {
		t.Fatalf("ban target audit = %#v", targets)
	}
	warned, err := database.MarkAdminUserBulkRuntimeWarning(ctx, result.ID, now.Add(90*time.Second))
	if err != nil {
		t.Fatalf("MarkAdminUserBulkRuntimeWarning() error = %v", err)
	}
	if warned.Status != AdminUserBulkStatusSucceeded || warned.LastError != "node runtime notification failed; state will reconcile on the next full pull" {
		t.Fatalf("warned ban result = %#v", warned)
	}
	replayed, err := database.BanAdminUsers(ctx, BanAdminUsersInput{
		AdministratorID: administrator.ID, IdempotencyKey: "u5-ban-request-0001",
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeAll},
	}, now.Add(2*time.Minute))
	if err != nil || replayed.ID != result.ID || replayed.LastError != warned.LastError {
		t.Fatalf("idempotent ban = %#v, %v", replayed, err)
	}
	if _, err := database.BanAdminUsers(ctx, BanAdminUsersInput{
		AdministratorID: administrator.ID, IdempotencyKey: "u5-ban-request-0001",
		Scope: AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{active.ID}},
	}, now.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused ban key error = %v, want ErrConflict", err)
	}
	_ = already
}
