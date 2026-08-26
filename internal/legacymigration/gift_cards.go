package legacymigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	maxLegacyGiftCardRows      = 1_000_000
	maxLegacyGiftCardDataBytes = int64(512 << 20)
)

type GiftCardsSnapshot struct {
	Path              string
	Size              int64
	SHA256            string
	Templates         []store.LegacyGiftCardTemplate
	Codes             []store.LegacyGiftCardCode
	Usages            []store.LegacyGiftCardUsage
	TemplatesChecksum string
	CodesChecksum     string
	UsagesChecksum    string
}

func ReadGiftCardsSnapshot(ctx context.Context, sourcePath string) (GiftCardsSnapshot, error) {
	templates := []store.LegacyGiftCardTemplate{}
	codes := []store.LegacyGiftCardCode{}
	usages := []store.LegacyGiftCardUsage{}
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		for table, columns := range map[string][]string{
			"v2_gift_card_template": {"id", "name", "description", "type", "status", "conditions", "rewards", "limits", "special_config", "icon", "background_image", "theme_color", "sort", "admin_id", "created_at", "updated_at"},
			"v2_gift_card_code":     {"id", "template_id", "code", "batch_id", "status", "user_id", "used_at", "expires_at", "actual_rewards", "usage_count", "max_usage", "metadata", "created_at", "updated_at"},
			"v2_gift_card_usage":    {"id", "code_id", "template_id", "user_id", "invite_user_id", "rewards_given", "invite_rewards", "user_level_at_use", "plan_id_at_use", "multiplier_applied", "ip_address", "user_agent", "notes", "created_at"},
		} {
			if err := requireRealTable(ctx, database, table, columns); err != nil {
				return err
			}
		}
		for _, budget := range []struct{ query, label string }{
			{`SELECT COUNT(*), COALESCE(SUM(length(CAST(name AS BLOB)) + COALESCE(length(CAST(description AS BLOB)),0) + COALESCE(length(CAST(conditions AS BLOB)),0) + length(CAST(rewards AS BLOB)) + COALESCE(length(CAST(limits AS BLOB)),0) + COALESCE(length(CAST(special_config AS BLOB)),0) + COALESCE(length(CAST(icon AS BLOB)),0) + COALESCE(length(CAST(background_image AS BLOB)),0)),0) FROM v2_gift_card_template`, "legacy gift card templates"},
			{`SELECT COUNT(*), COALESCE(SUM(length(CAST(code AS BLOB)) + COALESCE(length(CAST(batch_id AS BLOB)),0) + COALESCE(length(CAST(actual_rewards AS BLOB)),0) + COALESCE(length(CAST(metadata AS BLOB)),0)),0) FROM v2_gift_card_code`, "legacy gift card codes"},
			{`SELECT COUNT(*), COALESCE(SUM(length(CAST(rewards_given AS BLOB)) + COALESCE(length(CAST(invite_rewards AS BLOB)),0) + COALESCE(length(CAST(ip_address AS BLOB)),0) + COALESCE(length(CAST(user_agent AS BLOB)),0) + COALESCE(length(CAST(notes AS BLOB)),0)),0) FROM v2_gift_card_usage`, "legacy gift card usages"},
		} {
			if err := validateLegacyQueryBudget(ctx, database, budget.query, maxLegacyGiftCardRows, maxLegacyGiftCardDataBytes, budget.label); err != nil {
				return err
			}
		}
		var err error
		if templates, err = readLegacyGiftCardTemplates(ctx, database); err != nil {
			return err
		}
		if codes, err = readLegacyGiftCardCodes(ctx, database); err != nil {
			return err
		}
		if usages, err = readLegacyGiftCardUsages(ctx, database); err != nil {
			return err
		}
		return store.ValidateLegacyGiftCardsData(templates, codes, usages)
	})
	if err != nil {
		return GiftCardsSnapshot{}, err
	}
	return GiftCardsSnapshot{Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Templates: templates, Codes: codes, Usages: usages,
		TemplatesChecksum: store.LegacyGiftCardTemplatesChecksum(templates), CodesChecksum: store.LegacyGiftCardCodesChecksum(codes),
		UsagesChecksum: store.LegacyGiftCardUsagesChecksum(usages)}, nil
}

func readLegacyGiftCardTemplates(ctx context.Context, database *sql.DB) ([]store.LegacyGiftCardTemplate, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, name, description, type, status, conditions, rewards, limits, special_config, icon, background_image, theme_color, sort, admin_id, `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+` FROM v2_gift_card_template ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read legacy gift card templates: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyGiftCardTemplate, 0)
	for rows.Next() {
		var item store.LegacyGiftCardTemplate
		var description, conditions, limits, special, icon, background, theme sql.NullString
		var rewards string
		if err := rows.Scan(&item.ID, &item.Name, &description, &item.Type, &item.Status, &conditions, &rewards, &limits, &special, &icon, &background, &theme, &item.SortPosition, &item.AdminID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy gift card template: %w", err)
		}
		item.Description, item.Icon, item.BackgroundImage, item.Theme = nullString(description), nullString(icon), nullString(background), nullString(theme)
		if item.Theme == "" {
			item.Theme = "#1890ff"
		}
		if err := decodeStrictGiftCardJSON(conditions, &item.Conditions, "conditions"); err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d: %w", item.ID, err)
		}
		if err := decodeLegacyGiftCardRewards(rewards, item.Type, &item.Rewards); err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d rewards: %w", item.ID, err)
		}
		if err := decodeLegacyGiftCardLimits(limits, &item.Limits); err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d limits: %w", item.ID, err)
		}
		if err := decodeLegacyGiftCardSpecial(special, &item.SpecialConfig); err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d special config: %w", item.ID, err)
		}
		if item.Conditions.NewUserOnly && item.Conditions.NewUserMaxDays == nil {
			defaultDays := 7
			item.Conditions.NewUserMaxDays = &defaultDays
		}
		if item.Conditions.NewUserMaxDays != nil && !item.Conditions.NewUserOnly {
			item.Conditions.NewUserMaxDays = nil
		}
		item.Conditions.AllowedPlanIDs, err = normalizeLegacyGiftCardPlanIDs(item.Conditions.AllowedPlanIDs)
		if err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d allowed plans: %w", item.ID, err)
		}
		item.Conditions.DisallowedPlanIDs, err = normalizeLegacyGiftCardPlanIDs(item.Conditions.DisallowedPlanIDs)
		if err != nil {
			return nil, fmt.Errorf("legacy gift card template id %d disallowed plans: %w", item.ID, err)
		}
		for _, allowedID := range item.Conditions.AllowedPlanIDs {
			for _, disallowedID := range item.Conditions.DisallowedPlanIDs {
				if allowedID == disallowedID {
					return nil, fmt.Errorf("legacy gift card template id %d conditions: plan %d is both allowed and disallowed", item.ID, allowedID)
				}
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy gift card templates: %w", err)
	}
	return result, nil
}

func normalizeLegacyGiftCardPlanIDs(values []int64) ([]int64, error) {
	if len(values) > 100 {
		return nil, errors.New("more than 100 plan ids")
	}
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value < 1 {
			return nil, errors.New("plan ids must be positive")
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func readLegacyGiftCardCodes(ctx context.Context, database *sql.DB) ([]store.LegacyGiftCardCode, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, template_id, code, batch_id, status, user_id, used_at, expires_at, actual_rewards, usage_count, max_usage, metadata, `+legacyUnixExpression("created_at")+`, `+legacyUnixExpression("updated_at")+` FROM v2_gift_card_code ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read legacy gift card codes: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyGiftCardCode, 0)
	for rows.Next() {
		var item store.LegacyGiftCardCode
		var batch, actual, metadata sql.NullString
		var userID, usedAt, expiresAt sql.NullInt64
		var legacyStatus int
		if err := rows.Scan(&item.ID, &item.TemplateID, &item.Code, &batch, &legacyStatus, &userID, &usedAt, &expiresAt, &actual, &item.UsageCount, &item.MaxUsage, &metadata, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan legacy gift card code: %w", err)
		}
		item.UserID, item.UsedAt, item.ExpiresAt = legacyNullInt64Pointer(userID), legacyNullInt64Pointer(usedAt), legacyNullInt64Pointer(expiresAt)
		item.BatchNo = normalizeLegacyGiftCardBatch(batch.String, item.Code)
		item.MetadataJSON, err = normalizeLegacyJSONObject(metadata, "metadata")
		if err != nil {
			return nil, fmt.Errorf("legacy gift card code id %d: %w", item.ID, err)
		}
		item.Status, err = normalizeLegacyGiftCardStatus(legacyStatus, item.UsageCount, item.MaxUsage)
		if err != nil {
			return nil, fmt.Errorf("legacy gift card code id %d: %w", item.ID, err)
		}
		if actual.Valid && strings.TrimSpace(actual.String) != "" && strings.TrimSpace(actual.String) != "null" {
			var reward store.GiftCardReward
			if err := decodeLegacyGiftCardAppliedReward(actual.String, &reward); err != nil {
				return nil, fmt.Errorf("legacy gift card code id %d actual rewards: %w", item.ID, err)
			}
			item.ActualRewards = &reward
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy gift card codes: %w", err)
	}
	return result, nil
}

func readLegacyGiftCardUsages(ctx context.Context, database *sql.DB) ([]store.LegacyGiftCardUsage, error) {
	rows, err := database.QueryContext(ctx, `SELECT id, code_id, template_id, user_id, invite_user_id, rewards_given, invite_rewards, user_level_at_use, plan_id_at_use, CAST(multiplier_applied AS TEXT), ip_address, user_agent, notes, `+legacyUnixExpression("created_at")+` FROM v2_gift_card_usage ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read legacy gift card usages: %w", err)
	}
	defer rows.Close()
	result := make([]store.LegacyGiftCardUsage, 0)
	for rows.Next() {
		var item store.LegacyGiftCardUsage
		var inviter, level, plan sql.NullInt64
		var inviteRewards, ip, agent, notes sql.NullString
		var rewards, multiplier string
		if err := rows.Scan(&item.ID, &item.CodeID, &item.TemplateID, &item.UserID, &inviter, &rewards, &inviteRewards, &level, &plan, &multiplier, &ip, &agent, &notes, &item.UsedAt); err != nil {
			return nil, fmt.Errorf("scan legacy gift card usage: %w", err)
		}
		item.InviterID, item.UserLevelAtUse, item.UserPlanID = legacyNullInt64Pointer(inviter), legacyNullInt64Pointer(level), legacyNullInt64Pointer(plan)
		item.IPAddress, item.UserAgent, item.Notes = nullString(ip), nullString(agent), nullString(notes)
		if err := decodeLegacyGiftCardAppliedReward(rewards, &item.Rewards); err != nil {
			return nil, fmt.Errorf("legacy gift card usage id %d rewards: %w", item.ID, err)
		}
		if inviteRewards.Valid && strings.TrimSpace(inviteRewards.String) != "" && strings.TrimSpace(inviteRewards.String) != "null" {
			if err := decodeLegacyGiftCardAppliedReward(inviteRewards.String, &item.InviterRewards); err != nil {
				return nil, fmt.Errorf("legacy gift card usage id %d inviter rewards: %w", item.ID, err)
			}
		}
		item.MultiplierBasisPoints, err = decimalToBasisPoints(multiplier, 1_000_000)
		if err != nil {
			return nil, fmt.Errorf("legacy gift card usage id %d multiplier: %w", item.ID, err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy gift card usages: %w", err)
	}
	return result, nil
}

type legacyGiftCardRewardWire struct {
	Balance          int64                      `json:"balance,omitempty"`
	TransferEnable   int64                      `json:"transfer_enable,omitempty"`
	ExpireDays       int                        `json:"expire_days,omitempty"`
	DeviceLimit      int                        `json:"device_limit,omitempty"`
	ResetTraffic     bool                       `json:"reset_package,omitempty"`
	PlanID           *int64                     `json:"plan_id,omitempty"`
	PlanValidityDays int                        `json:"plan_validity_days,omitempty"`
	RandomRewards    []legacyGiftCardRandomWire `json:"random_rewards,omitempty"`
}

type legacyGiftCardRandomWire struct {
	Weight         int   `json:"weight"`
	Balance        int64 `json:"balance,omitempty"`
	TransferEnable int64 `json:"transfer_enable,omitempty"`
	ExpireDays     int   `json:"expire_days,omitempty"`
	DeviceLimit    int   `json:"device_limit,omitempty"`
	ResetTraffic   bool  `json:"reset_package,omitempty"`
}

func decodeLegacyGiftCardRewards(source string, kind store.GiftCardType, target *store.GiftCardReward) error {
	var wire legacyGiftCardRewardWire
	if err := decodeStrictJSONString(source, &wire); err != nil {
		return err
	}
	*target = store.GiftCardReward{Balance: wire.Balance, TransferEnable: wire.TransferEnable, ExpireDays: wire.ExpireDays, DeviceLimit: wire.DeviceLimit, ResetTraffic: wire.ResetTraffic, PlanID: wire.PlanID, PlanValidityDays: wire.PlanValidityDays}
	if kind == store.GiftCardTypeMystery {
		target.RandomRewards = make([]store.GiftCardRandomReward, 0, len(wire.RandomRewards))
		for _, item := range wire.RandomRewards {
			target.RandomRewards = append(target.RandomRewards, store.GiftCardRandomReward{Weight: item.Weight, Reward: store.GiftCardReward{Balance: item.Balance, TransferEnable: item.TransferEnable, ExpireDays: item.ExpireDays, DeviceLimit: item.DeviceLimit, ResetTraffic: item.ResetTraffic}})
		}
	}
	return nil
}

func decodeLegacyGiftCardAppliedReward(source string, target *store.GiftCardReward) error {
	var wire legacyGiftCardRewardWire
	if err := decodeStrictJSONString(source, &wire); err != nil {
		return err
	}
	if len(wire.RandomRewards) != 0 {
		return errors.New("applied rewards cannot contain a random reward pool")
	}
	*target = store.GiftCardReward{Balance: wire.Balance, TransferEnable: wire.TransferEnable, ExpireDays: wire.ExpireDays, DeviceLimit: wire.DeviceLimit, ResetTraffic: wire.ResetTraffic, PlanID: wire.PlanID, PlanValidityDays: wire.PlanValidityDays}
	return nil
}

type legacyGiftCardLimitsWire struct {
	MaxUsePerUser    int         `json:"max_use_per_user,omitempty"`
	CooldownHours    int         `json:"cooldown_hours,omitempty"`
	InviteRewardRate json.Number `json:"invite_reward_rate,omitempty"`
}

func decodeLegacyGiftCardLimits(source sql.NullString, target *store.GiftCardLimits) error {
	if !source.Valid || strings.TrimSpace(source.String) == "" || strings.TrimSpace(source.String) == "null" {
		target.MaxUsePerUser = 1
		return nil
	}
	var wire legacyGiftCardLimitsWire
	if err := decodeStrictJSONString(source.String, &wire); err != nil {
		return err
	}
	if wire.InviteRewardRate != "" {
		value, err := decimalToBasisPoints(wire.InviteRewardRate.String(), 10_000)
		if err != nil {
			return fmt.Errorf("invite_reward_rate: %w", err)
		}
		target.InviteRewardBasisPoints = value
	}
	target.MaxUsePerUser, target.CooldownHours = wire.MaxUsePerUser, wire.CooldownHours
	if target.MaxUsePerUser == 0 {
		target.MaxUsePerUser = 1
	}
	return nil
}

type legacyGiftCardSpecialWire struct {
	StartTime     *int64      `json:"start_time,omitempty"`
	EndTime       *int64      `json:"end_time,omitempty"`
	FestivalBonus json.Number `json:"festival_bonus,omitempty"`
}

func decodeLegacyGiftCardSpecial(source sql.NullString, target *store.GiftCardSpecialConfig) error {
	target.FestivalMultiplierBasisPoints = 10_000
	if !source.Valid || strings.TrimSpace(source.String) == "" || strings.TrimSpace(source.String) == "null" {
		return nil
	}
	var wire legacyGiftCardSpecialWire
	if err := decodeStrictJSONString(source.String, &wire); err != nil {
		return err
	}
	if wire.FestivalBonus != "" {
		value, err := decimalToBasisPoints(wire.FestivalBonus.String(), 1_000_000)
		if err != nil {
			return fmt.Errorf("festival_bonus: %w", err)
		}
		target.FestivalMultiplierBasisPoints = value
	}
	if (wire.StartTime == nil) != (wire.EndTime == nil) {
		return errors.New("start_time and end_time must either both be set or both be absent")
	}
	if wire.StartTime != nil {
		start, end := time.Unix(*wire.StartTime, 0).UTC(), time.Unix(*wire.EndTime, 0).UTC()
		target.StartedAt, target.EndedAt = &start, &end
	}
	return nil
}

func decodeStrictGiftCardJSON(source sql.NullString, target any, label string) error {
	if !source.Valid || strings.TrimSpace(source.String) == "" || strings.TrimSpace(source.String) == "null" {
		return nil
	}
	if err := decodeStrictJSONString(source.String, target); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func decodeStrictJSONString(source string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func normalizeLegacyJSONObject(source sql.NullString, label string) (string, error) {
	if !source.Valid || strings.TrimSpace(source.String) == "" || strings.TrimSpace(source.String) == "null" {
		return "{}", nil
	}
	decoder := json.NewDecoder(strings.NewReader(source.String))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return "", fmt.Errorf("%s must be a JSON object", label)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return "", fmt.Errorf("%s contains trailing JSON data", label)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func normalizeLegacyGiftCardStatus(value, usageCount, maxUsage int) (store.GiftCardCodeStatus, error) {
	switch value {
	case 0:
		return store.GiftCardCodeActive, nil
	case 1:
		if usageCount < maxUsage {
			return store.GiftCardCodeActive, nil
		}
		return store.GiftCardCodeUsed, nil
	case 2:
		return store.GiftCardCodeExpired, nil
	case 3:
		return store.GiftCardCodeDisabled, nil
	default:
		return 0, errors.New("invalid legacy gift card status")
	}
}

func normalizeLegacyGiftCardBatch(value, code string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 16 && len(value) <= 40 {
		valid := true
		for _, character := range value {
			if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-') {
				valid = false
				break
			}
		}
		if valid {
			return value
		}
	}
	seed := value
	if seed == "" {
		seed = code
	}
	digest := sha256.Sum256([]byte(seed))
	return "legacy_" + hex.EncodeToString(digest[:])[:25]
}

func decimalToBasisPoints(value string, maximum int) (int, error) {
	rational := new(big.Rat)
	if _, ok := rational.SetString(strings.TrimSpace(value)); !ok || rational.Sign() < 0 {
		return 0, errors.New("invalid non-negative decimal")
	}
	rational.Mul(rational, big.NewRat(10_000, 1))
	if !rational.IsInt() {
		return 0, errors.New("decimal has more than four fractional digits")
	}
	text := rational.Num().String()
	parsed, err := strconv.ParseInt(text, 10, 32)
	if err != nil || parsed > int64(maximum) {
		return 0, errors.New("decimal is outside the supported range")
	}
	return int(parsed), nil
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}
func legacyNullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
