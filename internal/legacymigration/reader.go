package legacymigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

const (
	maxLegacySnapshotSize      = int64(16 << 30)
	maxLegacyRelevantDataBytes = int64(64 << 20)
	maxLegacyNotices           = 100_000
	maxLegacyClientJSONBytes   = 1 << 20
)

type ContentSnapshot struct {
	Path                 string
	Size                 int64
	SHA256               string
	SiteSettings         store.LegacySiteSettings
	Notices              []store.LegacyNotice
	ClientCatalogPresent bool
	ClientCatalogLinks   []store.ClientCatalogOverride
	Checksums            store.LegacyContentChecksums
}

type snapshotIdentity struct {
	Path   string
	Size   int64
	SHA256 string
}

func ReadContentSnapshot(ctx context.Context, sourcePath string) (ContentSnapshot, error) {
	var settings store.LegacySiteSettings
	var clientPresent bool
	var clientLinks []store.ClientCatalogOverride
	var notices []store.LegacyNotice
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := requireRealTable(ctx, database, "v2_notice", []string{"id", "sort", "title", "content", "img_url", "tags", "show", "created_at", "updated_at"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)
			), 0)
			FROM v2_settings
			WHERE name IN ('app_name', 'app_description', 'app_url', 'tos_url', 'logo', 'client_catalog_links')
		`, 6, maxLegacyRelevantDataBytes, "legacy public settings"); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(
				length(CAST(title AS BLOB)) + length(CAST(content AS BLOB)) +
				COALESCE(length(CAST(img_url AS BLOB)), 0) + COALESCE(length(CAST(tags AS BLOB)), 0)
			), 0)
			FROM v2_notice
		`, maxLegacyNotices, maxLegacyRelevantDataBytes, "legacy notices"); err != nil {
			return err
		}
		var relevantBytes int64
		var readErr error
		settings, clientPresent, clientLinks, relevantBytes, readErr = readLegacySettings(ctx, database)
		if readErr != nil {
			return readErr
		}
		var noticeBytes int64
		notices, noticeBytes, readErr = readLegacyNotices(ctx, database, relevantBytes)
		if readErr != nil {
			return readErr
		}
		if relevantBytes+noticeBytes > maxLegacyRelevantDataBytes {
			return errors.New("legacy content exceeds the migration data limit")
		}
		return nil
	})
	if err != nil {
		return ContentSnapshot{}, err
	}
	checksums := store.LegacyContentChecksums{
		SiteSettings:  store.LegacySiteSettingsChecksum(settings),
		Notices:       store.LegacyNoticesChecksum(notices),
		ClientCatalog: store.LegacyClientCatalogChecksum(clientLinks),
	}
	return ContentSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		SiteSettings: settings, Notices: notices, ClientCatalogPresent: clientPresent,
		ClientCatalogLinks: clientLinks, Checksums: checksums,
	}, nil
}

func readLegacySnapshot(ctx context.Context, sourcePath string, read func(*sql.DB) error) (snapshotIdentity, error) {
	if err := ctx.Err(); err != nil {
		return snapshotIdentity{}, err
	}
	absolute, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return snapshotIdentity{}, fmt.Errorf("resolve legacy snapshot: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return snapshotIdentity{}, fmt.Errorf("inspect legacy snapshot: %w", err)
	}
	if !info.Mode().IsRegular() {
		return snapshotIdentity{}, errors.New("legacy snapshot must be a regular file, not a link or device")
	}
	if info.Size() < 512 || info.Size() > maxLegacySnapshotSize {
		return snapshotIdentity{}, fmt.Errorf("legacy snapshot size must be between 512 bytes and %d bytes", maxLegacySnapshotSize)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(absolute + suffix); err == nil {
			return snapshotIdentity{}, fmt.Errorf("legacy snapshot has an adjacent SQLite %s file; create a standalone online backup first", strings.ToUpper(strings.TrimPrefix(suffix, "-")))
		} else if !errors.Is(err, os.ErrNotExist) {
			return snapshotIdentity{}, fmt.Errorf("inspect legacy snapshot sidecar: %w", err)
		}
	}
	firstDigest, firstInfo, err := hashRegularFile(ctx, absolute, info)
	if err != nil {
		return snapshotIdentity{}, err
	}
	database, err := sql.Open("sqlite", readOnlyImmutableDSN(absolute))
	if err != nil {
		return snapshotIdentity{}, fmt.Errorf("open legacy snapshot: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	closeWithError := func(current error) error {
		if closeErr := database.Close(); current == nil && closeErr != nil {
			return fmt.Errorf("close legacy snapshot: %w", closeErr)
		}
		return current
	}
	if err := database.PingContext(ctx); err != nil {
		return snapshotIdentity{}, closeWithError(fmt.Errorf("ping legacy snapshot: %w", err))
	}
	if err := validateLegacySnapshot(ctx, database); err != nil {
		return snapshotIdentity{}, closeWithError(err)
	}
	if err := read(database); err != nil {
		return snapshotIdentity{}, closeWithError(err)
	}
	if err := closeWithError(nil); err != nil {
		return snapshotIdentity{}, err
	}
	secondDigest, secondInfo, err := hashRegularFile(ctx, absolute, firstInfo)
	if err != nil {
		return snapshotIdentity{}, err
	}
	if firstDigest != secondDigest || firstInfo.Size() != secondInfo.Size() || !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		return snapshotIdentity{}, errors.New("legacy snapshot changed while it was being read")
	}
	return snapshotIdentity{Path: absolute, Size: secondInfo.Size(), SHA256: secondDigest}, nil
}

func validateLegacySnapshot(ctx context.Context, database *sql.DB) error {
	var queryOnly, trustedSchema int
	if err := database.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return fmt.Errorf("verify legacy query-only mode: %w", err)
	}
	if err := database.QueryRowContext(ctx, `PRAGMA trusted_schema`).Scan(&trustedSchema); err != nil {
		return fmt.Errorf("verify legacy trusted schema mode: %w", err)
	}
	if queryOnly != 1 || trustedSchema != 0 {
		return errors.New("legacy snapshot did not open with query_only=1 and trusted_schema=0")
	}
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil {
		return fmt.Errorf("check legacy snapshot integrity: %w", err)
	}
	if integrity != "ok" {
		return errors.New("legacy snapshot failed SQLite integrity check")
	}
	return nil
}

func requireRealTable(ctx context.Context, database *sql.DB, table string, requiredColumns []string) error {
	var objectType string
	if err := database.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = ?`, table).Scan(&objectType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("legacy snapshot is missing required real table %q", table)
		}
		return fmt.Errorf("inspect legacy table %q: %w", table, err)
	}
	if objectType != "table" {
		return fmt.Errorf("legacy snapshot object %q must be a real table", table)
	}
	query := `PRAGMA table_info("` + table + `")`
	rows, err := database.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("inspect legacy table %q columns: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("inspect legacy table %q columns: %w", table, err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect legacy table %q columns: %w", table, err)
	}
	for _, required := range requiredColumns {
		if _, exists := columns[required]; !exists {
			return fmt.Errorf("legacy table %q is missing required column %q", table, required)
		}
	}
	return nil
}

func validateLegacyQueryBudget(ctx context.Context, database *sql.DB, query string, maxRows int, maxBytes int64, label string) error {
	var rows, bytes int64
	if err := database.QueryRowContext(ctx, query).Scan(&rows, &bytes); err != nil {
		return fmt.Errorf("measure %s: %w", label, err)
	}
	if rows < 0 || rows > int64(maxRows) {
		return fmt.Errorf("%s exceed the %d-row migration limit", label, maxRows)
	}
	if bytes < 0 || bytes > maxBytes {
		return fmt.Errorf("%s exceed the %d-byte migration limit", label, maxBytes)
	}
	return nil
}

func readLegacySettings(ctx context.Context, database *sql.DB) (store.LegacySiteSettings, bool, []store.ClientCatalogOverride, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT name, value FROM v2_settings
		WHERE name IN ('app_name', 'app_description', 'app_url', 'tos_url', 'logo', 'client_catalog_links')
		ORDER BY name
	`)
	if err != nil {
		return store.LegacySiteSettings{}, false, nil, 0, fmt.Errorf("read legacy public settings: %w", err)
	}
	defer rows.Close()
	settings := store.LegacySiteSettings{}
	clientPresent := false
	clientRaw := ""
	var bytesRead int64
	seen := make(map[string]struct{}, 6)
	for rows.Next() {
		var name string
		var value sql.NullString
		if err := rows.Scan(&name, &value); err != nil {
			return store.LegacySiteSettings{}, false, nil, 0, fmt.Errorf("scan legacy public setting: %w", err)
		}
		text := ""
		if value.Valid {
			text = value.String
		}
		bytesRead += int64(len(name) + len(text))
		if bytesRead > maxLegacyRelevantDataBytes {
			return store.LegacySiteSettings{}, false, nil, 0, errors.New("legacy public settings exceed the migration data limit")
		}
		if _, exists := seen[name]; exists {
			return store.LegacySiteSettings{}, false, nil, 0, fmt.Errorf("legacy public settings contain duplicate %q rows", name)
		}
		seen[name] = struct{}{}
		switch name {
		case "app_name":
			settings.AppName = stringPointer(text)
		case "app_description":
			settings.AppDescription = stringPointer(text)
		case "app_url":
			settings.AppURL = stringPointer(text)
		case "tos_url":
			settings.TOSURL = stringPointer(text)
		case "logo":
			settings.Logo = stringPointer(text)
		case "client_catalog_links":
			clientPresent = true
			clientRaw = text
		}
	}
	if err := rows.Err(); err != nil {
		return store.LegacySiteSettings{}, false, nil, 0, fmt.Errorf("iterate legacy public settings: %w", err)
	}
	links, err := decodeLegacyClientCatalog(clientRaw, clientPresent)
	if err != nil {
		return store.LegacySiteSettings{}, false, nil, 0, err
	}
	return settings, clientPresent, links, bytesRead, nil
}

func decodeLegacyClientCatalog(encoded string, present bool) ([]store.ClientCatalogOverride, error) {
	if !present {
		return []store.ClientCatalogOverride{}, nil
	}
	if len(encoded) > maxLegacyClientJSONBytes {
		return nil, errors.New("legacy client_catalog_links exceeds the migration limit")
	}
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" || trimmed == "null" || trimmed == "[]" || trimmed == "{}" {
		return []store.ClientCatalogOverride{}, nil
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, errors.New("legacy client_catalog_links must be a JSON object")
	}
	var input clientcatalog.OverrideInput
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("decode legacy client_catalog_links: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("decode legacy client_catalog_links: trailing JSON data")
	}
	links, err := clientcatalog.NormalizeOverrides(input)
	if err != nil {
		return nil, fmt.Errorf("validate legacy client_catalog_links: %w", err)
	}
	return links, nil
}

func readLegacyNotices(ctx context.Context, database *sql.DB, bytesAlreadyRead int64) ([]store.LegacyNotice, int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, sort, title, content, img_url, tags, show, created_at, updated_at
		FROM v2_notice ORDER BY id
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("read legacy notices: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyNotice, 0)
	var bytesRead int64
	for rows.Next() {
		if len(result) >= maxLegacyNotices {
			return nil, 0, fmt.Errorf("legacy notices exceed the %d-row migration limit", maxLegacyNotices)
		}
		var notice store.LegacyNotice
		var sortPosition sql.NullInt64
		var imageURL, tagsJSON sql.NullString
		var visible int
		if err := rows.Scan(&notice.ID, &sortPosition, &notice.Title, &notice.Content, &imageURL, &tagsJSON, &visible, &notice.CreatedAt, &notice.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan legacy notice: %w", err)
		}
		if sortPosition.Valid {
			if sortPosition.Int64 < 0 || sortPosition.Int64 > int64(^uint(0)>>1) {
				return nil, 0, fmt.Errorf("legacy notice id %d has an invalid sort value", notice.ID)
			}
			notice.SortPosition = int(sortPosition.Int64)
		}
		if imageURL.Valid {
			notice.ImageURL = imageURL.String
		}
		if visible != 0 && visible != 1 {
			return nil, 0, fmt.Errorf("legacy notice id %d has an invalid show value", notice.ID)
		}
		notice.Visible = visible == 1
		notice.Tags = []string{}
		if tagsJSON.Valid && strings.TrimSpace(tagsJSON.String) != "" && strings.TrimSpace(tagsJSON.String) != "null" {
			if err := json.Unmarshal([]byte(tagsJSON.String), &notice.Tags); err != nil {
				return nil, 0, fmt.Errorf("decode legacy notice id %d tags: %w", notice.ID, err)
			}
			if notice.Tags == nil {
				notice.Tags = []string{}
			}
		}
		bytesRead += int64(len(notice.Title) + len(notice.Content) + len(notice.ImageURL))
		for _, tag := range notice.Tags {
			bytesRead += int64(len(tag))
		}
		if bytesAlreadyRead+bytesRead > maxLegacyRelevantDataBytes {
			return nil, 0, errors.New("legacy notices exceed the migration data limit")
		}
		result = append(result, notice)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate legacy notices: %w", err)
	}
	return result, bytesRead, nil
}

func hashRegularFile(ctx context.Context, path string, expected os.FileInfo) (string, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("open legacy snapshot for hashing: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", nil, fmt.Errorf("inspect opened legacy snapshot: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(expected, openedInfo) {
		return "", nil, errors.New("legacy snapshot changed before it could be hashed")
	}
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", nil, fmt.Errorf("hash legacy snapshot: %w", readErr)
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), openedInfo, nil
}

func readOnlyImmutableDSN(path string) string {
	uriPath := filepath.ToSlash(path)
	if volume := filepath.VolumeName(path); volume != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	query := uri.Query()
	query.Set("mode", "ro")
	query.Set("immutable", "1")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "trusted_schema(0)")
	query.Add("_pragma", "busy_timeout(5000)")
	uri.RawQuery = query.Encode()
	return uri.String()
}

func stringPointer(value string) *string { return &value }
