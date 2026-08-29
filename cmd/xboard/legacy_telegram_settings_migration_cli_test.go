package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyTelegramSettingsWithoutExposingToken(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-telegram-settings.db")
	token := "123456789:abcdefghijklmnopqrstuvwxyzABCDE"
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('telegram_bot_enable','1'),('telegram_bot_token',?),
		('telegram_webhook_url','https://panel.example.test'),('telegram_discuss_link','https://t.me/xboard_group')`, token); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	key := bytes.Repeat([]byte{0x74}, 32)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	backupPath := filepath.Join(directory, "pre-telegram-settings.xbbackup")
	now := time.Date(2026, 8, 29, 16, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"migration", "import-legacy-telegram-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stdout.String(), base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("migration output exposed Telegram credential material")
	}
	var result legacyTelegramSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Result.AlreadyApplied || result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v", result)
	}
	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := inspection.GetTelegramSecretCiphers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := inspection.GetTelegramSettings(t.Context())
	_ = inspection.Close()
	if err != nil || !settings.BotEnabled || !settings.BotTokenSet || settings.WebhookURL != "https://panel.example.test" || settings.DiscussLink != "https://t.me/xboard_group" || settings.BotUsername != "" {
		t.Fatalf("migrated settings=%#v err=%v", settings, err)
	}
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipherBox.DecryptFor(appsettings.TelegramBotTokenPurpose, secrets.BotToken)
	if err != nil || string(plaintext) != token {
		t.Fatalf("decrypt migrated Telegram token error=%v", err)
	}
	clearMigrationKey(plaintext)
	restoredPath := filepath.Join(directory, "restored-pre-telegram-settings.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatalf("Restore(rollback backup) error=%v", err)
	}
	restored, err := store.OpenSQLite("file:" + restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	restoredSettings, settingsErr := restored.GetTelegramSettings(t.Context())
	_, imported, lookupErr := restored.LookupLegacyTelegramSettingsImport(t.Context(), result.Source.SHA256)
	closeErr := restored.Close()
	if settingsErr != nil || lookupErr != nil || closeErr != nil || restoredSettings.BotEnabled || restoredSettings.BotTokenSet || imported {
		t.Fatalf("restored rollback state settings=%#v imported=%t settingsErr=%v lookupErr=%v closeErr=%v", restoredSettings, imported, settingsErr, lookupErr, closeErr)
	}

	stdout.Reset()
	handled, err = runCommand(context.Background(), []string{
		"migration", "import-legacy-telegram-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil {
		t.Fatalf("idempotent run handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent result=%#v err=%v", result, err)
	}
}

func TestRunCommandRejectsTelegramMigrationWithoutOfflineConfirmationOrEncryptionKey(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-telegram-settings.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('telegram_bot_enable','1'),('telegram_bot_token','123456789:abcdefghijklmnopqrstuvwxyzABCDE')`); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-telegram-settings", "--source", sourcePath}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("missing confirmation handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-telegram-settings", "--source", sourcePath, "--confirm-offline"}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "XBOARD_SETTINGS_ENCRYPTION_KEY") {
		t.Fatalf("missing key handled=%t err=%v", handled, err)
	}
}
