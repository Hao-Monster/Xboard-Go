package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

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

func (s *server) legacyFetchCommissionSettings(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("key")) != "invite" {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "仅支持 invite 配置组")
		return
	}
	settings, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{"invite": legacyCommissionSettings(settings)})
}

func (s *server) legacySaveCommissionSettings(w http.ResponseWriter, r *http.Request) {
	var input commissionSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision != nil || !input.complete() {
		writeLegacyInviteFailure(w, http.StatusUnprocessableEntity, "请完整填写邀请佣金配置")
		return
	}
	current, err := s.store.GetCommissionSettings(r.Context())
	if err != nil {
		writeLegacyInviteFailure(w, http.StatusInternalServerError, "邀请佣金配置读取失败")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.UpdateCommissionSettings(r.Context(), session.UserID, current.Revision, input.storeInput(), s.now()); err != nil {
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
