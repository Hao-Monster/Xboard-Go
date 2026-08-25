package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRegisterUserNormalizesIdentityAndHonorsStopPolicy(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	user, err := database.RegisterUser(ctx, RegisterUserInput{
		Email: "  NEW-USER@EXAMPLE.TEST  ", PasswordHash: "argon2id-test-hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "new-user@example.test" || user.IsAdmin || user.Banned || user.AccountKind != AccountKindHuman {
		t.Fatalf("registered user = %#v", user)
	}
	var uuid, token string
	var groupID, expiredAt *int64
	var transfer, upload, download, speed, devices int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT uuid, subscription_token, group_id, expired_at, transfer_enable, traffic_u, traffic_d, speed_limit, device_limit
		FROM users WHERE id = ?
	`, user.ID).Scan(&uuid, &token, &groupID, &expiredAt, &transfer, &upload, &download, &speed, &devices); err != nil {
		t.Fatal(err)
	}
	if len(uuid) != 36 || len(token) != 32 || groupID != nil || expiredAt != nil || transfer != 0 || upload != 0 || download != 0 || speed != 0 || devices != 0 {
		t.Fatalf("registered defaults uuid=%q token_length=%d group=%v expiry=%v transfer=%d traffic=(%d,%d) speed=%d devices=%d", uuid, len(token), groupID, expiredAt, transfer, upload, download, speed, devices)
	}

	administrator := createTicketTestUser(t, database, "registration-admin@example.test", now)
	settings, err := database.GetSiteSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateSiteSettings(ctx, administrator.ID, settings.Revision, SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: true,
		EmailWhitelistSuffixes:     settings.EmailWhitelistSuffixes,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       settings.PasswordLimitEnabled,
		PasswordLimitCount:         settings.PasswordLimitCount,
		PasswordLimitMinutes:       settings.PasswordLimitMinutes,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "blocked@example.test", PasswordHash: "hash"}, now); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("RegisterUser(closed) error = %v, want ErrRegistrationClosed", err)
	}
	if _, err := database.FindUserByEmail(ctx, "blocked@example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closed registration persisted user: %v", err)
	}
}

func TestRegisterUserSerializesConcurrentDuplicateEmail(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	const workers = 8
	var successes, duplicates int
	var mutex sync.Mutex
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := database.RegisterUser(ctx, RegisterUserInput{Email: "Concurrent@Example.Test", PasswordHash: "hash"}, now)
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrEmailInUse):
				duplicates++
			default:
				t.Errorf("worker %d error = %v", index, err)
			}
		}(index)
	}
	group.Wait()
	if successes != 1 || duplicates != workers-1 {
		t.Fatalf("concurrent results successes=%d duplicates=%d", successes, duplicates)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = 'concurrent@example.test' COLLATE NOCASE`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("stored duplicate count=%d err=%v", count, err)
	}
}

func TestRegisterUserWithSessionRollsBackAccountWhenSessionInsertFails(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "registration-rollback-admin@example.test", now)
	updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.RegistrationIPLimitEnabled = true
		input.RegistrationIPLimitCount = 2
		input.RegistrationIPLimitMinutes = 60
	})
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER reject_test_registration_session
		BEFORE INSERT ON admin_sessions WHEN NEW.token_hash = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
		BEGIN SELECT RAISE(ABORT, 'reject test registration session'); END;
	`); err != nil {
		t.Fatal(err)
	}
	_, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
		Email: "atomic-registration@example.test", PasswordHash: "hash", SourceIP: "192.0.2.30",
	}, RegistrationSessionInput{
		TokenHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CSRFHash:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExpiresAt: now.Add(time.Hour),
	}, now)
	if err == nil {
		t.Fatal("RegisterUserWithSession() unexpectedly succeeded")
	}
	if _, err := database.FindUserByEmail(ctx, "atomic-registration@example.test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed session left a registered account: %v", err)
	}
	var counters int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_ip_limits WHERE source_ip = '192.0.2.30'`).Scan(&counters); err != nil || counters != 0 {
		t.Fatalf("failed session left an IP counter: count=%d err=%v", counters, err)
	}
}

func TestRegistrationEmailPoliciesNormalizeDomainsAndScopeGmailAliases(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "registration-policy-admin@example.test", now)
	settings := updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.EmailWhitelistEnabled = true
		input.EmailWhitelistSuffixes = []string{" Allowed.Test ", "allowed.test"}
	})
	if !settings.EmailWhitelistEnabled || len(settings.EmailWhitelistSuffixes) != 1 || settings.EmailWhitelistSuffixes[0] != "allowed.test" {
		t.Fatalf("normalized whitelist settings = %#v", settings)
	}
	if err := CheckRegistrationEmailPolicy(settings, "UPPER@ALLOWED.TEST"); err != nil {
		t.Fatalf("allowlisted fast email policy error = %v", err)
	}
	if err := CheckRegistrationEmailPolicy(settings, "blocked@example.test"); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Fatalf("blocked fast email policy error = %v", err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "UPPER@ALLOWED.TEST", PasswordHash: "hash"}, now); err != nil {
		t.Fatalf("allowlisted normalized registration error = %v", err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "blocked@example.test", PasswordHash: "hash"}, now); !errors.Is(err, ErrEmailDomainNotAllowed) {
		t.Fatalf("blocked domain error = %v, want ErrEmailDomainNotAllowed", err)
	}

	updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.EmailWhitelistEnabled = false
		input.GmailAliasLimitEnabled = true
	})
	for _, email := range []string{"first.last@gmail.com", "first+tag@googlemail.com"} {
		if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: email, PasswordHash: "hash"}, now); !errors.Is(err, ErrGmailAliasNotAllowed) {
			t.Fatalf("Gmail alias %q error = %v, want ErrGmailAliasNotAllowed", email, err)
		}
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "first.last+tag@example.test", PasswordHash: "hash"}, now); err != nil {
		t.Fatalf("non-Gmail dot/plus registration error = %v", err)
	}
}

func TestRegistrationSuccessfulIPLimitIsAtomicAndExpires(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "registration-ip-admin@example.test", now)
	policySettings := updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.RegistrationIPLimitEnabled = true
		input.RegistrationIPLimitCount = 2
		input.RegistrationIPLimitMinutes = 1
	})
	const sourceIP = "192.0.2.10"
	if err := database.CheckRegistrationIPLimit(ctx, policySettings, "not-an-ip", now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid fast IP policy error = %v", err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "ip-first@example.test", PasswordHash: "hash", SourceIP: sourceIP}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "ip-first@example.test", PasswordHash: "hash", SourceIP: sourceIP}, now); !errors.Is(err, ErrEmailInUse) {
		t.Fatalf("duplicate registration error = %v, want ErrEmailInUse", err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "ip-second@example.test", PasswordHash: "hash", SourceIP: sourceIP}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	precheckErr := database.CheckRegistrationIPLimit(ctx, policySettings, sourceIP, now.Add(2*time.Second))
	var limited *RegistrationIPLimitError
	if !errors.As(precheckErr, &limited) || limited.RetryAfterSeconds != 59 || limited.WindowMinutes != 1 || limited.Error() != ErrRegistrationIPLimited.Error() {
		t.Fatalf("fast IP policy error = %#v (%v)", limited, precheckErr)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "ip-third@example.test", PasswordHash: "hash", SourceIP: sourceIP}, now.Add(2*time.Second)); !errors.Is(err, ErrRegistrationIPLimited) {
		t.Fatalf("third registration error = %v, want ErrRegistrationIPLimited", err)
	}
	var count int
	var resetAt int64
	if err := database.db.QueryRowContext(ctx, `SELECT successful_count, reset_at FROM registration_ip_limits WHERE source_ip = ?`, sourceIP).Scan(&count, &resetAt); err != nil {
		t.Fatal(err)
	}
	if count != 2 || resetAt != now.Add(time.Second+time.Minute).Unix() {
		t.Fatalf("IP counter count=%d reset_at=%d", count, resetAt)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{Email: "ip-after-expiry@example.test", PasswordHash: "hash", SourceIP: sourceIP}, now.Add(62*time.Second)); err != nil {
		t.Fatalf("registration after IP window expiry error = %v", err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT successful_count FROM registration_ip_limits WHERE source_ip = ?`, sourceIP).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reset IP counter count=%d err=%v", count, err)
	}
}

func TestConcurrentSuccessfulIPLimitDoesNotOversubscribe(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "registration-ip-race-admin@example.test", now)
	updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.RegistrationIPLimitEnabled = true
		input.RegistrationIPLimitCount = 2
		input.RegistrationIPLimitMinutes = 60
	})
	const workers = 8
	var successes, limited int
	var mutex sync.Mutex
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, err := database.RegisterUser(ctx, RegisterUserInput{
				Email: fmt.Sprintf("ip-race-%d@example.test", index), PasswordHash: "hash", SourceIP: "192.0.2.20",
			}, now)
			mutex.Lock()
			defer mutex.Unlock()
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ErrRegistrationIPLimited):
				limited++
			default:
				t.Errorf("worker %d error = %v", index, err)
			}
		}(index)
	}
	group.Wait()
	if successes != 2 || limited != workers-2 {
		t.Fatalf("concurrent IP results successes=%d limited=%d", successes, limited)
	}
}

func TestExpiredRegistrationIPCountersArePrunedAndDisabledPolicyClearsState(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	administrator := createTicketTestUser(t, database, "registration-prune-admin@example.test", now)
	updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.RegistrationIPLimitEnabled = true
		input.RegistrationIPLimitCount = 3
		input.RegistrationIPLimitMinutes = 1
	})
	if _, err := database.RegisterUser(ctx, RegisterUserInput{
		Email: "registration-prune@example.test", PasswordHash: "hash", SourceIP: "192.0.2.40",
	}, now); err != nil {
		t.Fatal(err)
	}
	if removed, err := database.PruneExpiredRegistrationIPLimits(ctx, now.Add(59*time.Second), 100); err != nil || removed != 0 {
		t.Fatalf("early prune removed=%d err=%v", removed, err)
	}
	if removed, err := database.PruneExpiredRegistrationIPLimits(ctx, now.Add(time.Minute), 100); err != nil || removed != 1 {
		t.Fatalf("expired prune removed=%d err=%v", removed, err)
	}
	if _, err := database.RegisterUser(ctx, RegisterUserInput{
		Email: "registration-clear@example.test", PasswordHash: "hash", SourceIP: "192.0.2.41",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	updateRegistrationPolicy(t, database, administrator.ID, func(input *SaveSiteSettingsInput) {
		input.RegistrationIPLimitEnabled = false
	})
	var counters int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM registration_ip_limits`).Scan(&counters); err != nil || counters != 0 {
		t.Fatalf("disabled IP policy retained counters: count=%d err=%v", counters, err)
	}
	for _, limit := range []int{0, 1_001} {
		if _, err := database.PruneExpiredRegistrationIPLimits(ctx, now, limit); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("prune limit %d error=%v, want ErrInvalidInput", limit, err)
		}
	}
}

func updateRegistrationPolicy(t *testing.T, database *Store, administratorID int64, mutate func(*SaveSiteSettingsInput)) SiteSettings {
	t.Helper()
	settings, err := database.GetSiteSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := SaveSiteSettingsInput{
		AppName: settings.AppName, AppDescription: settings.AppDescription, AppURL: settings.AppURL,
		TOSURL: settings.TOSURL, Logo: settings.Logo, StopRegister: settings.StopRegister,
		EmailWhitelistEnabled: settings.EmailWhitelistEnabled, EmailWhitelistSuffixes: settings.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled:     settings.GmailAliasLimitEnabled,
		RegistrationIPLimitEnabled: settings.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount:   settings.RegistrationIPLimitCount,
		RegistrationIPLimitMinutes: settings.RegistrationIPLimitMinutes,
		PasswordLimitEnabled:       settings.PasswordLimitEnabled,
		PasswordLimitCount:         settings.PasswordLimitCount,
		PasswordLimitMinutes:       settings.PasswordLimitMinutes,
	}
	mutate(&input)
	updated, err := database.UpdateSiteSettings(t.Context(), administratorID, settings.Revision, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
