package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	userDeletionRecoveryWindow = 30 * 24 * time.Hour
	maxUserAnonymizationBatch  = 1_000
)

func (s *Store) GetAdminUserDeletionImpact(ctx context.Context, administratorID, userID int64) (AdminUserDeletionImpact, error) {
	if administratorID < 1 || userID < 1 {
		return AdminUserDeletionImpact{}, ErrInvalidInput
	}
	if err := requireActiveAdministrator(ctx, s.db, administratorID); err != nil {
		return AdminUserDeletionImpact{}, err
	}
	if administratorID == userID {
		return AdminUserDeletionImpact{}, ErrUserDeletionSelf
	}
	return getAdminUserDeletionImpact(ctx, s.db, userID)
}

func getAdminUserDeletionImpact(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) (AdminUserDeletionImpact, error) {
	impact := AdminUserDeletionImpact{UserID: userID, Allowed: true, Blockers: make([]string, 0)}
	var accountKind string
	var isAdmin bool
	var balance, commissionBalance, frozenCommissionBalance int64
	if err := database.QueryRowContext(ctx, `SELECT admin_revision,lifecycle_status,account_kind,is_admin,balance,commission_balance,frozen_commission_balance FROM users WHERE id=?`, userID).
		Scan(&impact.Revision, &impact.LifecycleStatus, &accountKind, &isAdmin, &balance, &commissionBalance, &frozenCommissionBalance); errors.Is(err, sql.ErrNoRows) {
		return AdminUserDeletionImpact{}, ErrNotFound
	} else if err != nil {
		return AdminUserDeletionImpact{}, fmt.Errorf("read deletion target: %w", err)
	}
	if accountKind != AccountKindHuman {
		impact.Blockers = append(impact.Blockers, "internal_account")
	}
	if impact.LifecycleStatus != UserLifecycleActive {
		impact.Blockers = append(impact.Blockers, "lifecycle_not_active")
	}
	if balance != 0 || commissionBalance != 0 || frozenCommissionBalance != 0 {
		impact.Blockers = append(impact.Blockers, "unsettled_financial_balance")
	}
	if err := database.QueryRowContext(ctx, `
		SELECT
		 (SELECT COUNT(*) FROM orders WHERE user_id=?),
		 (SELECT COUNT(*) FROM payment_checkout_attempts a JOIN orders o ON o.id=a.order_id WHERE o.user_id=?),
		 (SELECT COUNT(*) FROM commission_withdrawals WHERE user_id=?),
		 (SELECT COUNT(*) FROM commission_logs WHERE invite_user_id=? OR user_id=?),
		 (SELECT COUNT(*) FROM commission_transfer_events WHERE user_id=?),
		 (SELECT COUNT(*) FROM admin_balance_adjustment_events WHERE actor_user_id=? OR target_user_id=?),
		 (SELECT COUNT(*) FROM distributor_subscriptions WHERE distributor_user_id=? OR subscriber_user_id=?),
		 (SELECT COUNT(*) FROM invitation_codes WHERE user_id=?),
		 (SELECT COUNT(*) FROM users WHERE invite_user_id=?),
		 (SELECT COUNT(*) FROM tickets WHERE user_id=?),
		 (SELECT COUNT(*) FROM ticket_messages WHERE user_id=?),
		 (SELECT COUNT(*) FROM knowledge_attachments WHERE uploader_user_id=?),
		 (SELECT COUNT(*) FROM admin_audit_logs WHERE administrator_id=?)
	`, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID, userID).Scan(
		&impact.Orders, &impact.PaymentCheckouts, &impact.CommissionWithdrawals, &impact.CommissionLogs,
		&impact.CommissionTransfers, &impact.AdminBalanceAdjustments, &impact.DistributorSubscriptions,
		&impact.InvitationCodes, &impact.InvitedUsers, &impact.Tickets, &impact.TicketMessages,
		&impact.KnowledgeAttachments, &impact.AuditLogs,
	); err != nil {
		return AdminUserDeletionImpact{}, fmt.Errorf("count user deletion impact: %w", err)
	}
	var activeOrders, unsettledCommissions, activeDistributorResponsibilities int64
	if err := database.QueryRowContext(ctx, `
		SELECT
		 (SELECT COUNT(*) FROM orders WHERE user_id=? AND status IN (0,1)),
		 (SELECT COUNT(*) FROM orders WHERE invite_user_id=? AND status IN (0,1,3) AND commission_status IN (0,1) AND commission_balance>0),
		 (SELECT COUNT(*) FROM distributor_subscriptions WHERE distributor_user_id=? AND closed_at IS NULL)
	`, userID, userID, userID).Scan(&activeOrders, &unsettledCommissions, &activeDistributorResponsibilities); err != nil {
		return AdminUserDeletionImpact{}, fmt.Errorf("count active user responsibilities: %w", err)
	}
	if activeOrders > 0 {
		impact.Blockers = append(impact.Blockers, "active_order")
	}
	if unsettledCommissions > 0 {
		impact.Blockers = append(impact.Blockers, "unsettled_commission")
	}
	if activeDistributorResponsibilities > 0 {
		impact.Blockers = append(impact.Blockers, "active_distributor_responsibility")
	}
	if isAdmin {
		var remaining int64
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id<>? AND account_kind='human' AND is_admin=1 AND banned=0 AND lifecycle_status='active'`, userID).Scan(&remaining); err != nil {
			return AdminUserDeletionImpact{}, fmt.Errorf("count remaining administrators: %w", err)
		}
		if remaining == 0 {
			impact.Blockers = append(impact.Blockers, "last_administrator")
		}
	}
	impact.Allowed = len(impact.Blockers) == 0
	return impact, nil
}

func (s *Store) RequestAdminUserDeletion(ctx context.Context, administratorID, userID, revision int64, now time.Time) (AdminUser, error) {
	if administratorID < 1 || userID < 1 || revision < 1 || now.Unix() < 0 {
		return AdminUser{}, ErrInvalidInput
	}
	if administratorID == userID {
		return AdminUser{}, ErrUserDeletionSelf
	}
	newToken, err := newSubscriptionToken()
	if err != nil {
		return AdminUser{}, err
	}
	newUUID, err := uuid.NewRandom()
	if err != nil {
		return AdminUser{}, fmt.Errorf("generate revoked user uuid: %w", err)
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, fmt.Errorf("begin user deletion request: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveAdministrator(ctx, tx, administratorID); err != nil {
		return AdminUser{}, err
	}
	impact, err := getAdminUserDeletionImpact(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	if impact.Revision != revision {
		return AdminUser{}, ErrRevisionConflict
	}
	if !impact.Allowed {
		return AdminUser{}, ErrUserDeletionBlocked
	}
	var email string
	var banned bool
	if err := tx.QueryRowContext(ctx, `SELECT email,banned FROM users WHERE id=?`, userID).Scan(&email, &banned); err != nil {
		return AdminUser{}, fmt.Errorf("read user deletion credentials: %w", err)
	}
	due := now.Add(userDeletionRecoveryWindow)
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET lifecycle_status='pending_deletion',deletion_requested_at=?,deletion_due_at=?,
			deletion_banned_snapshot=?,anonymized_at=NULL,banned=1,password_hash=?,uuid=?,subscription_token=?,
			telegram_id=NULL,remind_expire=0,remind_traffic=0,online_count=0,admin_revision=admin_revision+1,updated_at=?
		WHERE id=? AND account_kind='human' AND lifecycle_status='active' AND admin_revision=?
	`, now.Unix(), due.Unix(), banned, "!pending-deletion:"+newUUID.String(), newUUID.String(), newToken, now.Unix(), userID, revision)
	if err != nil {
		return AdminUser{}, fmt.Errorf("request user deletion: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return AdminUser{}, ErrRevisionConflict
	}
	if err := revokeUserCredentialsForDeletion(ctx, tx, userID, email, now); err != nil {
		return AdminUser{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_lifecycle_events (user_id,actor_user_id,kind,from_status,to_status,revision,created_at) VALUES (?,?,'deletion_requested','active','pending_deletion',?,?)`, userID, administratorID, revision+1, now.Unix()); err != nil {
		return AdminUser{}, fmt.Errorf("record user deletion request: %w", err)
	}
	updated, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, fmt.Errorf("commit user deletion request: %w", err)
	}
	return updated, nil
}

func (s *Store) RestoreAdminUser(ctx context.Context, administratorID, userID, revision int64, now time.Time) (AdminUser, error) {
	if administratorID < 1 || userID < 1 || administratorID == userID || revision < 1 || now.Unix() < 0 {
		return AdminUser{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, fmt.Errorf("begin user restore: %w", err)
	}
	defer tx.Rollback()
	if err := requireActiveAdministrator(ctx, tx, administratorID); err != nil {
		return AdminUser{}, err
	}
	var status string
	var due sql.NullInt64
	var previousBanned sql.NullBool
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT lifecycle_status,deletion_due_at,deletion_banned_snapshot,admin_revision FROM users WHERE id=? AND account_kind='human'`, userID).
		Scan(&status, &due, &previousBanned, &currentRevision); errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrNotFound
	} else if err != nil {
		return AdminUser{}, fmt.Errorf("read user restore state: %w", err)
	}
	if currentRevision != revision {
		return AdminUser{}, ErrRevisionConflict
	}
	if status != UserLifecyclePendingDeletion || !due.Valid || !previousBanned.Valid || now.Unix() >= due.Int64 {
		return AdminUser{}, ErrUserDeletionState
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET lifecycle_status='active',deletion_requested_at=NULL,deletion_due_at=NULL,
			deletion_banned_snapshot=NULL,anonymized_at=NULL,banned=?,admin_revision=admin_revision+1,updated_at=?
		WHERE id=? AND lifecycle_status='pending_deletion' AND admin_revision=? AND deletion_due_at>?
	`, previousBanned.Bool, now.Unix(), userID, revision, now.Unix())
	if err != nil {
		return AdminUser{}, fmt.Errorf("restore user: %w", err)
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return AdminUser{}, ErrUserDeletionState
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_lifecycle_events (user_id,actor_user_id,kind,from_status,to_status,revision,created_at) VALUES (?,?,'restored','pending_deletion','active',?,?)`, userID, administratorID, revision+1, now.Unix()); err != nil {
		return AdminUser{}, fmt.Errorf("record user restoration: %w", err)
	}
	updated, err := getAdminUserTx(ctx, tx, userID)
	if err != nil {
		return AdminUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return AdminUser{}, fmt.Errorf("commit user restoration: %w", err)
	}
	return updated, nil
}

func (s *Store) ProcessDueUserAnonymizations(ctx context.Context, now time.Time, limit int) (UserAnonymizationResult, error) {
	if now.Unix() < 0 || limit < 1 || limit > maxUserAnonymizationBatch {
		return UserAnonymizationResult{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserAnonymizationResult{}, fmt.Errorf("begin due user anonymization: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,admin_revision,email FROM users WHERE lifecycle_status='pending_deletion' AND deletion_due_at<=? ORDER BY deletion_due_at,id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return UserAnonymizationResult{}, fmt.Errorf("list due user anonymizations: %w", err)
	}
	type dueUser struct {
		id, revision int64
		email        string
	}
	due := make([]dueUser, 0, limit)
	for rows.Next() {
		var item dueUser
		if err := rows.Scan(&item.id, &item.revision, &item.email); err != nil {
			_ = rows.Close()
			return UserAnonymizationResult{}, err
		}
		due = append(due, item)
	}
	if err := rows.Close(); err != nil {
		return UserAnonymizationResult{}, err
	}
	var result UserAnonymizationResult
	for _, item := range due {
		newToken, tokenErr := newSubscriptionToken()
		if tokenErr != nil {
			return UserAnonymizationResult{}, tokenErr
		}
		newUUID, tombstone, identityErr := newAnonymizedUserIdentity(ctx, tx)
		if identityErr != nil {
			return UserAnonymizationResult{}, identityErr
		}
		updated, updateErr := tx.ExecContext(ctx, `
			UPDATE users SET email=?,password_hash=?,uuid=?,subscription_token=?,is_admin=0,is_staff=0,is_distributor=0,
				distributor_name=NULL,banned=1,telegram_id=NULL,remarks=NULL,remind_expire=0,remind_traffic=0,
				lifecycle_status='anonymized',deletion_banned_snapshot=NULL,anonymized_at=?,admin_revision=admin_revision+1,updated_at=?
			WHERE id=? AND lifecycle_status='pending_deletion' AND deletion_due_at<=? AND admin_revision=?
		`, tombstone, "!anonymized:"+newUUID.String(), newUUID.String(), newToken, now.Unix(), now.Unix(), item.id, now.Unix(), item.revision)
		if updateErr != nil {
			return UserAnonymizationResult{}, fmt.Errorf("anonymize user %d: %w", item.id, updateErr)
		}
		count, countErr := updated.RowsAffected()
		if countErr != nil {
			return UserAnonymizationResult{}, countErr
		}
		if count == 0 {
			continue
		}
		if err := anonymizeUserDisplaySnapshots(ctx, tx, item.id, item.email, tombstone, now); err != nil {
			return UserAnonymizationResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_lifecycle_events (user_id,actor_user_id,kind,from_status,to_status,revision,created_at) VALUES (?,NULL,'anonymized','pending_deletion','anonymized',?,?)`, item.id, item.revision+1, now.Unix()); err != nil {
			return UserAnonymizationResult{}, fmt.Errorf("record user anonymization: %w", err)
		}
		result.Processed++
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE lifecycle_status='pending_deletion' AND deletion_due_at<=?`, now.Unix()).Scan(&result.Remaining); err != nil {
		return UserAnonymizationResult{}, fmt.Errorf("count due user anonymizations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return UserAnonymizationResult{}, fmt.Errorf("commit due user anonymization: %w", err)
	}
	return result, nil
}

func newAnonymizedUserIdentity(ctx context.Context, tx *sql.Tx) (uuid.UUID, string, error) {
	for range 16 {
		candidate, err := uuid.NewRandom()
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("generate anonymized user uuid: %w", err)
		}
		tombstone := "deleted+" + candidate.String() + "@invalid.invalid"
		var occupied bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE uuid=? OR email=? COLLATE NOCASE)`, candidate.String(), tombstone).Scan(&occupied); err != nil {
			return uuid.Nil, "", fmt.Errorf("check anonymized user identity: %w", err)
		}
		if !occupied {
			return candidate, tombstone, nil
		}
	}
	return uuid.Nil, "", errors.New("generate unique anonymized user identity: collision limit reached")
}

func revokeUserCredentialsForDeletion(ctx context.Context, tx *sql.Tx, userID int64, email string, now time.Time) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`UPDATE admin_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE user_id=? AND revoked_at IS NULL`, []any{now.Unix(), userID}},
		{`UPDATE access_tokens SET revoked_at=COALESCE(revoked_at,?),updated_at=? WHERE user_id=? AND revoked_at IS NULL`, []any{now.Unix(), now.Unix(), userID}},
		{`DELETE FROM login_link_tokens WHERE user_id=?`, []any{userID}},
		{`DELETE FROM password_reset_challenges WHERE user_id=?`, []any{userID}},
		{`DELETE FROM registration_email_challenges WHERE email_digest IN (SELECT email_digest FROM registration_email_mail_outbox WHERE recipient=? COLLATE NOCASE)`, []any{email}},
		{`DELETE FROM node_device_ips WHERE user_id=?`, []any{userID}},
		{`DELETE FROM node_user_online WHERE user_id=?`, []any{userID}},
		{`UPDATE login_link_mail_outbox SET cancelled_at=COALESCE(cancelled_at,?),token_cipher=NULL,claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{now.Unix(), now.Unix(), userID}},
		{`UPDATE subscription_reminder_outbox SET cancelled_at=COALESCE(cancelled_at,?),claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{now.Unix(), now.Unix(), userID}},
		{`UPDATE telegram_message_outbox SET cancelled_at=COALESCE(cancelled_at,?),claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE recipient_user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{now.Unix(), now.Unix(), userID}},
		{`UPDATE ticket_mail_outbox SET failed_at=COALESCE(failed_at,?),claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE recipient=? COLLATE NOCASE AND sent_at IS NULL AND failed_at IS NULL`, []any{now.Unix(), now.Unix(), email}},
		{`UPDATE invitation_codes SET consumed_at=COALESCE(consumed_at,?),updated_at=? WHERE user_id=? AND consumed_at IS NULL`, []any{now.Unix(), now.Unix(), userID}},
		{`UPDATE password_reset_mail_outbox SET cancelled_at=COALESCE(cancelled_at,?),code_cipher=NULL,claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE recipient=? COLLATE NOCASE AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{now.Unix(), now.Unix(), email}},
		{`UPDATE registration_email_mail_outbox SET cancelled_at=COALESCE(cancelled_at,?),code_cipher=NULL,claim_token=NULL,claimed_at=NULL,last_error='cancelled because user deletion was requested',updated_at=? WHERE recipient=? COLLATE NOCASE AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{now.Unix(), now.Unix(), email}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("revoke user deletion credential: %w", err)
		}
	}
	return cancelUserBulkMailTargetsForDeletion(ctx, tx, userID, now)
}

func cancelUserBulkMailTargetsForDeletion(ctx context.Context, tx *sql.Tx, userID int64, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT t.job_id FROM admin_user_bulk_targets t JOIN admin_user_bulk_jobs j ON j.id=t.job_id
		WHERE t.user_id=? AND j.kind='mail' AND t.status IN ('pending','processing')
	`, userID)
	if err != nil {
		return fmt.Errorf("list user deletion bulk mail targets: %w", err)
	}
	jobIDs := make([]string, 0)
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			_ = rows.Close()
			return err
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE admin_user_bulk_targets SET status='cancelled',claim_token=NULL,claimed_at=NULL,
			last_error='cancelled because user deletion was requested',processed_at=?
		WHERE user_id=? AND status IN ('pending','processing')
		  AND job_id IN (SELECT id FROM admin_user_bulk_jobs WHERE kind='mail')
	`, now.Unix(), userID); err != nil {
		return fmt.Errorf("cancel user deletion bulk mail targets: %w", err)
	}
	for _, jobID := range jobIDs {
		if err := refreshAdminUserBulkJobTx(ctx, tx, jobID, now); err != nil {
			return err
		}
	}
	return nil
}

func anonymizeUserDisplaySnapshots(ctx context.Context, tx *sql.Tx, userID int64, originalEmail, tombstone string, now time.Time) error {
	statements := []struct {
		query string
		args  []any
	}{
		{`UPDATE admin_audit_logs SET administrator_email=? WHERE administrator_id=?`, []any{tombstone, userID}},
		{`UPDATE traffic_reset_logs SET administrator_email=? WHERE administrator_id=?`, []any{tombstone, userID}},
		{`UPDATE admin_user_bulk_jobs SET administrator_email=?,updated_at=? WHERE administrator_id=?`, []any{tombstone, now.Unix(), userID}},
		{`UPDATE admin_user_bulk_targets SET email=?,uuid='',subscription_token='anonymized' WHERE user_id=?`, []any{tombstone, userID}},
		{`UPDATE ticket_mail_outbox SET recipient=?,updated_at=? WHERE recipient=? COLLATE NOCASE`, []any{tombstone, now.Unix(), originalEmail}},
		{`UPDATE password_reset_mail_outbox SET recipient=?,updated_at=? WHERE recipient=? COLLATE NOCASE`, []any{tombstone, now.Unix(), originalEmail}},
		{`UPDATE registration_email_mail_outbox SET recipient=?,updated_at=? WHERE recipient=? COLLATE NOCASE`, []any{tombstone, now.Unix(), originalEmail}},
		{`UPDATE login_link_mail_outbox SET recipient=?,updated_at=? WHERE user_id=?`, []any{tombstone, now.Unix(), userID}},
		{`UPDATE subscription_reminder_outbox SET recipient=?,updated_at=? WHERE user_id=?`, []any{tombstone, now.Unix(), userID}},
		{`UPDATE commission_withdrawals SET account_cipher=zeroblob(1),account_fingerprint=zeroblob(32),account_masked='[anonymized]' WHERE user_id=?`, []any{userID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("anonymize user display snapshot: %w", err)
		}
	}
	return nil
}

func requireActiveAdministrator(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, administratorID int64) error {
	var exists bool
	if err := database.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users
			WHERE id=? AND account_kind='human' AND lifecycle_status='active' AND is_admin=1 AND banned=0
		)
	`, administratorID).Scan(&exists); err != nil {
		return fmt.Errorf("read active administrator: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
