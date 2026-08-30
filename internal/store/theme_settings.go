package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

var themeDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Theme struct {
	Name          string                   `json:"name"`
	Description   string                   `json:"description"`
	Version       string                   `json:"version"`
	Images        []string                 `json:"images"`
	Backgrounds   []string                 `json:"backgrounds"`
	Palettes      map[string]theme.Palette `json:"palettes"`
	Config        theme.Config             `json:"config"`
	PackageSHA256 string                   `json:"package_sha256"`
	Revision      int64                    `json:"revision"`
	IsSystem      bool                     `json:"is_system"`
	IsActive      bool                     `json:"is_active"`
	CanDelete     bool                     `json:"can_delete"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

type ThemeCatalog struct {
	ActiveTheme string  `json:"active_theme"`
	Revision    int64   `json:"revision"`
	Themes      []Theme `json:"themes"`
}

type ThemeAppearance struct {
	Name          string        `json:"name"`
	Revision      int64         `json:"revision"`
	PackageSHA256 string        `json:"package_sha256"`
	Palette       theme.Palette `json:"palette"`
	Config        theme.Config  `json:"config"`
}

type ThemeAsset struct {
	MIME   string
	SHA256 string
	Width  int
	Height int
	Data   []byte
}

type themeAppearanceCacheEntry struct {
	activeRevision int64
	themeRevision  int64
	name           string
	packageSHA256  string
	appearance     ThemeAppearance
	valid          bool
}

func (s *Store) ListThemes(ctx context.Context) (ThemeCatalog, error) {
	return readThemeCatalog(ctx, s.db)
}

func (s *Store) GetTheme(ctx context.Context, name string) (Theme, error) {
	if !validThemeLookupName(name) {
		return Theme{}, ErrInvalidInput
	}
	item, err := readTheme(ctx, s.db, name)
	if errors.Is(err, sql.ErrNoRows) {
		return Theme{}, ErrNotFound
	}
	if err != nil {
		return Theme{}, fmt.Errorf("get theme: %w", err)
	}
	return item, nil
}

func (s *Store) InstallTheme(ctx context.Context, administratorID int64, packaged theme.Package, now time.Time) (Theme, error) {
	manifest := packaged.Manifest
	if administratorID < 1 || now.Unix() < 0 || strings.EqualFold(manifest.Name, "Xboard") || !themeDigestPattern.MatchString(packaged.SHA256) {
		return Theme{}, ErrInvalidInput
	}
	if err := theme.ValidateManifest(manifest); err != nil {
		return Theme{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return Theme{}, fmt.Errorf("encode theme manifest: %w", err)
	}
	if err := validateThemePackageAssets(packaged); err != nil {
		return Theme{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Theme{}, fmt.Errorf("begin theme installation: %w", err)
	}
	defer tx.Rollback()

	config := manifest.DefaultConfig
	revision := int64(1)
	var existingVersion, existingConfigJSON string
	var existingRevision int64
	var isSystem bool
	err = tx.QueryRowContext(ctx, `SELECT version, config_json, revision, is_system FROM themes WHERE name = ? COLLATE NOCASE`, manifest.Name).
		Scan(&existingVersion, &existingConfigJSON, &existingRevision, &isSystem)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		configJSON, marshalErr := json.Marshal(config)
		if marshalErr != nil {
			return Theme{}, fmt.Errorf("encode theme config: %w", marshalErr)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO themes (
				name, description, version, manifest_json, config_json, package_sha256,
				revision, is_system, created_by, updated_by, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 1, 0, ?, ?, ?, ?)
		`, manifest.Name, manifest.Description, manifest.Version, string(manifestJSON), string(configJSON), packaged.SHA256,
			administratorID, administratorID, now.Unix(), now.Unix())
		if err != nil {
			return Theme{}, fmt.Errorf("create theme: %w", err)
		}
	case err != nil:
		return Theme{}, fmt.Errorf("find existing theme: %w", err)
	case isSystem:
		return Theme{}, ErrConflict
	default:
		comparison, compareErr := theme.CompareVersions(manifest.Version, existingVersion)
		if compareErr != nil || comparison <= 0 {
			return Theme{}, ErrConflict
		}
		var previous theme.Config
		if json.Unmarshal([]byte(existingConfigJSON), &previous) == nil && theme.ValidateConfig(manifest, previous) == nil {
			config = previous
		}
		configJSON, marshalErr := json.Marshal(config)
		if marshalErr != nil {
			return Theme{}, fmt.Errorf("encode upgraded theme config: %w", marshalErr)
		}
		revision = existingRevision + 1
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE themes SET description = ?, version = ?, manifest_json = ?, config_json = ?, package_sha256 = ?,
				revision = revision + 1, updated_by = ?, updated_at = ?
			WHERE name = ? COLLATE NOCASE AND revision = ? AND is_system = 0
		`, manifest.Description, manifest.Version, string(manifestJSON), string(configJSON), packaged.SHA256,
			administratorID, now.Unix(), manifest.Name, existingRevision)
		if updateErr != nil {
			return Theme{}, fmt.Errorf("upgrade theme: %w", updateErr)
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return Theme{}, fmt.Errorf("count upgraded theme rows: %w", rowsErr)
		}
		if changed != 1 {
			return Theme{}, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM theme_assets WHERE theme_name = ? COLLATE NOCASE`, manifest.Name); err != nil {
			return Theme{}, fmt.Errorf("replace theme assets: %w", err)
		}
	}
	for _, asset := range packaged.Assets {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO theme_assets(theme_name, path, mime_type, size, sha256, width, height, body)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, manifest.Name, asset.Path, asset.MIME, len(asset.Data), asset.SHA256, asset.Width, asset.Height, asset.Data); err != nil {
			return Theme{}, fmt.Errorf("store theme asset %q: %w", asset.Path, err)
		}
	}
	installed, err := readTheme(ctx, tx, manifest.Name)
	if err != nil {
		return Theme{}, fmt.Errorf("read installed theme: %w", err)
	}
	if installed.Revision != revision {
		return Theme{}, errors.New("installed theme revision mismatch")
	}
	if err := tx.Commit(); err != nil {
		return Theme{}, fmt.Errorf("commit theme installation: %w", err)
	}
	return installed, nil
}

func (s *Store) UpdateThemeConfig(ctx context.Context, administratorID int64, name string, revision int64, config theme.Config, now time.Time) (Theme, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 || !validThemeLookupName(name) {
		return Theme{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Theme{}, fmt.Errorf("begin theme config update: %w", err)
	}
	defer tx.Rollback()
	current, err := readTheme(ctx, tx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return Theme{}, ErrNotFound
	}
	if err != nil {
		return Theme{}, fmt.Errorf("read theme config: %w", err)
	}
	manifest := theme.Manifest{Name: current.Name, Palettes: current.Palettes, Backgrounds: current.Backgrounds}
	if err := theme.ValidateConfig(manifest, config); err != nil {
		return Theme{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Theme{}, fmt.Errorf("encode theme config: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE themes SET config_json = ?, revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE name = ? COLLATE NOCASE AND revision = ?
	`, string(configJSON), administratorID, now.Unix(), name, revision)
	if err != nil {
		return Theme{}, fmt.Errorf("update theme config: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Theme{}, fmt.Errorf("count theme config update: %w", err)
	}
	if changed != 1 {
		return Theme{}, ErrConflict
	}
	updated, err := readTheme(ctx, tx, name)
	if err != nil {
		return Theme{}, fmt.Errorf("read updated theme config: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Theme{}, fmt.Errorf("commit theme config update: %w", err)
	}
	return updated, nil
}

func (s *Store) ActivateTheme(ctx context.Context, administratorID int64, name string, revision int64, now time.Time) (ThemeCatalog, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 || !validThemeLookupName(name) {
		return ThemeCatalog{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ThemeCatalog{}, fmt.Errorf("begin theme activation: %w", err)
	}
	defer tx.Rollback()
	var canonical string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM themes WHERE name = ? COLLATE NOCASE`, name).Scan(&canonical); errors.Is(err, sql.ErrNoRows) {
		return ThemeCatalog{}, ErrNotFound
	} else if err != nil {
		return ThemeCatalog{}, fmt.Errorf("find activation theme: %w", err)
	}
	var active string
	var currentRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT active_theme, revision FROM theme_settings WHERE id = 1`).Scan(&active, &currentRevision); err != nil {
		return ThemeCatalog{}, fmt.Errorf("read active theme: %w", err)
	}
	if currentRevision != revision {
		return ThemeCatalog{}, ErrConflict
	}
	if !strings.EqualFold(active, canonical) {
		result, err := tx.ExecContext(ctx, `
			UPDATE theme_settings SET active_theme = ?, revision = revision + 1, updated_by = ?, updated_at = ?
			WHERE id = 1 AND revision = ?
		`, canonical, administratorID, now.Unix(), revision)
		if err != nil {
			return ThemeCatalog{}, fmt.Errorf("activate theme: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return ThemeCatalog{}, fmt.Errorf("count theme activation rows: %w", err)
		}
		if changed != 1 {
			return ThemeCatalog{}, ErrConflict
		}
	}
	catalog, err := readThemeCatalog(ctx, tx)
	if err != nil {
		return ThemeCatalog{}, fmt.Errorf("read activated theme catalog: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ThemeCatalog{}, fmt.Errorf("commit theme activation: %w", err)
	}
	return catalog, nil
}

func (s *Store) DeleteTheme(ctx context.Context, administratorID int64, name string, now time.Time) error {
	if administratorID < 1 || now.Unix() < 0 || !validThemeLookupName(name) {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	var system, active bool
	err := s.db.QueryRowContext(ctx, `
		SELECT t.is_system, t.name = settings.active_theme COLLATE NOCASE
		FROM themes t CROSS JOIN theme_settings settings
		WHERE t.name = ? COLLATE NOCASE AND settings.id = 1
	`, name).Scan(&system, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("inspect deletable theme: %w", err)
	}
	if system || active {
		return ErrConflict
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM themes WHERE name = ? COLLATE NOCASE AND is_system = 0`, name)
	if err != nil {
		return fmt.Errorf("delete theme: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted theme rows: %w", err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) GetActiveThemeAppearance(ctx context.Context) (ThemeAppearance, error) {
	var appearance ThemeAppearance
	var themeRevision int64
	var manifestJSON, configJSON string
	err := s.db.QueryRowContext(ctx, `
		SELECT t.name, settings.revision, t.revision, t.package_sha256, t.manifest_json, t.config_json
		FROM theme_settings settings JOIN themes t ON t.name = settings.active_theme COLLATE NOCASE
		WHERE settings.id = 1
	`).Scan(&appearance.Name, &appearance.Revision, &themeRevision, &appearance.PackageSHA256, &manifestJSON, &configJSON)
	if err != nil {
		return ThemeAppearance{}, fmt.Errorf("get active theme appearance: %w", err)
	}
	s.themeAppearanceMu.RLock()
	cached := s.themeAppearance
	s.themeAppearanceMu.RUnlock()
	if cached.valid && cached.activeRevision == appearance.Revision && cached.themeRevision == themeRevision &&
		cached.name == appearance.Name && cached.packageSHA256 == appearance.PackageSHA256 {
		return cached.appearance, nil
	}
	var manifest theme.Manifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return ThemeAppearance{}, fmt.Errorf("decode active theme manifest: %w", err)
	}
	if err := json.Unmarshal([]byte(configJSON), &appearance.Config); err != nil {
		return ThemeAppearance{}, fmt.Errorf("decode active theme config: %w", err)
	}
	palette, exists := manifest.Palettes[appearance.Config.ThemeColor]
	if !exists {
		return ThemeAppearance{}, errors.New("active theme references an unknown palette")
	}
	appearance.Palette = palette
	s.themeAppearanceMu.Lock()
	s.themeAppearance = themeAppearanceCacheEntry{
		activeRevision: appearance.Revision, themeRevision: themeRevision, name: appearance.Name,
		packageSHA256: appearance.PackageSHA256, appearance: appearance, valid: true,
	}
	s.themeAppearanceMu.Unlock()
	return appearance, nil
}

func (s *Store) GetThemeAsset(ctx context.Context, name, packageSHA256, assetPath string) (ThemeAsset, error) {
	if !validThemeLookupName(name) || !themeDigestPattern.MatchString(packageSHA256) || assetPath == "" || len([]byte(assetPath)) > 512 {
		return ThemeAsset{}, ErrInvalidInput
	}
	var asset ThemeAsset
	err := s.db.QueryRowContext(ctx, `
		SELECT a.mime_type, a.sha256, a.width, a.height, a.body
		FROM theme_assets a JOIN themes t ON t.name = a.theme_name COLLATE NOCASE
		WHERE t.name = ? COLLATE NOCASE AND t.package_sha256 = ? AND a.path = ?
	`, name, packageSHA256, assetPath).Scan(&asset.MIME, &asset.SHA256, &asset.Width, &asset.Height, &asset.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return ThemeAsset{}, ErrNotFound
	}
	if err != nil {
		return ThemeAsset{}, fmt.Errorf("get theme asset: %w", err)
	}
	return asset, nil
}

type themeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readThemeCatalog(ctx context.Context, query themeQuery) (ThemeCatalog, error) {
	var catalog ThemeCatalog
	if err := query.QueryRowContext(ctx, `SELECT active_theme, revision FROM theme_settings WHERE id = 1`).Scan(&catalog.ActiveTheme, &catalog.Revision); err != nil {
		return ThemeCatalog{}, fmt.Errorf("read theme catalog settings: %w", err)
	}
	rows, err := query.QueryContext(ctx, `
		SELECT t.name, t.description, t.version, t.manifest_json, t.config_json, t.package_sha256,
		       t.revision, t.is_system, t.name = settings.active_theme COLLATE NOCASE, t.updated_at
		FROM themes t CROSS JOIN theme_settings settings WHERE settings.id = 1
		ORDER BY t.is_system DESC, lower(t.name), t.name
	`)
	if err != nil {
		return ThemeCatalog{}, fmt.Errorf("list themes: %w", err)
	}
	defer rows.Close()
	catalog.Themes = make([]Theme, 0)
	for rows.Next() {
		item, err := scanTheme(rows)
		if err != nil {
			return ThemeCatalog{}, err
		}
		catalog.Themes = append(catalog.Themes, item)
	}
	if err := rows.Err(); err != nil {
		return ThemeCatalog{}, fmt.Errorf("iterate themes: %w", err)
	}
	return catalog, nil
}

func readTheme(ctx context.Context, query themeQuery, name string) (Theme, error) {
	return scanTheme(query.QueryRowContext(ctx, `
		SELECT t.name, t.description, t.version, t.manifest_json, t.config_json, t.package_sha256,
		       t.revision, t.is_system, t.name = settings.active_theme COLLATE NOCASE, t.updated_at
		FROM themes t CROSS JOIN theme_settings settings
		WHERE settings.id = 1 AND t.name = ? COLLATE NOCASE
	`, name))
}

type themeScanner interface {
	Scan(...any) error
}

func scanTheme(scanner themeScanner) (Theme, error) {
	var item Theme
	var manifestJSON, configJSON string
	var updatedAt int64
	if err := scanner.Scan(&item.Name, &item.Description, &item.Version, &manifestJSON, &configJSON,
		&item.PackageSHA256, &item.Revision, &item.IsSystem, &item.IsActive, &updatedAt); err != nil {
		return Theme{}, err
	}
	var manifest theme.Manifest
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return Theme{}, fmt.Errorf("decode theme %q manifest: %w", item.Name, err)
	}
	if err := json.Unmarshal([]byte(configJSON), &item.Config); err != nil {
		return Theme{}, fmt.Errorf("decode theme %q config: %w", item.Name, err)
	}
	// Keep collection fields as JSON arrays even when a theme has no assets.
	// The admin client treats these as arrays by contract; encoding nil here
	// would turn them into JSON null and crash the first theme-catalog render.
	item.Images = append([]string{}, manifest.Images...)
	item.Backgrounds = append([]string{}, manifest.Backgrounds...)
	item.Palettes = manifest.Palettes
	item.CanDelete = !item.IsSystem && !item.IsActive
	item.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return item, nil
}

func validateThemePackageAssets(packaged theme.Package) error {
	references := make(map[string]struct{}, len(packaged.Manifest.Images)+len(packaged.Manifest.Backgrounds))
	for _, value := range append(append([]string(nil), packaged.Manifest.Images...), packaged.Manifest.Backgrounds...) {
		references[value] = struct{}{}
	}
	if len(references) != len(packaged.Assets) {
		return fmt.Errorf("%w: theme asset catalog does not match the manifest", ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(packaged.Assets))
	for _, asset := range packaged.Assets {
		if _, exists := references[asset.Path]; !exists || asset.Path == "" || len(asset.Data) == 0 || len(asset.Data) > 8<<20 ||
			(asset.MIME != "image/png" && asset.MIME != "image/jpeg" && asset.MIME != "image/gif") || asset.Width < 1 || asset.Height < 1 ||
			asset.Width > 20_000_000/asset.Height || !themeDigestPattern.MatchString(asset.SHA256) {
			return fmt.Errorf("%w: invalid theme asset %q", ErrInvalidInput, asset.Path)
		}
		if _, duplicate := seen[asset.Path]; duplicate {
			return fmt.Errorf("%w: duplicate theme asset %q", ErrInvalidInput, asset.Path)
		}
		seen[asset.Path] = struct{}{}
		digest := sha256.Sum256(asset.Data)
		if hex.EncodeToString(digest[:]) != asset.SHA256 {
			return fmt.Errorf("%w: theme asset digest mismatch", ErrInvalidInput)
		}
	}
	return nil
}

func validThemeLookupName(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	for _, character := range name {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}
