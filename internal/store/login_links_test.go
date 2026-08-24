package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestQuickLoginLinkExchangeIsAtomicOneTimeAndConcurrent(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	digest := testLoginLinkDigest(1)
	if err := database.CreateQuickLoginLink(ctx, user.ID, digest, "invite", now); err != nil {
		t.Fatalf("CreateQuickLoginLink() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for index := 0; index < 2; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, err := database.ExchangeLoginLink(ctx, LoginLinkExchangeInput{
				TokenDigest: digest, SessionTokenHash: fmt.Sprintf("login-link-session-%d", index),
				CSRFHash: fmt.Sprintf("login-link-csrf-%d", index), SessionExpiresAt: now.Add(12 * time.Hour),
			}, now)
			results <- err
		}(index)
	}
	close(start)
	workers.Wait()
	close(results)
	var succeeded, invalid int
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrLoginLinkInvalid):
			invalid++
		default:
			t.Fatalf("ExchangeLoginLink() error = %v", err)
		}
	}
	if succeeded != 1 || invalid != 1 {
		t.Fatalf("concurrent exchange results success=%d invalid=%d", succeeded, invalid)
	}
	var sessionCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_sessions WHERE token_hash LIKE 'login-link-session-%'`).Scan(&sessionCount); err != nil || sessionCount != 1 {
		t.Fatalf("created sessions = %d, err=%v", sessionCount, err)
	}
}

func TestQuickLoginLinksAreBoundedPerUserAndNewestLinkRemainsUsable(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := t.Context()
	for marker := byte(1); marker <= 40; marker++ {
		if err := database.CreateQuickLoginLink(ctx, user.ID, testLoginLinkDigest(marker), "dashboard", now); err != nil {
			t.Fatalf("CreateQuickLoginLink(marker=%d) error = %v", marker, err)
		}
	}

	var count int
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM login_link_tokens WHERE user_id = ? AND purpose = 'quick'
	`, user.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 32 {
		t.Fatalf("active quick links = %d, want 32", count)
	}
	for marker, wantCount := range map[byte]int{8: 0, 9: 1, 40: 1} {
		var digestCount int
		if err := database.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM login_link_tokens WHERE token_digest = ?
		`, testLoginLinkDigest(marker)).Scan(&digestCount); err != nil {
			t.Fatal(err)
		}
		if digestCount != wantCount {
			t.Fatalf("marker %d count = %d, want %d", marker, digestCount, wantCount)
		}
	}
}

func TestLoginLinkExchangeRollsBackConsumptionWhenSessionInsertFails(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	digest := testLoginLinkDigest(2)
	if err := database.CreateQuickLoginLink(ctx, user.ID, digest, "knowledge", now); err != nil {
		t.Fatalf("CreateQuickLoginLink() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_login_link_session BEFORE INSERT ON admin_sessions BEGIN SELECT RAISE(ABORT, 'forced session failure'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	input := LoginLinkExchangeInput{
		TokenDigest: digest, SessionTokenHash: "rollback-session", CSRFHash: "rollback-csrf",
		SessionExpiresAt: now.Add(12 * time.Hour),
	}
	if _, err := database.ExchangeLoginLink(ctx, input, now); err == nil || errors.Is(err, ErrLoginLinkInvalid) {
		t.Fatalf("ExchangeLoginLink(forced failure) error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_login_link_session`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	exchanged, err := database.ExchangeLoginLink(ctx, input, now)
	if err != nil || exchanged.User.ID != user.ID || exchanged.Redirect != "knowledge" {
		t.Fatalf("ExchangeLoginLink(retry) = %#v, err=%v", exchanged, err)
	}
}

func TestMailLoginLinkPersistsEqualCooldownWithoutEnumeratingAccounts(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled = 1, login_with_mail_link_enable = 1 WHERE id = 1`); err != nil {
		t.Fatalf("enable mail links: %v", err)
	}

	known := MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: testLoginLinkDigest(10), TokenDigest: testLoginLinkDigest(11),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0001"), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}
	queued, err := database.RequestMailLoginLink(ctx, known, now)
	if err != nil || !queued {
		t.Fatalf("RequestMailLoginLink(known) queued=%v err=%v", queued, err)
	}
	if _, err := database.RequestMailLoginLink(ctx, MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: known.EmailDigest, TokenDigest: testLoginLinkDigest(12),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0002"), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}, now.Add(time.Second)); !errors.Is(err, ErrMailLoginLimited) {
		t.Fatalf("known cooldown error = %v, want ErrMailLoginLimited", err)
	}

	unknown := MailLoginLinkRequestInput{
		Email: "unknown@example.test", EmailDigest: testLoginLinkDigest(20), TokenDigest: testLoginLinkDigest(21),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0003"), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}
	queued, err = database.RequestMailLoginLink(ctx, unknown, now)
	if err != nil || queued {
		t.Fatalf("RequestMailLoginLink(unknown) queued=%v err=%v", queued, err)
	}
	if _, err := database.RequestMailLoginLink(ctx, unknown, now.Add(time.Second)); !errors.Is(err, ErrMailLoginLimited) {
		t.Fatalf("unknown cooldown error = %v, want ErrMailLoginLimited", err)
	}
	mismatchedOwner := MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID + 999, EmailDigest: testLoginLinkDigest(22), TokenDigest: testLoginLinkDigest(23),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0006"), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}
	if queued, err := database.RequestMailLoginLink(ctx, mismatchedOwner, now); err != nil || queued {
		t.Fatalf("mismatched account snapshot queued=%v err=%v", queued, err)
	}
	var tokenCount, mailCount, cooldownCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_link_tokens`).Scan(&tokenCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_link_mail_outbox`).Scan(&mailCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_login_request_limits`).Scan(&cooldownCount); err != nil {
		t.Fatal(err)
	}
	if tokenCount != 1 || mailCount != 1 || cooldownCount != 3 {
		t.Fatalf("known/unknown persistence tokens=%d mail=%d cooldown=%d", tokenCount, mailCount, cooldownCount)
	}
}

func TestMailLoginClaimCompleteAndExpiredPruningEraseCiphertext(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `UPDATE app_settings SET smtp_enabled = 1, login_with_mail_link_enable = 1 WHERE id = 1`); err != nil {
		t.Fatalf("enable mail links: %v", err)
	}
	input := MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: testLoginLinkDigest(30), TokenDigest: testLoginLinkDigest(31),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0004"), Redirect: "invite", LinkBaseURL: "https://panel.example.test",
	}
	if queued, err := database.RequestMailLoginLink(ctx, input, now); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() queued=%v err=%v", queued, err)
	}
	job, claimed, err := database.ClaimLoginLinkMail(ctx, "claim-one", now, 30*time.Second)
	if err != nil || !claimed || job.UserID != user.ID || job.Redirect != "invite" || len(job.TokenCipher) == 0 {
		t.Fatalf("ClaimLoginLinkMail() job=%#v claimed=%v err=%v", job, claimed, err)
	}
	if err := database.CompleteLoginLinkMail(ctx, job.ID, "claim-one", now); err != nil {
		t.Fatalf("CompleteLoginLinkMail() error = %v", err)
	}
	var cipher any
	if err := database.db.QueryRowContext(ctx, `SELECT token_cipher FROM login_link_mail_outbox WHERE id = ?`, job.ID).Scan(&cipher); err != nil || cipher != nil {
		t.Fatalf("completed token cipher = %#v, err=%v", cipher, err)
	}

	if _, err := database.PruneExpiredLoginLinks(ctx, now.Add(6*time.Minute), 1_000); err != nil {
		t.Fatalf("PruneExpiredLoginLinks() error = %v", err)
	}
	var links int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_link_tokens`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("remaining links = %d, err=%v", links, err)
	}
}

func TestMailLoginSettingsRequireSMTPAndRevocationErasesPendingSecrets(t *testing.T) {
	database, user, now := newAuthTestStore(t)
	ctx := t.Context()
	initial, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mailEnabled := siteSettingsInput(initial)
	mailEnabled.MailLoginEnabled = true
	if _, err := database.UpdateSiteSettings(ctx, user.ID, initial.Revision, mailEnabled, now); !errors.Is(err, ErrMailLoginNeedsMail) {
		t.Fatalf("mail login without SMTP error=%v, want ErrMailLoginNeedsMail", err)
	}
	ticketSettings, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticketSettings, err = database.UpdateTicketSettings(ctx, user.ID, ticketSettings.Revision, SaveTicketSettingsInput{
		AppName: ticketSettings.AppName, AppURL: ticketSettings.AppURL, SMTPEnabled: true,
		SMTPHost: "mailpit", SMTPPort: 1025, SMTPEncryption: "none", SMTPFromAddress: "support@example.test",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := database.UpdateSiteSettings(ctx, user.ID, ticketSettings.Revision, mailEnabled, now)
	if err != nil || !enabled.MailLoginEnabled {
		t.Fatalf("enable mail login settings=%#v err=%v", enabled, err)
	}
	if _, err := database.UpdateTicketSettings(ctx, user.ID, enabled.Revision, SaveTicketSettingsInput{
		AppName: ticketSettings.AppName, AppURL: ticketSettings.AppURL, SMTPEnabled: false,
		SMTPPort: 25, SMTPEncryption: "none",
	}, now.Add(time.Second)); !errors.Is(err, ErrMailLoginNeedsMail) {
		t.Fatalf("disable required SMTP error=%v, want ErrMailLoginNeedsMail", err)
	}
	input := MailLoginLinkRequestInput{
		Email: user.Email, ExpectedUserID: user.ID, EmailDigest: testLoginLinkDigest(40), TokenDigest: testLoginLinkDigest(41),
		TokenCipher: []byte("authenticated-login-link-ciphertext-0005"), Redirect: "dashboard", LinkBaseURL: "https://panel.example.test",
	}
	if queued, err := database.RequestMailLoginLink(ctx, input, now); err != nil || !queued {
		t.Fatalf("RequestMailLoginLink() queued=%v err=%v", queued, err)
	}
	disabledInput := siteSettingsInput(enabled)
	disabledInput.MailLoginEnabled = false
	disabled, err := database.UpdateSiteSettings(ctx, user.ID, enabled.Revision, disabledInput, now.Add(2*time.Second))
	if err != nil || disabled.MailLoginEnabled {
		t.Fatalf("disable mail login settings=%#v err=%v", disabled, err)
	}
	if _, claimed, err := database.ClaimLoginLinkMail(ctx, "after-disable", now.Add(3*time.Second), time.Minute); err != nil || claimed {
		t.Fatalf("disabled mail remained claimable: claimed=%v err=%v", claimed, err)
	}
	if _, err := database.ExchangeLoginLink(ctx, LoginLinkExchangeInput{
		TokenDigest: input.TokenDigest, SessionTokenHash: "disabled-session", CSRFHash: "disabled-csrf",
		SessionExpiresAt: now.Add(time.Hour),
	}, now.Add(3*time.Second)); !errors.Is(err, ErrLoginLinkInvalid) {
		t.Fatalf("disabled email login link exchange error=%v, want ErrLoginLinkInvalid", err)
	}
	if required, err := database.LoginLinkProtectionRequired(ctx); err != nil || required {
		t.Fatalf("disabled mail protection required=%v err=%v", required, err)
	}
}

func siteSettingsInput(settings SiteSettings) SaveSiteSettingsInput {
	return SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL, TOSURL: settings.TOSURL,
		Logo: settings.Logo, StopRegister: settings.StopRegister, EmailVerificationEnabled: settings.EmailVerificationEnabled,
		EmailWhitelistEnabled: settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled: settings.GmailAliasLimitEnabled, RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount: settings.RegistrationIPLimitCount, RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		InvitationForceEnabled: settings.InvitationForceEnabled, InvitationCodeLimit: settings.InvitationCodeLimit,
		InvitationNeverExpire: settings.InvitationNeverExpire, MailLoginEnabled: settings.MailLoginEnabled,
	}
}

func testLoginLinkDigest(marker byte) []byte {
	value := make([]byte, 32)
	for index := range value {
		value[index] = marker
	}
	return value
}
