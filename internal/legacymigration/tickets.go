package legacymigration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyTicketRows        = 1_000_000
	maxLegacyTicketMessageRows = 10_000_000
	maxLegacyTicketBytes       = int64(2 << 30)
)

var legacyTicketColumns = []string{
	"id", "user_id", "subject", "level", "status", "reply_status", "created_at", "updated_at", "last_reply_user_id",
}

var legacyTicketMessageColumns = []string{
	"id", "user_id", "ticket_id", "message", "created_at", "updated_at",
}

type TicketsSnapshot struct {
	Path            string
	Size            int64
	SHA256          string
	Tickets         []store.LegacyTicket
	TicketChecksum  string
	MessageRows     int
	MessageChecksum string
}

type legacyTicketCandidate struct {
	ticket    store.LegacyTicket
	lastReply sql.NullInt64
	state     legacySourceTicketState
}

type legacySourceTicketState struct {
	messageCount  int
	lastCreatedAt int64
	lastMessageID int64
	lastUserID    int64
}

func (state *legacySourceTicketState) observe(message store.LegacyTicketMessage) {
	state.messageCount++
	if state.messageCount == 1 || message.CreatedAt > state.lastCreatedAt ||
		message.CreatedAt == state.lastCreatedAt && message.ID > state.lastMessageID {
		state.lastCreatedAt = message.CreatedAt
		state.lastMessageID = message.ID
		state.lastUserID = message.UserID
	}
}

func ReadTicketsSnapshot(ctx context.Context, sourcePath string) (TicketsSnapshot, error) {
	var tickets []store.LegacyTicket
	var messageRows int
	var messageChecksum string
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireLegacyTicketTables(ctx, database); err != nil {
			return err
		}
		if err := validateLegacyTicketBudgets(ctx, database); err != nil {
			return err
		}
		candidates, err := readLegacyTicketCandidates(ctx, database)
		if err != nil {
			return err
		}
		byID := make(map[int64]*legacyTicketCandidate, len(candidates))
		for index := range candidates {
			candidate := &candidates[index]
			if _, duplicate := byID[candidate.ticket.ID]; duplicate {
				return fmt.Errorf("legacy tickets contain duplicate id %d", candidate.ticket.ID)
			}
			byID[candidate.ticket.ID] = candidate
		}
		checksum := store.NewLegacyTicketMessageChecksum()
		var previousMessageID int64
		err = streamLegacyTicketMessages(ctx, database, func(message store.LegacyTicketMessage) error {
			if message.ID <= previousMessageID {
				return fmt.Errorf("legacy ticket messages are not strictly ordered by id")
			}
			if err := store.ValidateLegacyTicketMessageData(message); err != nil {
				return err
			}
			candidate, exists := byID[message.TicketID]
			if !exists {
				return fmt.Errorf("legacy ticket message id %d references a missing ticket", message.ID)
			}
			candidate.state.observe(message)
			checksum.Add(message)
			messageRows++
			previousMessageID = message.ID
			if messageRows > maxLegacyTicketMessageRows {
				return fmt.Errorf("legacy ticket messages exceed the %d-row migration limit", maxLegacyTicketMessageRows)
			}
			return nil
		})
		if err != nil {
			return err
		}
		messageChecksum = checksum.Sum()
		tickets = make([]store.LegacyTicket, len(candidates))
		for index := range candidates {
			candidate := &candidates[index]
			if candidate.state.messageCount == 0 {
				return fmt.Errorf("legacy ticket id %d has no messages", candidate.ticket.ID)
			}
			if candidate.lastReply.Valid {
				if candidate.lastReply.Int64 < 1 || candidate.lastReply.Int64 != candidate.state.lastUserID {
					return fmt.Errorf("legacy ticket id %d last reply does not match its final message", candidate.ticket.ID)
				}
				candidate.ticket.LastReplyUserID = candidate.lastReply.Int64
			} else {
				candidate.ticket.LastReplyUserID = candidate.state.lastUserID
			}
			wantReply := store.TicketReplyWaiting
			if candidate.ticket.LastReplyUserID != candidate.ticket.UserID {
				wantReply = store.TicketReplyAnswered
			}
			if candidate.ticket.ReplyStatus != wantReply {
				return fmt.Errorf("legacy ticket id %d reply status does not match its final author", candidate.ticket.ID)
			}
			tickets[index] = candidate.ticket
		}
		if err := store.ValidateLegacyTicketsData(tickets); err != nil {
			return fmt.Errorf("validate legacy tickets: %w", err)
		}
		return nil
	})
	if err != nil {
		return TicketsSnapshot{}, err
	}
	return TicketsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Tickets: tickets, TicketChecksum: store.LegacyTicketsChecksum(tickets),
		MessageRows: messageRows, MessageChecksum: messageChecksum,
	}, nil
}

func requireLegacyTicketTables(ctx context.Context, database *sql.DB) error {
	if err := requireRealTable(ctx, database, "v2_ticket", legacyTicketColumns); err != nil {
		return err
	}
	return requireRealTable(ctx, database, "v2_ticket_message", legacyTicketMessageColumns)
}

func validateLegacyTicketBudgets(ctx context.Context, database *sql.DB) error {
	var ticketRows, ticketBytes, messageRows, messageBytes int64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*),COALESCE(SUM(length(CAST(subject AS BLOB))),0) FROM v2_ticket
	`).Scan(&ticketRows, &ticketBytes); err != nil {
		return fmt.Errorf("measure legacy tickets: %w", err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*),COALESCE(SUM(length(CAST(message AS BLOB))),0) FROM v2_ticket_message
	`).Scan(&messageRows, &messageBytes); err != nil {
		return fmt.Errorf("measure legacy ticket messages: %w", err)
	}
	if ticketRows < 0 || ticketRows > maxLegacyTicketRows {
		return fmt.Errorf("legacy tickets exceed the %d-row migration limit", maxLegacyTicketRows)
	}
	if messageRows < 0 || messageRows > maxLegacyTicketMessageRows {
		return fmt.Errorf("legacy ticket messages exceed the %d-row migration limit", maxLegacyTicketMessageRows)
	}
	if ticketBytes < 0 || messageBytes < 0 || ticketBytes > maxLegacyTicketBytes-messageBytes {
		return fmt.Errorf("legacy ticket data exceed the %d-byte migration limit", maxLegacyTicketBytes)
	}
	return nil
}

func readLegacyTicketCandidates(ctx context.Context, database *sql.DB) ([]legacyTicketCandidate, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id,user_id,subject,level,status,reply_status,
		       `+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+`,last_reply_user_id
		FROM v2_ticket ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read legacy tickets: %w", err)
	}
	defer rows.Close()
	result := make([]legacyTicketCandidate, 0)
	var previousID int64
	for rows.Next() {
		if len(result) >= maxLegacyTicketRows {
			return nil, fmt.Errorf("legacy tickets exceed the %d-row migration limit", maxLegacyTicketRows)
		}
		var candidate legacyTicketCandidate
		if err := rows.Scan(&candidate.ticket.ID, &candidate.ticket.UserID, &candidate.ticket.Subject,
			&candidate.ticket.Level, &candidate.ticket.Status, &candidate.ticket.ReplyStatus,
			&candidate.ticket.CreatedAt, &candidate.ticket.UpdatedAt, &candidate.lastReply); err != nil {
			return nil, fmt.Errorf("scan legacy ticket: %w", err)
		}
		if candidate.ticket.ID <= previousID {
			return nil, errors.New("legacy tickets are not strictly ordered by id")
		}
		previousID = candidate.ticket.ID
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy tickets: %w", err)
	}
	return result, nil
}

func streamLegacyTicketMessages(ctx context.Context, database *sql.DB, yield func(store.LegacyTicketMessage) error) error {
	rows, err := database.QueryContext(ctx, `
		SELECT id,ticket_id,user_id,message,
		       `+legacyUnixExpression("created_at")+`,`+legacyUnixExpression("updated_at")+`
		FROM v2_ticket_message ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("read legacy ticket messages: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > maxLegacyTicketMessageRows {
			return fmt.Errorf("legacy ticket messages exceed the %d-row migration limit", maxLegacyTicketMessageRows)
		}
		var message store.LegacyTicketMessage
		if err := rows.Scan(&message.ID, &message.TicketID, &message.UserID, &message.Message, &message.CreatedAt, &message.UpdatedAt); err != nil {
			return fmt.Errorf("scan legacy ticket message: %w", err)
		}
		if err := yield(message); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy ticket messages: %w", err)
	}
	return nil
}

type TicketMessageStreamSession struct {
	mu       sync.Mutex
	database *sql.DB
	path     string
	expected snapshotIdentity
	opened   os.FileInfo
	streamed bool
	closed   bool
}

func (snapshot TicketsSnapshot) OpenMessageStream(ctx context.Context) (*TicketMessageStreamSession, error) {
	info, err := os.Lstat(snapshot.Path)
	if err != nil {
		return nil, fmt.Errorf("reinspect legacy ticket snapshot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != snapshot.Size {
		return nil, errors.New("legacy ticket snapshot identity changed before streaming")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(snapshot.Path + suffix); err == nil {
			return nil, fmt.Errorf("legacy ticket snapshot gained an adjacent SQLite %s file", suffix[1:])
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect legacy ticket snapshot sidecar: %w", err)
		}
	}
	digest, opened, err := hashRegularFile(ctx, snapshot.Path, info)
	if err != nil {
		return nil, err
	}
	if digest != snapshot.SHA256 || opened.Size() != snapshot.Size {
		return nil, errors.New("legacy ticket snapshot digest changed before streaming")
	}
	database, err := sql.Open("sqlite", readOnlyImmutableDSN(snapshot.Path))
	if err != nil {
		return nil, fmt.Errorf("open legacy ticket message stream: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	closeOnError := func(current error) (*TicketMessageStreamSession, error) {
		if closeErr := database.Close(); closeErr != nil {
			return nil, errors.Join(current, closeErr)
		}
		return nil, current
	}
	if err := database.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("ping legacy ticket message stream: %w", err))
	}
	if err := validateLegacySnapshot(ctx, database); err != nil {
		return closeOnError(err)
	}
	if err := requireLegacyTicketTables(ctx, database); err != nil {
		return closeOnError(err)
	}
	return &TicketMessageStreamSession{
		database: database, path: snapshot.Path, expected: snapshotIdentity{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256}, opened: opened,
	}, nil
}

func (session *TicketMessageStreamSession) Stream(ctx context.Context, yield func(store.LegacyTicketMessage) error) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.streamed || yield == nil {
		return errors.New("legacy ticket message stream is closed, invalid, or already consumed")
	}
	session.streamed = true
	return streamLegacyTicketMessages(ctx, session.database, yield)
}

func (session *TicketMessageStreamSession) VerifyAndClose(ctx context.Context) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || !session.streamed {
		return errors.New("legacy ticket message stream must be consumed exactly once before verification")
	}
	session.closed = true
	if err := session.database.Close(); err != nil {
		return fmt.Errorf("close legacy ticket message stream: %w", err)
	}
	info, err := os.Lstat(session.path)
	if err != nil {
		return fmt.Errorf("reinspect legacy ticket snapshot after streaming: %w", err)
	}
	digest, finalInfo, err := hashRegularFile(ctx, session.path, session.opened)
	if err != nil {
		return err
	}
	if digest != session.expected.SHA256 || finalInfo.Size() != session.expected.Size ||
		!finalInfo.ModTime().Equal(info.ModTime()) || !session.opened.ModTime().Equal(finalInfo.ModTime()) {
		return errors.New("legacy ticket snapshot changed while messages were being streamed")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(session.path + suffix); err == nil {
			return fmt.Errorf("legacy ticket snapshot gained an adjacent SQLite %s file while streaming", suffix[1:])
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reinspect legacy ticket snapshot sidecar: %w", err)
		}
	}
	return nil
}

func (session *TicketMessageStreamSession) Close() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil
	}
	session.closed = true
	return session.database.Close()
}
