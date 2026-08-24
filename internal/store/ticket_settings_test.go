package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTicketSettingsUseOptimisticRevisionAndControlConsecutiveUserReplies(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "settings-ticket-user@example.test", now)
	admin := createTicketTestUser(t, database, "settings-ticket-admin@example.test", now)

	initial, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 1 || initial.TicketMustWaitReply || initial.SMTPEnabled || initial.SMTPPasswordSet {
		t.Fatalf("initial settings = %#v", initial)
	}
	updated, err := database.UpdateTicketSettings(ctx, admin.ID, initial.Revision, SaveTicketSettingsInput{
		AppName: "Example", AppURL: "https://panel.example.test", TicketMustWaitReply: true,
		SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPUsername: "mailer",
		SMTPEncryption: "starttls", SMTPFromAddress: "support@example.test",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: []byte("encrypted-not-plaintext"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || !updated.TicketMustWaitReply || !updated.SMTPPasswordSet || len(updated.SMTPPasswordCipher) != 0 {
		t.Fatalf("updated settings = %#v", updated)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, initial.Revision, SaveTicketSettingsInput{AppName: "stale"}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateTicketSettings() error = %v, want ErrConflict", err)
	}

	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Wait rule", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "consecutive", now.Add(time.Minute)); !errors.Is(err, ErrTicketReplyPending) {
		t.Fatalf("consecutive ReplyTicketAsUser() error = %v, want ErrTicketReplyPending", err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "administrator", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsUser(ctx, user.ID, ticket.ID, "after administrator", now.Add(3*time.Minute)); err != nil {
		t.Fatalf("ReplyTicketAsUser(after administrator) error = %v", err)
	}
}

func TestAdministratorTicketRepliesCreateThrottledDurableMailJobs(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "mail-ticket-user@example.test", now)
	admin := createTicketTestUser(t, database, "mail-ticket-admin@example.test", now)
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Mail Test", AppURL: "https://panel.example.test", SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Mail notification", Level: TicketLevelMedium, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "first answer", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "suppressed answer", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, configured.Revision, SaveTicketSettingsInput{
		AppName: "Changed After Enqueue", AppURL: "https://changed.example.test", SMTPEnabled: true,
		SMTPHost: "smtp.changed.test", SMTPPort: 465, SMTPEncryption: "tls", SMTPFromAddress: "changed@example.test",
	}, now.Add(150*time.Second)); err != nil {
		t.Fatal(err)
	}

	job, ok, err := database.ClaimTicketMail(ctx, "worker-one", now.Add(3*time.Minute), time.Minute)
	if err != nil || !ok {
		t.Fatalf("ClaimTicketMail() = (%#v, %v, %v)", job, ok, err)
	}
	if job.Recipient != user.Email || job.Subject != ticket.Subject || job.Message != "first answer" || job.AppName != "Mail Test" ||
		job.AppURL != "https://panel.example.test" || job.SMTPHost != "smtp.changed.test" || job.Attempt != 1 {
		t.Fatalf("claimed job = %#v", job)
	}
	if err := database.CompleteTicketMail(ctx, job.ID, "worker-one", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := database.ClaimTicketMail(ctx, "worker-one", now.Add(5*time.Minute), time.Minute); err != nil || ok {
		t.Fatalf("suppressed job claim = (%v, %v), want no job", ok, err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "after throttle", now.Add(32*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := database.ClaimTicketMail(ctx, "worker-two", now.Add(33*time.Minute), time.Minute); err != nil || !ok {
		t.Fatalf("post-throttle claim = (%v, %v), want job", ok, err)
	}
}

func TestTicketMailClaimIsExclusiveAcrossWorkers(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "claim-ticket-user@example.test", now)
	admin := createTicketTestUser(t, database, "claim-ticket-admin@example.test", now)
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Claim", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Claim", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "answer", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	results := make(chan bool, 2)
	for _, token := range []string{"worker-a", "worker-b"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, ok, claimErr := database.ClaimTicketMail(ctx, token, now.Add(2*time.Minute), time.Minute)
			if claimErr != nil {
				t.Errorf("ClaimTicketMail() error = %v", claimErr)
			}
			results <- ok
		}()
	}
	wait.Wait()
	close(results)
	claimed := 0
	for ok := range results {
		if ok {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed workers = %d, want 1", claimed)
	}
}

func TestDisablingSMTPNotificationsCancelsUnclaimedMail(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "cancel-mail-user@example.test", now)
	admin := createTicketTestUser(t, database, "cancel-mail-admin@example.test", now)
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings, err = database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Cancel Mail", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Cancel queued mail", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "queued answer", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	settings, err = database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Cancel Mail", SMTPPort: 1025, SMTPEncryption: "none",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Cancel Mail", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimTicketMail(ctx, "worker-after-reenable", now.Add(4*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("ClaimTicketMail(after re-enable) = (%v, %v), want cancelled queue", claimed, err)
	}
}

func TestDisabledEmailDoesNotQueueStaleNotificationsAndTransportIsValidated(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 14, 0, 0, 0, time.UTC)
	user := createTicketTestUser(t, database, "disabled-mail-user@example.test", now)
	admin := createTicketTestUser(t, database, "disabled-mail-admin@example.test", now)
	initial, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, initial.Revision, SaveTicketSettingsInput{
		AppName: "Invalid", SMTPEnabled: true, SMTPHost: "bad:host", SMTPPort: 587,
		SMTPEncryption: "starttls", SMTPFromAddress: "support@example.test",
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid SMTP host error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, initial.Revision, SaveTicketSettingsInput{
		AppName: "Invalid", SMTPUsername: "mailer\r\nadmin", SMTPPort: 587, SMTPEncryption: "starttls",
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe SMTP username error = %v, want ErrInvalidInput", err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Disabled email", Level: TicketLevelLow, Message: "initial"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "answer while disabled", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, admin.ID, current.Revision, SaveTicketSettingsInput{
		AppName: "Enabled later", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, claimed, err := database.ClaimTicketMail(ctx, "late-worker", now.Add(3*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("mail created while notifications were disabled: claimed=%v err=%v", claimed, err)
	}
}
