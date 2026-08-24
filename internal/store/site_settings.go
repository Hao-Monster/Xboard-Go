package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxSiteAppNameRunes     = 100
	maxSiteDescriptionRunes = 500
	maxSiteURLBytes         = 2_048
)

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	settings, err := scanSiteSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, tos_url, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("get site settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateSiteSettings(ctx context.Context, administratorID, revision int64, input SaveSiteSettingsInput, now time.Time) (SiteSettings, error) {
	if administratorID < 1 || revision < 1 {
		return SiteSettings{}, ErrInvalidInput
	}
	normalized, err := normalizeSiteSettings(input)
	if err != nil {
		return SiteSettings{}, err
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		UPDATE app_settings
		SET app_name = ?, app_description = ?, app_url = ?, tos_url = ?,
		    updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, normalized.AppName, normalized.AppDescription, normalized.AppURL, normalized.TOSURL,
		administratorID, now.Unix(), revision)
	if err != nil {
		return SiteSettings{}, fmt.Errorf("update site settings: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return SiteSettings{}, fmt.Errorf("count updated site settings: %w", err)
	}
	if rows != 1 {
		return SiteSettings{}, ErrConflict
	}
	settings, err := scanSiteSettings(s.db.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, tos_url, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("get updated site settings: %w", err)
	}
	return settings, nil
}

func normalizeSiteSettings(input SaveSiteSettingsInput) (SaveSiteSettingsInput, error) {
	input.AppName = strings.TrimSpace(input.AppName)
	input.AppDescription = strings.TrimSpace(input.AppDescription)
	input.AppURL = strings.TrimSpace(input.AppURL)
	input.TOSURL = strings.TrimSpace(input.TOSURL)
	if !utf8.ValidString(input.AppName) || !utf8.ValidString(input.AppDescription) ||
		!utf8.ValidString(input.AppURL) || !utf8.ValidString(input.TOSURL) ||
		utf8.RuneCountInString(input.AppName) < 1 || utf8.RuneCountInString(input.AppName) > maxSiteAppNameRunes ||
		utf8.RuneCountInString(input.AppDescription) > maxSiteDescriptionRunes ||
		containsUnsafeTicketControl(input.AppName, false) || containsUnsafeTicketControl(input.AppDescription, true) ||
		len(input.AppURL) > maxSiteURLBytes || len(input.TOSURL) > maxSiteURLBytes ||
		(input.AppURL != "" && !validHTTPURL(input.AppURL)) ||
		(input.TOSURL != "" && !validHTTPURL(input.TOSURL)) {
		return SaveSiteSettingsInput{}, fmt.Errorf("%w: invalid site settings", ErrInvalidInput)
	}
	return input, nil
}

func scanSiteSettings(row rowScanner) (SiteSettings, error) {
	var settings SiteSettings
	var updatedAt int64
	if err := row.Scan(&settings.Revision, &settings.AppName, &settings.AppDescription, &settings.AppURL, &settings.TOSURL, &updatedAt); err != nil {
		return SiteSettings{}, err
	}
	settings.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return settings, nil
}
