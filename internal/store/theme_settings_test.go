package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

func TestThemeLifecycleUsesIndependentCASAndProtectsSystemAndActiveThemes(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "theme-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}

	initial, err := database.ListThemes(ctx)
	if err != nil || initial.ActiveTheme != "Xboard" || initial.Revision != 1 || len(initial.Themes) != 1 || !initial.Themes[0].IsSystem || !initial.Themes[0].IsActive {
		t.Fatalf("initial catalog=%#v err=%v", initial, err)
	}
	for _, palette := range []string{"default", "blue", "black", "darkblue"} {
		if _, exists := initial.Themes[0].Palettes[palette]; !exists {
			t.Fatalf("built-in Xboard theme is missing %q palette", palette)
		}
	}
	if err := database.DeleteTheme(ctx, administrator.ID, "Xboard", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteTheme(system) error=%v, want ErrConflict", err)
	}
	if _, err := database.GetActiveThemeAppearance(ctx); err != nil {
		t.Fatalf("prime active appearance cache: %v", err)
	}
	xboardConfigured, err := database.UpdateThemeConfig(ctx, administrator.ID, "Xboard", initial.Themes[0].Revision, theme.Config{
		ThemeColor: "blue", BackgroundURL: "", FontScale: "normal", Radius: "rounded",
	}, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("update active Xboard config: %v", err)
	}
	appearance, err := database.GetActiveThemeAppearance(ctx)
	if err != nil || appearance.Config != xboardConfigured.Config || appearance.Palette != xboardConfigured.Palettes["blue"] {
		t.Fatalf("active appearance cache was stale: appearance=%#v err=%v", appearance, err)
	}

	installed, err := database.InstallTheme(ctx, administrator.ID, testThemePackage("Aurora", "1.0.0", "a"), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if installed.Name != "Aurora" || installed.Revision != 1 || installed.IsActive || !installed.CanDelete {
		t.Fatalf("installed theme=%#v", installed)
	}
	if _, err := database.InstallTheme(ctx, administrator.ID, testThemePackage("Aurora", "1.0.0", "b"), now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("same version InstallTheme() error=%v, want ErrConflict", err)
	}

	updated, err := database.UpdateThemeConfig(ctx, administrator.ID, "aurora", installed.Revision, theme.Config{
		ThemeColor: "blue", BackgroundURL: "", FontScale: "large", Radius: "pill",
	}, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Config.ThemeColor != "blue" || updated.Config.FontScale != "large" {
		t.Fatalf("updated theme=%#v", updated)
	}
	if _, err := database.UpdateThemeConfig(ctx, administrator.ID, "Aurora", installed.Revision, installed.Config, now.Add(4*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateThemeConfig() error=%v, want ErrConflict", err)
	}

	active, err := database.ActivateTheme(ctx, administrator.ID, "Aurora", initial.Revision, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if active.ActiveTheme != "Aurora" || active.Revision != 2 {
		t.Fatalf("active catalog=%#v", active)
	}
	if _, err := database.ActivateTheme(ctx, administrator.ID, "Xboard", initial.Revision, now.Add(6*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ActivateTheme() error=%v, want ErrConflict", err)
	}
	if err := database.DeleteTheme(ctx, administrator.ID, "Aurora", now.Add(7*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteTheme(active) error=%v, want ErrConflict", err)
	}

	if _, err := database.InstallTheme(ctx, administrator.ID, testThemePackage("Disposable", "1.0.0", "c"), now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTheme(ctx, administrator.ID, "Disposable", now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetTheme(ctx, "Disposable"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTheme(deleted) error=%v, want ErrNotFound", err)
	}
}

func TestThemeUpgradePreservesCompatibleConfigAndReplacesAssetsAtomically(t *testing.T) {
	database := newTestStore(t)
	administrator, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "theme-upgrade@example.test", PasswordHash: "hash", IsAdmin: true}, time.Now())
	first := testThemePackage("Aurora", "1.0.0", "a")
	first.Assets = []theme.Asset{testThemeAsset("assets/background.png", "first")}
	first.Manifest.Backgrounds = []string{"assets/background.png"}
	first.Manifest.DefaultConfig.BackgroundURL = "assets/background.png"
	installed, err := database.InstallTheme(t.Context(), administrator.ID, first, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := database.UpdateThemeConfig(t.Context(), administrator.ID, "Aurora", installed.Revision, theme.Config{
		ThemeColor: "blue", BackgroundURL: "assets/background.png", FontScale: "large", Radius: "pill",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	upgrade := testThemePackage("Aurora", "2.0.0", "b")
	upgrade.Assets = []theme.Asset{testThemeAsset("assets/background.png", "second")}
	upgrade.Manifest.Backgrounds = []string{"assets/background.png"}
	upgrade.Manifest.DefaultConfig.BackgroundURL = ""
	upgraded, err := database.InstallTheme(t.Context(), administrator.ID, upgrade, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Version != "2.0.0" || upgraded.Revision != configured.Revision+1 || upgraded.Config != configured.Config {
		t.Fatalf("upgraded theme=%#v configured=%#v", upgraded, configured)
	}
	asset, err := database.GetThemeAsset(t.Context(), "Aurora", upgrade.SHA256, "assets/background.png")
	if err != nil || string(asset.Data) != "second" || asset.SHA256 != testThemeAsset("unused", "second").SHA256 {
		t.Fatalf("upgraded asset=%#v err=%v", asset, err)
	}
	if _, err := database.GetThemeAsset(t.Context(), "Aurora", first.SHA256, "assets/background.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old package asset error=%v, want ErrNotFound", err)
	}
}

func TestSchemaV51CreatesAndValidatesThemeCatalog(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	if _, err := database.db.ExecContext(ctx, `DROP TABLE theme_assets; DROP TABLE theme_settings; DROP TABLE themes; PRAGMA user_version = 50`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v50 to v51) error=%v", err)
	}
	var version, themes, settings int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	_ = database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM themes`).Scan(&themes)
	_ = database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM theme_settings`).Scan(&settings)
	if version != 51 || themes != 1 || settings != 1 {
		t.Fatalf("version=%d themes=%d settings=%d", version, themes, settings)
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		t.Fatalf("ValidateCurrentSchema() error=%v", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM theme_settings`); err != nil {
		t.Fatal(err)
	}
	if err := database.ValidateCurrentSchema(ctx); err == nil || !strings.Contains(err.Error(), "exactly one row") {
		t.Fatalf("ValidateCurrentSchema() missing singleton error=%v", err)
	}
}

func testThemePackage(name, version, digestCharacter string) theme.Package {
	manifest := theme.Manifest{
		FormatVersion: 1, Name: name, Description: "Safe theme", Version: version,
		Palettes: map[string]theme.Palette{
			"default": {Background: "#111111", Surface: "#18181b", Text: "#f4f4f5", Muted: "#a1a1aa", Primary: "#a5b4fc", PrimaryText: "#111111", Border: "#3f3f46"},
			"blue":    {Background: "#101827", Surface: "#172033", Text: "#f4f4f5", Muted: "#a1a1aa", Primary: "#93c5fd", PrimaryText: "#111111", Border: "#334155"},
		},
		DefaultConfig: theme.Config{ThemeColor: "default", FontScale: "normal", Radius: "rounded"},
	}
	return theme.Package{Manifest: manifest, ManifestJSON: []byte(`{}`), SHA256: strings.Repeat(digestCharacter, 64)}
}

func testThemeAsset(path, body string) theme.Asset {
	digest := sha256.Sum256([]byte(body))
	return theme.Asset{Path: path, MIME: "image/png", SHA256: hex.EncodeToString(digest[:]), Width: 1, Height: 1, Data: []byte(body)}
}
