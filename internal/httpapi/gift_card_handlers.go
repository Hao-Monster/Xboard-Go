package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

var giftCardTypeNames = map[int]string{1: "通用礼品卡", 2: "套餐礼品卡", 3: "盲盒礼品卡"}

type giftCardRewardRequest struct {
	Balance          int64                         `json:"balance"`
	TransferEnable   int64                         `json:"transfer_enable"`
	ExpireDays       int                           `json:"expire_days"`
	DeviceLimit      int                           `json:"device_limit"`
	ResetTraffic     bool                          `json:"reset_package"`
	PlanID           *int64                        `json:"plan_id"`
	PlanValidityDays int                           `json:"plan_validity_days"`
	RandomRewards    []giftCardRandomRewardRequest `json:"random_rewards"`
}

type giftCardRandomRewardRequest struct {
	Weight         int                    `json:"weight"`
	Balance        int64                  `json:"balance"`
	TransferEnable int64                  `json:"transfer_enable"`
	ExpireDays     int                    `json:"expire_days"`
	DeviceLimit    int                    `json:"device_limit"`
	ResetTraffic   bool                   `json:"reset_package"`
	Rewards        *giftCardRewardRequest `json:"rewards"`
}

func (input giftCardRewardRequest) storeReward() store.GiftCardReward {
	reward := store.GiftCardReward{Balance: input.Balance, TransferEnable: input.TransferEnable, ExpireDays: input.ExpireDays,
		DeviceLimit: input.DeviceLimit, ResetTraffic: input.ResetTraffic, PlanID: input.PlanID, PlanValidityDays: input.PlanValidityDays}
	for _, item := range input.RandomRewards {
		value := store.GiftCardReward{Balance: item.Balance, TransferEnable: item.TransferEnable, ExpireDays: item.ExpireDays,
			DeviceLimit: item.DeviceLimit, ResetTraffic: item.ResetTraffic}
		if item.Rewards != nil {
			value = item.Rewards.storeReward()
		}
		reward.RandomRewards = append(reward.RandomRewards, store.GiftCardRandomReward{Weight: item.Weight, Reward: value})
	}
	return reward
}

type giftCardLimitsRequest struct {
	MaxUsePerUser           int     `json:"max_use_per_user"`
	CooldownHours           int     `json:"cooldown_hours"`
	InviteRewardBasisPoints int     `json:"invite_reward_basis_points"`
	InviteRewardRate        float64 `json:"invite_reward_rate"`
}

func (input giftCardLimitsRequest) storeLimits() (store.GiftCardLimits, error) {
	basisPoints := input.InviteRewardBasisPoints
	if input.InviteRewardRate != 0 {
		converted := input.InviteRewardRate * 10_000
		if math.IsNaN(converted) || math.IsInf(converted, 0) || converted < 0 || converted > 10_000 || math.Abs(converted-math.Round(converted)) > 0.0000001 || basisPoints != 0 {
			return store.GiftCardLimits{}, store.ErrInvalidInput
		}
		basisPoints = int(math.Round(converted))
	}
	return store.GiftCardLimits{MaxUsePerUser: input.MaxUsePerUser, CooldownHours: input.CooldownHours, InviteRewardBasisPoints: basisPoints}, nil
}

type giftCardSpecialRequest struct {
	StartedAt                     json.RawMessage `json:"started_at"`
	EndedAt                       json.RawMessage `json:"ended_at"`
	StartTime                     json.RawMessage `json:"start_time"`
	EndTime                       json.RawMessage `json:"end_time"`
	FestivalMultiplierBasisPoints int             `json:"festival_multiplier_basis_points"`
	FestivalBonus                 float64         `json:"festival_bonus"`
}

func (input giftCardSpecialRequest) storeSpecial() (store.GiftCardSpecialConfig, error) {
	started, ended := input.StartedAt, input.EndedAt
	if len(started) == 0 {
		started = input.StartTime
	}
	if len(ended) == 0 {
		ended = input.EndTime
	}
	result := store.GiftCardSpecialConfig{FestivalMultiplierBasisPoints: input.FestivalMultiplierBasisPoints}
	var err error
	if result.StartedAt, err = giftCardWireTime(started); err != nil {
		return store.GiftCardSpecialConfig{}, err
	}
	if result.EndedAt, err = giftCardWireTime(ended); err != nil {
		return store.GiftCardSpecialConfig{}, err
	}
	if input.FestivalBonus != 0 {
		converted := input.FestivalBonus * 10_000
		if math.IsNaN(converted) || math.IsInf(converted, 0) || converted < 0 || converted > 1_000_000 || math.Abs(converted-math.Round(converted)) > 0.0000001 || result.FestivalMultiplierBasisPoints != 0 {
			return store.GiftCardSpecialConfig{}, store.ErrInvalidInput
		}
		result.FestivalMultiplierBasisPoints = int(math.Round(converted))
	}
	return result, nil
}

func giftCardWireTime(raw json.RawMessage) (*time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var unix int64
	if err := json.Unmarshal(raw, &unix); err == nil {
		value := time.Unix(unix, 0).UTC()
		return &value, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, store.ErrInvalidInput
	}
	value, err := time.Parse(time.RFC3339, encoded)
	if err != nil {
		return nil, store.ErrInvalidInput
	}
	value = value.UTC()
	return &value, nil
}

type giftCardTemplateRequest struct {
	ID              int64                     `json:"id"`
	Revision        int64                     `json:"revision"`
	Name            *string                   `json:"name"`
	Description     *string                   `json:"description"`
	Type            *store.GiftCardType       `json:"type"`
	Status          *bool                     `json:"status"`
	Conditions      *store.GiftCardConditions `json:"conditions"`
	Rewards         *giftCardRewardRequest    `json:"rewards"`
	Limits          *giftCardLimitsRequest    `json:"limits"`
	SpecialConfig   *giftCardSpecialRequest   `json:"special_config"`
	Icon            *string                   `json:"icon"`
	BackgroundImage *string                   `json:"background_image"`
	Theme           *string                   `json:"theme"`
	ThemeColor      *string                   `json:"theme_color"`
	SortPosition    *int                      `json:"sort"`
}

func (input giftCardTemplateRequest) storeInput(current *store.GiftCardTemplate) (store.SaveGiftCardTemplateInput, error) {
	result := store.SaveGiftCardTemplateInput{}
	if current != nil {
		result = store.SaveGiftCardTemplateInput{Name: current.Name, Description: current.Description, Type: current.Type, Status: current.Status,
			Conditions: current.Conditions, Rewards: current.Rewards, Limits: current.Limits, SpecialConfig: current.SpecialConfig,
			Icon: current.Icon, BackgroundImage: current.BackgroundImage, Theme: current.Theme, SortPosition: current.SortPosition}
	}
	if input.Name != nil {
		result.Name = *input.Name
	}
	if input.Description != nil {
		result.Description = *input.Description
	}
	if input.Type != nil {
		result.Type = *input.Type
	}
	if input.Status != nil {
		result.Status = *input.Status
	} else if current == nil {
		result.Status = true
	}
	if input.Conditions != nil {
		result.Conditions = *input.Conditions
	}
	if input.Rewards != nil {
		result.Rewards = input.Rewards.storeReward()
	}
	if input.Limits != nil {
		value, err := input.Limits.storeLimits()
		if err != nil {
			return result, err
		}
		result.Limits = value
	}
	if input.SpecialConfig != nil {
		value, err := input.SpecialConfig.storeSpecial()
		if err != nil {
			return result, err
		}
		result.SpecialConfig = value
	}
	if input.Icon != nil {
		result.Icon = *input.Icon
	}
	if input.BackgroundImage != nil {
		result.BackgroundImage = *input.BackgroundImage
	}
	if input.Theme != nil {
		result.Theme = *input.Theme
	}
	if input.ThemeColor != nil {
		result.Theme = *input.ThemeColor
	}
	if input.SortPosition != nil {
		result.SortPosition = *input.SortPosition
	}
	return result, nil
}

func (s *server) listGiftCardTemplates(w http.ResponseWriter, r *http.Request) {
	page, size, ok := giftCardPage(w, r, 20)
	if !ok {
		return
	}
	filter := store.GiftCardTemplateFilter{Page: page, PageSize: size}
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			handleGiftCardError(w, store.ErrInvalidInput)
			return
		}
		typed := store.GiftCardType(value)
		filter.Type = &typed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			numeric, numericErr := strconv.Atoi(raw)
			if numericErr != nil || numeric < 0 || numeric > 1 {
				handleGiftCardError(w, store.ErrInvalidInput)
				return
			}
			value = numeric == 1
		}
		filter.Status = &value
	}
	result, err := s.store.ListGiftCardTemplates(r.Context(), filter)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) createGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	var input giftCardTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := input.storeInput(nil)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	created, err := s.store.CreateGiftCardTemplate(r.Context(), value, session.UserID, s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, created)
}

func (s *server) updateGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "templateID")
	if !ok {
		return
	}
	var input giftCardTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetGiftCardTemplate(r.Context(), id)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	value, err := input.storeInput(&current)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	revision := input.Revision
	if revision == 0 {
		revision = current.Revision
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateGiftCardTemplate(r.Context(), id, revision, value, session.UserID, s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) deleteGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "templateID")
	if !ok {
		return
	}
	if err := s.store.DeleteGiftCardTemplate(r.Context(), id); err != nil {
		handleGiftCardError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type giftCardGenerateRequest struct {
	TemplateID   int64  `json:"template_id"`
	Count        int    `json:"count"`
	Prefix       string `json:"prefix"`
	ExpiresAt    *int64 `json:"expires_at"`
	ExpiresHours *int   `json:"expires_hours"`
	MaxUsage     int    `json:"max_usage"`
	DownloadCSV  bool   `json:"download_csv"`
}

func (input giftCardGenerateRequest) storeInput(now time.Time) store.GenerateGiftCardCodesInput {
	result := store.GenerateGiftCardCodesInput{Count: input.Count, Prefix: input.Prefix, MaxUsage: input.MaxUsage}
	if result.MaxUsage == 0 {
		result.MaxUsage = 1
	}
	if input.ExpiresAt != nil {
		value := time.Unix(*input.ExpiresAt, 0).UTC()
		result.ExpiresAt = &value
	}
	if input.ExpiresHours != nil {
		value := now.Add(time.Duration(*input.ExpiresHours) * time.Hour)
		result.ExpiresAt = &value
	}
	return result
}

func (s *server) generateGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	var input giftCardGenerateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	codes, err := s.store.GenerateGiftCardCodes(r.Context(), input.TemplateID, input.storeInput(s.now()), s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	if input.DownloadCSV {
		writeGiftCardCSV(w, codes)
		return
	}
	writeSuccess(w, http.StatusCreated, codes)
}

func (s *server) listGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	page, size, ok := giftCardPage(w, r, 20)
	if !ok {
		return
	}
	filter := store.GiftCardCodeFilter{Page: page, PageSize: size, Query: r.URL.Query().Get("query"), BatchNo: r.URL.Query().Get("batch_no")}
	if filter.BatchNo == "" {
		filter.BatchNo = r.URL.Query().Get("batch_id")
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("template_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			handleGiftCardError(w, store.ErrInvalidInput)
			return
		}
		filter.TemplateID = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			handleGiftCardError(w, store.ErrInvalidInput)
			return
		}
		status := store.GiftCardCodeStatus(value)
		filter.Status = &status
	}
	result, err := s.store.ListGiftCardCodes(r.Context(), filter)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

type giftCardCodeRequest struct {
	ID        int64                     `json:"id"`
	Code      string                    `json:"code"`
	Status    *store.GiftCardCodeStatus `json:"status"`
	ExpiresAt json.RawMessage           `json:"expires_at"`
	MaxUsage  *int                      `json:"max_usage"`
}

func (s *server) updateGiftCardCode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "codeID")
	if !ok {
		return
	}
	var input giftCardCodeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetGiftCardCode(r.Context(), id)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	value := store.SaveGiftCardCodeInput{Code: current.Code, Status: current.Status, ExpiresAt: current.ExpiresAt, MaxUsage: current.MaxUsage}
	if input.Code != "" {
		value.Code = input.Code
	}
	if input.Status != nil {
		value.Status = *input.Status
	}
	if input.MaxUsage != nil {
		value.MaxUsage = *input.MaxUsage
	}
	if len(input.ExpiresAt) > 0 {
		parsed, parseErr := giftCardWireTime(input.ExpiresAt)
		if parseErr != nil {
			handleGiftCardError(w, parseErr)
			return
		}
		value.ExpiresAt = parsed
	}
	updated, err := s.store.UpdateGiftCardCode(r.Context(), id, value, s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) toggleGiftCardCode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "codeID")
	if !ok {
		return
	}
	value, err := s.store.ToggleGiftCardCode(r.Context(), id, s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}
func (s *server) deleteGiftCardCode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "codeID")
	if !ok {
		return
	}
	if err := s.store.DeleteGiftCardCode(r.Context(), id); err != nil {
		handleGiftCardError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listGiftCardUsages(w http.ResponseWriter, r *http.Request) {
	page, size, ok := giftCardPage(w, r, 20)
	if !ok {
		return
	}
	filter := store.GiftCardUsageFilter{Page: page, PageSize: size}
	for name, target := range map[string]**int64{"user_id": &filter.UserID, "template_id": &filter.TemplateID, "code_id": &filter.CodeID} {
		if raw := strings.TrimSpace(r.URL.Query().Get(name)); raw != "" {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				handleGiftCardError(w, store.ErrInvalidInput)
				return
			}
			*target = &value
		}
	}
	result, err := s.store.ListGiftCardUsages(r.Context(), filter)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) giftCardStatistics(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GiftCardStatistics(r.Context(), s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}
func (s *server) giftCardTypes(w http.ResponseWriter, _ *http.Request) {
	writeSuccess(w, http.StatusOK, giftCardTypeNames)
}

func (s *server) giftCardCheckCompat(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.CredentialKind == store.CredentialKindAccessToken {
		s.legacyCheckGiftCard(w, r)
		return
	}
	s.checkGiftCard(w, r)
}

func (s *server) giftCardRedeemCompat(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.CredentialKind == store.CredentialKindAccessToken {
		s.legacyRedeemGiftCard(w, r)
		return
	}
	s.redeemGiftCard(w, r)
}

func (s *server) giftCardHistoryCompat(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.CredentialKind == store.CredentialKindAccessToken {
		s.legacyGiftCardHistory(w, r)
		return
	}
	s.giftCardHistory(w, r)
}

func (s *server) giftCardTypesCompat(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.CredentialKind == store.CredentialKindAccessToken {
		s.legacyUserGiftCardTypes(w, r)
		return
	}
	s.giftCardTypes(w, r)
}

func (s *server) checkGiftCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	preview, err := s.store.CheckGiftCard(r.Context(), session.UserID, input.Code, s.now())
	if err != nil && !giftCardEligibilityError(err) {
		handleGiftCardError(w, err)
		return
	}
	reason := ""
	if err != nil {
		reason = giftCardMessage(err)
	}
	writeSuccess(w, http.StatusOK, map[string]any{"code_info": preview.Code, "template": preview.Template, "reward_preview": preview.Rewards, "can_redeem": err == nil, "reason": reason})
}

func (s *server) redeemGiftCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	usage, err := s.store.RedeemGiftCard(r.Context(), store.RedeemGiftCardInput{UserID: session.UserID, Code: input.Code, IPAddress: clientIP(r), UserAgent: r.UserAgent()}, s.now())
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"message": "兑换成功！", "rewards": usage.Rewards, "invite_rewards": usage.InviterRewards, "template_name": usage.TemplateName, "usage": usage})
}

func (s *server) giftCardHistory(w http.ResponseWriter, r *http.Request) {
	page, size, ok := giftCardPage(w, r, 15)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ListGiftCardUsages(r.Context(), store.GiftCardUsageFilter{Page: page, PageSize: size, UserID: &session.UserID})
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	for index := range result.Items {
		result.Items[index].Code = maskGiftCardCode(result.Items[index].Code)
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) giftCardHistoryDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "usageID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.GetGiftCardUsage(r.Context(), id, session.UserID)
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) legacyGiftCardTemplates(w http.ResponseWriter, r *http.Request) {
	s.legacyGiftCardPage(w, r, "templates")
}
func (s *server) legacyGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	s.legacyGiftCardPage(w, r, "codes")
}
func (s *server) legacyGiftCardUsages(w http.ResponseWriter, r *http.Request) {
	s.legacyGiftCardPage(w, r, "usages")
}

func (s *server) legacyGiftCardPage(w http.ResponseWriter, r *http.Request, kind string) {
	page, size, ok := giftCardPage(w, r, 15)
	if !ok {
		return
	}
	var data any
	var total int64
	var err error
	switch kind {
	case "templates":
		filter := store.GiftCardTemplateFilter{Page: page, PageSize: size}
		if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				writeLegacyGiftCardError(w, store.ErrInvalidInput)
				return
			}
			value := store.GiftCardType(parsed)
			filter.Type = &value
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 || parsed > 1 {
				writeLegacyGiftCardError(w, store.ErrInvalidInput)
				return
			}
			value := parsed == 1
			filter.Status = &value
		}
		value, queryErr := s.store.ListGiftCardTemplates(r.Context(), filter)
		items := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			items = append(items, legacyGiftCardTemplate(item))
		}
		data, total, err = items, value.Total, queryErr
	case "codes":
		filter := store.GiftCardCodeFilter{Page: page, PageSize: size, BatchNo: r.URL.Query().Get("batch_id")}
		if raw := strings.TrimSpace(r.URL.Query().Get("template_id")); raw != "" {
			parsed, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				writeLegacyGiftCardError(w, store.ErrInvalidInput)
				return
			}
			filter.TemplateID = &parsed
		}
		if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil {
				writeLegacyGiftCardError(w, store.ErrInvalidInput)
				return
			}
			converted, convertErr := legacyGiftCardStatusToStore(parsed)
			if convertErr != nil {
				writeLegacyGiftCardError(w, convertErr)
				return
			}
			filter.Status = &converted
		}
		value, queryErr := s.store.ListGiftCardCodes(r.Context(), filter)
		items := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			items = append(items, legacyGiftCardCode(item))
		}
		data, total, err = items, value.Total, queryErr
	default:
		filter := store.GiftCardUsageFilter{Page: page, PageSize: size}
		for name, target := range map[string]**int64{"template_id": &filter.TemplateID, "user_id": &filter.UserID} {
			if raw := strings.TrimSpace(r.URL.Query().Get(name)); raw != "" {
				parsed, parseErr := strconv.ParseInt(raw, 10, 64)
				if parseErr != nil {
					writeLegacyGiftCardError(w, store.ErrInvalidInput)
					return
				}
				*target = &parsed
			}
		}
		value, queryErr := s.store.ListGiftCardUsages(r.Context(), filter)
		items := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			items = append(items, legacyGiftCardUsage(item, false))
		}
		data, total, err = items, value.Total, queryErr
	}
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "current_page": page, "per_page": size, "last_page": lastGiftCardPage(total, size), "data": data})
}

func (s *server) legacyCreateGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	var input giftCardTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := input.storeInput(nil)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	created, err := s.store.CreateGiftCardTemplate(r.Context(), value, session.UserID, s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacyGiftCardTemplate(created))
}
func (s *server) legacyUpdateGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	var input giftCardTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetGiftCardTemplate(r.Context(), input.ID)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	value, err := input.storeInput(&current)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateGiftCardTemplate(r.Context(), input.ID, current.Revision, value, session.UserID, s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacyGiftCardTemplate(updated))
}
func (s *server) legacyDeleteGiftCardTemplate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.DeleteGiftCardTemplate(r.Context(), input.ID); err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyGenerateGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	var input giftCardGenerateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	codes, err := s.store.GenerateGiftCardCodes(r.Context(), input.TemplateID, input.storeInput(s.now()), s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	if input.DownloadCSV {
		writeGiftCardCSV(w, codes)
		return
	}
	batch := ""
	if len(codes) > 0 {
		batch = codes[0].BatchNo
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"batch_id": batch, "count": len(codes), "message": "生成成功"})
}
func (s *server) legacyToggleGiftCardCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID     int64  `json:"id"`
		Action string `json:"action"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetGiftCardCode(r.Context(), input.ID)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	switch input.Action {
	case "disable":
		if current.Status == store.GiftCardCodeActive {
			_, err = s.store.ToggleGiftCardCode(r.Context(), input.ID, s.now())
		} else if current.Status != store.GiftCardCodeDisabled {
			err = store.ErrGiftCardUnavailable
		}
	case "enable":
		if current.Status == store.GiftCardCodeDisabled {
			_, err = s.store.ToggleGiftCardCode(r.Context(), input.ID, s.now())
		} else if current.Status != store.GiftCardCodeActive {
			err = store.ErrGiftCardUnavailable
		}
	default:
		err = store.ErrInvalidInput
	}
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	message := "已禁用"
	if input.Action == "enable" {
		message = "已启用"
	}
	writeLegacySuccess(w, http.StatusOK, map[string]string{"message": message})
}
func (s *server) legacyUpdateGiftCardCode(w http.ResponseWriter, r *http.Request) {
	var input giftCardCodeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetGiftCardCode(r.Context(), input.ID)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	value := store.SaveGiftCardCodeInput{Code: current.Code, Status: current.Status, ExpiresAt: current.ExpiresAt, MaxUsage: current.MaxUsage}
	if input.Code != "" {
		value.Code = input.Code
	}
	if input.Status != nil {
		converted, convertErr := legacyGiftCardStatusToStore(int(*input.Status))
		if convertErr != nil {
			writeLegacyGiftCardError(w, convertErr)
			return
		}
		value.Status = converted
	}
	if input.MaxUsage != nil {
		value.MaxUsage = *input.MaxUsage
	}
	if len(input.ExpiresAt) > 0 {
		parsed, parseErr := giftCardWireTime(input.ExpiresAt)
		if parseErr != nil {
			writeLegacyGiftCardError(w, parseErr)
			return
		}
		value.ExpiresAt = parsed
	}
	updated, err := s.store.UpdateGiftCardCode(r.Context(), input.ID, value, s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacyGiftCardCode(updated))
}

func (s *server) legacyExportGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	batch := strings.TrimSpace(r.URL.Query().Get("batch_id"))
	if batch == "" {
		writeLegacyGiftCardError(w, store.ErrInvalidInput)
		return
	}
	first, err := s.store.ListGiftCardCodes(r.Context(), store.GiftCardCodeFilter{Page: 1, PageSize: maxGiftCardListHTTP, BatchNo: batch})
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	if first.Total == 0 {
		writeLegacyGiftCardError(w, store.ErrNotFound)
		return
	}
	if first.Total > maxGiftCardExportRows {
		writeLegacyGiftCardError(w, store.ErrInvalidInput)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gift_cards_`+batch+`.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	for page := 1; int64((page-1)*maxGiftCardListHTTP) < first.Total; page++ {
		items := first.Items
		if page > 1 {
			value, listErr := s.store.ListGiftCardCodes(r.Context(), store.GiftCardCodeFilter{Page: page, PageSize: maxGiftCardListHTTP, BatchNo: batch})
			if listErr != nil {
				return
			}
			items = value.Items
		}
		for _, item := range items {
			_, _ = w.Write([]byte(item.Code + "\n"))
		}
	}
}
func (s *server) legacyDeleteGiftCardCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.DeleteGiftCardCode(r.Context(), input.ID); err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]string{"message": "删除成功"})
}
func (s *server) legacyGiftCardStatistics(w http.ResponseWriter, r *http.Request) {
	value, err := s.store.GiftCardStatistics(r.Context(), s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	typeStats := make([]map[string]any, 0, len(value.TemplateStats))
	for _, item := range value.TemplateStats {
		typeStats = append(typeStats, map[string]any{"template_name": item.TemplateName, "type_name": giftCardTypeNames[int(item.Type)], "count": item.Count})
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"total_stats": map[string]int64{"templates_count": value.TemplateTotal, "active_templates_count": value.ActiveTemplates, "codes_count": value.CodeTotal, "used_codes_count": value.UsedCodes, "usages_count": value.UsageTotal}, "daily_usages": value.DailyUsages, "type_stats": typeStats})
}
func (s *server) legacyGiftCardTypes(w http.ResponseWriter, _ *http.Request) {
	writeLegacySuccess(w, http.StatusOK, giftCardTypeNames)
}

func (s *server) legacyCheckGiftCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	preview, err := s.store.CheckGiftCard(r.Context(), session.UserID, input.Code, s.now())
	if err != nil && !giftCardEligibilityError(err) {
		writeLegacyGiftCardError(w, err)
		return
	}
	reason := ""
	if err != nil {
		reason = giftCardMessage(err)
	}
	codeInfo := legacyGiftCardCodeInfo(preview)
	if preview.Template.Type == store.GiftCardTypePlan && preview.Template.Rewards.PlanID != nil {
		if plan, planErr := s.store.GetPlan(r.Context(), *preview.Template.Rewards.PlanID, s.now()); planErr == nil {
			codeInfo["plan_info"] = legacyOrderPlanResponse(&plan)
		} else if !errors.Is(planErr, store.ErrNotFound) {
			writeLegacyGiftCardError(w, planErr)
			return
		}
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"code_info": codeInfo, "reward_preview": legacyGiftCardReward(preview.Rewards), "can_redeem": err == nil, "reason": nullableLegacyReason(reason)})
}
func (s *server) legacyRedeemGiftCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	usage, err := s.store.RedeemGiftCard(r.Context(), store.RedeemGiftCardInput{UserID: session.UserID, Code: input.Code, UserAgent: r.UserAgent()}, s.now())
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	var inviterRewards any
	if !giftCardRewardIsEmpty(usage.InviterRewards) {
		inviterRewards = legacyGiftCardReward(usage.InviterRewards)
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"message": "兑换成功！", "rewards": legacyGiftCardReward(usage.Rewards), "invite_rewards": inviterRewards, "template_name": usage.TemplateName})
}
func (s *server) legacyGiftCardHistory(w http.ResponseWriter, r *http.Request) {
	page, size, ok := giftCardPage(w, r, 15)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.ListGiftCardUsages(r.Context(), store.GiftCardUsageFilter{Page: page, PageSize: size, UserID: &session.UserID})
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	items := make([]any, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, legacyGiftCardUsage(item, true))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": map[string]any{"current_page": page, "last_page": lastGiftCardPage(value.Total, size), "per_page": size, "total": value.Total}})
}
func (s *server) legacyGiftCardDetail(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeLegacyGiftCardError(w, store.ErrInvalidInput)
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.GetGiftCardUsage(r.Context(), id, session.UserID)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	template, err := s.store.GetGiftCardTemplate(r.Context(), value.TemplateID)
	if err != nil {
		writeLegacyGiftCardError(w, err)
		return
	}
	var inviteUser any
	if value.InviterID != nil {
		inviteUser = map[string]any{"id": *value.InviterID, "email": maskedLegacyEmail(value.InviterEmail)}
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"id": value.ID, "code": value.Code,
		"template":      map[string]any{"name": template.Name, "description": template.Description, "type": template.Type, "type_name": giftCardTypeNames[int(template.Type)], "icon": template.Icon, "theme_color": template.Theme},
		"rewards_given": legacyGiftCardReward(value.Rewards), "invite_rewards": legacyGiftCardOptionalReward(value.InviterRewards), "invite_user": inviteUser,
		"user_level_at_use": value.UserLevelAtUse, "plan_id_at_use": value.UserPlanID, "multiplier_applied": float64(value.Multiplier) / 10_000,
		"notes": value.Notes, "created_at": value.UsedAt.Unix(),
	})
}
func (s *server) legacyUserGiftCardTypes(w http.ResponseWriter, _ *http.Request) {
	writeLegacySuccess(w, http.StatusOK, map[string]any{"types": giftCardTypeNames})
}

func legacyGiftCardTemplate(item store.GiftCardTemplate) map[string]any {
	return map[string]any{
		"id": item.ID, "name": item.Name, "description": item.Description, "type": item.Type,
		"type_name": giftCardTypeNames[int(item.Type)], "status": item.Status, "conditions": item.Conditions,
		"rewards": legacyGiftCardReward(item.Rewards), "limits": legacyGiftCardLimits(item.Limits),
		"special_config": legacyGiftCardSpecial(item.SpecialConfig), "icon": item.Icon,
		"background_image": item.BackgroundImage, "theme_color": item.Theme, "sort": item.SortPosition,
		"admin_id": item.AdminID, "created_at": item.CreatedAt.Unix(), "updated_at": item.UpdatedAt.Unix(),
	}
}

func legacyGiftCardReward(item store.GiftCardReward) map[string]any {
	result := map[string]any{}
	if item.Balance != 0 {
		result["balance"] = item.Balance
	}
	if item.TransferEnable != 0 {
		result["transfer_enable"] = item.TransferEnable
	}
	if item.ExpireDays != 0 {
		result["expire_days"] = item.ExpireDays
	}
	if item.DeviceLimit != 0 {
		result["device_limit"] = item.DeviceLimit
	}
	if item.ResetTraffic {
		result["reset_package"] = true
	}
	if item.PlanID != nil {
		result["plan_id"] = *item.PlanID
	}
	if item.PlanValidityDays != 0 {
		result["plan_validity_days"] = item.PlanValidityDays
	}
	if len(item.RandomRewards) > 0 {
		values := make([]map[string]any, 0, len(item.RandomRewards))
		for _, random := range item.RandomRewards {
			value := legacyGiftCardReward(random.Reward)
			value["weight"] = random.Weight
			values = append(values, value)
		}
		result["random_rewards"] = values
	}
	return result
}

func legacyGiftCardOptionalReward(item store.GiftCardReward) any {
	if giftCardRewardIsEmpty(item) {
		return nil
	}
	return legacyGiftCardReward(item)
}

func giftCardRewardIsEmpty(item store.GiftCardReward) bool {
	return item.Balance == 0 && item.TransferEnable == 0 && item.ExpireDays == 0 && item.DeviceLimit == 0 && !item.ResetTraffic && item.PlanID == nil && item.PlanValidityDays == 0 && len(item.RandomRewards) == 0
}

func legacyGiftCardLimits(item store.GiftCardLimits) map[string]any {
	result := map[string]any{"max_use_per_user": item.MaxUsePerUser}
	if item.CooldownHours != 0 {
		result["cooldown_hours"] = item.CooldownHours
	}
	if item.InviteRewardBasisPoints != 0 {
		result["invite_reward_rate"] = float64(item.InviteRewardBasisPoints) / 10_000
	}
	return result
}

func legacyGiftCardSpecial(item store.GiftCardSpecialConfig) map[string]any {
	result := map[string]any{"festival_bonus": float64(item.FestivalMultiplierBasisPoints) / 10_000}
	if item.StartedAt != nil {
		result["start_time"] = item.StartedAt.Unix()
	}
	if item.EndedAt != nil {
		result["end_time"] = item.EndedAt.Unix()
	}
	return result
}

func legacyGiftCardCode(item store.GiftCardCode) map[string]any {
	var usedAt, expiresAt any
	if item.UsedAt != nil {
		usedAt = item.UsedAt.Unix()
	}
	if item.ExpiresAt != nil {
		expiresAt = item.ExpiresAt.Unix()
	}
	return map[string]any{
		"id": item.ID, "template_id": item.TemplateID, "template_name": item.TemplateName, "code": item.Code,
		"batch_id": item.BatchNo, "status": giftCardStatusToLegacy(item.Status), "status_name": legacyGiftCardStatusName(item.Status),
		"user_id": item.UserID, "user_email": nil, "used_at": usedAt, "expires_at": expiresAt,
		"usage_count": item.UsageCount, "max_usage": item.MaxUsage, "created_at": item.CreatedAt.Unix(),
	}
}

func legacyGiftCardUsage(item store.GiftCardUsage, maskCode bool) map[string]any {
	code := item.Code
	if maskCode {
		code = maskGiftCardCode(code)
	}
	result := map[string]any{
		"id": item.ID, "code": code, "template_name": item.TemplateName, "user_email": item.UserEmail,
		"invite_user_email": maskedLegacyEmail(item.InviterEmail), "rewards_given": legacyGiftCardReward(item.Rewards),
		"invite_rewards": legacyGiftCardOptionalReward(item.InviterRewards), "multiplier_applied": float64(item.Multiplier) / 10_000,
		"created_at": item.UsedAt.Unix(),
	}
	if maskCode {
		result["template_type"] = item.TemplateType
		result["template_type_name"] = giftCardTypeNames[int(item.TemplateType)]
	}
	return result
}

func maskGiftCardCode(value string) string {
	if len(value) > 8 {
		return value[:8] + "****"
	}
	return value
}

func legacyGiftCardCodeInfo(preview store.GiftCardPreview) map[string]any {
	code := legacyGiftCardCode(preview.Code)
	return map[string]any{
		"code":     preview.Code.Code,
		"template": map[string]any{"name": preview.Template.Name, "description": preview.Template.Description, "type": preview.Template.Type, "type_name": giftCardTypeNames[int(preview.Template.Type)], "icon": preview.Template.Icon, "background_image": preview.Template.BackgroundImage, "theme_color": preview.Template.Theme},
		"status":   code["status"], "status_name": code["status_name"], "expires_at": code["expires_at"],
		"usage_count": preview.Code.UsageCount, "max_usage": preview.Code.MaxUsage,
	}
}

func giftCardStatusToLegacy(value store.GiftCardCodeStatus) int {
	switch value {
	case store.GiftCardCodeDisabled:
		return 3
	case store.GiftCardCodeExpired:
		return 2
	default:
		return int(value)
	}
}

func legacyGiftCardStatusToStore(value int) (store.GiftCardCodeStatus, error) {
	switch value {
	case 0:
		return store.GiftCardCodeActive, nil
	case 1:
		return store.GiftCardCodeUsed, nil
	case 2:
		return store.GiftCardCodeExpired, nil
	case 3:
		return store.GiftCardCodeDisabled, nil
	default:
		return 0, store.ErrInvalidInput
	}
}

func legacyGiftCardStatusName(value store.GiftCardCodeStatus) string {
	switch value {
	case store.GiftCardCodeActive:
		return "未使用"
	case store.GiftCardCodeUsed:
		return "已使用"
	case store.GiftCardCodeExpired:
		return "已过期"
	case store.GiftCardCodeDisabled:
		return "已禁用"
	default:
		return "未知状态"
	}
}

func maskedLegacyEmail(value string) any {
	if value == "" {
		return nil
	}
	prefix := value
	if len(prefix) > 3 {
		prefix = prefix[:3]
	}
	return prefix + "***@***"
}

func nullableLegacyReason(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func giftCardPage(w http.ResponseWriter, r *http.Request, defaultSize int) (int, int, bool) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return 0, 0, false
	}
	raw := r.URL.Query().Get("page_size")
	if raw == "" {
		raw = r.URL.Query().Get("per_page")
	}
	size := defaultSize
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxGiftCardListHTTP {
			handleGiftCardError(w, store.ErrInvalidInput)
			return 0, 0, false
		}
		size = value
	}
	return page, size, true
}

const maxGiftCardListHTTP = 200
const maxGiftCardExportRows = 10_000

func lastGiftCardPage(total int64, size int) int64 {
	if total == 0 {
		return 1
	}
	return (total + int64(size) - 1) / int64(size)
}

func giftCardEligibilityError(err error) bool {
	return errors.Is(err, store.ErrGiftCardUserLimit) || errors.Is(err, store.ErrGiftCardCooldown) || errors.Is(err, store.ErrGiftCardCondition) || errors.Is(err, store.ErrGiftCardActivePlan)
}
func giftCardMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrGiftCardUserLimit):
		return "已达到该礼品卡的使用次数限制"
	case errors.Is(err, store.ErrGiftCardCooldown):
		return "礼品卡仍在冷却期"
	case errors.Is(err, store.ErrGiftCardActivePlan):
		return "已有有效套餐，无法使用套餐礼品卡"
	case errors.Is(err, store.ErrGiftCardCondition):
		return "不满足礼品卡使用条件"
	case errors.Is(err, store.ErrGiftCardExpired):
		return "礼品卡已过期"
	case errors.Is(err, store.ErrGiftCardExhausted):
		return "礼品卡已无剩余次数"
	case errors.Is(err, store.ErrGiftCardUnavailable):
		return "礼品卡已禁用"
	default:
		return "礼品卡无效"
	}
}
func handleGiftCardError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "gift_card_invalid"
	if errors.Is(err, store.ErrNotFound) {
		status, code = http.StatusNotFound, "not_found"
	} else if errors.Is(err, store.ErrGiftCardExhausted) || errors.Is(err, store.ErrGiftCardUserLimit) || errors.Is(err, store.ErrGiftCardCooldown) || errors.Is(err, store.ErrGiftCardReferenced) || errors.Is(err, store.ErrConflict) {
		status, code = http.StatusConflict, "gift_card_conflict"
	} else if errors.Is(err, store.ErrInvalidInput) {
		status, code = http.StatusUnprocessableEntity, "validation_failed"
	} else if !errors.Is(err, store.ErrGiftCardUnavailable) && !errors.Is(err, store.ErrGiftCardExpired) && !giftCardEligibilityError(err) {
		handleStoreError(w, err)
		return
	}
	writeAPIError(w, status, code, giftCardMessage(err), nil)
}
func writeLegacyGiftCardError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, store.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeLegacyOrderFail(w, status, giftCardMessage(err))
}

func writeGiftCardCSV(w http.ResponseWriter, codes []store.GiftCardCode) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gift_codes.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"兑换码", "有效期", "最大使用次数", "批次号", "创建时间", "模板名称", "状态"})
	writeGiftCardCSVRows(writer, codes)
	writer.Flush()
}

func writeGiftCardCSVRows(writer *csv.Writer, codes []store.GiftCardCode) {
	for _, code := range codes {
		expires := "长期有效"
		if code.ExpiresAt != nil {
			expires = code.ExpiresAt.Format(time.DateTime)
		}
		_ = writer.Write([]string{code.Code, expires, strconv.Itoa(code.MaxUsage), code.BatchNo, code.CreatedAt.Format(time.DateTime), safeCSVCell(code.TemplateName), strconv.Itoa(int(code.Status))})
	}
}

func (s *server) exportGiftCardCodes(w http.ResponseWriter, r *http.Request) {
	batch := strings.TrimSpace(r.URL.Query().Get("batch_no"))
	if batch == "" {
		batch = strings.TrimSpace(r.URL.Query().Get("batch_id"))
	}
	if batch == "" {
		handleGiftCardError(w, store.ErrInvalidInput)
		return
	}
	first, err := s.store.ListGiftCardCodes(r.Context(), store.GiftCardCodeFilter{Page: 1, PageSize: maxGiftCardListHTTP, BatchNo: batch})
	if err != nil {
		handleGiftCardError(w, err)
		return
	}
	if first.Total > maxGiftCardExportRows {
		handleGiftCardError(w, store.ErrInvalidInput)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gift_codes.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)
	_ = writer.Write([]string{"兑换码", "有效期", "最大使用次数", "批次号", "创建时间", "模板名称", "状态"})
	writeGiftCardCSVRows(writer, first.Items)
	for page := 2; int64((page-1)*maxGiftCardListHTTP) < first.Total; page++ {
		result, listErr := s.store.ListGiftCardCodes(r.Context(), store.GiftCardCodeFilter{Page: page, PageSize: maxGiftCardListHTTP, BatchNo: batch})
		if listErr != nil {
			return
		}
		writeGiftCardCSVRows(writer, result.Items)
	}
	writer.Flush()
}

func clientIP(r *http.Request) string {
	host := strings.TrimSpace(r.RemoteAddr)
	if index := strings.LastIndex(host, ":"); index > -1 {
		host = strings.Trim(host[:index], "[]")
	}
	if len(host) > 45 {
		return ""
	}
	return host
}
