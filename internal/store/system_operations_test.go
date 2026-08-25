package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestAdminAuditLogIsAppendOnlyFilteredAndDoesNotStoreRequestBodies(t *testing.T) {
	database, err := OpenSQLite(fmt.Sprintf("file:system-operations-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "operator@example.test", "hash", time.Now()); err != nil {
		t.Fatal(err)
	}
	admin, err := database.FindUserByEmail(ctx, "operator@example.test")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 13, 0, 0, 0, time.UTC)
	for index, input := range []AdminAuditInput{
		{AdministratorID: admin.ID, AdministratorEmail: admin.Email, Method: "PUT", Route: "/api/v1/admin/ticket-settings", StatusCode: 200},
		{AdministratorID: admin.ID, AdministratorEmail: admin.Email, Method: "POST", Route: "/api/v1/admin/notices", StatusCode: 422},
	} {
		if err := database.RecordAdminAudit(ctx, input, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatalf("RecordAdminAudit(%d) error = %v", index, err)
		}
	}
	page, err := database.ListAdminAuditLogs(ctx, AdminAuditFilter{Page: 1, PageSize: 20, Method: "PUT", Query: "ticket-settings"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("audit page = %#v, want one matching item", page)
	}
	item := page.Items[0]
	if item.AdministratorEmail != admin.Email || item.Method != "PUT" || item.StatusCode != 200 || item.Route != "/api/v1/admin/ticket-settings" {
		t.Fatalf("audit item = %#v", item)
	}
	if err := database.RecordAdminAudit(ctx, AdminAuditInput{AdministratorID: admin.ID, AdministratorEmail: admin.Email, Method: "GET", Route: "/api/v1/admin/users", StatusCode: 200}, now); err == nil {
		t.Fatal("read-only method was accepted as an audit mutation")
	}
}

func TestSystemQueueStatsAndFailedMailPaginationExcludeMessageBodies(t *testing.T) {
	database, err := OpenSQLite(fmt.Sprintf("file:system-queue-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 14, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(ctx, "queue-admin@example.test", "hash", now); err != nil {
		t.Fatal(err)
	}
	admin, err := database.FindUserByEmail(ctx, "queue-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "queue-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := database.CreateTicket(ctx, user.ID, SaveTicketInput{Subject: "Queue subject", Level: TicketLevelMedium, Message: "private initial body"}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateTicketSettings(ctx, admin.ID, settings.Revision, SaveTicketSettingsInput{
		AppName: "Queue Test", SMTPEnabled: true, SMTPHost: "mailpit", SMTPPort: 1025,
		SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReplyTicketAsAdmin(ctx, admin.ID, ticket.ID, "private reply body", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	job, claimed, err := database.ClaimTicketMail(ctx, "system-queue-claim", now.Add(time.Second), 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("ClaimTicketMail() = (%#v, %v, %v)", job, claimed, err)
	}
	if err := database.FailTicketMail(ctx, job.ID, "system-queue-claim", "connection refused", now.Add(time.Minute), now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	stats, err := database.GetSystemQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 1 || stats.Failed != 0 || stats.Sent != 0 {
		t.Fatalf("queue stats after retryable failure = %#v", stats)
	}
	job, claimed, err = database.ClaimTicketMail(ctx, "system-queue-claim-2", now.Add(2*time.Minute), 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("second ClaimTicketMail() = (%#v, %v, %v)", job, claimed, err)
	}
	if err := database.FailTicketMail(ctx, job.ID, "system-queue-claim-2", "still refused", now.Add(7*time.Minute), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	job, claimed, err = database.ClaimTicketMail(ctx, "system-queue-claim-3", now.Add(8*time.Minute), 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("third ClaimTicketMail() = (%#v, %v, %v)", job, claimed, err)
	}
	if err := database.FailTicketMail(ctx, job.ID, "system-queue-claim-3", "permanent refusal", now.Add(8*time.Minute), now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stats, err = database.GetSystemQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pending != 0 || stats.Failed != 1 || stats.Sent != 0 {
		t.Fatalf("final queue stats = %#v", stats)
	}
	page, err := database.ListTicketMailFailures(ctx, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Kind != "ticket" || page.Items[0].LastError != "permanent refusal" || page.Items[0].Recipient != user.Email {
		t.Fatalf("failed mail page = %#v", page)
	}

	protector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	emailDigest, _ := protector.EmailDigest(user.Email)
	codeDigest, _ := protector.CodeDigest(user.Email, "382741")
	codeCipher, _ := protector.EncryptCode(user.Email, "382741")
	if queued, err := database.RequestPasswordReset(ctx, PasswordResetRequestInput{
		Email: user.Email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now.Add(9*time.Minute)); err != nil || !queued {
		t.Fatalf("RequestPasswordReset() = (%v, %v)", queued, err)
	}
	for attempt, claimAt := range []time.Time{now.Add(9 * time.Minute), now.Add(9*time.Minute + 20*time.Second), now.Add(10 * time.Minute)} {
		token := fmt.Sprintf("reset-failure-%d", attempt)
		resetJob, claimed, err := database.ClaimPasswordResetMail(ctx, token, claimAt, time.Minute)
		if err != nil || !claimed {
			t.Fatalf("password reset claim %d = (%#v, %v, %v)", attempt+1, resetJob, claimed, err)
		}
		retryDelay := 10 * time.Second
		if attempt == 1 {
			retryDelay = 30 * time.Second
		} else if attempt >= 2 {
			retryDelay = 0
		}
		if err := database.FailPasswordResetMail(ctx, resetJob.ID, token, "reset delivery refused", claimAt.Add(retryDelay), claimAt); err != nil {
			t.Fatal(err)
		}
	}
	stats, err = database.GetSystemQueueStats(ctx)
	if err != nil || stats.Failed != 2 || stats.Pending != 0 {
		t.Fatalf("combined queue stats = %#v err=%v", stats, err)
	}
	page, err = database.ListTicketMailFailures(ctx, 1, 20)
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.Items[0].Kind != "password_reset" ||
		page.Items[0].ID >= 0 || page.Items[0].TicketSubject != "密码重置验证码" || page.Items[0].Recipient != user.Email {
		t.Fatalf("combined failed mail page = %#v err=%v", page, err)
	}
	if strings.Contains(fmt.Sprint(page.Items), "382741") {
		t.Fatal("failed mail diagnostics exposed the password reset code")
	}

	siteSettings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateSiteSettings(ctx, admin.ID, siteSettings.Revision, SaveSiteSettingsInput{
		AppName: siteSettings.AppName, AppDescription: siteSettings.AppDescription, AppURL: siteSettings.AppURL,
		TOSURL: siteSettings.TOSURL, Logo: siteSettings.Logo, EmailVerificationEnabled: true,
		EmailWhitelistSuffixes:     siteSettings.EmailWhitelistSuffixes,
		RegistrationIPLimitCount:   siteSettings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: siteSettings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       siteSettings.PasswordLimitEnabled,
		PasswordLimitCount:         siteSettings.PasswordLimitCount,
		PasswordLimitMinutes:       siteSettings.PasswordLimitMinutes,
	}, now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	registrationProtector, _ := security.NewRegistrationEmailProtector(make([]byte, 32))
	const registrationEmail = "queue-registration@example.test"
	registrationEmailDigest, _ := registrationProtector.EmailDigest(registrationEmail)
	registrationCodeDigest, _ := registrationProtector.CodeDigest(registrationEmail, "593817")
	registrationCodeCipher, _ := registrationProtector.EncryptCode(registrationEmail, "593817")
	if queued, err := database.RequestRegistrationEmailVerification(ctx, RegistrationEmailVerificationRequestInput{
		Email: registrationEmail, SourceIP: "127.0.0.1", EmailDigest: registrationEmailDigest,
		CodeDigest: registrationCodeDigest, CodeCipher: registrationCodeCipher,
	}, now.Add(11*time.Minute)); err != nil || !queued {
		t.Fatalf("RequestRegistrationEmailVerification() = (%v, %v)", queued, err)
	}
	for attempt, claimAt := range []time.Time{now.Add(11 * time.Minute), now.Add(11*time.Minute + 20*time.Second), now.Add(12 * time.Minute)} {
		token := fmt.Sprintf("registration-failure-%d", attempt)
		registrationJob, claimed, err := database.ClaimRegistrationEmailVerificationMail(ctx, token, claimAt, time.Minute)
		if err != nil || !claimed {
			t.Fatalf("registration mail claim %d = (%#v, %v, %v)", attempt+1, registrationJob, claimed, err)
		}
		retryDelay := 10 * time.Second
		if attempt == 1 {
			retryDelay = 30 * time.Second
		} else if attempt >= 2 {
			retryDelay = 0
		}
		if err := database.FailRegistrationEmailVerificationMail(ctx, registrationJob.ID, token, "registration delivery refused", claimAt.Add(retryDelay), claimAt); err != nil {
			t.Fatal(err)
		}
	}
	stats, err = database.GetSystemQueueStats(ctx)
	if err != nil || stats.Failed != 3 || stats.Pending != 0 {
		t.Fatalf("three-kind queue stats = %#v err=%v", stats, err)
	}
	page, err = database.ListTicketMailFailures(ctx, 1, 20)
	if err != nil || page.Total != 3 || len(page.Items) != 3 || page.Items[0].Kind != "registration_email_verification" ||
		page.Items[0].TicketSubject != "注册邮箱验证码" || page.Items[0].Recipient != registrationEmail {
		t.Fatalf("three-kind failed mail page = %#v err=%v", page, err)
	}
	if strings.Contains(fmt.Sprint(page.Items), "593817") {
		t.Fatal("failed mail diagnostics exposed the registration email code")
	}

	latestSite, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mailLoginSettings := siteSettingsInput(latestSite)
	mailLoginSettings.MailLoginEnabled = true
	if _, err := database.UpdateSiteSettings(ctx, admin.ID, latestSite.Revision, mailLoginSettings, now.Add(13*time.Minute)); err != nil {
		t.Fatal(err)
	}
	loginProtector, _ := security.NewLoginLinkProtector(make([]byte, 32))
	loginToken, _ := loginProtector.NewToken()
	loginEmailDigest, _ := loginProtector.EmailDigest(user.Email)
	loginTokenDigest, _ := loginProtector.TokenDigest(security.LoginLinkPurposeEmail, loginToken)
	loginTokenCipher, _ := loginProtector.EncryptToken(user.ID, loginToken)
	if queued, err := database.RequestMailLoginLink(ctx, MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: loginEmailDigest, TokenDigest: loginTokenDigest, TokenCipher: loginTokenCipher,
		Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}, now.Add(13*time.Minute)); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() = (%v, %v)", queued, err)
	}
	for attempt, claimAt := range []time.Time{now.Add(13 * time.Minute), now.Add(13*time.Minute + 20*time.Second), now.Add(14 * time.Minute)} {
		token := fmt.Sprintf("login-link-failure-%d", attempt)
		loginJob, claimed, err := database.ClaimLoginLinkMail(ctx, token, claimAt, time.Minute)
		if err != nil || !claimed {
			t.Fatalf("login link mail claim %d = (%#v, %v, %v)", attempt+1, loginJob, claimed, err)
		}
		retryDelay := 10 * time.Second
		if attempt == 1 {
			retryDelay = 30 * time.Second
		} else if attempt >= 2 {
			retryDelay = 0
		}
		if err := database.FailLoginLinkMail(ctx, loginJob.ID, token, "login link delivery refused", claimAt.Add(retryDelay), claimAt); err != nil {
			t.Fatal(err)
		}
	}
	stats, err = database.GetSystemQueueStats(ctx)
	if err != nil || stats.Failed != 4 || stats.Pending != 0 {
		t.Fatalf("four-kind queue stats = %#v err=%v", stats, err)
	}
	page, err = database.ListTicketMailFailures(ctx, 1, 20)
	if err != nil || page.Total != 4 || len(page.Items) != 4 || page.Items[0].Kind != "login_link" ||
		page.Items[0].TicketSubject != "邮件登录链接" || page.Items[0].Recipient != user.Email {
		t.Fatalf("four-kind failed mail page = %#v err=%v", page, err)
	}
	if strings.Contains(fmt.Sprint(page.Items), loginToken) {
		t.Fatal("failed mail diagnostics exposed the login token")
	}
}

func TestMigrationFromSchemaV11AddsFailedMailIndexWithoutChangingAuditData(t *testing.T) {
	database, err := OpenSQLite(fmt.Sprintf("file:system-v11-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "migration-admin@example.test", "hash", time.Now()); err != nil {
		t.Fatal(err)
	}
	admin, err := database.FindUserByEmail(ctx, "migration-admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.RecordAdminAudit(ctx, AdminAuditInput{
		AdministratorID: admin.ID, AdministratorEmail: admin.Email, Method: "PUT",
		Route: "/api/v1/admin/ticket-settings", StatusCode: 200,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP INDEX idx_ticket_mail_outbox_failed`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE app_settings DROP COLUMN app_description`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE app_settings DROP COLUMN tos_url`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE app_settings DROP COLUMN logo`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE app_settings DROP COLUMN stop_register`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TABLE registration_ip_limits`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TABLE invitation_codes`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP INDEX idx_users_invite_user_id`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE users DROP COLUMN invite_user_id`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"legacy_migration_runs",
		"access_tokens",
		"login_failure_limits",
		"login_link_mail_outbox", "login_link_tokens", "mail_login_request_limits",
		"registration_email_mail_outbox", "registration_email_challenges",
		"password_reset_mail_outbox", "password_reset_challenges",
	} {
		if _, err := database.db.ExecContext(ctx, `DROP TABLE `+table); err != nil {
			t.Fatal(err)
		}
	}
	for _, column := range []string{
		"invite_force", "invite_gen_limit", "invite_never_expire",
		"login_with_mail_link_enable",
		"email_verify",
		"email_whitelist_enable", "email_whitelist_suffix", "email_gmail_limit_enable",
		"register_limit_by_ip_enable", "register_limit_count", "register_limit_expire",
		"password_limit_enable", "password_limit_count", "password_limit_expire",
		"captcha_enable", "captcha_type", "recaptcha_site_key", "recaptcha_secret_cipher",
		"recaptcha_v3_site_key", "recaptcha_v3_score_threshold", "recaptcha_v3_secret_cipher",
		"turnstile_site_key", "turnstile_secret_cipher",
	} {
		if _, err := database.db.ExecContext(ctx, `ALTER TABLE app_settings DROP COLUMN `+column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `ALTER TABLE users DROP COLUMN last_login_at`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 11`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v11 to current) error = %v", err)
	}
	var version, auditCount, indexCount int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_audit_logs`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_ticket_mail_outbox_failed'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || auditCount != 1 || indexCount != 1 {
		t.Fatalf("migration result: version=%d audits=%d failed_index=%d", version, auditCount, indexCount)
	}
}
