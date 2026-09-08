package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const UserRecoveryWindow = 30 * 24 * time.Hour

type UserLifecycleInput struct {
	AdministratorID int64
	UserID          int64
	Revision        int64
	Action          string
}

// ChangeUserLifecycle preserves financial facts and revokes old credentials even
// when a later restore succeeds. No scheduled job performs anonymization.
func (s *Store) ChangeUserLifecycle(ctx context.Context, input UserLifecycleInput, now time.Time) (AdminUser, AdminUserMutation, error) {
	if input.UserID < 1 || input.AdministratorID < 1 || input.Revision < 1 || now.Unix() < 0 || (input.Action != "deactivate" && input.Action != "restore" && input.Action != "anonymize") {
		return AdminUser{}, AdminUserMutation{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	defer tx.Rollback()
	var permitted bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=? AND account_kind='human' AND is_admin=1 AND banned=0)`, input.AdministratorID).Scan(&permitted); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	if !permitted || input.UserID == input.AdministratorID {
		return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: lifecycle requires another active administrator", ErrInvalidInput)
	}
	account, err := getAdminUserTx(ctx, tx, input.UserID)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	// A repeated request after a lost response returns the already-applied state.
	var repeated bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_lifecycle_events WHERE user_id=? AND administrator_id=? AND action=? AND revision=?)`, input.UserID, input.AdministratorID, input.Action, input.Revision+1).Scan(&repeated); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	if repeated && account.Revision == input.Revision+1 {
		return account, AdminUserMutation{}, nil
	}
	if account.Revision != input.Revision {
		return AdminUser{}, AdminUserMutation{}, ErrConflict
	}
	var oldUUID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT uuid FROM users WHERE id=?`, input.UserID).Scan(&oldUUID); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	at := now.UTC().Unix()
	if err := cancelUserLifecycleNotificationsTx(ctx, tx, account, at); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	switch input.Action {
	case "deactivate":
		if account.LifecycleStatus != "active" {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: user is already inactive", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_lifecycles(user_id,deactivated_at,restore_until,previous_banned) VALUES(?,?,?,?) ON CONFLICT(user_id) DO UPDATE SET deactivated_at=excluded.deactivated_at,restore_until=excluded.restore_until,previous_banned=excluded.previous_banned`, input.UserID, at, at+int64(UserRecoveryWindow/time.Second), account.Banned); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET banned=1 WHERE id=?`, input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
	case "restore":
		if account.LifecycleStatus != "deactivated" || account.RestoreUntil == nil || at < account.DeactivatedAt.Unix() || at >= account.RestoreUntil.Unix() {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: recovery window is closed", ErrConflict)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_lifecycles SET deactivated_at=NULL,restore_until=NULL WHERE user_id=?`, input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET banned=(SELECT previous_banned FROM user_lifecycles WHERE user_id=?) WHERE id=?`, input.UserID, input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
	case "anonymize":
		if account.LifecycleStatus != "deactivated" || account.RestoreUntil == nil || at < account.RestoreUntil.Unix() {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: anonymization requires a completed recovery window", ErrConflict)
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM commission_withdrawals WHERE user_id=? AND status IN ('pending','approved'))`, input.UserID).Scan(&pending); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
		if pending {
			return AdminUser{}, AdminUserMutation{}, fmt.Errorf("%w: unresolved withdrawal must be settled first", ErrConflict)
		}
		if err := anonymizeUserTx(ctx, tx, account, at); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_lifecycles SET anonymized_at=? WHERE user_id=?`, at, input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
	}
	if input.Action != "restore" {
		token, err := newSubscriptionToken()
		if err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE users SET subscription_token=?,uuid=?,online_count=0 WHERE id=?`, token, uuid.NewString(), input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
	}
	if err := revokeAllCredentialsTx(ctx, tx, input.UserID, now); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	for _, query := range []string{`DELETE FROM login_link_tokens WHERE user_id=?`, `DELETE FROM password_reset_challenges WHERE user_id=?`, `DELETE FROM node_device_ips WHERE user_id=?`, `DELETE FROM node_user_online WHERE user_id=?`} {
		if _, err = tx.ExecContext(ctx, query, input.UserID); err != nil {
			return AdminUser{}, AdminUserMutation{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET admin_revision=admin_revision+1,updated_at=?,online_count=0 WHERE id=?`, at, input.UserID); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_lifecycle_events(user_id,administrator_id,action,revision,created_at) VALUES(?,?,?,?,?)`, input.UserID, input.AdministratorID, input.Action, input.Revision+1, at); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	updated, err := getAdminUserTx(ctx, tx, input.UserID)
	if err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	if err = tx.Commit(); err != nil {
		return AdminUser{}, AdminUserMutation{}, err
	}
	return updated, AdminUserMutation{UUID: oldUUID.String, OldGroupID: account.GroupID, NewGroupID: account.GroupID, RuntimeChanged: true, AccessStateCleared: true}, nil
}

func cancelUserLifecycleNotificationsTx(ctx context.Context, tx *sql.Tx, account AdminUser, at int64) error {
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`UPDATE login_link_mail_outbox SET cancelled_at=?,token_cipher=NULL,claim_token=NULL,claimed_at=NULL WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{at, account.ID}},
		{`UPDATE password_reset_mail_outbox SET cancelled_at=?,code_cipher=NULL,claim_token=NULL,claimed_at=NULL WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{at, account.ID}},
		{`UPDATE registration_email_mail_outbox SET cancelled_at=?,code_cipher=NULL,claim_token=NULL,claimed_at=NULL WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{at, account.ID}},
		{`UPDATE subscription_reminder_outbox SET cancelled_at=?,claim_token=NULL,claimed_at=NULL WHERE user_id=? AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{at, account.ID}},
		{`UPDATE ticket_mail_outbox SET failed_at=?,last_error='account inactive',claim_token=NULL,claimed_at=NULL WHERE ticket_message_id IN (SELECT m.id FROM ticket_messages m JOIN tickets t ON t.id=m.ticket_id WHERE t.user_id=?) AND sent_at IS NULL AND failed_at IS NULL`, []any{at, account.ID}},
		{`UPDATE telegram_message_outbox SET cancelled_at=?,claim_token=NULL,claimed_at=NULL WHERE (recipient_user_id=? OR chat_id=?) AND sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL`, []any{at, account.ID, account.TelegramID}},
		{`UPDATE admin_user_bulk_targets SET status='cancelled',claim_token=NULL,claimed_at=NULL,processed_at=? WHERE user_id=? AND status IN ('pending','processing') AND job_id IN (SELECT id FROM admin_user_bulk_jobs WHERE kind='mail')`, []any{at, account.ID}},
	} {
		if _, err := tx.ExecContext(ctx, command.query, command.args...); err != nil {
			return fmt.Errorf("cancel inactive user notifications: %w", err)
		}
	}
	// A user can appear in many queued jobs. Refresh them in bounded pages while
	// retaining one transaction for cancellation and audit consistency.
	after := ""
	for {
		rows, err := tx.QueryContext(ctx, `SELECT j.id FROM admin_user_bulk_jobs j JOIN admin_user_bulk_targets t ON t.job_id=j.id WHERE t.user_id=? AND j.kind='mail' AND j.status IN ('queued','running','cancelling') AND j.id>? ORDER BY j.id LIMIT 64`, account.ID, after)
		if err != nil {
			return err
		}
		jobs := make([]string, 0, 64)
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			jobs = append(jobs, id)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}
		for _, id := range jobs {
			if err = refreshAdminUserBulkJobTx(ctx, tx, id, time.Unix(at, 0)); err != nil {
				return err
			}
		}
		after = jobs[len(jobs)-1]
	}
}

func anonymizeUserTx(ctx context.Context, tx *sql.Tx, account AdminUser, at int64) error {
	tombstone := fmt.Sprintf("anonymized-%d-%s@users.invalid", account.ID, uuid.NewString())
	// Scrub application-managed identity copies; identifiers and monetary facts
	// remain for reconciliation. Previously downloaded files/backups are outside
	// this database transaction and follow the retention policy.
	// Ticket ownership identifies historical recipients even after email reuse;
	// authorship alone must not replace somebody else's recipient. Bulk errors
	// copied from a target are scrubbed before that target loses its old error.
	for _, command := range []struct {
		query string
		args  []any
	}{
		{`UPDATE telegram_message_outbox SET cancelled_at=?,claim_token=NULL,claimed_at=NULL WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL AND ((source_kind='ticket' AND source_id IN (SELECT m.id FROM ticket_messages m JOIN tickets t ON t.id=m.ticket_id WHERE t.user_id=? OR m.user_id=?)) OR (source_kind='payment' AND source_id IN (SELECT id FROM orders WHERE user_id=?)))`, []any{at, account.ID, account.ID, account.ID}},
		{`UPDATE users SET email=?,password_hash='!',telegram_id=NULL,remarks=NULL,distributor_name=CASE WHEN is_distributor=1 THEN 'Anonymized' ELSE NULL END,remind_expire=0,remind_traffic=0,last_login_at=NULL,last_online_at=NULL WHERE id=?`, []any{tombstone, account.ID}},
		{`UPDATE ticket_messages SET message='[anonymized]' WHERE user_id=? OR ticket_id IN (SELECT id FROM tickets WHERE user_id=?)`, []any{account.ID, account.ID}},
		{`UPDATE tickets SET subject='[anonymized]' WHERE user_id=?`, []any{account.ID}},
		{`UPDATE ticket_mail_outbox SET recipient=CASE WHEN ticket_message_id IN (SELECT m.id FROM ticket_messages m JOIN tickets t ON t.id=m.ticket_id WHERE t.user_id=?) THEN ? ELSE recipient END,ticket_subject='[anonymized]',reply_message='[anonymized]',last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END,claim_token=NULL,claimed_at=NULL,failed_at=CASE WHEN sent_at IS NULL THEN COALESCE(failed_at,?) ELSE failed_at END WHERE ticket_message_id IN (SELECT m.id FROM ticket_messages m JOIN tickets t ON t.id=m.ticket_id WHERE t.user_id=? OR m.user_id=?)`, []any{account.ID, tombstone, at, account.ID, account.ID}},
		{`UPDATE login_link_mail_outbox SET recipient=?,token_cipher=NULL,last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE user_id=?`, []any{tombstone, account.ID}},
		{`UPDATE password_reset_mail_outbox SET recipient=?,code_cipher=NULL,last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE user_id=?`, []any{tombstone, account.ID}},
		{`UPDATE registration_email_mail_outbox SET recipient=?,code_cipher=NULL,last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE user_id=?`, []any{tombstone, account.ID}},
		{`UPDATE subscription_reminder_outbox SET recipient=?,last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE user_id=?`, []any{tombstone, account.ID}},
		{`UPDATE admin_audit_logs SET administrator_email=? WHERE administrator_id=?`, []any{tombstone, account.ID}},
		{`UPDATE traffic_reset_logs SET administrator_email=? WHERE administrator_id=?`, []any{tombstone, account.ID}},
		{`UPDATE traffic_reset_logs SET reason='[anonymized]' WHERE trigger_source='manual' AND (user_id=? OR administrator_id=?)`, []any{account.ID, account.ID}},
		{`UPDATE access_tokens SET name='[anonymized]' WHERE user_id=?`, []any{account.ID}},
		{`UPDATE admin_user_bulk_jobs SET administrator_email=? WHERE administrator_id=?`, []any{tombstone, account.ID}},
		{`UPDATE admin_user_bulk_jobs SET output_expires_at=0 WHERE kind='csv' AND id IN (SELECT job_id FROM admin_user_bulk_targets WHERE user_id=?)`, []any{account.ID}},
		{`UPDATE admin_user_bulk_jobs SET last_error='[anonymized]' WHERE id IN (SELECT t.job_id FROM admin_user_bulk_targets t JOIN admin_user_bulk_jobs j ON j.id=t.job_id WHERE t.user_id=? AND t.last_error=j.last_error)`, []any{account.ID}},
		{`UPDATE admin_user_bulk_targets SET email=?,uuid='',subscription_token='anonymized',last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE user_id=?`, []any{tombstone, account.ID}},
		{`UPDATE gift_card_usages SET ip_address='' WHERE user_id=?`, []any{account.ID}},
		{`UPDATE telegram_message_outbox SET text='[anonymized]',last_error=CASE WHEN last_error IS NOT NULL THEN '[anonymized]' END WHERE recipient_user_id=? OR chat_id=? OR (source_kind='ticket' AND source_id IN (SELECT m.id FROM ticket_messages m JOIN tickets t ON t.id=m.ticket_id WHERE t.user_id=? OR m.user_id=?)) OR (source_kind='payment' AND source_id IN (SELECT id FROM orders WHERE user_id=?))`, []any{account.ID, account.TelegramID, account.ID, account.ID, account.ID}},
		{`UPDATE telegram_message_outbox SET chat_id=-9223372036854775807+id WHERE recipient_user_id=? OR chat_id=?`, []any{account.ID, account.TelegramID}},
	} {
		if _, err := tx.ExecContext(ctx, command.query, command.args...); err != nil {
			return fmt.Errorf("anonymize user identity copies: %w", err)
		}
	}
	return anonymizeCommissionWithdrawalsTx(ctx, tx, account.ID, time.Unix(at, 0))
}

func userLifecycleTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	at := time.Unix(value.Int64, 0).UTC()
	return &at
}

// AdminUserBulkMailClaimActive is checked at the transport boundary because a
// lifecycle operation may cancel a message after a worker has claimed it.
func (s *Store) AdminUserBulkMailClaimActive(ctx context.Context, jobID string, sequence int64, claimToken string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM admin_user_bulk_targets t JOIN admin_user_bulk_jobs j ON j.id=t.job_id JOIN users u ON u.id=t.user_id
	 WHERE t.job_id=? AND t.sequence=? AND t.status='processing' AND t.claim_token=? AND j.kind='mail' AND j.status='running'
	 AND NOT EXISTS (SELECT 1 FROM user_lifecycles lifecycle WHERE lifecycle.user_id=u.id AND lifecycle.deactivated_at IS NOT NULL)
	)`, jobID, sequence, claimToken).Scan(&active)
	return active, err
}

// OutboxClaimActive fences snapshots already held by a delivery worker after a
// lifecycle operation cancelled their persisted claims. Table names come only
// from this fixed allowlist, never from caller-controlled SQL.
func (s *Store) OutboxClaimActive(ctx context.Context, kind string, id int64, claimToken string) (bool, error) {
	var table string
	switch kind {
	case "ticket":
		table = "ticket_mail_outbox"
	case "password_reset":
		table = "password_reset_mail_outbox"
	case "registration":
		table = "registration_email_mail_outbox"
	case "login_link":
		table = "login_link_mail_outbox"
	case "subscription_reminder":
		table = "subscription_reminder_outbox"
	case "telegram":
		table = "telegram_message_outbox"
	default:
		return false, ErrInvalidInput
	}
	if id < 1 || claimToken == "" {
		return false, ErrInvalidInput
	}
	activeState := ` AND sent_at IS NULL AND failed_at IS NULL`
	if kind != "ticket" {
		activeState += ` AND cancelled_at IS NULL`
	}
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id=? AND claim_token=?`+activeState+`)`, id, claimToken).Scan(&active)
	return active, err
}
