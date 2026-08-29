package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxClientAppVersionBytes = 128
	maxClientAppURLBytes     = 2_048
)

type clientAppSettingsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) GetClientAppSettings(ctx context.Context) (ClientAppSettings, error) {
	settings, err := readClientAppSettings(ctx, s.db)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("get client app settings: %w", err)
	}
	return settings, nil
}

// ClientAppVersionTokenExists intentionally checks identity only. Legacy
// getVersion returned release metadata for banned and expired accounts as long
// as their subscription token still belonged to an account.
func (s *Store) ClientAppVersionTokenExists(ctx context.Context, token string) (bool, error) {
	if !validSubscriptionToken(token) {
		return false, nil
	}
	var marker int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM users
		WHERE subscription_token = ? AND account_kind IN ('human', 'internal_subscription')
		LIMIT 1
	`, token).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find client app version token: %w", err)
	}
	return marker == 1, nil
}

func (s *Store) UpdateClientAppSettings(ctx context.Context, administratorID, revision int64, input SaveClientAppSettingsInput, now time.Time) (ClientAppSettings, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 {
		return ClientAppSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeClientAppSettings(input)
	if err != nil {
		return ClientAppSettings{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("begin client app settings update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE client_app_settings
		SET windows_version = ?, windows_download_url = ?,
		    macos_version = ?, macos_download_url = ?,
		    android_version = ?, android_download_url = ?,
		    revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE id = 1 AND revision = ?
	`, normalized.WindowsVersion, normalized.WindowsDownloadURL,
		normalized.MacOSVersion, normalized.MacOSDownloadURL,
		normalized.AndroidVersion, normalized.AndroidDownloadURL,
		administratorID, now.Unix(), revision)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("update client app settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("count client app settings update: %w", err)
	}
	if changed != 1 {
		return ClientAppSettings{}, ErrConflict
	}
	settings, err := readClientAppSettings(ctx, tx)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("read updated client app settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ClientAppSettings{}, fmt.Errorf("commit client app settings update: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateLegacyClientAppSettings(ctx context.Context, administratorID int64, input SaveLegacyClientAppSettingsInput, now time.Time) (ClientAppSettings, error) {
	if administratorID < 1 || now.Unix() < 0 || !legacyClientAppSettingsTouched(input) {
		return ClientAppSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("begin legacy client app settings update: %w", err)
	}
	defer tx.Rollback()
	current, err := readClientAppSettings(ctx, tx)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("read legacy client app settings: %w", err)
	}
	merged := SaveClientAppSettingsInput{
		WindowsVersion: current.WindowsVersion, WindowsDownloadURL: current.WindowsDownloadURL,
		MacOSVersion: current.MacOSVersion, MacOSDownloadURL: current.MacOSDownloadURL,
		AndroidVersion: current.AndroidVersion, AndroidDownloadURL: current.AndroidDownloadURL,
	}
	mergeLegacyClientAppSettings(&merged, input)
	normalized, err := normalizeClientAppSettings(merged)
	if err != nil {
		return ClientAppSettings{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE client_app_settings
		SET windows_version = ?, windows_download_url = ?,
		    macos_version = ?, macos_download_url = ?,
		    android_version = ?, android_download_url = ?,
		    revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE id = 1 AND revision = ?
	`, normalized.WindowsVersion, normalized.WindowsDownloadURL,
		normalized.MacOSVersion, normalized.MacOSDownloadURL,
		normalized.AndroidVersion, normalized.AndroidDownloadURL,
		administratorID, now.Unix(), current.Revision)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("update legacy client app settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("count legacy client app settings update: %w", err)
	}
	if changed != 1 {
		return ClientAppSettings{}, ErrConflict
	}
	settings, err := readClientAppSettings(ctx, tx)
	if err != nil {
		return ClientAppSettings{}, fmt.Errorf("read updated legacy client app settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ClientAppSettings{}, fmt.Errorf("commit legacy client app settings update: %w", err)
	}
	return settings, nil
}

func readClientAppSettings(ctx context.Context, query clientAppSettingsQuery) (ClientAppSettings, error) {
	var settings ClientAppSettings
	var updatedAt int64
	if err := query.QueryRowContext(ctx, `
		SELECT revision, windows_version, windows_download_url,
		       macos_version, macos_download_url, android_version, android_download_url, updated_at
		FROM client_app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.WindowsVersion, &settings.WindowsDownloadURL,
		&settings.MacOSVersion, &settings.MacOSDownloadURL,
		&settings.AndroidVersion, &settings.AndroidDownloadURL, &updatedAt); err != nil {
		return ClientAppSettings{}, err
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}

func normalizeClientAppSettings(input SaveClientAppSettingsInput) (SaveClientAppSettingsInput, error) {
	input.WindowsVersion = strings.TrimSpace(input.WindowsVersion)
	input.WindowsDownloadURL = strings.TrimSpace(input.WindowsDownloadURL)
	input.MacOSVersion = strings.TrimSpace(input.MacOSVersion)
	input.MacOSDownloadURL = strings.TrimSpace(input.MacOSDownloadURL)
	input.AndroidVersion = strings.TrimSpace(input.AndroidVersion)
	input.AndroidDownloadURL = strings.TrimSpace(input.AndroidDownloadURL)
	for _, version := range []string{input.WindowsVersion, input.MacOSVersion, input.AndroidVersion} {
		if !validClientAppVersion(version) {
			return SaveClientAppSettingsInput{}, fmt.Errorf("%w: invalid client app version", ErrInvalidInput)
		}
	}
	for _, address := range []string{input.WindowsDownloadURL, input.MacOSDownloadURL, input.AndroidDownloadURL} {
		if !validClientAppDownloadURL(address) {
			return SaveClientAppSettingsInput{}, fmt.Errorf("%w: invalid client app download URL", ErrInvalidInput)
		}
	}
	return input, nil
}

func validClientAppVersion(value string) bool {
	return utf8.ValidString(value) && len(value) <= maxClientAppVersionBytes &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validClientAppDownloadURL(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || len(value) > maxClientAppURLBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil &&
		parsed.Fragment == "" && parsed.Opaque == ""
}

func legacyClientAppSettingsTouched(input SaveLegacyClientAppSettingsInput) bool {
	return input.WindowsVersion != nil || input.WindowsDownloadURL != nil ||
		input.MacOSVersion != nil || input.MacOSDownloadURL != nil ||
		input.AndroidVersion != nil || input.AndroidDownloadURL != nil
}

func mergeLegacyClientAppSettings(target *SaveClientAppSettingsInput, input SaveLegacyClientAppSettingsInput) {
	if input.WindowsVersion != nil {
		target.WindowsVersion = *input.WindowsVersion
	}
	if input.WindowsDownloadURL != nil {
		target.WindowsDownloadURL = *input.WindowsDownloadURL
	}
	if input.MacOSVersion != nil {
		target.MacOSVersion = *input.MacOSVersion
	}
	if input.MacOSDownloadURL != nil {
		target.MacOSDownloadURL = *input.MacOSDownloadURL
	}
	if input.AndroidVersion != nil {
		target.AndroidVersion = *input.AndroidVersion
	}
	if input.AndroidDownloadURL != nil {
		target.AndroidDownloadURL = *input.AndroidDownloadURL
	}
}
