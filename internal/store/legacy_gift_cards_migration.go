package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyGiftCardsSlice = "gift-cards-v1"
	maxLegacyGiftCards   = 1_000_000
)

type LegacyGiftCardTemplate struct {
	ID              int64                 `json:"id"`
	Name            string                `json:"name"`
	Description     string                `json:"description"`
	Type            GiftCardType          `json:"type"`
	Status          bool                  `json:"status"`
	Conditions      GiftCardConditions    `json:"conditions"`
	Rewards         GiftCardReward        `json:"rewards"`
	Limits          GiftCardLimits        `json:"limits"`
	SpecialConfig   GiftCardSpecialConfig `json:"special_config"`
	Icon            string                `json:"icon"`
	BackgroundImage string                `json:"background_image"`
	Theme           string                `json:"theme"`
	SortPosition    int                   `json:"sort"`
	AdminID         int64                 `json:"admin_id"`
	CreatedAt       int64                 `json:"created_at"`
	UpdatedAt       int64                 `json:"updated_at"`
}

type LegacyGiftCardCode struct {
	ID            int64              `json:"id"`
	TemplateID    int64              `json:"template_id"`
	Code          string             `json:"code"`
	BatchNo       string             `json:"batch_no"`
	Status        GiftCardCodeStatus `json:"status"`
	UserID        *int64             `json:"user_id"`
	UsedAt        *int64             `json:"used_at"`
	ExpiresAt     *int64             `json:"expires_at"`
	ActualRewards *GiftCardReward    `json:"actual_rewards"`
	UsageCount    int                `json:"usage_count"`
	MaxUsage      int                `json:"max_usage"`
	MetadataJSON  string             `json:"metadata_json"`
	CreatedAt     int64              `json:"created_at"`
	UpdatedAt     int64              `json:"updated_at"`
}

type LegacyGiftCardUsage struct {
	ID                    int64          `json:"id"`
	CodeID                int64          `json:"code_id"`
	TemplateID            int64          `json:"template_id"`
	UserID                int64          `json:"user_id"`
	InviterID             *int64         `json:"inviter_id"`
	Rewards               GiftCardReward `json:"rewards"`
	InviterRewards        GiftCardReward `json:"inviter_rewards"`
	UserLevelAtUse        *int64         `json:"user_level_at_use"`
	UserPlanID            *int64         `json:"user_plan_id"`
	MultiplierBasisPoints int            `json:"multiplier_basis_points"`
	IPAddress             string         `json:"ip_address"`
	UserAgent             string         `json:"user_agent"`
	Notes                 string         `json:"notes"`
	UsedAt                int64          `json:"used_at"`
}

type LegacyGiftCardsImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Templates            []LegacyGiftCardTemplate
	Codes                []LegacyGiftCardCode
	Usages               []LegacyGiftCardUsage
	TemplatesChecksum    string
	CodesChecksum        string
	UsagesChecksum       string
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyGiftCardsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Templates            LegacyDomainResult `json:"templates"`
	Codes                LegacyDomainResult `json:"codes"`
	Usages               LegacyDomainResult `json:"usages"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyGiftCardTemplatesChecksum(values []LegacyGiftCardTemplate) string {
	ordered := append([]LegacyGiftCardTemplate(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyGiftCardTemplate{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyGiftCardCodesChecksum(values []LegacyGiftCardCode) string {
	ordered := append([]LegacyGiftCardCode(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyGiftCardCode{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyGiftCardUsagesChecksum(values []LegacyGiftCardUsage) string {
	ordered := append([]LegacyGiftCardUsage(nil), values...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	if ordered == nil {
		ordered = []LegacyGiftCardUsage{}
	}
	return legacyCanonicalChecksum(ordered)
}

func ValidateLegacyGiftCardsData(templates []LegacyGiftCardTemplate, codes []LegacyGiftCardCode, usages []LegacyGiftCardUsage) error {
	if len(templates) > maxLegacyGiftCards || len(codes) > maxLegacyGiftCards || len(usages) > maxLegacyGiftCards {
		return fmt.Errorf("%w: legacy gift cards exceed the %d-row per-table migration limit", ErrInvalidInput, maxLegacyGiftCards)
	}
	templateIDs := make(map[int64]LegacyGiftCardTemplate, len(templates))
	for _, item := range templates {
		if item.ID < 1 || !validLegacyUnixTimestamp(item.CreatedAt) || !validLegacyUnixTimestamp(item.UpdatedAt) || item.UpdatedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy gift card template id %d", ErrInvalidInput, item.ID)
		}
		if _, duplicate := templateIDs[item.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy gift card template id %d", ErrInvalidInput, item.ID)
		}
		normalized, _, err := normalizeGiftCardTemplate(SaveGiftCardTemplateInput{
			Name: item.Name, Description: item.Description, Type: item.Type, Status: item.Status,
			Conditions: item.Conditions, Rewards: item.Rewards, Limits: item.Limits, SpecialConfig: item.SpecialConfig,
			Icon: item.Icon, BackgroundImage: item.BackgroundImage, Theme: item.Theme, SortPosition: item.SortPosition,
		}, item.AdminID, time.Unix(item.CreatedAt, 0))
		if err != nil || normalized.Name != item.Name || normalized.Description != item.Description || normalized.Icon != item.Icon ||
			normalized.BackgroundImage != item.BackgroundImage || normalized.Theme != item.Theme {
			return fmt.Errorf("%w: invalid legacy gift card template id %d", ErrInvalidInput, item.ID)
		}
		templateIDs[item.ID] = item
	}
	codeIDs := make(map[int64]LegacyGiftCardCode, len(codes))
	codeValues := make(map[string]struct{}, len(codes))
	for _, item := range codes {
		var metadata map[string]any
		if item.ID < 1 || item.TemplateID < 1 || item.Code != strings.ToUpper(strings.TrimSpace(item.Code)) || !validGiftCardCode(item.Code) ||
			len(item.BatchNo) < 16 || len(item.BatchNo) > 40 || strings.IndexFunc(item.BatchNo, func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-')
		}) >= 0 || item.Status < GiftCardCodeActive || item.Status > GiftCardCodeExpired || item.UsageCount < 0 ||
			item.MaxUsage < 1 || item.MaxUsage > 1_000_000_000 || item.UsageCount > item.MaxUsage ||
			!validLegacyOptionalTimestamp(item.UsedAt) || !validLegacyOptionalTimestamp(item.ExpiresAt) ||
			!validLegacyUnixTimestamp(item.CreatedAt) || !validLegacyUnixTimestamp(item.UpdatedAt) || item.UpdatedAt < item.CreatedAt ||
			len(item.MetadataJSON) > 8192 || json.Unmarshal([]byte(item.MetadataJSON), &metadata) != nil || metadata == nil {
			return fmt.Errorf("%w: invalid legacy gift card code id %d", ErrInvalidInput, item.ID)
		}
		if _, exists := templateIDs[item.TemplateID]; !exists {
			return fmt.Errorf("%w: legacy gift card code id %d references a missing template", ErrConflict, item.ID)
		}
		if _, duplicate := codeIDs[item.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy gift card code id %d", ErrInvalidInput, item.ID)
		}
		if _, duplicate := codeValues[item.Code]; duplicate {
			return fmt.Errorf("%w: duplicate legacy gift card code %q", ErrConflict, item.Code)
		}
		template := templateIDs[item.TemplateID]
		if item.ActualRewards != nil && !validLegacyGiftCardAppliedReward(template.Type, *item.ActualRewards) {
			return fmt.Errorf("%w: invalid actual rewards on legacy gift card code id %d", ErrInvalidInput, item.ID)
		}
		codeIDs[item.ID], codeValues[item.Code] = item, struct{}{}
	}
	usageIDs := make(map[int64]struct{}, len(usages))
	for _, item := range usages {
		code, codeExists := codeIDs[item.CodeID]
		template := templateIDs[item.TemplateID]
		if item.ID < 1 || item.CodeID < 1 || item.TemplateID < 1 || item.UserID < 1 || !codeExists || code.TemplateID != item.TemplateID ||
			item.MultiplierBasisPoints < 0 || item.MultiplierBasisPoints > 1_000_000 || !validLegacyUnixTimestamp(item.UsedAt) ||
			!validLegacyGiftCardAuditText(item.IPAddress, 45) || !validLegacyGiftCardAuditText(item.UserAgent, 1024) ||
			!validLegacyGiftCardAuditText(item.Notes, 4096) || !validLegacyGiftCardAppliedReward(template.Type, item.Rewards) ||
			!giftCardRewardEmpty(item.InviterRewards) && validateGiftCardReward(GiftCardTypeGeneral, item.InviterRewards, true) != nil {
			return fmt.Errorf("%w: invalid legacy gift card usage id %d", ErrInvalidInput, item.ID)
		}
		if _, duplicate := usageIDs[item.ID]; duplicate {
			return fmt.Errorf("%w: duplicate legacy gift card usage id %d", ErrInvalidInput, item.ID)
		}
		usageIDs[item.ID] = struct{}{}
	}
	return nil
}

func (s *Store) LookupLegacyGiftCardsImport(ctx context.Context, sourceSHA256 string) (LegacyGiftCardsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyGiftCardsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyGiftCardsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyGiftCardsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyGiftCardsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyGiftCardsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyGiftCardsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyGiftCardsImportReport{}, false, fmt.Errorf("lookup legacy gift card migration: %w", err)
	}
	var report LegacyGiftCardsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyGiftCardsImportReport{}, false, fmt.Errorf("decode legacy gift card migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyGiftCards(ctx context.Context, input LegacyGiftCardsImport, now time.Time) (LegacyGiftCardsImportReport, error) {
	if err := validateLegacyGiftCardsImport(input); err != nil {
		return LegacyGiftCardsImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("begin legacy gift card import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("read legacy gift card target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("legacy gift card import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("validate legacy gift card target schema: %w", err)
	}
	if existing, found, err := lookupLegacyGiftCardsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyGiftCardsImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyGiftCardsImportReport{}, fmt.Errorf("commit idempotent legacy gift card import: %w", err)
		}
		return existing, nil
	}
	var otherRuns, templates, codes, usages int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyGiftCardsSlice).Scan(&otherRuns); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("count legacy gift card migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("%w: legacy gift card slice was already imported from another snapshot", ErrConflict)
	}
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM gift_card_templates), (SELECT COUNT(*) FROM gift_card_codes), (SELECT COUNT(*) FROM gift_card_usages)`).Scan(&templates, &codes, &usages); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("count target gift cards: %w", err)
	}
	if templates != 0 || codes != 0 || usages != 0 {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("%w: legacy gift card import requires empty gift card tables", ErrConflict)
	}
	if err := validateLegacyGiftCardExternalReferences(ctx, tx, input); err != nil {
		return LegacyGiftCardsImportReport{}, err
	}
	if err := insertLegacyGiftCards(ctx, tx, input); err != nil {
		return LegacyGiftCardsImportReport{}, err
	}
	targetTemplates, targetCodes, targetUsages, err := readLegacyTargetGiftCards(ctx, tx)
	if err != nil {
		return LegacyGiftCardsImportReport{}, err
	}
	report := LegacyGiftCardsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Templates: LegacyDomainResult{SourceRows: len(input.Templates), TargetRows: len(targetTemplates), SourceChecksum: input.TemplatesChecksum, TargetChecksum: LegacyGiftCardTemplatesChecksum(targetTemplates)},
		Codes:     LegacyDomainResult{SourceRows: len(input.Codes), TargetRows: len(targetCodes), SourceChecksum: input.CodesChecksum, TargetChecksum: LegacyGiftCardCodesChecksum(targetCodes)},
		Usages:    LegacyDomainResult{SourceRows: len(input.Usages), TargetRows: len(targetUsages), SourceChecksum: input.UsagesChecksum, TargetChecksum: LegacyGiftCardUsagesChecksum(targetUsages)},
		AppliedAt: now.UTC(),
	}
	if !legacyDomainMatches(report.Templates) || !legacyDomainMatches(report.Codes) || !legacyDomainMatches(report.Usages) {
		return LegacyGiftCardsImportReport{}, errors.New("legacy gift card target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("encode legacy gift card migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs (slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("record legacy gift card migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyGiftCardsImportReport{}, fmt.Errorf("commit legacy gift card import: %w", err)
	}
	return report, nil
}

func validateLegacyGiftCardsImport(input LegacyGiftCardsImport) error {
	if input.Slice != LegacyGiftCardsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		strings.TrimSpace(input.RollbackBackupPath) == "" || !validLowerSHA256(input.RollbackBackupSHA256) ||
		input.TemplatesChecksum != LegacyGiftCardTemplatesChecksum(input.Templates) ||
		input.CodesChecksum != LegacyGiftCardCodesChecksum(input.Codes) || input.UsagesChecksum != LegacyGiftCardUsagesChecksum(input.Usages) {
		return ErrInvalidInput
	}
	return ValidateLegacyGiftCardsData(input.Templates, input.Codes, input.Usages)
}

func validateLegacyGiftCardExternalReferences(ctx context.Context, tx *sql.Tx, input LegacyGiftCardsImport) error {
	users := make(map[int64]struct{})
	plans := make(map[int64]struct{})
	addUser := func(id *int64) {
		if id != nil {
			users[*id] = struct{}{}
		}
	}
	addPlan := func(id *int64) {
		if id != nil {
			plans[*id] = struct{}{}
		}
	}
	for _, item := range input.Templates {
		users[item.AdminID] = struct{}{}
		for _, id := range append(append([]int64(nil), item.Conditions.AllowedPlanIDs...), item.Conditions.DisallowedPlanIDs...) {
			plans[id] = struct{}{}
		}
		addPlan(item.Rewards.PlanID)
	}
	for _, item := range input.Codes {
		addUser(item.UserID)
	}
	for _, item := range input.Usages {
		users[item.UserID] = struct{}{}
		addUser(item.InviterID)
		addPlan(item.UserPlanID)
	}
	for id := range users {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("validate legacy gift card user: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: legacy gift card references missing user id %d", ErrConflict, id)
		}
	}
	for id := range plans {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("validate legacy gift card plan: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: legacy gift card references missing plan id %d", ErrConflict, id)
		}
	}
	return nil
}

func insertLegacyGiftCards(ctx context.Context, tx *sql.Tx, input LegacyGiftCardsImport) error {
	for _, item := range input.Templates {
		_, encoded, err := normalizeGiftCardTemplate(SaveGiftCardTemplateInput{Name: item.Name, Description: item.Description, Type: item.Type, Status: item.Status, Conditions: item.Conditions, Rewards: item.Rewards, Limits: item.Limits, SpecialConfig: item.SpecialConfig, Icon: item.Icon, BackgroundImage: item.BackgroundImage, Theme: item.Theme, SortPosition: item.SortPosition}, item.AdminID, time.Unix(item.CreatedAt, 0))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gift_card_templates (id, name, description, type, status, conditions_json, rewards_json, limits_json, special_config_json, icon, background_image, theme, sort_position, admin_id, revision, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			item.ID, item.Name, item.Description, item.Type, item.Status, encoded.conditions, encoded.rewards, encoded.limits, encoded.special, item.Icon, item.BackgroundImage, item.Theme, item.SortPosition, item.AdminID, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy gift card template id %d: %w", item.ID, err)
		}
	}
	for _, item := range input.Codes {
		var actual any
		if item.ActualRewards != nil {
			encoded, _ := json.Marshal(item.ActualRewards)
			actual = string(encoded)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gift_card_codes (id, template_id, code, batch_no, status, user_id, used_at, expires_at, actual_rewards_json, usage_count, max_usage, metadata_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.TemplateID, item.Code, item.BatchNo, item.Status, nullableLegacyInt64(item.UserID), nullableLegacyInt64(item.UsedAt), nullableLegacyInt64(item.ExpiresAt), actual, item.UsageCount, item.MaxUsage, item.MetadataJSON, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy gift card code id %d: %w", item.ID, err)
		}
	}
	for _, item := range input.Usages {
		rewards, _ := json.Marshal(item.Rewards)
		inviterRewards, _ := json.Marshal(item.InviterRewards)
		if _, err := tx.ExecContext(ctx, `INSERT INTO gift_card_usages (id, code_id, template_id, user_id, inviter_id, rewards_json, inviter_rewards_json, user_level_at_use, user_plan_id, multiplier_basis_points, ip_address, user_agent, notes, used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.CodeID, item.TemplateID, item.UserID, nullableLegacyInt64(item.InviterID), string(rewards), string(inviterRewards), nullableLegacyInt64(item.UserLevelAtUse), nullableLegacyInt64(item.UserPlanID), item.MultiplierBasisPoints, item.IPAddress, item.UserAgent, item.Notes, item.UsedAt); err != nil {
			return fmt.Errorf("import legacy gift card usage id %d: %w", item.ID, err)
		}
	}
	return nil
}

func readLegacyTargetGiftCards(ctx context.Context, database queryer) ([]LegacyGiftCardTemplate, []LegacyGiftCardCode, []LegacyGiftCardUsage, error) {
	templates := []LegacyGiftCardTemplate{}
	rows, err := database.QueryContext(ctx, `SELECT id, name, description, type, status, conditions_json, rewards_json, limits_json, special_config_json, icon, background_image, theme, sort_position, admin_id, created_at, updated_at FROM gift_card_templates ORDER BY id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("verify legacy gift card templates: %w", err)
	}
	for rows.Next() {
		var item LegacyGiftCardTemplate
		var conditions, rewards, limits, special string
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Type, &item.Status, &conditions, &rewards, &limits, &special, &item.Icon, &item.BackgroundImage, &item.Theme, &item.SortPosition, &item.AdminID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		if err := decodeGiftCardJSON(conditions, &item.Conditions); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		if err := decodeGiftCardJSON(rewards, &item.Rewards); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		if err := decodeGiftCardJSON(limits, &item.Limits); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		if err := decodeGiftCardJSON(special, &item.SpecialConfig); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		templates = append(templates, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	codes := []LegacyGiftCardCode{}
	rows, err = database.QueryContext(ctx, `SELECT id, template_id, code, batch_no, status, user_id, used_at, expires_at, actual_rewards_json, usage_count, max_usage, metadata_json, created_at, updated_at FROM gift_card_codes ORDER BY id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("verify legacy gift card codes: %w", err)
	}
	for rows.Next() {
		var item LegacyGiftCardCode
		var userID, usedAt, expiresAt sql.NullInt64
		var actual sql.NullString
		if err := rows.Scan(&item.ID, &item.TemplateID, &item.Code, &item.BatchNo, &item.Status, &userID, &usedAt, &expiresAt, &actual, &item.UsageCount, &item.MaxUsage, &item.MetadataJSON, &item.CreatedAt, &item.UpdatedAt); err != nil {
			_ = rows.Close()
			return nil, nil, nil, err
		}
		item.UserID, item.UsedAt, item.ExpiresAt = nullableInt64Pointer(userID), nullableInt64Pointer(usedAt), nullableInt64Pointer(expiresAt)
		if actual.Valid {
			var reward GiftCardReward
			if err := decodeGiftCardJSON(actual.String, &reward); err != nil {
				_ = rows.Close()
				return nil, nil, nil, err
			}
			item.ActualRewards = &reward
		}
		codes = append(codes, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, err
	}
	usages := []LegacyGiftCardUsage{}
	rows, err = database.QueryContext(ctx, `SELECT id, code_id, template_id, user_id, inviter_id, rewards_json, inviter_rewards_json, user_level_at_use, user_plan_id, multiplier_basis_points, ip_address, user_agent, notes, used_at FROM gift_card_usages ORDER BY id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("verify legacy gift card usages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item LegacyGiftCardUsage
		var inviterID, userLevel, planID sql.NullInt64
		var rewards, inviterRewards string
		if err := rows.Scan(&item.ID, &item.CodeID, &item.TemplateID, &item.UserID, &inviterID, &rewards, &inviterRewards, &userLevel, &planID, &item.MultiplierBasisPoints, &item.IPAddress, &item.UserAgent, &item.Notes, &item.UsedAt); err != nil {
			return nil, nil, nil, err
		}
		item.InviterID, item.UserLevelAtUse, item.UserPlanID = nullableInt64Pointer(inviterID), nullableInt64Pointer(userLevel), nullableInt64Pointer(planID)
		if err := decodeGiftCardJSON(rewards, &item.Rewards); err != nil {
			return nil, nil, nil, err
		}
		if err := decodeGiftCardJSON(inviterRewards, &item.InviterRewards); err != nil {
			return nil, nil, nil, err
		}
		usages = append(usages, item)
	}
	return templates, codes, usages, rows.Err()
}

func legacyDomainMatches(value LegacyDomainResult) bool {
	return value.SourceRows == value.TargetRows && value.SourceChecksum == value.TargetChecksum
}

func validLegacyGiftCardAuditText(value string, maximum int) bool {
	return utf8.ValidString(value) && len(value) <= maximum && strings.IndexByte(value, 0) < 0 && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validLegacyGiftCardAppliedReward(kind GiftCardType, reward GiftCardReward) bool {
	if kind == GiftCardTypePlan {
		return validateGiftCardReward(GiftCardTypePlan, reward, false) == nil
	}
	return validateGiftCardReward(GiftCardTypeGeneral, reward, true) == nil
}

func nullableLegacyInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
