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

func TestRunCommandImportsLegacyMailSettingsWithoutExposingPassword(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-mail-settings.db")
	password := "legacy-smtp-password"
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('email_host','smtp.example.test'),('email_port','465'),('email_username','mailer'),
		('email_password',?),('email_encryption','ssl'),('email_from_address','support@example.test'),('remind_mail_enable','1')`, password)
	if err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	key := bytes.Repeat([]byte{0x61}, 32)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	backupPath := filepath.Join(directory, "pre-mail-settings.xbbackup")
	now := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)

	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"migration", "import-legacy-mail-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	for _, secret := range []string{password, base64.StdEncoding.EncodeToString(key)} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatal("migration output exposed SMTP credential material")
		}
	}
	var result legacyMailSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.AlreadyApplied || result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	inspection, _ := store.OpenSQLite("file:" + targetPath)
	mail, mailErr := inspection.GetMailSettings(t.Context())
	ciphertext, cipherErr := inspection.GetSMTPPasswordCipher(t.Context())
	_ = inspection.Close()
	if mailErr != nil || cipherErr != nil || !mail.SMTPEnabled || mail.SMTPHost != "smtp.example.test" || mail.SMTPPort != 465 ||
		mail.SMTPUsername != "mailer" || !mail.SMTPPasswordSet || mail.SMTPEncryption != "tls" ||
		mail.SMTPFromAddress != "support@example.test" || !mail.RemindMailEnabled {
		t.Fatalf("mail=%#v mailErr=%v cipherErr=%v", mail, mailErr, cipherErr)
	}
	cipherBox, _ := appsettings.NewCipher(key)
	plaintext, err := cipherBox.Decrypt(ciphertext)
	if err != nil || string(plaintext) != password {
		t.Fatalf("decrypt migrated SMTP password error=%v", err)
	}
	clearMigrationKey(plaintext)

	restoredPath := filepath.Join(directory, "restored.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, _ := store.OpenSQLite("file:" + restoredPath)
	restoredMail, settingsErr := restored.GetMailSettings(t.Context())
	_, imported, lookupErr := restored.LookupLegacyMailSettingsImport(t.Context(), result.Source.SHA256)
	_ = restored.Close()
	if settingsErr != nil || lookupErr != nil || restoredMail.SMTPEnabled || restoredMail.SMTPPasswordSet || imported {
		t.Fatalf("restored mail=%#v imported=%t settingsErr=%v lookupErr=%v", restoredMail, imported, settingsErr, lookupErr)
	}

	stdout.Reset()
	handled, err = runCommand(context.Background(), []string{
		"migration", "import-legacy-mail-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil {
		t.Fatalf("idempotent run handled=%t err=%v", handled, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent result=%#v err=%v", result, err)
	}
}

func TestRunCommandRejectsMailSettingsMigrationWithoutConfirmationOrKey(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-mail-settings.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('email_host','smtp.example.test'),('email_username','mailer'),
		('email_password','secret'),('email_from_address','support@example.test')`)
	_ = source.Close()
	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-mail-settings", "--source", sourcePath}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("missing confirmation handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-mail-settings", "--source", sourcePath, "--confirm-offline"}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "XBOARD_SETTINGS_ENCRYPTION_KEY") {
		t.Fatalf("missing key handled=%t err=%v", handled, err)
	}
}
