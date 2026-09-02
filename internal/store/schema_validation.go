package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var requiredSchemaTables = []struct {
	name       string
	minVersion int
}{
	{"users", 1},
	{"admin_sessions", 1},
	{"server_machines", 1},
	{"nodes", 1},
	{"server_machine_credentials", 1},
	{"server_machine_enrollments", 1},
	{"server_machine_load_history", 1},
	{"server_activation_schedules", 1},
	{"node_group_memberships", 2},
	{"node_report_receipts", 2},
	{"node_report_traffic_stage", 2},
	{"user_traffic_stats", 2},
	{"node_traffic_stats", 2},
	{"node_device_ips", 2},
	{"node_user_online", 2},
	{"node_runtime_state", 2},
	{"server_groups", 3},
	{"routing_rules", 3},
	{"node_route_memberships", 3},
	{"notices", 5},
	{"client_catalog_config", 6},
	{"client_catalog_links", 6},
	{"knowledge", 7},
	{"tickets", 8},
	{"ticket_messages", 8},
	{"app_settings", 9},
	{"ticket_mail_outbox", 9},
	{"ticket_mail_throttle", 9},
	{"admin_audit_logs", 11},
	{"registration_ip_limits", 16},
	{"password_reset_challenges", 17},
	{"password_reset_mail_outbox", 17},
	{"registration_email_challenges", 18},
	{"registration_email_mail_outbox", 18},
	{"invitation_codes", 19},
	{"login_link_tokens", 20},
	{"mail_login_request_limits", 20},
	{"login_link_mail_outbox", 20},
	{"access_tokens", 21},
	{"login_failure_limits", 22},
	{"legacy_migration_runs", 24},
	{"node_protocol_definitions", 26},
	{"plans", 27},
	{"traffic_reset_logs", 27},
	{"orders", 29},
	{"order_entitlement_events", 29},
	{"coupons", 30},
	{"payments", 31},
	{"payment_checkout_attempts", 31},
	{"payment_webhook_receipts", 31},
	{"gift_card_templates", 32},
	{"gift_card_codes", 32},
	{"gift_card_usages", 32},
	{"distributor_subscriptions", 33},
	{"distributor_hwid_devices", 33},
	{"commission_logs", 34},
	{"knowledge_attachments", 35},
	{"knowledge_attachment_uploads", 35},
	{"knowledge_attachment_chunks", 35},
	{"admin_user_bulk_jobs", 39},
	{"admin_user_bulk_targets", 39},
	{"node_agent_settings", 43},
	{"subscription_reminder_outbox", 46},
	{"mail_templates", 48},
	{"client_app_settings", 49},
	{"themes", 51},
	{"theme_assets", 51},
	{"theme_settings", 51},
	{"trusted_plugins", 55},
	{"telegram_message_outbox", 56},
	{"commission_withdrawals", 60},
	{"commission_withdrawal_events", 60},
	{"user_lifecycle_events", 61},
}

var requiredSchemaColumns = map[string][]string{
	"knowledge_attachments": {
		"id", "uuid", "knowledge_id", "uploader_user_id", "draft_token_hash", "original_name", "storage_path",
		"mime_type", "extension", "size", "sha256", "status", "created_at", "updated_at", "deleted_at",
	},
	"knowledge_attachment_uploads": {
		"id", "uuid", "uploader_user_id", "draft_token_hash", "original_name", "declared_size", "expected_sha256",
		"chunk_size", "total_chunks", "received_chunks", "temporary_path", "status", "expires_at", "created_at", "updated_at",
	},
	"knowledge_attachment_chunks": {"upload_id", "chunk_index", "size", "sha256", "created_at"},
}

var requiredSchemaColumnsV37 = map[string][]string{
	"users": {"telegram_id", "remind_expire", "remind_traffic", "remarks"},
}

var requiredSchemaColumnsV38 = map[string][]string{
	"traffic_reset_logs": {
		"upload_after", "download_after", "trigger_source", "reason", "administrator_id", "administrator_email", "idempotency_key",
	},
}

var requiredSchemaColumnsV39 = map[string][]string{
	"admin_user_bulk_jobs": {
		"id", "kind", "scope", "administrator_id", "administrator_email", "status", "request_digest",
		"total_count", "processed_count", "success_count", "failure_count", "skipped_count", "cancelled_count", "created_at", "updated_at",
	},
	"admin_user_bulk_targets": {
		"job_id", "sequence", "user_id", "email", "uuid", "plan_name", "group_id", "expired_at", "transfer_enable",
		"transfer_used", "balance", "commission_balance", "subscription_token", "status", "attempt_count", "available_at",
	},
}

var requiredSchemaColumnsV40 = map[string][]string{
	"nodes": {"admin_revision"},
}

var requiredSchemaColumnsV41 = map[string][]string{
	"node_protocol_definitions": {"listen_address"},
}

var requiredSchemaColumnsV43 = map[string][]string{
	"node_agent_settings": {
		"id", "revision", "server_token_hash", "server_token_prefix", "pull_interval", "push_interval",
		"device_limit_mode", "websocket_enabled", "websocket_url", "updated_by", "updated_at",
	},
}

var requiredSchemaColumnsV44 = map[string][]string{
	"app_settings": {"try_out_plan_id", "try_out_hour"},
}

var requiredSchemaColumnsV45 = map[string][]string{
	"app_settings": {"default_remind_expire", "default_remind_traffic"},
}

var requiredSchemaColumnsV46 = map[string][]string{
	"app_settings": {"remind_mail_enable"},
	"subscription_reminder_outbox": {
		"id", "user_id", "kind", "reminder_day", "recipient", "app_name", "app_url", "available_at",
		"attempt_count", "claim_token", "claimed_at", "sent_at", "failed_at", "cancelled_at", "last_error",
		"created_at", "updated_at",
	},
}

var requiredSchemaColumnsV47 = map[string][]string{
	"app_settings": {
		"telegram_bot_enable", "telegram_bot_token_cipher", "telegram_webhook_url", "telegram_discuss_link",
		"telegram_webhook_secret_cipher", "telegram_webhook_pending_secret_cipher", "telegram_webhook_provision_id", "telegram_bot_username",
		"telegram_webhook_configured_at",
	},
	"telegram_webhook_updates": {"update_id", "claim_id", "completed", "updated_at"},
}

var requiredSchemaColumnsV48 = map[string][]string{
	"mail_templates": {"name", "subject", "content", "revision", "updated_by", "updated_at"},
}

var requiredSchemaColumnsV49 = map[string][]string{
	"client_app_settings": {
		"id", "windows_version", "windows_download_url", "macos_version", "macos_download_url",
		"android_version", "android_download_url", "revision", "updated_by", "updated_at",
	},
}

var requiredSchemaColumnsV50 = map[string][]string{
	"app_settings": {"currency", "currency_symbol"},
}

var requiredSchemaColumnsV51 = map[string][]string{
	"themes": {
		"name", "description", "version", "manifest_json", "config_json", "package_sha256",
		"revision", "is_system", "created_by", "updated_by", "created_at", "updated_at",
	},
	"theme_assets":   {"theme_name", "path", "mime_type", "size", "sha256", "width", "height", "body"},
	"theme_settings": {"id", "active_theme", "revision", "updated_by", "updated_at"},
}

var requiredSchemaColumnsV52 = map[string][]string{
	"app_settings": {"force_https", "subscribe_url"},
}

var requiredSchemaColumnsV53 = map[string][]string{
	"app_settings": {"safe_mode_enable", "secure_path"},
}

var requiredSchemaColumnsV54 = map[string][]string{
	"app_settings":   {"commission_withdraw_limit", "commission_withdraw_method"},
	"theme_settings": {"sidebar_style", "header_style"},
}

var requiredSchemaColumnsV55 = map[string][]string{
	"trusted_plugins": {"code", "name", "type", "version", "enabled", "config_json", "revision", "updated_by", "updated_at"},
}

var requiredSchemaColumnsV56 = map[string][]string{
	"telegram_message_outbox": {
		"id", "source_kind", "source_id", "chat_id", "text", "attempt_count", "available_at",
		"claim_token", "claimed_at", "sent_at", "failed_at", "cancelled_at", "last_error", "created_at", "updated_at",
	},
}

var requiredSchemaColumnsV57 = map[string][]string{
	"telegram_message_outbox": {"recipient_user_id"},
}

var requiredSchemaColumnsV60 = map[string][]string{
	"users":                        {"frozen_commission_balance"},
	"commission_withdrawals":       {"id", "user_id", "idempotency_key", "amount", "fee_basis_points", "fee_amount", "net_amount", "currency", "method", "account_cipher", "account_fingerprint", "account_masked", "status", "revision", "external_reference", "rejection_reason", "created_at", "updated_at", "approved_at", "paid_at", "rejected_at"},
	"commission_withdrawal_events": {"id", "withdrawal_id", "actor_user_id", "kind", "from_status", "to_status", "amount", "currency", "revision", "created_at"},
}

var requiredSchemaColumnsV61 = map[string][]string{
	"users":                 {"lifecycle_status", "deletion_requested_at", "deletion_due_at", "deletion_banned_snapshot", "anonymized_at"},
	"user_lifecycle_events": {"id", "user_id", "actor_user_id", "kind", "from_status", "to_status", "revision", "created_at"},
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ValidateSchema verifies that a versioned SQLite database contains every
// Xboard table introduced at or before that version. The check must run before
// maintenance or recovery code trusts PRAGMA user_version and mutates data.
func ValidateSchema(ctx context.Context, database schemaQueryer, schemaVersion int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if database == nil {
		return fmt.Errorf("validate Xboard schema: database is required")
	}
	if schemaVersion < 1 || schemaVersion > CurrentSchemaVersion() {
		return fmt.Errorf("unsupported schema version %d (supported 1-%d)", schemaVersion, CurrentSchemaVersion())
	}
	rows, err := database.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table'`)
	if err != nil {
		return fmt.Errorf("inspect Xboard schema: %w", err)
	}
	tables := make(map[string]struct{}, len(requiredSchemaTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Xboard schema: %w", err)
		}
		tables[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Xboard schema: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Xboard schema: %w", err)
	}
	for _, required := range requiredSchemaTables {
		if required.minVersion > schemaVersion {
			continue
		}
		if _, exists := tables[required.name]; !exists {
			return fmt.Errorf("Xboard schema version %d is missing required table %q", schemaVersion, required.name)
		}
	}
	if schemaVersion >= 35 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumns); err != nil {
			return err
		}
	}
	if schemaVersion >= 37 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV37); err != nil {
			return err
		}
	}
	if schemaVersion >= 38 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV38); err != nil {
			return err
		}
	}
	if schemaVersion >= 39 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV39); err != nil {
			return err
		}
	}
	if schemaVersion >= 40 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV40); err != nil {
			return err
		}
	}
	if schemaVersion >= 41 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV41); err != nil {
			return err
		}
	}
	if schemaVersion >= 43 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV43); err != nil {
			return err
		}
	}
	if schemaVersion >= 44 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV44); err != nil {
			return err
		}
	}
	if schemaVersion >= 45 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV45); err != nil {
			return err
		}
	}
	if schemaVersion >= 46 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV46); err != nil {
			return err
		}
	}
	if schemaVersion >= 47 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV47); err != nil {
			return err
		}
		if err := validateTelegramIDIndex(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 48 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV48); err != nil {
			return err
		}
		if err := validateMailTemplateCatalog(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 49 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV49); err != nil {
			return err
		}
		if err := validateClientAppSettingsSingleton(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 50 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV50); err != nil {
			return err
		}
	}
	if schemaVersion >= 51 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV51); err != nil {
			return err
		}
		if err := validateThemeCatalog(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 52 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV52); err != nil {
			return err
		}
	}
	if schemaVersion >= 53 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV53); err != nil {
			return err
		}
	}
	if schemaVersion >= 54 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV54); err != nil {
			return err
		}
	}
	if schemaVersion >= 55 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV55); err != nil {
			return err
		}
	}
	if schemaVersion >= 56 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV56); err != nil {
			return err
		}
	}
	if schemaVersion >= 57 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV57); err != nil {
			return err
		}
		if err := validateTelegramNotificationIndex(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
		if err := validateTelegramNotificationRecipientTriggers(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 58 {
		if err := validateNodeUserOnlineUserIndex(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 59 {
		if err := validateUserTrafficTotalTriggers(ctx, database); err != nil {
			return fmt.Errorf("Xboard schema version %d: %w", schemaVersion, err)
		}
	}
	if schemaVersion >= 60 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV60); err != nil {
			return err
		}
	}
	if schemaVersion >= 61 {
		if err := validateRequiredSchemaColumns(ctx, database, schemaVersion, requiredSchemaColumnsV61); err != nil {
			return err
		}
	}
	if schemaVersion >= 42 {
		rows, err := database.QueryContext(ctx, `
			SELECT n.id FROM nodes n
			LEFT JOIN node_protocol_definitions d ON d.node_id = n.id
			WHERE d.node_id IS NULL LIMIT 1
		`)
		if err != nil {
			return fmt.Errorf("validate node protocol definition coverage: %w", err)
		}
		missing := rows.Next()
		if err := rows.Close(); err != nil {
			return fmt.Errorf("validate node protocol definition coverage: %w", err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("validate node protocol definition coverage: %w", err)
		}
		if missing {
			return fmt.Errorf("Xboard schema version %d contains a node without a protocol definition", schemaVersion)
		}
	}
	return nil
}

func validateNodeUserOnlineUserIndex(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `PRAGMA index_info("idx_node_user_online_user_expiry")`)
	if err != nil {
		return fmt.Errorf("inspect online user index: %w", err)
	}
	defer rows.Close()
	columns := make([]string, 0, 2)
	for rows.Next() {
		var sequence, columnID int
		var name string
		if err := rows.Scan(&sequence, &columnID, &name); err != nil {
			return fmt.Errorf("inspect online user index: %w", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect online user index: %w", err)
	}
	if len(columns) != 2 || columns[0] != "user_id" || columns[1] != "expires_at" {
		return errors.New("online user index must cover user_id then expires_at")
	}
	return nil
}

func validateUserTrafficTotalTriggers(ctx context.Context, database schemaQueryer) error {
	expected := map[string]string{
		"users_validate_traffic_total_insert": `
			CREATE TRIGGER users_validate_traffic_total_insert
			BEFORE INSERT ON users
			WHEN NEW.traffic_u > 9223372036854775807 - NEW.traffic_d
			BEGIN
				SELECT RAISE(ABORT, 'user traffic total overflow');
			END
		`,
		"users_validate_traffic_total_update": `
			CREATE TRIGGER users_validate_traffic_total_update
			BEFORE UPDATE OF traffic_u, traffic_d ON users
			WHEN NEW.traffic_u > 9223372036854775807 - NEW.traffic_d
			BEGIN
				SELECT RAISE(ABORT, 'user traffic total overflow');
			END
		`,
	}
	rows, err := database.QueryContext(ctx, `
		SELECT name,sql FROM sqlite_schema
		WHERE type='trigger' AND name IN (
			'users_validate_traffic_total_insert','users_validate_traffic_total_update'
		)
	`)
	if err != nil {
		return fmt.Errorf("inspect user traffic total triggers: %w", err)
	}
	found := make(map[string]string, len(expected))
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect user traffic total triggers: %w", err)
		}
		found[name] = definition
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect user traffic total triggers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect user traffic total triggers: %w", err)
	}
	for name, definition := range expected {
		if normalizeSchemaDefinition(found[name]) != normalizeSchemaDefinition(definition) {
			return fmt.Errorf("user traffic total trigger %q is missing or invalid", name)
		}
	}
	return nil
}

func validateMailTemplateCatalog(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `SELECT name FROM mail_templates ORDER BY name`)
	if err != nil {
		return fmt.Errorf("inspect mail template catalog: %w", err)
	}
	defer rows.Close()
	names := make([]string, 0, 5)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("inspect mail template catalog: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect mail template catalog: %w", err)
	}
	want := []string{"mailLogin", "notify", "remindExpire", "remindTraffic", "verify"}
	if len(names) != len(want) {
		return errors.New("mail template catalog must contain exactly five templates")
	}
	for index := range want {
		if names[index] != want[index] {
			return errors.New("mail template catalog contains an unexpected template")
		}
	}
	return nil
}

func validateClientAppSettingsSingleton(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `SELECT id FROM client_app_settings ORDER BY id`)
	if err != nil {
		return fmt.Errorf("inspect client app settings singleton: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("inspect client app settings singleton: %w", err)
		}
		if id != 1 {
			return errors.New("client app settings contains an unexpected row")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect client app settings singleton: %w", err)
	}
	if count != 1 {
		return errors.New("client app settings must contain exactly one row")
	}
	return nil
}

func validateThemeCatalog(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `SELECT id, active_theme FROM theme_settings ORDER BY id`)
	if err != nil {
		return fmt.Errorf("inspect theme settings singleton: %w", err)
	}
	count := 0
	activeTheme := ""
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id, &activeTheme); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect theme settings singleton: %w", err)
		}
		if id != 1 {
			_ = rows.Close()
			return errors.New("theme settings contains an unexpected row")
		}
		count++
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect theme settings singleton: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect theme settings singleton: %w", err)
	}
	if count != 1 {
		return errors.New("theme settings must contain exactly one row")
	}
	rows, err = database.QueryContext(ctx, `SELECT name, is_system FROM themes WHERE name = 'Xboard' COLLATE NOCASE`)
	if err != nil {
		return fmt.Errorf("inspect built-in Xboard theme: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return errors.New("theme catalog must contain the built-in Xboard theme")
	}
	var name string
	var system int
	if err := rows.Scan(&name, &system); err != nil {
		return fmt.Errorf("inspect built-in Xboard theme: %w", err)
	}
	if !strings.EqualFold(name, "Xboard") || system != 1 || activeTheme == "" {
		return errors.New("theme catalog contains an invalid built-in or active theme")
	}
	if rows.Next() {
		return errors.New("theme catalog contains duplicate built-in themes")
	}
	return rows.Err()
}

func validateTelegramIDIndex(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `PRAGMA index_list("users")`)
	if err != nil {
		return fmt.Errorf("inspect Telegram identity index: %w", err)
	}
	found := false
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Telegram identity index: %w", err)
		}
		if name == "idx_users_unique_telegram_id" {
			found = unique == 1 && partial == 1
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Telegram identity index: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Telegram identity index: %w", err)
	}
	if !found {
		return errors.New("Telegram identity index must be unique and partial")
	}
	rows, err = database.QueryContext(ctx, `PRAGMA index_info("idx_users_unique_telegram_id")`)
	if err != nil {
		return fmt.Errorf("inspect Telegram identity index columns: %w", err)
	}
	columns := make([]string, 0, 1)
	for rows.Next() {
		var sequence, columnID int
		var name string
		if err := rows.Scan(&sequence, &columnID, &name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Telegram identity index columns: %w", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Telegram identity index columns: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Telegram identity index columns: %w", err)
	}
	if len(columns) != 1 || columns[0] != "telegram_id" {
		return errors.New("Telegram identity index must cover only users.telegram_id")
	}
	rows, err = database.QueryContext(ctx, `
		SELECT sql FROM sqlite_schema
		WHERE type = 'index' AND name = 'idx_users_unique_telegram_id' AND tbl_name = 'users'
	`)
	if err != nil {
		return fmt.Errorf("inspect Telegram identity index predicate: %w", err)
	}
	var definition string
	if rows.Next() {
		if err := rows.Scan(&definition); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Telegram identity index predicate: %w", err)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Telegram identity index predicate: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Telegram identity index predicate: %w", err)
	}
	const expectedDefinition = "createuniqueindexidx_users_unique_telegram_idonusers(telegram_id)wheretelegram_idisnotnull"
	if strings.Join(strings.Fields(strings.ToLower(definition)), "") != expectedDefinition {
		return errors.New("Telegram identity index must exclude only null identities")
	}
	return nil
}

func validateTelegramNotificationIndex(ctx context.Context, database schemaQueryer) error {
	rows, err := database.QueryContext(ctx, `
		SELECT sql FROM sqlite_schema
		WHERE type='index' AND name='idx_users_telegram_admin_notify' AND tbl_name='users'
	`)
	if err != nil {
		return fmt.Errorf("inspect Telegram administrator notification index: %w", err)
	}
	var definition string
	if rows.Next() {
		if err := rows.Scan(&definition); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Telegram administrator notification index: %w", err)
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Telegram administrator notification index: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Telegram administrator notification index: %w", err)
	}
	const expectedDefinition = "createindexidx_users_telegram_admin_notifyonusers(telegram_id)wheretelegram_idisnotnullandaccount_kind='human'and(is_admin=1oris_staff=1)"
	if strings.Join(strings.Fields(strings.ToLower(definition)), "") != expectedDefinition {
		return errors.New("Telegram administrator notification index must cover only bound human administrators and staff")
	}
	return nil
}

func validateTelegramNotificationRecipientTriggers(ctx context.Context, database schemaQueryer) error {
	expected := map[string]string{
		"telegram_message_outbox_recipient_insert": `
			CREATE TRIGGER telegram_message_outbox_recipient_insert
			BEFORE INSERT ON telegram_message_outbox
			WHEN (NEW.source_kind='command' AND NEW.recipient_user_id IS NOT NULL)
			  OR (NEW.source_kind IN ('ticket','payment') AND NEW.recipient_user_id IS NULL)
			BEGIN
				SELECT RAISE(ABORT,'invalid Telegram outbox recipient');
			END
		`,
		"telegram_message_outbox_recipient_update": `
			CREATE TRIGGER telegram_message_outbox_recipient_update
			BEFORE UPDATE OF source_kind,recipient_user_id ON telegram_message_outbox
			WHEN (NEW.source_kind='command' AND NEW.recipient_user_id IS NOT NULL)
			  OR (NEW.source_kind IN ('ticket','payment') AND NEW.recipient_user_id IS NULL)
			BEGIN
				SELECT RAISE(ABORT,'invalid Telegram outbox recipient');
			END
		`,
	}
	rows, err := database.QueryContext(ctx, `
		SELECT name,sql FROM sqlite_schema
		WHERE type='trigger' AND name IN (
			'telegram_message_outbox_recipient_insert','telegram_message_outbox_recipient_update'
		)
	`)
	if err != nil {
		return fmt.Errorf("inspect Telegram notification recipient triggers: %w", err)
	}
	found := make(map[string]string, len(expected))
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Telegram notification recipient triggers: %w", err)
		}
		found[name] = definition
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("inspect Telegram notification recipient triggers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect Telegram notification recipient triggers: %w", err)
	}
	for name, definition := range expected {
		if normalizeSchemaDefinition(found[name]) != normalizeSchemaDefinition(definition) {
			return fmt.Errorf("Telegram notification recipient trigger %q is missing or invalid", name)
		}
	}
	return nil
}

func normalizeSchemaDefinition(value string) string {
	return strings.TrimSuffix(strings.Join(strings.Fields(strings.ToLower(value)), ""), ";")
}

func validateRequiredSchemaColumns(ctx context.Context, database schemaQueryer, schemaVersion int, requiredByTable map[string][]string) error {
	for table, requiredColumns := range requiredByTable {
		rows, err := database.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info("%s")`, table))
		if err != nil {
			return fmt.Errorf("inspect Xboard table %q: %w", table, err)
		}
		columns := make(map[string]struct{}, len(requiredColumns))
		for rows.Next() {
			var sequence, notNull, primaryKey int
			var name, dataType string
			var defaultValue any
			if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = rows.Close()
				return fmt.Errorf("inspect Xboard table %q: %w", table, err)
			}
			columns[name] = struct{}{}
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("inspect Xboard table %q: %w", table, err)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("inspect Xboard table %q: %w", table, err)
		}
		for _, column := range requiredColumns {
			if _, exists := columns[column]; !exists {
				return fmt.Errorf("Xboard schema version %d table %q is missing required column %q", schemaVersion, table, column)
			}
		}
	}
	return nil
}

func (s *Store) ValidateCurrentSchema(ctx context.Context) error {
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion() {
		return fmt.Errorf("requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	return ValidateSchema(ctx, s.db, version)
}
