package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyMailSettingsIsComposableIdempotentAndDriftSafe(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.Exec(`UPDATE app_settings SET app_name='Migrated identity',currency='USD' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	settings := nonDefaultLegacyMailSettings()
	input := validLegacyMailSettingsImport(settings)
	now := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyMailSettings(t.Context(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyMailSettings()=(%#v,%v)", report, err)
	}
	mail, err := database.GetMailSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := database.GetSMTPPasswordCipher(t.Context())
	if err != nil || !mail.SMTPEnabled || mail.SMTPHost != "smtp.example.test" || mail.SMTPPort != 465 ||
		mail.SMTPUsername != "mailer" || !mail.SMTPPasswordSet || mail.SMTPEncryption != "tls" ||
		mail.SMTPFromAddress != "support@example.test" || !mail.RemindMailEnabled || !bytes.Equal(ciphertext, settings.SMTPPasswordCipher) {
		t.Fatalf("mail=%#v cipher=%x err=%v", mail, ciphertext, err)
	}
	repeated, err := database.ImportLegacyMailSettings(t.Context(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	if _, err := database.db.Exec(`UPDATE app_settings SET smtp_password_cipher=zeroblob(64) WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.LookupLegacyMailSettingsImport(t.Context(), input.SourceSHA256); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted lookup error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyMailSettingsRejectsNonPristineDifferentSourceAndDependentDisabledMail(t *testing.T) {
	settings := nonDefaultLegacyMailSettings()
	input := validLegacyMailSettingsImport(settings)
	nonPristine := newTestStore(t)
	if _, err := nonPristine.db.Exec(`UPDATE app_settings SET smtp_host='changed.example.test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := nonPristine.ImportLegacyMailSettings(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("non-pristine import error=%v, want ErrConflict", err)
	}

	clean := newTestStore(t)
	if _, err := clean.ImportLegacyMailSettings(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	input.SourceSHA256 = strings.Repeat("f", 64)
	if _, err := clean.ImportLegacyMailSettings(t.Context(), input, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source error=%v, want ErrConflict", err)
	}

	dependent := newTestStore(t)
	if _, err := dependent.db.Exec(`UPDATE app_settings SET email_verify=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	disabled := DefaultLegacyMailSettings()
	if _, err := dependent.ImportLegacyMailSettings(t.Context(), validLegacyMailSettingsImport(disabled), time.Unix(4, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("dependent disabled import error=%v, want ErrConflict", err)
	}
}

func nonDefaultLegacyMailSettings() LegacyMailSettings {
	return LegacyMailSettings{
		SMTPEnabled: true, SMTPHost: "smtp.example.test", SMTPPort: 465, SMTPUsername: "mailer",
		SMTPPasswordConfigured: true, SMTPPasswordCipher: bytes.Repeat([]byte{0x41}, 64),
		SMTPEncryption: "tls", SMTPFromAddress: "support@example.test", RemindMailEnabled: true,
	}
}

func validLegacyMailSettingsImport(settings LegacyMailSettings) LegacyMailSettingsImport {
	return LegacyMailSettingsImport{
		Slice: LegacyMailSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyMailSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
