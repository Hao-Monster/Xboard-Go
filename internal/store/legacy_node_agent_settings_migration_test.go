package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
)

func TestImportLegacyNodeAgentSettingsIsVerifiedAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	settings := LegacyNodeAgentSettings{
		ServerTokenHash: security.DigestToken("legacy-agent-token-1234567890"), ServerTokenPrefix: "legacy-a",
		PullInterval: 31, PushInterval: 29, DeviceLimitMode: 1, WebSocketEnabled: true, WebSocketURL: "wss://panel.example.test/ws",
	}
	input := LegacyNodeAgentSettingsImport{
		Slice: LegacyNodeAgentSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyNodeAgentSettingsChecksum(settings), RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyNodeAgentSettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyNodeAgentSettings()=(%#v,%v)", report, err)
	}
	valid, err := database.AuthenticateLegacyNodeToken(context.Background(), "legacy-agent-token-1234567890")
	if err != nil || !valid {
		t.Fatalf("AuthenticateLegacyNodeToken()=(%t,%v)", valid, err)
	}
	repeated, err := database.ImportLegacyNodeAgentSettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
}

func TestLegacyNodeAgentSettingsChecksumExcludesCredentialMaterial(t *testing.T) {
	first := LegacyNodeAgentSettings{
		ServerTokenHash: security.DigestToken("first-legacy-agent-token-123456789"), ServerTokenPrefix: "first-le",
		PullInterval: 31, PushInterval: 29, DeviceLimitMode: 1, WebSocketEnabled: true, WebSocketURL: "wss://panel.example.test/ws",
	}
	second := first
	second.ServerTokenHash = security.DigestToken("second-legacy-agent-token-12345678")
	second.ServerTokenPrefix = "second-l"
	if LegacyNodeAgentSettingsChecksum(first) != LegacyNodeAgentSettingsChecksum(second) {
		t.Fatal("public migration checksum varies with credential material")
	}
	second.ServerTokenHash, second.ServerTokenPrefix = "", ""
	if LegacyNodeAgentSettingsChecksum(first) == LegacyNodeAgentSettingsChecksum(second) {
		t.Fatal("public migration checksum does not preserve the non-secret configured state")
	}
}

func TestImportLegacyNodeAgentSettingsRollsBackCredentialVerificationMismatch(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER corrupt_imported_node_agent_token
		AFTER INSERT ON node_agent_settings
		WHEN NEW.server_token_hash IS NOT NULL
		BEGIN
			UPDATE node_agent_settings
			SET server_token_hash = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
			WHERE id = NEW.id;
		END;
	`); err != nil {
		t.Fatal(err)
	}
	settings := LegacyNodeAgentSettings{
		ServerTokenHash: security.DigestToken("legacy-agent-token-1234567890"), ServerTokenPrefix: "legacy-a",
		PullInterval: 31, PushInterval: 29, DeviceLimitMode: 1, WebSocketEnabled: true, WebSocketURL: "wss://panel.example.test/ws",
	}
	input := LegacyNodeAgentSettingsImport{
		Slice: LegacyNodeAgentSettingsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyNodeAgentSettingsChecksum(settings), RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	if _, err := database.ImportLegacyNodeAgentSettings(ctx, input, time.Date(2026, 8, 28, 12, 30, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("ImportLegacyNodeAgentSettings() error=%v, want verification failure", err)
	}
	var settingsRows, migrationRows int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_agent_settings`).Scan(&settingsRows); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyNodeAgentSettingsSlice).Scan(&migrationRows); err != nil {
		t.Fatal(err)
	}
	if settingsRows != 0 || migrationRows != 0 {
		t.Fatalf("failed verification committed settings=%d migration_runs=%d", settingsRows, migrationRows)
	}
}
