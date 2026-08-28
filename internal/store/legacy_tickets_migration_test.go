package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestImportLegacyTicketsStreamsAtomicallyAndDetectsTargetDrift(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "ticket-admin@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "ticket-owner@example.test", PasswordHash: "hash"}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("a", 64)
	insertHumanUsersLedger(t, database, sourceSHA)
	tickets := []LegacyTicket{
		{ID: 159, UserID: owner.ID, Subject: "Closed legacy ticket", Level: TicketLevelHigh, Status: TicketStatusClosed, ReplyStatus: TicketReplyAnswered, LastReplyUserID: admin.ID, CreatedAt: 100, UpdatedAt: 120},
		{ID: 160, UserID: owner.ID, Subject: "Open legacy ticket", Level: TicketLevelLow, Status: TicketStatusOpen, ReplyStatus: TicketReplyWaiting, LastReplyUserID: owner.ID, CreatedAt: 200, UpdatedAt: 200},
	}
	messages := []LegacyTicketMessage{
		{ID: 361, TicketID: 159, UserID: owner.ID, Message: "Owner question", CreatedAt: 100, UpdatedAt: 100},
		{ID: 362, TicketID: 159, UserID: admin.ID, Message: "Administrator answer", CreatedAt: 120, UpdatedAt: 120},
		{ID: 363, TicketID: 160, UserID: owner.ID, Message: "Waiting for support", CreatedAt: 200, UpdatedAt: 200},
	}
	verified := 0
	input := LegacyTicketsImport{
		Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 8192,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets),
		MessageRows: len(messages), MessageChecksum: LegacyTicketMessagesChecksum(messages),
		MessageStream: legacyMessageSliceStream(messages), VerifySource: func(context.Context) error { verified++; return nil },
		RollbackBackupPath: "/var/lib/xboard-backups/pre-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	report, err := database.ImportLegacyTickets(ctx, input, time.Unix(300, 0))
	if err != nil {
		t.Fatalf("ImportLegacyTickets() error = %v", err)
	}
	if report.AlreadyApplied || report.Tickets.SourceRows != 2 || report.Messages.SourceRows != 3 || verified != 1 ||
		report.Tickets.SourceChecksum != report.Tickets.TargetChecksum || report.Messages.SourceChecksum != report.Messages.TargetChecksum {
		t.Fatalf("report=%#v verified=%d", report, verified)
	}
	var outbox, throttle, ticketSequence, messageSequence int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_mail_outbox`).Scan(&outbox)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_mail_throttle`).Scan(&throttle)
	_ = database.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='tickets'`).Scan(&ticketSequence)
	_ = database.db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='ticket_messages'`).Scan(&messageSequence)
	if outbox != 0 || throttle != 0 || ticketSequence < 160 || messageSequence < 363 {
		t.Fatalf("side effects/sequence = outbox:%d throttle:%d tickets:%d messages:%d", outbox, throttle, ticketSequence, messageSequence)
	}
	repeated, err := database.ImportLegacyTickets(ctx, input, time.Unix(400, 0))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(time.Unix(300, 0).UTC()) || verified != 1 {
		t.Fatalf("repeated = (%#v, %v), verified=%d", repeated, err, verified)
	}
	if _, err := database.db.Exec(`UPDATE sqlite_sequence SET seq=999 WHERE name='tickets'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacyTicketsImport(ctx, sourceSHA); !errors.Is(err, ErrConflict) {
		t.Fatalf("LookupLegacyTicketsImport(sequence drift) error = %v", err)
	}
	if _, err := database.db.Exec(`UPDATE sqlite_sequence SET seq=? WHERE name='tickets'`, report.TicketSequence); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`UPDATE ticket_messages SET message='tampered' WHERE id=363`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacyTicketsImport(ctx, sourceSHA); !errors.Is(err, ErrConflict) {
		t.Fatalf("LookupLegacyTicketsImport(tampered) error = %v", err)
	}
}

func TestImportLegacyTicketsRejectsMissingHumanLedgerWithoutWrites(t *testing.T) {
	database := newTestStore(t)
	owner, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "missing-ledger@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	tickets := []LegacyTicket{{ID: 1, UserID: owner.ID, Subject: "No ledger", Level: TicketLevelLow, Status: TicketStatusClosed, ReplyStatus: TicketReplyWaiting, LastReplyUserID: owner.ID, CreatedAt: 1, UpdatedAt: 1}}
	messages := []LegacyTicketMessage{{ID: 1, TicketID: 1, UserID: owner.ID, Message: "body", CreatedAt: 1, UpdatedAt: 1}}
	input := LegacyTicketsImport{Slice: LegacyTicketsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets), MessageRows: 1, MessageChecksum: LegacyTicketMessagesChecksum(messages),
		MessageStream: legacyMessageSliceStream(messages), VerifySource: func(context.Context) error { return nil },
		RollbackBackupPath: "/tmp/pre-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64)}
	if _, err := database.ImportLegacyTickets(t.Context(), input, time.Unix(2, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyTickets(missing ledger) error = %v", err)
	}
	var ticketsCount, messagesCount, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&ticketsCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_messages`).Scan(&messagesCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTicketsSlice).Scan(&runs)
	if ticketsCount != 0 || messagesCount != 0 || runs != 0 {
		t.Fatalf("rejected import changed target: tickets=%d messages=%d runs=%d", ticketsCount, messagesCount, runs)
	}
}

func TestImportLegacyTicketsRejectsDirtyMailStateWithoutWrites(t *testing.T) {
	database := newTestStore(t)
	owner, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "dirty-mail@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("e", 64)
	insertHumanUsersLedger(t, database, sourceSHA)
	if _, err := database.db.Exec(`INSERT INTO ticket_mail_throttle (user_id,last_enqueued_at) VALUES (?,1)`, owner.ID); err != nil {
		t.Fatal(err)
	}
	tickets := []LegacyTicket{{ID: 1, UserID: owner.ID, Subject: "Dirty mail state", Level: TicketLevelLow, Status: TicketStatusClosed, ReplyStatus: TicketReplyWaiting, LastReplyUserID: owner.ID, CreatedAt: 1, UpdatedAt: 1}}
	messages := []LegacyTicketMessage{{ID: 1, TicketID: 1, UserID: owner.ID, Message: "body", CreatedAt: 1, UpdatedAt: 1}}
	input := LegacyTicketsImport{Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 1,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets), MessageRows: 1, MessageChecksum: LegacyTicketMessagesChecksum(messages),
		MessageStream: legacyMessageSliceStream(messages), VerifySource: func(context.Context) error { return nil },
		RollbackBackupPath: "/tmp/pre-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64)}
	if _, err := database.ImportLegacyTickets(t.Context(), input, time.Unix(2, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyTickets(dirty mail state) error = %v", err)
	}
	var ticketsCount, messagesCount, throttleCount int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&ticketsCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_messages`).Scan(&messagesCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_mail_throttle`).Scan(&throttleCount)
	if ticketsCount != 0 || messagesCount != 0 || throttleCount != 1 {
		t.Fatalf("rejected import changed target: tickets=%d messages=%d throttle=%d", ticketsCount, messagesCount, throttleCount)
	}
}

func TestImportLegacyTicketsRollsBackWhenSourceVerificationFails(t *testing.T) {
	database := newTestStore(t)
	admin, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "verify-admin@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "verify-owner@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("9", 64)
	insertHumanUsersLedger(t, database, sourceSHA)
	tickets := []LegacyTicket{{ID: 50, UserID: owner.ID, Subject: "Verify source", Level: TicketLevelMedium, Status: TicketStatusClosed, ReplyStatus: TicketReplyAnswered, LastReplyUserID: admin.ID, CreatedAt: 10, UpdatedAt: 20}}
	messages := []LegacyTicketMessage{{ID: 70, TicketID: 50, UserID: owner.ID, Message: "question", CreatedAt: 10, UpdatedAt: 10}, {ID: 71, TicketID: 50, UserID: admin.ID, Message: "answer", CreatedAt: 20, UpdatedAt: 20}}
	input := LegacyTicketsImport{Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 1,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets), MessageRows: len(messages), MessageChecksum: LegacyTicketMessagesChecksum(messages),
		MessageStream: legacyMessageSliceStream(messages), VerifySource: func(context.Context) error { return errors.New("source changed") },
		RollbackBackupPath: "/tmp/pre-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("8", 64)}
	if _, err := database.ImportLegacyTickets(t.Context(), input, time.Unix(30, 0)); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("ImportLegacyTickets(changed source) error = %v", err)
	}
	var ticketsCount, messagesCount, runs, ticketSequence, messageSequence int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&ticketsCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM ticket_messages`).Scan(&messagesCount)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTicketsSlice).Scan(&runs)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name='tickets'`).Scan(&ticketSequence)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM sqlite_sequence WHERE name='ticket_messages'`).Scan(&messageSequence)
	if ticketsCount != 0 || messagesCount != 0 || runs != 0 || ticketSequence != 0 || messageSequence != 0 {
		t.Fatalf("rollback state tickets=%d messages=%d runs=%d sequences=%d/%d", ticketsCount, messagesCount, runs, ticketSequence, messageSequence)
	}
}

func TestImportLegacyTicketsRejectsUnrelatedHumanAuthor(t *testing.T) {
	database := newTestStore(t)
	owner, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "author-owner@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "author-other@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("7", 64)
	insertHumanUsersLedger(t, database, sourceSHA)
	tickets := []LegacyTicket{{ID: 1, UserID: owner.ID, Subject: "Wrong author", Level: TicketLevelLow, Status: TicketStatusClosed, ReplyStatus: TicketReplyAnswered, LastReplyUserID: other.ID, CreatedAt: 1, UpdatedAt: 2}}
	messages := []LegacyTicketMessage{{ID: 1, TicketID: 1, UserID: owner.ID, Message: "question", CreatedAt: 1, UpdatedAt: 1}, {ID: 2, TicketID: 1, UserID: other.ID, Message: "not an administrator", CreatedAt: 2, UpdatedAt: 2}}
	input := LegacyTicketsImport{Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 1,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets), MessageRows: len(messages), MessageChecksum: LegacyTicketMessagesChecksum(messages),
		MessageStream: legacyMessageSliceStream(messages), VerifySource: func(context.Context) error { return nil },
		RollbackBackupPath: "/tmp/pre-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("6", 64)}
	if _, err := database.ImportLegacyTickets(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("ImportLegacyTickets(unrelated author) error = %v", err)
	}
}

func TestImportLegacyTicketsHasOneAtomicWinnerAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-tickets.db")
	setup, err := OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.Migrate(t.Context()); err != nil {
		_ = setup.Close()
		t.Fatal(err)
	}
	admin, err := setup.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "race-admin@example.test", PasswordHash: "hash", IsAdmin: true}, time.Unix(1, 0))
	if err != nil {
		_ = setup.Close()
		t.Fatal(err)
	}
	owner, err := setup.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "race-owner@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		_ = setup.Close()
		t.Fatal(err)
	}
	sourceSHA := strings.Repeat("5", 64)
	insertHumanUsersLedger(t, setup, sourceSHA)
	if err := setup.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenSQLite("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	tickets := []LegacyTicket{{ID: 10, UserID: owner.ID, Subject: "Concurrent", Level: TicketLevelHigh, Status: TicketStatusClosed, ReplyStatus: TicketReplyAnswered, LastReplyUserID: admin.ID, CreatedAt: 1, UpdatedAt: 2}}
	messages := []LegacyTicketMessage{{ID: 20, TicketID: 10, UserID: owner.ID, Message: "question", CreatedAt: 1, UpdatedAt: 1}, {ID: 21, TicketID: 10, UserID: admin.ID, Message: "answer", CreatedAt: 2, UpdatedAt: 2}}
	base := LegacyTicketsImport{Slice: LegacyTicketsSlice, SourceSHA256: sourceSHA, SourceSize: 1,
		Tickets: tickets, TicketChecksum: LegacyTicketsChecksum(tickets), MessageRows: len(messages), MessageChecksum: LegacyTicketMessagesChecksum(messages),
		RollbackBackupPath: "/tmp/pre-race-tickets.xbbackup", RollbackBackupSHA256: strings.Repeat("4", 64)}
	type outcome struct {
		report LegacyTicketsImportReport
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store) {
			defer wait.Done()
			<-start
			input := base
			input.MessageStream = legacyMessageSliceStream(messages)
			input.VerifySource = func(context.Context) error { return nil }
			report, err := database.ImportLegacyTickets(context.Background(), input, time.Unix(3, 0))
			results <- outcome{report: report, err: err}
		}(database)
	}
	close(start)
	wait.Wait()
	close(results)
	applied, repeated := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent ImportLegacyTickets() error = %v", result.err)
		}
		if result.report.AlreadyApplied {
			repeated++
		} else {
			applied++
		}
	}
	if applied != 1 || repeated != 1 {
		t.Fatalf("concurrent outcomes applied=%d repeated=%d", applied, repeated)
	}
	var ticketRows, messageRows, runs int
	_ = first.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&ticketRows)
	_ = first.db.QueryRow(`SELECT COUNT(*) FROM ticket_messages`).Scan(&messageRows)
	_ = first.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTicketsSlice).Scan(&runs)
	if ticketRows != 1 || messageRows != 2 || runs != 1 {
		t.Fatalf("concurrent target tickets=%d messages=%d runs=%d", ticketRows, messageRows, runs)
	}
}

func legacyMessageSliceStream(messages []LegacyTicketMessage) LegacyTicketMessageStream {
	return func(ctx context.Context, yield func(LegacyTicketMessage) error) error {
		for _, message := range messages {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := yield(message); err != nil {
				return err
			}
		}
		return nil
	}
}

func insertHumanUsersLedger(t *testing.T, database *Store, sourceSHA string) {
	t.Helper()
	if _, err := database.db.Exec(`
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?, ?, 1, '/tmp/pre-users.xbbackup', ?, '{}', 1)
	`, LegacyHumanUsersSlice, sourceSHA, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
}
