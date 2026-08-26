package store

import (
	"context"
	"database/sql"
	"fmt"
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
