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
	legacyFormatVersion = 1
	bundleFormatVersion = 2
	manifestName        = "manifest.json"
	databaseName        = "database.sqlite"
	attachmentIndexName = "attachments.json"
	attachmentsPrefix   = "attachments/"
	maxManifestBytes    = 64 << 10
	maxAttachmentIndex  = 32 << 20
	maxArchiveBytes     = 64 << 30
	maxDatabaseBytes    = 64 << 30
	maxAttachmentBytes  = 1 << 40
	maxAttachmentFiles  = 1_000_000
	maxRevisionBytes    = 256
	backupFileMode      = 0o600
	backupDirectoryMode = 0o700
)

type Manifest struct {
	FormatVersion    int       `json:"format_version"`
	CreatedAt        time.Time `json:"created_at"`
	AppRevision      string    `json:"app_revision"`
	SchemaVersion    int       `json:"schema_version"`
	DatabaseSize     int64     `json:"database_size"`
	DatabaseSHA256   string    `json:"database_sha256"`
	AttachmentCount  int       `json:"attachment_count,omitempty"`
	AttachmentSize   int64     `json:"attachment_size,omitempty"`
	AttachmentSHA256 string    `json:"attachment_index_sha256,omitempty"`
}

type AttachmentManifest struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func Create(ctx context.Context, sourceDSN, outputPath, appRevision string, now time.Time, attachmentRoots ...string) (Manifest, error) {
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
		FormatVersion: legacyFormatVersion, CreatedAt: now.UTC(), AppRevision: appRevision,
		SchemaVersion: schemaVersion, DatabaseSize: databaseSize, DatabaseSHA256: databaseDigest,
	}
	var attachmentRoot string
	var attachmentEntries []AttachmentManifest
	if len(attachmentRoots) > 1 {
		return Manifest{}, errors.New("at most one attachment root may be provided")
	}
	if len(attachmentRoots) == 1 && strings.TrimSpace(attachmentRoots[0]) != "" {
		attachmentRoot, err = validateAttachmentRoot(attachmentRoots[0])
		if err != nil {
			return Manifest{}, err
		}
		manifest.FormatVersion = bundleFormatVersion
		attachmentEntries, manifest.AttachmentSize, err = readAttachmentManifest(ctx, snapshotPath, attachmentRoot)
		if err != nil {
			return Manifest{}, err
		}
		manifest.AttachmentCount = len(attachmentEntries)
		encoded, err := json.Marshal(attachmentEntries)
		if err != nil || len(encoded) > maxAttachmentIndex {
			return Manifest{}, errors.New("attachment backup index exceeds the supported limit")
		}
		digest := sha256.Sum256(encoded)
		manifest.AttachmentSHA256 = hex.EncodeToString(digest[:])
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.FormatVersion == bundleFormatVersion {
		if err := validateAttachmentEntries(manifest, attachmentEntries); err != nil {
			return Manifest{}, err
		}
	}

	archivePath, err := writeArchive(ctx, filepath.Dir(outputPath), manifest, snapshotPath, attachmentRoot, attachmentEntries)
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
	attachmentPath := filepath.Join(directory, "attachments")
	manifest, err := extractAndVerify(ctx, inputPath, databasePath, attachmentPath)
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Restore(ctx context.Context, inputPath, outputPath string, attachmentOutputs ...string) (Manifest, error) {
	if len(attachmentOutputs) > 1 {
		return Manifest{}, errors.New("at most one attachment output may be provided")
	}
	outputPath, err := prepareNewOutputPath(outputPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("prepare restore output: %w", err)
	}
	temporaryPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-restore-*.sqlite")
	if err != nil {
		return Manifest{}, fmt.Errorf("reserve restore path: %w", err)
	}
	defer os.Remove(temporaryPath)
	var attachmentOutput string
	var temporaryAttachmentPath string
	if len(attachmentOutputs) == 1 && strings.TrimSpace(attachmentOutputs[0]) != "" {
		attachmentOutput, err = prepareNewDirectoryPath(attachmentOutputs[0])
		if err != nil {
			return Manifest{}, fmt.Errorf("prepare attachment restore output: %w", err)
		}
		temporaryAttachmentPath, err = unusedTemporaryDirectory(filepath.Dir(attachmentOutput), ".xboard-restore-attachments-*")
		if err != nil {
			return Manifest{}, fmt.Errorf("reserve attachment restore path: %w", err)
		}
		defer os.RemoveAll(temporaryAttachmentPath)
	}

	manifest, err := extractAndVerify(ctx, inputPath, temporaryPath, temporaryAttachmentPath)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.FormatVersion == bundleFormatVersion {
		if attachmentOutput == "" {
			return Manifest{}, errors.New("attachment bundle restore requires an attachment output path")
		}
		if err := os.Rename(temporaryAttachmentPath, attachmentOutput); err != nil {
			return Manifest{}, fmt.Errorf("publish restored attachments: %w", err)
		}
		temporaryAttachmentPath = ""
	} else if attachmentOutput != "" {
		return Manifest{}, errors.New("legacy database-only backup does not contain an attachment bundle")
	}
	if err := publishNoReplace(temporaryPath, outputPath); err != nil {
		if attachmentOutput != "" {
			_ = os.RemoveAll(attachmentOutput)
		}
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

func writeArchive(ctx context.Context, directory string, manifest Manifest, databasePath, attachmentRoot string, attachments []AttachmentManifest) (string, error) {
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
	if manifest.FormatVersion == bundleFormatVersion {
		encoded, err := json.Marshal(attachments)
		if err != nil {
			return "", fmt.Errorf("encode backup attachment index: %w", err)
		}
		if err := writeEntry(attachmentIndexName, int64(len(encoded)), strings.NewReader(string(encoded))); err != nil {
			return "", fmt.Errorf("write backup attachment index: %w", err)
		}
	}
	for _, attachment := range attachments {
		path, err := safeAttachmentPath(attachmentRoot, attachment.Path)
		if err != nil {
			return "", err
		}
		fileInfo, err := os.Lstat(path)
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != attachment.Size {
			return "", fmt.Errorf("attachment %q changed while creating the backup", attachment.Path)
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open backup attachment %q: %w", attachment.Path, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !os.SameFile(fileInfo, openedInfo) {
			_ = file.Close()
			return "", fmt.Errorf("attachment %q changed before it was archived", attachment.Path)
		}
		hash := sha256.New()
		if err := writeEntry(attachmentsPrefix+attachment.Path, attachment.Size, io.TeeReader(file, hash)); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("write backup attachment %q: %w", attachment.Path, err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("close backup attachment %q: %w", attachment.Path, err)
		}
		if hex.EncodeToString(hash.Sum(nil)) != attachment.SHA256 {
			return "", fmt.Errorf("attachment %q changed while it was archived", attachment.Path)
		}
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

func extractAndVerify(ctx context.Context, inputPath, databasePath, attachmentPath string) (Manifest, error) {
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
	if manifest.FormatVersion == bundleFormatVersion {
		if strings.TrimSpace(attachmentPath) == "" {
			return Manifest{}, errors.New("attachment bundle extraction requires a destination")
		}
		if err := os.Mkdir(attachmentPath, backupDirectoryMode); err != nil {
			return Manifest{}, fmt.Errorf("create attachment extraction directory: %w", err)
		}
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
	attachments := []AttachmentManifest{}
	if manifest.FormatVersion == bundleFormatVersion {
		header, err := tarReader.Next()
		if err != nil {
			return Manifest{}, fmt.Errorf("read backup attachment index header: %w", err)
		}
		if header.Name != attachmentIndexName || header.Typeflag != tar.TypeReg || header.Size < 2 || header.Size > maxAttachmentIndex {
			return Manifest{}, errors.New("backup attachment index is invalid")
		}
		encoded := make([]byte, header.Size)
		if _, err := io.ReadFull(tarReader, encoded); err != nil {
			return Manifest{}, fmt.Errorf("read backup attachment index: %w", err)
		}
		digest := sha256.Sum256(encoded)
		if hex.EncodeToString(digest[:]) != manifest.AttachmentSHA256 {
			return Manifest{}, errors.New("backup attachment index SHA-256 does not match the manifest")
		}
		decoder := json.NewDecoder(strings.NewReader(string(encoded)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&attachments); err != nil {
			return Manifest{}, fmt.Errorf("decode backup attachment index: %w", err)
		}
		var trailing json.RawMessage
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return Manifest{}, errors.New("backup attachment index contains trailing JSON values")
		}
		if err := validateAttachmentEntries(manifest, attachments); err != nil {
			return Manifest{}, err
		}
	}
	for _, attachment := range attachments {
		header, err := tarReader.Next()
		if err != nil {
			return Manifest{}, fmt.Errorf("read backup attachment %q header: %w", attachment.Path, err)
		}
		if header.Name != attachmentsPrefix+attachment.Path || header.Typeflag != tar.TypeReg || header.Size != attachment.Size {
			return Manifest{}, fmt.Errorf("backup attachment %q does not match the manifest", attachment.Path)
		}
		outputPath, err := safeAttachmentPath(attachmentPath, attachment.Path)
		if err != nil {
			return Manifest{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), backupDirectoryMode); err != nil {
			return Manifest{}, fmt.Errorf("create restored attachment directory: %w", err)
		}
		output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
		if err != nil {
			return Manifest{}, fmt.Errorf("create restored attachment %q: %w", attachment.Path, err)
		}
		hash := sha256.New()
		copied, copyErr := io.CopyN(io.MultiWriter(output, hash), &contextReader{ctx: ctx, reader: tarReader}, attachment.Size)
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil {
			return Manifest{}, fmt.Errorf("extract backup attachment %q: %w", attachment.Path, errors.Join(copyErr, closeErr))
		}
		if copied != attachment.Size || hex.EncodeToString(hash.Sum(nil)) != attachment.SHA256 {
			return Manifest{}, fmt.Errorf("backup attachment %q size or SHA-256 does not match the manifest", attachment.Path)
		}
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
	if manifest.FormatVersion != legacyFormatVersion && manifest.FormatVersion != bundleFormatVersion {
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
	if manifest.FormatVersion == legacyFormatVersion {
		if manifest.AttachmentCount != 0 || manifest.AttachmentSize != 0 || manifest.AttachmentSHA256 != "" {
			return errors.New("legacy backup manifest must not contain attachments")
		}
		return nil
	}
	if manifest.AttachmentCount < 0 || manifest.AttachmentCount > maxAttachmentFiles || manifest.AttachmentSize < 0 || manifest.AttachmentSize > maxAttachmentBytes ||
		len(manifest.AttachmentSHA256) != sha256.Size*2 || manifest.AttachmentSHA256 != strings.ToLower(manifest.AttachmentSHA256) {
		return errors.New("backup manifest attachment totals are invalid")
	}
	if _, err := hex.DecodeString(manifest.AttachmentSHA256); err != nil {
		return errors.New("backup manifest attachment index SHA-256 is invalid")
	}
	return nil
}

func validateAttachmentEntries(manifest Manifest, attachments []AttachmentManifest) error {
	if manifest.AttachmentCount != len(attachments) {
		return errors.New("backup attachment index count does not match the manifest")
	}
	var total int64
	previous := ""
	for _, attachment := range attachments {
		if !validAttachmentRelativePath(attachment.Path) || attachment.Path <= previous || attachment.Size < 1 || attachment.Size > maxAttachmentBytes ||
			len(attachment.SHA256) != sha256.Size*2 || attachment.SHA256 != strings.ToLower(attachment.SHA256) {
			return errors.New("backup manifest contains an invalid attachment entry")
		}
		if _, err := hex.DecodeString(attachment.SHA256); err != nil || total > maxAttachmentBytes-attachment.Size {
			return errors.New("backup manifest contains an invalid attachment entry")
		}
		total += attachment.Size
		previous = attachment.Path
	}
	if total != manifest.AttachmentSize {
		return errors.New("backup attachment index size does not match the manifest")
	}
	return nil
}

func readAttachmentManifest(ctx context.Context, databasePath, root string) ([]AttachmentManifest, int64, error) {
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, 0, fmt.Errorf("open attachment backup snapshot: %w", err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return nil, 0, fmt.Errorf("read attachment backup schema: %w", err)
	}
	if schemaVersion < 35 {
		return []AttachmentManifest{}, 0, nil
	}
	rows, err := database.QueryContext(ctx, `
		SELECT path, size, sha256 FROM (
			SELECT storage_path AS path, size, sha256 FROM knowledge_attachments
			UNION ALL
			SELECT u.temporary_path || '/chunks/' || c.chunk_index || '.part' AS path, c.size, c.sha256
			FROM knowledge_attachment_uploads u
			JOIN knowledge_attachment_chunks c ON c.upload_id = u.id
			WHERE u.status <> 'completed'
		) ORDER BY path
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read attachment backup metadata: %w", err)
	}
	defer rows.Close()
	attachments := make([]AttachmentManifest, 0)
	var total int64
	for rows.Next() {
		if len(attachments) >= maxAttachmentFiles {
			return nil, 0, errors.New("attachment backup exceeds the file-count limit")
		}
		var attachment AttachmentManifest
		if err := rows.Scan(&attachment.Path, &attachment.Size, &attachment.SHA256); err != nil {
			return nil, 0, fmt.Errorf("scan attachment backup metadata: %w", err)
		}
		if !validAttachmentRelativePath(attachment.Path) || attachment.Size < 1 || attachment.Size > maxAttachmentBytes ||
			len(attachment.SHA256) != sha256.Size*2 || attachment.SHA256 != strings.ToLower(attachment.SHA256) {
			return nil, 0, fmt.Errorf("attachment %q has invalid backup metadata", attachment.Path)
		}
		path, err := safeAttachmentPath(root, attachment.Path)
		if err != nil {
			return nil, 0, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != attachment.Size {
			return nil, 0, fmt.Errorf("attachment %q is missing or does not match its recorded size", attachment.Path)
		}
		digest, err := fileSHA256(ctx, path)
		if err != nil || digest != attachment.SHA256 {
			return nil, 0, fmt.Errorf("attachment %q does not match its recorded SHA-256", attachment.Path)
		}
		if total > maxAttachmentBytes-attachment.Size {
			return nil, 0, errors.New("attachment backup exceeds the size limit")
		}
		total += attachment.Size
		attachments = append(attachments, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate attachment backup metadata: %w", err)
	}
	return attachments, total, nil
}

func validateAttachmentRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("attachment root must be an absolute directory")
	}
	absolute := filepath.Clean(path)
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("attachment root must be an existing directory without symbolic links")
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Clean(real) != absolute {
		return "", errors.New("attachment root must not resolve through symbolic links")
	}
	return absolute, nil
}

func validAttachmentRelativePath(path string) bool {
	if path == "" || len(path) > 512 || strings.Contains(path, `\`) || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func safeAttachmentPath(root, relative string) (string, error) {
	if strings.TrimSpace(root) == "" || !validAttachmentRelativePath(relative) {
		return "", errors.New("backup attachment path is unsafe")
	}
	root = filepath.Clean(root)
	path := filepath.Clean(filepath.Join(root, filepath.FromSlash(relative)))
	prefix := root + string(filepath.Separator)
	if path == root || !strings.HasPrefix(path, prefix) {
		return "", errors.New("backup attachment path escapes its root")
	}
	for current := filepath.Dir(path); current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("backup attachment path contains an unsafe directory")
		}
	}
	return path, nil
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

func prepareNewDirectoryPath(path string) (string, error) {
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
	return absolute, os.Chmod(filepath.Dir(absolute), backupDirectoryMode)
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

func unusedTemporaryDirectory(directory, pattern string) (string, error) {
	path, err := os.MkdirTemp(directory, pattern)
	if err != nil {
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
