package store

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMailSettingsUseSharedRevisionAndNeverReturnPasswordCipher(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "mail-settings-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetMailSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || initial.SMTPEnabled || initial.SMTPPasswordSet || initial.RemindMailEnabled {
		t.Fatalf("initial mail settings=%#v", initial)
	}
	passwordCipher := []byte("authenticated-encrypted-smtp-password")
	updated, err := database.UpdateMailSettings(ctx, administrator.ID, initial.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPUsername: "mailer",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: passwordCipher, SMTPEncryption: "starttls",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.SMTPEnabled || !updated.SMTPPasswordSet || !updated.RemindMailEnabled ||
		updated.SMTPHost != "smtp.example.test" || updated.SMTPPort != 587 {
		t.Fatalf("updated mail settings=%#v", updated)
	}
	storedCipher, err := database.GetSMTPPasswordCipher(ctx)
	if err != nil || !bytes.Equal(storedCipher, passwordCipher) {
		t.Fatalf("stored SMTP cipher=%q err=%v", storedCipher, err)
	}
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, initial.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "stale.example.test", SMTPPort: 587,
		SMTPEncryption: "starttls", SMTPFromAddress: "support@example.test",
	}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateMailSettings() error=%v, want ErrConflict", err)
	}
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, updated.Revision, SaveMailSettingsInput{
		SMTPPort: 587, SMTPEncryption: "starttls", RemindMailEnabled: true,
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("reminders without SMTP error=%v, want ErrInvalidInput", err)
	}
	ticketSettings, err := database.GetTicketSettings(ctx)
	if err != nil || ticketSettings.Revision != updated.Revision || ticketSettings.SMTPHost != updated.SMTPHost || !ticketSettings.SMTPPasswordSet {
		t.Fatalf("shared ticket settings=%#v err=%v", ticketSettings, err)
	}
}

func TestSubscriptionReminderSchedulingIsConcurrentSafe(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "concurrent-reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "concurrent-reminder@example.test", PasswordHash: "hash", ExpiredAt: &expiresAt,
	}, now); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	results := make(chan SubscriptionReminderScheduleResult, workers)
	errorsChannel := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, scheduleErr := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 500)
			results <- result
			errorsChannel <- scheduleErr
		}()
	}
	group.Wait()
	close(results)
	close(errorsChannel)
	for scheduleErr := range errorsChannel {
		if scheduleErr != nil {
			t.Fatal(scheduleErr)
		}
	}
	var expiryTotal, trafficTotal int64
	for result := range results {
		expiryTotal += result.ExpireQueued
		trafficTotal += result.TrafficQueued
	}
	if expiryTotal != 1 || trafficTotal != 0 {
		t.Fatalf("concurrent queued expiry=%d traffic=%d", expiryTotal, trafficTotal)
	}
}

func TestSubscriptionReminderFailureBecomesTerminalAfterThreeAttempts(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "retry-reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "retry-reminder@example.test", PasswordHash: "hash", ExpiredAt: &expiresAt,
	}, now); err != nil {
		t.Fatal(err)
	}
	if result, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 500); err != nil || result.ExpireQueued != 1 {
		t.Fatalf("ScheduleSubscriptionReminders()=%#v err=%v", result, err)
	}

	attemptAt := now
	for attempt := 1; attempt <= maxReminderAttempts; attempt++ {
		claimToken := fmt.Sprintf("retry-worker-%d", attempt)
		job, claimed, err := database.ClaimSubscriptionReminder(ctx, claimToken, attemptAt, time.Minute)
		if err != nil || !claimed || job.Attempt != attempt {
			t.Fatalf("attempt %d claim=(%#v,%v,%v)", attempt, job, claimed, err)
		}
		retryAt := attemptAt.Add(time.Minute)
		if err := database.FailSubscriptionReminder(ctx, job.ID, claimToken, "temporary SMTP failure", retryAt, attemptAt); err != nil {
			t.Fatalf("attempt %d failure: %v", attempt, err)
		}
		attemptAt = retryAt
	}
	if _, claimed, err := database.ClaimSubscriptionReminder(ctx, "after-terminal", attemptAt, time.Minute); err != nil || claimed {
		t.Fatalf("terminal reminder claimed=%v err=%v", claimed, err)
	}
	var terminal int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscription_reminder_outbox
		WHERE attempt_count = 3 AND failed_at IS NOT NULL AND last_error = 'temporary SMTP failure'
	`).Scan(&terminal); err != nil || terminal != 1 {
		t.Fatalf("terminal reminder rows=%d err=%v", terminal, err)
	}
}

func TestDisablingSubscriptionRemindersAtomicallyCancelsPendingJobs(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "cancel-reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	settings, err = database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "cancel-reminder@example.test", PasswordHash: "hash", ExpiredAt: &expiresAt,
	}, now); err != nil {
		t.Fatal(err)
	}
	if result, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 500); err != nil || result.ExpireQueued != 1 {
		t.Fatalf("ScheduleSubscriptionReminders()=%#v err=%v", result, err)
	}
	settings, err = database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: settings.SMTPHost, SMTPPort: settings.SMTPPort,
		SMTPUsername: settings.SMTPUsername, SMTPEncryption: settings.SMTPEncryption,
		SMTPFromAddress: settings.SMTPFromAddress, RemindMailEnabled: false,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var cancelled int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscription_reminder_outbox
		WHERE cancelled_at IS NOT NULL AND sent_at IS NULL AND failed_at IS NULL AND claim_token IS NULL
	`).Scan(&cancelled); err != nil || cancelled != 1 {
		t.Fatalf("cancelled reminder rows=%d err=%v", cancelled, err)
	}
	if _, claimed, err := database.ClaimSubscriptionReminder(ctx, "after-cancel", now.Add(2*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("cancelled reminder claimed=%v err=%v", claimed, err)
	}
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: settings.SMTPHost, SMTPPort: settings.SMTPPort,
		SMTPUsername: settings.SMTPUsername, SMTPEncryption: settings.SMTPEncryption,
		SMTPFromAddress: settings.SMTPFromAddress, RemindMailEnabled: true,
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if result, err := database.ScheduleSubscriptionReminders(ctx, now.Add(4*time.Second), "2026-08-29", 500); err != nil || result.ExpireQueued != 0 || result.TrafficQueued != 0 {
		t.Fatalf("same-day cancelled reminder was requeued: result=%#v err=%v", result, err)
	}
}

func TestAdminUserDeliveryChangesCancelUnclaimedSubscriptionReminders(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "user-change-reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "user-change-reminder@example.test", PasswordHash: "hash", ExpiredAt: &expiresAt,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 500); err != nil || result.ExpireQueued != 1 {
		t.Fatalf("ScheduleSubscriptionReminders()=%#v err=%v", result, err)
	}
	disableExpiryReminder := false
	if _, _, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: "new-user-change-reminder@example.test", GroupID: user.GroupID,
		TransferEnable: user.TransferEnable, ExpiredAt: user.ExpiredAt, SpeedLimit: user.SpeedLimit,
		DeviceLimit: user.DeviceLimit, Banned: user.Banned, RemindExpire: &disableExpiryReminder,
	}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var cancelled int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM subscription_reminder_outbox
		WHERE user_id = ? AND recipient = ? AND cancelled_at IS NOT NULL AND claim_token IS NULL
	`, user.ID, user.Email).Scan(&cancelled); err != nil || cancelled != 1 {
		t.Fatalf("changed user cancelled reminders=%d err=%v", cancelled, err)
	}
	if _, claimed, err := database.ClaimSubscriptionReminder(ctx, "changed-user", now.Add(2*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("changed user reminder claimed=%v err=%v", claimed, err)
	}
}

func TestSubscriptionReminderQueriesUseDedicatedPartialIndexes(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	queryPlan := func(query string, arguments ...any) string {
		t.Helper()
		rows, err := database.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, arguments...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(details, " | ")
	}
	expiryPlan := queryPlan(`
		SELECT u.id FROM users u
		WHERE u.banned = 0 AND u.remind_expire = 1 AND u.email <> ''
		  AND u.expired_at IS NOT NULL AND u.expired_at > ? AND u.expired_at < ?
		ORDER BY u.id LIMIT ?
	`, 1, 2, 500)
	if !strings.Contains(expiryPlan, "idx_users_reminder_expire") {
		t.Fatalf("expiry query plan=%s", expiryPlan)
	}
	trafficPlan := queryPlan(`
		SELECT u.id FROM users u
		WHERE u.banned = 0 AND u.remind_traffic = 1 AND u.email <> '' AND u.transfer_enable > 0
		  AND (u.traffic_u + u.traffic_d) >= u.transfer_enable - u.transfer_enable / 5
		  AND (u.traffic_u + u.traffic_d) < u.transfer_enable
		ORDER BY u.id LIMIT ?
	`, 500)
	if !strings.Contains(trafficPlan, "idx_users_reminder_traffic") {
		t.Fatalf("traffic query plan=%s", trafficPlan)
	}
}

func TestSubscriptionReminderSchedulingIsBoundedAndDailyIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 3, 30, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "reminder-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := database.GetMailSettings(ctx)
	if _, err := database.UpdateMailSettings(ctx, administrator.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none",
		SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}

	create := func(email string, expiredAt *int64, transfer, upload, download int64, banned, remindExpire, remindTraffic bool) int64 {
		t.Helper()
		user, createErr := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: email, PasswordHash: "hash"}, now)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, updateErr := database.db.ExecContext(ctx, `
			UPDATE users SET expired_at = ?, transfer_enable = ?, traffic_u = ?, traffic_d = ?, banned = ?,
			                 remind_expire = ?, remind_traffic = ? WHERE id = ?
		`, expiredAt, transfer, upload, download, banned, remindExpire, remindTraffic, user.ID); updateErr != nil {
			t.Fatal(updateErr)
		}
		return user.ID
	}
	eligibleExpiry := now.Add(23 * time.Hour).Unix()
	eligibleID := create("both-reminders@example.test", &eligibleExpiry, 1_000, 400, 400, false, true, true)
	exactDay := now.Add(24 * time.Hour).Unix()
	create("exact-day@example.test", &exactDay, 1_000, 799, 0, false, true, true)
	expired := now.Unix()
	create("expired@example.test", &expired, 1_000, 1_000, 0, false, true, true)
	create("banned@example.test", &eligibleExpiry, 1_000, 900, 0, true, true, true)
	create("disabled-preference@example.test", &eligibleExpiry, 1_000, 900, 0, false, false, false)

	first, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 1)
	if err != nil || first.ExpireQueued != 1 || first.TrafficQueued != 1 {
		t.Fatalf("first ScheduleSubscriptionReminders()=%#v err=%v", first, err)
	}
	second, err := database.ScheduleSubscriptionReminders(ctx, now, "2026-08-29", 1)
	if err != nil || second.ExpireQueued != 0 || second.TrafficQueued != 0 {
		t.Fatalf("second ScheduleSubscriptionReminders()=%#v err=%v", second, err)
	}
	var count, distinctUsers int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(DISTINCT user_id) FROM subscription_reminder_outbox
	`).Scan(&count, &distinctUsers); err != nil || count != 2 || distinctUsers != 1 {
		t.Fatalf("reminder rows=%d users=%d err=%v", count, distinctUsers, err)
	}
	var queuedUserID int64
	if err := database.db.QueryRowContext(ctx, `SELECT user_id FROM subscription_reminder_outbox LIMIT 1`).Scan(&queuedUserID); err != nil || queuedUserID != eligibleID {
		t.Fatalf("queued user=%d want=%d err=%v", queuedUserID, eligibleID, err)
	}

	claimedKinds := map[SubscriptionReminderKind]bool{}
	for index := 0; index < 2; index++ {
		claimToken := "worker-" + string(rune('a'+index))
		job, claimed, claimErr := database.ClaimSubscriptionReminder(ctx, claimToken, now, time.Minute)
		if claimErr != nil || !claimed {
			t.Fatalf("ClaimSubscriptionReminder(%s)=(%#v,%v,%v)", claimToken, job, claimed, claimErr)
		}
		claimedKinds[job.Kind] = true
		if completeErr := database.CompleteSubscriptionReminder(ctx, job.ID, claimToken, now); completeErr != nil {
			t.Fatal(completeErr)
		}
	}
	if !claimedKinds[SubscriptionReminderExpire] || !claimedKinds[SubscriptionReminderTraffic] {
		t.Fatalf("claimed reminder kinds=%#v", claimedKinds)
	}
	if _, claimed, err := database.ClaimSubscriptionReminder(ctx, "worker-empty", now, time.Minute); err != nil || claimed {
		t.Fatalf("completed reminders remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func TestSchemaV46AddsMailReminderStateWithoutChangingExistingSettings(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name = 'V45 preserved', revision = 17;
		DROP TABLE subscription_reminder_outbox;
		ALTER TABLE app_settings DROP COLUMN remind_mail_enable;
		PRAGMA user_version = 45;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v45 to v46) error=%v", err)
	}
	settings, err := database.GetMailSettings(ctx)
	if err != nil || settings.Revision != 17 || settings.RemindMailEnabled {
		t.Fatalf("migrated mail settings=%#v err=%v", settings, err)
	}
	var version, tableCount int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='subscription_reminder_outbox'`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || tableCount != 1 {
		t.Fatalf("schema version=%d reminder table=%d", version, tableCount)
	}
}
