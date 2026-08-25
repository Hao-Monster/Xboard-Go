package maintenance

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestCleanupExpiredWithSQLitePreservesLiveState(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 16, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "cleanup.db")
	database, err := store.OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	firstUser, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "cleanup-one@example.com", PasswordHash: "test-password-hash", TransferEnable: 1,
	}, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	secondUser, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{
		Email: "cleanup-two@example.com", PasswordHash: "test-password-hash", TransferEnable: 1,
	}, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	cutoff := now.Add(-24 * time.Hour)
	staleTicketID := insertCleanupFixture(t, raw, `
		INSERT INTO tickets (user_id, subject, level, status, reply_status, last_reply_user_id, created_at, updated_at)
		VALUES (?, 'stale answered', 1, 0, 1, ?, ?, ?)
	`, firstUser.ID, secondUser.ID, cutoff.Add(-time.Hour).Unix(), cutoff.Unix())
	liveTicketID := insertCleanupFixture(t, raw, `
		INSERT INTO tickets (user_id, subject, level, status, reply_status, last_reply_user_id, created_at, updated_at)
		VALUES (?, 'live answered', 1, 0, 1, ?, ?, ?)
	`, secondUser.ID, firstUser.ID, cutoff.Add(-time.Hour).Unix(), cutoff.Add(time.Second).Unix())

	insertCleanupFixture(t, raw, `INSERT INTO registration_ip_limits VALUES ('192.0.2.1', 1, ?, ?)`, now.Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO registration_ip_limits VALUES ('192.0.2.2', 1, ?, ?)`, now.Add(time.Second).Unix(), now.Unix())
	insertCleanupFixture(t, raw, `INSERT INTO password_reset_challenges VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)`, digest(1), firstUser.ID, digest(2), now.Unix(), now.Unix(), now.Add(-time.Hour).Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO password_reset_challenges VALUES (?, ?, ?, ?, ?, 0, NULL, ?, ?)`, digest(3), secondUser.ID, digest(4), now.Add(time.Second).Unix(), now.Unix(), now.Add(-time.Hour).Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO registration_email_challenges VALUES (?, ?, ?, ?, 0, NULL, ?, ?)`, digest(5), digest(6), now.Unix(), now.Unix(), now.Add(-time.Hour).Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO registration_email_challenges VALUES (?, ?, ?, ?, 0, NULL, ?, ?)`, digest(7), digest(8), now.Add(time.Second).Unix(), now.Unix(), now.Add(-time.Hour).Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO login_link_tokens VALUES (?, ?, 'quick', 'dashboard', ?, ?)`, digest(9), firstUser.ID, now.Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO login_link_tokens VALUES (?, ?, 'quick', 'dashboard', ?, ?)`, digest(10), secondUser.ID, now.Add(time.Second).Unix(), now.Unix())
	insertCleanupFixture(t, raw, `INSERT INTO login_failure_limits VALUES (?, 1, ?, ?)`, digest(11), now.Unix(), now.Add(-time.Hour).Unix())
	insertCleanupFixture(t, raw, `INSERT INTO login_failure_limits VALUES (?, 1, ?, ?)`, digest(12), now.Add(time.Second).Unix(), now.Unix())

	result, err := CleanupExpired(ctx, database, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := CleanupResult{
		StaleTicketsClosed:                   1,
		RegistrationIPLimitsPruned:           1,
		PasswordResetsPruned:                 1,
		RegistrationEmailVerificationsPruned: 1,
		LoginLinksPruned:                     1,
		LoginFailureLimitsPruned:             1,
	}
	if result != want {
		t.Fatalf("CleanupExpired() = %#v, want %#v", result, want)
	}
	secondResult, err := CleanupExpired(ctx, database, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if secondResult != (CleanupResult{}) {
		t.Fatalf("second CleanupExpired() = %#v, want empty result", secondResult)
	}

	assertCleanupTicket(t, raw, staleTicketID, 1, now.Unix())
	assertCleanupTicket(t, raw, liveTicketID, 0, cutoff.Add(time.Second).Unix())
	for _, table := range []string{
		"registration_ip_limits", "password_reset_challenges", "registration_email_challenges",
		"login_link_tokens", "login_failure_limits",
	} {
		var count int
		if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1 live row", table, count)
		}
	}
	var integrity string
	if err := raw.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
	var foreignKeyViolations int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&foreignKeyViolations); err != nil || foreignKeyViolations != 0 {
		t.Fatalf("foreign_key_check = %d, %v", foreignKeyViolations, err)
	}
}

func TestCleanupExpiredSharesWorkSafelyWithSchedulerPrune(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 25, 16, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "concurrent-cleanup.db")
	database, err := store.OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `
		WITH RECURSIVE entries(value) AS (
			VALUES(1) UNION ALL SELECT value + 1 FROM entries WHERE value < 100
		)
		INSERT INTO login_failure_limits (credential_digest, failure_count, expires_at, updated_at)
		SELECT randomblob(32), 1, ?, ? FROM entries
	`, now.Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var commandResult CleanupResult
	var commandErr error
	var schedulerRemoved int64
	var schedulerErr error
	go func() {
		defer wait.Done()
		<-start
		commandResult, commandErr = CleanupExpired(ctx, database, now, 100)
	}()
	go func() {
		defer wait.Done()
		<-start
		schedulerRemoved, schedulerErr = database.PruneExpiredLoginFailureLimits(ctx, now, 100)
	}()
	close(start)
	wait.Wait()
	if commandErr != nil || schedulerErr != nil {
		t.Fatalf("concurrent cleanup errors = command %v, scheduler %v", commandErr, schedulerErr)
	}
	if commandResult.LoginFailureLimitsPruned+schedulerRemoved != 100 {
		t.Fatalf("concurrent cleanup total = %d + %d, want 100", commandResult.LoginFailureLimitsPruned, schedulerRemoved)
	}
	var remaining int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_failure_limits`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("concurrent cleanup left %d rows", remaining)
	}
}

func insertCleanupFixture(t *testing.T, database *sql.DB, query string, arguments ...any) int64 {
	t.Helper()
	result, err := database.ExecContext(context.Background(), query, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertCleanupTicket(t *testing.T, database *sql.DB, ticketID int64, wantStatus int, wantUpdatedAt int64) {
	t.Helper()
	var status int
	var updatedAt int64
	if err := database.QueryRowContext(context.Background(), `SELECT status, updated_at FROM tickets WHERE id = ?`, ticketID).Scan(&status, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || updatedAt != wantUpdatedAt {
		t.Fatalf("ticket %d = status %d updated_at %d, want %d/%d", ticketID, status, updatedAt, wantStatus, wantUpdatedAt)
	}
}

func digest(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}
