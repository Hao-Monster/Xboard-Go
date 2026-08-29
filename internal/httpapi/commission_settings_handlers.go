package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type legacyConfigInt int

func (value *legacyConfigInt) UnmarshalJSON(data []byte) error {
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*value = legacyConfigInt(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.Atoi(text)
	if err != nil || strconv.Itoa(parsed) != text {
		return errors.New("legacy config integer must be a canonical base-10 integer")
	}
	*value = legacyConfigInt(parsed)
	return nil
}

func legacyConfigIntPointer(value *legacyConfigInt) *int {
	if value == nil {
		return nil
	}
	result := int(*value)
	return &result
}

type commissionSettingsRequest struct {
	Revision            *int64 `json:"revision,omitempty"`
	InviteCommission    *int   `json:"invite_commission"`
	FirstTimeEnabled    *bool  `json:"commission_first_time_enable"`
	AutoCheckEnabled    *bool  `json:"commission_auto_check_enable"`
	WithdrawClosed      *bool  `json:"withdraw_close_enable"`
	DistributionEnabled *bool  `json:"commission_distribution_enable"`
	DistributionL1      *int   `json:"commission_distribution_l1"`
	DistributionL2      *int   `json:"commission_distribution_l2"`
	DistributionL3      *int   `json:"commission_distribution_l3"`
}

type legacyConfigSaveRequest struct {
	InviteCommission    *int  `json:"invite_commission"`
	FirstTimeEnabled    *bool `json:"commission_first_time_enable"`
	AutoCheckEnabled    *bool `json:"commission_auto_check_enable"`
	WithdrawClosed      *bool `json:"withdraw_close_enable"`
	DistributionEnabled *bool `json:"commission_distribution_enable"`
	DistributionL1      *int  `json:"commission_distribution_l1"`
	DistributionL2      *int  `json:"commission_distribution_l2"`
	DistributionL3      *int  `json:"commission_distribution_l3"`

	PlanChangeEnabled    *bool            `json:"plan_change_enable"`
	ResetTrafficMethod   *legacyConfigInt `json:"reset_traffic_method"`
	SurplusEnabled       *bool            `json:"surplus_enable"`
	NewOrderEventID      *legacyConfigInt `json:"new_order_event_id"`
	RenewOrderEventID    *legacyConfigInt `json:"renew_order_event_id"`
	ChangeOrderEventID   *legacyConfigInt `json:"change_order_event_id"`
	ShowInfo             *bool            `json:"show_info_to_server_enable"`
	ShowProtocol         *bool            `json:"show_protocol_to_server_enable"`
	DefaultRemindExpire  *bool            `json:"default_remind_expire"`
	DefaultRemindTraffic *bool            `json:"default_remind_traffic"`
	Path                 *string          `json:"subscribe_path"`
}

func (input legacyConfigSaveRequest) hasInvite() bool {
	return input.InviteCommission != nil || input.FirstTimeEnabled != nil || input.AutoCheckEnabled != nil ||
		input.WithdrawClosed != nil || input.DistributionEnabled != nil || input.DistributionL1 != nil ||
		input.DistributionL2 != nil || input.DistributionL3 != nil
}

func (input legacyConfigSaveRequest) completeInvite() bool {
	return input.InviteCommission != nil && input.FirstTimeEnabled != nil && input.AutoCheckEnabled != nil &&
		input.WithdrawClosed != nil && input.DistributionEnabled != nil && input.DistributionL1 != nil &&
		input.DistributionL2 != nil && input.DistributionL3 != nil
}

func (input legacyConfigSaveRequest) hasSubscribe() bool {
	return input.PlanChangeEnabled != nil || input.ResetTrafficMethod != nil || input.SurplusEnabled != nil ||
		input.NewOrderEventID != nil || input.RenewOrderEventID != nil || input.ChangeOrderEventID != nil ||
		input.ShowInfo != nil || input.ShowProtocol != nil || input.DefaultRemindExpire != nil ||
		input.DefaultRemindTraffic != nil || input.Path != nil
}

func (input commissionSettingsRequest) complete() bool {
	return input.InviteCommission != nil && input.FirstTimeEnabled != nil && input.AutoCheckEnabled != nil &&
		input.WithdrawClosed != nil && input.DistributionEnabled != nil && input.DistributionL1 != nil &&
		input.DistributionL2 != nil && input.DistributionL3 != nil
}

func (input commissionSettingsRequest) storeInput() store.SaveCommissionSettingsInput {
	return store.SaveCommissionSettingsInput{
		InviteCommission: *input.InviteCommission, FirstTimeEnabled: *input.FirstTimeEnabled,
		AutoCheckEnabled: *input.AutoCheckEnabled, WithdrawClosed: *input.WithdrawClosed,
		DistributionEnabled: *input.DistributionEnabled, DistributionL1: *input.DistributionL1,
		DistributionL2: *input.DistributionL2, DistributionL3: *input.DistributionL3,
	}
}

func (s *server) getCommissionSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateCommissionSettings(w http.ResponseWriter, r *http.Request) {
	var input commissionSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision == nil || !input.complete() {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请完整填写佣金设置", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateCommissionSettings(r.Context(), session.UserID, *input.Revision, input.storeInput(), s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) legacyFetchConfigSettings(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimSpace(r.URL.Query().Get("key")) {
	case "invite":
		settings, err := s.store.GetCommissionSettings(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"invite": legacyCommissionSettings(settings)})
	case "subscribe":
		settings, err := s.store.GetLegacyAdminSubscriptionConfig(r.Context())
		if err != nil {
			writeLegacyInviteFailure(w, http.StatusInternalServerError, "订阅配置读取失败")
			return
		}
		writeLegacySuccess(w, http.StatusOK, map[string]any{"subscribe": settings})
	default:
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "不支持的配置组")
	}
}

func (s *server) legacySaveConfigSettings(w http.ResponseWriter, r *http.Request) {
	var input legacyConfigSaveRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	invite, subscribe := input.hasInvite(), input.hasSubscribe()
	if invite == subscribe {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "请提交单一配置组")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if subscribe {
		_, err := s.store.UpdateLegacyAdminSubscriptionConfig(r.Context(), session.UserID, store.SaveLegacyAdminSubscriptionConfigInput{
			PlanChangeEnabled: input.PlanChangeEnabled, ResetTrafficMethod: legacyConfigIntPointer(input.ResetTrafficMethod),
			SurplusEnabled: input.SurplusEnabled, NewOrderEventID: legacyConfigIntPointer(input.NewOrderEventID),
			RenewOrderEventID: legacyConfigIntPointer(input.RenewOrderEventID), ChangeOrderEventID: legacyConfigIntPointer(input.ChangeOrderEventID),
			ShowInfo: input.ShowInfo, ShowProtocol: input.ShowProtocol,
			DefaultRemindExpire: input.DefaultRemindExpire, DefaultRemindTraffic: input.DefaultRemindTraffic,
			Path: input.Path,
		}, s.now())
		if err != nil {
			status, message := http.StatusInternalServerError, "订阅配置保存失败"
			if errors.Is(err, store.ErrInvalidInput) {
				status, message = http.StatusUnprocessableEntity, "订阅配置无效"
			}
			writeLegacyInviteFailure(w, status, message)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	if !input.completeInvite() {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "请完整填写邀请佣金配置")
		return
	}
	commissionInput := commissionSettingsRequest{
		InviteCommission: input.InviteCommission, FirstTimeEnabled: input.FirstTimeEnabled,
		AutoCheckEnabled: input.AutoCheckEnabled, WithdrawClosed: input.WithdrawClosed,
		DistributionEnabled: input.DistributionEnabled, DistributionL1: input.DistributionL1,
		DistributionL2: input.DistributionL2, DistributionL3: input.DistributionL3,
	}
	current, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
		return
	}
	if _, err := s.store.UpdateCommissionSettings(r.Context(), session.UserID, current.Revision, commissionInput.storeInput(), s.now()); err != nil {
		status := http.StatusUnprocessableEntity
		message := "邀请佣金配置无效"
		if errors.Is(err, store.ErrConflict) {
			status = http.StatusConflict
			message = "配置已被其他管理员修改，请重试"
		} else if !errors.Is(err, store.ErrInvalidInput) {
			status = http.StatusInternalServerError
			message = "邀请佣金配置保存失败"
		}
		writeLegacyInviteFailure(w, status, message)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func legacyCommissionSettings(settings store.CommissionSettings) map[string]any {
	return map[string]any{
		"invite_commission":              settings.InviteCommission,
		"commission_first_time_enable":   settings.FirstTimeEnabled,
		"commission_auto_check_enable":   settings.AutoCheckEnabled,
		"withdraw_close_enable":          settings.WithdrawClosed,
		"commission_distribution_enable": settings.DistributionEnabled,
		"commission_distribution_l1":     settings.DistributionL1,
		"commission_distribution_l2":     settings.DistributionL2,
		"commission_distribution_l3":     settings.DistributionL3,
	}
}
