package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const currentSchemaVersion = 53

func CurrentSchemaVersion() int {
	return currentSchemaVersion
}

type Store struct {
	db                *sql.DB
	writeMu           sync.Mutex
	themeAppearanceMu sync.RWMutex
	themeAppearance   themeAppearanceCacheEntry
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
	if version < 24 {
		if _, err := tx.ExecContext(ctx, schemaV24); err != nil {
			return fmt.Errorf("apply schema v24: %w", err)
		}
		version = 24
	}
	if version < 25 {
		if _, err := tx.ExecContext(ctx, schemaV25); err != nil {
			return fmt.Errorf("apply schema v25: %w", err)
		}
		version = 25
	}
	if version < 26 {
		if _, err := tx.ExecContext(ctx, schemaV26); err != nil {
			return fmt.Errorf("apply schema v26: %w", err)
		}
		version = 26
	}
	if version < 27 {
		if _, err := tx.ExecContext(ctx, schemaV27); err != nil {
			return fmt.Errorf("apply schema v27: %w", err)
		}
		version = 27
	}
	if version < 28 {
		if _, err := tx.ExecContext(ctx, schemaV28); err != nil {
			return fmt.Errorf("apply schema v28: %w", err)
		}
		version = 28
	}
	if version < 29 {
		if _, err := tx.ExecContext(ctx, schemaV29); err != nil {
			return fmt.Errorf("apply schema v29: %w", err)
		}
		version = 29
	}
	if version < 30 {
		if _, err := tx.ExecContext(ctx, schemaV30); err != nil {
			return fmt.Errorf("apply schema v30: %w", err)
		}
		version = 30
	}
	if version < 31 {
		if _, err := tx.ExecContext(ctx, schemaV31); err != nil {
			return fmt.Errorf("apply schema v31: %w", err)
		}
		version = 31
	}
	if version < 32 {
		if _, err := tx.ExecContext(ctx, schemaV32); err != nil {
			return fmt.Errorf("apply schema v32: %w", err)
		}
		version = 32
	}
	if version < 33 {
		if _, err := tx.ExecContext(ctx, schemaV33); err != nil {
			return fmt.Errorf("apply schema v33: %w", err)
		}
		version = 33
	}
	if version < 34 {
		if _, err := tx.ExecContext(ctx, schemaV34); err != nil {
			return fmt.Errorf("apply schema v34: %w", err)
		}
		version = 34
	}
	if version < 35 {
		if _, err := tx.ExecContext(ctx, schemaV35); err != nil {
			return fmt.Errorf("apply schema v35: %w", err)
		}
		version = 35
	}
	if version < 36 {
		if _, err := tx.ExecContext(ctx, schemaV36); err != nil {
			return fmt.Errorf("apply schema v36: %w", err)
		}
		version = 36
	}
	if version < 37 {
		if err := applySchemaV37(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v37: %w", err)
		}
		version = 37
	}
	if version < 38 {
		if _, err := tx.ExecContext(ctx, schemaV38); err != nil {
			return fmt.Errorf("apply schema v38: %w", err)
		}
		version = 38
	}
	if version < 39 {
		if _, err := tx.ExecContext(ctx, schemaV39); err != nil {
			return fmt.Errorf("apply schema v39: %w", err)
		}
		version = 39
	}
	if version < 40 {
		if _, err := tx.ExecContext(ctx, schemaV40); err != nil {
			return fmt.Errorf("apply schema v40: %w", err)
		}
		version = 40
	}
	if version < 41 {
		if err := applySchemaV41(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v41: %w", err)
		}
		version = 41
	}
	if version < 42 {
		if err := applySchemaV42(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v42: %w", err)
		}
		version = 42
	}
	if version < 43 {
		if _, err := tx.ExecContext(ctx, schemaV43); err != nil {
			return fmt.Errorf("apply schema v43: %w", err)
		}
		version = 43
	}
	if version < 44 {
		if err := applySchemaV44(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v44: %w", err)
		}
		version = 44
	}
	if version < 45 {
		if err := applySchemaV45(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v45: %w", err)
		}
		version = 45
	}
	if version < 46 {
		if err := applySchemaV46(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v46: %w", err)
		}
		version = 46
	}
	if version < 47 {
		if err := applySchemaV47(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v47: %w", err)
		}
		version = 47
	}
	if version < 48 {
		if _, err := tx.ExecContext(ctx, schemaV48); err != nil {
			return fmt.Errorf("apply schema v48: %w", err)
		}
		version = 48
	}
	if version < 49 {
		if _, err := tx.ExecContext(ctx, schemaV49); err != nil {
			return fmt.Errorf("apply schema v49: %w", err)
		}
		version = 49
	}
	if version < 50 {
		if err := applySchemaV50(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v50: %w", err)
		}
		version = 50
	}
	if version < 51 {
		if _, err := tx.ExecContext(ctx, schemaV51); err != nil {
			return fmt.Errorf("apply schema v51: %w", err)
		}
		version = 51
	}
	if version < 52 {
		if err := applySchemaV52(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v52: %w", err)
		}
		version = 52
	}
	if version < 53 {
		if err := applySchemaV53(ctx, tx); err != nil {
			return fmt.Errorf("apply schema v53: %w", err)
		}
		version = 53
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return fmt.Errorf("validate migrated schema: %w", err)
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

const schemaV24 = `
CREATE TABLE legacy_migration_runs (
    slice TEXT NOT NULL CHECK (length(slice) BETWEEN 1 AND 64),
    source_sha256 TEXT NOT NULL
        CHECK (length(source_sha256) = 64 AND source_sha256 NOT GLOB '*[^0-9a-f]*'),
    source_size INTEGER NOT NULL CHECK (source_size > 0),
    rollback_backup_path TEXT NOT NULL CHECK (length(rollback_backup_path) BETWEEN 1 AND 4096),
    rollback_backup_sha256 TEXT NOT NULL
        CHECK (length(rollback_backup_sha256) = 64 AND rollback_backup_sha256 NOT GLOB '*[^0-9a-f]*'),
    report_json TEXT NOT NULL CHECK (json_valid(report_json) AND json_type(report_json) = 'object'),
    applied_at INTEGER NOT NULL,
    PRIMARY KEY (slice, source_sha256)
);
CREATE UNIQUE INDEX idx_legacy_migration_runs_slice ON legacy_migration_runs(slice);
`

const schemaV25 = `
ALTER TABLE users ADD COLUMN last_login_at INTEGER
    CHECK (last_login_at IS NULL OR last_login_at >= 0);
`

const schemaV26 = `
CREATE TABLE node_protocol_definitions (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    external_code TEXT CHECK (external_code IS NULL OR length(external_code) BETWEEN 1 AND 255),
    parent_id INTEGER REFERENCES nodes(id) ON DELETE RESTRICT,
    server_port INTEGER NOT NULL CHECK (server_port BETWEEN 1 AND 65535),
    tags_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(tags_json) AND json_type(tags_json) = 'array'),
    protocol_settings_json TEXT NOT NULL
        CHECK (json_valid(protocol_settings_json) AND json_type(protocol_settings_json) = 'object'),
    rate_time_enabled INTEGER NOT NULL DEFAULT 0 CHECK (rate_time_enabled IN (0, 1)),
    rate_time_ranges_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(rate_time_ranges_json) AND json_type(rate_time_ranges_json) = 'array'),
    custom_outbounds_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(custom_outbounds_json) AND json_type(custom_outbounds_json) = 'array'),
    custom_routes_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(custom_routes_json) AND json_type(custom_routes_json) = 'array'),
    cert_config_json TEXT NOT NULL DEFAULT '{}'
        CHECK (json_valid(cert_config_json) AND json_type(cert_config_json) = 'object'),
    transfer_enable INTEGER NOT NULL DEFAULT 0 CHECK (transfer_enable >= 0),
    configured_rate_micros INTEGER NOT NULL CHECK (configured_rate_micros BETWEEN 0 AND 1000000000)
);
CREATE INDEX idx_node_protocol_parent ON node_protocol_definitions(parent_id, node_id);
CREATE INDEX idx_node_protocol_code ON node_protocol_definitions(external_code, node_id)
    WHERE external_code IS NOT NULL;
`

const schemaV27 = `
CREATE TABLE plans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER REFERENCES server_groups(id) ON DELETE RESTRICT,
    transfer_enable_gib INTEGER NOT NULL CHECK (transfer_enable_gib BETWEEN 1 AND 8388607),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
    speed_limit INTEGER CHECK (speed_limit IS NULL OR speed_limit BETWEEN 0 AND 1000000000),
    show INTEGER NOT NULL DEFAULT 0 CHECK (show IN (0, 1)),
    sort_position INTEGER NOT NULL DEFAULT 0 CHECK (sort_position >= 0),
    renew INTEGER NOT NULL DEFAULT 1 CHECK (renew IN (0, 1)),
    content TEXT NOT NULL DEFAULT '' CHECK (length(CAST(content AS BLOB)) <= 262144),
    reset_traffic_method INTEGER CHECK (reset_traffic_method IS NULL OR reset_traffic_method BETWEEN 0 AND 4),
    capacity_limit INTEGER CHECK (capacity_limit IS NULL OR capacity_limit BETWEEN 0 AND 1000000000),
    prices_json TEXT NOT NULL DEFAULT '{}'
        CHECK (json_valid(prices_json) AND json_type(prices_json) = 'object' AND length(CAST(prices_json AS BLOB)) <= 4096),
    sell INTEGER NOT NULL DEFAULT 0 CHECK (sell IN (0, 1)),
    device_limit INTEGER CHECK (device_limit IS NULL OR device_limit BETWEEN 0 AND 1000),
    tags_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(tags_json) AND json_type(tags_json) = 'array' AND length(CAST(tags_json AS BLOB)) <= 8192),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX idx_plans_catalog ON plans(sort_position, id);
CREATE INDEX idx_plans_public ON plans(show, sell, sort_position, id);
CREATE INDEX idx_plans_group ON plans(group_id, id) WHERE group_id IS NOT NULL;

ALTER TABLE users ADD COLUMN plan_id INTEGER REFERENCES plans(id) ON DELETE RESTRICT;
ALTER TABLE users ADD COLUMN next_reset_at INTEGER CHECK (next_reset_at IS NULL OR next_reset_at >= 0);
ALTER TABLE users ADD COLUMN last_reset_at INTEGER CHECK (last_reset_at IS NULL OR last_reset_at >= 0);
ALTER TABLE users ADD COLUMN reset_count INTEGER NOT NULL DEFAULT 0 CHECK (reset_count >= 0);
CREATE INDEX idx_users_plan_capacity ON users(plan_id, account_kind, expired_at) WHERE plan_id IS NOT NULL;
CREATE INDEX idx_users_due_traffic_reset ON users(next_reset_at, id) WHERE next_reset_at IS NOT NULL AND plan_id IS NOT NULL;

ALTER TABLE app_settings ADD COLUMN traffic_reset_method INTEGER NOT NULL DEFAULT 0
    CHECK (traffic_reset_method BETWEEN 0 AND 4);

CREATE TABLE traffic_reset_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    scheduled_for INTEGER NOT NULL CHECK (scheduled_for >= 0),
    reset_at INTEGER NOT NULL CHECK (reset_at >= scheduled_for),
    upload_before INTEGER NOT NULL CHECK (upload_before >= 0),
    download_before INTEGER NOT NULL CHECK (download_before >= 0),
    reset_count INTEGER NOT NULL CHECK (reset_count > 0),
    UNIQUE (user_id, scheduled_for)
);
CREATE INDEX idx_traffic_reset_logs_user ON traffic_reset_logs(user_id, reset_at DESC, id DESC);
`

const schemaV28 = `
CREATE TABLE subscription_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    path TEXT NOT NULL DEFAULT 's'
        CHECK (length(path) BETWEEN 1 AND 64 AND path NOT GLOB '*[^A-Za-z0-9_-]*'),
    show_info INTEGER NOT NULL DEFAULT 0 CHECK (show_info IN (0, 1)),
    show_protocol INTEGER NOT NULL DEFAULT 0 CHECK (show_protocol IN (0, 1)),
    updated_by INTEGER REFERENCES users(id) ON DELETE RESTRICT,
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0)
);
INSERT INTO subscription_settings (id) VALUES (1);

CREATE TABLE subscription_templates (
    name TEXT PRIMARY KEY CHECK (name IN ('singbox','clash','clashmeta','stash','surge','surfboard')),
    content TEXT NOT NULL DEFAULT '' CHECK (length(CAST(content AS BLOB)) <= 1048576),
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0)
);
INSERT INTO subscription_templates (name) VALUES
    ('singbox'),('clash'),('clashmeta'),('stash'),('surge'),('surfboard');
`

const schemaV29 = `
ALTER TABLE users ADD COLUMN balance INTEGER NOT NULL DEFAULT 0
    CHECK (balance BETWEEN 0 AND 9000000000000000);
ALTER TABLE users ADD COLUMN discount INTEGER
    CHECK (discount IS NULL OR discount BETWEEN 0 AND 100);
ALTER TABLE users ADD COLUMN commission_type INTEGER NOT NULL DEFAULT 0
    CHECK (commission_type BETWEEN 0 AND 2);
ALTER TABLE users ADD COLUMN commission_rate INTEGER
    CHECK (commission_rate IS NULL OR commission_rate BETWEEN 0 AND 100);
ALTER TABLE users ADD COLUMN commission_balance INTEGER NOT NULL DEFAULT 0
    CHECK (commission_balance BETWEEN 0 AND 9000000000000000);

ALTER TABLE app_settings ADD COLUMN plan_change_enable INTEGER NOT NULL DEFAULT 1
    CHECK (plan_change_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN surplus_enable INTEGER NOT NULL DEFAULT 1
    CHECK (surplus_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN new_order_event_id INTEGER NOT NULL DEFAULT 0
    CHECK (new_order_event_id IN (0, 1));
ALTER TABLE app_settings ADD COLUMN renew_order_event_id INTEGER NOT NULL DEFAULT 0
    CHECK (renew_order_event_id IN (0, 1));
ALTER TABLE app_settings ADD COLUMN change_order_event_id INTEGER NOT NULL DEFAULT 0
    CHECK (change_order_event_id IN (0, 1));
ALTER TABLE app_settings ADD COLUMN commission_first_time_enable INTEGER NOT NULL DEFAULT 1
    CHECK (commission_first_time_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN invite_commission INTEGER NOT NULL DEFAULT 10
    CHECK (invite_commission BETWEEN 0 AND 100);

CREATE TABLE orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    plan_id INTEGER NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    payment_id INTEGER CHECK (payment_id IS NULL OR payment_id > 0),
    period TEXT NOT NULL CHECK (period IN (
        'monthly','quarterly','half_yearly','yearly','two_yearly','three_yearly','onetime','reset_traffic'
    )),
    trade_no TEXT NOT NULL UNIQUE CHECK (
        (length(trade_no) = 25 AND trade_no NOT GLOB '*[^0-9]*')
        OR
        (length(trade_no) = 32 AND trade_no NOT GLOB '*[^0-9a-f]*')
    ),
    original_amount INTEGER NOT NULL CHECK (original_amount BETWEEN 0 AND 9000000000000000),
    total_amount INTEGER NOT NULL CHECK (total_amount BETWEEN 0 AND 9000000000000000),
    handling_amount INTEGER CHECK (handling_amount IS NULL OR handling_amount BETWEEN 0 AND 9000000000000000),
    balance_amount INTEGER NOT NULL DEFAULT 0 CHECK (balance_amount BETWEEN 0 AND 9000000000000000),
    surplus_credit INTEGER NOT NULL DEFAULT 0 CHECK (surplus_credit BETWEEN 0 AND 9000000000000000),
    surplus_amount INTEGER NOT NULL DEFAULT 0 CHECK (surplus_amount BETWEEN 0 AND 9000000000000000),
    type INTEGER NOT NULL CHECK (type BETWEEN 1 AND 4),
    status INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 4),
    surplus_order_ids_json TEXT NOT NULL DEFAULT '[]'
        CHECK (json_valid(surplus_order_ids_json) AND json_type(surplus_order_ids_json) = 'array'
               AND length(CAST(surplus_order_ids_json AS BLOB)) <= 262144),
    coupon_id INTEGER CHECK (coupon_id IS NULL OR coupon_id > 0),
    commission_status INTEGER NOT NULL DEFAULT 0 CHECK (commission_status BETWEEN 0 AND 3),
    invite_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    actual_commission_balance INTEGER CHECK (actual_commission_balance IS NULL OR actual_commission_balance BETWEEN 0 AND 9000000000000000),
    commission_rate INTEGER CHECK (commission_rate IS NULL OR commission_rate BETWEEN 0 AND 100),
    commission_auto_check INTEGER CHECK (commission_auto_check IS NULL OR commission_auto_check IN (0, 1)),
    commission_balance INTEGER NOT NULL DEFAULT 0 CHECK (commission_balance BETWEEN 0 AND 9000000000000000),
    discount_amount INTEGER NOT NULL DEFAULT 0 CHECK (discount_amount BETWEEN 0 AND 9000000000000000),
    paid_at INTEGER CHECK (paid_at IS NULL OR paid_at >= 0),
    callback_no TEXT CHECK (callback_no IS NULL OR length(callback_no) BETWEEN 1 AND 255),
    distributor_order_id INTEGER CHECK (distributor_order_id IS NULL OR distributor_order_id > 0),
    entitlement_expired_at_before INTEGER CHECK (entitlement_expired_at_before IS NULL OR entitlement_expired_at_before >= 0),
    entitlement_expired_at_after INTEGER CHECK (entitlement_expired_at_after IS NULL OR entitlement_expired_at_after >= 0),
    distributor_idempotency_key TEXT CHECK (distributor_idempotency_key IS NULL OR length(distributor_idempotency_key) BETWEEN 1 AND 128),
    distributor_settled_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE UNIQUE INDEX idx_orders_user_active ON orders(user_id) WHERE status IN (0, 1);
CREATE INDEX idx_orders_user_created ON orders(user_id, created_at DESC, id DESC);
CREATE INDEX idx_orders_status_created ON orders(status, created_at, id);
CREATE INDEX idx_orders_plan ON orders(plan_id, id);
CREATE INDEX idx_orders_inviter ON orders(invite_user_id, status, id) WHERE invite_user_id IS NOT NULL;

CREATE TABLE order_entitlement_events (
    order_id INTEGER PRIMARY KEY REFERENCES orders(id) ON DELETE RESTRICT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    event_type TEXT NOT NULL CHECK (event_type IN ('new','renewal','upgrade','reset_traffic')),
    before_json TEXT NOT NULL CHECK (json_valid(before_json) AND json_type(before_json) = 'object'),
    after_json TEXT NOT NULL CHECK (json_valid(after_json) AND json_type(after_json) = 'object'),
    applied_at INTEGER NOT NULL CHECK (applied_at >= 0)
);
CREATE INDEX idx_order_entitlement_events_user ON order_entitlement_events(user_id, applied_at DESC, order_id DESC);
`

const schemaV30 = `
ALTER TABLE app_settings ADD COLUMN coupon_enabled INTEGER NOT NULL DEFAULT 1
    CHECK (coupon_enabled IN (0, 1));

CREATE TABLE coupons (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code TEXT NOT NULL UNIQUE CHECK (length(CAST(code AS BLOB)) BETWEEN 1 AND 64),
    name TEXT NOT NULL CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 200),
    type INTEGER NOT NULL CHECK (type IN (1, 2)),
    value INTEGER NOT NULL CHECK (
        (type = 1 AND value BETWEEN 1 AND 9000000000000000)
        OR (type = 2 AND value BETWEEN 1 AND 100)
    ),
    show INTEGER NOT NULL DEFAULT 0 CHECK (show IN (0, 1)),
    limit_use INTEGER CHECK (limit_use IS NULL OR limit_use BETWEEN 0 AND 1000000000),
    limit_use_with_user INTEGER CHECK (limit_use_with_user IS NULL OR limit_use_with_user BETWEEN 0 AND 1000000000),
    limit_plan_ids_json TEXT NOT NULL DEFAULT '[]' CHECK (
        json_valid(limit_plan_ids_json) AND json_type(limit_plan_ids_json) = 'array'
        AND length(CAST(limit_plan_ids_json AS BLOB)) <= 65536
    ),
    limit_periods_json TEXT NOT NULL DEFAULT '[]' CHECK (
        json_valid(limit_periods_json) AND json_type(limit_periods_json) = 'array'
        AND length(CAST(limit_periods_json AS BLOB)) <= 4096
    ),
    started_at INTEGER NOT NULL CHECK (started_at >= 0),
    ended_at INTEGER NOT NULL CHECK (ended_at > started_at),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX idx_coupons_created ON coupons(created_at DESC, id DESC);
CREATE INDEX idx_coupons_show_window ON coupons(show, started_at, ended_at, id);
CREATE INDEX idx_orders_coupon_user_status ON orders(coupon_id, user_id, status) WHERE coupon_id IS NOT NULL;

-- SQLite cannot add a foreign key to the v29 orders table without rebuilding it.
-- These triggers provide the same RESTRICT semantics for the coupon relationship
-- while keeping the upgrade atomic and avoiding a potentially long table copy.
CREATE TRIGGER trg_orders_coupon_insert
BEFORE INSERT ON orders
FOR EACH ROW
WHEN NEW.coupon_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM coupons WHERE id = NEW.coupon_id)
BEGIN
    SELECT RAISE(ABORT, 'orders.coupon_id references a missing coupon');
END;

CREATE TRIGGER trg_orders_coupon_update
BEFORE UPDATE OF coupon_id ON orders
FOR EACH ROW
WHEN NEW.coupon_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM coupons WHERE id = NEW.coupon_id)
BEGIN
    SELECT RAISE(ABORT, 'orders.coupon_id references a missing coupon');
END;

CREATE TRIGGER trg_coupons_delete_restrict
BEFORE DELETE ON coupons
FOR EACH ROW
WHEN EXISTS (SELECT 1 FROM orders WHERE coupon_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'coupon is referenced by an order');
END;
`

const schemaV31 = `
CREATE TABLE payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE CHECK (
        length(uuid) BETWEEN 8 AND 32 AND uuid NOT GLOB '*[^A-Za-z0-9]*'
    ),
    provider TEXT NOT NULL CHECK (provider IN ('AlipayF2F','BTCPay','CoinPayments','Coinbase','EPay','MGate')),
    name TEXT NOT NULL CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 255),
    icon TEXT CHECK (icon IS NULL OR length(CAST(icon AS BLOB)) BETWEEN 1 AND 2048),
    config_ciphertext BLOB NOT NULL CHECK (length(config_ciphertext) BETWEEN 1 AND 8192),
    notify_domain TEXT CHECK (notify_domain IS NULL OR length(CAST(notify_domain AS BLOB)) BETWEEN 1 AND 512),
    handling_fee_fixed INTEGER NOT NULL DEFAULT 0 CHECK (handling_fee_fixed BETWEEN 0 AND 9000000000000000),
    handling_fee_basis_points INTEGER NOT NULL DEFAULT 0 CHECK (handling_fee_basis_points BETWEEN 0 AND 10000),
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    sort_position INTEGER NOT NULL CHECK (sort_position BETWEEN 1 AND 1000000000),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX idx_payments_order ON payments(sort_position, id);
CREATE INDEX idx_payments_enabled ON payments(enabled, sort_position, id);
CREATE INDEX idx_payments_provider ON payments(provider, id);
CREATE INDEX idx_orders_payment_status ON orders(payment_id, status, id) WHERE payment_id IS NOT NULL;

CREATE TABLE payment_checkout_attempts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    payment_id INTEGER NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE CHECK (length(idempotency_key) BETWEEN 32 AND 128),
    expected_amount INTEGER NOT NULL CHECK (expected_amount BETWEEN 1 AND 9000000000000000),
    currency TEXT NOT NULL DEFAULT 'CNY' CHECK (length(currency) = 3 AND currency NOT GLOB '*[^A-Z]*'),
    status INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 2),
    external_id TEXT CHECK (external_id IS NULL OR length(CAST(external_id AS BLOB)) BETWEEN 1 AND 255),
    response_type INTEGER CHECK (response_type IS NULL OR response_type IN (0, 1)),
    response_data TEXT CHECK (response_data IS NULL OR length(CAST(response_data AS BLOB)) BETWEEN 1 AND 4096),
    error_code TEXT CHECK (error_code IS NULL OR length(error_code) BETWEEN 1 AND 64),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    UNIQUE (order_id, payment_id)
);
CREATE INDEX idx_payment_attempts_order ON payment_checkout_attempts(order_id, status, id);

CREATE TABLE payment_webhook_receipts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    payment_id INTEGER NOT NULL REFERENCES payments(id) ON DELETE RESTRICT,
    order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('AlipayF2F','BTCPay','CoinPayments','Coinbase','EPay','MGate')),
    external_id TEXT NOT NULL CHECK (length(CAST(external_id AS BLOB)) BETWEEN 1 AND 255),
    trade_no TEXT NOT NULL CHECK (length(trade_no) BETWEEN 1 AND 64),
    amount INTEGER NOT NULL CHECK (amount BETWEEN 1 AND 9000000000000000),
    currency TEXT NOT NULL CHECK (length(currency) = 3 AND currency NOT GLOB '*[^A-Z]*'),
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64 AND payload_sha256 NOT GLOB '*[^0-9a-f]*'),
    received_at INTEGER NOT NULL CHECK (received_at >= 0),
    UNIQUE (payment_id, external_id)
);
CREATE INDEX idx_payment_receipts_order ON payment_webhook_receipts(order_id, received_at DESC, id DESC);

CREATE TRIGGER trg_orders_payment_insert
BEFORE INSERT ON orders
FOR EACH ROW
WHEN NEW.payment_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM payments WHERE id = NEW.payment_id)
BEGIN
    SELECT RAISE(ABORT, 'orders.payment_id references a missing payment');
END;

CREATE TRIGGER trg_orders_payment_update
BEFORE UPDATE OF payment_id ON orders
FOR EACH ROW
WHEN NEW.payment_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM payments WHERE id = NEW.payment_id)
BEGIN
    SELECT RAISE(ABORT, 'orders.payment_id references a missing payment');
END;

CREATE TRIGGER trg_payments_delete_restrict
BEFORE DELETE ON payments
FOR EACH ROW
WHEN EXISTS (SELECT 1 FROM orders WHERE payment_id = OLD.id)
    OR EXISTS (SELECT 1 FROM payment_checkout_attempts WHERE payment_id = OLD.id)
    OR EXISTS (SELECT 1 FROM payment_webhook_receipts WHERE payment_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'payment is referenced by an order or receipt');
END;
`

const schemaV32 = `
CREATE TABLE gift_card_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(CAST(name AS BLOB)) BETWEEN 1 AND 255),
    description TEXT NOT NULL DEFAULT '' CHECK (length(CAST(description AS BLOB)) <= 4096),
    type INTEGER NOT NULL CHECK (type BETWEEN 1 AND 3),
    status INTEGER NOT NULL DEFAULT 1 CHECK (status IN (0, 1)),
    conditions_json TEXT NOT NULL DEFAULT '{}' CHECK (
        json_valid(conditions_json) AND json_type(conditions_json) = 'object'
        AND length(CAST(conditions_json AS BLOB)) <= 16384
    ),
    rewards_json TEXT NOT NULL CHECK (
        json_valid(rewards_json) AND json_type(rewards_json) = 'object'
        AND length(CAST(rewards_json AS BLOB)) <= 65536
    ),
    limits_json TEXT NOT NULL DEFAULT '{}' CHECK (
        json_valid(limits_json) AND json_type(limits_json) = 'object'
        AND length(CAST(limits_json AS BLOB)) <= 8192
    ),
    special_config_json TEXT NOT NULL DEFAULT '{}' CHECK (
        json_valid(special_config_json) AND json_type(special_config_json) = 'object'
        AND length(CAST(special_config_json AS BLOB)) <= 8192
    ),
    icon TEXT NOT NULL DEFAULT '' CHECK (length(CAST(icon AS BLOB)) <= 255),
    background_image TEXT NOT NULL DEFAULT '' CHECK (length(CAST(background_image AS BLOB)) <= 255),
    theme TEXT NOT NULL DEFAULT '#1890ff' CHECK (length(CAST(theme AS BLOB)) = 7),
    sort_position INTEGER NOT NULL DEFAULT 0 CHECK (sort_position BETWEEN 0 AND 1000000000),
    admin_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX idx_gift_templates_list ON gift_card_templates(sort_position, id);
CREATE INDEX idx_gift_templates_active ON gift_card_templates(status, type, sort_position, id);

CREATE TABLE gift_card_codes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    template_id INTEGER NOT NULL REFERENCES gift_card_templates(id) ON DELETE RESTRICT,
    code TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (
        length(code) BETWEEN 8 AND 32 AND code NOT GLOB '*[^A-Z0-9]*'
    ),
    batch_no TEXT NOT NULL CHECK (length(batch_no) BETWEEN 16 AND 40 AND batch_no NOT GLOB '*[^A-Za-z0-9_-]*'),
    status INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 3),
    user_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
    used_at INTEGER CHECK (used_at IS NULL OR used_at >= 0),
    expires_at INTEGER CHECK (expires_at IS NULL OR expires_at >= 0),
    actual_rewards_json TEXT CHECK (
        actual_rewards_json IS NULL OR (
            json_valid(actual_rewards_json) AND json_type(actual_rewards_json) = 'object'
            AND length(CAST(actual_rewards_json AS BLOB)) <= 65536
        )
    ),
    usage_count INTEGER NOT NULL DEFAULT 0 CHECK (usage_count >= 0),
    max_usage INTEGER NOT NULL DEFAULT 1 CHECK (max_usage BETWEEN 1 AND 1000000000),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (
        json_valid(metadata_json) AND json_type(metadata_json) = 'object'
        AND length(CAST(metadata_json AS BLOB)) <= 8192
    ),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    CHECK (usage_count <= max_usage)
);
CREATE INDEX idx_gift_codes_list ON gift_card_codes(created_at DESC, id DESC);
CREATE INDEX idx_gift_codes_template ON gift_card_codes(template_id, status, id);
CREATE INDEX idx_gift_codes_batch ON gift_card_codes(batch_no, id);
CREATE INDEX idx_gift_codes_user ON gift_card_codes(user_id, used_at DESC, id DESC) WHERE user_id IS NOT NULL;

CREATE TABLE gift_card_usages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    code_id INTEGER NOT NULL REFERENCES gift_card_codes(id) ON DELETE RESTRICT,
    template_id INTEGER NOT NULL REFERENCES gift_card_templates(id) ON DELETE RESTRICT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    inviter_id INTEGER REFERENCES users(id) ON DELETE RESTRICT,
    rewards_json TEXT NOT NULL CHECK (
        json_valid(rewards_json) AND json_type(rewards_json) = 'object'
        AND length(CAST(rewards_json AS BLOB)) <= 65536
    ),
    inviter_rewards_json TEXT NOT NULL DEFAULT '{}' CHECK (
        json_valid(inviter_rewards_json) AND json_type(inviter_rewards_json) = 'object'
        AND length(CAST(inviter_rewards_json AS BLOB)) <= 65536
    ),
    user_level_at_use INTEGER,
    user_plan_id INTEGER REFERENCES plans(id) ON DELETE RESTRICT,
    multiplier_basis_points INTEGER NOT NULL DEFAULT 10000 CHECK (multiplier_basis_points BETWEEN 0 AND 1000000),
    ip_address TEXT NOT NULL DEFAULT '' CHECK (length(ip_address) <= 45),
    user_agent TEXT NOT NULL DEFAULT '' CHECK (length(CAST(user_agent AS BLOB)) <= 1024),
    notes TEXT NOT NULL DEFAULT '' CHECK (length(CAST(notes AS BLOB)) <= 4096),
    traffic_reset_upload_before INTEGER CHECK (traffic_reset_upload_before IS NULL OR traffic_reset_upload_before >= 0),
    traffic_reset_download_before INTEGER CHECK (traffic_reset_download_before IS NULL OR traffic_reset_download_before >= 0),
    used_at INTEGER NOT NULL CHECK (used_at >= 0),
    CHECK ((traffic_reset_upload_before IS NULL) = (traffic_reset_download_before IS NULL))
);
CREATE INDEX idx_gift_usages_user ON gift_card_usages(user_id, used_at DESC, id DESC);
CREATE INDEX idx_gift_usages_code ON gift_card_usages(code_id, used_at DESC, id DESC);
CREATE INDEX idx_gift_usages_template ON gift_card_usages(template_id, used_at DESC, id DESC);
`

const schemaV33 = `
ALTER TABLE users ADD COLUMN is_staff INTEGER NOT NULL DEFAULT 0
    CHECK (is_staff IN (0, 1));
ALTER TABLE users ADD COLUMN is_distributor INTEGER NOT NULL DEFAULT 0
    CHECK (is_distributor IN (0, 1));
ALTER TABLE users ADD COLUMN distributor_name TEXT CHECK (
    (is_distributor = 0 AND distributor_name IS NULL)
    OR
    (is_distributor = 1 AND distributor_name IS NOT NULL
        AND distributor_name = trim(distributor_name)
        AND length(distributor_name) BETWEEN 1 AND 100)
);
CREATE INDEX idx_users_distributor
    ON users(account_kind, is_distributor, banned, distributor_name COLLATE NOCASE, id);

CREATE TABLE distributor_subscriptions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    original_order_id INTEGER NOT NULL UNIQUE REFERENCES orders(id) ON DELETE RESTRICT,
    distributor_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    subscriber_user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    customer_name TEXT CHECK (
        customer_name IS NULL
        OR (customer_name = trim(customer_name) AND length(customer_name) BETWEEN 1 AND 64)
    ),
    remark TEXT CHECK (
        remark IS NULL
        OR (remark = trim(remark) AND length(remark) BETWEEN 1 AND 500)
    ),
    claim_token_hash TEXT NOT NULL UNIQUE CHECK (
        length(claim_token_hash) = 64 AND claim_token_hash NOT GLOB '*[^0-9a-f]*'
    ),
    delivery_status INTEGER NOT NULL DEFAULT 0 CHECK (delivery_status BETWEEN 0 AND 2),
    config_issued_at INTEGER CHECK (config_issued_at IS NULL OR config_issued_at >= 0),
    connected_at INTEGER CHECK (connected_at IS NULL OR connected_at >= 0),
    connected_node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
    connected_node_name TEXT CHECK (
        connected_node_name IS NULL OR length(CAST(connected_node_name AS BLOB)) BETWEEN 1 AND 255
    ),
    claimed_at INTEGER CHECK (claimed_at IS NULL OR claimed_at >= 0),
    claim_ip TEXT CHECK (claim_ip IS NULL OR length(claim_ip) BETWEEN 1 AND 45),
    claim_user_agent TEXT CHECK (
        claim_user_agent IS NULL OR length(CAST(claim_user_agent AS BLOB)) BETWEEN 1 AND 255
    ),
    closed_at INTEGER CHECK (closed_at IS NULL OR closed_at >= 0),
    settlement_status INTEGER NOT NULL DEFAULT 0 CHECK (settlement_status IN (0, 1)),
    settled_at INTEGER CHECK (settled_at IS NULL OR settled_at >= 0),
    settled_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    hwid_enabled INTEGER NOT NULL DEFAULT 1 CHECK (hwid_enabled IN (0, 1)),
    hwid_limit INTEGER NOT NULL DEFAULT 1 CHECK (hwid_limit BETWEEN 1 AND 100),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    CHECK ((settlement_status = 0 AND settled_at IS NULL AND settled_by IS NULL)
        OR (settlement_status = 1 AND settled_at IS NOT NULL)),
    CHECK (claimed_at IS NOT NULL OR (claim_ip IS NULL AND claim_user_agent IS NULL))
);
CREATE INDEX idx_distributor_subscriptions_owner_settlement
    ON distributor_subscriptions(distributor_user_id, settlement_status, created_at DESC, id DESC);
CREATE INDEX idx_distributor_subscriptions_subscriber
    ON distributor_subscriptions(subscriber_user_id, id);
CREATE INDEX idx_distributor_subscriptions_delivery
    ON distributor_subscriptions(delivery_status, updated_at, id);

CREATE TABLE distributor_hwid_devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subscription_id INTEGER NOT NULL REFERENCES distributor_subscriptions(id) ON DELETE RESTRICT,
    hwid TEXT NOT NULL CHECK (length(hwid) BETWEEN 1 AND 128),
    device_os TEXT CHECK (device_os IS NULL OR length(device_os) BETWEEN 1 AND 100),
    os_version TEXT CHECK (os_version IS NULL OR length(os_version) BETWEEN 1 AND 100),
    device_model TEXT CHECK (device_model IS NULL OR length(device_model) BETWEEN 1 AND 150),
    user_agent TEXT CHECK (user_agent IS NULL OR length(CAST(user_agent AS BLOB)) BETWEEN 1 AND 255),
    ip_address TEXT CHECK (ip_address IS NULL OR length(ip_address) BETWEEN 1 AND 45),
    first_seen_at INTEGER NOT NULL CHECK (first_seen_at >= 0),
    last_seen_at INTEGER NOT NULL CHECK (last_seen_at >= first_seen_at),
    UNIQUE (subscription_id, hwid)
);
CREATE INDEX idx_distributor_hwid_last_seen
    ON distributor_hwid_devices(subscription_id, last_seen_at DESC, id DESC);

CREATE UNIQUE INDEX idx_orders_distributor_idempotency
    ON orders(user_id, distributor_idempotency_key)
    WHERE distributor_idempotency_key IS NOT NULL;
CREATE INDEX idx_orders_distributor_settlement
    ON orders(user_id, distributor_order_id, status, paid_at, created_at DESC, id DESC)
    WHERE distributor_order_id IS NOT NULL;

CREATE TRIGGER trg_distributor_subscriptions_insert_guard
BEFORE INSERT ON distributor_subscriptions
FOR EACH ROW
WHEN NOT EXISTS (
        SELECT 1 FROM users
        WHERE id = NEW.distributor_user_id
          AND account_kind = 'human' AND is_distributor = 1
    )
    OR NOT EXISTS (
        SELECT 1 FROM users
        WHERE id = NEW.subscriber_user_id AND account_kind = 'internal_subscription'
    )
    OR NOT EXISTS (
        SELECT 1 FROM orders
        WHERE id = NEW.original_order_id
          AND user_id = NEW.distributor_user_id AND type = 1 AND status = 3
    )
BEGIN
    SELECT RAISE(ABORT, 'invalid distributor subscription relationship');
END;

CREATE TRIGGER trg_distributor_subscriptions_update_guard
BEFORE UPDATE OF original_order_id, distributor_user_id, subscriber_user_id ON distributor_subscriptions
FOR EACH ROW
WHEN NOT EXISTS (
        SELECT 1 FROM users
        WHERE id = NEW.distributor_user_id
          AND account_kind = 'human' AND is_distributor = 1
    )
    OR NOT EXISTS (
        SELECT 1 FROM users
        WHERE id = NEW.subscriber_user_id AND account_kind = 'internal_subscription'
    )
    OR NOT EXISTS (
        SELECT 1 FROM orders
        WHERE id = NEW.original_order_id
          AND user_id = NEW.distributor_user_id AND type = 1 AND status = 3
    )
BEGIN
    SELECT RAISE(ABORT, 'invalid distributor subscription relationship');
END;

CREATE TRIGGER trg_orders_distributor_insert_guard
BEFORE INSERT ON orders
FOR EACH ROW
WHEN NEW.distributor_order_id IS NOT NULL
    AND NOT EXISTS (
        SELECT 1 FROM distributor_subscriptions
        WHERE id = NEW.distributor_order_id AND distributor_user_id = NEW.user_id
    )
BEGIN
    SELECT RAISE(ABORT, 'invalid distributor order owner');
END;

CREATE TRIGGER trg_orders_distributor_update_guard
BEFORE UPDATE OF distributor_order_id, user_id ON orders
FOR EACH ROW
WHEN NEW.distributor_order_id IS NOT NULL
    AND NOT EXISTS (
        SELECT 1 FROM distributor_subscriptions
        WHERE id = NEW.distributor_order_id AND distributor_user_id = NEW.user_id
    )
BEGIN
    SELECT RAISE(ABORT, 'invalid distributor order owner');
END;

CREATE TRIGGER trg_distributor_subscriptions_delete_restrict
BEFORE DELETE ON distributor_subscriptions
FOR EACH ROW
WHEN EXISTS (SELECT 1 FROM orders WHERE distributor_order_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'distributor subscription is referenced by an order');
END;
`

const schemaV34 = `
ALTER TABLE app_settings ADD COLUMN commission_auto_check_enable INTEGER NOT NULL DEFAULT 1
    CHECK (commission_auto_check_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN withdraw_close_enable INTEGER NOT NULL DEFAULT 0
    CHECK (withdraw_close_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN commission_distribution_enable INTEGER NOT NULL DEFAULT 0
    CHECK (commission_distribution_enable IN (0, 1));
ALTER TABLE app_settings ADD COLUMN commission_distribution_l1 INTEGER NOT NULL DEFAULT 100
    CHECK (commission_distribution_l1 BETWEEN 0 AND 100);
ALTER TABLE app_settings ADD COLUMN commission_distribution_l2 INTEGER NOT NULL DEFAULT 0
    CHECK (commission_distribution_l2 BETWEEN 0 AND 100);
ALTER TABLE app_settings ADD COLUMN commission_distribution_l3 INTEGER NOT NULL DEFAULT 0
    CHECK (commission_distribution_l3 BETWEEN 0 AND 100);

CREATE TABLE commission_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_id INTEGER NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    invite_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    trade_no TEXT NOT NULL CHECK (
        (length(trade_no) = 25 AND trade_no NOT GLOB '*[^0-9]*')
        OR
        (length(trade_no) = 32 AND trade_no NOT GLOB '*[^0-9a-f]*')
    ),
    order_amount INTEGER NOT NULL CHECK (order_amount BETWEEN 0 AND 9000000000000000),
    get_amount INTEGER NOT NULL CHECK (get_amount BETWEEN 1 AND 9000000000000000),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    UNIQUE (order_id, invite_user_id)
);
CREATE INDEX idx_commission_logs_owner_created
    ON commission_logs(invite_user_id, created_at DESC, id DESC);
CREATE INDEX idx_commission_logs_user
    ON commission_logs(user_id, created_at DESC, id DESC);
`

const schemaV35 = `
CREATE TABLE IF NOT EXISTS knowledge_attachments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE CHECK (
        length(uuid) = 36 AND uuid = lower(uuid) AND uuid NOT GLOB '*[^0-9a-f-]*'
        AND substr(uuid, 9, 1) = '-' AND substr(uuid, 14, 1) = '-'
        AND substr(uuid, 19, 1) = '-' AND substr(uuid, 24, 1) = '-'
		AND substr(uuid, 15, 1) IN ('1', '2', '3', '4', '5')
		AND substr(uuid, 20, 1) IN ('8', '9', 'a', 'b')
    ),
    knowledge_id INTEGER REFERENCES knowledge(id) ON DELETE SET NULL,
    uploader_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    draft_token_hash TEXT CHECK (
        draft_token_hash IS NULL OR (
            length(draft_token_hash) = 64 AND draft_token_hash NOT GLOB '*[^0-9a-f]*'
        )
    ),
    original_name TEXT NOT NULL CHECK (length(CAST(original_name AS BLOB)) BETWEEN 1 AND 1024),
    storage_path TEXT NOT NULL UNIQUE CHECK (
        length(CAST(storage_path AS BLOB)) BETWEEN 1 AND 512
        AND storage_path NOT LIKE '/%' AND instr(storage_path, char(92)) = 0
        AND storage_path NOT LIKE '%..%'
    ),
    mime_type TEXT NOT NULL DEFAULT 'application/octet-stream' CHECK (
        length(mime_type) BETWEEN 1 AND 191 AND mime_type = lower(mime_type)
    ),
    extension TEXT CHECK (
        extension IS NULL OR (
            length(extension) BETWEEN 1 AND 32 AND extension = lower(extension)
            AND extension NOT GLOB '*[^a-z0-9]*'
        )
    ),
    size INTEGER NOT NULL CHECK (size BETWEEN 1 AND 1099511627776),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    status TEXT NOT NULL CHECK (status IN ('quarantined', 'ready', 'rejected')),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    deleted_at INTEGER CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachments_article ON knowledge_attachments(knowledge_id, deleted_at, status, id);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachments_article_list ON knowledge_attachments(knowledge_id, deleted_at, id DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachments_draft ON knowledge_attachments(uploader_user_id, draft_token_hash, deleted_at, id DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachments_purge ON knowledge_attachments(deleted_at, id);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachments_stale_draft ON knowledge_attachments(knowledge_id, deleted_at, created_at, id);

CREATE TABLE IF NOT EXISTS knowledge_attachment_uploads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    uuid TEXT NOT NULL UNIQUE CHECK (
        length(uuid) = 36 AND uuid = lower(uuid) AND uuid NOT GLOB '*[^0-9a-f-]*'
        AND substr(uuid, 9, 1) = '-' AND substr(uuid, 14, 1) = '-'
        AND substr(uuid, 19, 1) = '-' AND substr(uuid, 24, 1) = '-'
		AND substr(uuid, 15, 1) IN ('1', '2', '3', '4', '5')
		AND substr(uuid, 20, 1) IN ('8', '9', 'a', 'b')
    ),
    uploader_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    draft_token_hash TEXT NOT NULL CHECK (
        length(draft_token_hash) = 64 AND draft_token_hash NOT GLOB '*[^0-9a-f]*'
    ),
    original_name TEXT NOT NULL CHECK (length(CAST(original_name AS BLOB)) BETWEEN 1 AND 1024),
    declared_size INTEGER NOT NULL CHECK (declared_size BETWEEN 1 AND 1099511627776),
    expected_sha256 TEXT CHECK (
        expected_sha256 IS NULL OR (
            length(expected_sha256) = 64 AND expected_sha256 NOT GLOB '*[^0-9a-f]*'
        )
    ),
    chunk_size INTEGER NOT NULL CHECK (chunk_size BETWEEN 1 AND 1073741824),
    total_chunks INTEGER NOT NULL CHECK (total_chunks BETWEEN 1 AND 1000000),
    received_chunks INTEGER NOT NULL DEFAULT 0 CHECK (received_chunks BETWEEN 0 AND total_chunks),
    temporary_path TEXT NOT NULL UNIQUE CHECK (
        length(CAST(temporary_path AS BLOB)) BETWEEN 1 AND 512
        AND temporary_path NOT LIKE '/%' AND instr(temporary_path, char(92)) = 0
        AND temporary_path NOT LIKE '%..%'
    ),
    status TEXT NOT NULL CHECK (status IN ('initialized', 'uploading', 'completing', 'completed', 'failed', 'expired')),
    expires_at INTEGER NOT NULL CHECK (expires_at >= 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at)
);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachment_uploads_owner ON knowledge_attachment_uploads(uploader_user_id, status, id DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_attachment_uploads_expiry ON knowledge_attachment_uploads(expires_at, id);

CREATE TABLE IF NOT EXISTS knowledge_attachment_chunks (
    upload_id INTEGER NOT NULL REFERENCES knowledge_attachment_uploads(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL CHECK (chunk_index BETWEEN 0 AND 999999),
    size INTEGER NOT NULL CHECK (size BETWEEN 1 AND 1073741824),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    PRIMARY KEY (upload_id, chunk_index)
) WITHOUT ROWID;
`

const schemaV36 = `
CREATE INDEX IF NOT EXISTS idx_orders_created
    ON orders(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_type_created
    ON orders(type, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_period_created
    ON orders(period, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_total_amount
    ON orders(total_amount, id);
CREATE INDEX IF NOT EXISTS idx_orders_status
    ON orders(status, id);
CREATE INDEX IF NOT EXISTS idx_orders_commission_balance
    ON orders(commission_balance, id);
CREATE INDEX IF NOT EXISTS idx_orders_commission_status
    ON orders(commission_status, id);
CREATE INDEX IF NOT EXISTS idx_orders_commission_status_created
    ON orders(commission_status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_orders_admin_filters
    ON orders(status, type, period, commission_status);
`

const schemaV37 = `
CREATE INDEX IF NOT EXISTS idx_users_directory_plan_id
    ON users(account_kind, plan_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_expired_at
    ON users(account_kind, expired_at, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_online_count
    ON users(account_kind, online_count, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_total_used
    ON users(account_kind, (traffic_u + traffic_d), id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_transfer_enable
    ON users(account_kind, transfer_enable, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_balance
    ON users(account_kind, balance, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_commission_balance
    ON users(account_kind, commission_balance, id DESC);
CREATE INDEX IF NOT EXISTS idx_users_directory_created_at
    ON users(account_kind, created_at, id DESC);
`

func applySchemaV37(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"telegram_id", `ALTER TABLE users ADD COLUMN telegram_id INTEGER CHECK (telegram_id IS NULL OR telegram_id > 0)`},
		{"remind_expire", `ALTER TABLE users ADD COLUMN remind_expire INTEGER NOT NULL DEFAULT 1 CHECK (remind_expire IN (0, 1))`},
		{"remind_traffic", `ALTER TABLE users ADD COLUMN remind_traffic INTEGER NOT NULL DEFAULT 1 CHECK (remind_traffic IN (0, 1))`},
		{"remarks", `ALTER TABLE users ADD COLUMN remarks TEXT CHECK (remarks IS NULL OR length(CAST(remarks AS BLOB)) <= 4096)`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('users') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect users.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add users.%s: %w", column.name, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, schemaV37); err != nil {
		return fmt.Errorf("add user directory indexes: %w", err)
	}
	return nil
}

const schemaV38 = `
DROP INDEX idx_traffic_reset_logs_user;
ALTER TABLE traffic_reset_logs RENAME TO traffic_reset_logs_v27;

CREATE TABLE traffic_reset_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id INTEGER REFERENCES plans(id) ON DELETE SET NULL,
    scheduled_for INTEGER CHECK (scheduled_for IS NULL OR scheduled_for >= 0),
    reset_at INTEGER NOT NULL CHECK (reset_at >= 0 AND (scheduled_for IS NULL OR reset_at >= scheduled_for)),
    upload_before INTEGER NOT NULL CHECK (upload_before >= 0),
    download_before INTEGER NOT NULL CHECK (download_before >= 0),
    upload_after INTEGER NOT NULL DEFAULT 0 CHECK (upload_after >= 0),
    download_after INTEGER NOT NULL DEFAULT 0 CHECK (download_after >= 0),
    reset_count INTEGER NOT NULL CHECK (reset_count > 0),
    trigger_source TEXT NOT NULL DEFAULT 'scheduled' CHECK (trigger_source IN ('scheduled', 'manual')),
    reason TEXT CHECK (reason IS NULL OR length(reason) <= 255),
    administrator_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    administrator_email TEXT CHECK (administrator_email IS NULL OR length(CAST(administrator_email AS BLOB)) BETWEEN 3 AND 320),
    idempotency_key TEXT CHECK (
        idempotency_key IS NULL OR (
            length(idempotency_key) BETWEEN 8 AND 128
            AND idempotency_key NOT GLOB '*[^A-Za-z0-9._:-]*'
        )
    ),
    CHECK (
        (trigger_source = 'scheduled' AND scheduled_for IS NOT NULL AND administrator_id IS NULL
            AND administrator_email IS NULL AND idempotency_key IS NULL)
        OR
        (trigger_source = 'manual' AND scheduled_for IS NULL AND administrator_email IS NOT NULL
            AND idempotency_key IS NOT NULL)
    )
);

INSERT INTO traffic_reset_logs (
    id, user_id, plan_id, scheduled_for, reset_at, upload_before, download_before,
    upload_after, download_after, reset_count, trigger_source
)
SELECT id, user_id, plan_id, scheduled_for, reset_at, upload_before, download_before,
       0, 0, reset_count, 'scheduled'
FROM traffic_reset_logs_v27;

DROP TABLE traffic_reset_logs_v27;
CREATE INDEX idx_traffic_reset_logs_user ON traffic_reset_logs(user_id, reset_at DESC, id DESC);
CREATE UNIQUE INDEX idx_traffic_reset_logs_scheduled
    ON traffic_reset_logs(user_id, scheduled_for) WHERE trigger_source = 'scheduled';
CREATE UNIQUE INDEX idx_traffic_reset_logs_manual_idempotency
    ON traffic_reset_logs(administrator_id, idempotency_key) WHERE trigger_source = 'manual';
CREATE INDEX IF NOT EXISTS idx_user_traffic_stats_user_record
    ON user_traffic_stats(user_id, record_at DESC, record_type DESC, rate_micros DESC);
`

const schemaV39 = `
CREATE TABLE admin_user_bulk_jobs (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 36 AND id GLOB '[0-9a-f]*-[0-9a-f]*-[0-9a-f]*-[0-9a-f]*-[0-9a-f]*'
    ),
    kind TEXT NOT NULL CHECK (kind IN ('mail', 'csv', 'ban')),
    scope TEXT NOT NULL CHECK (scope IN ('selected', 'filtered', 'all')),
    administrator_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    administrator_email TEXT NOT NULL CHECK (length(CAST(administrator_email AS BLOB)) BETWEEN 3 AND 320),
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'cancelling', 'cancelled', 'succeeded', 'failed')),
    request_digest TEXT NOT NULL CHECK (length(request_digest) = 64 AND request_digest NOT GLOB '*[^0-9a-f]*'),
    idempotency_key TEXT CHECK (
        idempotency_key IS NULL OR (
            length(idempotency_key) BETWEEN 8 AND 128
            AND idempotency_key NOT GLOB '*[^A-Za-z0-9._:-]*'
        )
    ),
    subject TEXT CHECK (subject IS NULL OR length(subject) BETWEEN 1 AND 255),
    content TEXT CHECK (content IS NULL OR length(CAST(content AS BLOB)) BETWEEN 1 AND 65536),
    app_name TEXT CHECK (app_name IS NULL OR length(app_name) BETWEEN 1 AND 100),
    app_url TEXT CHECK (app_url IS NULL OR length(CAST(app_url AS BLOB)) <= 2048),
    smtp_host TEXT CHECK (smtp_host IS NULL OR length(smtp_host) BETWEEN 1 AND 255),
    smtp_port INTEGER CHECK (smtp_port IS NULL OR smtp_port BETWEEN 1 AND 65535),
    smtp_username TEXT CHECK (smtp_username IS NULL OR length(CAST(smtp_username AS BLOB)) <= 320),
    smtp_password_cipher BLOB CHECK (smtp_password_cipher IS NULL OR length(smtp_password_cipher) <= 16384),
    smtp_encryption TEXT CHECK (smtp_encryption IS NULL OR smtp_encryption IN ('starttls', 'tls', 'none')),
    smtp_from_address TEXT CHECK (smtp_from_address IS NULL OR length(CAST(smtp_from_address AS BLOB)) BETWEEN 3 AND 320),
    total_count INTEGER NOT NULL DEFAULT 0 CHECK (total_count BETWEEN 0 AND 10000),
    processed_count INTEGER NOT NULL DEFAULT 0 CHECK (processed_count BETWEEN 0 AND total_count),
    success_count INTEGER NOT NULL DEFAULT 0 CHECK (success_count BETWEEN 0 AND processed_count),
    failure_count INTEGER NOT NULL DEFAULT 0 CHECK (failure_count BETWEEN 0 AND processed_count),
    skipped_count INTEGER NOT NULL DEFAULT 0 CHECK (skipped_count BETWEEN 0 AND processed_count),
    cancelled_count INTEGER NOT NULL DEFAULT 0 CHECK (cancelled_count BETWEEN 0 AND processed_count),
    output_filename TEXT CHECK (output_filename IS NULL OR length(CAST(output_filename AS BLOB)) BETWEEN 1 AND 255),
    output_relative_path TEXT CHECK (
        output_relative_path IS NULL OR (
            length(CAST(output_relative_path AS BLOB)) BETWEEN 1 AND 255
            AND output_relative_path NOT LIKE '/%' AND instr(output_relative_path, char(92)) = 0
            AND output_relative_path NOT LIKE '%..%'
        )
    ),
    output_size INTEGER CHECK (output_size IS NULL OR output_size BETWEEN 0 AND 33554432),
    output_sha256 TEXT CHECK (
        output_sha256 IS NULL OR (length(output_sha256) = 64 AND output_sha256 NOT GLOB '*[^0-9a-f]*')
    ),
    output_expires_at INTEGER CHECK (output_expires_at IS NULL OR output_expires_at >= 0),
    claim_token TEXT CHECK (claim_token IS NULL OR length(CAST(claim_token AS BLOB)) BETWEEN 8 AND 128),
    claimed_at INTEGER CHECK (claimed_at IS NULL OR claimed_at >= 0),
    last_error TEXT CHECK (last_error IS NULL OR length(CAST(last_error AS BLOB)) <= 2048),
    started_at INTEGER CHECK (started_at IS NULL OR started_at >= 0),
    completed_at INTEGER CHECK (completed_at IS NULL OR completed_at >= 0),
    cancelled_at INTEGER CHECK (cancelled_at IS NULL OR cancelled_at >= 0),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= created_at),
    CHECK (processed_count = success_count + failure_count + skipped_count + cancelled_count),
    CHECK ((kind = 'mail' AND subject IS NOT NULL AND content IS NOT NULL AND app_name IS NOT NULL
            AND smtp_host IS NOT NULL AND smtp_port IS NOT NULL AND smtp_encryption IS NOT NULL
            AND smtp_from_address IS NOT NULL)
        OR (kind IN ('csv', 'ban') AND subject IS NULL AND content IS NULL)),
    CHECK ((claim_token IS NULL) = (claimed_at IS NULL))
);
CREATE INDEX idx_admin_user_bulk_jobs_list
    ON admin_user_bulk_jobs(created_at DESC, id DESC);
CREATE INDEX idx_admin_user_bulk_jobs_claim
    ON admin_user_bulk_jobs(kind, status, claimed_at, created_at);
CREATE UNIQUE INDEX idx_admin_user_bulk_jobs_ban_idempotency
    ON admin_user_bulk_jobs(administrator_id, idempotency_key)
    WHERE kind = 'ban' AND idempotency_key IS NOT NULL;

CREATE TABLE admin_user_bulk_targets (
    job_id TEXT NOT NULL REFERENCES admin_user_bulk_jobs(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    email TEXT NOT NULL CHECK (length(CAST(email AS BLOB)) BETWEEN 3 AND 320),
    uuid TEXT NOT NULL CHECK (length(CAST(uuid AS BLOB)) <= 128),
    plan_name TEXT NOT NULL CHECK (length(CAST(plan_name AS BLOB)) <= 255),
    group_id INTEGER REFERENCES server_groups(id) ON DELETE SET NULL,
    expired_at INTEGER CHECK (expired_at IS NULL OR expired_at >= 0),
    transfer_enable INTEGER NOT NULL CHECK (transfer_enable >= 0),
    transfer_used INTEGER NOT NULL CHECK (transfer_used >= 0),
    balance INTEGER NOT NULL CHECK (balance >= 0),
    commission_balance INTEGER NOT NULL CHECK (commission_balance >= 0),
    subscription_token TEXT NOT NULL CHECK (length(CAST(subscription_token AS BLOB)) BETWEEN 1 AND 255),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'succeeded', 'failed', 'skipped', 'cancelled')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    available_at INTEGER NOT NULL CHECK (available_at >= 0),
    claim_token TEXT CHECK (claim_token IS NULL OR length(CAST(claim_token AS BLOB)) BETWEEN 8 AND 128),
    claimed_at INTEGER CHECK (claimed_at IS NULL OR claimed_at >= 0),
    last_error TEXT CHECK (last_error IS NULL OR length(CAST(last_error AS BLOB)) <= 2048),
    processed_at INTEGER CHECK (processed_at IS NULL OR processed_at >= 0),
    PRIMARY KEY (job_id, sequence),
    UNIQUE (job_id, user_id),
    CHECK ((status = 'processing') = (claim_token IS NOT NULL AND claimed_at IS NOT NULL))
) WITHOUT ROWID;
CREATE INDEX idx_admin_user_bulk_targets_claim
    ON admin_user_bulk_targets(status, available_at, claimed_at, job_id, sequence);
CREATE INDEX idx_admin_user_bulk_targets_job_status
    ON admin_user_bulk_targets(job_id, status, sequence);
`

const schemaV40 = `
ALTER TABLE nodes ADD COLUMN admin_revision INTEGER NOT NULL DEFAULT 1 CHECK (admin_revision > 0);
CREATE INDEX idx_nodes_admin_sort ON nodes(sort, id);
CREATE INDEX idx_nodes_admin_type_sort ON nodes(type, sort, id);
`

const schemaV41 = `
ALTER TABLE node_protocol_definitions ADD COLUMN listen_address TEXT NOT NULL DEFAULT '0.0.0.0'
    CHECK (length(listen_address) BETWEEN 2 AND 45);
`

const schemaV43 = `
CREATE TABLE node_agent_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    server_token_hash TEXT CHECK (
        server_token_hash IS NULL OR (
            length(server_token_hash) = 64
            AND server_token_hash NOT GLOB '*[^0-9a-f]*'
        )
    ),
    server_token_prefix TEXT NOT NULL DEFAULT '' CHECK (length(server_token_prefix) <= 8),
    pull_interval INTEGER NOT NULL CHECK (pull_interval BETWEEN 1 AND 3600),
    push_interval INTEGER NOT NULL CHECK (push_interval BETWEEN 1 AND 3600),
    device_limit_mode INTEGER NOT NULL CHECK (device_limit_mode IN (0, 1)),
    websocket_enabled INTEGER NOT NULL CHECK (websocket_enabled IN (0, 1)),
    websocket_url TEXT NOT NULL DEFAULT '' CHECK (length(CAST(websocket_url AS BLOB)) <= 2048),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0),
    CHECK (
        (server_token_hash IS NULL AND server_token_prefix = '') OR
        (server_token_hash IS NOT NULL AND length(server_token_prefix) BETWEEN 1 AND 8)
    )
);
`

const schemaV46 = `
CREATE TABLE IF NOT EXISTS subscription_reminder_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('expire', 'traffic')),
    reminder_day TEXT NOT NULL CHECK (length(reminder_day) = 10),
    recipient TEXT NOT NULL CHECK (length(recipient) BETWEEN 3 AND 320),
    app_name TEXT NOT NULL CHECK (length(app_name) BETWEEN 1 AND 100),
    app_url TEXT NOT NULL DEFAULT '' CHECK (length(app_url) <= 2048),
    available_at INTEGER NOT NULL CHECK (available_at >= 0),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count BETWEEN 0 AND 3),
    claim_token TEXT CHECK (claim_token IS NULL OR length(claim_token) BETWEEN 1 AND 128),
    claimed_at INTEGER CHECK (claimed_at IS NULL OR claimed_at >= 0),
    sent_at INTEGER CHECK (sent_at IS NULL OR sent_at >= 0),
    failed_at INTEGER CHECK (failed_at IS NULL OR failed_at >= 0),
    cancelled_at INTEGER CHECK (cancelled_at IS NULL OR cancelled_at >= 0),
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 1024),
    created_at INTEGER NOT NULL CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0),
    UNIQUE(user_id, kind, reminder_day),
    CHECK ((claim_token IS NULL) = (claimed_at IS NULL)),
    CHECK (
        (sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL) OR
        (sent_at IS NOT NULL AND failed_at IS NULL AND cancelled_at IS NULL) OR
        (sent_at IS NULL AND failed_at IS NOT NULL AND cancelled_at IS NULL) OR
        (sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NOT NULL)
    )
);
CREATE INDEX IF NOT EXISTS idx_subscription_reminder_due
    ON subscription_reminder_outbox(available_at, id)
    WHERE sent_at IS NULL AND failed_at IS NULL AND cancelled_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_subscription_reminder_failed
    ON subscription_reminder_outbox(failed_at DESC, id DESC)
    WHERE failed_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_reminder_expire
    ON users(expired_at, id)
    WHERE banned = 0 AND remind_expire = 1 AND email <> '' AND expired_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_reminder_traffic
    ON users(id)
    WHERE banned = 0 AND remind_traffic = 1 AND email <> '' AND transfer_enable > 0;
`

func applySchemaV46(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = 'remind_mail_enable')
	`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect app_settings.remind_mail_enable: %w", err)
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `
			ALTER TABLE app_settings ADD COLUMN remind_mail_enable INTEGER NOT NULL DEFAULT 0
				CHECK (remind_mail_enable IN (0, 1))
		`); err != nil {
			return fmt.Errorf("add app_settings.remind_mail_enable: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, schemaV46); err != nil {
		return err
	}
	return nil
}

const schemaV47 = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_unique_telegram_id
    ON users(telegram_id) WHERE telegram_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS telegram_webhook_updates (
    update_id INTEGER PRIMARY KEY CHECK (update_id > 0),
    claim_id TEXT NOT NULL CHECK (length(claim_id) = 32 AND claim_id NOT GLOB '*[^0-9a-f]*'),
    completed INTEGER NOT NULL DEFAULT 0 CHECK (completed IN (0, 1)),
    updated_at INTEGER NOT NULL CHECK (updated_at >= 0)
) STRICT;

CREATE INDEX IF NOT EXISTS idx_telegram_webhook_updates_cleanup
    ON telegram_webhook_updates(updated_at, update_id);
`

const schemaV48 = `
CREATE TABLE IF NOT EXISTS mail_templates (
    name TEXT PRIMARY KEY CHECK (name IN ('verify', 'notify', 'remindExpire', 'remindTraffic', 'mailLogin')),
    subject TEXT,
    content TEXT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0),
    CHECK ((subject IS NULL) = (content IS NULL)),
    CHECK (subject IS NULL OR (length(subject) > 0 AND length(CAST(subject AS BLOB)) <= 1024)),
    CHECK (content IS NULL OR (length(content) > 0 AND length(CAST(content AS BLOB)) <= 262144))
) STRICT;

INSERT OR IGNORE INTO mail_templates(name) VALUES
    ('verify'), ('notify'), ('remindExpire'), ('remindTraffic'), ('mailLogin');
`

const schemaV49 = `
CREATE TABLE IF NOT EXISTS client_app_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    windows_version TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(windows_version AS BLOB)) <= 128 AND instr(windows_version, char(0)) = 0
    ),
    windows_download_url TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(windows_download_url AS BLOB)) <= 2048
        AND instr(windows_download_url, char(0)) = 0
        AND (windows_download_url = '' OR lower(substr(windows_download_url, 1, 8)) = 'https://')
    ),
    macos_version TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(macos_version AS BLOB)) <= 128 AND instr(macos_version, char(0)) = 0
    ),
    macos_download_url TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(macos_download_url AS BLOB)) <= 2048
        AND instr(macos_download_url, char(0)) = 0
        AND (macos_download_url = '' OR lower(substr(macos_download_url, 1, 8)) = 'https://')
    ),
    android_version TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(android_version AS BLOB)) <= 128 AND instr(android_version, char(0)) = 0
    ),
    android_download_url TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(android_download_url AS BLOB)) <= 2048
        AND instr(android_download_url, char(0)) = 0
        AND (android_download_url = '' OR lower(substr(android_download_url, 1, 8)) = 'https://')
    ),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0)
) STRICT;

INSERT OR IGNORE INTO client_app_settings(id) VALUES (1);
`

const schemaV51 = `
CREATE TABLE IF NOT EXISTS themes (
    name TEXT PRIMARY KEY COLLATE NOCASE CHECK (
        length(name) BETWEEN 1 AND 64
        AND name NOT GLOB '*[^A-Za-z0-9._-]*'
    ),
    description TEXT NOT NULL DEFAULT '' CHECK (
        length(CAST(description AS BLOB)) <= 512 AND instr(description, char(0)) = 0
    ),
    version TEXT NOT NULL CHECK (
        length(version) BETWEEN 5 AND 32 AND version NOT GLOB '*[^0-9.]*'
    ),
    manifest_json TEXT NOT NULL CHECK (
        length(CAST(manifest_json AS BLOB)) BETWEEN 2 AND 262144 AND json_valid(manifest_json)
    ),
    config_json TEXT NOT NULL CHECK (
        length(CAST(config_json AS BLOB)) BETWEEN 2 AND 8192 AND json_valid(config_json)
    ),
    package_sha256 TEXT NOT NULL CHECK (
        length(package_sha256) = 64 AND package_sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    is_system INTEGER NOT NULL DEFAULT 0 CHECK (is_system IN (0, 1)),
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at INTEGER NOT NULL DEFAULT 0 CHECK (created_at >= 0),
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0)
) STRICT;

CREATE TABLE IF NOT EXISTS theme_assets (
    theme_name TEXT NOT NULL COLLATE NOCASE REFERENCES themes(name) ON UPDATE CASCADE ON DELETE CASCADE,
    path TEXT NOT NULL CHECK (
        length(CAST(path AS BLOB)) BETWEEN 1 AND 512
        AND instr(path, char(0)) = 0 AND instr(path, char(92)) = 0
        AND substr(path, 1, 1) <> '/'
    ),
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/png', 'image/jpeg', 'image/gif')),
    size INTEGER NOT NULL CHECK (size BETWEEN 1 AND 8388608),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0 AND width * height <= 20000000),
    body BLOB NOT NULL CHECK (length(body) = size),
    PRIMARY KEY(theme_name, path)
) STRICT, WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS theme_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    active_theme TEXT NOT NULL COLLATE NOCASE REFERENCES themes(name) ON UPDATE CASCADE ON DELETE RESTRICT,
    revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    updated_at INTEGER NOT NULL DEFAULT 0 CHECK (updated_at >= 0)
) STRICT;

CREATE TRIGGER IF NOT EXISTS themes_protect_system_delete
BEFORE DELETE ON themes WHEN OLD.is_system = 1
BEGIN
    SELECT RAISE(ABORT, 'system theme cannot be deleted');
END;

CREATE TRIGGER IF NOT EXISTS themes_protect_identity
BEFORE UPDATE OF name, is_system ON themes
WHEN OLD.is_system = 1 OR NEW.is_system <> OLD.is_system
BEGIN
    SELECT RAISE(ABORT, 'theme identity cannot be changed');
END;

INSERT OR IGNORE INTO themes (
    name, description, version, manifest_json, config_json, package_sha256, revision, is_system
) VALUES (
    'Xboard', 'Xboard built-in safe theme', '1.0.0',
    '{"format_version":1,"name":"Xboard","description":"Xboard built-in safe theme","version":"1.0.0","images":[],"backgrounds":[],"palettes":{"default":{"background":"#0b0d12","surface":"#151922","text":"#e8ebf2","muted":"#9ba3b5","primary":"#9ab2ff","primary_text":"#101218","border":"#303746"},"blue":{"background":"#0c1426","surface":"#14213a","text":"#e8efff","muted":"#9cabc5","primary":"#8fb5ff","primary_text":"#0a1020","border":"#30466d"},"black":{"background":"#050505","surface":"#111111","text":"#f5f5f5","muted":"#a3a3a3","primary":"#d4d4d4","primary_text":"#0a0a0a","border":"#333333"},"darkblue":{"background":"#07111f","surface":"#0d1b2d","text":"#e5f0ff","muted":"#94a8c3","primary":"#82b7ff","primary_text":"#07111f","border":"#294462"}},"default_config":{"theme_color":"default","background_url":"","font_scale":"normal","radius":"rounded"}}',
    '{"theme_color":"default","background_url":"","font_scale":"normal","radius":"rounded"}',
    '0000000000000000000000000000000000000000000000000000000000000000', 1, 1
);

INSERT OR IGNORE INTO theme_settings(id, active_theme) VALUES (1, 'Xboard');
`

func applySchemaV52(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"force_https", `ALTER TABLE app_settings ADD COLUMN force_https INTEGER NOT NULL DEFAULT 0 CHECK (force_https IN (0, 1))`},
		{"subscribe_url", `ALTER TABLE app_settings ADD COLUMN subscribe_url TEXT NOT NULL DEFAULT '' CHECK (length(CAST(subscribe_url AS BLOB)) <= 8192 AND instr(subscribe_url, char(0)) = 0)`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add app_settings.%s: %w", column.name, err)
			}
		}
	}
	return nil
}

func applySchemaV53(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"safe_mode_enable", `ALTER TABLE app_settings ADD COLUMN safe_mode_enable INTEGER NOT NULL DEFAULT 0 CHECK (safe_mode_enable IN (0, 1))`},
		{"secure_path", `ALTER TABLE app_settings ADD COLUMN secure_path TEXT NOT NULL DEFAULT '' CHECK (length(CAST(secure_path AS BLOB)) <= 64 AND instr(secure_path, char(0)) = 0 AND secure_path NOT GLOB '*[^0-9A-Za-z_-]*')`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add app_settings.%s: %w", column.name, err)
			}
		}
	}
	return nil
}

func applySchemaV50(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"currency", `ALTER TABLE app_settings ADD COLUMN currency TEXT NOT NULL DEFAULT 'CNY' CHECK (length(currency) = 3 AND currency NOT GLOB '*[^A-Z]*')`},
		{"currency_symbol", `ALTER TABLE app_settings ADD COLUMN currency_symbol TEXT NOT NULL DEFAULT '¥' CHECK (length(CAST(currency_symbol AS BLOB)) <= 16 AND instr(currency_symbol, char(0)) = 0)`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add app_settings.%s: %w", column.name, err)
			}
		}
	}
	return nil
}

func applySchemaV47(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"telegram_bot_enable", `ALTER TABLE app_settings ADD COLUMN telegram_bot_enable INTEGER NOT NULL DEFAULT 0 CHECK (telegram_bot_enable IN (0, 1))`},
		{"telegram_bot_token_cipher", `ALTER TABLE app_settings ADD COLUMN telegram_bot_token_cipher BLOB CHECK (telegram_bot_token_cipher IS NULL OR length(telegram_bot_token_cipher) BETWEEN 33 AND 8192)`},
		{"telegram_webhook_url", `ALTER TABLE app_settings ADD COLUMN telegram_webhook_url TEXT NOT NULL DEFAULT '' CHECK (length(CAST(telegram_webhook_url AS BLOB)) <= 2048)`},
		{"telegram_discuss_link", `ALTER TABLE app_settings ADD COLUMN telegram_discuss_link TEXT NOT NULL DEFAULT '' CHECK (length(CAST(telegram_discuss_link AS BLOB)) <= 2048)`},
		{"telegram_webhook_secret_cipher", `ALTER TABLE app_settings ADD COLUMN telegram_webhook_secret_cipher BLOB CHECK (telegram_webhook_secret_cipher IS NULL OR length(telegram_webhook_secret_cipher) BETWEEN 33 AND 8192)`},
		{"telegram_webhook_pending_secret_cipher", `ALTER TABLE app_settings ADD COLUMN telegram_webhook_pending_secret_cipher BLOB CHECK (telegram_webhook_pending_secret_cipher IS NULL OR length(telegram_webhook_pending_secret_cipher) BETWEEN 33 AND 8192)`},
		{"telegram_webhook_provision_id", `ALTER TABLE app_settings ADD COLUMN telegram_webhook_provision_id TEXT CHECK (telegram_webhook_provision_id IS NULL OR (length(telegram_webhook_provision_id) = 32 AND telegram_webhook_provision_id NOT GLOB '*[^0-9a-f]*'))`},
		{"telegram_bot_username", `ALTER TABLE app_settings ADD COLUMN telegram_bot_username TEXT NOT NULL DEFAULT '' CHECK (length(CAST(telegram_bot_username AS BLOB)) <= 64)`},
		{"telegram_webhook_configured_at", `ALTER TABLE app_settings ADD COLUMN telegram_webhook_configured_at INTEGER CHECK (telegram_webhook_configured_at IS NULL OR telegram_webhook_configured_at >= 0)`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add app_settings.%s: %w", column.name, err)
			}
		}
	}
	var duplicateTelegramID int64
	err := tx.QueryRowContext(ctx, `
		SELECT telegram_id FROM users WHERE telegram_id IS NOT NULL
		GROUP BY telegram_id HAVING COUNT(*) > 1 LIMIT 1
	`).Scan(&duplicateTelegramID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect duplicate Telegram user ids: %w", err)
	}
	if err == nil {
		return fmt.Errorf("users contain duplicate Telegram id %d", duplicateTelegramID)
	}
	if _, err := tx.ExecContext(ctx, schemaV47); err != nil {
		return fmt.Errorf("add Telegram settings indexes: %w", err)
	}
	if err := validateTelegramIDIndex(ctx, tx); err != nil {
		return err
	}
	return nil
}

func applySchemaV45(ctx context.Context, tx *sql.Tx) error {
	columns := []struct {
		name string
		ddl  string
	}{
		{"default_remind_expire", `ALTER TABLE app_settings ADD COLUMN default_remind_expire INTEGER NOT NULL DEFAULT 1 CHECK (default_remind_expire IN (0, 1))`},
		{"default_remind_traffic", `ALTER TABLE app_settings ADD COLUMN default_remind_traffic INTEGER NOT NULL DEFAULT 1 CHECK (default_remind_traffic IN (0, 1))`},
	}
	for _, column := range columns {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if !exists {
			if _, err := tx.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("add app_settings.%s: %w", column.name, err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET traffic_reset_method = 0
		WHERE id = 1 AND revision = 1 AND updated_at = 0 AND traffic_reset_method = 1
	`); err != nil {
		return fmt.Errorf("align pristine traffic reset method: %w", err)
	}
	return nil
}

func applySchemaV44(ctx context.Context, tx *sql.Tx) error {
	for _, column := range []struct {
		name      string
		statement string
	}{
		{"try_out_plan_id", `ALTER TABLE app_settings ADD COLUMN try_out_plan_id INTEGER NOT NULL DEFAULT 0 CHECK (try_out_plan_id >= 0)`},
		{"try_out_hour", `ALTER TABLE app_settings ADD COLUMN try_out_hour INTEGER NOT NULL DEFAULT 1 CHECK (try_out_hour BETWEEN 1 AND 8760)`},
	} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pragma_table_info('app_settings') WHERE name = ?)`, column.name).Scan(&exists); err != nil {
			return fmt.Errorf("inspect app_settings.%s: %w", column.name, err)
		}
		if exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, column.statement); err != nil {
			return err
		}
	}
	return nil
}

func applySchemaV41(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM pragma_table_info('node_protocol_definitions') WHERE name = 'listen_address')
	`).Scan(&exists); err != nil {
		return fmt.Errorf("inspect node_protocol_definitions.listen_address: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, schemaV41); err != nil {
		return fmt.Errorf("add node_protocol_definitions.listen_address: %w", err)
	}
	return nil
}
