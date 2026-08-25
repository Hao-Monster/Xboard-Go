package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

const (
	formatVersion       = 1
	manifestName        = "manifest.json"
	databaseName        = "database.sqlite"
	maxManifestBytes    = 64 << 10
	maxArchiveBytes     = 64 << 30
	maxDatabaseBytes    = 64 << 30
	maxRevisionBytes    = 256
	backupFileMode      = 0o600
	backupDirectoryMode = 0o700
)

type Manifest struct {
	FormatVersion  int       `json:"format_version"`
	CreatedAt      time.Time `json:"created_at"`
	AppRevision    string    `json:"app_revision"`
	SchemaVersion  int       `json:"schema_version"`
	DatabaseSize   int64     `json:"database_size"`
	DatabaseSHA256 string    `json:"database_sha256"`
}

func Create(ctx context.Context, sourceDSN, outputPath, appRevision string, now time.Time) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	appRevision = strings.TrimSpace(appRevision)
	if appRevision == "" || len(appRevision) > maxRevisionBytes {
		return Manifest{}, errors.New("app revision must contain between 1 and 256 bytes")
	}
	if now.IsZero() {
		return Manifest{}, errors.New("backup creation time is required")
	}
	if _, err := sqliteFileFromDSN(sourceDSN); err != nil {
		return Manifest{}, err
	}
	outputPath, err := prepareNewOutputPath(outputPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare backup output: %w", err)
	}

	snapshotPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-backup-snapshot-*.sqlite")
	if err != nil {
		return Manifest{}, fmt.Errorf("reserve backup snapshot path: %w", err)
	}
	defer os.Remove(snapshotPath)
	if err := createSQLiteSnapshot(ctx, sourceDSN, snapshotPath); err != nil {
		return Manifest{}, err
	}
	if err := os.Chmod(snapshotPath, backupFileMode); err != nil {
		return Manifest{}, fmt.Errorf("restrict backup snapshot permissions: %w", err)
	}

	schemaVersion, databaseSize, err := validateSQLite(ctx, snapshotPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("validate backup snapshot: %w", err)
	}
	databaseDigest, err := fileSHA256(ctx, snapshotPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("hash backup snapshot: %w", err)
	}
	manifest := Manifest{
		FormatVersion: formatVersion, CreatedAt: now.UTC(), AppRevision: appRevision,
		SchemaVersion: schemaVersion, DatabaseSize: databaseSize, DatabaseSHA256: databaseDigest,
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}

	archivePath, err := writeArchive(ctx, filepath.Dir(outputPath), manifest, snapshotPath)
	if err != nil {
		return Manifest{}, err
	}
	defer os.Remove(archivePath)
	if err := publishNoReplace(archivePath, outputPath); err != nil {
		return Manifest{}, fmt.Errorf("publish backup archive: %w", err)
	}
	return manifest, nil
}

func Verify(ctx context.Context, inputPath string) (Manifest, error) {
	directory, err := os.MkdirTemp("", "xboard-backup-verify-")
	if err != nil {
		return Manifest{}, fmt.Errorf("create verification directory: %w", err)
	}
	defer os.RemoveAll(directory)
	databasePath := filepath.Join(directory, databaseName)
	manifest, err := extractAndVerify(ctx, inputPath, databasePath)
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Restore(ctx context.Context, inputPath, outputPath string) (Manifest, error) {
	outputPath, err := prepareNewOutputPath(outputPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare restore output: %w", err)
	}
	temporaryPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-restore-*.sqlite")
	if err != nil {
		return Manifest{}, fmt.Errorf("reserve restore path: %w", err)
	}
	defer os.Remove(temporaryPath)

	manifest, err := extractAndVerify(ctx, inputPath, temporaryPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := publishNoReplace(temporaryPath, outputPath); err != nil {
		return Manifest{}, fmt.Errorf("publish restored database: %w", err)
	}
	return manifest, nil
}

func createSQLiteSnapshot(ctx context.Context, sourceDSN, destinationPath string) error {
	database, err := sql.Open("sqlite", appendDSNOptions(sourceDSN, "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"))
	if err != nil {
		return fmt.Errorf("open backup source: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping backup source: %w", err)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return fmt.Errorf("read backup source schema: %w", err)
	}
	if schemaVersion < 1 || schemaVersion > store.CurrentSchemaVersion() {
		return fmt.Errorf("unsupported schema version %d (supported 1-%d)", schemaVersion, store.CurrentSchemaVersion())
	}
	if _, err := database.ExecContext(ctx, `VACUUM INTO ?`, destinationPath); err != nil {
		return fmt.Errorf("create consistent SQLite snapshot: %w", err)
	}
	return nil
}

func writeArchive(ctx context.Context, directory string, manifest Manifest, databasePath string) (string, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode backup manifest: %w", err)
	}
	file, err := os.CreateTemp(directory, ".xboard-backup-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create backup archive: %w", err)
	}
	path := file.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(backupFileMode); err != nil {
		return "", fmt.Errorf("restrict backup archive permissions: %w", err)
	}
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestSpeed)
	if err != nil {
		return "", fmt.Errorf("create backup compressor: %w", err)
	}
	gzipWriter.Header.ModTime = manifest.CreatedAt
	tarWriter := tar.NewWriter(gzipWriter)
	writeEntry := func(name string, size int64, reader io.Reader) error {
		header := &tar.Header{
			Name: name, Mode: backupFileMode, Size: size, ModTime: manifest.CreatedAt,
			Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		_, err := io.CopyN(tarWriter, &contextReader{ctx: ctx, reader: reader}, size)
		return err
	}
	if err := writeEntry(manifestName, int64(len(manifestJSON)), strings.NewReader(string(manifestJSON))); err != nil {
		return "", fmt.Errorf("write backup manifest: %w", err)
	}
	database, err := os.Open(databasePath)
	if err != nil {
		return "", fmt.Errorf("open backup snapshot: %w", err)
	}
	if err := writeEntry(databaseName, manifest.DatabaseSize, database); err != nil {
		_ = database.Close()
		return "", fmt.Errorf("write backup database: %w", err)
	}
	if err := database.Close(); err != nil {
		return "", fmt.Errorf("close backup snapshot: %w", err)
	}
	if err := tarWriter.Close(); err != nil {
		return "", fmt.Errorf("finalize backup tar: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return "", fmt.Errorf("finalize backup compression: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync backup archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close backup archive: %w", err)
	}
	succeeded = true
	return path, nil
}

func extractAndVerify(ctx context.Context, inputPath, databasePath string) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	archiveInfo, err := os.Lstat(inputPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() || archiveInfo.Size() <= 0 || archiveInfo.Size() > maxArchiveBytes {
		return Manifest{}, errors.New("backup archive must be a non-empty regular file within the size limit")
	}
	archive, err := os.Open(inputPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup archive: %w", err)
	}
	defer archive.Close()
	buffered := bufio.NewReader(archive)
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup compression: %w", err)
	}
	gzipReader.Multistream(false)
	defer gzipReader.Close()
	tarReader := tar.NewReader(&contextReader{ctx: ctx, reader: gzipReader})

	manifestHeader, err := tarReader.Next()
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest header: %w", err)
	}
	if manifestHeader.Name != manifestName || manifestHeader.Typeflag != tar.TypeReg || manifestHeader.Size <= 0 || manifestHeader.Size > maxManifestBytes {
		return Manifest{}, errors.New("backup archive must begin with a bounded regular manifest.json")
	}
	manifestBytes := make([]byte, manifestHeader.Size)
	if _, err := io.ReadFull(tarReader, manifestBytes); err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("backup manifest contains trailing JSON values")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}

	databaseHeader, err := tarReader.Next()
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup database header: %w", err)
	}
	if databaseHeader.Name != databaseName || databaseHeader.Typeflag != tar.TypeReg || databaseHeader.Size != manifest.DatabaseSize {
		return Manifest{}, errors.New("backup database entry does not match the manifest")
	}
	database, err := os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return Manifest{}, fmt.Errorf("create extracted database: %w", err)
	}
	extractionSucceeded := false
	defer func() {
		_ = database.Close()
		if !extractionSucceeded {
			_ = os.Remove(databasePath)
		}
	}()
	hash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(database, hash), &contextReader{ctx: ctx, reader: tarReader}, manifest.DatabaseSize)
	if err != nil {
		return Manifest{}, fmt.Errorf("extract backup database: %w", err)
	}
	if written != manifest.DatabaseSize || hex.EncodeToString(hash.Sum(nil)) != manifest.DatabaseSHA256 {
		return Manifest{}, errors.New("backup database size or SHA-256 does not match the manifest")
	}
	if _, err := tarReader.Next(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("backup archive contains unexpected entries")
		}
		return Manifest{}, fmt.Errorf("finish reading backup archive: %w", err)
	}
	if _, err := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: gzipReader}); err != nil {
		return Manifest{}, fmt.Errorf("validate backup compression checksum: %w", err)
	}
	if _, err := buffered.Peek(1); !errors.Is(err, io.EOF) {
		if err == nil {
			return Manifest{}, errors.New("backup archive contains trailing compressed data")
		}
		return Manifest{}, fmt.Errorf("inspect backup archive trailer: %w", err)
	}
	if err := database.Sync(); err != nil {
		return Manifest{}, fmt.Errorf("sync extracted database: %w", err)
	}
	if err := database.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close extracted database: %w", err)
	}
	if err := os.Chmod(databasePath, backupFileMode); err != nil {
		return Manifest{}, fmt.Errorf("restrict extracted database permissions: %w", err)
	}

	schemaVersion, size, err := validateSQLite(ctx, databasePath)
	if err != nil {
		return Manifest{}, fmt.Errorf("validate extracted database: %w", err)
	}
	if schemaVersion != manifest.SchemaVersion || size != manifest.DatabaseSize {
		return Manifest{}, errors.New("extracted database metadata does not match the manifest")
	}
	extractionSucceeded = true
	return manifest, nil
}

func validateSQLite(ctx context.Context, path string) (int, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxDatabaseBytes {
		return 0, 0, errors.New("database must be a non-empty regular file within the size limit")
	}
	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return 0, 0, err
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return 0, 0, err
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return 0, 0, err
	}
	if schemaVersion < 1 || schemaVersion > store.CurrentSchemaVersion() {
		return 0, 0, fmt.Errorf("unsupported schema version %d (supported 1-%d)", schemaVersion, store.CurrentSchemaVersion())
	}
	if err := store.ValidateSchema(ctx, database, schemaVersion); err != nil {
		return 0, 0, err
	}
	rows, err := database.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return 0, 0, err
	}
	var integrityMessages []string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		integrityMessages = append(integrityMessages, message)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(integrityMessages) != 1 || integrityMessages[0] != "ok" {
		return 0, 0, fmt.Errorf("SQLite integrity check failed: %s", strings.Join(integrityMessages, "; "))
	}
	foreignKeys, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, 0, err
	}
	foreignKeyViolation := foreignKeys.Next()
	foreignKeyErr := foreignKeys.Err()
	if err := foreignKeys.Close(); err != nil {
		return 0, 0, err
	}
	if foreignKeyErr != nil {
		return 0, 0, foreignKeyErr
	}
	if foreignKeyViolation {
		return 0, 0, errors.New("SQLite foreign key check failed")
	}
	return schemaVersion, info.Size(), nil
}

func validateManifest(manifest Manifest) error {
	if manifest.FormatVersion != formatVersion {
		return fmt.Errorf("unsupported backup format version %d", manifest.FormatVersion)
	}
	if manifest.CreatedAt.IsZero() {
		return errors.New("backup manifest creation time is required")
	}
	if revision := strings.TrimSpace(manifest.AppRevision); revision == "" || revision != manifest.AppRevision || len(revision) > maxRevisionBytes {
		return errors.New("backup manifest app revision is invalid")
	}
	if manifest.SchemaVersion < 1 || manifest.SchemaVersion > store.CurrentSchemaVersion() {
		return fmt.Errorf("unsupported schema version %d (supported 1-%d)", manifest.SchemaVersion, store.CurrentSchemaVersion())
	}
	if manifest.DatabaseSize <= 0 || manifest.DatabaseSize > maxDatabaseBytes {
		return errors.New("backup manifest database size is outside the supported range")
	}
	if len(manifest.DatabaseSHA256) != sha256.Size*2 {
		return errors.New("backup manifest database SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(manifest.DatabaseSHA256); err != nil || manifest.DatabaseSHA256 != strings.ToLower(manifest.DatabaseSHA256) {
		return errors.New("backup manifest database SHA-256 is invalid")
	}
	return nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sqliteFileFromDSN(dsn string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if !strings.HasPrefix(dsn, "file:") {
		return "", errors.New("database DSN must reference a local SQLite file")
	}
	withoutScheme := strings.TrimPrefix(strings.SplitN(dsn, "?", 2)[0], "file:")
	if withoutScheme == "" || withoutScheme == ":memory:" || strings.Contains(strings.ToLower(dsn), "mode=memory") || strings.HasPrefix(withoutScheme, "//") {
		return "", errors.New("database DSN must reference a local SQLite file")
	}
	info, err := os.Lstat(withoutScheme)
	if err != nil {
		return "", fmt.Errorf("inspect SQLite source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("SQLite source must be a regular file")
	}
	return withoutScheme, nil
}

func prepareNewOutputPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("output path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("destination already exists: %s", absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), backupDirectoryMode); err != nil {
		return "", err
	}
	if err := os.Chmod(filepath.Dir(absolute), backupDirectoryMode); err != nil {
		return "", err
	}
	return absolute, nil
}

func unusedTemporaryPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func publishNoReplace(sourcePath, destinationPath string) error {
	if err := os.Link(sourcePath, destinationPath); err != nil {
		if _, statErr := os.Lstat(destinationPath); statErr == nil {
			return fmt.Errorf("destination already exists: %s", destinationPath)
		}
		return err
	}
	if err := os.Chmod(destinationPath, backupFileMode); err != nil {
		_ = os.Remove(destinationPath)
		return err
	}
	if err := os.Remove(sourcePath); err != nil {
		_ = os.Remove(destinationPath)
		return err
	}
	return nil
}

func appendDSNOptions(dsn, options string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + options
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
