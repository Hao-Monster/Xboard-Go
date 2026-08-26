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

func runLegacyDistributorsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-distributors", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-distributors requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-distributors requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadDistributorsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy distributor migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy distributor migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy distributor migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy distributor migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy distributor migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy distributor migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy distributor migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyDistributorsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy distributor migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed distributor migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy distributor rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy distributor rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyDistributorsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy distributor migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy distributor rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import distributor rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import distributor rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import distributor rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacyDistributors(ctx, store.LegacyDistributorsImport{
		Slice: store.LegacyDistributorsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Data: snapshot.Data, Checksum: snapshot.Checksum,
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
	return true, encodeLegacyDistributorsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyDistributorsMigrationResult(output io.Writer, snapshot legacymigration.DistributorsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyDistributorsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyDistributorsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-distributors",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
