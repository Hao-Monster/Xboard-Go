package gen

import (
	"context"
	"database/sql"
)

// representativeLegacySchema is the smallest schema that can exercise the
// implemented migration dependency chain with realistic relationships. The
// D-013 traffic table is present only because the node reader requires the
// historical table shape; populateRepresentativeDomains deliberately leaves
// it empty and the manifest never counts it as migrated data.
const representativeLegacySchema = `
PRAGMA user_version = 22;

CREATE TABLE v2_settings (
    id INTEGER PRIMARY KEY,
    "group" TEXT,
    type TEXT,
    name TEXT NOT NULL UNIQUE,
    value TEXT,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE v2_notice (
    id INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    show INTEGER NOT NULL,
    img_url TEXT,
    tags TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    sort INTEGER
);
CREATE TABLE v2_server_group (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE v2_server_route (
    id INTEGER PRIMARY KEY,
    remarks TEXT NOT NULL,
    match TEXT NOT NULL,
    action TEXT NOT NULL,
    action_value TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE v2_plan (
    id INTEGER PRIMARY KEY,
    group_id INTEGER,
    transfer_enable INTEGER,
    name TEXT,
    speed_limit INTEGER,
    show INTEGER,
    sort INTEGER,
    renew INTEGER,
    content TEXT,
    reset_traffic_method INTEGER,
    capacity_limit INTEGER,
    created_at,
    updated_at,
    prices TEXT,
    sell INTEGER,
    device_limit INTEGER,
    tags TEXT
);
CREATE TABLE v2_user (
    id INTEGER PRIMARY KEY,
    invite_user_id INTEGER,
    telegram_id INTEGER,
    email TEXT NOT NULL,
    password TEXT NOT NULL,
    password_algo TEXT,
    password_salt TEXT,
    balance INTEGER NOT NULL DEFAULT 0,
    discount REAL,
    commission_type INTEGER NOT NULL DEFAULT 0,
    commission_rate REAL,
    commission_balance INTEGER NOT NULL DEFAULT 0,
    t INTEGER NOT NULL DEFAULT 0,
    u INTEGER NOT NULL DEFAULT 0,
    d INTEGER NOT NULL DEFAULT 0,
    transfer_enable INTEGER NOT NULL DEFAULT 0,
    banned INTEGER NOT NULL DEFAULT 0,
    is_admin INTEGER NOT NULL DEFAULT 0,
    last_login_at INTEGER,
    is_staff INTEGER NOT NULL DEFAULT 0,
    last_login_ip TEXT,
    uuid TEXT NOT NULL,
    group_id INTEGER,
    plan_id INTEGER,
    speed_limit INTEGER,
    remind_expire INTEGER NOT NULL DEFAULT 1,
    remind_traffic INTEGER NOT NULL DEFAULT 1,
    token TEXT NOT NULL,
    expired_at INTEGER,
    remarks TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    device_limit INTEGER,
    online_count INTEGER,
    last_online_at INTEGER,
    next_reset_at INTEGER,
    last_reset_at INTEGER,
    reset_count INTEGER NOT NULL DEFAULT 0,
    is_distributor INTEGER NOT NULL DEFAULT 0,
    distributor_name TEXT
);
CREATE TABLE personal_access_tokens (
    id INTEGER PRIMARY KEY,
    tokenable_type TEXT NOT NULL,
    tokenable_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    token TEXT NOT NULL,
    abilities TEXT,
    last_used_at DATETIME,
    expires_at DATETIME,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE v2_invite_code (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    code TEXT NOT NULL,
    status INTEGER NOT NULL DEFAULT 0,
    pv INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE v2_coupon (
    id INTEGER PRIMARY KEY,
    code TEXT,
    name TEXT,
    type INTEGER,
    value INTEGER,
    show INTEGER,
    limit_use INTEGER,
    limit_use_with_user INTEGER,
    limit_plan_ids TEXT,
    limit_period TEXT,
    started_at INTEGER,
    ended_at INTEGER,
    created_at INTEGER,
    updated_at INTEGER
);
CREATE TABLE v2_payment (
    id INTEGER PRIMARY KEY,
    uuid TEXT,
    payment TEXT,
    name TEXT,
    icon TEXT,
    config TEXT,
    notify_domain TEXT,
    handling_fee_fixed INTEGER,
    handling_fee_percent NUMERIC,
    enable INTEGER,
    sort INTEGER,
    created_at INTEGER,
    updated_at INTEGER
);
CREATE TABLE v2_order (
    id INTEGER PRIMARY KEY,
    invite_user_id INTEGER,
    user_id INTEGER NOT NULL,
    plan_id INTEGER NOT NULL,
    coupon_id INTEGER,
    payment_id INTEGER,
    type INTEGER NOT NULL,
    period TEXT NOT NULL,
    trade_no TEXT NOT NULL,
    callback_no TEXT,
    total_amount INTEGER NOT NULL,
    handling_amount INTEGER,
    discount_amount INTEGER,
    surplus_amount INTEGER,
    surplus_credit INTEGER,
    balance_amount INTEGER,
    surplus_order_ids TEXT,
    status INTEGER NOT NULL,
    commission_status INTEGER,
    commission_balance INTEGER,
    actual_commission_balance INTEGER,
    paid_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    distributor_order_id INTEGER,
    entitlement_expired_at_before INTEGER,
    entitlement_expired_at_after INTEGER,
    distributor_idempotency_key TEXT,
    distributor_settled_by INTEGER
);
CREATE TABLE v2_ticket (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    subject TEXT NOT NULL,
    level INTEGER NOT NULL,
    status INTEGER NOT NULL,
    reply_status INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_reply_user_id INTEGER
);
CREATE TABLE v2_ticket_message (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    ticket_id INTEGER NOT NULL,
    message TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE v2_server_machine (
    id INTEGER PRIMARY KEY,
    name TEXT,
    token TEXT,
    notes TEXT,
    is_active INTEGER,
    last_seen_at INTEGER,
    load_status TEXT,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE v2_server_machine_credential (
    id INTEGER PRIMARY KEY,
    machine_id INTEGER,
    token_hash TEXT,
    token_prefix TEXT,
    last_used_at INTEGER,
    revoked_at INTEGER,
    created_at DATETIME
);
CREATE TABLE v2_server_machine_enrollment (
    id INTEGER PRIMARY KEY,
    machine_id INTEGER,
    code_hash TEXT,
    revoke_existing INTEGER,
    expires_at INTEGER,
    consumed_at INTEGER,
    created_at DATETIME
);
CREATE TABLE v2_server_machine_load_history (
    id INTEGER PRIMARY KEY,
    machine_id INTEGER,
    cpu REAL,
    mem_total INTEGER,
    mem_used INTEGER,
    disk_total INTEGER,
    disk_used INTEGER,
    net_in_speed REAL,
    net_out_speed REAL,
    recorded_at INTEGER
);
CREATE TABLE v2_server (
    id INTEGER PRIMARY KEY,
    type TEXT,
    code TEXT,
    parent_id INTEGER,
    group_ids TEXT,
    route_ids TEXT,
    name TEXT,
    rate NUMERIC,
    tags TEXT,
    host TEXT,
    port TEXT,
    server_port INTEGER,
    protocol_settings TEXT,
    show INTEGER,
    sort INTEGER,
    created_at DATETIME,
    updated_at DATETIME,
    rate_time_enable INTEGER,
    rate_time_ranges TEXT,
    custom_outbounds TEXT,
    custom_routes TEXT,
    cert_config TEXT,
    transfer_enable INTEGER,
    u INTEGER,
    d INTEGER,
    machine_id INTEGER,
    enabled INTEGER
);
CREATE TABLE v2_server_activation_schedule (
    id INTEGER PRIMARY KEY,
    server_id INTEGER,
    schedule_type TEXT,
    timezone TEXT,
    enable_second INTEGER,
    disable_second INTEGER,
    enable_at INTEGER,
    disable_at INTEGER,
    revision TEXT,
    next_transition_at INTEGER,
    next_target_enabled INTEGER,
    enabled_applied_at INTEGER,
    disabled_applied_at INTEGER,
    created_at DATETIME,
    updated_at DATETIME
);
CREATE TABLE v2_stat_server (
    id INTEGER PRIMARY KEY,
    server_id INTEGER,
    server_type TEXT,
    u INTEGER,
    d INTEGER,
    record_type TEXT,
    record_at INTEGER,
    created_at INTEGER,
    updated_at INTEGER
);
CREATE TABLE v2_server_report_receipt (id INTEGER PRIMARY KEY);
`

func (g *Generator) buildSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, representativeLegacySchema)
	return err
}
