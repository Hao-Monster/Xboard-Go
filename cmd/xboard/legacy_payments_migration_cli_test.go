package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyPaymentsEncryptedWithIndependentRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyPaymentsCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "pre-payments.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x5a}, 32)
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY_FILE", "")
	now := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)

	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{"migration", "import-legacy-payments", "--source", sourcePath, "--backup-output", rollbackPath}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("missing confirmation = handled %v error %v stdout=%q", handled, err, blockedOut.String())
	}

	result, raw := runLegacyPaymentsCommand(t, []string{"migration", "import-legacy-payments", "--source", sourcePath, "--backup-output", rollbackPath, "--confirm-offline"}, now)
	if result.Status != "success" || result.Action != "migration.import-legacy-payments" || result.Result.AlreadyApplied ||
		result.Result.Payments.SourceRows != 1 || result.Result.Payments.SourceChecksum != result.Result.Payments.TargetChecksum ||
		result.Result.PlaintextSourceChecksum == "" || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("migration output = %#v", result)
	}
	for _, secret := range []string{"merchant-cli", "ipn-secret-cli"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("migration output leaked payment credential %q: %s", secret, raw)
		}
	}
	if verified, err := backup.Verify(t.Context(), rollbackPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify(rollback) = (%#v, %v)", verified, err)
	}

	inspection, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	method, err := inspection.GetPayment(t.Context(), 17)
	if err != nil || method.UUID != "paycli17" || method.HandlingFeeFixed != 123 || method.HandlingFeeBasisPoints != 250 {
		t.Fatalf("imported method = (%#v, %v)", method, err)
	}
	if bytes.Contains(method.ConfigCiphertext, []byte("ipn-secret-cli")) {
		t.Fatalf("payment credential was stored in plaintext: %q", method.ConfigCiphertext)
	}
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	config, err := payment.OpenConfig(cipherBox, method.Provider, method.ConfigCiphertext)
	if err != nil || config["coinpayments_merchant_id"] != "merchant-cli" || config["coinpayments_ipn_secret"] != "ipn-secret-cli" {
		t.Fatalf("decrypted imported config = (%#v, %v)", config, err)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	repeated, _ := runLegacyPaymentsCommand(t, []string{"migration", "import-legacy-payments", "--source", sourcePath, "--confirm-offline"}, now.Add(time.Minute))
	if !repeated.Result.AlreadyApplied || repeated.Result.AppliedAt != now || repeated.RollbackBackup.SHA256 != result.RollbackBackup.SHA256 {
		t.Fatalf("idempotent output = %#v", repeated)
	}
}

func TestRunCommandRejectsLegacyPaymentsWithoutEncryptionKeyBeforeBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := createLegacyPaymentsCLIInput(t, directory)
	targetPath := filepath.Join(directory, "target.db")
	rollbackPath := filepath.Join(directory, "must-not-exist.xbbackup")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", "")
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY_FILE", "")
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-payments", "--source", sourcePath,
		"--backup-output", rollbackPath, "--confirm-offline",
	}, &stdout, &stderr, time.Now)
	if !handled || err == nil || !strings.Contains(err.Error(), "XBOARD_SETTINGS_ENCRYPTION_KEY") || stdout.Len() != 0 {
		t.Fatalf("missing key = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if _, statErr := os.Stat(rollbackPath); !os.IsNotExist(statErr) {
		t.Fatalf("backup was created before encryption validation: %v", statErr)
	}
}

func runLegacyPaymentsCommand(t *testing.T, arguments []string, now time.Time) (legacyPaymentsMigrationCommandResult, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	var output legacyPaymentsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode migration output %q: %v", stdout.String(), err)
	}
	return output, stdout.String()
}

func createLegacyPaymentsCLIInput(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "legacy-payments.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		CREATE TABLE v2_payment (
			id INTEGER PRIMARY KEY, uuid TEXT, payment TEXT, name TEXT, icon TEXT, config TEXT, notify_domain TEXT,
			handling_fee_fixed INTEGER, handling_fee_percent NUMERIC, enable INTEGER, sort INTEGER,
			created_at INTEGER, updated_at INTEGER
		);
		INSERT INTO v2_payment VALUES (
			17, 'paycli17', 'CoinPayments', 'CLI CoinPayments', 'https://cdn.example.test/payment.svg',
			'{"coinpayments_merchant_id":"merchant-cli","coinpayments_ipn_secret":"ipn-secret-cli","coinpayments_currency":"CNY"}',
			'https://notify.example.test', 123, 2.5, 1, 3, 1700000000, 1700000100
		);
	`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
