package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	GiftCardTypeGeneral GiftCardType = iota + 1
	GiftCardTypePlan
	GiftCardTypeMystery
)

const (
	GiftCardCodeActive GiftCardCodeStatus = iota
	GiftCardCodeUsed
	GiftCardCodeDisabled
	GiftCardCodeExpired
)

const (
	maxGiftCardMoney       = int64(9_000_000_000_000_000)
	maxGiftCardTransfer    = int64(9_000_000_000_000_000)
	maxGiftCardBatch       = 10_000
	maxGiftCardListSize    = 200
	maxGiftCardUsage       = 1_000
	maxGiftCardPlanIDs     = 100
	maxGiftCardRandomItems = 100
)

const giftCardAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type GiftCardType int
type GiftCardCodeStatus int

type GiftCardReward struct {
	Balance          int64                  `json:"balance,omitempty"`
	TransferEnable   int64                  `json:"transfer_enable,omitempty"`
	ExpireDays       int                    `json:"expire_days,omitempty"`
	DeviceLimit      int                    `json:"device_limit,omitempty"`
	ResetTraffic     bool                   `json:"reset_package,omitempty"`
	PlanID           *int64                 `json:"plan_id,omitempty"`
	PlanValidityDays int                    `json:"plan_validity_days,omitempty"`
	RandomRewards    []GiftCardRandomReward `json:"random_rewards,omitempty"`
}

type GiftCardRandomReward struct {
	Weight int            `json:"weight"`
	Reward GiftCardReward `json:"rewards"`
}

type GiftCardConditions struct {
	NewUserMaxDays    *int    `json:"new_user_max_days,omitempty"`
	NewUserOnly       bool    `json:"new_user_only,omitempty"`
	PaidUserOnly      bool    `json:"paid_user_only,omitempty"`
	RequireInvite     bool    `json:"require_invite,omitempty"`
	AllowedPlanIDs    []int64 `json:"allowed_plans,omitempty"`
	DisallowedPlanIDs []int64 `json:"disallowed_plans,omitempty"`
}

type GiftCardLimits struct {
	MaxUsePerUser           int `json:"max_use_per_user,omitempty"`
	CooldownHours           int `json:"cooldown_hours,omitempty"`
	InviteRewardBasisPoints int `json:"invite_reward_basis_points,omitempty"`
}

type GiftCardSpecialConfig struct {
	StartedAt                     *time.Time `json:"started_at,omitempty"`
	EndedAt                       *time.Time `json:"ended_at,omitempty"`
	FestivalMultiplierBasisPoints int        `json:"festival_multiplier_basis_points,omitempty"`
}

type GiftCardTemplate struct {
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
	Revision        int64                 `json:"revision"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type SaveGiftCardTemplateInput struct {
	Name            string
	Description     string
	Type            GiftCardType
	Status          bool
	Conditions      GiftCardConditions
	Rewards         GiftCardReward
	Limits          GiftCardLimits
	SpecialConfig   GiftCardSpecialConfig
	Icon            string
	BackgroundImage string
	Theme           string
	SortPosition    int
}

type GiftCardCode struct {
	ID            int64              `json:"id"`
	TemplateID    int64              `json:"template_id"`
	TemplateName  string             `json:"template_name,omitempty"`
	Code          string             `json:"code"`
	BatchNo       string             `json:"batch_no"`
	Status        GiftCardCodeStatus `json:"status"`
	UserID        *int64             `json:"user_id"`
	UsedAt        *time.Time         `json:"used_at"`
	ExpiresAt     *time.Time         `json:"expires_at"`
	ActualRewards *GiftCardReward    `json:"actual_rewards,omitempty"`
	UsageCount    int                `json:"usage_count"`
	MaxUsage      int                `json:"max_usage"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}

type GenerateGiftCardCodesInput struct {
	Count     int
	Prefix    string
	ExpiresAt *time.Time
	MaxUsage  int
}

type GiftCardPreview struct {
	Template GiftCardTemplate `json:"template"`
	Code     GiftCardCode     `json:"code"`
	Rewards  GiftCardReward   `json:"rewards"`
}

type RedeemGiftCardInput struct {
	UserID    int64
	Code      string
	IPAddress string
	UserAgent string
}

type GiftCardUsage struct {
	ID                         int64          `json:"id"`
	CodeID                     int64          `json:"code_id"`
	Code                       string         `json:"code,omitempty"`
	TemplateID                 int64          `json:"template_id"`
	TemplateName               string         `json:"template_name,omitempty"`
	TemplateType               GiftCardType   `json:"template_type,omitempty"`
	UserID                     int64          `json:"user_id"`
	UserEmail                  string         `json:"user_email,omitempty"`
	InviterID                  *int64         `json:"inviter_id"`
	InviterEmail               string         `json:"inviter_email,omitempty"`
	Rewards                    GiftCardReward `json:"rewards"`
	InviterRewards             GiftCardReward `json:"inviter_rewards"`
	UserLevelAtUse             *int64         `json:"user_level_at_use,omitempty"`
	UserPlanID                 *int64         `json:"user_plan_id"`
	Multiplier                 int            `json:"multiplier_basis_points"`
	IPAddress                  string         `json:"ip_address,omitempty"`
	UserAgent                  string         `json:"user_agent,omitempty"`
	Notes                      string         `json:"notes,omitempty"`
	TrafficResetUploadBefore   *int64         `json:"traffic_reset_upload_before,omitempty"`
	TrafficResetDownloadBefore *int64         `json:"traffic_reset_download_before,omitempty"`
	UsedAt                     time.Time      `json:"used_at"`
}

type GiftCardTemplateFilter struct {
	Page     int
	PageSize int
	Type     *GiftCardType
	Status   *bool
}

type GiftCardTemplatePage struct {
	Items    []GiftCardTemplate `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

type GiftCardCodeFilter struct {
	Page       int
	PageSize   int
	Query      string
	TemplateID *int64
	Status     *GiftCardCodeStatus
	BatchNo    string
}

type GiftCardCodePage struct {
	Items    []GiftCardCode `json:"items"`
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
}

type SaveGiftCardCodeInput struct {
	Code      string
	Status    GiftCardCodeStatus
	ExpiresAt *time.Time
	MaxUsage  int
}

type GiftCardUsageFilter struct {
	Page       int
	PageSize   int
	UserID     *int64
	TemplateID *int64
	CodeID     *int64
}

type GiftCardUsagePage struct {
	Items    []GiftCardUsage `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

type GiftCardTypeStat struct {
	Type  GiftCardType `json:"type"`
	Count int64        `json:"count"`
}

type GiftCardDailyUsage struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type GiftCardTemplateUsageStat struct {
	TemplateName string       `json:"template_name"`
	Type         GiftCardType `json:"type"`
	Count        int64        `json:"count"`
}

type GiftCardStatistics struct {
	TemplateTotal   int64                       `json:"template_total"`
	ActiveTemplates int64                       `json:"active_templates"`
	CodeTotal       int64                       `json:"code_total"`
	UsedCodes       int64                       `json:"used_codes"`
	UsageTotal      int64                       `json:"usage_total"`
	DailyUsages     []GiftCardDailyUsage        `json:"daily_usages"`
	TypeStats       []GiftCardTypeStat          `json:"type_stats"`
	TemplateStats   []GiftCardTemplateUsageStat `json:"-"`
}

type giftCardUserState struct {
	orderUserState
	createdAt int64
}

func (s *Store) CreateGiftCardTemplate(ctx context.Context, input SaveGiftCardTemplateInput, adminID int64, now time.Time) (GiftCardTemplate, error) {
	normalized, encoded, err := normalizeGiftCardTemplate(input, adminID, now)
	if err != nil {
		return GiftCardTemplate{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("begin create gift card template: %w", err)
	}
	defer tx.Rollback()
	if err := validateGiftCardReferences(ctx, tx, normalized); err != nil {
		return GiftCardTemplate{}, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO gift_card_templates (
			name, description, type, status, conditions_json, rewards_json, limits_json,
			special_config_json, icon, background_image, theme, sort_position, admin_id,
			revision, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, normalized.Name, normalized.Description, normalized.Type, normalized.Status, encoded.conditions,
		encoded.rewards, encoded.limits, encoded.special, normalized.Icon, normalized.BackgroundImage,
		normalized.Theme, normalized.SortPosition, adminID, now.Unix(), now.Unix())
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("create gift card template: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("read gift card template ID: %w", err)
	}
	template, err := getGiftCardTemplate(ctx, tx, id)
	if err != nil {
		return GiftCardTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return GiftCardTemplate{}, fmt.Errorf("commit gift card template: %w", err)
	}
	return template, nil
}

func (s *Store) UpdateGiftCardTemplate(ctx context.Context, templateID, revision int64, input SaveGiftCardTemplateInput, adminID int64, now time.Time) (GiftCardTemplate, error) {
	if templateID < 1 || revision < 1 {
		return GiftCardTemplate{}, ErrInvalidInput
	}
	normalized, encoded, err := normalizeGiftCardTemplate(input, adminID, now)
	if err != nil {
		return GiftCardTemplate{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("begin update gift card template: %w", err)
	}
	defer tx.Rollback()
	if err := validateGiftCardReferences(ctx, tx, normalized); err != nil {
		return GiftCardTemplate{}, err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE gift_card_templates SET name = ?, description = ?, type = ?, status = ?,
			conditions_json = ?, rewards_json = ?, limits_json = ?, special_config_json = ?,
			icon = ?, background_image = ?, theme = ?, sort_position = ?, admin_id = ?,
			revision = revision + 1, updated_at = ?
		WHERE id = ? AND revision = ?
	`, normalized.Name, normalized.Description, normalized.Type, normalized.Status, encoded.conditions,
		encoded.rewards, encoded.limits, encoded.special, normalized.Icon, normalized.BackgroundImage,
		normalized.Theme, normalized.SortPosition, adminID, now.Unix(), templateID, revision)
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("update gift card template: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("count gift card template update: %w", err)
	}
	if updated != 1 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gift_card_templates WHERE id = ?)`, templateID).Scan(&exists); err != nil {
			return GiftCardTemplate{}, fmt.Errorf("check gift card template: %w", err)
		}
		if exists {
			return GiftCardTemplate{}, ErrRevisionConflict
		}
		return GiftCardTemplate{}, ErrNotFound
	}
	template, err := getGiftCardTemplate(ctx, tx, templateID)
	if err != nil {
		return GiftCardTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return GiftCardTemplate{}, fmt.Errorf("commit gift card template update: %w", err)
	}
	return template, nil
}

func (s *Store) GetGiftCardTemplate(ctx context.Context, templateID int64) (GiftCardTemplate, error) {
	if templateID < 1 {
		return GiftCardTemplate{}, ErrInvalidInput
	}
	return getGiftCardTemplate(ctx, s.db, templateID)
}

func (s *Store) DeleteGiftCardTemplate(ctx context.Context, templateID int64) error {
	if templateID < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM gift_card_templates
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM gift_card_codes WHERE template_id = ?)
	`, templateID, templateID)
	if err != nil {
		return fmt.Errorf("delete gift card template: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted gift card template: %w", err)
	}
	if deleted == 1 {
		return nil
	}
	var exists, referenced bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gift_card_templates WHERE id = ?), EXISTS(SELECT 1 FROM gift_card_codes WHERE template_id = ?)`, templateID, templateID).Scan(&exists, &referenced); err != nil {
		return fmt.Errorf("inspect gift card template delete: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	if referenced {
		return ErrGiftCardReferenced
	}
	return ErrConflict
}

func (s *Store) ListGiftCardTemplates(ctx context.Context, filter GiftCardTemplateFilter) (GiftCardTemplatePage, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxGiftCardListSize ||
		filter.Type != nil && (*filter.Type < GiftCardTypeGeneral || *filter.Type > GiftCardTypeMystery) {
		return GiftCardTemplatePage{}, ErrInvalidInput
	}
	where := make([]string, 0, 2)
	arguments := make([]any, 0, 4)
	if filter.Type != nil {
		where, arguments = append(where, "type = ?"), append(arguments, *filter.Type)
	}
	if filter.Status != nil {
		where, arguments = append(where, "status = ?"), append(arguments, *filter.Status)
	}
	whereSQL := giftCardWhere(where)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_card_templates`+whereSQL, arguments...).Scan(&total); err != nil {
		return GiftCardTemplatePage{}, fmt.Errorf("count gift card templates: %w", err)
	}
	listArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, giftCardTemplateSelect+whereSQL+` ORDER BY sort_position ASC, id DESC LIMIT ? OFFSET ?`, listArguments...)
	if err != nil {
		return GiftCardTemplatePage{}, fmt.Errorf("list gift card templates: %w", err)
	}
	defer rows.Close()
	items := make([]GiftCardTemplate, 0, filter.PageSize)
	for rows.Next() {
		item, scanErr := scanGiftCardTemplate(rows)
		if scanErr != nil {
			return GiftCardTemplatePage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GiftCardTemplatePage{}, fmt.Errorf("iterate gift card templates: %w", err)
	}
	return GiftCardTemplatePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) GenerateGiftCardCodes(ctx context.Context, templateID int64, input GenerateGiftCardCodesInput, now time.Time) ([]GiftCardCode, error) {
	prefix := strings.ToUpper(strings.TrimSpace(input.Prefix))
	if prefix == "" {
		prefix = "GC"
	}
	if templateID < 1 || input.Count < 1 || input.Count > maxGiftCardBatch || input.MaxUsage < 1 ||
		input.MaxUsage > maxGiftCardUsage || len(prefix) > 10 || !validGiftCardToken(prefix) || now.Unix() < 0 ||
		input.ExpiresAt != nil && input.ExpiresAt.Before(now) {
		return nil, ErrInvalidInput
	}
	batchRandom, err := randomGiftCardString(rand.Reader, 12)
	if err != nil {
		return nil, err
	}
	batchNo := fmt.Sprintf("%d-%s", now.Unix(), batchRandom)
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin generate gift card codes: %w", err)
	}
	defer tx.Rollback()
	template, err := getGiftCardTemplate(ctx, tx, templateID)
	if err != nil {
		return nil, err
	}
	if !template.Status {
		return nil, ErrGiftCardUnavailable
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO gift_card_codes (
			template_id, code, batch_no, status, expires_at, usage_count, max_usage, metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, 0, ?, 0, ?, '{}', ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare gift card code batch: %w", err)
	}
	defer statement.Close()
	codes := make([]GiftCardCode, 0, input.Count)
	for index := 0; index < input.Count; index++ {
		var inserted bool
		for attempt := 0; attempt < 8; attempt++ {
			random, randomErr := randomGiftCardString(rand.Reader, 20)
			if randomErr != nil {
				return nil, randomErr
			}
			code := prefix + random
			result, insertErr := statement.ExecContext(ctx, templateID, code, batchNo, nullableUnix(input.ExpiresAt), input.MaxUsage, now.Unix(), now.Unix())
			if insertErr != nil {
				var collision bool
				if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gift_card_codes WHERE code = ?)`, code).Scan(&collision); queryErr != nil {
					return nil, fmt.Errorf("inspect gift card code collision: %w", queryErr)
				}
				if collision {
					continue
				}
				return nil, fmt.Errorf("generate gift card code: %w", insertErr)
			}
			id, idErr := result.LastInsertId()
			if idErr != nil {
				return nil, fmt.Errorf("read gift card code ID: %w", idErr)
			}
			codes = append(codes, GiftCardCode{ID: id, TemplateID: templateID, TemplateName: template.Name, Code: code, BatchNo: batchNo, Status: GiftCardCodeActive, ExpiresAt: cloneTime(input.ExpiresAt), MaxUsage: input.MaxUsage, CreatedAt: now, UpdatedAt: now})
			inserted = true
			break
		}
		if !inserted {
			return nil, fmt.Errorf("%w: gift card code collision", ErrConflict)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit gift card code batch: %w", err)
	}
	return codes, nil
}

func (s *Store) ListGiftCardCodes(ctx context.Context, filter GiftCardCodeFilter) (GiftCardCodePage, error) {
	filter.Query = strings.TrimSpace(filter.Query)
	filter.BatchNo = strings.TrimSpace(filter.BatchNo)
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxGiftCardListSize || len(filter.Query) > 255 ||
		len(filter.BatchNo) > 40 || filter.TemplateID != nil && *filter.TemplateID < 1 ||
		filter.Status != nil && (*filter.Status < GiftCardCodeActive || *filter.Status > GiftCardCodeExpired) {
		return GiftCardCodePage{}, ErrInvalidInput
	}
	where := make([]string, 0, 4)
	arguments := make([]any, 0, 8)
	if filter.Query != "" {
		where, arguments = append(where, `(c.code LIKE ? ESCAPE '\' OR t.name LIKE ? ESCAPE '\')`), append(arguments, "%"+escapeLike(filter.Query)+"%", "%"+escapeLike(filter.Query)+"%")
	}
	if filter.TemplateID != nil {
		where, arguments = append(where, "c.template_id = ?"), append(arguments, *filter.TemplateID)
	}
	if filter.Status != nil {
		where, arguments = append(where, "c.status = ?"), append(arguments, *filter.Status)
	}
	if filter.BatchNo != "" {
		where, arguments = append(where, "c.batch_no = ?"), append(arguments, filter.BatchNo)
	}
	whereSQL := giftCardWhere(where)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_card_codes c JOIN gift_card_templates t ON t.id = c.template_id`+whereSQL, arguments...).Scan(&total); err != nil {
		return GiftCardCodePage{}, fmt.Errorf("count gift card codes: %w", err)
	}
	listArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, giftCardCodeSelect+whereSQL+` ORDER BY c.created_at DESC, c.id DESC LIMIT ? OFFSET ?`, listArguments...)
	if err != nil {
		return GiftCardCodePage{}, fmt.Errorf("list gift card codes: %w", err)
	}
	defer rows.Close()
	items := make([]GiftCardCode, 0, filter.PageSize)
	for rows.Next() {
		item, scanErr := scanGiftCardCode(rows)
		if scanErr != nil {
			return GiftCardCodePage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GiftCardCodePage{}, fmt.Errorf("iterate gift card codes: %w", err)
	}
	return GiftCardCodePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) GetGiftCardCode(ctx context.Context, codeID int64) (GiftCardCode, error) {
	if codeID < 1 {
		return GiftCardCode{}, ErrInvalidInput
	}
	return getGiftCardCode(ctx, s.db, codeID)
}

func (s *Store) UpdateGiftCardCode(ctx context.Context, codeID int64, input SaveGiftCardCodeInput, now time.Time) (GiftCardCode, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	if codeID < 1 || !validGiftCardCode(input.Code) || input.Status < GiftCardCodeActive || input.Status > GiftCardCodeExpired ||
		input.MaxUsage < 1 || input.MaxUsage > maxGiftCardUsage || input.ExpiresAt != nil && input.ExpiresAt.Unix() < 0 || now.Unix() < 0 {
		return GiftCardCode{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiftCardCode{}, fmt.Errorf("begin update gift card code: %w", err)
	}
	defer tx.Rollback()
	current, err := getGiftCardCode(ctx, tx, codeID)
	if err != nil {
		return GiftCardCode{}, err
	}
	if input.MaxUsage < current.UsageCount || input.Status == GiftCardCodeActive && (current.UsageCount >= input.MaxUsage || input.ExpiresAt != nil && input.ExpiresAt.Before(now)) {
		return GiftCardCode{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `UPDATE gift_card_codes SET code = ?, status = ?, expires_at = ?, max_usage = ?, updated_at = ? WHERE id = ?`, input.Code, input.Status, nullableUnix(input.ExpiresAt), input.MaxUsage, now.Unix(), codeID)
	if err != nil {
		var collision bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gift_card_codes WHERE code = ? AND id <> ?)`, input.Code, codeID).Scan(&collision); queryErr == nil && collision {
			return GiftCardCode{}, ErrConflict
		}
		return GiftCardCode{}, fmt.Errorf("update gift card code: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return GiftCardCode{}, fmt.Errorf("count updated gift card code: %w", err)
	}
	if updated != 1 {
		return GiftCardCode{}, ErrNotFound
	}
	item, err := getGiftCardCode(ctx, tx, codeID)
	if err != nil {
		return GiftCardCode{}, err
	}
	if err := tx.Commit(); err != nil {
		return GiftCardCode{}, fmt.Errorf("commit gift card code update: %w", err)
	}
	return item, nil
}

func (s *Store) ToggleGiftCardCode(ctx context.Context, codeID int64, now time.Time) (GiftCardCode, error) {
	if codeID < 1 || now.Unix() < 0 {
		return GiftCardCode{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiftCardCode{}, fmt.Errorf("begin toggle gift card code: %w", err)
	}
	defer tx.Rollback()
	current, err := getGiftCardCode(ctx, tx, codeID)
	if err != nil {
		return GiftCardCode{}, err
	}
	next := GiftCardCodeDisabled
	if current.Status == GiftCardCodeDisabled {
		if current.UsageCount >= current.MaxUsage {
			return GiftCardCode{}, ErrGiftCardExhausted
		}
		if current.ExpiresAt != nil && current.ExpiresAt.Before(now) {
			return GiftCardCode{}, ErrGiftCardExpired
		}
		next = GiftCardCodeActive
	} else if current.Status != GiftCardCodeActive {
		return GiftCardCode{}, ErrGiftCardUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gift_card_codes SET status = ?, updated_at = ? WHERE id = ?`, next, now.Unix(), codeID); err != nil {
		return GiftCardCode{}, fmt.Errorf("toggle gift card code: %w", err)
	}
	current.Status, current.UpdatedAt = next, now
	if err := tx.Commit(); err != nil {
		return GiftCardCode{}, fmt.Errorf("commit gift card code toggle: %w", err)
	}
	return current, nil
}

func (s *Store) DeleteGiftCardCode(ctx context.Context, codeID int64) error {
	if codeID < 1 {
		return ErrInvalidInput
	}
	defer s.lockWrite()()
	result, err := s.db.ExecContext(ctx, `DELETE FROM gift_card_codes WHERE id = ? AND NOT EXISTS (SELECT 1 FROM gift_card_usages WHERE code_id = ?)`, codeID, codeID)
	if err != nil {
		return fmt.Errorf("delete gift card code: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted gift card code: %w", err)
	}
	if deleted == 1 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM gift_card_codes WHERE id = ?)`, codeID).Scan(&exists); err != nil {
		return fmt.Errorf("inspect gift card code delete: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrGiftCardReferenced
}

func (s *Store) CheckGiftCard(ctx context.Context, userID int64, code string, now time.Time) (GiftCardPreview, error) {
	if userID < 1 || !validGiftCardCode(code) || now.Unix() < 0 {
		return GiftCardPreview{}, ErrInvalidInput
	}
	return checkGiftCard(ctx, s.db, userID, strings.ToUpper(strings.TrimSpace(code)), now, rand.Reader)
}

func (s *Store) RedeemGiftCard(ctx context.Context, input RedeemGiftCardInput, now time.Time) (GiftCardUsage, error) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.IPAddress = strings.TrimSpace(input.IPAddress)
	input.UserAgent = strings.TrimSpace(input.UserAgent)
	if input.UserID < 1 || !validGiftCardCode(input.Code) || now.Unix() < 0 || len(input.IPAddress) > 45 ||
		len(input.UserAgent) > 1024 || !utf8.ValidString(input.UserAgent) || strings.IndexByte(input.UserAgent, 0) >= 0 {
		return GiftCardUsage{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("begin redeem gift card: %w", err)
	}
	defer tx.Rollback()
	preview, err := checkGiftCard(ctx, tx, input.UserID, input.Code, now, rand.Reader)
	if err != nil {
		return GiftCardUsage{}, err
	}
	user, err := readGiftCardUser(ctx, tx, input.UserID)
	if err != nil {
		return GiftCardUsage{}, err
	}
	nextStatus := GiftCardCodeActive
	if preview.Code.UsageCount+1 >= preview.Code.MaxUsage {
		nextStatus = GiftCardCodeUsed
	}
	rewardJSON, err := json.Marshal(preview.Rewards)
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("encode actual gift card reward: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE gift_card_codes SET usage_count = usage_count + 1, status = ?, user_id = ?, used_at = ?,
			actual_rewards_json = ?, updated_at = ?
		WHERE id = ? AND status = 0 AND usage_count < max_usage
		  AND (expires_at IS NULL OR expires_at >= ?)
	`, nextStatus, input.UserID, now.Unix(), string(rewardJSON), now.Unix(), preview.Code.ID, now.Unix())
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("claim gift card code: %w", err)
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("count claimed gift card code: %w", err)
	}
	if claimed != 1 {
		return GiftCardUsage{}, ErrGiftCardExhausted
	}
	var trafficResetUploadBefore, trafficResetDownloadBefore *int64
	if preview.Rewards.ResetTraffic && user.planID.Valid {
		upload, download := user.trafficUpload, user.trafficDownload
		trafficResetUploadBefore, trafficResetDownloadBefore = &upload, &download
	}
	if err := applyGiftCardReward(ctx, tx, &user, preview.Template.Type, preview.Rewards, now); err != nil {
		return GiftCardUsage{}, err
	}
	inviterReward := GiftCardReward{}
	var inviterID *int64
	if user.inviteUserID.Valid && preview.Template.Limits.InviteRewardBasisPoints > 0 {
		value := user.inviteUserID.Int64
		inviterID = &value
		inviterReward, err = applyGiftCardInviterReward(ctx, tx, value, preview.Rewards, preview.Template.Limits.InviteRewardBasisPoints, now)
		if err != nil {
			return GiftCardUsage{}, err
		}
	}
	inviterJSON, err := json.Marshal(inviterReward)
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("encode inviter gift card reward: %w", err)
	}
	multiplier := giftCardMultiplier(preview.Template.SpecialConfig, now)
	var userLevelAtUse sql.NullInt64
	if user.planID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT sort_position FROM plans WHERE id = ?`, user.planID.Int64).Scan(&userLevelAtUse); err != nil {
			return GiftCardUsage{}, fmt.Errorf("read gift card user level: %w", err)
		}
	}
	usageResult, err := tx.ExecContext(ctx, `
		INSERT INTO gift_card_usages (
			code_id, template_id, user_id, inviter_id, rewards_json, inviter_rewards_json,
			user_level_at_use, user_plan_id, multiplier_basis_points, ip_address, user_agent, notes,
			traffic_reset_upload_before, traffic_reset_download_before, used_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)
	`, preview.Code.ID, preview.Template.ID, input.UserID, inviterID, string(rewardJSON), string(inviterJSON),
		nullableSQLInt(userLevelAtUse), nullableSQLInt(user.planID), multiplier, input.IPAddress, input.UserAgent,
		trafficResetUploadBefore, trafficResetDownloadBefore, now.Unix())
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("record gift card usage: %w", err)
	}
	usageID, err := usageResult.LastInsertId()
	if err != nil {
		return GiftCardUsage{}, fmt.Errorf("read gift card usage ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GiftCardUsage{}, fmt.Errorf("commit gift card redemption: %w", err)
	}
	return GiftCardUsage{ID: usageID, CodeID: preview.Code.ID, Code: preview.Code.Code, TemplateID: preview.Template.ID,
		TemplateName: preview.Template.Name, UserID: input.UserID, InviterID: inviterID, Rewards: preview.Rewards,
		InviterRewards: inviterReward, UserLevelAtUse: nullableInt64Pointer(userLevelAtUse), UserPlanID: nullableInt64Pointer(user.planID), Multiplier: multiplier,
		IPAddress: input.IPAddress, UserAgent: input.UserAgent, TrafficResetUploadBefore: trafficResetUploadBefore,
		TrafficResetDownloadBefore: trafficResetDownloadBefore, UsedAt: now}, nil
}

func (s *Store) ListGiftCardUsages(ctx context.Context, filter GiftCardUsageFilter) (GiftCardUsagePage, error) {
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > maxGiftCardListSize ||
		filter.UserID != nil && *filter.UserID < 1 || filter.TemplateID != nil && *filter.TemplateID < 1 || filter.CodeID != nil && *filter.CodeID < 1 {
		return GiftCardUsagePage{}, ErrInvalidInput
	}
	where := make([]string, 0, 3)
	arguments := make([]any, 0, 5)
	if filter.UserID != nil {
		where, arguments = append(where, "g.user_id = ?"), append(arguments, *filter.UserID)
	}
	if filter.TemplateID != nil {
		where, arguments = append(where, "g.template_id = ?"), append(arguments, *filter.TemplateID)
	}
	if filter.CodeID != nil {
		where, arguments = append(where, "g.code_id = ?"), append(arguments, *filter.CodeID)
	}
	whereSQL := giftCardWhere(where)
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_card_usages g`+whereSQL, arguments...).Scan(&total); err != nil {
		return GiftCardUsagePage{}, fmt.Errorf("count gift card usages: %w", err)
	}
	listArguments := append(append([]any(nil), arguments...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.db.QueryContext(ctx, giftCardUsageSelect+whereSQL+` ORDER BY g.used_at DESC, g.id DESC LIMIT ? OFFSET ?`, listArguments...)
	if err != nil {
		return GiftCardUsagePage{}, fmt.Errorf("list gift card usages: %w", err)
	}
	defer rows.Close()
	items := make([]GiftCardUsage, 0, filter.PageSize)
	for rows.Next() {
		item, scanErr := scanGiftCardUsage(rows)
		if scanErr != nil {
			return GiftCardUsagePage{}, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GiftCardUsagePage{}, fmt.Errorf("iterate gift card usages: %w", err)
	}
	return GiftCardUsagePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Store) GetGiftCardUsage(ctx context.Context, usageID, userID int64) (GiftCardUsage, error) {
	if usageID < 1 || userID < 1 {
		return GiftCardUsage{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, giftCardUsageSelect+` WHERE g.id = ? AND g.user_id = ?`, usageID, userID)
	usage, err := scanGiftCardUsage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GiftCardUsage{}, ErrNotFound
	}
	return usage, err
}

func (s *Store) GiftCardStatistics(ctx context.Context, now time.Time) (GiftCardStatistics, error) {
	if now.Unix() < 0 {
		return GiftCardStatistics{}, ErrInvalidInput
	}
	var result GiftCardStatistics
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM gift_card_templates),
			(SELECT COUNT(*) FROM gift_card_templates WHERE status = 1),
			(SELECT COUNT(*) FROM gift_card_codes),
			(SELECT COUNT(*) FROM gift_card_codes WHERE status = 1),
			(SELECT COUNT(*) FROM gift_card_usages)
	`).Scan(&result.TemplateTotal, &result.ActiveTemplates, &result.CodeTotal, &result.UsedCodes, &result.UsageTotal); err != nil {
		return GiftCardStatistics{}, fmt.Errorf("read gift card totals: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.type, COUNT(g.id) FROM gift_card_templates t LEFT JOIN gift_card_usages g ON g.template_id = t.id GROUP BY t.type ORDER BY t.type`)
	if err != nil {
		return GiftCardStatistics{}, fmt.Errorf("read gift card type statistics: %w", err)
	}
	for rows.Next() {
		var item GiftCardTypeStat
		if err := rows.Scan(&item.Type, &item.Count); err != nil {
			_ = rows.Close()
			return GiftCardStatistics{}, fmt.Errorf("scan gift card type statistics: %w", err)
		}
		result.TypeStats = append(result.TypeStats, item)
	}
	if err := rows.Close(); err != nil {
		return GiftCardStatistics{}, fmt.Errorf("close gift card type statistics: %w", err)
	}
	templateRows, err := s.db.QueryContext(ctx, `SELECT t.name, t.type, COUNT(g.id) FROM gift_card_usages g JOIN gift_card_templates t ON t.id = g.template_id GROUP BY t.id, t.name, t.type ORDER BY t.sort_position, t.id`)
	if err != nil {
		return GiftCardStatistics{}, fmt.Errorf("read gift card template statistics: %w", err)
	}
	for templateRows.Next() {
		var item GiftCardTemplateUsageStat
		if err := templateRows.Scan(&item.TemplateName, &item.Type, &item.Count); err != nil {
			_ = templateRows.Close()
			return GiftCardStatistics{}, fmt.Errorf("scan gift card template statistics: %w", err)
		}
		result.TemplateStats = append(result.TemplateStats, item)
	}
	if err := templateRows.Close(); err != nil {
		return GiftCardStatistics{}, fmt.Errorf("close gift card template statistics: %w", err)
	}
	daily, err := s.db.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%d', used_at, 'unixepoch'), COUNT(*) FROM gift_card_usages
		WHERE used_at >= ? GROUP BY strftime('%Y-%m-%d', used_at, 'unixepoch') ORDER BY 1
	`, now.AddDate(0, 0, -29).Unix())
	if err != nil {
		return GiftCardStatistics{}, fmt.Errorf("read gift card daily statistics: %w", err)
	}
	defer daily.Close()
	for daily.Next() {
		var item GiftCardDailyUsage
		if err := daily.Scan(&item.Date, &item.Count); err != nil {
			return GiftCardStatistics{}, fmt.Errorf("scan gift card daily statistics: %w", err)
		}
		result.DailyUsages = append(result.DailyUsages, item)
	}
	if result.TypeStats == nil {
		result.TypeStats = []GiftCardTypeStat{}
	}
	if result.DailyUsages == nil {
		result.DailyUsages = []GiftCardDailyUsage{}
	}
	if result.TemplateStats == nil {
		result.TemplateStats = []GiftCardTemplateUsageStat{}
	}
	return result, daily.Err()
}

const giftCardTemplateSelect = `SELECT id, name, description, type, status, conditions_json, rewards_json,
	limits_json, special_config_json, icon, background_image, theme, sort_position, admin_id, revision,
	created_at, updated_at FROM gift_card_templates`

const giftCardCodeSelect = `SELECT c.id, c.template_id, t.name, c.code, c.batch_no, c.status, c.user_id,
	c.used_at, c.expires_at, c.actual_rewards_json, c.usage_count, c.max_usage, c.created_at, c.updated_at
	FROM gift_card_codes c JOIN gift_card_templates t ON t.id = c.template_id`

const giftCardUsageSelect = `SELECT g.id, g.code_id, c.code, g.template_id, t.name, t.type, g.user_id, u.email,
	g.inviter_id, COALESCE(i.email, ''), g.rewards_json, g.inviter_rewards_json, g.user_level_at_use, g.user_plan_id, g.multiplier_basis_points,
	g.ip_address, g.user_agent, g.notes, g.traffic_reset_upload_before, g.traffic_reset_download_before, g.used_at
	FROM gift_card_usages g JOIN gift_card_codes c ON c.id = g.code_id
	JOIN gift_card_templates t ON t.id = g.template_id JOIN users u ON u.id = g.user_id
	LEFT JOIN users i ON i.id = g.inviter_id`

func scanGiftCardTemplate(row rowScanner) (GiftCardTemplate, error) {
	var template GiftCardTemplate
	var conditionsJSON, rewardsJSON, limitsJSON, specialJSON string
	var createdAt, updatedAt int64
	if err := row.Scan(&template.ID, &template.Name, &template.Description, &template.Type, &template.Status,
		&conditionsJSON, &rewardsJSON, &limitsJSON, &specialJSON, &template.Icon, &template.BackgroundImage,
		&template.Theme, &template.SortPosition, &template.AdminID, &template.Revision, &createdAt, &updatedAt); err != nil {
		return GiftCardTemplate{}, err
	}
	for _, item := range []struct {
		source string
		target any
	}{{conditionsJSON, &template.Conditions}, {rewardsJSON, &template.Rewards}, {limitsJSON, &template.Limits}, {specialJSON, &template.SpecialConfig}} {
		if err := decodeGiftCardJSON(item.source, item.target); err != nil {
			return GiftCardTemplate{}, err
		}
	}
	template.CreatedAt, template.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return template, nil
}

func getGiftCardCode(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, codeID int64) (GiftCardCode, error) {
	item, err := scanGiftCardCode(database.QueryRowContext(ctx, giftCardCodeSelect+` WHERE c.id = ?`, codeID))
	if errors.Is(err, sql.ErrNoRows) {
		return GiftCardCode{}, ErrNotFound
	}
	return item, err
}

func scanGiftCardCode(row rowScanner) (GiftCardCode, error) {
	var item GiftCardCode
	var userID, usedAt, expiresAt sql.NullInt64
	var actual sql.NullString
	var createdAt, updatedAt int64
	if err := row.Scan(&item.ID, &item.TemplateID, &item.TemplateName, &item.Code, &item.BatchNo, &item.Status,
		&userID, &usedAt, &expiresAt, &actual, &item.UsageCount, &item.MaxUsage, &createdAt, &updatedAt); err != nil {
		return GiftCardCode{}, err
	}
	item.UserID, item.UsedAt, item.ExpiresAt = nullableInt64Pointer(userID), nullableUnixTime(usedAt), nullableUnixTime(expiresAt)
	item.CreatedAt, item.UpdatedAt = time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	if actual.Valid {
		var reward GiftCardReward
		if err := decodeGiftCardJSON(actual.String, &reward); err != nil {
			return GiftCardCode{}, err
		}
		item.ActualRewards = &reward
	}
	return item, nil
}

func scanGiftCardUsage(row rowScanner) (GiftCardUsage, error) {
	var item GiftCardUsage
	var inviterID, userLevelAtUse, userPlanID, trafficResetUploadBefore, trafficResetDownloadBefore sql.NullInt64
	var rewardsJSON, inviterJSON string
	var usedAt int64
	if err := row.Scan(&item.ID, &item.CodeID, &item.Code, &item.TemplateID, &item.TemplateName, &item.TemplateType, &item.UserID,
		&item.UserEmail, &inviterID, &item.InviterEmail, &rewardsJSON, &inviterJSON, &userLevelAtUse, &userPlanID, &item.Multiplier,
		&item.IPAddress, &item.UserAgent, &item.Notes, &trafficResetUploadBefore, &trafficResetDownloadBefore, &usedAt); err != nil {
		return GiftCardUsage{}, err
	}
	item.InviterID, item.UserLevelAtUse, item.UserPlanID = nullableInt64Pointer(inviterID), nullableInt64Pointer(userLevelAtUse), nullableInt64Pointer(userPlanID)
	item.TrafficResetUploadBefore = nullableInt64Pointer(trafficResetUploadBefore)
	item.TrafficResetDownloadBefore = nullableInt64Pointer(trafficResetDownloadBefore)
	if err := decodeGiftCardJSON(rewardsJSON, &item.Rewards); err != nil {
		return GiftCardUsage{}, err
	}
	if err := decodeGiftCardJSON(inviterJSON, &item.InviterRewards); err != nil {
		return GiftCardUsage{}, err
	}
	item.UsedAt = time.Unix(usedAt, 0).UTC()
	return item, nil
}

func giftCardWhere(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(values, " AND ")
}

type giftCardEncoded struct{ conditions, rewards, limits, special string }

func normalizeGiftCardTemplate(input SaveGiftCardTemplateInput, adminID int64, now time.Time) (SaveGiftCardTemplateInput, giftCardEncoded, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Icon = strings.TrimSpace(input.Icon)
	input.BackgroundImage = strings.TrimSpace(input.BackgroundImage)
	input.Theme = strings.TrimSpace(input.Theme)
	if input.Theme == "" {
		input.Theme = "#1890ff"
	}
	if adminID < 1 || now.Unix() < 0 || input.Type < GiftCardTypeGeneral || input.Type > GiftCardTypeMystery ||
		!validateGiftCardText(input.Name, 255, false) || !validateGiftCardText(input.Description, 4096, true) ||
		!validateGiftCardText(input.Icon, 255, true) || !validateGiftCardText(input.BackgroundImage, 255, true) ||
		(input.BackgroundImage != "" && !validHTTPURL(input.BackgroundImage)) || !validGiftCardTheme(input.Theme) ||
		input.SortPosition < 0 || input.SortPosition > 1_000_000_000 {
		return SaveGiftCardTemplateInput{}, giftCardEncoded{}, ErrInvalidInput
	}
	if err := normalizeGiftCardConditions(&input.Conditions); err != nil {
		return SaveGiftCardTemplateInput{}, giftCardEncoded{}, err
	}
	if err := validateGiftCardReward(input.Type, input.Rewards, false); err != nil {
		return SaveGiftCardTemplateInput{}, giftCardEncoded{}, err
	}
	if input.Limits.MaxUsePerUser == 0 {
		input.Limits.MaxUsePerUser = 1
	}
	if input.Limits.MaxUsePerUser < 1 || input.Limits.MaxUsePerUser > 1_000_000_000 ||
		input.Limits.CooldownHours < 0 || input.Limits.CooldownHours > 87_600 ||
		input.Limits.InviteRewardBasisPoints < 0 || input.Limits.InviteRewardBasisPoints > 10_000 {
		return SaveGiftCardTemplateInput{}, giftCardEncoded{}, ErrInvalidInput
	}
	if input.SpecialConfig.FestivalMultiplierBasisPoints == 0 {
		input.SpecialConfig.FestivalMultiplierBasisPoints = 10_000
	}
	if input.SpecialConfig.FestivalMultiplierBasisPoints < 0 || input.SpecialConfig.FestivalMultiplierBasisPoints > 1_000_000 ||
		(input.SpecialConfig.StartedAt == nil) != (input.SpecialConfig.EndedAt == nil) ||
		input.SpecialConfig.StartedAt != nil && (!input.SpecialConfig.EndedAt.After(*input.SpecialConfig.StartedAt) || input.SpecialConfig.StartedAt.Unix() < 0) {
		return SaveGiftCardTemplateInput{}, giftCardEncoded{}, ErrInvalidInput
	}
	values := []any{input.Conditions, input.Rewards, input.Limits, input.SpecialConfig}
	encodedValues := make([]string, len(values))
	limits := []int{16 << 10, 64 << 10, 8 << 10, 8 << 10}
	for index, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded) > limits[index] {
			return SaveGiftCardTemplateInput{}, giftCardEncoded{}, ErrInvalidInput
		}
		encodedValues[index] = string(encoded)
	}
	return input, giftCardEncoded{encodedValues[0], encodedValues[1], encodedValues[2], encodedValues[3]}, nil
}

func normalizeGiftCardConditions(conditions *GiftCardConditions) error {
	if conditions.NewUserOnly && conditions.NewUserMaxDays == nil {
		defaultDays := 7
		conditions.NewUserMaxDays = &defaultDays
	}
	if conditions.NewUserMaxDays != nil && (*conditions.NewUserMaxDays < 0 || *conditions.NewUserMaxDays > 36_500) {
		return ErrInvalidInput
	}
	allowed, err := normalizeGiftCardIDs(conditions.AllowedPlanIDs)
	if err != nil {
		return err
	}
	disallowed, err := normalizeGiftCardIDs(conditions.DisallowedPlanIDs)
	if err != nil {
		return err
	}
	for _, allowedID := range allowed {
		if containsGiftCardID(disallowed, allowedID) {
			return ErrInvalidInput
		}
	}
	conditions.AllowedPlanIDs = allowed
	conditions.DisallowedPlanIDs = disallowed
	return nil
}

func normalizeGiftCardIDs(values []int64) ([]int64, error) {
	if len(values) > maxGiftCardPlanIDs {
		return nil, ErrInvalidInput
	}
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value < 1 {
			return nil, ErrInvalidInput
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func validateGiftCardReward(kind GiftCardType, reward GiftCardReward, nested bool) error {
	if reward.Balance < 0 || reward.Balance > maxGiftCardMoney || reward.TransferEnable < 0 || reward.TransferEnable > maxGiftCardTransfer ||
		reward.ExpireDays < 0 || reward.ExpireDays > 36_500 || reward.DeviceLimit < 0 || reward.DeviceLimit > 1_000 ||
		reward.PlanValidityDays < 0 || reward.PlanValidityDays > 36_500 || reward.PlanID != nil && *reward.PlanID < 1 {
		return ErrInvalidInput
	}
	if nested && (reward.PlanID != nil || reward.PlanValidityDays != 0 || len(reward.RandomRewards) != 0) {
		return ErrInvalidInput
	}
	switch kind {
	case GiftCardTypeGeneral:
		if reward.PlanID != nil || reward.PlanValidityDays != 0 || len(reward.RandomRewards) != 0 || giftCardRewardEmpty(reward) {
			return ErrInvalidInput
		}
	case GiftCardTypePlan:
		if reward.PlanID == nil || len(reward.RandomRewards) != 0 || reward.Balance != 0 || reward.TransferEnable != 0 ||
			reward.ExpireDays != 0 || reward.DeviceLimit != 0 || reward.ResetTraffic {
			return ErrInvalidInput
		}
	case GiftCardTypeMystery:
		if reward.PlanID != nil || reward.PlanValidityDays != 0 || reward.Balance != 0 || reward.TransferEnable != 0 ||
			reward.ExpireDays != 0 || reward.DeviceLimit != 0 || reward.ResetTraffic || len(reward.RandomRewards) < 1 || len(reward.RandomRewards) > maxGiftCardRandomItems {
			return ErrInvalidInput
		}
		total := int64(0)
		for _, item := range reward.RandomRewards {
			if item.Weight < 1 || item.Weight > 1_000_000 || validateGiftCardReward(GiftCardTypeGeneral, item.Reward, true) != nil {
				return ErrInvalidInput
			}
			total += int64(item.Weight)
			if total > math.MaxInt32 {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func giftCardRewardEmpty(reward GiftCardReward) bool {
	return reward.Balance == 0 && reward.TransferEnable == 0 && reward.ExpireDays == 0 && reward.DeviceLimit == 0 && !reward.ResetTraffic
}

func validateGiftCardReferences(ctx context.Context, tx *sql.Tx, input SaveGiftCardTemplateInput) error {
	// The administrator foreign key is checked by SQLite at insertion. Plan IDs
	// are checked explicitly here so invalid configuration returns ErrInvalidInput.
	ids := append(append([]int64(nil), input.Conditions.AllowedPlanIDs...), input.Conditions.DisallowedPlanIDs...)
	if input.Rewards.PlanID != nil {
		ids = append(ids, *input.Rewards.PlanID)
	}
	for _, id := range ids {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("validate gift card plan: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: gift card plan does not exist", ErrInvalidInput)
		}
	}
	return nil
}

func getGiftCardTemplate(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, templateID int64) (GiftCardTemplate, error) {
	var template GiftCardTemplate
	var conditionsJSON, rewardsJSON, limitsJSON, specialJSON string
	var createdAt, updatedAt int64
	err := database.QueryRowContext(ctx, `
		SELECT id, name, description, type, status, conditions_json, rewards_json, limits_json,
			special_config_json, icon, background_image, theme, sort_position, admin_id, revision,
			created_at, updated_at
		FROM gift_card_templates WHERE id = ?
	`, templateID).Scan(&template.ID, &template.Name, &template.Description, &template.Type, &template.Status,
		&conditionsJSON, &rewardsJSON, &limitsJSON, &specialJSON, &template.Icon, &template.BackgroundImage,
		&template.Theme, &template.SortPosition, &template.AdminID, &template.Revision, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return GiftCardTemplate{}, ErrNotFound
	}
	if err != nil {
		return GiftCardTemplate{}, fmt.Errorf("read gift card template: %w", err)
	}
	if err := decodeGiftCardJSON(conditionsJSON, &template.Conditions); err != nil {
		return GiftCardTemplate{}, err
	}
	if err := decodeGiftCardJSON(rewardsJSON, &template.Rewards); err != nil {
		return GiftCardTemplate{}, err
	}
	if err := decodeGiftCardJSON(limitsJSON, &template.Limits); err != nil {
		return GiftCardTemplate{}, err
	}
	if err := decodeGiftCardJSON(specialJSON, &template.SpecialConfig); err != nil {
		return GiftCardTemplate{}, err
	}
	template.CreatedAt = time.Unix(createdAt, 0).UTC()
	template.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return template, nil
}

func checkGiftCard(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64, code string, now time.Time, random io.Reader) (GiftCardPreview, error) {
	codeRecord, template, err := readGiftCardCodeAndTemplate(ctx, database, code)
	if err != nil {
		return GiftCardPreview{}, err
	}
	user, err := readGiftCardUser(ctx, database, userID)
	if err != nil {
		return GiftCardPreview{}, err
	}
	if !template.Status || codeRecord.Status == GiftCardCodeDisabled {
		return GiftCardPreview{}, ErrGiftCardUnavailable
	}
	if codeRecord.Status == GiftCardCodeExpired || codeRecord.ExpiresAt != nil && codeRecord.ExpiresAt.Before(now) {
		return GiftCardPreview{}, ErrGiftCardExpired
	}
	if codeRecord.Status == GiftCardCodeUsed || codeRecord.UsageCount >= codeRecord.MaxUsage {
		return GiftCardPreview{}, ErrGiftCardExhausted
	}
	conditionErr := validateGiftCardUserConditions(ctx, database, user, template, now)
	reward := template.Rewards
	if template.Type == GiftCardTypeMystery {
		reward, err = selectGiftCardMysteryReward(random, reward.RandomRewards)
		if err != nil {
			return GiftCardPreview{}, err
		}
	}
	reward, err = multiplyGiftCardReward(reward, giftCardMultiplier(template.SpecialConfig, now))
	if err != nil {
		return GiftCardPreview{}, err
	}
	return GiftCardPreview{Template: template, Code: codeRecord, Rewards: reward}, conditionErr
}

func readGiftCardCodeAndTemplate(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, code string) (GiftCardCode, GiftCardTemplate, error) {
	var record GiftCardCode
	var template GiftCardTemplate
	var userID, usedAt, expiresAt sql.NullInt64
	var actualRewards sql.NullString
	var conditionsJSON, rewardsJSON, limitsJSON, specialJSON string
	var codeCreated, codeUpdated, templateCreated, templateUpdated int64
	err := database.QueryRowContext(ctx, `
		SELECT c.id, c.template_id, c.code, c.batch_no, c.status, c.user_id, c.used_at, c.expires_at,
			c.actual_rewards_json, c.usage_count, c.max_usage, c.created_at, c.updated_at,
			t.id, t.name, t.description, t.type, t.status, t.conditions_json, t.rewards_json,
			t.limits_json, t.special_config_json, t.icon, t.background_image, t.theme,
			t.sort_position, t.admin_id, t.revision, t.created_at, t.updated_at
		FROM gift_card_codes c JOIN gift_card_templates t ON t.id = c.template_id
		WHERE c.code = ?
	`, code).Scan(&record.ID, &record.TemplateID, &record.Code, &record.BatchNo, &record.Status, &userID,
		&usedAt, &expiresAt, &actualRewards, &record.UsageCount, &record.MaxUsage, &codeCreated, &codeUpdated,
		&template.ID, &template.Name, &template.Description, &template.Type, &template.Status, &conditionsJSON,
		&rewardsJSON, &limitsJSON, &specialJSON, &template.Icon, &template.BackgroundImage, &template.Theme,
		&template.SortPosition, &template.AdminID, &template.Revision, &templateCreated, &templateUpdated)
	if errors.Is(err, sql.ErrNoRows) {
		return GiftCardCode{}, GiftCardTemplate{}, ErrNotFound
	}
	if err != nil {
		return GiftCardCode{}, GiftCardTemplate{}, fmt.Errorf("read gift card code: %w", err)
	}
	record.UserID = nullableInt64Pointer(userID)
	record.UsedAt = nullableUnixTime(usedAt)
	record.ExpiresAt = nullableUnixTime(expiresAt)
	record.CreatedAt, record.UpdatedAt = time.Unix(codeCreated, 0).UTC(), time.Unix(codeUpdated, 0).UTC()
	if actualRewards.Valid {
		var value GiftCardReward
		if err := decodeGiftCardJSON(actualRewards.String, &value); err != nil {
			return GiftCardCode{}, GiftCardTemplate{}, err
		}
		record.ActualRewards = &value
	}
	for _, item := range []struct {
		source string
		target any
	}{{conditionsJSON, &template.Conditions}, {rewardsJSON, &template.Rewards}, {limitsJSON, &template.Limits}, {specialJSON, &template.SpecialConfig}} {
		if err := decodeGiftCardJSON(item.source, item.target); err != nil {
			return GiftCardCode{}, GiftCardTemplate{}, err
		}
	}
	template.CreatedAt, template.UpdatedAt = time.Unix(templateCreated, 0).UTC(), time.Unix(templateUpdated, 0).UTC()
	record.TemplateName = template.Name
	return record, template, nil
}

func readGiftCardUser(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID int64) (giftCardUserState, error) {
	var user giftCardUserState
	err := database.QueryRowContext(ctx, `
		SELECT id, plan_id, expired_at, balance, discount, invite_user_id, transfer_enable, traffic_u,
			traffic_d, speed_limit, device_limit, group_id, next_reset_at, last_reset_at, reset_count,
			banned, account_kind, commission_type, commission_rate, commission_balance, created_at
		FROM users WHERE id = ? AND account_kind = 'human'
	`, userID).Scan(&user.id, &user.planID, &user.expiredAt, &user.balance, &user.discount,
		&user.inviteUserID, &user.transferEnable, &user.trafficUpload, &user.trafficDownload,
		&user.speedLimit, &user.deviceLimit, &user.groupID, &user.nextResetAt, &user.lastResetAt,
		&user.resetCount, &user.banned, &user.accountKind, &user.commissionType, &user.commissionRate,
		&user.commissionBalance, &user.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return giftCardUserState{}, ErrNotFound
	}
	if err != nil {
		return giftCardUserState{}, fmt.Errorf("read gift card user: %w", err)
	}
	return user, nil
}

func validateGiftCardUserConditions(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, user giftCardUserState, template GiftCardTemplate, now time.Time) error {
	if user.banned {
		return ErrGiftCardCondition
	}
	activePlan := user.planID.Valid && (!user.expiredAt.Valid || user.expiredAt.Int64 > now.Unix())
	conditions := template.Conditions
	if conditions.RequireInvite && !user.inviteUserID.Valid {
		return ErrGiftCardCondition
	}
	if conditions.PaidUserOnly {
		var paid bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM orders WHERE user_id = ? AND status = ?)`, user.id, OrderStatusCompleted).Scan(&paid); err != nil {
			return fmt.Errorf("read gift card paid-user condition: %w", err)
		}
		if !paid {
			return ErrGiftCardCondition
		}
	}
	if conditions.NewUserOnly && conditions.NewUserMaxDays != nil && time.Unix(user.createdAt, 0).AddDate(0, 0, *conditions.NewUserMaxDays).Before(now) {
		return ErrGiftCardCondition
	}
	if len(conditions.AllowedPlanIDs) > 0 && (!user.planID.Valid || !containsGiftCardID(conditions.AllowedPlanIDs, user.planID.Int64)) {
		return ErrGiftCardCondition
	}
	if user.planID.Valid && containsGiftCardID(conditions.DisallowedPlanIDs, user.planID.Int64) {
		return ErrGiftCardCondition
	}
	if template.Type == GiftCardTypePlan && activePlan {
		return ErrGiftCardActivePlan
	}
	if template.Type == GiftCardTypeGeneral && (template.Rewards.TransferEnable > 0 || template.Rewards.ExpireDays > 0 || template.Rewards.ResetTraffic) && !user.planID.Valid {
		return ErrGiftCardCondition
	}
	var count int
	var latest sql.NullInt64
	if err := database.QueryRowContext(ctx, `
		SELECT COUNT(*), MAX(used_at) FROM gift_card_usages WHERE user_id = ? AND template_id = ?
	`, user.id, template.ID).Scan(&count, &latest); err != nil {
		return fmt.Errorf("read gift card user limits: %w", err)
	}
	if count >= template.Limits.MaxUsePerUser {
		return ErrGiftCardUserLimit
	}
	if latest.Valid && template.Limits.CooldownHours > 0 && latest.Int64+int64(template.Limits.CooldownHours)*3600 > now.Unix() {
		return ErrGiftCardCooldown
	}
	return nil
}

func selectGiftCardMysteryReward(reader io.Reader, values []GiftCardRandomReward) (GiftCardReward, error) {
	total := int64(0)
	for _, value := range values {
		total += int64(value.Weight)
	}
	if total < 1 {
		return GiftCardReward{}, fmt.Errorf("%w: empty mystery reward pool", ErrInvalidInput)
	}
	selected, err := rand.Int(reader, big.NewInt(total))
	if err != nil {
		return GiftCardReward{}, fmt.Errorf("select mystery gift card reward: %w", err)
	}
	position := selected.Int64()
	for _, value := range values {
		if position < int64(value.Weight) {
			return value.Reward, nil
		}
		position -= int64(value.Weight)
	}
	return GiftCardReward{}, fmt.Errorf("%w: invalid mystery reward pool", ErrInvalidInput)
}

func giftCardMultiplier(config GiftCardSpecialConfig, now time.Time) int {
	if config.FestivalMultiplierBasisPoints <= 0 {
		return 10_000
	}
	if config.StartedAt == nil || config.EndedAt == nil {
		return 10_000
	}
	if config.StartedAt != nil && (now.Before(*config.StartedAt) || now.After(*config.EndedAt)) {
		return 10_000
	}
	return config.FestivalMultiplierBasisPoints
}

func multiplyGiftCardReward(reward GiftCardReward, multiplier int) (GiftCardReward, error) {
	var err error
	if reward.Balance, err = multiplyGiftCardInt64(reward.Balance, multiplier); err != nil {
		return GiftCardReward{}, err
	}
	if reward.TransferEnable, err = multiplyGiftCardInt64(reward.TransferEnable, multiplier); err != nil {
		return GiftCardReward{}, err
	}
	expire, err := multiplyGiftCardInt64(int64(reward.ExpireDays), multiplier)
	if err != nil || expire > 36_500 {
		return GiftCardReward{}, ErrInvalidInput
	}
	reward.ExpireDays = int(expire)
	deviceLimit, err := multiplyGiftCardInt64(int64(reward.DeviceLimit), multiplier)
	if err != nil || deviceLimit > 1_000 {
		return GiftCardReward{}, ErrInvalidInput
	}
	reward.DeviceLimit = int(deviceLimit)
	planValidityDays, err := multiplyGiftCardInt64(int64(reward.PlanValidityDays), multiplier)
	if err != nil || planValidityDays > 36_500 {
		return GiftCardReward{}, ErrInvalidInput
	}
	reward.PlanValidityDays = int(planValidityDays)
	reward.RandomRewards = nil
	return reward, nil
}

func multiplyGiftCardInt64(value int64, multiplier int) (int64, error) {
	if value == 0 || multiplier == 0 {
		return 0, nil
	}
	if multiplier < 0 {
		return 0, ErrInvalidInput
	}
	factor := int64(multiplier)
	whole := value / 10_000
	remainder := value % 10_000
	if whole > math.MaxInt64/factor {
		return 0, ErrInvalidInput
	}
	result := whole * factor
	fraction := remainder * factor / 10_000
	if result > math.MaxInt64-fraction || result+fraction > maxGiftCardMoney {
		return 0, ErrInvalidInput
	}
	return result + fraction, nil
}

func applyGiftCardReward(ctx context.Context, tx *sql.Tx, user *giftCardUserState, kind GiftCardType, reward GiftCardReward, now time.Time) error {
	if kind == GiftCardTypePlan {
		plan, err := getPlan(ctx, tx, *reward.PlanID, now)
		if err != nil {
			return err
		}
		var expires any
		var expiresTime *time.Time
		if reward.PlanValidityDays > 0 {
			value := now.AddDate(0, 0, reward.PlanValidityDays)
			expires, expiresTime = value.Unix(), &value
		} else if user.expiredAt.Valid {
			expires = user.expiredAt.Int64
			value := time.Unix(user.expiredAt.Int64, 0)
			expiresTime = &value
		}
		systemMethod, err := readSystemTrafficResetMethod(ctx, tx)
		if err != nil {
			return err
		}
		next := CalculateNextTrafficReset(plan.ResetTrafficMethod, systemMethod, expiresTime, now)
		_, err = tx.ExecContext(ctx, `
			UPDATE users SET plan_id = ?, group_id = ?, transfer_enable = ?, traffic_u = 0, traffic_d = 0,
				expired_at = ?, speed_limit = ?, device_limit = ?, next_reset_at = ?, updated_at = ?
			WHERE id = ?
		`, plan.ID, plan.GroupID, plan.TransferEnableGiB*bytesPerGiB, expires, optionalPlanInt(plan.SpeedLimit),
			optionalPlanInt(plan.DeviceLimit), nullableUnix(next), now.Unix(), user.id)
		if err != nil {
			return fmt.Errorf("apply gift card plan: %w", err)
		}
		user.planID = sql.NullInt64{Int64: plan.ID, Valid: true}
		return nil
	}
	if user.balance > maxGiftCardMoney-reward.Balance || user.transferEnable > maxGiftCardTransfer-reward.TransferEnable ||
		user.deviceLimit > 1_000-reward.DeviceLimit {
		return ErrInvalidInput
	}
	baseExpiry := now
	if user.expiredAt.Valid && user.expiredAt.Int64 > now.Unix() {
		baseExpiry = time.Unix(user.expiredAt.Int64, 0)
	}
	expiry := user.expiredAt
	if reward.ExpireDays > 0 {
		expiry = sql.NullInt64{Int64: baseExpiry.AddDate(0, 0, reward.ExpireDays).Unix(), Valid: true}
	}
	upload, download := user.trafficUpload, user.trafficDownload
	lastResetAt := nullableSQLInt(user.lastResetAt)
	resetCountIncrement := 0
	if reward.ResetTraffic && user.planID.Valid {
		upload, download = 0, 0
		lastResetAt = now.Unix()
		resetCountIncrement = 1
	}
	var next any
	if expiry.Valid && user.planID.Valid {
		plan, err := getPlan(ctx, tx, user.planID.Int64, now)
		if err != nil {
			return err
		}
		systemMethod, err := readSystemTrafficResetMethod(ctx, tx)
		if err != nil {
			return err
		}
		expiresAt := time.Unix(expiry.Int64, 0)
		next = nullableUnix(CalculateNextTrafficReset(plan.ResetTrafficMethod, systemMethod, &expiresAt, now))
	} else if user.nextResetAt.Valid {
		next = user.nextResetAt.Int64
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE users SET balance = balance + ?, transfer_enable = transfer_enable + ?, device_limit = device_limit + ?,
			traffic_u = ?, traffic_d = ?, expired_at = ?, next_reset_at = ?, last_reset_at = ?,
			reset_count = reset_count + ?, updated_at = ? WHERE id = ?
	`, reward.Balance, reward.TransferEnable, reward.DeviceLimit, upload, download, nullableSQLInt(expiry), next,
		lastResetAt, resetCountIncrement, now.Unix(), user.id)
	if err != nil {
		return fmt.Errorf("apply gift card reward: %w", err)
	}
	return nil
}

func applyGiftCardInviterReward(ctx context.Context, tx *sql.Tx, inviterID int64, reward GiftCardReward, basisPoints int, now time.Time) (GiftCardReward, error) {
	inviter, err := readGiftCardUser(ctx, tx, inviterID)
	if err != nil {
		return GiftCardReward{}, err
	}
	balance, err := multiplyGiftCardInt64(reward.Balance, basisPoints)
	if err != nil {
		return GiftCardReward{}, err
	}
	transfer, err := multiplyGiftCardInt64(reward.TransferEnable, basisPoints)
	if err != nil {
		return GiftCardReward{}, err
	}
	if inviter.balance > maxGiftCardMoney-balance || inviter.transferEnable > maxGiftCardTransfer-transfer {
		return GiftCardReward{}, ErrInvalidInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET balance = balance + ?, transfer_enable = transfer_enable + ?, updated_at = ? WHERE id = ?`, balance, transfer, now.Unix(), inviterID); err != nil {
		return GiftCardReward{}, fmt.Errorf("apply gift card inviter reward: %w", err)
	}
	return GiftCardReward{Balance: balance, TransferEnable: transfer}, nil
}

func validateGiftCardText(value string, maximum int, empty bool) bool {
	if !utf8.ValidString(value) || len(value) > maximum || strings.IndexByte(value, 0) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	return empty || value != ""
}

func validGiftCardToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')) {
			return false
		}
	}
	return true
}

func validGiftCardTheme(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func validGiftCardCode(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	return len(value) >= 8 && len(value) <= 32 && validGiftCardToken(value)
}

func randomGiftCardString(reader io.Reader, length int) (string, error) {
	buffer := make([]byte, length)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("generate gift card token: %w", err)
	}
	for index := range buffer {
		buffer[index] = giftCardAlphabet[int(buffer[index])&31]
	}
	return string(buffer), nil
}

func decodeGiftCardJSON(source string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode gift card configuration: %w", err)
	}
	return nil
}

func containsGiftCardID(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func nullableUnix(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
