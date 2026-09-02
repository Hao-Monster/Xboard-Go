package store

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxCommissionWithdrawalPage = 1_000_000

const commissionWithdrawalSelect = `
	SELECT w.id, w.user_id, u.email, w.amount, w.fee_basis_points, w.fee_amount, w.net_amount, w.currency, w.method, w.account_masked,
	       w.status, w.revision, w.external_reference, w.rejection_reason,
	       w.created_at, w.updated_at, w.approved_at, w.paid_at, w.rejected_at
	FROM commission_withdrawals w JOIN users u ON u.id=w.user_id`

func (s *Store) CreateCommissionWithdrawal(ctx context.Context, userID int64, input CreateCommissionWithdrawalInput, now time.Time) (CommissionWithdrawal, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Method = strings.TrimSpace(input.Method)
	input.AccountMasked = strings.TrimSpace(input.AccountMasked)
	if userID < 1 || now.Unix() < 0 || !validWithdrawalIdempotencyKey(input.IdempotencyKey) ||
		!validWithdrawalText(input.Method, 1, 128) || !validWithdrawalText(input.AccountMasked, 1, 320) ||
		len(input.AccountCipher) < 1 || len(input.AccountCipher) > 8192 || len(input.AccountFingerprint) != 32 {
		return CommissionWithdrawal{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("begin commission withdrawal: %w", err)
	}
	defer tx.Rollback()
	existing, err := getCommissionWithdrawalByIdempotency(ctx, tx, userID, input.IdempotencyKey)
	if err == nil {
		var fingerprint []byte
		if err := tx.QueryRowContext(ctx, `SELECT account_fingerprint FROM commission_withdrawals WHERE id=?`, existing.ID).Scan(&fingerprint); err != nil {
			return CommissionWithdrawal{}, fmt.Errorf("read commission withdrawal fingerprint: %w", err)
		}
		if existing.Method != input.Method || subtle.ConstantTimeCompare(fingerprint, input.AccountFingerprint) != 1 {
			return CommissionWithdrawal{}, ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return CommissionWithdrawal{}, err
	}
	var currency, methodsJSON, accountKind, lifecycle string
	var limit, amount, frozen int64
	var banned bool
	if err := tx.QueryRowContext(ctx, `
		SELECT s.currency, s.commission_withdraw_limit, s.commission_withdraw_method,
		       u.account_kind, u.lifecycle_status, u.banned, u.commission_balance, u.frozen_commission_balance
		FROM users u CROSS JOIN app_settings s WHERE u.id=? AND s.id=1
	`, userID).Scan(&currency, &limit, &methodsJSON, &accountKind, &lifecycle, &banned, &amount, &frozen); errors.Is(err, sql.ErrNoRows) {
		return CommissionWithdrawal{}, ErrNotFound
	} else if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("read commission withdrawal policy: %w", err)
	}
	if accountKind != AccountKindHuman || lifecycle != UserLifecycleActive || banned {
		return CommissionWithdrawal{}, ErrConflict
	}
	var methods []string
	if err := json.Unmarshal([]byte(methodsJSON), &methods); err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("decode commission withdrawal methods: %w", err)
	}
	allowed := false
	for _, method := range methods {
		allowed = allowed || method == input.Method
	}
	if !allowed {
		return CommissionWithdrawal{}, ErrCommissionWithdrawalMethod
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commission_withdrawals WHERE user_id=? AND status IN ('pending','approved'))`, userID).Scan(&active); err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("check active commission withdrawal: %w", err)
	}
	if active {
		return CommissionWithdrawal{}, ErrCommissionWithdrawalActive
	}
	if amount < 1 {
		return CommissionWithdrawal{}, ErrInsufficientCommission
	}
	if amount < limit {
		return CommissionWithdrawal{}, ErrCommissionWithdrawalLimit
	}
	if frozen > maxOrderMoneyCents-amount {
		return CommissionWithdrawal{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO commission_withdrawals (user_id,idempotency_key,amount,fee_basis_points,fee_amount,net_amount,currency,method,account_cipher,account_fingerprint,account_masked,status,revision,created_at,updated_at)
		VALUES (?,?,?,0,0,?,?,?,?,?,?,'pending',1,?,?)
	`, userID, input.IdempotencyKey, amount, amount, currency, input.Method, input.AccountCipher, input.AccountFingerprint, input.AccountMasked, now.Unix(), now.Unix())
	if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("create commission withdrawal: %w", err)
	}
	withdrawalID, err := result.LastInsertId()
	if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("read commission withdrawal ID: %w", err)
	}
	updated, err := tx.ExecContext(ctx, `
		UPDATE users SET commission_balance=commission_balance-?, frozen_commission_balance=frozen_commission_balance+?, updated_at=?
		WHERE id=? AND commission_balance=? AND frozen_commission_balance<=?
	`, amount, amount, now.Unix(), userID, amount, maxOrderMoneyCents-amount)
	if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("freeze commission withdrawal: %w", err)
	}
	if count, countErr := updated.RowsAffected(); countErr != nil || count != 1 {
		return CommissionWithdrawal{}, ErrConflict
	}
	if err := insertCommissionWithdrawalEvent(ctx, tx, withdrawalID, &userID, "created", "", CommissionWithdrawalPending, amount, currency, 1, now); err != nil {
		return CommissionWithdrawal{}, err
	}
	created, err := getCommissionWithdrawal(ctx, tx, withdrawalID)
	if err != nil {
		return CommissionWithdrawal{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("commit commission withdrawal: %w", err)
	}
	return created, nil
}

func (s *Store) ApproveCommissionWithdrawal(ctx context.Context, administratorID, withdrawalID, revision int64, now time.Time) (CommissionWithdrawal, error) {
	return s.transitionCommissionWithdrawal(ctx, administratorID, withdrawalID, revision, CommissionWithdrawalApproved, "", "", now)
}

func (s *Store) RejectCommissionWithdrawal(ctx context.Context, administratorID, withdrawalID, revision int64, reason string, now time.Time) (CommissionWithdrawal, error) {
	reason = strings.TrimSpace(reason)
	if !validWithdrawalText(reason, 1, 500) {
		return CommissionWithdrawal{}, ErrInvalidInput
	}
	return s.transitionCommissionWithdrawal(ctx, administratorID, withdrawalID, revision, CommissionWithdrawalRejected, "", reason, now)
}

func (s *Store) PayCommissionWithdrawal(ctx context.Context, administratorID, withdrawalID, revision int64, externalReference string, now time.Time) (CommissionWithdrawal, error) {
	externalReference = strings.TrimSpace(externalReference)
	if !validWithdrawalText(externalReference, 1, 128) {
		return CommissionWithdrawal{}, ErrInvalidInput
	}
	return s.transitionCommissionWithdrawal(ctx, administratorID, withdrawalID, revision, CommissionWithdrawalPaid, externalReference, "", now)
}

func (s *Store) transitionCommissionWithdrawal(ctx context.Context, administratorID, withdrawalID, revision int64, target, externalReference, reason string, now time.Time) (CommissionWithdrawal, error) {
	if administratorID < 1 || withdrawalID < 1 || revision < 1 || now.Unix() < 0 {
		return CommissionWithdrawal{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("begin commission withdrawal transition: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveAdministrator(ctx, tx, administratorID); err != nil {
		return CommissionWithdrawal{}, err
	}
	current, err := getCommissionWithdrawal(ctx, tx, withdrawalID)
	if err != nil {
		return CommissionWithdrawal{}, err
	}
	if current.Revision != revision {
		return CommissionWithdrawal{}, ErrRevisionConflict
	}
	allowed := target == CommissionWithdrawalApproved && current.Status == CommissionWithdrawalPending ||
		target == CommissionWithdrawalPaid && current.Status == CommissionWithdrawalApproved ||
		target == CommissionWithdrawalRejected && (current.Status == CommissionWithdrawalPending || current.Status == CommissionWithdrawalApproved)
	if !allowed {
		return CommissionWithdrawal{}, ErrCommissionWithdrawalState
	}
	nextRevision := revision + 1
	var result sql.Result
	switch target {
	case CommissionWithdrawalApproved:
		result, err = tx.ExecContext(ctx, `UPDATE commission_withdrawals SET status='approved',revision=?,approved_at=?,updated_at=? WHERE id=? AND revision=? AND status='pending'`, nextRevision, now.Unix(), now.Unix(), withdrawalID, revision)
	case CommissionWithdrawalRejected:
		result, err = tx.ExecContext(ctx, `UPDATE commission_withdrawals SET status='rejected',revision=?,rejection_reason=?,rejected_at=?,updated_at=? WHERE id=? AND revision=? AND status IN ('pending','approved')`, nextRevision, reason, now.Unix(), now.Unix(), withdrawalID, revision)
	case CommissionWithdrawalPaid:
		result, err = tx.ExecContext(ctx, `UPDATE commission_withdrawals SET status='paid',revision=?,external_reference=?,paid_at=?,updated_at=? WHERE id=? AND revision=? AND status='approved'`, nextRevision, externalReference, now.Unix(), now.Unix(), withdrawalID, revision)
	}
	if err != nil {
		if target == CommissionWithdrawalPaid && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return CommissionWithdrawal{}, ErrConflict
		}
		return CommissionWithdrawal{}, fmt.Errorf("transition commission withdrawal: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return CommissionWithdrawal{}, ErrRevisionConflict
	}
	if target == CommissionWithdrawalRejected {
		result, err = tx.ExecContext(ctx, `UPDATE users SET frozen_commission_balance=frozen_commission_balance-?, commission_balance=commission_balance+?, updated_at=? WHERE id=? AND frozen_commission_balance>=? AND commission_balance<=?`, current.Amount, current.Amount, now.Unix(), current.UserID, current.Amount, maxOrderMoneyCents-current.Amount)
	} else if target == CommissionWithdrawalPaid {
		result, err = tx.ExecContext(ctx, `UPDATE users SET frozen_commission_balance=frozen_commission_balance-?, updated_at=? WHERE id=? AND frozen_commission_balance>=?`, current.Amount, now.Unix(), current.UserID, current.Amount)
	}
	if target == CommissionWithdrawalRejected || target == CommissionWithdrawalPaid {
		if err != nil {
			return CommissionWithdrawal{}, fmt.Errorf("settle commission withdrawal balance: %w", err)
		}
		if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
			return CommissionWithdrawal{}, ErrConflict
		}
	}
	kind := target
	if err := insertCommissionWithdrawalEvent(ctx, tx, withdrawalID, &administratorID, kind, current.Status, target, current.Amount, current.Currency, nextRevision, now); err != nil {
		return CommissionWithdrawal{}, err
	}
	updated, err := getCommissionWithdrawal(ctx, tx, withdrawalID)
	if err != nil {
		return CommissionWithdrawal{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommissionWithdrawal{}, fmt.Errorf("commit commission withdrawal transition: %w", err)
	}
	return updated, nil
}

func (s *Store) ListCommissionWithdrawals(ctx context.Context, userID int64, page, pageSize int) (CommissionWithdrawalPage, error) {
	return s.listCommissionWithdrawals(ctx, userID, "", page, pageSize)
}

func (s *Store) GetCommissionWithdrawalPolicy(ctx context.Context, userID int64) (CommissionWithdrawalPolicy, error) {
	if userID < 1 {
		return CommissionWithdrawalPolicy{}, ErrInvalidInput
	}
	var policy CommissionWithdrawalPolicy
	var methodsJSON, lifecycle, accountKind string
	var banned bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT s.currency,s.commission_withdraw_limit,s.commission_withdraw_method,
		       u.commission_balance,u.frozen_commission_balance,u.lifecycle_status,u.account_kind,u.banned
		FROM users u CROSS JOIN app_settings s WHERE u.id=? AND s.id=1
	`, userID).Scan(&policy.Currency, &policy.MinimumAmount, &methodsJSON, &policy.AvailableCommission,
		&policy.FrozenCommission, &lifecycle, &accountKind, &banned); errors.Is(err, sql.ErrNoRows) {
		return CommissionWithdrawalPolicy{}, ErrNotFound
	} else if err != nil {
		return CommissionWithdrawalPolicy{}, fmt.Errorf("read commission withdrawal policy: %w", err)
	}
	if lifecycle != UserLifecycleActive || accountKind != AccountKindHuman || banned {
		return CommissionWithdrawalPolicy{}, ErrConflict
	}
	if err := json.Unmarshal([]byte(methodsJSON), &policy.Methods); err != nil {
		return CommissionWithdrawalPolicy{}, fmt.Errorf("decode commission withdrawal policy: %w", err)
	}
	active, err := scanCommissionWithdrawal(s.db.QueryRowContext(ctx, commissionWithdrawalSelect+` WHERE w.user_id=? AND w.status IN ('pending','approved')`, userID))
	if err == nil {
		policy.Active = &active
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CommissionWithdrawalPolicy{}, err
	}
	return policy, nil
}

func (s *Store) GetCommissionWithdrawalAccountCipher(ctx context.Context, withdrawalID int64) ([]byte, error) {
	if withdrawalID < 1 {
		return nil, ErrInvalidInput
	}
	var ciphertext []byte
	if err := s.db.QueryRowContext(ctx, `SELECT account_cipher FROM commission_withdrawals WHERE id=?`, withdrawalID).Scan(&ciphertext); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read commission withdrawal account: %w", err)
	}
	return append([]byte(nil), ciphertext...), nil
}

func (s *Store) RecordCommissionWithdrawalAccountRevealAudit(ctx context.Context, administratorID int64, administratorEmail string, withdrawalID int64, now time.Time) error {
	if administratorID < 1 || withdrawalID < 1 || now.IsZero() {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin commission withdrawal account reveal audit: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveAdministrator(ctx, tx, administratorID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commission_withdrawals WHERE id=?)`, withdrawalID).Scan(&exists); err != nil {
		return fmt.Errorf("read commission withdrawal for reveal audit: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if err := insertAdminAudit(ctx, tx, AdminAuditInput{
		AdministratorID: administratorID, AdministratorEmail: administratorEmail,
		Method: "POST", Route: fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/account/reveal", withdrawalID), StatusCode: 200,
	}, now); err != nil {
		return fmt.Errorf("record commission withdrawal account reveal audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit commission withdrawal account reveal audit: %w", err)
	}
	return nil
}

func (s *Store) ListAdminCommissionWithdrawals(ctx context.Context, status string, page, pageSize int) (CommissionWithdrawalPage, error) {
	status = strings.TrimSpace(status)
	if status != "" && status != CommissionWithdrawalPending && status != CommissionWithdrawalApproved && status != CommissionWithdrawalPaid && status != CommissionWithdrawalRejected {
		return CommissionWithdrawalPage{}, ErrInvalidInput
	}
	return s.listCommissionWithdrawals(ctx, 0, status, page, pageSize)
}

func (s *Store) listCommissionWithdrawals(ctx context.Context, userID int64, status string, page, pageSize int) (CommissionWithdrawalPage, error) {
	if userID < 0 || page < 1 || page > maxCommissionWithdrawalPage || pageSize < 1 || pageSize > 100 {
		return CommissionWithdrawalPage{}, ErrInvalidInput
	}
	where := " WHERE 1=1"
	args := make([]any, 0, 4)
	if userID > 0 {
		where += " AND w.user_id=?"
		args = append(args, userID)
	}
	if status != "" {
		where += " AND w.status=?"
		args = append(args, status)
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commission_withdrawals w`+where, args...).Scan(&total); err != nil {
		return CommissionWithdrawalPage{}, fmt.Errorf("count commission withdrawals: %w", err)
	}
	args = append(args, pageSize, int64(page-1)*int64(pageSize))
	rows, err := s.db.QueryContext(ctx, commissionWithdrawalSelect+where+` ORDER BY w.created_at DESC,w.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return CommissionWithdrawalPage{}, fmt.Errorf("list commission withdrawals: %w", err)
	}
	defer rows.Close()
	items := make([]CommissionWithdrawal, 0, min(pageSize, int(total)))
	for rows.Next() {
		item, scanErr := scanCommissionWithdrawal(rows)
		if scanErr != nil {
			return CommissionWithdrawalPage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return CommissionWithdrawalPage{}, err
	}
	return CommissionWithdrawalPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func getCommissionWithdrawal(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, withdrawalID int64) (CommissionWithdrawal, error) {
	item, err := scanCommissionWithdrawal(database.QueryRowContext(ctx, commissionWithdrawalSelect+` WHERE w.id=?`, withdrawalID))
	if errors.Is(err, sql.ErrNoRows) {
		return CommissionWithdrawal{}, ErrNotFound
	}
	return item, err
}

func getCommissionWithdrawalByIdempotency(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64, key string) (CommissionWithdrawal, error) {
	item, err := scanCommissionWithdrawal(database.QueryRowContext(ctx, commissionWithdrawalSelect+` WHERE w.user_id=? AND w.idempotency_key=?`, userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return CommissionWithdrawal{}, ErrNotFound
	}
	return item, err
}

func scanCommissionWithdrawal(row rowScanner) (CommissionWithdrawal, error) {
	var item CommissionWithdrawal
	var external, rejection sql.NullString
	var created, updated int64
	var approved, paid, rejected sql.NullInt64
	if err := row.Scan(&item.ID, &item.UserID, &item.UserEmail, &item.Amount, &item.FeeBasisPoints, &item.FeeAmount, &item.NetAmount, &item.Currency, &item.Method, &item.AccountMasked,
		&item.Status, &item.Revision, &external, &rejection, &created, &updated, &approved, &paid, &rejected); err != nil {
		return CommissionWithdrawal{}, err
	}
	item.ExternalReference = external.String
	item.RejectionReason = rejection.String
	item.CreatedAt = time.Unix(created, 0).UTC()
	item.UpdatedAt = time.Unix(updated, 0).UTC()
	for source, target := range map[*sql.NullInt64]**time.Time{&approved: &item.ApprovedAt, &paid: &item.PaidAt, &rejected: &item.RejectedAt} {
		if source.Valid {
			value := time.Unix(source.Int64, 0).UTC()
			*target = &value
		}
	}
	return item, nil
}

func insertCommissionWithdrawalEvent(ctx context.Context, tx *sql.Tx, withdrawalID int64, actorID *int64, kind, from, to string, amount int64, currency string, revision int64, now time.Time) error {
	var fromValue any
	if from != "" {
		fromValue = from
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO commission_withdrawal_events (withdrawal_id,actor_user_id,kind,from_status,to_status,amount,currency,revision,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, withdrawalID, actorID, kind, fromValue, to, amount, currency, revision, now.Unix()); err != nil {
		return fmt.Errorf("record commission withdrawal event: %w", err)
	}
	return nil
}

func validWithdrawalText(value string, minimum, maximum int) bool {
	return utf8.ValidString(value) && len(value) >= minimum && len(value) <= maximum && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validWithdrawalIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}
