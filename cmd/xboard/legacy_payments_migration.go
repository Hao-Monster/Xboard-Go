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
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func runLegacyPaymentsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-payments", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-payments requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-payments requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadPaymentsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	settings, err := config.Load()
	if err != nil {
		return true, fmt.Errorf("load payment migration configuration: %w", err)
	}
	defer clearMigrationKey(settings.SettingsEncryptionKey)
	if len(settings.SettingsEncryptionKey) != 32 {
		return true, errors.New("legacy payment migration requires XBOARD_SETTINGS_ENCRYPTION_KEY")
	}
	targetDSN := settings.DatabaseDSN
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy payment migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy payment migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy payment migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy payment migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy payment migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy payment migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy payment migration target validation failed: %w", err)
	}
	cipherBox, err := initializeSettingsCipher(ctx, database, settings.SettingsEncryptionKey)
	if err != nil {
		_ = database.Close()
		return true, fmt.Errorf("validate payment migration encryption key: %w", err)
	}
	existing, found, err := database.LookupLegacyPaymentsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy payment migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed payment migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy payment rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy payment rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyPaymentsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	prepared, err := prepareLegacyPayments(snapshot, cipherBox)
	if err != nil {
		return true, err
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy payment migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy payment rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC())
	if err != nil {
		return true, fmt.Errorf("create pre-import payment rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import payment rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import payment rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyPaymentsImport{
		Slice: store.LegacyPaymentsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Payments: prepared, PaymentsChecksum: store.LegacyPaymentsChecksum(prepared),
		PlaintextSourceChecksum: snapshot.PaymentsChecksum,
		RollbackBackupPath:      rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyPayments(ctx, input, now().UTC())
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
	return true, encodeLegacyPaymentsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func prepareLegacyPayments(snapshot legacymigration.PaymentsSnapshot, cipherBox *appsettings.Cipher) ([]store.LegacyPayment, error) {
	prepared := make([]store.LegacyPayment, 0, len(snapshot.Payments))
	for _, item := range snapshot.Payments {
		ciphertext, err := payment.SealConfig(cipherBox, item.Provider, item.Config)
		if err != nil {
			return nil, fmt.Errorf("encrypt legacy payment id %d configuration: %w", item.ID, err)
		}
		prepared = append(prepared, store.LegacyPayment{
			ID: item.ID, UUID: item.UUID, Provider: item.Provider, Name: item.Name, Icon: item.Icon,
			ConfigCiphertext: ciphertext, NotifyDomain: item.NotifyDomain, HandlingFeeFixed: item.HandlingFeeFixed,
			HandlingFeeBasisPoints: item.HandlingFeeBasisPoints, Enabled: item.Enabled,
			SortPosition: item.SortPosition, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		})
	}
	if err := store.ValidateLegacyPaymentsData(prepared); err != nil {
		return nil, fmt.Errorf("validate prepared legacy payments: %w", err)
	}
	return prepared, nil
}

func encodeLegacyPaymentsMigrationResult(output io.Writer, snapshot legacymigration.PaymentsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyPaymentsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyPaymentsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-payments",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func clearMigrationKey(key []byte) {
	for index := range key {
		key[index] = 0
	}
}
