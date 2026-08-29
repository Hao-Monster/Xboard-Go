package legacymigration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type ClientAppSettingsSnapshot struct {
	Path     string
	Size     int64
	SHA256   string
	Settings store.LegacyClientAppSettings
	Checksum string
}

func ReadClientAppSettingsSnapshot(ctx context.Context, sourcePath string) (ClientAppSettingsSnapshot, error) {
	var settings store.LegacyClientAppSettings
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		if err := validateLegacyQueryBudget(ctx, database, `
			SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(value AS BLOB)), 0)), 0)
			FROM v2_settings WHERE name IN (
				'windows_version','windows_download_url','macos_version','macos_download_url','android_version','android_download_url'
			)
		`, 6, 7_000, "legacy client app settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `
			SELECT name,COALESCE(CAST(value AS TEXT),'') FROM v2_settings
			WHERE name IN (
				'windows_version','windows_download_url','macos_version','macos_download_url','android_version','android_download_url'
			) ORDER BY name
		`)
		if err != nil {
			return fmt.Errorf("read legacy client app settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 6)
		for rows.Next() {
			var name, value string
			if err := rows.Scan(&name, &value); err != nil {
				return fmt.Errorf("scan legacy client app setting: %w", err)
			}
			if _, exists := seen[name]; exists {
				return fmt.Errorf("legacy client app settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			switch name {
			case "windows_version":
				settings.WindowsVersion = value
			case "windows_download_url":
				settings.WindowsDownloadURL = value
			case "macos_version":
				settings.MacOSVersion = value
			case "macos_download_url":
				settings.MacOSDownloadURL = value
			case "android_version":
				settings.AndroidVersion = value
			case "android_download_url":
				settings.AndroidDownloadURL = value
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy client app settings: %w", err)
		}
		settings, err = store.NormalizeLegacyClientAppSettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy client app settings: %w", err)
		}
		return nil
	})
	if err != nil {
		return ClientAppSettingsSnapshot{}, err
	}
	return ClientAppSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, Checksum: store.LegacyClientAppSettingsChecksum(settings),
	}, nil
}
