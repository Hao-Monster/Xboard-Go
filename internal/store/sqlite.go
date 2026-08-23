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

	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("apply schema v1: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
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
