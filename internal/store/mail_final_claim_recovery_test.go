package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

const recoveryTestLease = 30 * time.Second

func TestMailFinalClaimRecoveryCommitsWithoutAnotherCandidate(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		t.Run(kind, func(t *testing.T) {
			f := newMailRecoveryFixture(t, kind)
			id, claimedAt, token := f.abandonFinalClaim(t)
			before := f.state(t, id)
			if _, claimed, err := f.claim(t.Context(), f.database, "before-expiry", claimedAt.Add(recoveryTestLease-time.Second)); err != nil || claimed {
				t.Fatalf("active final claim = (%v, %v), want unchanged lease", claimed, err)
			}
			if got := f.state(t, id); got != before {
				t.Fatalf("unexpired claim changed: before=%#v after=%#v", before, got)
			}
			recoveredAt := claimedAt.Add(recoveryTestLease)
			if _, claimed, err := f.claim(t.Context(), f.database, "recover-expired-final", recoveredAt); err != nil || claimed {
				t.Fatalf("final claim recovery = (%v, %v), want no fourth attempt", claimed, err)
			}
			f.assertRecovered(t, id, recoveredAt)
			stats, err := f.database.GetSystemQueueStats(t.Context())
			if err != nil || stats.Pending != 0 || stats.Claimed != 0 || stats.Failed != 1 {
				t.Fatalf("recovered queue stats = %#v, %v", stats, err)
			}
			failures, err := f.database.ListTicketMailFailures(t.Context(), 1, 20)
			if err != nil || failures.Total != 1 || len(failures.Items) != 1 ||
				failures.Items[0].AttemptCount != 3 || failures.Items[0].LastError != "mail delivery result unknown after worker restart" {
				t.Fatalf("failed mail listing = %#v, %v", failures, err)
			}
			wantKind := "ticket"
			if kind == "reminder" {
				wantKind = "subscription_reminder_expire"
			}
			if failures.Items[0].Kind != wantKind {
				t.Fatalf("failure kind = %q, want %q", failures.Items[0].Kind, wantKind)
			}
			terminal := f.state(t, id)
			if err := f.complete(t.Context(), id, token, recoveredAt.Add(time.Second)); !errors.Is(err, ErrConflict) {
				t.Fatalf("late completion = %v, want ErrConflict", err)
			}
			if err := f.fail(t.Context(), id, token, recoveredAt.Add(time.Second)); !errors.Is(err, ErrConflict) {
				t.Fatalf("late failure = %v, want ErrConflict", err)
			}
			for index := range 2 {
				if _, claimed, err := f.claim(t.Context(), f.database, fmt.Sprintf("repeat-recovery-%d", index), recoveredAt.Add(time.Hour)); err != nil || claimed {
					t.Fatalf("repeat recovery = (%v, %v)", claimed, err)
				}
			}
			if got := f.state(t, id); got != terminal {
				t.Fatalf("terminal state changed: before=%#v after=%#v", terminal, got)
			}
		})
	}
}

func TestMailFinalClaimRecoveryPreservesEarlierLeaseRetries(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		t.Run(kind, func(t *testing.T) {
			f := newMailRecoveryFixture(t, kind)
			for attempt := 1; attempt <= 3; attempt++ {
				job, claimed, err := f.claim(t.Context(), f.database, fmt.Sprintf("lease-retry-%d", attempt), f.now.Add(time.Duration(attempt-1)*recoveryTestLease))
				if err != nil || !claimed || job.attempt != attempt {
					t.Fatalf("attempt %d = (%#v, %v, %v)", attempt, job, claimed, err)
				}
				if state := f.state(t, job.id); state.failed.Valid {
					t.Fatalf("retryable attempt became terminal: %#v", state)
				}
			}
		})
	}
}

func TestMailFinalClaimRecoveryPreservesTerminalRows(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		for _, terminal := range []string{"sent_at", "failed_at", "cancelled_at"} {
			if kind == "ticket" && terminal == "cancelled_at" {
				continue // Ticket mail represents cancellation with failed_at.
			}
			t.Run(kind+"/"+terminal, func(t *testing.T) {
				f := newMailRecoveryFixture(t, kind)
				id, at, _ := f.abandonFinalClaim(t)
				if _, err := f.database.db.ExecContext(t.Context(), `UPDATE `+f.table+` SET `+terminal+`=? WHERE id=?`, at.Unix(), id); err != nil {
					t.Fatal(err)
				}
				before := f.state(t, id)
				if _, claimed, err := f.claim(t.Context(), f.database, "terminal-check", at.Add(time.Hour)); err != nil || claimed {
					t.Fatalf("terminal row claim = (%v, %v)", claimed, err)
				}
				if after := f.state(t, id); after != before {
					t.Fatalf("terminal row changed: before=%#v after=%#v", before, after)
				}
			})
		}
	}
}

func TestMailFinalClaimRecoverySurvivesReopen(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		t.Run(kind, func(t *testing.T) {
			f := newMailRecoveryFixture(t, kind)
			id, at, _ := f.abandonFinalClaim(t)
			if err := f.database.Close(); err != nil {
				t.Fatal(err)
			}
			database, err := OpenSQLite(f.dsn)
			if err != nil {
				t.Fatal(err)
			}
			f.database = database
			t.Cleanup(func() { _ = database.Close() })
			at = at.Add(recoveryTestLease)
			if _, claimed, err := f.claim(t.Context(), database, "restarted-worker", at); err != nil || claimed {
				t.Fatalf("restart recovery = (%v, %v)", claimed, err)
			}
			f.assertRecovered(t, id, at)
		})
	}
}

func TestMailFinalClaimRecoveryIsAtomicAcrossWorkers(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		t.Run(kind, func(t *testing.T) {
			f := newMailRecoveryFixture(t, kind)
			id, at, _ := f.abandonFinalClaim(t)
			f.addMail(t)
			other, err := OpenSQLite(f.dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			at = at.Add(recoveryTestLease)
			start := make(chan struct{})
			results := make(chan mailRecoveryClaimResult, 2)
			var workers sync.WaitGroup
			for index, database := range []*Store{f.database, other} {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					job, claimed, err := f.claim(t.Context(), database, fmt.Sprintf("concurrent-worker-%d", index), at)
					results <- mailRecoveryClaimResult{job: job, claimed: claimed, err: err}
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			claimedCount := 0
			for result := range results {
				if result.err != nil {
					t.Fatal(result.err)
				}
				if result.claimed {
					claimedCount++
					if result.job.id == id || result.job.attempt != 1 {
						t.Fatalf("wrong mail claimed: %#v", result.job)
					}
				}
			}
			if claimedCount != 1 {
				t.Fatalf("ordinary mail claimed %d times, want one", claimedCount)
			}
			f.assertRecovered(t, id, at)
			stats, err := f.database.GetSystemQueueStats(t.Context())
			if err != nil || stats.Pending != 1 || stats.Claimed != 1 || stats.Failed != 1 {
				t.Fatalf("concurrent stats = %#v, %v", stats, err)
			}
		})
	}
}

func TestMailFinalClaimRecoveryRollsBackOnDatabaseFailure(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		for _, stage := range []string{"recovery", "following_claim"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				f := newMailRecoveryFixture(t, kind)
				id, at, _ := f.abandonFinalClaim(t)
				condition := `NEW.failed_at IS NOT NULL`
				if stage == "following_claim" {
					f.addMail(t)
					condition = `NEW.attempt_count > OLD.attempt_count`
				}
				before := f.state(t, id)
				if _, err := f.database.db.ExecContext(t.Context(), `CREATE TRIGGER reject_mail_recovery BEFORE UPDATE ON `+f.table+`
					WHEN `+condition+` BEGIN SELECT RAISE(ABORT, 'synthetic mail recovery failure'); END`); err != nil {
					t.Fatal(err)
				}
				at = at.Add(recoveryTestLease)
				if _, _, err := f.claim(t.Context(), f.database, "worker-with-db-failure", at); err == nil || !strings.Contains(err.Error(), "synthetic mail recovery failure") {
					t.Fatalf("recovery error = %v, want injected database failure", err)
				}
				if after := f.state(t, id); before != after {
					t.Fatalf("failed transaction changed final claim: before=%#v after=%#v", before, after)
				}
				if _, err := f.database.db.ExecContext(t.Context(), `DROP TRIGGER reject_mail_recovery`); err != nil {
					t.Fatal(err)
				}
				if _, claimed, err := f.claim(t.Context(), f.database, "recovery-after-db-failure", at); err != nil || claimed != (stage == "following_claim") {
					t.Fatalf("retry after DB recovery = (%v, %v)", claimed, err)
				}
				f.assertRecovered(t, id, at)
			})
		}
	}
}

func TestMailFinalClaimRecoveryIsBounded(t *testing.T) {
	for _, kind := range []string{"ticket", "reminder"} {
		t.Run(kind, func(t *testing.T) {
			f := newMailRecoveryFixture(t, kind)
			for range 100 {
				f.addMail(t)
			}
			if _, err := f.database.db.ExecContext(t.Context(), `UPDATE `+f.table+`
				SET attempt_count=3, claim_token='abandoned-fixture-claim', claimed_at=?`, f.now.Unix()); err != nil {
				t.Fatal(err)
			}
			at := f.now.Add(recoveryTestLease)
			for index, expected := range []int64{100, 101} {
				if _, claimed, err := f.claim(t.Context(), f.database, fmt.Sprintf("bounded-recovery-%d", index), at); err != nil || claimed {
					t.Fatalf("bounded recovery = (%v, %v)", claimed, err)
				}
				stats, err := f.database.GetSystemQueueStats(t.Context())
				if err != nil || stats.Failed != expected || stats.Pending != 101-expected || stats.Claimed != 101-expected {
					t.Fatalf("bounded batch %d stats = %#v, %v", index, stats, err)
				}
			}
		})
	}
}

type mailRecoveryFixture struct {
	database      *Store
	dsn           string
	kind          string
	table         string
	administrator int64
	users         int
	now           time.Time
}

func newMailRecoveryFixture(t *testing.T, kind string) *mailRecoveryFixture {
	t.Helper()
	dsn := cloneMigratedTestDatabase(t)
	database, err := OpenSQLite(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	f := &mailRecoveryFixture{database: database, dsn: dsn, kind: kind, table: "ticket_mail_outbox", now: time.Date(2026, 10, 10, 3, 30, 0, 0, time.UTC)}
	if kind == "reminder" {
		f.table = "subscription_reminder_outbox"
	}
	admin, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "qa-mail-recovery-admin@example.test", PasswordHash: "synthetic", IsAdmin: true}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	f.administrator = admin.ID
	settings, err := database.GetMailSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateMailSettings(t.Context(), admin.ID, settings.Revision, SaveMailSettingsInput{
		SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587, SMTPEncryption: "starttls",
		SMTPFromAddress: "qa-support@example.test", RemindMailEnabled: true,
	}, f.now); err != nil {
		t.Fatal(err)
	}
	f.addMail(t)
	return f
}

func (f *mailRecoveryFixture) addMail(t *testing.T) {
	t.Helper()
	f.users++
	expires := f.now.Add(time.Hour)
	user, err := f.database.CreateAdminUser(t.Context(), CreateAdminUserInput{
		Email: fmt.Sprintf("qa-mail-recovery-%d@example.test", f.users), PasswordHash: "synthetic", ExpiredAt: &expires,
	}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if f.kind == "reminder" {
		result, err := f.database.ScheduleSubscriptionReminders(t.Context(), f.now, "2026-10-10", 500)
		if err != nil || result.ExpireQueued != 1 || result.TrafficQueued != 0 {
			t.Fatalf("seed reminder = %#v, %v", result, err)
		}
		return
	}
	ticket, err := f.database.CreateTicket(t.Context(), user.ID, SaveTicketInput{Subject: "QA worker recovery", Level: TicketLevelLow, Message: "synthetic question"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ReplyTicketAsAdmin(t.Context(), f.administrator, ticket.ID, "synthetic reply", f.now); err != nil {
		t.Fatal(err)
	}
}

type mailRecoveryJob struct {
	id      int64
	attempt int
}

type mailRecoveryClaimResult struct {
	job     mailRecoveryJob
	claimed bool
	err     error
}

func (f *mailRecoveryFixture) claim(ctx context.Context, database *Store, token string, at time.Time) (mailRecoveryJob, bool, error) {
	if f.kind == "ticket" {
		job, claimed, err := database.ClaimTicketMail(ctx, token, at, recoveryTestLease)
		return mailRecoveryJob{id: job.ID, attempt: job.Attempt}, claimed, err
	}
	job, claimed, err := database.ClaimSubscriptionReminder(ctx, token, at, recoveryTestLease)
	return mailRecoveryJob{id: job.ID, attempt: job.Attempt}, claimed, err
}

func (f *mailRecoveryFixture) complete(ctx context.Context, id int64, token string, at time.Time) error {
	if f.kind == "ticket" {
		return f.database.CompleteTicketMail(ctx, id, token, at)
	}
	return f.database.CompleteSubscriptionReminder(ctx, id, token, at)
}

func (f *mailRecoveryFixture) fail(ctx context.Context, id int64, token string, at time.Time) error {
	if f.kind == "ticket" {
		return f.database.FailTicketMail(ctx, id, token, "synthetic delivery failure", at.Add(time.Minute), at)
	}
	return f.database.FailSubscriptionReminder(ctx, id, token, "synthetic delivery failure", at.Add(time.Minute), at)
}

func (f *mailRecoveryFixture) abandonFinalClaim(t *testing.T) (int64, time.Time, string) {
	t.Helper()
	var job mailRecoveryJob
	var at time.Time
	var token string
	for attempt := 1; attempt <= 3; attempt++ {
		at = f.now.Add(time.Duration(attempt-1) * time.Minute)
		token = fmt.Sprintf("abandoned-attempt-%d", attempt)
		var claimed bool
		var err error
		job, claimed, err = f.claim(t.Context(), f.database, token, at)
		if err != nil || !claimed || job.attempt != attempt {
			t.Fatalf("claim attempt %d = (%#v, %v, %v)", attempt, job, claimed, err)
		}
		if attempt < 3 {
			if err := f.fail(t.Context(), job.id, token, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	return job.id, at, token
}

type mailRecoveryState struct {
	attempt                          int
	claim, lastError                 sql.NullString
	claimed, sent, failed, cancelled sql.NullInt64
	updated                          int64
}

func (f *mailRecoveryFixture) state(t *testing.T, id int64) mailRecoveryState {
	t.Helper()
	cancelled := "NULL"
	if f.kind == "reminder" {
		cancelled = "cancelled_at"
	}
	var state mailRecoveryState
	if err := f.database.db.QueryRowContext(t.Context(), `SELECT attempt_count, claim_token, last_error, claimed_at,
		sent_at, failed_at, `+cancelled+`, updated_at FROM `+f.table+` WHERE id=?`, id).
		Scan(&state.attempt, &state.claim, &state.lastError, &state.claimed, &state.sent, &state.failed, &state.cancelled, &state.updated); err != nil {
		t.Fatal(err)
	}
	return state
}

func (f *mailRecoveryFixture) assertRecovered(t *testing.T, id int64, at time.Time) {
	t.Helper()
	got := f.state(t, id)
	if got.attempt != 3 || got.claim.Valid || got.claimed.Valid || got.sent.Valid || !got.failed.Valid ||
		got.failed.Int64 != at.Unix() || got.updated != at.Unix() || got.lastError.String != "mail delivery result unknown after worker restart" {
		t.Fatalf("abandoned final mail did not reach its terminal state: %#v", got)
	}
}
