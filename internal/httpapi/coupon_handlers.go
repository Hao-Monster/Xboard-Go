package httpapi

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type couponSaveRequest struct {
	Code             string           `json:"code"`
	Name             string           `json:"name"`
	Type             store.CouponType `json:"type"`
	Value            int64            `json:"value"`
	Show             *bool            `json:"show"`
	LimitUse         *int             `json:"limit_use"`
	LimitUseWithUser *int             `json:"limit_use_with_user"`
	LimitPlanIDs     []int64          `json:"limit_plan_ids"`
	LimitPeriods     []string         `json:"limit_period"`
	StartedAt        int64            `json:"started_at"`
	EndedAt          int64            `json:"ended_at"`
}

func (input couponSaveRequest) storeInput(defaultShow bool) store.SaveCouponInput {
	show := defaultShow
	if input.Show != nil {
		show = *input.Show
	}
	return store.SaveCouponInput{
		Code: input.Code, Name: input.Name, Type: input.Type, Value: input.Value, Show: show,
		LimitUse: input.LimitUse, LimitUseWithUser: input.LimitUseWithUser,
		LimitPlanIDs: input.LimitPlanIDs, LimitPeriods: input.LimitPeriods,
		StartedAt: time.Unix(input.StartedAt, 0).UTC(), EndedAt: time.Unix(input.EndedAt, 0).UTC(),
	}
}

func (s *server) checkUserCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code   string `json:"code"`
		PlanID int64  `json:"plan_id"`
		Period string `json:"period"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	quote, err := s.store.CheckCoupon(r.Context(), store.CouponCheckInput{
		UserID: session.UserID, PlanID: input.PlanID, Period: input.Period, Code: input.Code,
	}, s.now())
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, quote)
}

func (s *server) listAdminCoupons(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 200)
	if !ok {
		return
	}
	filter := store.CouponFilter{Page: page, PageSize: pageSize, Query: r.URL.Query().Get("query"), Sort: strings.TrimSpace(r.URL.Query().Get("sort"))}
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "优惠券类型格式无效", nil)
			return
		}
		typeValue := store.CouponType(value)
		filter.Type = &typeValue
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("show")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "启用状态格式无效", nil)
			return
		}
		filter.Show = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("desc")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "排序方向格式无效", nil)
			return
		}
		filter.Desc = value
	}
	result, err := s.store.ListCoupons(r.Context(), filter)
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) createAdminCoupon(w http.ResponseWriter, r *http.Request) {
	var input couponSaveRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	var coupon store.Coupon
	var err error
	if strings.TrimSpace(input.Code) == "" {
		coupons, createErr := s.store.CreateCouponBatch(r.Context(), input.storeInput(false), 1, s.now())
		err = createErr
		if createErr == nil {
			coupon = coupons[0]
		}
	} else {
		coupon, err = s.store.CreateCoupon(r.Context(), input.storeInput(false), s.now())
	}
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, coupon)
}

func (s *server) updateAdminCoupon(w http.ResponseWriter, r *http.Request) {
	couponID, ok := pathID(w, r, "couponID")
	if !ok {
		return
	}
	var input couponSaveRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetCoupon(r.Context(), couponID)
	if err != nil {
		handleCouponError(w, err)
		return
	}
	if input.Show == nil {
		input.Show = &current.Show
	}
	if strings.TrimSpace(input.Code) == "" {
		input.Code = current.Code
	}
	coupon, err := s.store.UpdateCoupon(r.Context(), couponID, input.storeInput(current.Show), s.now())
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, coupon)
}

func (s *server) setAdminCouponVisibility(w http.ResponseWriter, r *http.Request) {
	couponID, ok := pathID(w, r, "couponID")
	if !ok {
		return
	}
	var input struct {
		Show *bool `json:"show"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Show == nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "启用状态不能为空", nil)
		return
	}
	coupon, err := s.store.SetCouponVisibility(r.Context(), couponID, *input.Show, s.now())
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, coupon)
}

func (s *server) deleteAdminCoupon(w http.ResponseWriter, r *http.Request) {
	couponID, ok := pathID(w, r, "couponID")
	if !ok {
		return
	}
	if err := s.store.DeleteCoupon(r.Context(), couponID); err != nil {
		handleCouponError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) batchAdminCoupons(w http.ResponseWriter, r *http.Request) {
	var input struct {
		couponSaveRequest
		Count int `json:"count"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	base := input.couponSaveRequest.storeInput(true)
	base.Code = ""
	coupons, err := s.store.CreateCouponBatch(r.Context(), base, input.Count, s.now())
	if err != nil {
		handleCouponError(w, err)
		return
	}
	writeCouponCSV(w, coupons)
}

func (s *server) legacyCheckUserCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code   string `json:"code"`
		PlanID int64  `json:"plan_id"`
		Period string `json:"period"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Code) == "" {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "优惠券不能为空")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	quote, err := s.store.CheckCoupon(r.Context(), store.CouponCheckInput{
		UserID: session.UserID, PlanID: input.PlanID, Period: input.Period, Code: input.Code,
	}, s.now())
	if err != nil {
		writeLegacyCouponError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacyUserCouponResponseOf(quote.Coupon))
}

func (s *server) legacyListAdminCoupons(w http.ResponseWriter, r *http.Request) {
	filter := store.CouponFilter{Page: 1, PageSize: 10}
	if r.Method == http.MethodGet {
		if raw := r.URL.Query().Get("current"); raw != "" {
			filter.Page, _ = strconv.Atoi(raw)
		}
		if raw := r.URL.Query().Get("pageSize"); raw != "" {
			filter.PageSize, _ = strconv.Atoi(raw)
		}
	} else {
		var input struct {
			Current  int `json:"current"`
			PageSize int `json:"pageSize"`
			Filter   []struct {
				ID    string          `json:"id"`
				Value json.RawMessage `json:"value"`
			} `json:"filter"`
			Sort []struct {
				ID   string `json:"id"`
				Desc bool   `json:"desc"`
			} `json:"sort"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.Current != 0 {
			filter.Page = input.Current
		}
		if input.PageSize != 0 {
			filter.PageSize = input.PageSize
		}
		for _, item := range input.Filter {
			switch item.ID {
			case "name", "code":
				_ = json.Unmarshal(item.Value, &filter.Query)
			case "type":
				var value int
				if json.Unmarshal(item.Value, &value) == nil {
					typeValue := store.CouponType(value)
					filter.Type = &typeValue
				}
			case "show":
				var value bool
				if json.Unmarshal(item.Value, &value) == nil {
					filter.Show = &value
				}
			}
		}
		if len(input.Sort) > 0 {
			filter.Sort = input.Sort[0].ID
			filter.Desc = input.Sort[0].Desc
		}
	}
	page, err := s.store.ListCoupons(r.Context(), filter)
	if err != nil {
		writeLegacyAdminCouponError(w, err)
		return
	}
	items := make([]legacyAdminCouponResponse, 0, len(page.Items))
	for _, coupon := range page.Items {
		items = append(items, legacyAdminCouponResponseOf(coupon))
	}
	lastPage := int((page.Total + int64(page.PageSize) - 1) / int64(page.PageSize))
	if lastPage < 1 {
		lastPage = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": page.Total, "current_page": page.Page, "per_page": page.PageSize, "last_page": lastPage, "data": items,
	})
}

func (s *server) legacyGenerateAdminCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		couponSaveRequest
		ID            int64 `json:"id"`
		GenerateCount int   `json:"generate_count"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.GenerateCount > 0 {
		base := input.couponSaveRequest.storeInput(true)
		base.Code = ""
		coupons, err := s.store.CreateCouponBatch(r.Context(), base, input.GenerateCount, s.now())
		if err != nil {
			writeLegacyAdminCouponError(w, err)
			return
		}
		writeCouponCSV(w, coupons)
		return
	}
	if input.ID > 0 {
		current, err := s.store.GetCoupon(r.Context(), input.ID)
		if err != nil {
			writeLegacyAdminCouponError(w, err)
			return
		}
		if strings.TrimSpace(input.Code) == "" {
			input.Code = current.Code
		}
		input.Show = &current.Show
		if _, err := s.store.UpdateCoupon(r.Context(), input.ID, input.couponSaveRequest.storeInput(current.Show), s.now()); err != nil {
			writeLegacyAdminCouponError(w, err)
			return
		}
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	var err error
	if strings.TrimSpace(input.Code) == "" {
		_, err = s.store.CreateCouponBatch(r.Context(), input.couponSaveRequest.storeInput(false), 1, s.now())
	} else {
		_, err = s.store.CreateCoupon(r.Context(), input.couponSaveRequest.storeInput(false), s.now())
	}
	if err != nil {
		writeLegacyAdminCouponError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyToggleAdminCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	coupon, err := s.store.GetCoupon(r.Context(), input.ID)
	if err == nil {
		_, err = s.store.SetCouponVisibility(r.Context(), coupon.ID, !coupon.Show, s.now())
	}
	if err != nil {
		writeLegacyAdminCouponError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyUpdateAdminCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID   int64 `json:"id"`
		Show *bool `json:"show"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Show == nil {
		writeLegacyAdminCouponError(w, store.ErrInvalidInput)
		return
	}
	if _, err := s.store.SetCouponVisibility(r.Context(), input.ID, *input.Show, s.now()); err != nil {
		writeLegacyAdminCouponError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *server) legacyDeleteAdminCoupon(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.DeleteCoupon(r.Context(), input.ID); err != nil {
		writeLegacyAdminCouponError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

type legacyAdminCouponResponse struct {
	ID               int64            `json:"id"`
	Code             string           `json:"code"`
	Name             string           `json:"name"`
	Type             store.CouponType `json:"type"`
	Value            int64            `json:"value"`
	Show             bool             `json:"show"`
	LimitUse         *int             `json:"limit_use"`
	LimitUseWithUser *int             `json:"limit_use_with_user"`
	LimitPlanIDs     any              `json:"limit_plan_ids"`
	LimitPeriods     any              `json:"limit_period"`
	StartedAt        int64            `json:"started_at"`
	EndedAt          int64            `json:"ended_at"`
	CreatedAt        int64            `json:"created_at"`
	UpdatedAt        int64            `json:"updated_at"`
}

func legacyAdminCouponResponseOf(coupon store.Coupon) legacyAdminCouponResponse {
	plans := any(coupon.LimitPlanIDs)
	if len(coupon.LimitPlanIDs) == 0 {
		plans = nil
	}
	periods := any(coupon.LimitPeriods)
	return legacyAdminCouponResponse{
		ID: coupon.ID, Code: coupon.Code, Name: coupon.Name, Type: coupon.Type, Value: coupon.Value, Show: coupon.Show,
		LimitUse: coupon.LimitUse, LimitUseWithUser: coupon.LimitUseWithUser, LimitPlanIDs: plans, LimitPeriods: periods,
		StartedAt: coupon.StartedAt.Unix(), EndedAt: coupon.EndedAt.Unix(), CreatedAt: coupon.CreatedAt.Unix(), UpdatedAt: coupon.UpdatedAt.Unix(),
	}
}

func legacyUserCouponResponseOf(coupon store.Coupon) legacyAdminCouponResponse {
	response := legacyAdminCouponResponseOf(coupon)
	if len(coupon.LimitPlanIDs) > 0 {
		values := make([]string, len(coupon.LimitPlanIDs))
		for index, value := range coupon.LimitPlanIDs {
			values[index] = strconv.FormatInt(value, 10)
		}
		response.LimitPlanIDs = values
	}
	if len(coupon.LimitPeriods) == 0 {
		response.LimitPeriods = nil
	} else {
		values := make([]string, len(coupon.LimitPeriods))
		for index, value := range coupon.LimitPeriods {
			values[index] = legacyOrderPeriod(value)
		}
		response.LimitPeriods = values
	}
	return response
}

func writeCouponCSV(w http.ResponseWriter, coupons []store.Coupon) {
	var buffer bytes.Buffer
	buffer.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"名称", "类型", "金额或比例", "开始时间", "结束时间", "可用次数", "可用于订阅", "券码", "生成时间"})
	for _, coupon := range coupons {
		typeName, value := "金额", formatCouponCents(coupon.Value)
		if coupon.Type == store.CouponTypePercentage {
			typeName, value = "比例", strconv.FormatInt(coupon.Value, 10)
		}
		limit := "不限制"
		if coupon.LimitUse != nil {
			limit = strconv.Itoa(*coupon.LimitUse)
		}
		plans := "不限制"
		if len(coupon.LimitPlanIDs) > 0 {
			values := make([]string, len(coupon.LimitPlanIDs))
			for index, planID := range coupon.LimitPlanIDs {
				values[index] = strconv.FormatInt(planID, 10)
			}
			plans = strings.Join(values, "/")
		}
		_ = writer.Write([]string{
			safeCSVCell(coupon.Name), typeName, value, coupon.StartedAt.Format(time.DateTime), coupon.EndedAt.Format(time.DateTime),
			limit, plans, safeCSVCell(coupon.Code), coupon.CreatedAt.Format(time.DateTime),
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "csv_failed", "优惠券文件生成失败", nil)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="coupons.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer.Bytes())
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimSpace(value)
	first, _ := utf8.DecodeRuneInString(trimmed)
	if strings.ContainsRune("=+-@", first) {
		return "'" + value
	}
	return value
}

func formatCouponCents(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

func handleCouponError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCouponInvalid):
		writeAPIError(w, http.StatusBadRequest, "coupon_invalid", "优惠券无效", nil)
	case errors.Is(err, store.ErrCouponNotStarted):
		writeAPIError(w, http.StatusBadRequest, "coupon_not_started", "优惠券还未到可用时间", nil)
	case errors.Is(err, store.ErrCouponExpired):
		writeAPIError(w, http.StatusBadRequest, "coupon_expired", "优惠券已过期", nil)
	case errors.Is(err, store.ErrCouponExhausted):
		writeAPIError(w, http.StatusConflict, "coupon_exhausted", "优惠券已无剩余次数", nil)
	case errors.Is(err, store.ErrCouponPlanRestricted):
		writeAPIError(w, http.StatusBadRequest, "coupon_plan_restricted", "该订阅无法使用此优惠券", nil)
	case errors.Is(err, store.ErrCouponPeriodRestricted):
		writeAPIError(w, http.StatusBadRequest, "coupon_period_restricted", "该付款周期无法使用此优惠券", nil)
	case errors.Is(err, store.ErrCouponUserLimit):
		writeAPIError(w, http.StatusConflict, "coupon_user_limit", "已达到每个用户可使用次数", nil)
	case errors.Is(err, store.ErrCouponReferenced):
		writeAPIError(w, http.StatusConflict, "coupon_referenced", "优惠券已被订单使用，只能禁用，不能删除", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "coupon_conflict", "优惠券码已存在", nil)
	default:
		handleStoreError(w, err)
	}
}

func writeLegacyCouponError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCouponNotStarted):
		writeLegacyOrderFail(w, http.StatusBadRequest, "优惠券还未到可用时间")
	case errors.Is(err, store.ErrCouponExpired):
		writeLegacyOrderFail(w, http.StatusBadRequest, "优惠券已过期")
	case errors.Is(err, store.ErrCouponExhausted):
		writeLegacyOrderFail(w, http.StatusBadRequest, "此优惠券已不可用")
	case errors.Is(err, store.ErrCouponPlanRestricted):
		writeLegacyOrderFail(w, http.StatusBadRequest, "该订阅无法使用此优惠码")
	case errors.Is(err, store.ErrCouponPeriodRestricted):
		writeLegacyOrderFail(w, http.StatusBadRequest, "此优惠券无法用于该付款周期")
	case errors.Is(err, store.ErrCouponUserLimit):
		writeLegacyOrderFail(w, http.StatusBadRequest, "此优惠券已达到每人使用次数限制")
	case errors.Is(err, store.ErrCouponInvalid):
		writeLegacyOrderFail(w, http.StatusBadRequest, "优惠券无效")
	default:
		writeLegacyOrderStoreError(w, err)
	}
}

func writeLegacyAdminCouponError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusBadRequest, "优惠券不存在")
	case errors.Is(err, store.ErrCouponReferenced):
		writeLegacyOrderFail(w, http.StatusConflict, "优惠券已被订单使用，只能禁用")
	case errors.Is(err, store.ErrConflict):
		writeLegacyOrderFail(w, http.StatusConflict, "优惠券码已存在")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "优惠券参数格式无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "优惠券操作失败")
	}
}
