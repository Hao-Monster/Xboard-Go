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

type legacyThemeSettingsMigrationCommandResult struct {
	Status         string                                `json:"status"`
	Action         string                                `json:"action"`
	Source         legacyMigrationSourceResult           `json:"source"`
	RollbackBackup legacyMigrationBackupResult           `json:"rollback_backup"`
	Result         store.LegacyThemeSettingsImportReport `json:"result"`
}

func runLegacyThemeSettingsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-theme-settings", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-theme-settings requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-theme-settings requires --confirm-offline after the target application is stopped")
	}
	snapshot, err := legacymigration.ReadThemeSettingsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy theme settings migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy theme settings migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil || !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy theme settings migration target must be an existing regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy theme settings migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy theme settings migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy theme settings migration target validation failed: %w", err)
	}
	existing, found, lookupErr := database.LookupLegacyThemeSettingsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if lookupErr != nil {
		return true, lookupErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy theme settings migration target: %w", closeErr)
	}
	if found {
		rollback, err := verifyRecordedThemeSettingsRollback(ctx, existing, *backupOutput)
		if err != nil {
			return true, err
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyThemeSettingsMigrationResult(stdout, snapshot, rollback, existing)
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy theme settings migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy theme settings rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	created, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import theme settings rollback backup: %w", err)
	}
	verified, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import theme settings rollback backup: %w", err)
	}
	if created != verified {
		return true, errors.New("pre-import theme settings rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacyThemeSettings(ctx, store.LegacyThemeSettingsImport{
		Slice: store.LegacyThemeSettingsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Settings: snapshot.Settings, Checksum: snapshot.Checksum,
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
	return true, encodeLegacyThemeSettingsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{Path: rollbackPath, SHA256: rollbackDigest, Manifest: verified}, report)
}

func verifyRecordedThemeSettingsRollback(ctx context.Context, existing store.LegacyThemeSettingsImportReport, requestedOutput string) (legacyMigrationBackupResult, error) {
	if strings.TrimSpace(requestedOutput) != "" {
		requested, err := filepath.Abs(requestedOutput)
		if err != nil {
			return legacyMigrationBackupResult{}, err
		}
		recorded, err := filepath.Abs(existing.RollbackBackupPath)
		if err != nil || requested != recorded {
			return legacyMigrationBackupResult{}, errors.New("--backup-output does not match the rollback backup recorded by the completed theme settings migration")
		}
	}
	manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
	if err != nil {
		return legacyMigrationBackupResult{}, fmt.Errorf("verify recorded theme settings rollback backup: %w", err)
	}
	digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
	if err != nil {
		return legacyMigrationBackupResult{}, err
	}
	if digest != existing.RollbackBackupSHA256 {
		return legacyMigrationBackupResult{}, errors.New("recorded theme settings rollback backup digest does not match")
	}
	return legacyMigrationBackupResult{Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest}, nil
}

func encodeLegacyThemeSettingsMigrationResult(output io.Writer, snapshot legacymigration.ThemeSettingsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyThemeSettingsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyThemeSettingsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-theme-settings",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
