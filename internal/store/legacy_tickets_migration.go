package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyTicketsSlice       = "tickets-v1"
	maxLegacyTickets         = 1_000_000
	maxLegacyTicketMessages  = 10_000_000
	maxLegacyTicketDataBytes = int64(2 << 30)
)

type LegacyTicket struct {
	ID              int64             `json:"id"`
	UserID          int64             `json:"user_id"`
	Subject         string            `json:"subject"`
	Level           TicketLevel       `json:"level"`
	Status          TicketStatus      `json:"status"`
	ReplyStatus     TicketReplyStatus `json:"reply_status"`
	LastReplyUserID int64             `json:"last_reply_user_id"`
	CreatedAt       int64             `json:"created_at"`
	UpdatedAt       int64             `json:"updated_at"`
}

type LegacyTicketMessage struct {
	ID        int64  `json:"id"`
	TicketID  int64  `json:"ticket_id"`
	UserID    int64  `json:"user_id"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// LegacyTicketMessageStream permits large historical message bodies to be
// validated and inserted without retaining the complete domain in memory.
type LegacyTicketMessageStream func(context.Context, func(LegacyTicketMessage) error) error

type LegacyTicketsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Tickets              []LegacyTicket
	TicketChecksum       string
	MessageRows          int
	MessageChecksum      string
	MessageStream        LegacyTicketMessageStream
	VerifySource         func(context.Context) error
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyTicketsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Tickets              LegacyDomainResult `json:"tickets"`
	Messages             LegacyDomainResult `json:"messages"`
	TicketSequence       int64              `json:"ticket_sequence"`
	MessageSequence      int64              `json:"message_sequence"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

type legacyTicketDigest struct {
	digest hash.Hash
	number [8]byte
}

type LegacyTicketMessageChecksum struct {
	digest *legacyTicketDigest
}

func NewLegacyTicketMessageChecksum() *LegacyTicketMessageChecksum {
	return &LegacyTicketMessageChecksum{digest: newLegacyTicketDigest("ticket-messages")}
}

func (checksum *LegacyTicketMessageChecksum) Add(message LegacyTicketMessage) {
	checksum.digest.addMessage(message)
}

func (checksum *LegacyTicketMessageChecksum) Sum() string {
	return checksum.digest.sum()
}

func newLegacyTicketDigest(domain string) *legacyTicketDigest {
	digest := sha256.New()
	_, _ = io.WriteString(digest, "xboard-go/legacy/"+domain+"/v1\x00")
	return &legacyTicketDigest{digest: digest}
}

func (digest *legacyTicketDigest) writeInt64(value int64) {
	binary.BigEndian.PutUint64(digest.number[:], uint64(value))
	_, _ = digest.digest.Write(digest.number[:])
}

func (digest *legacyTicketDigest) writeString(value string) {
	digest.writeInt64(int64(len(value)))
	_, _ = io.WriteString(digest.digest, value)
}

func (digest *legacyTicketDigest) addTicket(ticket LegacyTicket) {
	digest.writeInt64(ticket.ID)
	digest.writeInt64(ticket.UserID)
	digest.writeString(ticket.Subject)
	digest.writeInt64(int64(ticket.Level))
	digest.writeInt64(int64(ticket.Status))
	digest.writeInt64(int64(ticket.ReplyStatus))
	digest.writeInt64(ticket.LastReplyUserID)
	digest.writeInt64(ticket.CreatedAt)
	digest.writeInt64(ticket.UpdatedAt)
}

func (digest *legacyTicketDigest) addMessage(message LegacyTicketMessage) {
	digest.writeInt64(message.ID)
	digest.writeInt64(message.TicketID)
	digest.writeInt64(message.UserID)
	digest.writeString(message.Message)
	digest.writeInt64(message.CreatedAt)
	digest.writeInt64(message.UpdatedAt)
}

func (digest *legacyTicketDigest) sum() string {
	return hex.EncodeToString(digest.digest.Sum(nil))
}

func LegacyTicketsChecksum(tickets []LegacyTicket) string {
	ordered := append([]LegacyTicket(nil), tickets...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	digest := newLegacyTicketDigest("tickets")
	for _, ticket := range ordered {
		digest.addTicket(ticket)
	}
	return digest.sum()
}

func LegacyTicketMessagesChecksum(messages []LegacyTicketMessage) string {
	ordered := append([]LegacyTicketMessage(nil), messages...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	digest := NewLegacyTicketMessageChecksum()
	for _, message := range ordered {
		digest.Add(message)
	}
	return digest.Sum()
}

func ValidateLegacyTicketsData(tickets []LegacyTicket) error {
	if len(tickets) > maxLegacyTickets {
		return fmt.Errorf("%w: legacy tickets exceed the %d-row migration limit", ErrInvalidInput, maxLegacyTickets)
	}
	openOwners := make(map[int64]struct{})
	var previousID int64
	var relevantBytes int64
	for _, ticket := range tickets {
		if ticket.ID <= previousID || ticket.ID < 1 || ticket.UserID < 1 || ticket.LastReplyUserID < 1 ||
			!utf8.ValidString(ticket.Subject) || !validTicketSubject(ticket.Subject) || !validTicketLevel(ticket.Level) ||
			!validTicketStatus(ticket.Status) || !validTicketReplyStatus(ticket.ReplyStatus) ||
			!validLegacyUnixTimestamp(ticket.CreatedAt) || !validLegacyUnixTimestamp(ticket.UpdatedAt) || ticket.UpdatedAt < ticket.CreatedAt {
			return fmt.Errorf("%w: invalid legacy ticket id %d", ErrInvalidInput, ticket.ID)
		}
		if ticket.Status == TicketStatusOpen {
			if _, duplicate := openOwners[ticket.UserID]; duplicate {
				return fmt.Errorf("%w: legacy user %d has multiple open tickets", ErrConflict, ticket.UserID)
			}
			openOwners[ticket.UserID] = struct{}{}
		}
		previousID = ticket.ID
		relevantBytes += int64(len(ticket.Subject))
		if relevantBytes > maxLegacyTicketDataBytes {
			return fmt.Errorf("%w: legacy ticket data exceed the migration byte limit", ErrInvalidInput)
		}
	}
	return nil
}

func ValidateLegacyTicketMessageData(message LegacyTicketMessage) error {
	if message.ID < 1 || message.TicketID < 1 || message.UserID < 1 || !utf8.ValidString(message.Message) ||
		!validTicketMessage(message.Message) || !validLegacyUnixTimestamp(message.CreatedAt) ||
		!validLegacyUnixTimestamp(message.UpdatedAt) || message.UpdatedAt < message.CreatedAt {
		return fmt.Errorf("%w: invalid legacy ticket message id %d", ErrInvalidInput, message.ID)
	}
	return nil
}

func (s *Store) LookupLegacyTicketsImport(ctx context.Context, sourceSHA256 string) (LegacyTicketsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyTicketsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyTicketsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyTicketsImport(ctx context.Context, database queryer, sourceSHA256 string) (LegacyTicketsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyTicketsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyTicketsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyTicketsImportReport{}, false, fmt.Errorf("lookup legacy ticket migration: %w", err)
	}
	var report LegacyTicketsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyTicketsImportReport{}, false, fmt.Errorf("decode legacy ticket migration report: %w", err)
	}
	if err := validateLegacyTicketsReport(report, sourceSHA256); err != nil {
		return LegacyTicketsImportReport{}, false, err
	}
	if err := verifyLegacyTicketsTarget(ctx, database, report); err != nil {
		return LegacyTicketsImportReport{}, false, err
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyTickets(ctx context.Context, input LegacyTicketsImport, now time.Time) (LegacyTicketsImportReport, error) {
	if err := validateLegacyTicketsImport(input); err != nil {
		return LegacyTicketsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("begin legacy ticket import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("read legacy ticket target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyTicketsImportReport{}, fmt.Errorf("legacy ticket import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("validate legacy ticket target schema: %w", err)
	}
	if existing, found, err := lookupLegacyTicketsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyTicketsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyTicketsImportReport{}, fmt.Errorf("commit idempotent legacy ticket import: %w", err)
		}
		return existing, nil
	}
	if err := requireLegacyTicketPrerequisites(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyTicketsImportReport{}, err
	}
	roles, err := readLegacyTicketHumanRoles(ctx, tx)
	if err != nil {
		return LegacyTicketsImportReport{}, err
	}
	states := make([]legacyTicketState, len(input.Tickets))
	stateByTicketID := make(map[int64]int, len(input.Tickets))
	ticketStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO tickets
		(id,user_id,subject,level,status,reply_status,last_reply_user_id,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("prepare legacy ticket import: %w", err)
	}
	defer ticketStatement.Close()
	var maxTicketID int64
	var relevantBytes int64
	for index, ticket := range input.Tickets {
		_, ownerExists := roles[ticket.UserID]
		lastAdmin, lastExists := roles[ticket.LastReplyUserID]
		if !ownerExists || !lastExists || ticket.LastReplyUserID != ticket.UserID && !lastAdmin {
			return LegacyTicketsImportReport{}, fmt.Errorf("%w: legacy ticket id %d references a missing or non-administrator human user", ErrConflict, ticket.ID)
		}
		states[index] = legacyTicketState{ticket: ticket}
		stateByTicketID[ticket.ID] = index
		if _, err := ticketStatement.ExecContext(ctx, ticket.ID, ticket.UserID, ticket.Subject, ticket.Level, ticket.Status,
			ticket.ReplyStatus, ticket.LastReplyUserID, ticket.CreatedAt, ticket.UpdatedAt); err != nil {
			return LegacyTicketsImportReport{}, fmt.Errorf("import legacy ticket id %d: %w", ticket.ID, err)
		}
		maxTicketID = ticket.ID
		relevantBytes += int64(len(ticket.Subject))
	}
	messageStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO ticket_messages (id,ticket_id,user_id,message,created_at,updated_at) VALUES (?,?,?,?,?,?)
	`)
	if err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("prepare legacy ticket message import: %w", err)
	}
	defer messageStatement.Close()
	messageDigest := newLegacyTicketDigest("ticket-messages")
	messageRows := 0
	var previousMessageID, maxMessageID int64
	streamErr := input.MessageStream(ctx, func(message LegacyTicketMessage) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		messageRows++
		if messageRows > maxLegacyTicketMessages || messageRows > input.MessageRows || message.ID <= previousMessageID {
			return fmt.Errorf("%w: invalid legacy ticket message id %d", ErrInvalidInput, message.ID)
		}
		if err := ValidateLegacyTicketMessageData(message); err != nil {
			return err
		}
		if int64(len(message.Message)) > maxLegacyTicketDataBytes-relevantBytes {
			return fmt.Errorf("%w: legacy ticket data exceed the migration byte limit", ErrInvalidInput)
		}
		relevantBytes += int64(len(message.Message))
		stateIndex, exists := stateByTicketID[message.TicketID]
		if !exists {
			return fmt.Errorf("%w: legacy ticket message id %d references a missing ticket", ErrConflict, message.ID)
		}
		state := &states[stateIndex]
		isAdmin, humanExists := roles[message.UserID]
		if !humanExists || message.UserID != state.ticket.UserID && !isAdmin {
			return fmt.Errorf("%w: legacy ticket message id %d references a missing or unrelated human user", ErrConflict, message.ID)
		}
		state.observe(message)
		messageDigest.addMessage(message)
		if _, err := messageStatement.ExecContext(ctx, message.ID, message.TicketID, message.UserID, message.Message,
			message.CreatedAt, message.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy ticket message id %d: %w", message.ID, err)
		}
		previousMessageID, maxMessageID = message.ID, message.ID
		return nil
	})
	if streamErr != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("stream legacy ticket messages: %w", streamErr)
	}
	if messageRows != input.MessageRows || messageDigest.sum() != input.MessageChecksum {
		return LegacyTicketsImportReport{}, errors.New("legacy ticket message stream does not match the validated source")
	}
	for index, ticket := range input.Tickets {
		state := &states[index]
		if state.messageCount == 0 {
			return LegacyTicketsImportReport{}, fmt.Errorf("%w: legacy ticket id %d has no messages", ErrConflict, ticket.ID)
		}
		if state.lastUserID != ticket.LastReplyUserID {
			return LegacyTicketsImportReport{}, fmt.Errorf("%w: legacy ticket id %d last reply does not match its final message", ErrConflict, ticket.ID)
		}
		wantReply := TicketReplyWaiting
		if state.lastUserID != ticket.UserID {
			wantReply = TicketReplyAnswered
		}
		if ticket.ReplyStatus != wantReply {
			return LegacyTicketsImportReport{}, fmt.Errorf("%w: legacy ticket id %d reply status does not match its final author", ErrConflict, ticket.ID)
		}
	}
	if err := advanceLegacySequence(ctx, tx, "tickets", maxTicketID); err != nil {
		return LegacyTicketsImportReport{}, err
	}
	if err := advanceLegacySequence(ctx, tx, "ticket_messages", maxMessageID); err != nil {
		return LegacyTicketsImportReport{}, err
	}
	ticketSequence, err := readLegacySequence(ctx, tx, "tickets")
	if err != nil {
		return LegacyTicketsImportReport{}, err
	}
	messageSequence, err := readLegacySequence(ctx, tx, "ticket_messages")
	if err != nil {
		return LegacyTicketsImportReport{}, err
	}
	report := LegacyTicketsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Tickets:        LegacyDomainResult{SourceRows: len(input.Tickets), TargetRows: len(input.Tickets), SourceChecksum: input.TicketChecksum, TargetChecksum: input.TicketChecksum},
		Messages:       LegacyDomainResult{SourceRows: input.MessageRows, TargetRows: input.MessageRows, SourceChecksum: input.MessageChecksum, TargetChecksum: input.MessageChecksum},
		TicketSequence: ticketSequence, MessageSequence: messageSequence, AppliedAt: now.UTC(),
	}
	if err := verifyLegacyTicketsTarget(ctx, tx, report); err != nil {
		return LegacyTicketsImportReport{}, err
	}
	if err := input.VerifySource(ctx); err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("verify legacy ticket source before commit: %w", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("encode legacy ticket migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES (?,?,?,?,?,?,?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("record legacy ticket migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyTicketsImportReport{}, fmt.Errorf("commit legacy ticket import: %w", err)
	}
	return report, nil
}

type legacyTicketState struct {
	ticket        LegacyTicket
	messageCount  int
	lastCreatedAt int64
	lastMessageID int64
	lastUserID    int64
}

func (state *legacyTicketState) observe(message LegacyTicketMessage) {
	state.messageCount++
	if state.messageCount == 1 || message.CreatedAt > state.lastCreatedAt ||
		message.CreatedAt == state.lastCreatedAt && message.ID > state.lastMessageID {
		state.lastCreatedAt = message.CreatedAt
		state.lastMessageID = message.ID
		state.lastUserID = message.UserID
	}
}

func validateLegacyTicketsImport(input LegacyTicketsImport) error {
	if input.Slice != LegacyTicketsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.TicketChecksum != LegacyTicketsChecksum(input.Tickets) || input.MessageRows < 0 || input.MessageRows > maxLegacyTicketMessages ||
		!validLowerSHA256(input.MessageChecksum) || input.MessageStream == nil || input.VerifySource == nil {
		return fmt.Errorf("%w: invalid legacy ticket import", ErrInvalidInput)
	}
	return ValidateLegacyTicketsData(input.Tickets)
}

func requireLegacyTicketPrerequisites(ctx context.Context, tx *sql.Tx, sourceSHA256 string) error {
	var humans int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyHumanUsersSlice, sourceSHA256).Scan(&humans); err != nil {
		return fmt.Errorf("validate legacy ticket human-user migration: %w", err)
	}
	if humans != 1 {
		return fmt.Errorf("%w: import the same legacy human-user snapshot before tickets", ErrConflict)
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTicketsSlice).Scan(&otherRuns); err != nil {
		return fmt.Errorf("count legacy ticket migrations: %w", err)
	}
	if otherRuns != 0 {
		return fmt.Errorf("%w: legacy ticket slice was already imported from another snapshot", ErrConflict)
	}
	for _, table := range []string{"tickets", "ticket_messages", "ticket_mail_outbox", "ticket_mail_throttle"} {
		var rows int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&rows); err != nil {
			return fmt.Errorf("count target %s: %w", table, err)
		}
		if rows != 0 {
			return fmt.Errorf("%w: legacy ticket import requires empty ticket targets", ErrConflict)
		}
	}
	return nil
}

func readLegacyTicketHumanRoles(ctx context.Context, database queryer) (map[int64]bool, error) {
	rows, err := database.QueryContext(ctx, `SELECT id,is_admin FROM users WHERE account_kind='human' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read target human users for legacy tickets: %w", err)
	}
	defer rows.Close()
	roles := make(map[int64]bool)
	for rows.Next() {
		var id int64
		var admin bool
		if err := rows.Scan(&id, &admin); err != nil {
			return nil, fmt.Errorf("scan target human user for legacy tickets: %w", err)
		}
		roles[id] = admin
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate target human users for legacy tickets: %w", err)
	}
	return roles, nil
}

func advanceLegacySequence(ctx context.Context, tx *sql.Tx, name string, maximum int64) error {
	if maximum < 1 {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=CASE WHEN seq < ? THEN ? ELSE seq END WHERE name=?`, maximum, maximum, name)
	if err != nil {
		return fmt.Errorf("advance legacy %s sequence: %w", name, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect legacy %s sequence update: %w", name, err)
	}
	if rows == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)`, name, maximum); err != nil {
			return fmt.Errorf("create legacy %s sequence: %w", name, err)
		}
	}
	return nil
}

func validateLegacyTicketsReport(report LegacyTicketsImportReport, sourceSHA256 string) error {
	validDomain := func(domain LegacyDomainResult, maximum int) bool {
		return domain.SourceRows >= 0 && domain.SourceRows <= maximum && domain.TargetRows == domain.SourceRows &&
			validLowerSHA256(domain.SourceChecksum) && domain.TargetChecksum == domain.SourceChecksum
	}
	if report.Slice != LegacyTicketsSlice || report.SourceSHA256 != sourceSHA256 || report.SourceSize < 1 ||
		report.RollbackBackupPath == "" || len(report.RollbackBackupPath) > 4096 || !utf8.ValidString(report.RollbackBackupPath) ||
		strings.IndexFunc(report.RollbackBackupPath, unicode.IsControl) >= 0 || !validLowerSHA256(report.RollbackBackupSHA256) ||
		report.TicketSequence < 0 || report.MessageSequence < 0 || report.AppliedAt.IsZero() ||
		!validDomain(report.Tickets, maxLegacyTickets) || !validDomain(report.Messages, maxLegacyTicketMessages) {
		return errors.New("stored legacy ticket migration report is invalid")
	}
	return nil
}

func verifyLegacyTicketsTarget(ctx context.Context, database queryer, report LegacyTicketsImportReport) error {
	ticketRows, ticketChecksum, err := readLegacyTargetTicketDigest(ctx, database)
	if err != nil {
		return err
	}
	messageRows, messageChecksum, err := readLegacyTargetTicketMessageDigest(ctx, database)
	if err != nil {
		return err
	}
	if ticketRows != report.Tickets.TargetRows || ticketChecksum != report.Tickets.TargetChecksum ||
		messageRows != report.Messages.TargetRows || messageChecksum != report.Messages.TargetChecksum {
		return fmt.Errorf("%w: imported legacy ticket target no longer matches its migration ledger", ErrConflict)
	}
	ticketSequence, err := readLegacySequence(ctx, database, "tickets")
	if err != nil {
		return err
	}
	messageSequence, err := readLegacySequence(ctx, database, "ticket_messages")
	if err != nil {
		return err
	}
	if ticketSequence != report.TicketSequence || messageSequence != report.MessageSequence {
		return fmt.Errorf("%w: imported legacy ticket sequence no longer matches its migration ledger", ErrConflict)
	}
	return nil
}

func readLegacySequence(ctx context.Context, database queryer, name string) (int64, error) {
	var rows int
	var sequence int64
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(MAX(seq),0) FROM sqlite_sequence WHERE name=?`, name).Scan(&rows, &sequence); err != nil {
		return 0, fmt.Errorf("read legacy %s sequence: %w", name, err)
	}
	if rows > 1 || sequence < 0 {
		return 0, fmt.Errorf("%w: legacy %s sequence is invalid", ErrConflict, name)
	}
	return sequence, nil
}

func readLegacyTargetTicketDigest(ctx context.Context, database queryer) (int, string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,user_id,subject,level,status,reply_status,last_reply_user_id,created_at,updated_at
		FROM tickets ORDER BY id
	`)
	if err != nil {
		return 0, "", fmt.Errorf("read imported legacy tickets: %w", err)
	}
	defer rows.Close()
	digest := newLegacyTicketDigest("tickets")
	count := 0
	for rows.Next() {
		var ticket LegacyTicket
		if err := rows.Scan(&ticket.ID, &ticket.UserID, &ticket.Subject, &ticket.Level, &ticket.Status, &ticket.ReplyStatus,
			&ticket.LastReplyUserID, &ticket.CreatedAt, &ticket.UpdatedAt); err != nil {
			return 0, "", fmt.Errorf("scan imported legacy ticket: %w", err)
		}
		count++
		digest.addTicket(ticket)
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("iterate imported legacy tickets: %w", err)
	}
	return count, digest.sum(), nil
}

func readLegacyTargetTicketMessageDigest(ctx context.Context, database queryer) (int, string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,ticket_id,user_id,message,created_at,updated_at FROM ticket_messages ORDER BY id
	`)
	if err != nil {
		return 0, "", fmt.Errorf("read imported legacy ticket messages: %w", err)
	}
	defer rows.Close()
	digest := newLegacyTicketDigest("ticket-messages")
	count := 0
	for rows.Next() {
		var message LegacyTicketMessage
		if err := rows.Scan(&message.ID, &message.TicketID, &message.UserID, &message.Message, &message.CreatedAt, &message.UpdatedAt); err != nil {
			return 0, "", fmt.Errorf("scan imported legacy ticket message: %w", err)
		}
		count++
		digest.addMessage(message)
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("iterate imported legacy ticket messages: %w", err)
	}
	return count, digest.sum(), nil
}
