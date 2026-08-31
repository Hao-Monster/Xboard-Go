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

type legacyInvitationCodesMigrationCommandResult struct {
	Status         string                                  `json:"status"`
	Action         string                                  `json:"action"`
	Source         legacyMigrationSourceResult             `json:"source"`
	RollbackBackup legacyMigrationBackupResult             `json:"rollback_backup"`
	Result         store.LegacyInvitationCodesImportReport `json:"result"`
}

func runLegacyInvitationCodesMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-invitation-codes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-invitation-codes requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-invitation-codes requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadInvitationCodesSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	defer snapshot.ClearSecrets()
	configuration, err := config.Load()
	if err != nil {
		return true, fmt.Errorf("load invitation code migration configuration: %w", err)
	}
	defer clearMigrationKey(configuration.SettingsEncryptionKey)
	if len(snapshot.Codes) != 0 && len(configuration.SettingsEncryptionKey) != 32 {
		return true, errors.New("legacy invitation code migration requires XBOARD_SETTINGS_ENCRYPTION_KEY")
	}
	targetDSN := configuration.DatabaseDSN
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy invitation code migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy invitation code migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy invitation code migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy invitation code migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy invitation code migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy invitation code migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy invitation code migration target validation failed: %w", err)
	}
	protector, err := initializeInvitationProtector(ctx, database, configuration.SettingsEncryptionKey)
	if err != nil {
		_ = database.Close()
		return true, fmt.Errorf("validate invitation code migration encryption key: %w", err)
	}
	if len(snapshot.Codes) != 0 && protector == nil {
		_ = database.Close()
		return true, errors.New("legacy invitation code migration requires XBOARD_SETTINGS_ENCRYPTION_KEY")
	}
	existing, found, err := database.LookupLegacyInvitationCodesImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy invitation code migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed invitation code migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy invitation code rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy invitation code rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyInvitationCodesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	prepared, checksum, err := snapshot.Prepare(protector)
	if err != nil {
		return true, err
	}
	defer legacymigration.ClearPreparedInvitationCodes(prepared)
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy invitation code migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy invitation code rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import invitation code rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import invitation code rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import invitation code rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	report, importErr := database.ImportLegacyInvitationCodes(ctx, store.LegacyInvitationCodesImport{
		Slice: store.LegacyInvitationCodesSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Codes: prepared, Checksum: checksum,
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
	return true, encodeLegacyInvitationCodesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyInvitationCodesMigrationResult(output io.Writer, snapshot legacymigration.InvitationCodesSnapshot, rollback legacyMigrationBackupResult, report store.LegacyInvitationCodesImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyInvitationCodesMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-invitation-codes",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}
