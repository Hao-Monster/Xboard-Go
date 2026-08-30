package httpapi

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type userSubscriptionResponse struct {
	PlanID            *int64      `json:"plan_id"`
	Token             string      `json:"token"`
	ExpiredAt         *time.Time  `json:"expired_at"`
	Upload            int64       `json:"u"`
	Download          int64       `json:"d"`
	TransferEnable    int64       `json:"transfer_enable"`
	Email             string      `json:"email"`
	UUID              string      `json:"uuid"`
	DeviceLimit       int         `json:"device_limit"`
	SpeedLimit        int         `json:"speed_limit"`
	NextResetAt       *time.Time  `json:"next_reset_at"`
	Plan              *store.Plan `json:"plan"`
	SubscribeURL      string      `json:"subscribe_url"`
	ResetDay          *int        `json:"reset_day"`
	SubscriptionValid bool        `json:"subscription_valid"`
}

// legacyUserSubscriptionResponse preserves the scalar and nullability contract
// consumed by the closed-source Xboard frontend. In particular, Laravel emits
// Unix seconds for cast timestamps and omits plan when the account has none.
type legacyUserSubscriptionResponse struct {
	PlanID         *int64              `json:"plan_id"`
	Token          string              `json:"token"`
	ExpiredAt      *int64              `json:"expired_at"`
	Upload         int64               `json:"u"`
	Download       int64               `json:"d"`
	TransferEnable int64               `json:"transfer_enable"`
	Email          string              `json:"email"`
	UUID           string              `json:"uuid"`
	DeviceLimit    int                 `json:"device_limit"`
	SpeedLimit     int                 `json:"speed_limit"`
	NextResetAt    *int64              `json:"next_reset_at"`
	Plan           *legacyPlanResponse `json:"plan,omitempty"`
	SubscribeURL   string              `json:"subscribe_url"`
	ResetDay       *int                `json:"reset_day"`
}

type legacyPlanResponse struct {
	ID                 int64            `json:"id"`
	GroupID            *int64           `json:"group_id"`
	TransferEnableGiB  int64            `json:"transfer_enable"`
	Name               string           `json:"name"`
	SpeedLimit         *int             `json:"speed_limit"`
	Show               bool             `json:"show"`
	SortPosition       int              `json:"sort"`
	Renew              bool             `json:"renew"`
	Content            *string          `json:"content"`
	ResetTrafficMethod *int             `json:"reset_traffic_method"`
	CapacityLimit      *int             `json:"capacity_limit"`
	CreatedAt          int64            `json:"created_at"`
	UpdatedAt          int64            `json:"updated_at"`
	Prices             store.PlanPrices `json:"prices"`
	Sell               int              `json:"sell"`
	DeviceLimit        *int             `json:"device_limit"`
	Tags               []string         `json:"tags"`
}

func (s *server) getUserSubscription(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	response, err := s.userSubscription(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, response)
}

func (s *server) getUserSubscriptionQR(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	response, err := s.userSubscription(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	dataURL, err := clientcatalog.QRDataURL(response.SubscribeURL)
	if err != nil {
		s.logger.Error("encode subscription QR", "user_id", session.UserID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "subscription_qr_failed", "订阅二维码生成失败", nil)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"subscribe_url": response.SubscribeURL, "qr_code": dataURL})
}

func (s *server) legacyGetUserSubscription(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	response, err := s.userSubscription(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacySubscriptionResponse(response))
}

func legacySubscriptionResponse(response userSubscriptionResponse) legacyUserSubscriptionResponse {
	legacy := legacyUserSubscriptionResponse{
		PlanID: response.PlanID, Token: response.Token, ExpiredAt: unixSeconds(response.ExpiredAt),
		Upload: response.Upload, Download: response.Download, TransferEnable: response.TransferEnable,
		Email: response.Email, UUID: response.UUID, DeviceLimit: response.DeviceLimit, SpeedLimit: response.SpeedLimit,
		NextResetAt: unixSeconds(response.NextResetAt), SubscribeURL: response.SubscribeURL, ResetDay: response.ResetDay,
	}
	if response.Plan != nil {
		plan := response.Plan
		var content *string
		if plan.Content != "" {
			value := plan.Content
			content = &value
		}
		prices := plan.Prices
		if len(prices) == 0 {
			prices = nil
		}
		tags := plan.Tags
		if len(tags) == 0 {
			tags = nil
		}
		sell := 0
		if plan.Sell {
			sell = 1
		}
		legacy.Plan = &legacyPlanResponse{
			ID: plan.ID, GroupID: plan.GroupID, TransferEnableGiB: plan.TransferEnableGiB, Name: plan.Name,
			SpeedLimit: plan.SpeedLimit, Show: plan.Show, SortPosition: plan.SortPosition, Renew: plan.Renew,
			Content: content, ResetTrafficMethod: plan.ResetTrafficMethod, CapacityLimit: plan.CapacityLimit,
			CreatedAt: plan.CreatedAt.Unix(), UpdatedAt: plan.UpdatedAt.Unix(), Prices: prices, Sell: sell,
			DeviceLimit: plan.DeviceLimit, Tags: tags,
		}
	}
	return legacy
}

func unixSeconds(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	seconds := value.Unix()
	return &seconds
}

func (s *server) resetUserSubscriptionSecurity(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	response, ok := s.rotateUserSubscriptionSecurity(w, r, session.UserID)
	if !ok {
		return
	}
	writeSuccess(w, http.StatusOK, response)
}

func (s *server) legacyResetUserSubscriptionSecurity(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	response, ok := s.rotateUserSubscriptionSecurity(w, r, session.UserID)
	if !ok {
		return
	}
	writeLegacySuccess(w, http.StatusOK, response.SubscribeURL)
}

func (s *server) rotateUserSubscriptionSecurity(w http.ResponseWriter, r *http.Request, userID int64) (userSubscriptionResponse, bool) {
	if !s.subscriptionResetRequests.allow(r, userID, s.now()) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "subscription_reset_rate_limited", "订阅信息重置过于频繁，请稍后重试", nil)
		return userSubscriptionResponse{}, false
	}
	account, mutation, err := s.store.ResetSubscriptionSecurity(r.Context(), userID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return userSubscriptionResponse{}, false
	}
	if s.hub != nil {
		s.hub.NotifyUserMutation(r.Context(), userID, mutation.PreviousUUID, mutation.GroupID, mutation.GroupID, true)
	}
	response, err := s.userSubscriptionForAccount(r.Context(), account)
	if err != nil {
		handleStoreError(w, err)
		return userSubscriptionResponse{}, false
	}
	return response, true
}

func (s *server) userSubscription(ctx context.Context, userID int64) (userSubscriptionResponse, error) {
	account, err := s.store.GetSubscriptionAccount(ctx, userID)
	if err != nil {
		return userSubscriptionResponse{}, err
	}
	return s.userSubscriptionForAccount(ctx, account)
}

func (s *server) userSubscriptionForAccount(ctx context.Context, account store.SubscriptionAccount) (userSubscriptionResponse, error) {
	config, err := s.store.GetSubscriptionRenderConfig(ctx, "")
	if err != nil {
		return userSubscriptionResponse{}, err
	}
	var plan *store.Plan
	if account.PlanID != nil {
		item, err := s.store.GetPlan(ctx, *account.PlanID, s.now())
		if err != nil {
			return userSubscriptionResponse{}, err
		}
		plan = &item
	}
	subscribeURL, err := s.publicSubscriptionURLFromConfig(config, account.SubscriptionToken, "")
	if err != nil {
		return userSubscriptionResponse{}, err
	}
	return userSubscriptionResponse{
		PlanID: account.PlanID, Token: account.SubscriptionToken, ExpiredAt: account.ExpiredAt,
		Upload: account.TrafficUpload, Download: account.TrafficDownload, TransferEnable: account.TransferEnable,
		Email: account.Email, UUID: account.UUID, DeviceLimit: account.DeviceLimit, SpeedLimit: account.SpeedLimit,
		NextResetAt: account.NextResetAt, Plan: plan, SubscribeURL: subscribeURL,
		ResetDay: resetDay(account.NextResetAt, s.now()), SubscriptionValid: account.AvailableAt(s.now()),
	}, nil
}

func resetDay(next *time.Time, now time.Time) *int {
	if next == nil {
		return nil
	}
	days := 0
	if next.After(now) {
		days = int(math.Ceil(next.Sub(now).Hours() / 24))
	}
	return &days
}
