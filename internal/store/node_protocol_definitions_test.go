package store

import (
	"context"
	"testing"
)

func TestCurrentSchemaIncludesLosslessNodeProtocolDefinitions(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()

	var columns int
	err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pragma_table_info('node_protocol_definitions')
		WHERE name IN (
			'node_id', 'external_code', 'parent_id', 'server_port', 'tags_json',
			'protocol_settings_json', 'rate_time_enabled', 'rate_time_ranges_json',
			'custom_outbounds_json', 'custom_routes_json', 'cert_config_json', 'transfer_enable'
		)
	`).Scan(&columns)
	if err != nil {
		t.Fatalf("inspect node protocol definitions: %v", err)
	}
	if columns != 12 {
		t.Fatalf("node protocol definition columns = %d, want 12", columns)
	}
}
