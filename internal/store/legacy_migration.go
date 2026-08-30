package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacyContentSlice = "content-settings-v1"

type LegacySiteSettings struct {
	AppName        *string `json:"app_name,omitempty"`
	AppDescription *string `json:"app_description,omitempty"`
	AppURL         *string `json:"app_url,omitempty"`
	TOSURL         *string `json:"tos_url,omitempty"`
	Logo           *string `json:"logo,omitempty"`
}

func (settings LegacySiteSettings) PresentCount() int {
	count := 0
	for _, value := range []*string{settings.AppName, settings.AppDescription, settings.AppURL, settings.TOSURL, settings.Logo} {
		if value != nil {
			count++
		}
	}
	return count
}

type LegacyNotice struct {
	ID           int64    `json:"id"`
	SortPosition int      `json:"sort"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	ImageURL     string   `json:"image_url"`
	Tags         []string `json:"tags"`
	Visible      bool     `json:"show"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
}

type LegacyContentChecksums struct {
	SiteSettings  string `json:"site_settings"`
	Notices       string `json:"notices"`
	ClientCatalog string `json:"client_catalog"`
}

type LegacyContentImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	SiteSettings         LegacySiteSettings
	Notices              []LegacyNotice
	ClientCatalogPresent bool
	ClientCatalogLinks   []ClientCatalogOverride
	Checksums            LegacyContentChecksums
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyDomainResult struct {
	SourceRows     int    `json:"source_rows"`
	TargetRows     int    `json:"target_rows"`
	SourceChecksum string `json:"source_checksum"`
	TargetChecksum string `json:"target_checksum"`
}

type LegacyContentImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	SiteSettings         LegacyDomainResult `json:"site_settings"`
	Notices              LegacyDomainResult `json:"notices"`
	ClientCatalog        LegacyDomainResult `json:"client_catalog"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacySiteSettingsChecksum(settings LegacySiteSettings) string {
	return legacyCanonicalChecksum(settings)
}

func LegacyNoticesChecksum(notices []LegacyNotice) string {
	ordered := append([]LegacyNotice(nil), notices...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyNotice{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyClientCatalogChecksum(links []ClientCatalogOverride) string {
	ordered := append([]ClientCatalogOverride(nil), links...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].ClientID != ordered[right].ClientID {
			return ordered[left].ClientID < ordered[right].ClientID
		}
		if ordered[left].Platform != ordered[right].Platform {
			return ordered[left].Platform < ordered[right].Platform
		}
		return ordered[left].Action < ordered[right].Action
	})
	if ordered == nil {
		ordered = []ClientCatalogOverride{}
	}
	return legacyCanonicalChecksum(ordered)
}

func legacyCanonicalChecksum(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal canonical legacy migration value: %v", err))
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Store) LookupLegacyContentImport(ctx context.Context, sourceSHA256 string) (LegacyContentImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyContentImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyContentImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyContentImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyContentImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `
		SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?
	`, LegacyContentSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyContentImportReport{}, false, nil
	}
	if err != nil {
		return LegacyContentImportReport{}, false, fmt.Errorf("lookup legacy content migration: %w", err)
	}
	var report LegacyContentImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyContentImportReport{}, false, fmt.Errorf("decode legacy content migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyContent(ctx context.Context, input LegacyContentImport, now time.Time) (LegacyContentImportReport, error) {
	if err := validateLegacyContentImport(input); err != nil {
		return LegacyContentImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("begin legacy content import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("read legacy import target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyContentImportReport{}, fmt.Errorf("legacy content import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("validate legacy import target schema: %w", err)
	}
	if existing, found, err := lookupLegacyContentImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyContentImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyContentImportReport{}, fmt.Errorf("commit idempotent legacy content import: %w", err)
		}
		return existing, nil
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyContentSlice).Scan(&otherRuns); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("count legacy content migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyContentImportReport{}, fmt.Errorf("%w: legacy content slice was already imported from another snapshot", ErrConflict)
	}

	current, err := readLegacyImportSiteSettings(ctx, tx)
	if err != nil {
		return LegacyContentImportReport{}, err
	}
	var noticeCount, linkCount int
	var catalogRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notices`).Scan(&noticeCount); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("count target notices: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM client_catalog_config WHERE id = 1`).Scan(&catalogRevision); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("read target client catalog revision: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM client_catalog_links`).Scan(&linkCount); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("count target client links: %w", err)
	}
	if current.Revision != 1 || current.AppName != "Xboard-Go" || current.AppDescription != "" || current.AppURL != "" || current.TOSURL != "" || current.Logo != "" ||
		noticeCount != 0 || catalogRevision != 1 || linkCount != 0 {
		return LegacyContentImportReport{}, fmt.Errorf("%w: legacy content import requires default site identity, empty notices, and default client catalog", ErrConflict)
	}

	mergedSettings, err := validateAndMergeLegacySiteSettings(current, input.SiteSettings)
	if err != nil {
		return LegacyContentImportReport{}, err
	}
	if input.SiteSettings.PresentCount() > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE app_settings
			SET app_name = ?, app_description = ?, app_url = ?, tos_url = ?, logo = ?,
			    revision = revision + 1, updated_at = ?
			WHERE id = 1 AND revision = 1
		`, mergedSettings.AppName, mergedSettings.AppDescription, mergedSettings.AppURL, mergedSettings.TOSURL, mergedSettings.Logo, now.UTC().Unix()); err != nil {
			return LegacyContentImportReport{}, fmt.Errorf("import legacy site settings: %w", err)
		}
	}

	for _, notice := range input.Notices {
		tagsJSON, err := validateLegacyNotice(notice)
		if err != nil {
			return LegacyContentImportReport{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notices (id, sort_position, title, content, image_url, tags_json, visible, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		`, notice.ID, notice.SortPosition, notice.Title, notice.Content, nullableString(notice.ImageURL), tagsJSON, notice.Visible, notice.CreatedAt, notice.UpdatedAt); err != nil {
			return LegacyContentImportReport{}, fmt.Errorf("import legacy notice id %d: %w", notice.ID, err)
		}
	}
	if input.ClientCatalogPresent {
		for _, link := range input.ClientCatalogLinks {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO client_catalog_links (client_id, platform, action, url, updated_at)
				VALUES (?, ?, ?, ?, ?)
			`, link.ClientID, link.Platform, link.Action, link.URL, now.UTC().Unix()); err != nil {
				return LegacyContentImportReport{}, fmt.Errorf("import legacy client catalog link: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE client_catalog_config SET revision = 2, updated_at = ? WHERE id = 1 AND revision = 1`, now.UTC().Unix()); err != nil {
			return LegacyContentImportReport{}, fmt.Errorf("import legacy client catalog revision: %w", err)
		}
	}

	targetSettings := legacySiteSettingsFromTarget(mergedSettings, input.SiteSettings)
	targetNotices, err := readLegacyTargetNotices(ctx, tx)
	if err != nil {
		return LegacyContentImportReport{}, err
	}
	targetLinks, err := readLegacyTargetClientLinks(ctx, tx)
	if err != nil {
		return LegacyContentImportReport{}, err
	}
	report := LegacyContentImportReport{
		Slice: LegacyContentSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		SiteSettings:  LegacyDomainResult{SourceRows: input.SiteSettings.PresentCount(), TargetRows: targetSettings.PresentCount(), SourceChecksum: input.Checksums.SiteSettings, TargetChecksum: LegacySiteSettingsChecksum(targetSettings)},
		Notices:       LegacyDomainResult{SourceRows: len(input.Notices), TargetRows: len(targetNotices), SourceChecksum: input.Checksums.Notices, TargetChecksum: LegacyNoticesChecksum(targetNotices)},
		ClientCatalog: LegacyDomainResult{SourceRows: len(input.ClientCatalogLinks), TargetRows: len(targetLinks), SourceChecksum: input.Checksums.ClientCatalog, TargetChecksum: LegacyClientCatalogChecksum(targetLinks)},
		AppliedAt:     now.UTC(), AlreadyApplied: false,
	}
	if report.SiteSettings.SourceRows != report.SiteSettings.TargetRows || report.SiteSettings.SourceChecksum != report.SiteSettings.TargetChecksum ||
		report.Notices.SourceRows != report.Notices.TargetRows || report.Notices.SourceChecksum != report.Notices.TargetChecksum ||
		report.ClientCatalog.SourceRows != report.ClientCatalog.TargetRows || report.ClientCatalog.SourceChecksum != report.ClientCatalog.TargetChecksum {
		return LegacyContentImportReport{}, errors.New("legacy content target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("encode legacy content migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("record legacy content migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyContentImportReport{}, fmt.Errorf("commit legacy content import: %w", err)
	}
	return report, nil
}

func validateLegacyContentImport(input LegacyContentImport) error {
	if input.Slice != LegacyContentSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 ||
		!utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 {
		return ErrInvalidInput
	}
	if input.Checksums.SiteSettings != LegacySiteSettingsChecksum(input.SiteSettings) || input.Checksums.Notices != LegacyNoticesChecksum(input.Notices) ||
		input.Checksums.ClientCatalog != LegacyClientCatalogChecksum(input.ClientCatalogLinks) {
		return fmt.Errorf("%w: legacy source checksum mismatch", ErrInvalidInput)
	}
	seenNotices := make(map[int64]struct{}, len(input.Notices))
	for _, notice := range input.Notices {
		if _, exists := seenNotices[notice.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy notice id %d", ErrInvalidInput, notice.ID)
		}
		seenNotices[notice.ID] = struct{}{}
		if _, err := validateLegacyNotice(notice); err != nil {
			return err
		}
	}
	normalizedLinks, err := normalizeClientCatalogOverrides(input.ClientCatalogLinks)
	if err != nil {
		return err
	}
	if len(normalizedLinks) != len(input.ClientCatalogLinks) {
		return ErrInvalidInput
	}
	for index, link := range normalizedLinks {
		if link != input.ClientCatalogLinks[index] || !validLegacyClientURL(link.URL, link.Action) {
			return fmt.Errorf("%w: invalid legacy client catalog link", ErrInvalidInput)
		}
	}
	return nil
}

func validLowerSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validateAndMergeLegacySiteSettings(current SiteSettings, legacy LegacySiteSettings) (SiteSettings, error) {
	input := SaveSiteSettingsInput{
		AppName: current.AppName, AppDescription: current.AppDescription, AppURL: current.AppURL, TOSURL: current.TOSURL, Logo: current.Logo,
		Currency: stringCopyPointer(current.Currency), CurrencySymbol: stringCopyPointer(current.CurrencySymbol),
		StopRegister: current.StopRegister, EmailVerificationEnabled: current.EmailVerificationEnabled,
		EmailWhitelistEnabled: current.EmailWhitelistEnabled, EmailWhitelistSuffixes: current.EmailWhitelistSuffixes,
		GmailAliasLimitEnabled: current.GmailAliasLimitEnabled, RegistrationIPLimitEnabled: current.RegistrationIPLimitEnabled,
		RegistrationIPLimitCount: current.RegistrationIPLimitCount, RegistrationIPLimitMinutes: current.RegistrationIPLimitMinutes,
		PasswordLimitEnabled: current.PasswordLimitEnabled, PasswordLimitCount: current.PasswordLimitCount, PasswordLimitMinutes: current.PasswordLimitMinutes,
		InvitationForceEnabled: current.InvitationForceEnabled, InvitationCodeLimit: current.InvitationCodeLimit, InvitationNeverExpire: current.InvitationNeverExpire,
		MailLoginEnabled: current.MailLoginEnabled, CaptchaEnabled: current.CaptchaEnabled, CaptchaType: current.CaptchaType,
		RecaptchaSiteKey: current.RecaptchaSiteKey, RecaptchaV3SiteKey: current.RecaptchaV3SiteKey,
		RecaptchaV3ScoreThreshold: current.RecaptchaV3ScoreThreshold, TurnstileSiteKey: current.TurnstileSiteKey,
	}
	if legacy.AppName != nil {
		input.AppName = *legacy.AppName
	}
	if legacy.AppDescription != nil {
		input.AppDescription = *legacy.AppDescription
	}
	if legacy.AppURL != nil {
		input.AppURL = *legacy.AppURL
	}
	if legacy.TOSURL != nil {
		input.TOSURL = *legacy.TOSURL
	}
	if legacy.Logo != nil {
		input.Logo = *legacy.Logo
	}
	normalized, err := normalizeSiteSettings(input)
	if err != nil {
		return SiteSettings{}, err
	}
	if normalized.AppName != input.AppName || normalized.AppDescription != input.AppDescription || normalized.AppURL != input.AppURL || normalized.TOSURL != input.TOSURL || normalized.Logo != input.Logo {
		return SiteSettings{}, fmt.Errorf("%w: legacy site settings require normalization", ErrInvalidInput)
	}
	current.AppName, current.AppDescription, current.AppURL, current.TOSURL, current.Logo = input.AppName, input.AppDescription, input.AppURL, input.TOSURL, input.Logo
	return current, nil
}

func validateLegacyNotice(notice LegacyNotice) (string, error) {
	if notice.ID < 1 || notice.SortPosition < 0 || notice.CreatedAt < 0 || notice.UpdatedAt < notice.CreatedAt ||
		!utf8.ValidString(notice.Title) || strings.TrimSpace(notice.Title) == "" || utf8.RuneCountInString(notice.Title) > maxNoticeTitleRunes || strings.IndexFunc(notice.Title, unicode.IsControl) >= 0 ||
		!utf8.ValidString(notice.Content) || strings.TrimSpace(notice.Content) == "" || len(notice.Content) > maxNoticeContentBytes {
		return "", fmt.Errorf("%w: invalid legacy notice id %d", ErrInvalidInput, notice.ID)
	}
	if notice.ImageURL != "" {
		parsed, err := url.ParseRequestURI(notice.ImageURL)
		if err != nil || len(notice.ImageURL) > maxNoticeImageURL || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return "", fmt.Errorf("%w: invalid legacy notice id %d image URL", ErrInvalidInput, notice.ID)
		}
	}
	if len(notice.Tags) > maxNoticeTags {
		return "", fmt.Errorf("%w: invalid legacy notice id %d tags", ErrInvalidInput, notice.ID)
	}
	for _, tag := range notice.Tags {
		if tag == "" || !utf8.ValidString(tag) || utf8.RuneCountInString(tag) > maxNoticeTagRunes || strings.IndexFunc(tag, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("%w: invalid legacy notice id %d tag", ErrInvalidInput, notice.ID)
		}
	}
	tags := notice.Tags
	if tags == nil {
		tags = []string{}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return "", fmt.Errorf("encode legacy notice id %d tags: %w", notice.ID, err)
	}
	return string(encoded), nil
}

func validLegacyClientURL(address, action string) bool {
	if address == "" || len(address) > 2048 || !utf8.ValidString(address) || strings.IndexFunc(address, unicode.IsControl) >= 0 {
		return false
	}
	if action == "tutorial" && strings.HasPrefix(address, "/") && !strings.HasPrefix(address, "//") && !strings.Contains(address, `\`) && strings.IndexFunc(address, unicode.IsSpace) < 0 {
		return true
	}
	if strings.IndexFunc(address, unicode.IsSpace) >= 0 {
		return false
	}
	parsed, err := url.ParseRequestURI(address)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func readLegacyImportSiteSettings(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (SiteSettings, error) {
	settings, err := scanSiteSettings(database.QueryRowContext(ctx, `
		SELECT revision, app_name, app_description, app_url, force_https, subscribe_url, tos_url, logo, currency, currency_symbol, stop_register,
		       email_verify, email_whitelist_enable, email_whitelist_suffix, email_gmail_limit_enable,
		       register_limit_by_ip_enable, register_limit_count, register_limit_expire,
		       password_limit_enable, password_limit_count, password_limit_expire,
		       invite_force, invite_gen_limit, invite_never_expire, login_with_mail_link_enable, try_out_plan_id, try_out_hour, traffic_reset_method, coupon_enabled,
		       captcha_enable, captcha_type, recaptcha_site_key, recaptcha_secret_cipher,
		       recaptcha_v3_site_key, recaptcha_v3_score_threshold, recaptcha_v3_secret_cipher,
		       turnstile_site_key, turnstile_secret_cipher, updated_at
		FROM app_settings WHERE id = 1
	`))
	if err != nil {
		return SiteSettings{}, fmt.Errorf("read legacy import target site settings: %w", err)
	}
	return settings, nil
}

func legacySiteSettingsFromTarget(target SiteSettings, mask LegacySiteSettings) LegacySiteSettings {
	result := LegacySiteSettings{}
	if mask.AppName != nil {
		result.AppName = stringCopyPointer(target.AppName)
	}
	if mask.AppDescription != nil {
		result.AppDescription = stringCopyPointer(target.AppDescription)
	}
	if mask.AppURL != nil {
		result.AppURL = stringCopyPointer(target.AppURL)
	}
	if mask.TOSURL != nil {
		result.TOSURL = stringCopyPointer(target.TOSURL)
	}
	if mask.Logo != nil {
		result.Logo = stringCopyPointer(target.Logo)
	}
	return result
}

func stringCopyPointer(value string) *string { return &value }

func readLegacyTargetNotices(ctx context.Context, database queryer) ([]LegacyNotice, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, sort_position, title, content, image_url, tags_json, visible, created_at, updated_at
		FROM notices ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("read imported notices: %w", err)
	}
	defer rows.Close()
	result := make([]LegacyNotice, 0)
	for rows.Next() {
		var notice LegacyNotice
		var image sql.NullString
		var tagsJSON string
		if err := rows.Scan(&notice.ID, &notice.SortPosition, &notice.Title, &notice.Content, &image, &tagsJSON, &notice.Visible, &notice.CreatedAt, &notice.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan imported notice: %w", err)
		}
		if image.Valid {
			notice.ImageURL = image.String
		}
		if err := json.Unmarshal([]byte(tagsJSON), &notice.Tags); err != nil {
			return nil, fmt.Errorf("decode imported notice tags: %w", err)
		}
		if notice.Tags == nil {
			notice.Tags = []string{}
		}
		result = append(result, notice)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported notices: %w", err)
	}
	return result, nil
}

func readLegacyTargetClientLinks(ctx context.Context, database queryer) ([]ClientCatalogOverride, error) {
	rows, err := database.QueryContext(ctx, `SELECT client_id, platform, action, url FROM client_catalog_links ORDER BY client_id, platform, action`)
	if err != nil {
		return nil, fmt.Errorf("read imported client catalog: %w", err)
	}
	defer rows.Close()
	result := make([]ClientCatalogOverride, 0)
	for rows.Next() {
		var link ClientCatalogOverride
		if err := rows.Scan(&link.ClientID, &link.Platform, &link.Action, &link.URL); err != nil {
			return nil, fmt.Errorf("scan imported client catalog: %w", err)
		}
		result = append(result, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate imported client catalog: %w", err)
	}
	return result, nil
}
