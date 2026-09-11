package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The ledger, rather than a ticket's text or closed state, owns settlement.
// Historical tickets are deliberately not converted into financial obligations.
// Money writes also invalidate administrator edit snapshots, including writes
// from payments and commission jobs. Otherwise a stale absolute-value form can
// silently recreate frozen funds or erase a refund. Explicit admin edits already
// advance the revision and must not receive a second increment.
const schemaV60CommissionWithdrawals = `
CREATE TRIGGER IF NOT EXISTS users_money_admin_revision AFTER UPDATE OF balance, commission_balance ON users
 WHEN (NEW.balance <> OLD.balance OR NEW.commission_balance <> OLD.commission_balance)
   AND NEW.admin_revision = OLD.admin_revision
 BEGIN UPDATE users SET admin_revision = admin_revision + 1 WHERE id = NEW.id; END;
CREATE TABLE IF NOT EXISTS commission_withdrawals (
 id INTEGER PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 ticket_id INTEGER NOT NULL UNIQUE REFERENCES tickets(id) ON DELETE RESTRICT,
 amount INTEGER NOT NULL CHECK(typeof(amount) = 'integer' AND amount > 0 AND amount <= 9000000000000000),
 status TEXT NOT NULL CHECK(status IN ('pending','approved','paid','rejected')),
 request_key TEXT CHECK(request_key IS NULL OR length(request_key) BETWEEN 8 AND 128),
 method TEXT NOT NULL CHECK(length(method) BETWEEN 1 AND 64),
 account TEXT NOT NULL CHECK(length(account) BETWEEN 1 AND 512),
 payment_reference TEXT NOT NULL DEFAULT '' CHECK(length(payment_reference) <= 128),
 payment_reference_hash TEXT NOT NULL DEFAULT '',
 anonymized_at INTEGER CHECK(anonymized_at IS NULL OR anonymized_at >= updated_at),
 created_at INTEGER NOT NULL CHECK(created_at >= 0),
 updated_at INTEGER NOT NULL CHECK(updated_at >= created_at),
 UNIQUE(user_id, request_key),
 CHECK((status = 'paid' AND length(payment_reference) > 0 AND length(payment_reference_hash) = 64) OR (status <> 'paid' AND payment_reference = '' AND payment_reference_hash = ''))
);
CREATE UNIQUE INDEX IF NOT EXISTS commission_withdrawals_active_user ON commission_withdrawals(user_id) WHERE status IN ('pending','approved');
CREATE INDEX IF NOT EXISTS commission_withdrawals_user_history ON commission_withdrawals(user_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS commission_withdrawals_payment_reference ON commission_withdrawals(method, payment_reference_hash) WHERE status = 'paid';
CREATE TABLE IF NOT EXISTS commission_withdrawal_events (
 id INTEGER PRIMARY KEY,
 withdrawal_id INTEGER NOT NULL REFERENCES commission_withdrawals(id) ON DELETE RESTRICT,
 actor_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
 status TEXT NOT NULL CHECK(status IN ('pending','approved','paid','rejected')),
 available_delta INTEGER NOT NULL,
 frozen_delta INTEGER NOT NULL,
 paid_delta INTEGER NOT NULL,
 created_at INTEGER NOT NULL CHECK(created_at >= 0),
 UNIQUE(withdrawal_id, status),
 CHECK(typeof(available_delta) = 'integer' AND typeof(frozen_delta) = 'integer' AND typeof(paid_delta) = 'integer'),
 CHECK(available_delta + frozen_delta + paid_delta = 0),
 CHECK((status = 'pending' AND available_delta < 0 AND frozen_delta = -available_delta AND paid_delta = 0)
    OR (status = 'approved' AND available_delta = 0 AND frozen_delta = 0 AND paid_delta = 0)
    OR (status = 'paid' AND available_delta = 0 AND frozen_delta < 0 AND paid_delta = -frozen_delta)
    OR (status = 'rejected' AND available_delta > 0 AND frozen_delta = -available_delta AND paid_delta = 0))
);
CREATE TRIGGER IF NOT EXISTS commission_withdrawals_immutable BEFORE UPDATE ON commission_withdrawals
 WHEN OLD.user_id <> NEW.user_id OR OLD.ticket_id <> NEW.ticket_id OR OLD.amount <> NEW.amount
   OR OLD.request_key IS NOT NEW.request_key OR OLD.method <> NEW.method
	   OR OLD.created_at <> NEW.created_at
 BEGIN SELECT RAISE(ABORT, 'withdrawal financial facts are immutable'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawals_account_immutable BEFORE UPDATE ON commission_withdrawals
 WHEN (OLD.account <> NEW.account OR OLD.anonymized_at IS NOT NEW.anonymized_at
       OR (OLD.status = 'paid' AND OLD.payment_reference <> NEW.payment_reference))
   AND NOT (OLD.anonymized_at IS NULL AND NEW.anonymized_at IS NOT NULL
       AND OLD.status IN ('paid','rejected') AND OLD.status = NEW.status
       AND NEW.account = '[anonymized]' AND NEW.payment_reference = CASE WHEN OLD.status = 'paid' THEN '[anonymized]' ELSE '' END)
 BEGIN SELECT RAISE(ABORT, 'withdrawal account may only be anonymized once after settlement'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawals_transition BEFORE UPDATE ON commission_withdrawals
 WHEN NOT ((OLD.status = 'pending' AND NEW.status IN ('approved','rejected')) OR (OLD.status = 'approved' AND NEW.status IN ('paid','rejected'))
   OR (OLD.status IN ('paid','rejected') AND NEW.status = OLD.status AND OLD.anonymized_at IS NULL AND NEW.anonymized_at IS NOT NULL))
 BEGIN SELECT RAISE(ABORT, 'invalid withdrawal state transition'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawals_receipt_immutable BEFORE UPDATE ON commission_withdrawals
 WHEN OLD.status = 'paid' AND OLD.payment_reference_hash <> NEW.payment_reference_hash
 BEGIN SELECT RAISE(ABORT, 'withdrawal receipt digest is immutable'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawals_no_delete BEFORE DELETE ON commission_withdrawals
 BEGIN SELECT RAISE(ABORT, 'withdrawal ledger is retained'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawal_events_no_update BEFORE UPDATE ON commission_withdrawal_events
 BEGIN SELECT RAISE(ABORT, 'withdrawal events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS commission_withdrawal_events_no_delete BEFORE DELETE ON commission_withdrawal_events
 BEGIN SELECT RAISE(ABORT, 'withdrawal events are append-only'); END;
`

type CommissionWithdrawal struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	TicketID         int64     `json:"ticket_id"`
	Amount           int64     `json:"amount"`
	Status           string    `json:"status"`
	Method           string    `json:"method"`
	Account          string    `json:"account"`
	PaymentReference string    `json:"payment_reference"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CommissionWithdrawalTransitionInput struct {
	Status           string `json:"status"`
	PaymentReference string `json:"payment_reference"`
}

var ErrCommissionWithdrawalState = fmt.Errorf("%w: invalid commission withdrawal transition", ErrConflict)
var ErrCommissionWithdrawalReplay = fmt.Errorf("%w: withdrawal request conflicts with a previous request", ErrConflict)

const withdrawalColumns = `id, user_id, ticket_id, amount, status, method, account, payment_reference, created_at, updated_at`

func scanCommissionWithdrawal(row interface{ Scan(...any) error }) (CommissionWithdrawal, error) {
	var item CommissionWithdrawal
	var createdAt, updatedAt int64
	err := row.Scan(&item.ID, &item.UserID, &item.TicketID, &item.Amount, &item.Status, &item.Method, &item.Account, &item.PaymentReference, &createdAt, &updatedAt)
	item.CreatedAt, item.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return item, err
}

func loadTicketWithdrawal(ctx context.Context, database commissionSummaryQueryer, ticket *Ticket) error {
	item, err := scanCommissionWithdrawal(database.QueryRowContext(ctx, `SELECT `+withdrawalColumns+` FROM commission_withdrawals WHERE ticket_id = ? AND user_id = ?`, ticket.ID, ticket.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read ticket withdrawal: %w", err)
	}
	ticket.Withdrawal = &item
	return nil
}

func validWithdrawalRequestKey(value string) bool {
	if value == "" {
		return true // Old clients did not send a request key; deduplicate their active request.
	}
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

func findCommissionWithdrawalReplay(ctx context.Context, tx *sql.Tx, userID int64, input CommissionWithdrawalInput) (*Ticket, error) {
	where := `user_id = ? AND status IN ('pending','approved')`
	args := []any{userID}
	if input.RequestKey != "" {
		where = `user_id = ? AND (request_key = ? OR status IN ('pending','approved'))`
		args = append(args, input.RequestKey)
	}
	var ticketID int64
	var method, account string
	var key sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT ticket_id, method, account, request_key FROM commission_withdrawals WHERE `+where+` ORDER BY (request_key = ?) DESC LIMIT 1`, append(args, input.RequestKey)...).
		Scan(&ticketID, &method, &account, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read withdrawal request: %w", err)
	}
	if method != input.Method || account != input.Account || input.RequestKey != "" && key.String != input.RequestKey {
		return nil, ErrCommissionWithdrawalReplay
	}
	ticket, err := getTicketTx(ctx, tx, ticketID, false)
	if err != nil {
		return nil, err
	}
	return &ticket, nil
}

func freezeCommissionWithdrawalTx(ctx context.Context, tx *sql.Tx, userID int64, input CommissionWithdrawalInput, ticket *Ticket, amount int64, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE users SET commission_balance = commission_balance - ?, updated_at = ? WHERE id = ? AND commission_balance >= ? AND account_kind = 'human' AND banned = 0`, amount, now.Unix(), userID, amount)
	if err != nil {
		return fmt.Errorf("freeze commission withdrawal: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count frozen withdrawal funds: %w", err)
	}
	if count != 1 {
		return ErrInsufficientCommission
	}
	var requestKey any
	if input.RequestKey != "" {
		requestKey = input.RequestKey
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO commission_withdrawals (user_id, ticket_id, amount, status, request_key, method, account, created_at, updated_at) VALUES (?, ?, ?, 'pending', ?, ?, ?, ?, ?)`, userID, ticket.ID, amount, requestKey, input.Method, input.Account, now.Unix(), now.Unix())
	if err != nil {
		return fmt.Errorf("record commission withdrawal: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read withdrawal id: %w", err)
	}
	if err := insertWithdrawalEvent(ctx, tx, id, userID, "pending", -amount, amount, 0, now); err != nil {
		return err
	}
	return loadTicketWithdrawal(ctx, tx, ticket)
}

func insertWithdrawalEvent(ctx context.Context, tx *sql.Tx, withdrawalID, actorID int64, status string, available, frozen, paid int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO commission_withdrawal_events (withdrawal_id, actor_id, status, available_delta, frozen_delta, paid_delta, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, withdrawalID, actorID, status, available, frozen, paid, now.Unix())
	if err != nil {
		return fmt.Errorf("audit commission withdrawal: %w", err)
	}
	return nil
}

// TransitionCommissionWithdrawal records a human administrator's settlement
// decision. Marking paid is a confirmation with a receipt, never a payment call.
func (s *Store) TransitionCommissionWithdrawal(ctx context.Context, adminID, ticketID int64, input CommissionWithdrawalTransitionInput, now time.Time) (Ticket, error) {
	input.PaymentReference = strings.TrimSpace(input.PaymentReference)
	if adminID < 1 || ticketID < 1 || now.Unix() < 0 ||
		(input.Status != "approved" && input.Status != "paid" && input.Status != "rejected") ||
		(input.Status == "paid" && !validWithdrawalField(input.PaymentReference, 128)) ||
		(input.Status != "paid" && input.PaymentReference != "") {
		return Ticket{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, fmt.Errorf("begin withdrawal transition: %w", err)
	}
	defer tx.Rollback()
	var administratorEmail string
	if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ? AND account_kind = 'human' AND is_admin = 1 AND banned = 0`, adminID).Scan(&administratorEmail); errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	} else if err != nil {
		return Ticket{}, fmt.Errorf("read withdrawal administrator: %w", err)
	}
	item, err := scanCommissionWithdrawal(tx.QueryRowContext(ctx, `SELECT `+withdrawalColumns+` FROM commission_withdrawals WHERE ticket_id = ?`, ticketID))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	} else if err != nil {
		return Ticket{}, fmt.Errorf("read withdrawal transition: %w", err)
	}
	if input.Status == item.Status || input.Status == "approved" && item.Status == "paid" {
		if input.Status == "paid" {
			var hash string
			if err := tx.QueryRowContext(ctx, `SELECT payment_reference_hash FROM commission_withdrawals WHERE id = ?`, item.ID).Scan(&hash); err != nil {
				return Ticket{}, fmt.Errorf("read withdrawal receipt digest: %w", err)
			}
			if withdrawalReceiptHash(input.PaymentReference) != hash {
				return Ticket{}, ErrCommissionWithdrawalReplay
			}
		}
		return getTicketTx(ctx, tx, ticketID, true)
	}
	if now.Before(item.UpdatedAt) || !(item.Status == "pending" && (input.Status == "approved" || input.Status == "rejected") || item.Status == "approved" && (input.Status == "paid" || input.Status == "rejected")) {
		return Ticket{}, ErrCommissionWithdrawalState
	}
	var available, frozen, paid int64
	var receiptHash string
	if input.Status == "rejected" {
		result, err := tx.ExecContext(ctx, `UPDATE users SET commission_balance = commission_balance + ?, updated_at = ? WHERE id = ? AND commission_balance <= ?`, item.Amount, now.Unix(), item.UserID, maxOrderMoneyCents-item.Amount)
		if err != nil {
			return Ticket{}, fmt.Errorf("release withdrawal funds: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return Ticket{}, fmt.Errorf("count released withdrawal funds: %w", err)
		}
		if count != 1 {
			return Ticket{}, ErrCommissionWithdrawalState
		}
		available, frozen = item.Amount, -item.Amount
	}
	if input.Status == "paid" {
		receiptHash = withdrawalReceiptHash(input.PaymentReference)
		var reused bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commission_withdrawals WHERE method = ? AND payment_reference_hash = ? AND status = 'paid')`, item.Method, receiptHash).Scan(&reused); err != nil {
			return Ticket{}, fmt.Errorf("check withdrawal receipt: %w", err)
		}
		if reused {
			return Ticket{}, ErrCommissionWithdrawalReplay
		}
		frozen, paid = -item.Amount, item.Amount
	}
	if _, err := tx.ExecContext(ctx, `UPDATE commission_withdrawals SET status = ?, payment_reference = ?, payment_reference_hash = ?, updated_at = ? WHERE id = ? AND status = ?`, input.Status, input.PaymentReference, receiptHash, now.Unix(), item.ID, item.Status); err != nil {
		return Ticket{}, fmt.Errorf("transition withdrawal: %w", err)
	}
	if err := insertWithdrawalEvent(ctx, tx, item.ID, adminID, input.Status, available, frozen, paid, now); err != nil {
		return Ticket{}, err
	}
	if err := insertAdminAudit(ctx, tx, AdminAuditInput{AdministratorID: adminID, AdministratorEmail: administratorEmail, Method: "POST", Route: "/api/v1/admin/tickets/{ticketID}/withdrawal", StatusCode: 200}, now); err != nil {
		return Ticket{}, err
	}
	// Financial decisions remain accessible even if the support ticket was closed.
	ticket, err := getTicketTx(ctx, tx, ticketID, true)
	if err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, fmt.Errorf("commit withdrawal transition: %w", err)
	}
	return ticket, nil
}

func withdrawalReceiptHash(reference string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(reference)))
}

func anonymizeCommissionWithdrawalsTx(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE commission_withdrawals SET account = '[anonymized]',
	 payment_reference = CASE WHEN status = 'paid' THEN '[anonymized]' ELSE '' END, anonymized_at = ?
	 WHERE user_id = ? AND status IN ('paid','rejected') AND anonymized_at IS NULL`, now.Unix(), userID)
	if err != nil {
		return fmt.Errorf("anonymize settled withdrawal accounts: %w", err)
	}
	return nil
}
