package legacymigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadTelegramSettingsSnapshotKeepsTokenOutOfSerializableEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-telegram-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	token := "123456789:abcdefghijklmnopqrstuvwxyzABCDE"
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('telegram_bot_enable','1'),('telegram_bot_token',?),
		('telegram_webhook_url','https://panel.example.test'),('telegram_discuss_link','https://t.me/xboard_group')`, token); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadTelegramSettingsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.ClearSecrets()
	if !snapshot.Settings.BotEnabled || !snapshot.Settings.BotTokenConfigured || string(snapshot.BotToken) != token || snapshot.Settings.WebhookURL != "https://panel.example.test" || snapshot.Settings.DiscussLink != "https://t.me/xboard_group" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("snapshot serialization exposed the legacy Telegram token")
	}
}

func TestReadTelegramSettingsSnapshotRejectsDuplicatesInvalidTokenAndUnsafeURL(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":     `('telegram_bot_token','123456789:abcdefghijklmnopqrstuvwxyzABCDE'),('telegram_bot_token','123456789:abcdefghijklmnopqrstuvwxyzABCDE')`,
		"invalid token": `('telegram_bot_enable','1'),('telegram_bot_token','bad-token')`,
		"unsafe URL":    `('telegram_webhook_url','http://panel.example.test')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-invalid-telegram.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadTelegramSettingsSnapshot(context.Background(), path); err == nil {
				t.Fatal("invalid Telegram snapshot was accepted")
			}
		})
	}
}
