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

type legacyOperationalLogsMigrationCommandResult struct {
	Status         string                                  `json:"status"`
	Action         string                                  `json:"action"`
	Source         legacyMigrationSourceResult             `json:"source"`
	RollbackBackup legacyMigrationBackupResult             `json:"rollback_backup"`
	Result         store.LegacyOperationalLogsImportReport `json:"result"`
}

func runLegacyOperationalLogsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-operational-logs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	asOfFlag := flags.String("as-of", "", "fixed RFC3339 retention boundary; defaults to now on first import and reuses the recorded boundary on replay")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-operational-logs requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-operational-logs requires --confirm-offline after the target application is stopped")
	}

	migrationNow := now().UTC()
	asOf := migrationNow
	if strings.TrimSpace(*asOfFlag) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*asOfFlag))
		if err != nil {
			return true, fmt.Errorf("--as-of must be RFC3339: %w", err)
		}
		asOf = parsed.UTC()
	}
	if asOf.After(migrationNow) {
		return true, errors.New("--as-of must not be in the future")
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy operational log migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy operational log migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy operational log migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy operational log migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(*sourcePath)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy operational log migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy operational log migration source and Xboard-Go target must be different files")
	}
	snapshot, err := legacymigration.ReadOperationalLogsSnapshot(ctx, *sourcePath, asOf)
	if err != nil {
		return true, err
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy operational log migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyOperationalLogsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy operational log migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*asOfFlag) != "" && asOf.Unix() != existing.AsOf {
			return true, errors.New("--as-of must match the fixed boundary recorded by the completed operational log migration")
		}
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed operational log migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy operational log rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy operational log rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyOperationalLogsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy operational log migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy operational log rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, migrationNow, configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import operational log rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import operational log rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import operational log rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyOperationalLogsImport{
		Slice: store.LegacyOperationalLogsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Logs: snapshot.Logs, Checksum: snapshot.Checksum,
		UserFactsChecksum: snapshot.UserFactsChecksum,
		AsOf:              snapshot.AsOf, CutoffAt: snapshot.CutoffAt, ExcludedRows: snapshot.ExcludedRows, ExcludedFailedJobs: snapshot.ExcludedFailedJobs,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyOperationalLogs(ctx, input, migrationNow)
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
	return true, encodeLegacyOperationalLogsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyOperationalLogsMigrationResult(output io.Writer, snapshot legacymigration.OperationalLogsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyOperationalLogsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyOperationalLogsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-operational-logs",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
