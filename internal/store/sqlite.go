package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 23

type Store struct {
	db      *sql.DB
	writeMu sync.Mutex
}

func (s *Store) lockWrite() func() {
	s.writeMu.Lock()
	return s.writeMu.Unlock
}

func OpenSQLite(dsn string) (*Store, error) {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > currentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	if version < 1 {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
		version = 1
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
			return fmt.Errorf("apply schema v2: %w", err)
		}
		version = 2
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, schemaV3); err != nil {
			return fmt.Errorf("apply schema v3: %w", err)
		}
		version = 3
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, schemaV4); err != nil {
			return fmt.Errorf("apply schema v4: %w", err)
		}
		version = 4
	}
	if version < 5 {
		if _, err := tx.ExecContext(ctx, schemaV5); err != nil {
			return fmt.Errorf("apply schema v5: %w", err)
		}
		version = 5
	}
	if version < 6 {
		if _, err := tx.ExecContext(ctx, schemaV6); err != nil {
			return fmt.Errorf("apply schema v6: %w", err)
		}
		version = 6
	}
	if version < 7 {
		if _, err := tx.ExecContext(ctx, schemaV7); err != nil {
			return fmt.Errorf("apply schema v7: %w", err)
		}
		if err := backfillSubscriptionTokens(ctx, tx); err != nil {
			return fmt.Errorf("backfill schema v7 subscription tokens: %w", err)
		}
		if _, err := tx.ExecContext(ctx, schemaV7Constraints); err != nil {
			return fmt.Errorf("apply schema v7 constraints: %w", err)
		}
		version = 7
	}
	if version < 8 {
		if _, err := tx.ExecContext(ctx, schemaV8); err != nil {
			return fmt.Errorf("apply schema v8: %w", err)
		}
		version = 8
	}
	if version < 9 {
		if _, err := tx.ExecContext(ctx, schemaV9); err != nil {
			return fmt.Errorf("apply schema v9: %w", err)
		}
		version = 9
	}
	if version < 10 {
		if _, err := tx.ExecContext(ctx, schemaV10); err != nil {
			return fmt.Errorf("apply schema v10: %w", err)
		}
		version = 10
	}
	if version < 11 {
		if _, err := tx.ExecContext(ctx, schemaV11); err != nil {
			return fmt.Errorf("apply schema v11: %w", err)
		}
		version = 11
	}
	if version < 12 {
		if _, err := tx.ExecContext(ctx, schemaV12); err != nil {
			return fmt.Errorf("apply schema v12: %w", err)
		}
		version = 12
	}
	if version < 13 {
		if _, err := tx.ExecContext(ctx, schemaV13); err != nil {
			return fmt.Errorf("apply schema v13: %w", err)
		}
		version = 13
	}
	if version < 14 {
		if _, err := tx.ExecContext(ctx, schemaV14); err != nil {
			return fmt.Errorf("apply schema v14: %w", err)
		}
		version = 14
	}
	if version < 15 {
		if _, err := tx.ExecContext(ctx, schemaV15); err != nil {
			return fmt.Errorf("apply schema v15: %w", err)
		}
		version = 15
	}
	if version < 16 {
		if _, err := tx.ExecContext(ctx, schemaV16); err != nil {
			return fmt.Errorf("apply schema v16: %w", err)
		}
		version = 16
	}
	if version < 17 {
		if _, err := tx.ExecContext(ctx, schemaV17); err != nil {
			return fmt.Errorf("apply schema v17: %w", err)
		}
		version = 17
	}
	if version < 18 {
		if _, err := tx.ExecContext(ctx, schemaV18); err != nil {
			return fmt.Errorf("apply schema v18: %w", err)
		}
		version = 18
	}
	if version < 19 {
		if _, err := tx.ExecContext(ctx, schemaV19); err != nil {
			return fmt.Errorf("apply schema v19: %w", err)
		}
		version = 19
	}
	if version < 20 {
		if _, err := tx.ExecContext(ctx, schemaV20); err != nil {
			return fmt.Errorf("apply schema v20: %w", err)
		}
		version = 20
	}
	if version < 21 {
		if _, err := tx.ExecContext(ctx, schemaV21); err != nil {
			return fmt.Errorf("apply schema v21: %w", err)
		}
		version = 21
	}
	if version < 22 {
		if _, err := tx.ExecContext(ctx, schemaV22); err != nil {
			return fmt.Errorf("apply schema v22: %w", err)
		}
		version = 22
	}
	if version < 23 {
		if _, err := tx.ExecContext(ctx, schemaV23); err != nil {
			return fmt.Errorf("apply schema v23: %w", err)
		}
		version = 23
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}

func backfillSubscriptionTokens(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM users WHERE subscription_token IS NULL ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list users missing subscription tokens: %w", err)
	}
	userIDs := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan user missing subscription token: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close subscription token migration rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate users missing subscription tokens: %w", err)
	}
	for _, userID := range userIDs {
		token, err := newSubscriptionToken()
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET subscription_token = ? WHERE id = ? AND subscription_token IS NULL`, token, userID)
		if err != nil {
			return fmt.Errorf("set subscription token for user %d: %w", userID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read subscription token update for user %d: %w", userID, err)
		}
		if updated != 1 {
			return fmt.Errorf("set subscription token for user %d: unexpected updated rows %d", userID, updated)
		}
	}
	return nil
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    banned INTEGER NOT NULL DEFAULT 0 CHECK (banned IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS admin_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    csrf_hash TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_user_active ON admin_sessions(user_id, revoked_at, expires_at);

CREATE TABLE IF NOT EXISTS server_machines (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    is_active INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    last_seen_at INTEGER,
    load_status TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    host TEXT NOT NULL,
    port TEXT NOT NULL,
    show INTEGER NOT NULL DEFAULT 1 CHECK (show IN (0, 1)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    sort INTEGER NOT NULL DEFAULT 0,
    machine_id INTEGER REFERENCES server_machines(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_nodes_machine_sort ON nodes(machine_id, sort, id);

CREATE TABLE IF NOT EXISTS server_machine_credentials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER NOT NULL REFERENCES server_machines(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    last_used_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_machine_credentials_active ON server_machine_credentials(machine_id, revoked_at, created_at);

CREATE TABLE IF NOT EXISTS server_machine_enrollments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER NOT NULL REFERENCES server_machines(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    revoke_existing INTEGER NOT NULL DEFAULT 0 CHECK (revoke_existing IN (0, 1)),
    expires_at INTEGER NOT NULL,
    consumed_at INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_machine_enrollments_active ON server_machine_enrollments(machine_id, consumed_at, expires_at);

CREATE TABLE IF NOT EXISTS server_machine_load_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    machine_id INTEGER NOT NULL REFERENCES server_machines(id) ON DELETE CASCADE,
    cpu REAL NOT NULL,
    mem_total INTEGER NOT NULL,
    mem_used INTEGER NOT NULL,
    disk_total INTEGER NOT NULL,
    disk_used INTEGER NOT NULL,
    net_in_speed REAL NOT NULL DEFAULT 0,
    net_out_speed REAL NOT NULL DEFAULT 0,
    recorded_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_machine_history_time ON server_machine_load_history(machine_id, recorded_at DESC);

CREATE TABLE IF NOT EXISTS server_activation_schedules (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    schedule_type TEXT NOT NULL DEFAULT 'daily' CHECK (schedule_type IN ('daily', 'once')),
    timezone TEXT,
    enable_second INTEGER,
    disable_second INTEGER,
    enable_at INTEGER,
    disable_at INTEGER,
    revision TEXT NOT NULL,
    next_transition_at INTEGER,
    next_target_enabled INTEGER CHECK (next_target_enabled IN (0, 1)),
    enabled_applied_at INTEGER,
    disabled_applied_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (
        (schedule_type = 'daily' AND timezone IS NOT NULL AND enable_second BETWEEN 0 AND 86399 AND disable_second BETWEEN 0 AND 86399 AND enable_second <> disable_second)
        OR
        (schedule_type = 'once' AND enable_at IS NOT NULL AND disable_at IS NOT NULL AND enable_at < disable_at)
    )
);
CREATE INDEX IF NOT EXISTS idx_activation_schedules_due ON server_activation_schedules(next_transition_at);
`

const schemaV2 = `
ALTER TABLE users ADD COLUMN uuid TEXT;
ALTER TABLE users ADD COLUMN group_id INTEGER;
ALTER TABLE users ADD COLUMN transfer_enable INTEGER NOT NULL DEFAULT 0 CHECK (transfer_enable >= 0);
ALTER TABLE users ADD COLUMN traffic_u INTEGER NOT NULL DEFAULT 0 CHECK (traffic_u >= 0);
ALTER TABLE users ADD COLUMN traffic_d INTEGER NOT NULL DEFAULT 0 CHECK (traffic_d >= 0);
ALTER TABLE users ADD COLUMN expired_at INTEGER;
ALTER TABLE users ADD COLUMN speed_limit INTEGER NOT NULL DEFAULT 0 CHECK (speed_limit >= 0);
ALTER TABLE users ADD COLUMN device_limit INTEGER NOT NULL DEFAULT 0 CHECK (device_limit >= 0);
ALTER TABLE users ADD COLUMN online_count INTEGER NOT NULL DEFAULT 0 CHECK (online_count >= 0);
ALTER TABLE users ADD COLUMN last_online_at INTEGER;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_runtime_uuid ON users(uuid) WHERE uuid IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_runtime_group ON users(group_id, banned, expired_at);

ALTER TABLE nodes ADD COLUMN rate_micros INTEGER NOT NULL DEFAULT 1000000 CHECK (rate_micros BETWEEN 1 AND 1000000000);
ALTER TABLE nodes ADD COLUMN runtime_config TEXT;
ALTER TABLE nodes ADD COLUMN traffic_u INTEGER NOT NULL DEFAULT 0 CHECK (traffic_u >= 0);
ALTER TABLE nodes ADD COLUMN traffic_d INTEGER NOT NULL DEFAULT 0 CHECK (traffic_d >= 0);
ALTER TABLE nodes ADD COLUMN last_check_at INTEGER;
ALTER TABLE nodes ADD COLUMN last_push_at INTEGER;

CREATE TABLE IF NOT EXISTS node_group_memberships (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL CHECK (group_id > 0),
    PRIMARY KEY (node_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_node_groups_group ON node_group_memberships(group_id, node_id);

CREATE TABLE IF NOT EXISTS node_report_receipts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    report_id TEXT NOT NULL,
    traffic_hash BLOB NOT NULL CHECK (length(traffic_hash) = 32),
    created_at INTEGER NOT NULL,
    UNIQUE (node_id, report_id)
);
CREATE INDEX IF NOT EXISTS idx_node_report_receipts_created ON node_report_receipts(created_at);

CREATE TABLE IF NOT EXISTS node_report_traffic_stage (
    report_key TEXT NOT NULL,
    user_id INTEGER NOT NULL CHECK (user_id > 0),
    upload INTEGER NOT NULL CHECK (upload >= 0),
    download INTEGER NOT NULL CHECK (download >= 0),
    weighted_upload INTEGER NOT NULL CHECK (weighted_upload >= 0),
    weighted_download INTEGER NOT NULL CHECK (weighted_download >= 0),
    PRIMARY KEY (report_key, user_id)
);

CREATE TABLE IF NOT EXISTS user_traffic_stats (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rate_micros INTEGER NOT NULL,
    record_at INTEGER NOT NULL,
    record_type TEXT NOT NULL DEFAULT 'd' CHECK (record_type IN ('d', 'm')),
    upload INTEGER NOT NULL CHECK (upload >= 0),
    download INTEGER NOT NULL CHECK (download >= 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (user_id, rate_micros, record_at, record_type)
);

CREATE TABLE IF NOT EXISTS node_traffic_stats (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    record_at INTEGER NOT NULL,
    record_type TEXT NOT NULL DEFAULT 'd' CHECK (record_type IN ('d', 'm')),
    upload INTEGER NOT NULL CHECK (upload >= 0),
    download INTEGER NOT NULL CHECK (download >= 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, record_at, record_type)
);

CREATE TABLE IF NOT EXISTS node_device_ips (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    ip TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, user_id, ip)
);
CREATE INDEX IF NOT EXISTS idx_node_device_ips_user_expiry ON node_device_ips(user_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_node_device_ips_expiry ON node_device_ips(expires_at, user_id);

CREATE TABLE IF NOT EXISTS node_user_online (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    connections INTEGER NOT NULL CHECK (connections >= 0),
    expires_at INTEGER NOT NULL,
    PRIMARY KEY (node_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_node_user_online_expiry ON node_user_online(expires_at);

CREATE TABLE IF NOT EXISTS node_runtime_state (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    status_json TEXT,
    metrics_json TEXT,
    updated_at INTEGER NOT NULL
);
`

const schemaV3 = `
CREATE TABLE server_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO server_groups (id, name, created_at, updated_at)
SELECT group_id, 'Imported group ' || group_id, unixepoch(), unixepoch()
FROM (
    SELECT group_id FROM users WHERE group_id IS NOT NULL AND group_id > 0
    UNION
    SELECT group_id FROM node_group_memberships WHERE group_id > 0
);

ALTER TABLE node_group_memberships RENAME TO node_group_memberships_v2;
CREATE TABLE node_group_memberships (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    group_id INTEGER NOT NULL REFERENCES server_groups(id) ON DELETE RESTRICT,
    PRIMARY KEY (node_id, group_id)
);
INSERT INTO node_group_memberships (node_id, group_id)
SELECT node_id, group_id FROM node_group_memberships_v2;
DROP TABLE node_group_memberships_v2;
CREATE INDEX idx_node_groups_group ON node_group_memberships(group_id, node_id);

CREATE TRIGGER users_group_insert_guard
BEFORE INSERT ON users
WHEN NEW.group_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM server_groups WHERE id = NEW.group_id)
BEGIN
    SELECT RAISE(ABORT, 'user group does not exist');
END;

CREATE TRIGGER users_group_update_guard
BEFORE UPDATE OF group_id ON users
WHEN NEW.group_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM server_groups WHERE id = NEW.group_id)
BEGIN
    SELECT RAISE(ABORT, 'user group does not exist');
END;

CREATE TRIGGER server_groups_user_delete_guard
BEFORE DELETE ON server_groups
WHEN EXISTS (SELECT 1 FROM users WHERE group_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'server group is referenced by users');
END;

CREATE TABLE routing_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    remarks TEXT NOT NULL CHECK (length(remarks) BETWEEN 1 AND 255),
    match_json TEXT NOT NULL CHECK (json_valid(match_json) AND json_type(match_json) = 'array' AND json_array_length(match_json) BETWEEN 1 AND 1000),
    action TEXT NOT NULL CHECK (action IN ('block', 'direct', 'dns', 'proxy')),
    action_value TEXT NOT NULL DEFAULT '' CHECK (
        length(action_value) <= 255 AND
        ((action IN ('block', 'direct') AND action_value = '') OR (action IN ('dns', 'proxy') AND length(action_value) > 0))
    ),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE node_route_memberships (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    route_id INTEGER NOT NULL REFERENCES routing_rules(id) ON DELETE RESTRICT,
    PRIMARY KEY (node_id, route_id)
);
CREATE INDEX idx_node_routes_route ON node_route_memberships(route_id, node_id);
`

const schemaV4 = `
ALTER TABLE users ADD COLUMN account_kind TEXT NOT NULL DEFAULT 'human'
    CHECK (account_kind IN ('human', 'internal_subscription'));
ALTER TABLE users ADD COLUMN admin_revision INTEGER NOT NULL DEFAULT 1 CHECK (admin_revision > 0);

CREATE INDEX idx_users_directory_kind_id ON users(account_kind, id DESC);
CREATE INDEX idx_users_directory_banned_id ON users(account_kind, banned, id DESC);
CREATE INDEX idx_users_directory_group_id ON users(account_kind, group_id, id DESC);
CREATE INDEX idx_users_directory_email_id ON users(account_kind, email COLLATE NOCASE, id DESC);
`

const schemaV5 = `
CREATE TABLE notices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    sort_position INTEGER NOT NULL DEFAULT 0 CHECK (sort_position >= 0),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 255),
    content TEXT NOT NULL CHECK (length(content) BETWEEN 1 AND 262144),
    image_url TEXT CHECK (image_url IS NULL OR length(image_url) BETWEEN 1 AND 2048),
    tags_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(tags_json) AND json_type(tags_json) = 'array' AND json_array_length(tags_json) <= 20),
    visible INTEGER NOT NULL DEFAULT 0 CHECK (visible IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_notices_sort ON notices(sort_position, id DESC);
CREATE INDEX idx_notices_visible_sort ON notices(visible, sort_position, id DESC);
`

const schemaV6 = `
CREATE TABLE client_catalog_config (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO client_catalog_config (id, revision, updated_at) VALUES (1, 1, 0);

CREATE TABLE client_catalog_links (
    client_id TEXT NOT NULL CHECK (length(client_id) BETWEEN 1 AND 64),
    platform TEXT NOT NULL CHECK (platform IN ('android', 'ios', 'windows', 'macos', 'linux')),
    action TEXT NOT NULL CHECK (action IN ('direct', 'qr', 'cloud', 'tutorial')),
    url TEXT NOT NULL CHECK (length(url) BETWEEN 1 AND 2048),
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (client_id, platform, action)
);
`

const schemaV7 = `
ALTER TABLE users ADD COLUMN subscription_token TEXT;

CREATE TABLE knowledge (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    language TEXT NOT NULL CHECK (language IN ('en-US', 'ja-JP', 'ko-KR', 'vi-VN', 'zh-CN', 'zh-TW', 'ru-RU')),
    category TEXT NOT NULL CHECK (length(category) BETWEEN 1 AND 255),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 255),
    body TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 1048576),
    sort_position INTEGER NOT NULL DEFAULT 0 CHECK (sort_position >= 0),
    visible INTEGER NOT NULL DEFAULT 0 CHECK (visible IN (0, 1)),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_knowledge_admin_sort ON knowledge(sort_position, id DESC);
CREATE INDEX idx_knowledge_user_language_sort ON knowledge(visible, language, sort_position, id DESC);
CREATE INDEX idx_knowledge_public_sort ON knowledge(visible, sort_position, id DESC);
`

const schemaV7Constraints = `
CREATE UNIQUE INDEX idx_users_subscription_token ON users(subscription_token);

CREATE TRIGGER users_subscription_token_insert_guard
BEFORE INSERT ON users
WHEN NEW.subscription_token IS NULL OR (
    length(NEW.subscription_token) <> 32 OR NEW.subscription_token GLOB '*[^0-9a-f]*'
)
BEGIN
    SELECT RAISE(ABORT, 'invalid subscription token');
END;

CREATE TRIGGER users_subscription_token_update_guard
BEFORE UPDATE OF subscription_token ON users
WHEN NEW.subscription_token IS NULL OR length(NEW.subscription_token) <> 32 OR NEW.subscription_token GLOB '*[^0-9a-f]*'
BEGIN
    SELECT RAISE(ABORT, 'invalid subscription token');
END;
`

const schemaV8 = `
CREATE TABLE tickets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    level INTEGER NOT NULL CHECK (level IN (0, 1, 2)),
    status INTEGER NOT NULL DEFAULT 0 CHECK (status IN (0, 1)),
    reply_status INTEGER NOT NULL DEFAULT 0 CHECK (reply_status IN (0, 1)),
    last_reply_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_tickets_one_open_per_user ON tickets(user_id) WHERE status = 0;
CREATE INDEX idx_tickets_user_created ON tickets(user_id, created_at DESC, id DESC);
CREATE INDEX idx_tickets_status_updated ON tickets(status, updated_at DESC, id DESC);
CREATE INDEX idx_tickets_status_reply_updated ON tickets(status, reply_status, updated_at DESC, id DESC);
CREATE INDEX idx_tickets_level_updated ON tickets(level, updated_at DESC, id DESC);

CREATE TABLE ticket_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    message TEXT NOT NULL CHECK (length(message) BETWEEN 1 AND 65536),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_ticket_messages_ticket ON ticket_messages(ticket_id, id);
`

const schemaV9 = `
CREATE TABLE app_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    app_name TEXT NOT NULL DEFAULT 'Xboard-Go' CHECK (length(app_name) BETWEEN 1 AND 100),
    app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048),
    ticket_must_wait_reply INTEGER NOT NULL DEFAULT 0 CHECK (ticket_must_wait_reply IN (0, 1)),
    smtp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (smtp_enabled IN (0, 1)),
    smtp_host TEXT NOT NULL DEFAULT '' CHECK (length(smtp_host) <= 253),
    smtp_port INTEGER NOT NULL DEFAULT 587 CHECK (smtp_port BETWEEN 1 AND 65535),
    smtp_username TEXT NOT NULL DEFAULT '' CHECK (length(smtp_username) <= 320),
    smtp_password_cipher BLOB CHECK (smtp_password_cipher IS NULL OR length(smtp_password_cipher) BETWEEN 1 AND 8192),
    smtp_encryption TEXT NOT NULL DEFAULT 'starttls' CHECK (smtp_encryption IN ('starttls', 'tls', 'none')),
    smtp_from_address TEXT NOT NULL DEFAULT '' CHECK (length(smtp_from_address) <= 320),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO app_settings (id) VALUES (1);

CREATE TABLE ticket_mail_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ticket_message_id INTEGER NOT NULL UNIQUE REFERENCES ticket_messages(id) ON DELETE CASCADE,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    available_at INTEGER NOT NULL,
    claim_token TEXT,
    claimed_at INTEGER,
    sent_at INTEGER,
    failed_at INTEGER,
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 1024),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK ((claim_token IS NULL) = (claimed_at IS NULL)),
    CHECK (sent_at IS NULL OR failed_at IS NULL)
);
CREATE INDEX idx_ticket_mail_outbox_due ON ticket_mail_outbox(available_at, id)
    WHERE sent_at IS NULL AND failed_at IS NULL;

CREATE TABLE ticket_mail_throttle (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    last_enqueued_at INTEGER NOT NULL
);
`

const schemaV10 = `
ALTER TABLE ticket_mail_outbox ADD COLUMN recipient TEXT NOT NULL DEFAULT '' CHECK (length(recipient) <= 320);
ALTER TABLE ticket_mail_outbox ADD COLUMN ticket_subject TEXT NOT NULL DEFAULT '' CHECK (length(ticket_subject) <= 200);
ALTER TABLE ticket_mail_outbox ADD COLUMN reply_message TEXT NOT NULL DEFAULT '' CHECK (length(reply_message) <= 10000);
ALTER TABLE ticket_mail_outbox ADD COLUMN app_name TEXT NOT NULL DEFAULT '' CHECK (length(app_name) <= 100);
ALTER TABLE ticket_mail_outbox ADD COLUMN app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048);
UPDATE ticket_mail_outbox
SET recipient = (SELECT u.email FROM ticket_messages m JOIN tickets t ON t.id = m.ticket_id JOIN users u ON u.id = t.user_id WHERE m.id = ticket_mail_outbox.ticket_message_id),
    ticket_subject = (SELECT t.subject FROM ticket_messages m JOIN tickets t ON t.id = m.ticket_id WHERE m.id = ticket_mail_outbox.ticket_message_id),
    reply_message = (SELECT m.message FROM ticket_messages m WHERE m.id = ticket_mail_outbox.ticket_message_id),
    app_name = (SELECT app_name FROM app_settings WHERE id = 1),
    app_url = (SELECT app_url FROM app_settings WHERE id = 1);
`

const schemaV11 = `
CREATE TABLE admin_audit_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    administrator_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    administrator_email TEXT NOT NULL CHECK (length(administrator_email) BETWEEN 1 AND 320),
    method TEXT NOT NULL CHECK (method IN ('POST', 'PUT', 'PATCH', 'DELETE')),
    route TEXT NOT NULL CHECK (length(route) BETWEEN 1 AND 512),
    status_code INTEGER NOT NULL CHECK (status_code BETWEEN 100 AND 599),
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_admin_audit_logs_created ON admin_audit_logs(id DESC);
CREATE INDEX idx_admin_audit_logs_administrator ON admin_audit_logs(administrator_id, id DESC);
CREATE INDEX idx_admin_audit_logs_method ON admin_audit_logs(method, id DESC);
`

const schemaV12 = `
CREATE INDEX idx_ticket_mail_outbox_failed ON ticket_mail_outbox(failed_at DESC, id DESC)
    WHERE failed_at IS NOT NULL;
`

const schemaV13 = `
ALTER TABLE app_settings ADD COLUMN app_description TEXT NOT NULL DEFAULT '' CHECK (length(app_description) <= 500);
ALTER TABLE app_settings ADD COLUMN tos_url TEXT NOT NULL DEFAULT '' CHECK (length(tos_url) <= 2048);
`

const schemaV14 = `
ALTER TABLE app_settings ADD COLUMN logo TEXT NOT NULL DEFAULT '' CHECK (length(logo) <= 2048);
`

const schemaV15 = `
ALTER TABLE app_settings ADD COLUMN stop_register INTEGER NOT NULL DEFAULT 0 CHECK (stop_register IN (0, 1));
`

const schemaV16 = `
ALTER TABLE app_settings ADD COLUMN email_whitelist_enable INTEGER NOT NULL DEFAULT 0 CHECK (email_whitelist_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN email_whitelist_suffix TEXT NOT NULL DEFAULT '` + defaultEmailWhitelistStorage + `' CHECK (length(email_whitelist_suffix) <= 8192);
ALTER TABLE app_settings ADD COLUMN email_gmail_limit_enable INTEGER NOT NULL DEFAULT 0 CHECK (email_gmail_limit_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN register_limit_by_ip_enable INTEGER NOT NULL DEFAULT 0 CHECK (register_limit_by_ip_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN register_limit_count INTEGER NOT NULL DEFAULT 3 CHECK (register_limit_count BETWEEN 1 AND 100);
ALTER TABLE app_settings ADD COLUMN register_limit_expire INTEGER NOT NULL DEFAULT 60 CHECK (register_limit_expire BETWEEN 1 AND 10080);
CREATE TABLE registration_ip_limits (
    source_ip TEXT PRIMARY KEY CHECK (length(source_ip) BETWEEN 2 AND 64),
    successful_count INTEGER NOT NULL CHECK (successful_count BETWEEN 1 AND 100),
    reset_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_registration_ip_limits_reset_at ON registration_ip_limits(reset_at);
`

const schemaV17 = `
CREATE TABLE password_reset_challenges (
    email_digest BLOB PRIMARY KEY CHECK (length(email_digest) = 32),
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    code_digest BLOB CHECK (code_digest IS NULL OR length(code_digest) = 32),
    expires_at INTEGER NOT NULL,
    resend_after INTEGER NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 3),
    failure_reset_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_password_reset_challenges_expires ON password_reset_challenges(expires_at);

CREATE TABLE password_reset_mail_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email_digest BLOB NOT NULL CHECK (length(email_digest) = 32),
    recipient TEXT NOT NULL CHECK (length(recipient) BETWEEN 3 AND 320),
    code_cipher BLOB CHECK (code_cipher IS NULL OR length(code_cipher) BETWEEN 32 AND 512),
    app_name TEXT NOT NULL CHECK (length(app_name) BETWEEN 1 AND 100),
    app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048),
    available_at INTEGER NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    claim_token TEXT,
    claimed_at INTEGER,
    sent_at INTEGER,
    failed_at INTEGER,
    cancelled_at INTEGER,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (claim_token IS NULL OR length(claim_token) BETWEEN 1 AND 128),
    CHECK (last_error IS NULL OR length(last_error) <= 4096)
);
CREATE INDEX idx_password_reset_mail_due ON password_reset_mail_outbox(available_at, id)
    WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL;
CREATE INDEX idx_password_reset_mail_failed ON password_reset_mail_outbox(failed_at DESC, id DESC)
    WHERE failed_at IS NOT NULL;
`

const schemaV18 = `
ALTER TABLE app_settings ADD COLUMN email_verify INTEGER NOT NULL DEFAULT 0 CHECK (email_verify IN (0, 1));

CREATE TABLE registration_email_challenges (
    email_digest BLOB PRIMARY KEY CHECK (length(email_digest) = 32),
    code_digest BLOB NOT NULL CHECK (length(code_digest) = 32),
    expires_at INTEGER NOT NULL,
    resend_after INTEGER NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts BETWEEN 0 AND 3),
    failure_reset_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_registration_email_challenges_expires ON registration_email_challenges(expires_at);

CREATE TABLE registration_email_mail_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email_digest BLOB NOT NULL CHECK (length(email_digest) = 32),
    recipient TEXT NOT NULL CHECK (length(recipient) BETWEEN 3 AND 320),
    code_cipher BLOB CHECK (code_cipher IS NULL OR length(code_cipher) BETWEEN 32 AND 512),
    app_name TEXT NOT NULL CHECK (length(app_name) BETWEEN 1 AND 100),
    app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048),
    available_at INTEGER NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    claim_token TEXT,
    claimed_at INTEGER,
    sent_at INTEGER,
    failed_at INTEGER,
    cancelled_at INTEGER,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (claim_token IS NULL OR length(claim_token) BETWEEN 1 AND 128),
    CHECK (last_error IS NULL OR length(last_error) <= 4096)
);
CREATE INDEX idx_registration_email_mail_due ON registration_email_mail_outbox(available_at, id)
    WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL;
CREATE INDEX idx_registration_email_mail_failed ON registration_email_mail_outbox(failed_at DESC, id DESC)
    WHERE failed_at IS NOT NULL;
`

const schemaV19 = `
ALTER TABLE app_settings ADD COLUMN invite_force INTEGER NOT NULL DEFAULT 0 CHECK (invite_force IN (0, 1));
ALTER TABLE app_settings ADD COLUMN invite_gen_limit INTEGER NOT NULL DEFAULT 5 CHECK (invite_gen_limit BETWEEN 0 AND 100);
ALTER TABLE app_settings ADD COLUMN invite_never_expire INTEGER NOT NULL DEFAULT 0 CHECK (invite_never_expire IN (0, 1));

ALTER TABLE users ADD COLUMN invite_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_users_invite_user_id ON users(invite_user_id);

CREATE TABLE invitation_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_digest BLOB NOT NULL UNIQUE CHECK (length(code_digest) = 32),
    code_cipher BLOB NOT NULL CHECK (length(code_cipher) BETWEEN 32 AND 128),
    pv INTEGER NOT NULL DEFAULT 0 CHECK (pv >= 0),
    consumed_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_invitation_codes_owner_active ON invitation_codes(user_id, created_at DESC, id DESC)
    WHERE consumed_at IS NULL;
`

const schemaV20 = `
ALTER TABLE app_settings ADD COLUMN login_with_mail_link_enable INTEGER NOT NULL DEFAULT 0
    CHECK (login_with_mail_link_enable IN (0, 1));

CREATE TABLE login_link_tokens (
    token_digest BLOB PRIMARY KEY CHECK (length(token_digest) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('quick', 'email')),
    redirect_path TEXT NOT NULL DEFAULT 'dashboard'
        CHECK (redirect_path IN ('dashboard', 'invite', 'knowledge', 'ticket', 'subscribe')),
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_login_link_tokens_expiry ON login_link_tokens(expires_at, token_digest);
CREATE INDEX idx_login_link_tokens_user_purpose_created
    ON login_link_tokens(user_id, purpose, created_at DESC, token_digest);

CREATE TABLE mail_login_request_limits (
    email_digest BLOB PRIMARY KEY CHECK (length(email_digest) = 32),
    resend_after INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_mail_login_request_limits_expiry ON mail_login_request_limits(resend_after, email_digest);

CREATE TABLE login_link_mail_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    token_digest BLOB NOT NULL UNIQUE CHECK (length(token_digest) = 32),
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient TEXT NOT NULL CHECK (length(recipient) BETWEEN 3 AND 320),
    token_cipher BLOB CHECK (token_cipher IS NULL OR length(token_cipher) BETWEEN 32 AND 512),
    redirect_path TEXT NOT NULL
        CHECK (redirect_path IN ('dashboard', 'invite', 'knowledge', 'ticket', 'subscribe')),
    app_name TEXT NOT NULL CHECK (length(app_name) BETWEEN 1 AND 100),
    app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048),
    available_at INTEGER NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    claim_token TEXT,
    claimed_at INTEGER,
    sent_at INTEGER,
    failed_at INTEGER,
    cancelled_at INTEGER,
    last_error TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (claim_token IS NULL OR length(claim_token) BETWEEN 1 AND 128),
    CHECK (last_error IS NULL OR length(last_error) <= 4096)
);
CREATE INDEX idx_login_link_mail_due ON login_link_mail_outbox(available_at, id)
    WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL;
CREATE INDEX idx_login_link_mail_failed ON login_link_mail_outbox(failed_at DESC, id DESC)
    WHERE failed_at IS NOT NULL AND cancelled_at IS NULL;
`

const schemaV21 = `
CREATE TABLE access_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    expires_at INTEGER,
    last_used_at INTEGER,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (last_used_at IS NULL OR last_used_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);
CREATE INDEX idx_access_tokens_user_active ON access_tokens(user_id, created_at DESC, id DESC)
    WHERE revoked_at IS NULL;
CREATE INDEX idx_access_tokens_expiry ON access_tokens(expires_at, id)
    WHERE expires_at IS NOT NULL AND revoked_at IS NULL;
`

const schemaV22 = `
ALTER TABLE app_settings ADD COLUMN password_limit_enable INTEGER NOT NULL DEFAULT 1
    CHECK (password_limit_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN password_limit_count INTEGER NOT NULL DEFAULT 5
    CHECK (password_limit_count BETWEEN 1 AND 20);
ALTER TABLE app_settings ADD COLUMN password_limit_expire INTEGER NOT NULL DEFAULT 60
    CHECK (password_limit_expire BETWEEN 1 AND 1440);

CREATE TABLE login_failure_limits (
    credential_digest BLOB PRIMARY KEY CHECK (length(credential_digest) = 32),
    failure_count INTEGER NOT NULL CHECK (failure_count BETWEEN 1 AND 1000000),
    expires_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CHECK (expires_at > updated_at)
);
CREATE INDEX idx_login_failure_limits_expiry
    ON login_failure_limits(expires_at, credential_digest);
`

const schemaV23 = `
ALTER TABLE app_settings ADD COLUMN captcha_enable INTEGER NOT NULL DEFAULT 0
    CHECK (captcha_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN captcha_type TEXT NOT NULL DEFAULT 'recaptcha'
    CHECK (captcha_type IN ('recaptcha', 'recaptcha-v3', 'turnstile'));
ALTER TABLE app_settings ADD COLUMN recaptcha_site_key TEXT NOT NULL DEFAULT ''
    CHECK (length(recaptcha_site_key) <= 512);
ALTER TABLE app_settings ADD COLUMN recaptcha_secret_cipher BLOB
    CHECK (recaptcha_secret_cipher IS NULL OR length(recaptcha_secret_cipher) BETWEEN 33 AND 8192);
ALTER TABLE app_settings ADD COLUMN recaptcha_v3_site_key TEXT NOT NULL DEFAULT ''
    CHECK (length(recaptcha_v3_site_key) <= 512);
ALTER TABLE app_settings ADD COLUMN recaptcha_v3_score_threshold REAL NOT NULL DEFAULT 0.5
    CHECK (recaptcha_v3_score_threshold > 0 AND recaptcha_v3_score_threshold <= 1);
ALTER TABLE app_settings ADD COLUMN recaptcha_v3_secret_cipher BLOB
    CHECK (recaptcha_v3_secret_cipher IS NULL OR length(recaptcha_v3_secret_cipher) BETWEEN 33 AND 8192);
ALTER TABLE app_settings ADD COLUMN turnstile_site_key TEXT NOT NULL DEFAULT ''
    CHECK (length(turnstile_site_key) <= 512);
ALTER TABLE app_settings ADD COLUMN turnstile_secret_cipher BLOB
    CHECK (turnstile_secret_cipher IS NULL OR length(turnstile_secret_cipher) BETWEEN 33 AND 8192);
`
