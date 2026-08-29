package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type subscriptionPolicySettingsRequest struct {
	Revision             *int64 `json:"revision"`
	PlanChangeEnabled    *bool  `json:"plan_change_enable"`
	ResetTrafficMethod   *int   `json:"reset_traffic_method"`
	SurplusEnabled       *bool  `json:"surplus_enable"`
	NewOrderEventID      *int   `json:"new_order_event_id"`
	RenewOrderEventID    *int   `json:"renew_order_event_id"`
	ChangeOrderEventID   *int   `json:"change_order_event_id"`
	DefaultRemindExpire  *bool  `json:"default_remind_expire"`
	DefaultRemindTraffic *bool  `json:"default_remind_traffic"`
}

func (input subscriptionPolicySettingsRequest) complete() bool {
	return input.Revision != nil && input.PlanChangeEnabled != nil && input.ResetTrafficMethod != nil &&
		input.SurplusEnabled != nil && input.NewOrderEventID != nil && input.RenewOrderEventID != nil &&
		input.ChangeOrderEventID != nil && input.DefaultRemindExpire != nil && input.DefaultRemindTraffic != nil
}

func (s *server) getSubscriptionPolicySettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSubscriptionPolicySettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateSubscriptionPolicySettings(w http.ResponseWriter, r *http.Request) {
	var input subscriptionPolicySettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.complete() {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请完整填写订阅策略", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateSubscriptionPolicySettings(r.Context(), session.UserID, *input.Revision, store.SaveSubscriptionPolicySettingsInput{
		PlanChangeEnabled: *input.PlanChangeEnabled, ResetTrafficMethod: *input.ResetTrafficMethod,
		SurplusEnabled: *input.SurplusEnabled, NewOrderEventID: *input.NewOrderEventID,
		RenewOrderEventID: *input.RenewOrderEventID, ChangeOrderEventID: *input.ChangeOrderEventID,
		DefaultRemindExpire: *input.DefaultRemindExpire, DefaultRemindTraffic: *input.DefaultRemindTraffic,
	}, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrInvalidInput) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "订阅策略参数无效", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}
