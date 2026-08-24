package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestPrepareSQLiteDirectory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nested", "xboard.db")
	if err := prepareSQLiteDirectory("file:" + databasePath); err != nil {
		t.Fatalf("prepareSQLiteDirectory() error = %v", err)
	}
	info, err := os.Stat(filepath.Dir(databasePath))
	if err != nil || !info.IsDir() {
		t.Fatalf("database directory was not created: info=%v err=%v", info, err)
	}
	if err := prepareSQLiteDirectory("file:memory?mode=memory&cache=shared"); err != nil {
		t.Fatalf("memory DSN should be a no-op: %v", err)
	}
}

func TestInitializeSettingsCipherFailsClosedForStoredCredentials(t *testing.T) {
	ctx := context.Background()
	database, err := store.OpenSQLite("file:" + filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, store.CreateAdminUserInput{Email: "settings-main@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipherBox.Encrypt([]byte("smtp-password"))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := database.GetTicketSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateTicketSettings(ctx, administrator.ID, initial.Revision, store.SaveTicketSettingsInput{
		AppName: "Xboard", SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 587,
		SMTPUsername: "mailer", SMTPEncryption: "starttls", SMTPFromAddress: "support@example.test",
		ReplaceSMTPPassword: true, SMTPPasswordCipher: ciphertext,
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeSettingsCipher(ctx, database, nil); err == nil {
		t.Fatal("initializeSettingsCipher() accepted a missing key")
	}
	if _, err := initializeSettingsCipher(ctx, database, bytes.Repeat([]byte{0x24}, 32)); err == nil {
		t.Fatal("initializeSettingsCipher() accepted the wrong key")
	}
	if _, err := initializeSettingsCipher(ctx, database, key); err != nil {
		t.Fatalf("initializeSettingsCipher() rejected the matching key: %v", err)
	}
}

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XBOARD_HEALTH_URL", server.URL)
	if err := runHealthcheck(); err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}
}
