package store

import (
	"context"
	"database/sql"
	"fmt"
)

func applySchemaV61UserLifecycle(ctx context.Context, tx *sql.Tx) error {
	// Old unowned mail is not evidence that the current holder of an email
	// address owns its history. Only new registration requests opt into binding.
	for _, column := range []struct{ table, name, definition string }{
		{"password_reset_mail_outbox", "user_id", `INTEGER REFERENCES users(id) ON DELETE RESTRICT`},
		{"registration_email_mail_outbox", "user_id", `INTEGER REFERENCES users(id) ON DELETE RESTRICT`},
		{"registration_email_mail_outbox", "owner_pending", `INTEGER NOT NULL DEFAULT 0 CHECK (owner_pending IN (0,1) AND (owner_pending=0 OR user_id IS NULL))`},
	} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info(?) WHERE name=?)`, column.table, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect lifecycle mail ownership column: %w", err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, `ALTER TABLE `+column.table+` ADD COLUMN `+column.name+` `+column.definition); err != nil {
				return fmt.Errorf("add lifecycle mail ownership column: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, schemaV61UserLifecycle); err != nil {
		return err
	}
	// The latest reset request can be linked while its exact challenge still
	// exists. Older aliases and deleted challenges deliberately remain unknown.
	_, err := tx.ExecContext(ctx, `UPDATE password_reset_mail_outbox AS o SET user_id=(
	 SELECT c.user_id FROM password_reset_challenges c JOIN users u ON u.id=c.user_id
	 WHERE c.email_digest=o.email_digest AND c.updated_at=o.created_at AND u.account_kind='human'
	) WHERE o.user_id IS NULL AND o.id IN (
	 SELECT (SELECT latest.id FROM password_reset_mail_outbox latest WHERE latest.email_digest=c.email_digest ORDER BY latest.id DESC LIMIT 1)
	 FROM password_reset_challenges c WHERE c.user_id IS NOT NULL
	)
	 AND EXISTS(SELECT 1 FROM password_reset_challenges c JOIN users u ON u.id=c.user_id
	 WHERE c.email_digest=o.email_digest AND c.updated_at=o.created_at AND u.account_kind='human')`)
	if err != nil {
		return fmt.Errorf("backfill proven password reset mail owner: %w", err)
	}
	return nil
}

const schemaV61UserLifecycle = `
CREATE TABLE IF NOT EXISTS user_lifecycles (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE RESTRICT,
    deactivated_at INTEGER CHECK (deactivated_at IS NULL OR deactivated_at >= 0),
    restore_until INTEGER,
    anonymized_at INTEGER,
    previous_banned INTEGER NOT NULL CHECK (previous_banned IN (0,1)),
    CHECK ((deactivated_at IS NULL) = (restore_until IS NULL)),
    CHECK (restore_until IS NULL OR restore_until = deactivated_at + 2592000),
    CHECK (anonymized_at IS NULL OR (deactivated_at IS NOT NULL AND anonymized_at >= restore_until))
);
CREATE TABLE IF NOT EXISTS user_lifecycle_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    administrator_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    action TEXT NOT NULL CHECK (action IN ('deactivate','restore','anonymize')),
    revision INTEGER NOT NULL CHECK (revision > 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    UNIQUE(user_id, revision)
);
CREATE INDEX IF NOT EXISTS idx_user_lifecycle_events_user ON user_lifecycle_events(user_id,id);
CREATE INDEX IF NOT EXISTS idx_admin_user_bulk_targets_user ON admin_user_bulk_targets(user_id,job_id);
CREATE INDEX IF NOT EXISTS idx_password_reset_mail_owner ON password_reset_mail_outbox(user_id,id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_password_reset_mail_digest ON password_reset_mail_outbox(email_digest,id DESC);
CREATE INDEX IF NOT EXISTS idx_registration_email_mail_owner ON registration_email_mail_outbox(user_id,id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_registration_email_mail_pending_owner ON registration_email_mail_outbox(recipient COLLATE NOCASE,created_at) WHERE user_id IS NULL AND owner_pending=1;
CREATE TRIGGER IF NOT EXISTS users_registration_mail_owner AFTER INSERT ON users
WHEN NEW.account_kind='human'
BEGIN
 UPDATE registration_email_mail_outbox SET user_id=NEW.id,owner_pending=0
 WHERE user_id IS NULL AND owner_pending=1 AND recipient=NEW.email COLLATE NOCASE AND created_at<=NEW.created_at;
END;
CREATE TRIGGER IF NOT EXISTS password_reset_mail_owner_guard BEFORE UPDATE OF user_id ON password_reset_mail_outbox
WHEN OLD.user_id IS NOT NULL AND NEW.user_id IS NOT OLD.user_id
BEGIN SELECT RAISE(ABORT,'mail ownership is immutable'); END;
CREATE TRIGGER IF NOT EXISTS registration_email_mail_owner_guard BEFORE UPDATE OF user_id,owner_pending ON registration_email_mail_outbox
WHEN (OLD.user_id IS NOT NULL AND NEW.user_id IS NOT OLD.user_id) OR NEW.owner_pending>OLD.owner_pending
BEGIN SELECT RAISE(ABORT,'mail ownership is immutable'); END;
CREATE TRIGGER IF NOT EXISTS user_lifecycles_anonymized_guard BEFORE UPDATE ON user_lifecycles
WHEN OLD.anonymized_at IS NOT NULL
BEGIN SELECT RAISE(ABORT,'anonymized lifecycle is immutable'); END;
CREATE TRIGGER IF NOT EXISTS user_lifecycle_events_update_guard BEFORE UPDATE ON user_lifecycle_events
BEGIN SELECT RAISE(ABORT,'lifecycle events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS user_lifecycle_events_delete_guard BEFORE DELETE ON user_lifecycle_events
BEGIN SELECT RAISE(ABORT,'lifecycle events are append-only'); END;
CREATE TRIGGER IF NOT EXISTS users_lifecycle_ban_guard BEFORE UPDATE OF banned ON users
WHEN NEW.banned = 0 AND EXISTS (SELECT 1 FROM user_lifecycles WHERE user_id=NEW.id AND deactivated_at IS NOT NULL)
BEGIN SELECT RAISE(ABORT,'inactive user cannot be unbanned'); END;
CREATE TRIGGER IF NOT EXISTS users_lifecycle_identity_guard BEFORE UPDATE OF email,password_hash,telegram_id,remarks,distributor_name ON users
WHEN EXISTS (SELECT 1 FROM user_lifecycles WHERE user_id=NEW.id AND anonymized_at IS NOT NULL)
 AND (NEW.email IS NOT OLD.email OR NEW.password_hash IS NOT OLD.password_hash OR NEW.telegram_id IS NOT OLD.telegram_id
      OR NEW.remarks IS NOT OLD.remarks OR NEW.distributor_name IS NOT OLD.distributor_name)
BEGIN SELECT RAISE(ABORT,'anonymized identity is immutable'); END;
`
