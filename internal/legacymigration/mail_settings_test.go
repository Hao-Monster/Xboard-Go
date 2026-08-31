package legacymigration

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadMailSettingsSnapshotPreservesSecureConfigurationAndHidesPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mail-settings.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	password := []byte(" smtp-password\r\n")
	_, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('email_host','smtp.example.test'),('email_port','465'),('email_username','mailer'),
		('email_password',?),('email_encryption','ssl'),('email_from_address','support@example.test'),
		('remind_mail_enable','1'),('unrelated_secret',zeroblob(1048576))`, password)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	snapshot, err := ReadMailSettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.ClearSecrets()
	settings := snapshot.Settings
	if !settings.SMTPEnabled || settings.SMTPHost != "smtp.example.test" || settings.SMTPPort != 465 ||
		settings.SMTPUsername != "mailer" || !settings.SMTPPasswordConfigured || settings.SMTPEncryption != "tls" ||
		settings.SMTPFromAddress != "support@example.test" || !settings.RemindMailEnabled || !bytes.Equal(snapshot.SMTPPassword, password) {
		t.Fatalf("snapshot=%#v password=%q", snapshot, snapshot.SMTPPassword)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, password) || strings.Contains(snapshot.Checksum, string(password)) {
		t.Fatal("mail snapshot evidence exposed the SMTP password")
	}
}

func TestReadMailSettingsSnapshotUsesLegacyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mail-defaults.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT)`)
	_ = database.Close()
	snapshot, err := ReadMailSettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.ClearSecrets()
	settings := snapshot.Settings
	if settings.SMTPEnabled || settings.SMTPHost != "" || settings.SMTPPort != 587 || settings.SMTPUsername != "" ||
		settings.SMTPPasswordConfigured || settings.SMTPEncryption != "starttls" || settings.SMTPFromAddress != "" || settings.RemindMailEnabled {
		t.Fatalf("defaults=%#v", settings)
	}
}

func TestReadMailSettingsSnapshotRejectsUnsafeOrIncompleteData(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":          `('email_host','smtp.example.test'),('email_host','other.example.test')`,
		"invalid port":       `('email_host','smtp.example.test'),('email_port','0'),('email_from_address','a@example.test')`,
		"missing from":       `('email_host','smtp.example.test')`,
		"username no secret": `('email_host','smtp.example.test'),('email_username','mailer'),('email_from_address','a@example.test')`,
		"cleartext":          `('email_host','smtp.example.test'),('email_encryption',''),('email_from_address','a@example.test')`,
		"orphan field":       `('email_port','465')`,
		"orphan reminder":    `('remind_mail_enable','1')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-mail.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadMailSettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("unsafe legacy mail settings were accepted")
			}
		})
	}
}

func TestParseLegacyMailSecretPreservesBytesAndRecognizesNull(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("null"), []byte(" \tNuLl\r\n")} {
		secret, configured, err := parseLegacyMailSecret(raw)
		if err != nil || configured || secret != nil {
			t.Fatalf("parseLegacyMailSecret(%q)=(%q,%t,%v)", raw, secret, configured, err)
		}
	}
	raw := []byte(" credential-bytes\r\n")
	secret, configured, err := parseLegacyMailSecret(raw)
	if err != nil || !configured || !bytes.Equal(secret, raw) {
		t.Fatalf("parseLegacyMailSecret()=(%q,%t,%v)", secret, configured, err)
	}
	zeroLegacyBytes(secret)
}

func TestParseLegacyMailEncryptionMatchesXboardSemantics(t *testing.T) {
	for name, test := range map[string]struct {
		raw  []byte
		want string
	}{
		"default": {nil, "starttls"},
		"tls":     {[]byte("tls"), "starttls"},
		"ssl":     {[]byte("ssl"), "tls"},
		"empty":   {[]byte{}, "none"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseLegacyMailEncryption(test.raw, "starttls")
			if err != nil || got != test.want {
				t.Fatalf("parseLegacyMailEncryption(%q)=(%q,%v), want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func BenchmarkReadMailSettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-mail-benchmark.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('email_host','smtp.example.test'),('email_port','587'),('email_username','mailer'),
		('email_password','benchmark-secret'),('email_encryption','tls'),
		('email_from_address','support@example.test'),('remind_mail_enable','1')`); err != nil {
		b.Fatal(err)
	}
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		snapshot, err := ReadMailSettingsSnapshot(b.Context(), path)
		if err != nil {
			b.Fatal(err)
		}
		snapshot.ClearSecrets()
	}
}
