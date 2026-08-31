package main

import (
	"context"
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
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type legacySafeAccessSettingsMigrationCommandResult struct {
	Status         string                                     `json:"status"`
	Action         string                                     `json:"action"`
	Source         legacyMigrationSourceResult                `json:"source"`
	RollbackBackup legacyMigrationBackupResult                `json:"rollback_backup"`
	Result         store.LegacySafeAccessSettingsImportReport `json:"result"`
}

func runLegacySafeAccessSettingsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-safe-access-settings", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	sourceEffectiveSecurePath := flags.String("source-effective-secure-path", "", "old effective secure path when the snapshot has no secure_path or frontend_admin_path row")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-safe-access-settings requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-safe-access-settings requires --confirm-offline after the target application is stopped")
	}
	snapshot, err := legacymigration.ReadSafeAccessSettingsSnapshot(ctx, *sourcePath, strings.TrimSpace(*sourceEffectiveSecurePath))
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy safe access settings migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy safe access settings migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil || !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy safe access settings migration target must be an existing regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy safe access settings migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy safe access settings migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy safe access settings migration target validation failed: %w", err)
	}
	existing, found, lookupErr := database.LookupLegacySafeAccessSettingsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if lookupErr != nil {
		return true, lookupErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy safe access settings migration target: %w", closeErr)
	}
	if found {
		if existing.Settings.SourceChecksum != snapshot.Checksum {
			return true, errors.New("legacy safe access settings source options differ from the completed migration")
		}
		rollback, err := verifyRecordedSafeAccessSettingsRollback(ctx, existing, *backupOutput)
		if err != nil {
			return true, err
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacySafeAccessSettingsMigrationResult(stdout, snapshot, rollback, existing)
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy safe access settings migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy safe access settings rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	created, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import safe access settings rollback backup: %w", err)
	}
	verified, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import safe access settings rollback backup: %w", err)
	}
	if created != verified {
		return true, errors.New("pre-import safe access settings rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	fallbackSecurePath := strings.TrimSpace(os.Getenv("XBOARD_LEGACY_ADMIN_PATH"))
	if fallbackSecurePath == "" {
		fallbackSecurePath = "admin"
	}
	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacySafeAccessSettings(ctx, store.LegacySafeAccessSettingsImport{
		Slice: store.LegacySafeAccessSettingsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Settings: snapshot.Settings, Checksum: snapshot.Checksum, FallbackSecurePath: fallbackSecurePath,
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
	return true, encodeLegacySafeAccessSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{Path: rollbackPath, SHA256: rollbackDigest, Manifest: verified}, report)
}

func verifyRecordedSafeAccessSettingsRollback(ctx context.Context, existing store.LegacySafeAccessSettingsImportReport, requestedOutput string) (legacyMigrationBackupResult, error) {
	if strings.TrimSpace(requestedOutput) != "" {
		requested, err := filepath.Abs(requestedOutput)
		if err != nil {
			return legacyMigrationBackupResult{}, err
		}
		recorded, err := filepath.Abs(existing.RollbackBackupPath)
		if err != nil || requested != recorded {
			return legacyMigrationBackupResult{}, errors.New("--backup-output does not match the rollback backup recorded by the completed safe access settings migration")
		}
	}
	manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
	if err != nil {
		return legacyMigrationBackupResult{}, fmt.Errorf("verify recorded safe access settings rollback backup: %w", err)
	}
	digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
	if err != nil {
		return legacyMigrationBackupResult{}, err
	}
	if digest != existing.RollbackBackupSHA256 {
		return legacyMigrationBackupResult{}, errors.New("recorded safe access settings rollback backup digest does not match")
	}
	return legacyMigrationBackupResult{Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest}, nil
}

func encodeLegacySafeAccessSettingsMigrationResult(output io.Writer, snapshot legacymigration.SafeAccessSettingsSnapshot, rollback legacyMigrationBackupResult, report store.LegacySafeAccessSettingsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacySafeAccessSettingsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-safe-access-settings",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
