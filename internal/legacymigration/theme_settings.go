package legacymigration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

type ThemeSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyThemeSettings
	Checksum string
}

func ReadThemeSettingsSnapshot(ctx context.Context, sourcePath string) (ThemeSettingsSnapshot, error) {
	settings := store.LegacyThemeSettings{ActiveTheme: "Xboard", Config: theme.Config{ThemeColor: "default", FontScale: "normal", Radius: "rounded"}}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN ('frontend_theme','current_theme','theme_xboard')
		`, 3, 8_192, "legacy theme settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name, COALESCE(CAST(value AS TEXT), '') FROM v2_settings
			WHERE name IN ('frontend_theme','current_theme','theme_xboard') ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy theme settings: %w", err)
		}
		defer rows.Close()
		values := make(map[string]string, 3)
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy theme setting: %w", err)
			}
			if _, exists := values[name]; exists {
				return fmt.Errorf("legacy theme settings contain duplicate %q rows", name)
			}
			values[name] = value
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy theme settings: %w", err)
		}
		frontend := strings.TrimSpace(values["frontend_theme"])
		current := strings.TrimSpace(values["current_theme"])
		if frontend != "" && current != "" && frontend != current {
			return errors.New("legacy frontend_theme and current_theme disagree")
		}
		if frontend != "" {
			settings.ActiveTheme = frontend
		} else if current != "" {
			settings.ActiveTheme = current
		}
		if encoded, exists := values["theme_xboard"]; exists {
			var legacy struct {
				ThemeColor    string `json:"theme_color"`
				BackgroundURL string `json:"background_url"`
				CustomHTML    string `json:"custom_html"`
			}
			decoder := json.NewDecoder(bytes.NewBufferString(encoded))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&legacy); err != nil {
				return errors.New("legacy theme_xboard JSON is invalid")
			}
			if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
				return errors.New("legacy theme_xboard JSON has trailing content")
			}
			if strings.TrimSpace(legacy.CustomHTML) != "" {
				return errors.New("legacy theme_xboard contains custom HTML or scripts")
			}
			settings.Config.ThemeColor = legacy.ThemeColor
			settings.Config.BackgroundURL = legacy.BackgroundURL
		}
		settings, err = store.NormalizeLegacyThemeSettings(settings)
		if err != nil {
			return fmt.Errorf("legacy theme settings cannot be safely migrated: %w", err)
		}
		return nil
	})
	if err != nil {
		return ThemeSettingsSnapshot{}, err
	}
	return ThemeSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyThemeSettingsChecksum(settings),
	}, nil
}
