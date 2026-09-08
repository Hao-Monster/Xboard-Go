package mailer

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestClaimedMailIsNotSentAfterUserLifecycleCancellation(t *testing.T) {
	for _, action := range []string{"deactivate", "anonymize"} {
		t.Run(action, func(t *testing.T) {
			ctx := t.Context()
			now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
			db, err := store.OpenSQLite(filepath.Join(t.TempDir(), "claimed-lifecycle.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			admin, err := db.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "lifecycle-mail-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
			if err != nil {
				t.Fatal(err)
			}
			settings, err := db.GetTicketSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.UpdateTicketSettings(ctx, admin.ID, settings.Revision, store.SaveTicketSettingsInput{
				AppName: "Lifecycle Board", AppURL: "https://panel.example.test", SMTPEnabled: true,
				SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: EncryptionNone, SMTPFromAddress: "support@example.test",
			}, now); err != nil {
				t.Fatal(err)
			}
			site, err := db.GetSiteSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.UpdateSiteSettings(ctx, admin.ID, site.Revision, store.SaveSiteSettingsInput{
				AppName: site.AppName, AppURL: site.AppURL, EmailVerificationEnabled: true, MailLoginEnabled: true,
				RegistrationIPLimitCount: site.RegistrationIPLimitCount, RegistrationIPLimitMinutes: site.RegistrationIPLimitMinutes,
				InvitationCodeLimit: site.InvitationCodeLimit, PasswordLimitEnabled: site.PasswordLimitEnabled,
				PasswordLimitCount: site.PasswordLimitCount, PasswordLimitMinutes: site.PasswordLimitMinutes,
			}, now); err != nil {
				t.Fatal(err)
			}
			mail, err := db.GetMailSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.UpdateMailSettings(ctx, admin.ID, mail.Revision, store.SaveMailSettingsInput{
				SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: EncryptionNone,
				SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
			}, now); err != nil {
				t.Fatal(err)
			}
			resetProtector, err := security.NewPasswordResetProtector(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			registrationProtector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			linkProtector, err := security.NewLoginLinkProtector(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			const email = "lifecycle-mail-owner@example.test"
			const claim = "lifecycle-held-claim"
			registrationEmailDigest, _ := registrationProtector.EmailDigest(email)
			registrationCodeDigest, _ := registrationProtector.CodeDigest(email, "123456")
			registrationCipher, err := registrationProtector.EncryptCode(email, "123456")
			if err != nil {
				t.Fatal(err)
			}
			if queued, err := db.RequestRegistrationEmailVerification(ctx, store.RegistrationEmailVerificationRequestInput{
				Email: email, SourceIP: "127.0.0.1", EmailDigest: registrationEmailDigest, CodeDigest: registrationCodeDigest, CodeCipher: registrationCipher,
			}, now); err != nil || !queued {
				t.Fatalf("queue registration: queued=%v err=%v", queued, err)
			}
			registrationJob, claimed, err := db.ClaimRegistrationEmailVerificationMail(ctx, claim, now, time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim registration: claimed=%v err=%v", claimed, err)
			}
			expires := now.Add(time.Hour)
			user, err := db.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: email, PasswordHash: "hash", TransferEnable: 1000, ExpiredAt: &expires}, now)
			if err != nil {
				t.Fatal(err)
			}
			resetEmailDigest, _ := resetProtector.EmailDigest(email)
			resetCodeDigest, _ := resetProtector.CodeDigest(email, "654321")
			resetCipher, err := resetProtector.EncryptCode(email, "654321")
			if err != nil {
				t.Fatal(err)
			}
			if queued, err := db.RequestPasswordReset(ctx, store.PasswordResetRequestInput{Email: email, EmailDigest: resetEmailDigest, CodeDigest: resetCodeDigest, CodeCipher: resetCipher}, now); err != nil || !queued {
				t.Fatalf("queue reset: queued=%v err=%v", queued, err)
			}
			resetJob, claimed, err := db.ClaimPasswordResetMail(ctx, claim, now, time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim reset: claimed=%v err=%v", claimed, err)
			}
			token, err := linkProtector.NewToken()
			if err != nil {
				t.Fatal(err)
			}
			linkEmailDigest, _ := linkProtector.EmailDigest(email)
			linkTokenDigest, _ := linkProtector.TokenDigest(security.LoginLinkPurposeEmail, token)
			linkCipher, err := linkProtector.EncryptToken(user.ID, token)
			if err != nil {
				t.Fatal(err)
			}
			if queued, err := db.RequestMailLoginLink(ctx, store.MailLoginLinkRequestInput{Email: email, ExpectedUserID: user.ID, EmailDigest: linkEmailDigest, TokenDigest: linkTokenDigest, TokenCipher: linkCipher, Redirect: "invite", LinkBaseURL: "https://panel.example.test"}, now); err != nil || !queued {
				t.Fatalf("queue login link: queued=%v err=%v", queued, err)
			}
			linkJob, claimed, err := db.ClaimLoginLinkMail(ctx, claim, now, time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim login link: claimed=%v err=%v", claimed, err)
			}
			ticket, err := db.CreateTicket(ctx, user.ID, store.SaveTicketInput{Subject: "Private subject", Level: store.TicketLevelHigh, Message: "Private request"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "Private reply", now); err != nil {
				t.Fatal(err)
			}
			ticketJob, claimed, err := db.ClaimTicketMail(ctx, claim, now, time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim ticket: claimed=%v err=%v", claimed, err)
			}
			if queued, err := db.ScheduleSubscriptionReminders(ctx, now, "2026-09-08", 500); err != nil || queued.ExpireQueued != 1 {
				t.Fatalf("queue reminders: queued=%#v err=%v", queued, err)
			}
			reminderJob, claimed, err := db.ClaimSubscriptionReminder(ctx, claim, now, time.Minute)
			if err != nil || !claimed {
				t.Fatalf("claim reminder: claimed=%v err=%v", claimed, err)
			}
			current, err := db.GetAdminUser(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			inactive, _, err := db.ChangeUserLifecycle(ctx, store.UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: current.Revision, Action: "deactivate"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if action == "anonymize" {
				if _, _, err := db.ChangeUserLifecycle(ctx, store.UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: inactive.Revision, Action: "anonymize"}, now.Add(31*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			sender := &recordingSender{}
			worker := NewWorker(db, nil, resetProtector, registrationProtector, linkProtector, sender, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
			for _, delivery := range []struct {
				kind string
				run  func() error
			}{
				{"ticket", func() error { return worker.deliverTicket(ctx, ticketJob, claim, now) }},
				{"password_reset", func() error { return worker.deliverPasswordReset(ctx, resetJob, claim, now) }},
				{"registration", func() error { return worker.deliverRegistrationEmailVerification(ctx, registrationJob, claim, now) }},
				{"login_link", func() error { return worker.deliverLoginLink(ctx, linkJob, claim, now) }},
				{"subscription_reminder", func() error { return worker.deliverSubscriptionReminder(ctx, reminderJob, claim, now) }},
			} {
				before := len(sender.messages)
				err := delivery.run()
				if len(sender.messages) != before {
					t.Errorf("%s disclosed claimed message after %s", delivery.kind, action)
				}
				if err != nil {
					t.Errorf("cancelled %s delivery returned error: %v", delivery.kind, err)
				}
			}
		})
	}
}
