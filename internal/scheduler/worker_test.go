package scheduler

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
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

func TestWorkerAutomaticallyClosesTicketsAnsweredMoreThanOneDayAgo(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-ticket-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "worker-ticket-user@example.test", PasswordHash: "hash"}, now.Add(-26*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "worker-ticket-admin@example.test", PasswordHash: "hash"}, now.Add(-26*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, store.SaveTicketInput{Subject: "stale answer", Level: store.TicketLevelLow, Message: "question"}, now.Add(-26*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "answer", now.Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now }
	worker.applyDue(ctx)

	updated, err := database.GetAdminTicket(ctx, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != store.TicketStatusClosed {
		t.Fatalf("ticket status = %d, want closed", updated.Status)
	}
}

func TestWorkerPrunesExpiredRegistrationIPState(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-registration-ip-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "registration-ip-worker-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, store.SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: settings.StopRegister,
		EmailWhitelistSuffixes:     settings.EmailWhitelistSuffixes,
		RegistrationIPLimitEnabled: true, RegistrationIPLimitCount: 3, RegistrationIPLimitMinutes: 1,
		PasswordLimitEnabled: settings.PasswordLimitEnabled, PasswordLimitCount: settings.PasswordLimitCount,
		PasswordLimitMinutes: settings.PasswordLimitMinutes,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterUser(ctx, store.RegisterUserInput{
		Email: "registration-ip-worker@example.test", PasswordHash: "hash", SourceIP: "192.0.2.50",
	}, now); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now.Add(time.Minute) }
	worker.applyDue(ctx)
	if removed, err := database.PruneExpiredRegistrationIPLimits(ctx, now.Add(time.Minute), 100); err != nil || removed != 0 {
		t.Fatalf("worker left expired registration IP state: removed=%d err=%v", removed, err)
	}
}

func TestWorkerPrunesExpiredLoginFailureState(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-login-failure-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 1, 0, 0, 0, time.UTC)
	digest := make([]byte, 32)
	digest[0] = 0x7f
	if _, err := database.RecordLoginFailure(ctx, digest, now); err != nil {
		t.Fatal(err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now.Add(61 * time.Minute) }
	worker.applyDue(ctx)
	if removed, err := database.PruneExpiredLoginFailureLimits(ctx, now.Add(61*time.Minute), 100); err != nil || removed != 0 {
		t.Fatalf("worker left expired login failure state: removed=%d err=%v", removed, err)
	}
}

func TestWorkerPrunesExpiredPasswordResetChallengesAndEncryptedMail(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-password-reset-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 5, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "worker-reset@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, settings.Revision, store.SaveTicketSettingsInput{
		AppName: "Worker Reset", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewPasswordResetProtector(make([]byte, 32))
	emailDigest, _ := protector.EmailDigest(user.Email)
	codeDigest, _ := protector.CodeDigest(user.Email, "789012")
	codeCipher, _ := protector.EncryptCode(user.Email, "789012")
	if queued, err := database.RequestPasswordReset(ctx, store.PasswordResetRequestInput{
		Email: user.Email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now.Add(5 * time.Minute) }
	worker.applyDue(ctx)
	if removed, err := database.PruneExpiredPasswordResets(ctx, now.Add(5*time.Minute), 100); err != nil || removed != 0 {
		t.Fatalf("worker left expired password reset state: removed=%d err=%v", removed, err)
	}
	if _, claimed, err := database.ClaimPasswordResetMail(ctx, "expired-worker-check", now.Add(5*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("expired password reset mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func TestWorkerPrunesExpiredRegistrationEmailChallengesAndEncryptedMail(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-registration-email-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "worker-registration@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, _ := database.GetTicketSettings(ctx)
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: "Worker Registration", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, _ := database.GetSiteSettings(ctx)
	if _, err := database.UpdateSiteSettings(ctx, administrator.ID, siteSettings.Revision, store.SaveSiteSettingsInput{
		AppName: siteSettings.AppName, EmailVerificationEnabled: true,
		EmailWhitelistSuffixes:     siteSettings.EmailWhitelistSuffixes,
		RegistrationIPLimitCount:   siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       siteSettings.PasswordLimitEnabled,
		PasswordLimitCount:         siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:       siteSettings.PasswordLimitMinutes,
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewRegistrationEmailProtector(make([]byte, 32))
	const email = "worker-registration-new@example.test"
	emailDigest, _ := protector.EmailDigest(email)
	codeDigest, _ := protector.CodeDigest(email, "890123")
	codeCipher, _ := protector.EncryptCode(email, "890123")
	if queued, err := database.RequestRegistrationEmailVerification(ctx, store.RegistrationEmailVerificationRequestInput{
		Email: email, SourceIP: "127.0.0.1", EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now); err != nil || !queued {
		t.Fatalf("RequestRegistrationEmailVerification() = (%v, %v)", queued, err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now.Add(5 * time.Minute) }
	worker.applyDue(ctx)
	if removed, err := database.PruneExpiredRegistrationEmailVerifications(ctx, now.Add(5*time.Minute), 100); err != nil || removed != 0 {
		t.Fatalf("worker left expired registration email state: removed=%d err=%v", removed, err)
	}
	if _, claimed, err := database.ClaimRegistrationEmailVerificationMail(ctx, "expired-registration-check", now.Add(5*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("expired registration email remained claimable: claimed=%v err=%v", claimed, err)
	}
}

func TestWorkerPrunesExpiredLoginLinksAndEncryptedMail(t *testing.T) {
	database, err := store.OpenSQLite(fmt.Sprintf("file:worker-login-link-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := t.Context()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "worker-login-link@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, _ := database.GetTicketSettings(ctx)
	if _, err := database.UpdateTicketSettings(ctx, user.ID, ticketSettings.Revision, store.SaveTicketSettingsInput{
		AppName: "Worker Login", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now); err != nil {
		t.Fatal(err)
	}
	siteSettings, _ := database.GetSiteSettings(ctx)
	if _, err := database.UpdateSiteSettings(ctx, user.ID, siteSettings.Revision, store.SaveSiteSettingsInput{
		AppName: siteSettings.AppName, RegistrationIPLimitCount: siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes, InvitationCodeLimit: siteSettings.InvitationCodeLimit,
		PasswordLimitEnabled: siteSettings.PasswordLimitEnabled, PasswordLimitCount: siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:  siteSettings.PasswordLimitMinutes,
		InvitationNeverExpire: siteSettings.InvitationNeverExpire, MailLoginEnabled: true,
	}, now); err != nil {
		t.Fatal(err)
	}
	protector, _ := security.NewLoginLinkProtector(make([]byte, 32))
	token, _ := protector.NewToken()
	emailDigest, _ := protector.EmailDigest(user.Email)
	tokenDigest, _ := protector.TokenDigest(security.LoginLinkPurposeEmail, token)
	tokenCipher, _ := protector.EncryptToken(user.ID, token)
	if queued, err := database.RequestMailLoginLink(ctx, store.MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: emailDigest, TokenDigest: tokenDigest, TokenCipher: tokenCipher,
		Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}, now); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() = (%v, %v)", queued, err)
	}

	worker := NewWorker(database, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	worker.now = func() time.Time { return now.Add(5 * time.Minute) }
	worker.applyDue(ctx)
	if removed, err := database.PruneExpiredLoginLinks(ctx, now.Add(5*time.Minute), 100); err != nil || removed != 0 {
		t.Fatalf("worker left expired login link state: removed=%d err=%v", removed, err)
	}
	if _, claimed, err := database.ClaimLoginLinkMail(ctx, "expired-login-link-check", now.Add(5*time.Minute), time.Minute); err != nil || claimed {
		t.Fatalf("expired login link mail remained claimable: claimed=%v err=%v", claimed, err)
	}
}
