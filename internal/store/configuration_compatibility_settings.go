package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

type LegacyInvitationSettings struct {
	Revision              int64
	InvitationForce       bool
	InviteCommission      int
	InvitationCodeLimit   int
	InvitationNeverExpire bool
	FirstTimeEnabled      bool
	AutoCheckEnabled      bool
	WithdrawLimit         CurrencyAmount
	WithdrawMethods       []string
	WithdrawClosed        bool
	DistributionEnabled   bool
	DistributionL1        int
	DistributionL2        int
	DistributionL3        int
}

type SaveLegacyInvitationSettingsInput struct {
	InvitationForce       *bool
	InviteCommission      *int
	InvitationCodeLimit   *int
	InvitationNeverExpire *bool
	FirstTimeEnabled      *bool
	AutoCheckEnabled      *bool
	WithdrawLimit         *CurrencyAmount
	WithdrawMethods       *[]string
	WithdrawClosed        *bool
	DistributionEnabled   *bool
	DistributionL1        *int
	DistributionL2        *int
	DistributionL3        *int
}

func (s *Store) GetLegacyInvitationSettings(ctx context.Context) (LegacyInvitationSettings, error) {
	settings, err := readLegacyInvitationSettings(ctx, s.db)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("get legacy invitation settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateLegacyInvitationSettings(ctx context.Context, administratorID int64, input SaveLegacyInvitationSettingsInput, now time.Time) (LegacyInvitationSettings, error) {
	if administratorID < 1 || now.Unix() < 0 || !hasLegacyInvitationSettingsInput(input) {
		return LegacyInvitationSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("begin legacy invitation settings update: %w", err)
	}
	defer tx.Rollback()
	current, err := readLegacyInvitationSettings(ctx, tx)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("read legacy invitation settings: %w", err)
	}
	applyLegacyInvitationSettings(&current, input)
	commissionInput := SaveCommissionSettingsInput{
		InviteCommission: current.InviteCommission, FirstTimeEnabled: current.FirstTimeEnabled,
		AutoCheckEnabled: current.AutoCheckEnabled, WithdrawLimit: &current.WithdrawLimit,
		WithdrawMethods: &current.WithdrawMethods, WithdrawClosed: current.WithdrawClosed,
		DistributionEnabled: current.DistributionEnabled, DistributionL1: current.DistributionL1,
		DistributionL2: current.DistributionL2, DistributionL3: current.DistributionL3,
	}
	if current.InvitationCodeLimit < 0 || current.InvitationCodeLimit > maxInvitationCodeLimit || !validCommissionSettings(commissionInput) {
		return LegacyInvitationSettings{}, ErrInvalidInput
	}
	methodsJSON, err := json.Marshal(current.WithdrawMethods)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("encode legacy commission withdrawal methods: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET
			invite_force = ?, invite_commission = ?, invite_gen_limit = ?, invite_never_expire = ?,
			commission_first_time_enable = ?, commission_auto_check_enable = ?,
			commission_withdraw_limit = ?, commission_withdraw_method = ?, withdraw_close_enable = ?,
			commission_distribution_enable = ?, commission_distribution_l1 = ?,
			commission_distribution_l2 = ?, commission_distribution_l3 = ?,
			updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, current.InvitationForce, current.InviteCommission, current.InvitationCodeLimit, current.InvitationNeverExpire,
		current.FirstTimeEnabled, current.AutoCheckEnabled, current.WithdrawLimit, string(methodsJSON), current.WithdrawClosed,
		current.DistributionEnabled, current.DistributionL1, current.DistributionL2, current.DistributionL3,
		administratorID, now.Unix(), current.Revision)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("update legacy invitation settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("count legacy invitation settings update: %w", err)
	}
	if changed != 1 {
		return LegacyInvitationSettings{}, ErrConflict
	}
	updated, err := readLegacyInvitationSettings(ctx, tx)
	if err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("read updated legacy invitation settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyInvitationSettings{}, fmt.Errorf("commit legacy invitation settings: %w", err)
	}
	return updated, nil
}

func readLegacyInvitationSettings(ctx context.Context, query commissionSettingsQuery) (LegacyInvitationSettings, error) {
	var settings LegacyInvitationSettings
	var methodsJSON string
	if err := query.QueryRowContext(ctx, `
		SELECT revision, invite_force, invite_commission, invite_gen_limit, invite_never_expire,
		       commission_first_time_enable, commission_auto_check_enable,
		       commission_withdraw_limit, commission_withdraw_method, withdraw_close_enable,
		       commission_distribution_enable, commission_distribution_l1,
		       commission_distribution_l2, commission_distribution_l3
		FROM app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.InvitationForce, &settings.InviteCommission, &settings.InvitationCodeLimit,
		&settings.InvitationNeverExpire, &settings.FirstTimeEnabled, &settings.AutoCheckEnabled,
		&settings.WithdrawLimit, &methodsJSON, &settings.WithdrawClosed, &settings.DistributionEnabled,
		&settings.DistributionL1, &settings.DistributionL2, &settings.DistributionL3); err != nil {
		return LegacyInvitationSettings{}, err
	}
	if err := json.Unmarshal([]byte(methodsJSON), &settings.WithdrawMethods); err != nil || !validCommissionWithdrawMethods(settings.WithdrawMethods) {
		if err == nil {
			err = ErrInvalidInput
		}
		return LegacyInvitationSettings{}, fmt.Errorf("decode legacy commission withdrawal methods: %w", err)
	}
	settings.WithdrawMethods = append([]string{}, settings.WithdrawMethods...)
	return settings, nil
}

func hasLegacyInvitationSettingsInput(input SaveLegacyInvitationSettingsInput) bool {
	return input.InvitationForce != nil || input.InviteCommission != nil || input.InvitationCodeLimit != nil ||
		input.InvitationNeverExpire != nil || input.FirstTimeEnabled != nil || input.AutoCheckEnabled != nil ||
		input.WithdrawLimit != nil || input.WithdrawMethods != nil || input.WithdrawClosed != nil ||
		input.DistributionEnabled != nil || input.DistributionL1 != nil || input.DistributionL2 != nil || input.DistributionL3 != nil
}

func applyLegacyInvitationSettings(current *LegacyInvitationSettings, input SaveLegacyInvitationSettingsInput) {
	if input.InvitationForce != nil {
		current.InvitationForce = *input.InvitationForce
	}
	if input.InviteCommission != nil {
		current.InviteCommission = *input.InviteCommission
	}
	if input.InvitationCodeLimit != nil {
		current.InvitationCodeLimit = *input.InvitationCodeLimit
	}
	if input.InvitationNeverExpire != nil {
		current.InvitationNeverExpire = *input.InvitationNeverExpire
	}
	if input.FirstTimeEnabled != nil {
		current.FirstTimeEnabled = *input.FirstTimeEnabled
	}
	if input.AutoCheckEnabled != nil {
		current.AutoCheckEnabled = *input.AutoCheckEnabled
	}
	if input.WithdrawLimit != nil {
		current.WithdrawLimit = *input.WithdrawLimit
	}
	if input.WithdrawMethods != nil {
		current.WithdrawMethods = append([]string{}, (*input.WithdrawMethods)...)
	}
	if input.WithdrawClosed != nil {
		current.WithdrawClosed = *input.WithdrawClosed
	}
	if input.DistributionEnabled != nil {
		current.DistributionEnabled = *input.DistributionEnabled
	}
	if input.DistributionL1 != nil {
		current.DistributionL1 = *input.DistributionL1
	}
	if input.DistributionL2 != nil {
		current.DistributionL2 = *input.DistributionL2
	}
	if input.DistributionL3 != nil {
		current.DistributionL3 = *input.DistributionL3
	}
}

type LegacySiteConfig struct {
	Revision            int64
	Logo                string
	ForceHTTPS          bool
	StopRegister        bool
	AppName             string
	AppDescription      string
	AppURL              string
	SubscribeURL        string
	TrialPlanID         int64
	TrialHours          int
	TOSURL              string
	Currency            string
	CurrencySymbol      string
	TicketMustWaitReply bool
}

type SaveLegacySiteConfigInput struct {
	Logo                *string
	ForceHTTPS          *bool
	StopRegister        *bool
	AppName             *string
	AppDescription      *string
	AppURL              *string
	SubscribeURL        *string
	TrialPlanID         *int64
	TrialHours          *int
	TOSURL              *string
	Currency            *string
	CurrencySymbol      *string
	TicketMustWaitReply *bool
}

func (s *Store) GetLegacySiteConfig(ctx context.Context) (LegacySiteConfig, error) {
	settings, err := readLegacySiteConfig(ctx, s.db)
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("get legacy site config: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateLegacySiteConfig(ctx context.Context, administratorID int64, input SaveLegacySiteConfigInput, now time.Time) (LegacySiteConfig, error) {
	if administratorID < 1 || now.Unix() < 0 || !hasLegacySiteConfigInput(input) {
		return LegacySiteConfig{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("begin legacy site config update: %w", err)
	}
	defer tx.Rollback()
	current, err := readLegacySiteConfig(ctx, tx)
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("read legacy site config: %w", err)
	}
	if err := applyLegacySiteConfig(&current, input); err != nil {
		return LegacySiteConfig{}, err
	}
	if current.TrialPlanID > 0 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, current.TrialPlanID).Scan(&exists); err != nil {
			return LegacySiteConfig{}, fmt.Errorf("validate legacy trial plan: %w", err)
		}
		if !exists {
			return LegacySiteConfig{}, fmt.Errorf("%w: trial plan does not exist", ErrInvalidInput)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE app_settings SET logo = ?, force_https = ?, stop_register = ?, app_name = ?, app_description = ?,
			app_url = ?, subscribe_url = ?, try_out_plan_id = ?, try_out_hour = ?, tos_url = ?, currency = ?,
			currency_symbol = ?, ticket_must_wait_reply = ?, updated_by = ?, updated_at = ?, revision = revision + 1
		WHERE id = 1 AND revision = ?
	`, current.Logo, current.ForceHTTPS, current.StopRegister, current.AppName, current.AppDescription,
		current.AppURL, current.SubscribeURL, current.TrialPlanID, current.TrialHours, current.TOSURL,
		current.Currency, current.CurrencySymbol, current.TicketMustWaitReply, administratorID, now.Unix(), current.Revision)
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("update legacy site config: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("count legacy site config update: %w", err)
	}
	if changed != 1 {
		return LegacySiteConfig{}, ErrConflict
	}
	updated, err := readLegacySiteConfig(ctx, tx)
	if err != nil {
		return LegacySiteConfig{}, fmt.Errorf("read updated legacy site config: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacySiteConfig{}, fmt.Errorf("commit legacy site config: %w", err)
	}
	return updated, nil
}

func readLegacySiteConfig(ctx context.Context, query commissionSettingsQuery) (LegacySiteConfig, error) {
	var settings LegacySiteConfig
	err := query.QueryRowContext(ctx, `
		SELECT revision, logo, force_https, stop_register, app_name, app_description, app_url, subscribe_url,
		       try_out_plan_id, try_out_hour, tos_url, currency, currency_symbol, ticket_must_wait_reply
		FROM app_settings WHERE id = 1
	`).Scan(&settings.Revision, &settings.Logo, &settings.ForceHTTPS, &settings.StopRegister, &settings.AppName,
		&settings.AppDescription, &settings.AppURL, &settings.SubscribeURL, &settings.TrialPlanID, &settings.TrialHours,
		&settings.TOSURL, &settings.Currency, &settings.CurrencySymbol, &settings.TicketMustWaitReply)
	return settings, err
}

func hasLegacySiteConfigInput(input SaveLegacySiteConfigInput) bool {
	return input.Logo != nil || input.ForceHTTPS != nil || input.StopRegister != nil || input.AppName != nil ||
		input.AppDescription != nil || input.AppURL != nil || input.SubscribeURL != nil || input.TrialPlanID != nil ||
		input.TrialHours != nil || input.TOSURL != nil || input.Currency != nil || input.CurrencySymbol != nil || input.TicketMustWaitReply != nil
}

func applyLegacySiteConfig(current *LegacySiteConfig, input SaveLegacySiteConfigInput) error {
	if input.Logo != nil {
		current.Logo = strings.TrimSpace(*input.Logo)
	}
	if input.ForceHTTPS != nil {
		current.ForceHTTPS = *input.ForceHTTPS
	}
	if input.StopRegister != nil {
		current.StopRegister = *input.StopRegister
	}
	if input.AppName != nil {
		current.AppName = strings.TrimSpace(*input.AppName)
	}
	if input.AppDescription != nil {
		current.AppDescription = strings.TrimSpace(*input.AppDescription)
	}
	if input.AppURL != nil {
		current.AppURL = strings.TrimSpace(*input.AppURL)
	}
	if input.SubscribeURL != nil {
		normalized, err := normalizeSubscribeURLStorage(*input.SubscribeURL)
		if err != nil {
			return err
		}
		current.SubscribeURL = normalized
	}
	if input.TrialPlanID != nil {
		current.TrialPlanID = *input.TrialPlanID
	}
	if input.TrialHours != nil {
		current.TrialHours = *input.TrialHours
	}
	if input.TOSURL != nil {
		current.TOSURL = strings.TrimSpace(*input.TOSURL)
	}
	if input.Currency != nil {
		current.Currency = strings.ToUpper(strings.TrimSpace(*input.Currency))
	}
	if input.CurrencySymbol != nil {
		current.CurrencySymbol = strings.TrimSpace(*input.CurrencySymbol)
	}
	if input.TicketMustWaitReply != nil {
		current.TicketMustWaitReply = *input.TicketMustWaitReply
	}
	if !utf8.ValidString(current.Logo) || !utf8.ValidString(current.AppName) || !utf8.ValidString(current.AppDescription) ||
		!utf8.ValidString(current.AppURL) || !utf8.ValidString(current.TOSURL) || !utf8.ValidString(current.CurrencySymbol) ||
		utf8.RuneCountInString(current.AppName) < 1 || utf8.RuneCountInString(current.AppName) > maxSiteAppNameRunes ||
		utf8.RuneCountInString(current.AppDescription) > maxSiteDescriptionRunes ||
		containsUnsafeTicketControl(current.AppName, false) || containsUnsafeTicketControl(current.AppDescription, true) ||
		len(current.Logo) > maxSiteURLBytes || len(current.AppURL) > maxSiteURLBytes || len(current.TOSURL) > maxSiteURLBytes ||
		(current.Logo != "" && !validHTTPURL(current.Logo)) || (current.AppURL != "" && !validHTTPURL(current.AppURL)) ||
		(current.TOSURL != "" && !validHTTPURL(current.TOSURL)) || current.TrialPlanID < 0 || current.TrialHours < 1 || current.TrialHours > 8760 ||
		!validCurrencyCode(current.Currency) || len(current.CurrencySymbol) > maxCurrencySymbolBytes ||
		strings.IndexFunc(current.CurrencySymbol, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: invalid legacy site config", ErrInvalidInput)
	}
	return nil
}

type LegacyFrontendSettings struct {
	Theme         string
	SidebarStyle  string
	HeaderStyle   string
	ThemeColor    string
	BackgroundURL string
}

type SaveLegacyFrontendSettingsInput struct {
	Theme         *string
	SidebarStyle  *string
	HeaderStyle   *string
	ThemeColor    *string
	BackgroundURL *string
}

func (s *Store) GetLegacyFrontendSettings(ctx context.Context) (LegacyFrontendSettings, error) {
	settings, err := readLegacyFrontendSettings(ctx, s.db)
	if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("get legacy frontend settings: %w", err)
	}
	return settings, nil
}

func (s *Store) UpdateLegacyFrontendSettings(ctx context.Context, administratorID int64, input SaveLegacyFrontendSettingsInput, now time.Time) (LegacyFrontendSettings, error) {
	if administratorID < 1 || now.Unix() < 0 || !hasLegacyFrontendSettingsInput(input) {
		return LegacyFrontendSettings{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("begin legacy frontend settings update: %w", err)
	}
	defer tx.Rollback()
	var activeTheme, sidebarStyle, headerStyle string
	var settingsRevision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT active_theme, revision, sidebar_style, header_style FROM theme_settings WHERE id = 1
	`).Scan(&activeTheme, &settingsRevision, &sidebarStyle, &headerStyle); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("read legacy frontend settings: %w", err)
	}
	targetTheme := activeTheme
	if input.Theme != nil {
		targetTheme = strings.TrimSpace(*input.Theme)
	}
	if input.SidebarStyle != nil {
		sidebarStyle = strings.TrimSpace(*input.SidebarStyle)
	}
	if input.HeaderStyle != nil {
		headerStyle = strings.TrimSpace(*input.HeaderStyle)
	}
	if !validThemeLayoutStyle(sidebarStyle) || !validThemeLayoutStyle(headerStyle) || !validThemeLookupName(targetTheme) {
		return LegacyFrontendSettings{}, ErrInvalidInput
	}
	var canonicalTheme, manifestJSON, configJSON string
	var themeRevision int64
	if err := tx.QueryRowContext(ctx, `
		SELECT name, manifest_json, config_json, revision FROM themes WHERE name = ? COLLATE NOCASE
	`, targetTheme).Scan(&canonicalTheme, &manifestJSON, &configJSON, &themeRevision); errors.Is(err, sql.ErrNoRows) {
		return LegacyFrontendSettings{}, ErrNotFound
	} else if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("read legacy frontend theme: %w", err)
	}
	var manifest theme.Manifest
	var config theme.Config
	if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("decode legacy frontend manifest: %w", err)
	}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("decode legacy frontend config: %w", err)
	}
	if input.ThemeColor != nil {
		config.ThemeColor = strings.TrimSpace(*input.ThemeColor)
	}
	if input.BackgroundURL != nil {
		config.BackgroundURL = strings.TrimSpace(*input.BackgroundURL)
	}
	if err := theme.ValidateConfig(manifest, config); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("encode legacy frontend config: %w", err)
	}
	if string(encodedConfig) != configJSON {
		result, err := tx.ExecContext(ctx, `
			UPDATE themes SET config_json = ?, revision = revision + 1, updated_by = ?, updated_at = ?
			WHERE name = ? COLLATE NOCASE AND revision = ?
		`, string(encodedConfig), administratorID, now.Unix(), canonicalTheme, themeRevision)
		if err != nil {
			return LegacyFrontendSettings{}, fmt.Errorf("update legacy frontend theme config: %w", err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			if err != nil {
				return LegacyFrontendSettings{}, fmt.Errorf("count legacy frontend theme update: %w", err)
			}
			return LegacyFrontendSettings{}, ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE theme_settings SET active_theme = ?, sidebar_style = ?, header_style = ?,
			revision = revision + 1, updated_by = ?, updated_at = ? WHERE id = 1 AND revision = ?
	`, canonicalTheme, sidebarStyle, headerStyle, administratorID, now.Unix(), settingsRevision)
	if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("update legacy frontend layout: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return LegacyFrontendSettings{}, fmt.Errorf("count legacy frontend layout update: %w", err)
		}
		return LegacyFrontendSettings{}, ErrConflict
	}
	updated, err := readLegacyFrontendSettings(ctx, tx)
	if err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("read updated legacy frontend settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("commit legacy frontend settings: %w", err)
	}
	return updated, nil
}

func readLegacyFrontendSettings(ctx context.Context, query commissionSettingsQuery) (LegacyFrontendSettings, error) {
	var settings LegacyFrontendSettings
	var configJSON string
	err := query.QueryRowContext(ctx, `
		SELECT theme.active_theme, theme.sidebar_style, theme.header_style, installed.config_json
		FROM theme_settings theme JOIN themes installed ON installed.name = theme.active_theme COLLATE NOCASE
		WHERE theme.id = 1
	`).Scan(&settings.Theme, &settings.SidebarStyle, &settings.HeaderStyle, &configJSON)
	if err != nil {
		return LegacyFrontendSettings{}, err
	}
	var config theme.Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return LegacyFrontendSettings{}, fmt.Errorf("decode active legacy frontend config: %w", err)
	}
	settings.ThemeColor = config.ThemeColor
	settings.BackgroundURL = config.BackgroundURL
	return settings, nil
}

func hasLegacyFrontendSettingsInput(input SaveLegacyFrontendSettingsInput) bool {
	return input.Theme != nil || input.SidebarStyle != nil || input.HeaderStyle != nil || input.ThemeColor != nil || input.BackgroundURL != nil
}
