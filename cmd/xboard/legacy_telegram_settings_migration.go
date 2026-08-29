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

type legacyTelegramSettingsMigrationCommandResult struct {
	Status         string                                   `json:"status"`
	Action         string                                   `json:"action"`
	Source         legacyMigrationSourceResult              `json:"source"`
	RollbackBackup legacyMigrationBackupResult              `json:"rollback_backup"`
	Result         store.LegacyTelegramSettingsImportReport `json:"result"`
}

func runLegacyTelegramSettingsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-telegram-settings", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-telegram-settings requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-telegram-settings requires --confirm-offline after the target application is stopped")
	}
	snapshot, err := legacymigration.ReadTelegramSettingsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	defer snapshot.ClearSecrets()
	settings, err := config.Load()
	if err != nil {
		return true, fmt.Errorf("load Telegram migration configuration: %w", err)
	}
	defer clearMigrationKey(settings.SettingsEncryptionKey)
	if snapshot.Settings.BotTokenConfigured && len(settings.SettingsEncryptionKey) != 32 {
		return true, errors.New("legacy Telegram settings migration requires XBOARD_SETTINGS_ENCRYPTION_KEY when a bot token is configured")
	}
	targetDSN := settings.DatabaseDSN
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy Telegram settings migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy Telegram settings migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy Telegram settings migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy Telegram settings migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy Telegram settings migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy Telegram settings migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy Telegram settings migration target validation failed: %w", err)
	}
	cipherBox, err := initializeSettingsCipher(ctx, database, settings.SettingsEncryptionKey)
	if err != nil {
		_ = database.Close()
		return true, fmt.Errorf("validate Telegram migration encryption key: %w", err)
	}
	existing, found, err := database.LookupLegacyTelegramSettingsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy Telegram settings migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed Telegram settings migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded Telegram settings rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded Telegram settings rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyTelegramSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}
	prepared := snapshot.Settings
	if prepared.BotTokenConfigured {
		if cipherBox == nil {
			return true, errors.New("legacy Telegram settings migration encryption is unavailable")
		}
		prepared.BotTokenCipher, err = cipherBox.EncryptFor(appsettings.TelegramBotTokenPurpose, snapshot.BotToken)
		if err != nil {
			return true, errors.New("encrypt legacy Telegram bot token")
		}
		verification, err := cipherBox.DecryptFor(appsettings.TelegramBotTokenPurpose, prepared.BotTokenCipher)
		if err != nil || subtle.ConstantTimeCompare(verification, snapshot.BotToken) != 1 {
			clearMigrationKey(verification)
			return true, errors.New("verify encrypted legacy Telegram bot token")
		}
		clearMigrationKey(verification)
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy Telegram settings migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy Telegram settings rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import Telegram settings rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import Telegram settings rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import Telegram settings rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacyTelegramSettings(ctx, store.LegacyTelegramSettingsImport{
		Slice: store.LegacyTelegramSettingsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
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
	return true, encodeLegacyTelegramSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyTelegramSettingsMigrationResult(output io.Writer, snapshot legacymigration.TelegramSettingsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyTelegramSettingsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyTelegramSettingsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-telegram-settings",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
