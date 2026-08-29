package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"
)

func BenchmarkSubscriptionPolicyRescheduleTenThousandUsers(b *testing.B) {
	database, err := OpenSQLite(fmt.Sprintf("file:%s?mode=memory&cache=shared", b.Name()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "policy-benchmark-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, time.Unix(1, 0))
	if err != nil {
		b.Fatal(err)
	}
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Benchmark plan", TransferEnableGiB: 1}, time.Unix(1, 0))
	if err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (email, password_hash, subscription_token, created_at, updated_at, plan_id, expired_at, next_reset_at)
		VALUES (?, 'hash', ?, 1, 1, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	for index := 0; index < 10_000; index++ {
		if _, err := statement.ExecContext(ctx, fmt.Sprintf("policy-benchmark-%05d@example.test", index), fmt.Sprintf("%032x", index+1), plan.ID, 2_000_000_000, 2_000_000_000); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	policy, err := database.GetSubscriptionPolicySettings(ctx)
	if err != nil {
		b.Fatal(err)
	}
	revision := policy.Revision
	b.ReportAllocs()
	b.ResetTimer()
	iteration := 0
	for b.Loop() {
		method := 0
		if iteration%2 == 0 {
			method = 3
		}
		updated, err := database.UpdateSubscriptionPolicySettings(ctx, administrator.ID, revision, SaveSubscriptionPolicySettingsInput{
			PlanChangeEnabled: true, ResetTrafficMethod: method, SurplusEnabled: true,
			DefaultRemindExpire: true, DefaultRemindTraffic: true,
		}, time.Unix(int64(iteration+2), 0))
		if err != nil {
			b.Fatal(err)
		}
		revision = updated.Revision
		iteration++
	}
	b.ReportMetric(10_000, "users/op")
}

func TestSubscriptionPolicySettingsUseRevisionValidationAndAtomicLegacyMerges(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 11, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "policy-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetSubscriptionPolicySettings(ctx)
	if err != nil || initial.Revision != 1 || !initial.PlanChangeEnabled || !initial.SurplusEnabled || initial.ResetTrafficMethod != 0 {
		t.Fatalf("initial policy=%#v err=%v", initial, err)
	}
	input := SaveSubscriptionPolicySettingsInput{
		PlanChangeEnabled: false, ResetTrafficMethod: 4, SurplusEnabled: false,
		NewOrderEventID: 1, RenewOrderEventID: 1, ChangeOrderEventID: 1,
		DefaultRemindExpire: false, DefaultRemindTraffic: true,
	}
	updated, err := database.UpdateSubscriptionPolicySettings(ctx, administrator.ID, initial.Revision, input, now.Add(time.Second))
	if err != nil || updated.Revision != 2 || updated.PlanChangeEnabled || updated.ResetTrafficMethod != 4 ||
		updated.SurplusEnabled || updated.NewOrderEventID != 1 || updated.DefaultRemindExpire || !updated.DefaultRemindTraffic {
		t.Fatalf("updated policy=%#v err=%v", updated, err)
	}
	if _, err := database.UpdateSubscriptionPolicySettings(ctx, administrator.ID, initial.Revision, input, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale policy update error=%v, want ErrConflict", err)
	}
	input.ResetTrafficMethod = 5
	if _, err := database.UpdateSubscriptionPolicySettings(ctx, administrator.ID, updated.Revision, input, now.Add(2*time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid policy update error=%v, want ErrInvalidInput", err)
	}

	path := "legacy_feed"
	show := true
	planChange := true
	reset := 2
	legacy, err := database.UpdateLegacyAdminSubscriptionConfig(ctx, administrator.ID, SaveLegacyAdminSubscriptionConfigInput{
		PlanChangeEnabled: &planChange, ResetTrafficMethod: &reset, ShowInfo: &show, Path: &path,
	}, now.Add(3*time.Second))
	if err != nil || !legacy.PlanChangeEnabled || legacy.ResetTrafficMethod != 2 || !legacy.ShowInfo || legacy.Path != path ||
		legacy.SurplusEnabled || legacy.ShowProtocol || legacy.DefaultRemindExpire || !legacy.DefaultRemindTraffic {
		t.Fatalf("legacy partial update=%#v err=%v", legacy, err)
	}
	unsafe := "../unsafe"
	disable := false
	if _, err := database.UpdateLegacyAdminSubscriptionConfig(ctx, administrator.ID, SaveLegacyAdminSubscriptionConfigInput{
		PlanChangeEnabled: &disable, Path: &unsafe,
	}, now.Add(4*time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid legacy update error=%v, want ErrInvalidInput", err)
	}
	preserved, err := database.GetLegacyAdminSubscriptionConfig(ctx)
	if err != nil || preserved != legacy {
		t.Fatalf("invalid legacy update changed config: got=%#v want=%#v err=%v", preserved, legacy, err)
	}
}

func TestSchemaV45AddsReminderDefaultsAndPreservesConfiguredTrafficReset(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET traffic_reset_method = 4, revision = 2, updated_at = 100 WHERE id = 1;
		ALTER TABLE app_settings DROP COLUMN default_remind_expire;
		ALTER TABLE app_settings DROP COLUMN default_remind_traffic;
		PRAGMA user_version = 44;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v44 to v45) error=%v", err)
	}
	var version, reset int
	var expire, traffic bool
	if err := database.db.QueryRowContext(ctx, `
		SELECT traffic_reset_method, default_remind_expire, default_remind_traffic FROM app_settings WHERE id = 1
	`).Scan(&reset, &expire, &traffic); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || reset != 4 || !expire || !traffic {
		t.Fatalf("migration version=%d reset=%d defaults=(%t,%t)", version, reset, expire, traffic)
	}
}

func TestSchemaV45AlignsOnlyPristineTrafficResetDefault(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET traffic_reset_method = 1, revision = 1, updated_at = 0 WHERE id = 1;
		ALTER TABLE app_settings DROP COLUMN default_remind_expire;
		ALTER TABLE app_settings DROP COLUMN default_remind_traffic;
		PRAGMA user_version = 44;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v44 pristine to v45) error=%v", err)
	}
	var reset int
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_reset_method FROM app_settings WHERE id = 1`).Scan(&reset); err != nil {
		t.Fatal(err)
	}
	if reset != 0 {
		t.Fatalf("pristine traffic reset method=%d, want legacy default 0", reset)
	}
}

func TestSubscriptionPolicyResetChangesRescheduleOnlySystemFollowingUsers(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "reset-policy-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	systemPlan, err := database.CreatePlan(ctx, SavePlanInput{Name: "System reset plan", TransferEnableGiB: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	fixedMethod := 1
	fixedPlan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Fixed reset plan", TransferEnableGiB: 1, ResetTrafficMethod: &fixedMethod,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(400 * 24 * time.Hour)
	systemUser, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "system-reset-user@example.test", PasswordHash: "hash", PlanID: &systemPlan.ID, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	fixedUser, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "fixed-reset-user@example.test", PasswordHash: "hash", PlanID: &fixedPlan.ID, ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initialSystem := readUserNextReset(t, database, systemUser.ID)
	initialFixed := readUserNextReset(t, database, fixedUser.ID)
	if want := CalculateNextTrafficReset(nil, 0, &expiresAt, now); want == nil || !initialSystem.Valid || initialSystem.Int64 != want.Unix() {
		t.Fatalf("initial system reset=%#v want=%v", initialSystem, want)
	}
	if want := CalculateNextTrafficReset(&fixedMethod, 0, &expiresAt, now); want == nil || !initialFixed.Valid || initialFixed.Int64 != want.Unix() {
		t.Fatalf("initial fixed reset=%#v want=%v", initialFixed, want)
	}

	policy, err := database.GetSubscriptionPolicySettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disabled := SaveSubscriptionPolicySettingsInput{
		PlanChangeEnabled: policy.PlanChangeEnabled, ResetTrafficMethod: 2, SurplusEnabled: policy.SurplusEnabled,
		NewOrderEventID: policy.NewOrderEventID, RenewOrderEventID: policy.RenewOrderEventID,
		ChangeOrderEventID: policy.ChangeOrderEventID, DefaultRemindExpire: policy.DefaultRemindExpire,
		DefaultRemindTraffic: policy.DefaultRemindTraffic,
	}
	if _, err := database.UpdateSubscriptionPolicySettings(ctx, administrator.ID, policy.Revision, disabled, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if reset := readUserNextReset(t, database, systemUser.ID); reset.Valid {
		t.Fatalf("system-following user reset=%#v after no-reset policy, want NULL", reset)
	}
	if reset := readUserNextReset(t, database, fixedUser.ID); reset != initialFixed {
		t.Fatalf("fixed-plan user reset=%#v, want unchanged %#v", reset, initialFixed)
	}

	annual := 3
	if _, err := database.UpdateLegacyAdminSubscriptionConfig(ctx, administrator.ID, SaveLegacyAdminSubscriptionConfigInput{
		ResetTrafficMethod: &annual,
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if want := CalculateNextTrafficReset(nil, annual, &expiresAt, now.Add(2*time.Minute)); want == nil {
		t.Fatal("annual system reset calculation returned nil")
	} else if reset := readUserNextReset(t, database, systemUser.ID); !reset.Valid || reset.Int64 != want.Unix() {
		t.Fatalf("legacy-rescheduled system reset=%#v want=%d", reset, want.Unix())
	}
	if reset := readUserNextReset(t, database, fixedUser.ID); reset != initialFixed {
		t.Fatalf("legacy update changed fixed-plan reset=%#v, want %#v", reset, initialFixed)
	}
}

func TestNewUsersInheritSubscriptionReminderDefaultsWithoutChangingExistingUsers(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	existing, err := database.RegisterUser(ctx, RegisterUserInput{Email: "existing-reminders@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings
		SET default_remind_expire = 0, default_remind_traffic = 1
		WHERE id = 1
	`); err != nil {
		t.Fatalf("configure reminder defaults: %v", err)
	}

	registered, err := database.RegisterUser(ctx, RegisterUserInput{Email: "registered-reminders@example.test", PasswordHash: "hash"}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	created, err := database.CreateAdminUsers(ctx, []CreateAdminUserInput{
		{Email: "admin-created-a@example.test", PasswordHash: "hash"},
		{Email: "admin-created-b@example.test", PasswordHash: "hash"},
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}

	assertUserReminders(t, database, existing.ID, true, true)
	assertUserReminders(t, database, registered.ID, false, true)
	for _, item := range created {
		assertUserReminders(t, database, item.User.ID, false, true)
	}
}

func assertUserReminders(t *testing.T, database *Store, userID int64, wantExpire, wantTraffic bool) {
	t.Helper()
	var expire, traffic bool
	if err := database.db.QueryRowContext(t.Context(), `
		SELECT remind_expire, remind_traffic FROM users WHERE id = ?
	`, userID).Scan(&expire, &traffic); err != nil {
		t.Fatalf("read user %d reminders: %v", userID, err)
	}
	if expire != wantExpire || traffic != wantTraffic {
		t.Fatalf("user %d reminders=(%t,%t), want (%t,%t)", userID, expire, traffic, wantExpire, wantTraffic)
	}
}

func readUserNextReset(t *testing.T, database *Store, userID int64) sql.NullInt64 {
	t.Helper()
	var value sql.NullInt64
	if err := database.db.QueryRowContext(t.Context(), `SELECT next_reset_at FROM users WHERE id = ?`, userID).Scan(&value); err != nil {
		t.Fatalf("read user %d next reset: %v", userID, err)
	}
	return value
}
