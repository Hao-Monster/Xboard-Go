package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyTrustedPluginsWithVerifiedRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyTrustedPluginsCLIFile(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-trusted-plugins.xbbackup")
	now := time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath, "--backup-output", backupPath,
	}, &stdout, &stderr, func() time.Time { return now }); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || stdout.Len() != 0 {
		t.Fatalf("offline gate handled=%t err=%v stdout=%s", handled, err, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now }); !handled || err == nil || !strings.Contains(err.Error(), "backup-output") || stdout.Len() != 0 {
		t.Fatalf("backup gate handled=%t err=%v stdout=%s", handled, err, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("command handled=%t err=%v stdout=%s stderr=%s", handled, err, stdout.String(), stderr.String())
	}
	var result legacyTrustedPluginsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Status != "success" ||
		result.Action != "migration.import-legacy-trusted-plugins" || result.Result.Plugins.SourceRows != 7 ||
		result.Result.Plugins.SourceChecksum != result.Result.Plugins.TargetChecksum || result.RollbackBackup.Path == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}

	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := inspection.ListTrustedPlugins(t.Context())
	if closeErr := inspection.Close(); err != nil || closeErr != nil {
		t.Fatalf("inspect plugins err=%v close=%v", err, closeErr)
	}
	for _, plugin := range plugins {
		if plugin.Code == store.TrustedPluginTelegram && (plugin.Enabled || plugin.Config["help_text"] != "迁移帮助") {
			t.Fatalf("migrated Telegram=%#v", plugin)
		}
		if plugin.Code == store.TrustedPluginEPay && plugin.Enabled {
			t.Fatalf("migrated EPay=%#v", plugin)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath,
		"--backup-output", filepath.Join(directory, "wrong-backup.xbbackup"), "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(30 * time.Minute) }); !handled || err == nil || !strings.Contains(err.Error(), "does not match") || stdout.Len() != 0 {
		t.Fatalf("mismatched recorded backup handled=%t err=%v stdout=%s", handled, err, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Hour) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied || !result.Result.AppliedAt.Equal(now) {
		t.Fatalf("idempotent command handled=%t err=%v result=%#v", handled, err, result)
	}
	drifted, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drifted.Exec(`UPDATE trusted_plugins SET enabled=1 WHERE code='telegram'`); err != nil {
		t.Fatal(err)
	}
	if err := drifted.Close(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(2 * time.Hour) }); !handled || err == nil || !strings.Contains(err.Error(), "migration ledger") || stdout.Len() != 0 {
		t.Fatalf("drifted idempotent command handled=%t err=%v stdout=%s", handled, err, stdout.String())
	}

	restoredPath := filepath.Join(directory, "restored.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := store.OpenSQLite("file:" + restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredPlugins, err := restored.ListTrustedPlugins(t.Context())
	if closeErr := restored.Close(); err != nil || closeErr != nil {
		t.Fatalf("inspect restored plugins err=%v close=%v", err, closeErr)
	}
	if len(restoredPlugins) != 7 {
		t.Fatalf("restored plugin count=%d", len(restoredPlugins))
	}
	for _, plugin := range restoredPlugins {
		if !plugin.Enabled || plugin.Revision != 1 || !plugin.UpdatedAt.Equal(time.Unix(0, 0).UTC()) {
			t.Fatalf("rollback did not restore pristine plugin=%#v", plugin)
		}
	}
}

func TestRunCommandImportsCapturedRealTrustedPluginsSnapshot(t *testing.T) {
	sourcePath := strings.TrimSpace(os.Getenv("XBOARD_TEST_LEGACY_TRUSTED_PLUGINS_SNAPSHOT"))
	if sourcePath == "" {
		t.Skip("set XBOARD_TEST_LEGACY_TRUSTED_PLUGINS_SNAPSHOT to the captured local legacy snapshot")
	}
	directory := t.TempDir()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-real-trusted-plugins.xbbackup")
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-trusted-plugins", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 31, 23, 30, 0, 0, time.UTC) })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("real command handled=%t err=%v stderr=%s", handled, err, stderr.String())
	}
	var result legacyTrustedPluginsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil ||
		result.Source.SHA256 != "a23d9a90866b4aec783a617afe766c227221cf7fbde272ae1f261ddb5f02ac9b" ||
		result.Result.Plugins.SourceRows != 7 || result.Result.Plugins.SourceChecksum != result.Result.Plugins.TargetChecksum {
		t.Fatalf("real result=%#v err=%v", result, err)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := inspection.ListTrustedPlugins(t.Context())
	if closeErr := inspection.Close(); err != nil || closeErr != nil {
		t.Fatalf("inspect real import err=%v close=%v", err, closeErr)
	}
	for _, plugin := range plugins {
		if !plugin.Enabled || plugin.Revision != 2 {
			t.Fatalf("real imported plugin=%#v", plugin)
		}
	}
}

func createLegacyTrustedPluginsCLIFile(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-trusted-plugins.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_plugins(
		id INTEGER PRIMARY KEY AUTOINCREMENT,name TEXT NOT NULL,code TEXT NOT NULL,version TEXT NOT NULL,
		is_enabled INTEGER NOT NULL,config TEXT NOT NULL,installed_at INTEGER,created_at INTEGER,updated_at INTEGER,type TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	telegramConfig := `{"enable_ticket_notify":false,"enable_payment_notify":true,"start_welcome_title":"欢迎","start_bot_description":"助手","start_bind_guide":"绑定","start_unbind_guide":"解绑","start_bind_commands":"命令","start_footer":"页脚","help_text":"迁移帮助"}`
	rows := []struct {
		name, code, version, config, pluginType string
		enabled                                 int
	}{
		{name: "AlipayF2F", code: "alipay_f2f", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "BTCPay", code: "btcpay", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "CoinPayments", code: "coin_payments", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "Coinbase", code: "coinbase", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "EPay", code: "epay", version: "1.0.0", enabled: 0, config: "[]", pluginType: "payment"},
		{name: "MGate", code: "mgate", version: "1.0.0", enabled: 1, config: "[]", pluginType: "payment"},
		{name: "Telegram Bot 集成", code: "telegram", version: "1.0.1", enabled: 0, config: telegramConfig, pluginType: "feature"},
	}
	for _, row := range rows {
		if _, err := database.Exec(`INSERT INTO v2_plugins(name,code,version,is_enabled,config,type) VALUES(?,?,?,?,?,?)`, row.name, row.code, row.version, row.enabled, row.config, row.pluginType); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
