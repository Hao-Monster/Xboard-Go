package store

import (
	"context"
	"errors"
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
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER reject_test_registration_session
		BEFORE INSERT ON admin_sessions WHEN NEW.token_hash = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
		BEGIN SELECT RAISE(ABORT, 'reject test registration session'); END;
	`); err != nil {
		t.Fatal(err)
	}
	_, err := database.RegisterUserWithSession(ctx, RegisterUserInput{
		Email: "atomic-registration@example.test", PasswordHash: "hash",
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
}
