package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/config"
	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type legacyMailSettingsMigrationCommandResult struct {
	Status         string                               `json:"status"`
	Action         string                               `json:"action"`
	Source         legacyMigrationSourceResult          `json:"source"`
	RollbackBackup legacyMigrationBackupResult          `json:"rollback_backup"`
	Result         store.LegacyMailSettingsImportReport `json:"result"`
}

func runLegacyMailSettingsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-mail-settings", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-mail-settings requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-mail-settings requires --confirm-offline after the target application is stopped")
	}
	snapshot, err := legacymigration.ReadMailSettingsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	defer snapshot.ClearSecrets()
	configuration, err := config.Load()
	if err != nil {
		return true, fmt.Errorf("load mail settings migration configuration: %w", err)
	}
	defer clearMigrationKey(configuration.SettingsEncryptionKey)
	if snapshot.Settings.SMTPPasswordConfigured && len(configuration.SettingsEncryptionKey) != 32 {
		return true, errors.New("legacy mail settings migration requires XBOARD_SETTINGS_ENCRYPTION_KEY when an SMTP password is configured")
	}
	targetDSN := configuration.DatabaseDSN
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy mail settings migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy mail settings migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy mail settings migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy mail settings migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy mail settings migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy mail settings migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy mail settings migration target validation failed: %w", err)
	}
	cipherBox, err := initializeSettingsCipher(ctx, database, configuration.SettingsEncryptionKey)
	if err != nil {
		_ = database.Close()
		return true, fmt.Errorf("validate mail settings migration encryption key: %w", err)
	}
	existing, found, err := database.LookupLegacyMailSettingsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy mail settings migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed mail settings migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded mail settings rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded mail settings rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyMailSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	prepared := snapshot.Settings
	defer clearMigrationKey(prepared.SMTPPasswordCipher)
	if prepared.SMTPPasswordConfigured {
		prepared.SMTPPasswordCipher, err = encryptAndVerifyLegacyMailPassword(cipherBox, snapshot.SMTPPassword)
		if err != nil {
			return true, errors.New("encrypt legacy SMTP password")
		}
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy mail settings migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy mail settings rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import mail settings rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import mail settings rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import mail settings rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacyMailSettings(ctx, store.LegacyMailSettingsImport{
		Slice: store.LegacyMailSettingsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Settings: prepared, Checksum: snapshot.Checksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyMailSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encryptAndVerifyLegacyMailPassword(cipherBox *appsettings.Cipher, plaintext []byte) ([]byte, error) {
	if cipherBox == nil {
		return nil, errors.New("settings encryption is unavailable")
	}
	ciphertext, err := cipherBox.Encrypt(plaintext)
	if err != nil {
		return nil, err
	}
	verification, err := cipherBox.Decrypt(ciphertext)
	if err != nil || subtle.ConstantTimeCompare(verification, plaintext) != 1 {
		clearMigrationKey(verification)
		clearMigrationKey(ciphertext)
		return nil, errors.New("encrypted SMTP password verification failed")
	}
	clearMigrationKey(verification)
	return ciphertext, nil
}

func encodeLegacyMailSettingsMigrationResult(output io.Writer, snapshot legacymigration.MailSettingsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyMailSettingsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyMailSettingsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-mail-settings",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
