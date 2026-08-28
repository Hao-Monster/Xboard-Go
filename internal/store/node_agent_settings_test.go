package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNodeAgentSettingsKeepLegacyTokenHashedAndRevisionSafe(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	defaults := NodeAgentSettingsDefaults{
		PullInterval: 45, PushInterval: 30, DeviceLimitMode: 1,
		WebSocketEnabled: true, WebSocketURL: "wss://panel.example.test/ws",
	}
	if err := database.EnsureNodeAgentSettings(ctx, defaults, now); err != nil {
		t.Fatalf("EnsureNodeAgentSettings() error = %v", err)
	}
	initial, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatalf("GetNodeAgentSettings() error = %v", err)
	}
	if initial.Revision != 1 || initial.ServerTokenConfigured || initial.PullInterval != 45 || initial.PushInterval != 30 ||
		initial.DeviceLimitMode != 1 || !initial.WebSocketEnabled || initial.WebSocketURL != "wss://panel.example.test/ws" {
		t.Fatalf("unexpected initial settings: %#v", initial)
	}

	// Process restarts must not overwrite administrator-owned values.
	if err := database.EnsureNodeAgentSettings(ctx, NodeAgentSettingsDefaults{PullInterval: 99, PushInterval: 99}, now.Add(time.Minute)); err != nil {
		t.Fatalf("second EnsureNodeAgentSettings() error = %v", err)
	}
	unchanged, _ := database.GetNodeAgentSettings(ctx)
	if unchanged != initial {
		t.Fatalf("second ensure changed settings: got %#v want %#v", unchanged, initial)
	}

	token := "legacy-compatible-token-1234567890"
	updated, err := database.UpdateNodeAgentSettings(ctx, UpdateNodeAgentSettingsInput{
		Revision: initial.Revision, ServerToken: &token,
		PullInterval: 60, PushInterval: 90, DeviceLimitMode: 0,
		WebSocketEnabled: false, WebSocketURL: "",
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("UpdateNodeAgentSettings() error = %v", err)
	}
	if updated.Revision != 2 || !updated.ServerTokenConfigured || updated.ServerTokenPrefix != token[:8] {
		t.Fatalf("unexpected updated settings: %#v", updated)
	}
	var storedHash, storedPrefix string
	if err := database.db.QueryRowContext(ctx, `SELECT server_token_hash, server_token_prefix FROM node_agent_settings WHERE id = 1`).Scan(&storedHash, &storedPrefix); err != nil {
		t.Fatalf("read stored token: %v", err)
	}
	if storedHash == token || len(storedHash) != 64 || storedPrefix != token[:8] {
		t.Fatalf("token was not stored as a digest: hash_len=%d prefix=%q", len(storedHash), storedPrefix)
	}
	if valid, err := database.AuthenticateLegacyNodeToken(ctx, token); err != nil || !valid {
		t.Fatalf("AuthenticateLegacyNodeToken(valid) = (%v, %v)", valid, err)
	}
	if valid, err := database.AuthenticateLegacyNodeToken(ctx, token+"x"); err != nil || valid {
		t.Fatalf("AuthenticateLegacyNodeToken(invalid) = (%v, %v)", valid, err)
	}

	if _, err := database.UpdateNodeAgentSettings(ctx, UpdateNodeAgentSettingsInput{
		Revision: initial.Revision, PullInterval: 60, PushInterval: 60,
	}, now.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	clear := ""
	cleared, err := database.UpdateNodeAgentSettings(ctx, UpdateNodeAgentSettingsInput{
		Revision: updated.Revision, ServerToken: &clear,
		PullInterval: 60, PushInterval: 60, DeviceLimitMode: 0,
	}, now.Add(4*time.Minute))
	if err != nil || cleared.ServerTokenConfigured || cleared.ServerTokenPrefix != "" {
		t.Fatalf("clear token = (%#v, %v)", cleared, err)
	}
	if valid, err := database.AuthenticateLegacyNodeToken(ctx, token); err != nil || valid {
		t.Fatalf("cleared token authentication = (%v, %v)", valid, err)
	}
}

func TestNodeAgentSettingsRejectUnsafeBoundaries(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	if err := database.EnsureNodeAgentSettings(ctx, NodeAgentSettingsDefaults{PullInterval: 60, PushInterval: 60}, now); err != nil {
		t.Fatal(err)
	}
	initial, _ := database.GetNodeAgentSettings(ctx)
	administratorID, otherAdministratorID := int64(1), int64(2)

	for name, input := range map[string]UpdateNodeAgentSettingsInput{
		"short token":           {Revision: initial.Revision, ServerToken: nodeAgentStringPointer("too-short"), PullInterval: 60, PushInterval: 60},
		"token whitespace":      {Revision: initial.Revision, ServerToken: nodeAgentStringPointer("invalid token with spaces"), PullInterval: 60, PushInterval: 60},
		"zero pull":             {Revision: initial.Revision, PullInterval: 0, PushInterval: 60},
		"large push":            {Revision: initial.Revision, PullInterval: 60, PushInterval: 3601},
		"bad device mode":       {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, DeviceLimitMode: 2},
		"http websocket":        {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, WebSocketURL: "https://panel.example.test/ws"},
		"websocket credentials": {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, WebSocketURL: "wss://user@panel.example.test/ws"},
		"websocket query":       {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, WebSocketURL: "wss://panel.example.test/ws?token=secret"},
		"audit without admin":   {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, Audit: &AdminAuditInput{AdministratorID: administratorID}},
		"admin without audit":   {Revision: initial.Revision, PullInterval: 60, PushInterval: 60, UpdatedBy: &administratorID},
		"mismatched audit": {
			Revision: initial.Revision, PullInterval: 60, PushInterval: 60, UpdatedBy: &administratorID,
			Audit: &AdminAuditInput{AdministratorID: otherAdministratorID},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.UpdateNodeAgentSettings(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNodeAgentSettingsAndAdministratorAuditAreAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "node-settings-auditor@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.EnsureNodeAgentSettings(ctx, NodeAgentSettingsDefaults{PullInterval: 60, PushInterval: 60}, now); err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_node_agent_settings_audit
		BEFORE INSERT ON admin_audit_logs
		BEGIN
			SELECT RAISE(ABORT, 'audit unavailable');
		END;
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := database.UpdateNodeAgentSettings(ctx, UpdateNodeAgentSettingsInput{
		Revision: initial.Revision, PullInterval: 31, PushInterval: 29, UpdatedBy: &admin.ID,
		Audit: &AdminAuditInput{
			AdministratorID: admin.ID, AdministratorEmail: admin.Email, Method: "PUT",
			Route: "/api/v1/admin/node-agent-settings", StatusCode: 200,
		},
	}, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "audit") {
		t.Fatalf("UpdateNodeAgentSettings() error=%v, want audit failure", err)
	}
	current, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current != initial {
		t.Fatalf("failed audit committed settings: got %#v want %#v", current, initial)
	}
}

func TestSchemaV43RejectsPreexistingNodeAgentSettingsTable(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE node_agent_settings;
		CREATE TABLE node_agent_settings (
			id INTEGER PRIMARY KEY,
			revision INTEGER NOT NULL,
			server_token_hash TEXT,
			server_token_prefix TEXT NOT NULL,
			pull_interval INTEGER NOT NULL,
			push_interval INTEGER NOT NULL,
			device_limit_mode INTEGER NOT NULL,
			websocket_enabled INTEGER NOT NULL,
			websocket_url TEXT NOT NULL,
			updated_by INTEGER,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO node_agent_settings VALUES (1, 1, NULL, 'untrusted', 60, 60, 0, 0, '', NULL, 0);
		PRAGMA user_version = 42;
	`); err != nil {
		t.Fatal(err)
	}

	if err := database.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "node_agent_settings") {
		t.Fatalf("Migrate(preexisting node_agent_settings) error=%v", err)
	}
	var version int
	var prefix string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT server_token_prefix FROM node_agent_settings WHERE id = 1`).Scan(&prefix); err != nil {
		t.Fatal(err)
	}
	if version != 42 || prefix != "untrusted" {
		t.Fatalf("failed migration committed state: version=%d prefix=%q", version, prefix)
	}
}

func nodeAgentStringPointer(value string) *string { return &value }

func BenchmarkAuthenticateLegacyNodeToken(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	if err := database.EnsureNodeAgentSettings(ctx, NodeAgentSettingsDefaults{PullInterval: 60, PushInterval: 60}, now); err != nil {
		b.Fatal(err)
	}
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		b.Fatal(err)
	}
	token := "benchmark-node-agent-token-1234567890"
	if _, err := database.UpdateNodeAgentSettings(ctx, UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &token, PullInterval: 60, PushInterval: 60,
	}, now); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		valid, err := database.AuthenticateLegacyNodeToken(ctx, token)
		if err != nil || !valid {
			b.Fatalf("AuthenticateLegacyNodeToken()=(%t,%v)", valid, err)
		}
	}
}

func BenchmarkGetNodeAgentSettings(b *testing.B) {
	database := newTestStore(b)
	ctx := context.Background()
	if err := database.EnsureNodeAgentSettings(ctx, NodeAgentSettingsDefaults{PullInterval: 60, PushInterval: 60}, time.Unix(1_800_000_000, 0)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := database.GetNodeAgentSettings(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
