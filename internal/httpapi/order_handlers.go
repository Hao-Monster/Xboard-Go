package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listUserOrders(w http.ResponseWriter, r *http.Request) {
	status, ok := optionalOrderStatus(w, r, "status")
	if !ok {
		return
	}
	limit, ok := orderQueryInt(w, r, "limit", 100, 200)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	orders, err := s.store.ListUserOrders(r.Context(), session.UserID, status, limit)
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, orders)
}

func (s *server) createOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PlanID     int64  `json:"plan_id"`
		Period     string `json:"period"`
		CouponCode string `json:"coupon_code,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	order, err := s.store.CreateOrder(r.Context(), store.CreateOrderInput{
		UserID: session.UserID, PlanID: input.PlanID, Period: input.Period, CouponCode: input.CouponCode,
	}, s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, order)
}

func (s *server) getUserOrder(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	order, err := s.store.GetUserOrder(r.Context(), session.UserID, r.PathValue("tradeNo"))
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

func (s *server) checkoutUserOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PaymentID int64 `json:"payment_id,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	order, err := s.store.GetUserOrder(r.Context(), session.UserID, r.PathValue("tradeNo"))
	if err != nil {
		handleOrderError(w, err)
		return
	}
	if order.Status == store.OrderStatusCompleted {
		writeSuccess(w, http.StatusOK, order)
		return
	}
	if order.Status != store.OrderStatusPending {
		handleOrderError(w, store.ErrOrderState)
		return
	}
	if order.TotalAmount > 0 {
		checkout, err := s.checkoutPayment(r.Context(), session.UserID, order.TradeNo, input.PaymentID)
		if err != nil {
			handlePaymentCheckoutError(w, err)
			return
		}
		writeSuccess(w, http.StatusOK, checkout)
		return
	}
	order, err = s.store.CompleteOrder(r.Context(), order.TradeNo, order.TradeNo, s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

func (s *server) cancelUserOrder(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	order, err := s.store.CancelOrder(r.Context(), session.UserID, r.PathValue("tradeNo"), s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

func (s *server) listAdminOrders(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 100)
	if !ok {
		return
	}
	status, ok := optionalOrderStatus(w, r, "status")
	if !ok {
		return
	}
	typeFilter, ok := optionalOrderType(w, r, "type")
	if !ok {
		return
	}
	pageResult, err := s.store.ListAdminOrders(r.Context(), store.AdminOrderFilter{
		Page: page, PageSize: pageSize, Status: status, Type: typeFilter,
		Period: r.URL.Query().Get("period"), Query: r.URL.Query().Get("query"),
	})
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, pageResult)
}

func (s *server) getAdminOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.store.GetAdminOrder(r.Context(), r.PathValue("tradeNo"))
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

func (s *server) assignOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		PlanID      int64  `json:"plan_id"`
		Period      string `json:"period"`
		TotalAmount int64  `json:"total_amount"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	order, err := s.store.AssignOrder(r.Context(), store.AssignOrderInput{
		Email: input.Email, PlanID: input.PlanID, Period: input.Period, TotalAmount: input.TotalAmount,
	}, s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, order)
}

func (s *server) paidAdminOrder(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeJSON(w, r, &input) {
		return
	}
	order, err := s.store.CompletePendingOrder(r.Context(), r.PathValue("tradeNo"), "manual_operation", s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

type legacyAdminOrderResponse struct {
	legacyOrderResponse
	User map[string]any `json:"user"`
}

type legacyAdminOrderDetailResponse struct {
	legacyAdminOrderResponse
	InviteUser              map[string]any                 `json:"invite_user"`
	CommissionLog           []any                          `json:"commission_log"`
	SubscribeURL            *string                        `json:"subscribe_url"`
	SubscriptionEntitlement *store.DistributorEntitlement  `json:"subscription_entitlement,omitempty"`
	HWID                    *store.DistributorHWIDSettings `json:"hwid,omitempty"`
}

func legacyAdminOrderResponseOf(order store.AdminOrder) legacyAdminOrderResponse {
	response := legacyOrderResponseOf(order.Order)
	response.Plan = map[string]any{"id": order.PlanID, "name": order.PlanName}
	return legacyAdminOrderResponse{
		legacyOrderResponse: response,
		User:                map[string]any{"id": order.UserID, "email": order.UserEmail},
	}
}

func (s *server) legacyListAdminOrders(w http.ResponseWriter, r *http.Request) {
	filter := store.AdminOrderFilter{Page: 1, PageSize: 10}
	distributorOnly := false
	var distributorUserID *int64
	var distributorSettlementStatus *store.DistributorSettlementStatus
	distributorSearch := ""
	if r.Method == http.MethodGet {
		filter.Page = legacyPositiveInt(r.URL.Query().Get("current"), 1)
		filter.PageSize = legacyPositiveInt(r.URL.Query().Get("pageSize"), 10)
		filter.Query = strings.TrimSpace(r.URL.Query().Get("search"))
		distributorSearch = filter.Query
		distributorOnly, _ = strconv.ParseBool(r.URL.Query().Get("distributor_only"))
		if raw := strings.TrimSpace(r.URL.Query().Get("distributor_user_id")); raw != "" {
			if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
				distributorUserID = &value
			}
		}
		if value, err := strconv.Atoi(r.URL.Query().Get("settlement_status")); err == nil && value >= 0 && value <= 1 {
			status := store.DistributorSettlementStatus(value)
			distributorSettlementStatus = &status
		}
	} else {
		var input map[string]json.RawMessage
		if !decodeJSON(w, r, &input) {
			return
		}
		filter.Page = legacyRawPositiveInt(input["current"], 1)
		filter.PageSize = legacyRawPositiveInt(input["pageSize"], 10)
		_ = json.Unmarshal(input["search"], &filter.Query)
		distributorSearch = strings.TrimSpace(filter.Query)
		_ = json.Unmarshal(input["distributor_only"], &distributorOnly)
		var rawDistributorID int64
		if json.Unmarshal(input["distributor_user_id"], &rawDistributorID) == nil && rawDistributorID > 0 {
			distributorUserID = &rawDistributorID
		}
		var rawSettlement int
		if json.Unmarshal(input["settlement_status"], &rawSettlement) == nil && rawSettlement >= 0 && rawSettlement <= 1 && len(input["settlement_status"]) > 0 && string(input["settlement_status"]) != "null" {
			status := store.DistributorSettlementStatus(rawSettlement)
			distributorSettlementStatus = &status
		}
		var filters []struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		}
		_ = json.Unmarshal(input["filter"], &filters)
		for _, item := range filters {
			value := legacyRawString(item.Value)
			switch item.ID {
			case "status":
				if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 && parsed <= 4 {
					status := store.OrderStatus(parsed)
					filter.Status = &status
				}
			case "type":
				if parsed, err := strconv.Atoi(value); err == nil && parsed >= 1 && parsed <= 4 {
					orderType := store.OrderType(parsed)
					filter.Type = &orderType
				}
			case "period":
				filter.Period = value
			case "trade_no", "user.email", "email":
				filter.Query = value
			}
		}
	}
	maximumSearchLength := 128
	if distributorOnly || distributorUserID != nil || distributorSettlementStatus != nil {
		maximumSearchLength = 512
	}
	if filter.Page < 1 || filter.PageSize < 1 || filter.PageSize > 100 || len(filter.Query) > maximumSearchLength {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "分页或搜索条件格式无效")
		return
	}
	if distributorOnly || distributorUserID != nil || distributorSettlementStatus != nil {
		page, err := s.store.ListDistributorOrders(r.Context(), store.DistributorOrderFilter{
			Page: filter.Page, PageSize: filter.PageSize, DistributorUserID: distributorUserID,
			SettlementStatus: distributorSettlementStatus, Search: distributorSearch, IncludeTokenSearch: true,
		}, s.now())
		if err != nil {
			writeLegacyAdminDistributorError(w, err)
			return
		}
		items := make([]legacyAdminOrderResponse, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, legacyAdminOrderResponse{
				legacyOrderResponse: legacyDistributorOrderResponseOf(item),
				User:                map[string]any{"id": item.Subscription.DistributorUserID, "email": item.DistributorEmail},
			})
		}
		lastPage := int((page.Total + int64(page.PageSize) - 1) / int64(page.PageSize))
		if lastPage < 1 {
			lastPage = 1
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"total": page.Total, "current_page": page.Page, "per_page": page.PageSize, "last_page": lastPage, "data": items,
		})
		return
	}
	page, err := s.store.ListAdminOrders(r.Context(), filter)
	if err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	items := make([]legacyAdminOrderResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, legacyAdminOrderResponseOf(item))
	}
	lastPage := int((page.Total + int64(page.PageSize) - 1) / int64(page.PageSize))
	if lastPage < 1 {
		lastPage = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": page.Total, "current_page": page.Page, "per_page": page.PageSize,
		"last_page": lastPage, "data": items,
	})
}

func (s *server) legacyAssignOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email       string `json:"email"`
		PlanID      int64  `json:"plan_id"`
		Period      string `json:"period"`
		TotalAmount int64  `json:"total_amount"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	order, err := s.store.AssignOrder(r.Context(), store.AssignOrderInput{
		Email: input.Email, PlanID: input.PlanID, Period: input.Period, TotalAmount: input.TotalAmount,
	}, s.now())
	if err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, order.TradeNo)
}

func (s *server) legacyGetAdminOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	order, err := s.store.GetAdminOrderByID(r.Context(), input.ID)
	if err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	plan, err := s.store.GetPlan(r.Context(), order.PlanID, s.now())
	if err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	response := legacyAdminOrderDetailResponse{
		legacyAdminOrderResponse: legacyAdminOrderResponseOf(order),
		CommissionLog:            make([]any, 0),
	}
	if order.DistributorOrderID != nil {
		distributorOrder, distributorErr := s.store.GetDistributorOrderByID(r.Context(), order.ID, s.now())
		if distributorErr != nil {
			writeLegacyAdminDistributorError(w, distributorErr)
			return
		}
		response.legacyOrderResponse = legacyDistributorOrderResponseOf(distributorOrder)
		response.User = map[string]any{"id": distributorOrder.Subscription.DistributorUserID, "email": distributorOrder.DistributorEmail}
		response.SubscriptionEntitlement = &distributorOrder.Entitlement
		hwid, hwidErr := s.store.GetDistributorHWIDSettings(r.Context(), order.ID)
		if hwidErr != nil {
			writeLegacyAdminDistributorError(w, hwidErr)
			return
		}
		response.HWID = &hwid
		config, configErr := s.store.GetSubscriptionRenderConfig(r.Context(), "")
		if configErr != nil {
			writeLegacyAdminDistributorError(w, configErr)
			return
		}
		base := strings.TrimRight(config.AppURL, "/")
		if base == "" {
			base = strings.TrimRight(s.panelURL, "/")
		}
		subscribeURL := base + "/" + config.Path + "/" + distributorOrder.Subscription.SubscriptionToken + "#" + distributorOrder.Subscription.OriginalTradeNo
		response.SubscribeURL = &subscribeURL
		response.Plan = legacyRawPlanResponse(plan)
		writeLegacySuccess(w, http.StatusOK, response)
		return
	}
	response.Plan = legacyRawPlanResponse(plan)
	if order.InviteUserID != nil {
		inviter, findErr := s.store.FindUserByID(r.Context(), *order.InviteUserID)
		if findErr != nil {
			writeLegacyAdminOrderError(w, findErr)
			return
		}
		response.InviteUser = map[string]any{"id": inviter.ID, "email": inviter.Email}
	}
	if order.Status == store.OrderStatusCompleted {
		subscription, subscriptionErr := s.userSubscription(r.Context(), order.UserID)
		if subscriptionErr != nil {
			writeLegacyAdminOrderError(w, subscriptionErr)
			return
		}
		response.SubscribeURL = &subscription.SubscribeURL
	}
	writeLegacySuccess(w, http.StatusOK, response)
}

func legacyRawPlanResponse(plan store.Plan) map[string]any {
	prices := make(map[string]json.Number, len(plan.Prices))
	for period, cents := range plan.Prices {
		whole, fraction := cents/100, cents%100
		encoded := strconv.FormatInt(whole, 10)
		if fraction != 0 {
			encoded += "." + strings.TrimRight(fmt.Sprintf("%02d", fraction), "0")
		}
		prices[period] = json.Number(encoded)
	}
	return map[string]any{
		"id": plan.ID, "group_id": plan.GroupID, "transfer_enable": plan.TransferEnableGiB,
		"name": plan.Name, "speed_limit": plan.SpeedLimit, "show": plan.Show, "sort": plan.SortPosition,
		"renew": plan.Renew, "content": plan.Content, "prices": prices,
		"reset_traffic_method": plan.ResetTrafficMethod, "capacity_limit": plan.CapacityLimit,
		"sell": plan.Sell, "device_limit": plan.DeviceLimit, "tags": plan.Tags,
		"created_at": plan.CreatedAt.Unix(), "updated_at": plan.UpdatedAt.Unix(),
	}
}

func (s *server) legacyPaidAdminOrder(w http.ResponseWriter, r *http.Request) {
	tradeNo, ok := legacyAdminTradeNo(w, r)
	if !ok {
		return
	}
	if _, err := s.store.CompletePendingOrder(r.Context(), tradeNo, "manual_operation", s.now()); err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyCancelAdminOrder(w http.ResponseWriter, r *http.Request) {
	tradeNo, ok := legacyAdminTradeNo(w, r)
	if !ok {
		return
	}
	if _, err := s.store.CancelAdminOrder(r.Context(), tradeNo, s.now()); err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func legacyAdminTradeNo(w http.ResponseWriter, r *http.Request) (string, bool) {
	var input struct {
		TradeNo string `json:"trade_no"`
	}
	if !decodeJSON(w, r, &input) {
		return "", false
	}
	return input.TradeNo, true
}

func writeLegacyAdminOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusBadRequest, "订单、用户或订阅不存在")
	case errors.Is(err, store.ErrActiveOrderExists):
		writeLegacyOrderFail(w, http.StatusBadRequest, "该用户还有待支付的订单，无法分配")
	case errors.Is(err, store.ErrOrderState):
		writeLegacyOrderFail(w, http.StatusBadRequest, "只能对待支付的订单进行操作")
	case errors.Is(err, store.ErrInvalidInput), errors.Is(err, store.ErrPlanUnavailable):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "订单参数格式无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "更新失败")
	}
}

func legacyPositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func legacyRawPositiveInt(value json.RawMessage, fallback int) int {
	var number int
	if len(value) == 0 || json.Unmarshal(value, &number) != nil || number < 1 {
		return fallback
	}
	return number
}

func legacyRawString(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	var list []json.RawMessage
	if json.Unmarshal(value, &list) == nil && len(list) == 1 {
		return legacyRawString(list[0])
	}
	return ""
}

func (s *server) cancelAdminOrder(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if !decodeJSON(w, r, &input) {
		return
	}
	order, err := s.store.CancelAdminOrder(r.Context(), r.PathValue("tradeNo"), s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, order)
}

func (s *server) legacyListUserOrders(w http.ResponseWriter, r *http.Request) {
	status, ok := optionalOrderStatus(w, r, "status")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if session.IsDistributor {
		settlementStatus, ok := optionalDistributorSettlementStatus(w, r.URL.Query().Get("settlement_status"))
		if !ok {
			return
		}
		page, err := s.store.ListDistributorOrders(r.Context(), store.DistributorOrderFilter{
			Page: 1, PageSize: 200, DistributorUserID: &session.UserID, Status: status,
			SettlementStatus: settlementStatus, Search: r.URL.Query().Get("search"),
		}, s.now())
		if err != nil {
			writeLegacyDistributorError(w, err)
			return
		}
		data := make([]legacyOrderResponse, 0, len(page.Items))
		for _, order := range page.Items {
			data = append(data, legacyDistributorOrderResponseOf(order))
		}
		writeLegacySuccess(w, http.StatusOK, data)
		return
	}
	orders, err := s.store.ListUserOrders(r.Context(), session.UserID, status, 200)
	if err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	data := make([]legacyOrderResponse, 0, len(orders))
	for _, order := range orders {
		data = append(data, legacyOrderResponseOf(order))
	}
	writeLegacySuccess(w, http.StatusOK, data)
}

func (s *server) legacyCreateOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PlanID       int64   `json:"plan_id"`
		Period       string  `json:"period"`
		CouponCode   string  `json:"coupon_code,omitempty"`
		CustomerName *string `json:"customer_name,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	if session.IsDistributor {
		if strings.TrimSpace(input.CouponCode) != "" {
			writeLegacyOrderFail(w, http.StatusBadRequest, "分销订单不支持优惠券或折扣")
			return
		}
		order, err := s.store.CreateDistributorOrder(r.Context(), store.CreateDistributorOrderInput{
			DistributorUserID: session.UserID, PlanID: input.PlanID, Period: input.Period, CustomerName: input.CustomerName,
		}, s.now())
		if err != nil {
			writeLegacyDistributorError(w, err)
			return
		}
		writeLegacySuccess(w, http.StatusOK, order.Order.TradeNo)
		return
	}
	order, err := s.store.CreateOrder(r.Context(), store.CreateOrderInput{UserID: session.UserID, PlanID: input.PlanID, Period: input.Period, CouponCode: input.CouponCode}, s.now())
	if err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, order.TradeNo)
}

func (s *server) legacyGetUserOrder(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.IsDistributor {
		order, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, r.URL.Query().Get("trade_no"), s.now())
		if err != nil {
			writeLegacyDistributorError(w, err)
			return
		}
		response := legacyDistributorOrderResponseOf(order)
		tryOutPlanID := 0
		response.TryOutPlanID = &tryOutPlanID
		var payment map[string]any
		response.Payment = &payment
		writeLegacySuccess(w, http.StatusOK, response)
		return
	}
	order, err := s.store.GetUserOrder(r.Context(), session.UserID, r.URL.Query().Get("trade_no"))
	if err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	response := legacyOrderResponseOf(order)
	tryOutPlanID := 0
	response.TryOutPlanID = &tryOutPlanID
	var payment map[string]any
	response.Payment = &payment
	writeLegacySuccess(w, http.StatusOK, response)
}

func (s *server) legacyCheckUserOrder(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if session.IsDistributor {
		order, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, r.URL.Query().Get("trade_no"), s.now())
		if err != nil {
			writeLegacyOrderFail(w, http.StatusForbidden, "分销商账号不能访问普通订单功能")
			return
		}
		writeLegacySuccess(w, http.StatusOK, order.Order.Status)
		return
	}
	order, err := s.store.GetUserOrder(r.Context(), session.UserID, r.URL.Query().Get("trade_no"))
	if err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, order.Status)
}

func (s *server) legacyCheckoutUserOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TradeNo string `json:"trade_no"`
		Method  *int64 `json:"method,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	if session.IsDistributor {
		order, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, input.TradeNo, s.now())
		if err != nil || order.Order.Status != store.OrderStatusCompleted {
			writeLegacyOrderFail(w, http.StatusForbidden, "分销商账号不能访问普通订单功能")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": -1, "data": true})
		return
	}
	order, err := s.store.GetUserOrder(r.Context(), session.UserID, input.TradeNo)
	if err != nil || order.Status != store.OrderStatusPending {
		writeLegacyOrderFail(w, http.StatusBadRequest, "订单不存在或已支付")
		return
	}
	if order.TotalAmount > 0 {
		if input.Method == nil {
			writeLegacyOrderFail(w, http.StatusBadRequest, "支付方式不可用")
			return
		}
		checkout, err := s.checkoutPayment(r.Context(), session.UserID, order.TradeNo, *input.Method)
		if err != nil {
			writeLegacyPaymentCheckoutError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"type": checkout.Type, "data": checkout.Data})
		return
	}
	if _, err := s.store.CompleteOrder(r.Context(), order.TradeNo, order.TradeNo, s.now()); err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"type": -1, "data": true})
}

func (s *server) legacyCancelUserOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TradeNo string `json:"trade_no"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	if session.IsDistributor {
		writeLegacyOrderFail(w, http.StatusForbidden, "分销商账号不能访问普通订单功能")
		return
	}
	if _, err := s.store.CancelOrder(r.Context(), session.UserID, input.TradeNo, s.now()); err != nil {
		writeLegacyOrderStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

type legacyOrderResponse struct {
	ID                         int64                              `json:"id"`
	UserID                     int64                              `json:"user_id"`
	PlanID                     int64                              `json:"plan_id"`
	PaymentID                  *int64                             `json:"payment_id"`
	Period                     string                             `json:"period"`
	TradeNo                    string                             `json:"trade_no"`
	OriginalAmount             int64                              `json:"original_amount"`
	TotalAmount                int64                              `json:"total_amount"`
	HandlingAmount             *int64                             `json:"handling_amount"`
	BalanceAmount              int64                              `json:"balance_amount"`
	SurplusCredit              int64                              `json:"surplus_credit"`
	SurplusAmount              int64                              `json:"surplus_amount"`
	Type                       store.OrderType                    `json:"type"`
	Status                     store.OrderStatus                  `json:"status"`
	SurplusOrderIDs            []int64                            `json:"surplus_order_ids"`
	CouponID                   *int64                             `json:"coupon_id"`
	CommissionStatus           *int                               `json:"commission_status"`
	InviteUserID               *int64                             `json:"invite_user_id"`
	ActualCommissionBalance    *int64                             `json:"actual_commission_balance"`
	CommissionBalance          int64                              `json:"commission_balance"`
	DiscountAmount             int64                              `json:"discount_amount"`
	PaidAt                     *int64                             `json:"paid_at"`
	CallbackNo                 *string                            `json:"callback_no"`
	EntitlementExpiredAtBefore *int64                             `json:"entitlement_expired_at_before"`
	EntitlementExpiredAtAfter  *int64                             `json:"entitlement_expired_at_after"`
	CreatedAt                  int64                              `json:"created_at"`
	UpdatedAt                  int64                              `json:"updated_at"`
	TryOutPlanID               *int                               `json:"try_out_plan_id,omitempty"`
	Payment                    *map[string]any                    `json:"payment,omitempty"`
	IsDistributorOrder         bool                               `json:"is_distributor_order"`
	IsSubscriptionOrigin       bool                               `json:"is_subscription_origin"`
	OrderTypeLabel             string                             `json:"order_type_label"`
	CanViewSubscriptionQR      bool                               `json:"can_view_subscription_qr"`
	CanRenew                   bool                               `json:"can_renew"`
	SubscriptionTradeNo        *string                            `json:"subscription_trade_no,omitempty"`
	CustomerName               *string                            `json:"customer_name"`
	Remark                     *string                            `json:"remark"`
	PaymentLabel               *string                            `json:"payment_label,omitempty"`
	DeliveryStatus             *store.DistributorDeliveryStatus   `json:"delivery_status,omitempty"`
	SettlementStatus           *store.DistributorSettlementStatus `json:"settlement_status,omitempty"`
	SettledAt                  *int64                             `json:"settled_at,omitempty"`
	ConfigIssuedAt             *int64                             `json:"config_issued_at,omitempty"`
	ConnectedAt                *int64                             `json:"connected_at,omitempty"`
	ConnectedNodeID            *int64                             `json:"connected_node_id,omitempty"`
	ConnectedNodeName          *string                            `json:"connected_node_name,omitempty"`
	ClaimedAt                  *int64                             `json:"claimed_at,omitempty"`
	ClosedAt                   *int64                             `json:"closed_at,omitempty"`
	HWIDEnabled                *bool                              `json:"hwid_enabled,omitempty"`
	HWIDLimit                  *int                               `json:"hwid_limit,omitempty"`
	BoundDevices               []string                           `json:"bound_devices,omitempty"`
	SubscriptionEntitlement    *store.DistributorEntitlement      `json:"subscription_entitlement,omitempty"`
	Plan                       map[string]any                     `json:"plan,omitempty"`
	DistributorEmail           *string                            `json:"distributor_email,omitempty"`
	DistributorName            *string                            `json:"distributor_name,omitempty"`
}

func legacyOrderResponseOf(order store.Order) legacyOrderResponse {
	response := legacyOrderResponse{
		ID: order.ID, UserID: order.UserID, PlanID: order.PlanID, PaymentID: order.PaymentID,
		Period: legacyOrderPeriod(order.Period), TradeNo: order.TradeNo, OriginalAmount: order.OriginalAmount, TotalAmount: order.TotalAmount,
		HandlingAmount: order.HandlingAmount, BalanceAmount: order.BalanceAmount, SurplusCredit: order.SurplusCredit,
		SurplusAmount: order.SurplusAmount, Type: order.Type, Status: order.Status,
		SurplusOrderIDs: order.SurplusOrderIDs, CouponID: order.CouponID, CommissionStatus: order.CommissionStatus,
		InviteUserID: order.InviteUserID, ActualCommissionBalance: order.ActualCommissionBalance,
		CommissionBalance: order.CommissionBalance, DiscountAmount: order.DiscountAmount,
		PaidAt: unixPointer(order.PaidAt), CallbackNo: stringPointer(order.CallbackNo),
		EntitlementExpiredAtBefore: unixPointer(order.EntitlementExpiredAtBefore),
		EntitlementExpiredAtAfter:  unixPointer(order.EntitlementExpiredAtAfter),
		CreatedAt:                  order.CreatedAt.Unix(), UpdatedAt: order.UpdatedAt.Unix(), OrderTypeLabel: orderTypeLabel(order.Type),
	}
	if order.Plan != nil {
		response.Plan = legacyOrderPlanResponse(order.Plan)
	}
	return response
}

func legacyDistributorOrderResponseOf(value store.DistributorOrder) legacyOrderResponse {
	response := legacyOrderResponseOf(value.Order)
	response.IsDistributorOrder = true
	response.IsSubscriptionOrigin = value.IsSubscriptionOrigin
	response.CanViewSubscriptionQR = value.CanViewSubscriptionQR
	response.CanRenew = value.CanRenew
	response.SubscriptionTradeNo = &value.Subscription.OriginalTradeNo
	response.CustomerName = value.Subscription.CustomerName
	response.Remark = value.Subscription.Remark
	paymentLabel := "分销免支付"
	response.PaymentLabel = &paymentLabel
	deliveryStatus := value.Subscription.DeliveryStatus
	response.DeliveryStatus = &deliveryStatus
	settlementStatus := value.SettlementStatus
	response.SettlementStatus = &settlementStatus
	response.SettledAt = unixPointer(value.Order.PaidAt)
	response.ConfigIssuedAt = unixPointer(value.Subscription.ConfigIssuedAt)
	response.ConnectedAt = unixPointer(value.Subscription.ConnectedAt)
	response.ConnectedNodeID = value.Subscription.ConnectedNodeID
	response.ConnectedNodeName = value.Subscription.ConnectedNodeName
	response.ClaimedAt = unixPointer(value.Subscription.ClaimedAt)
	response.ClosedAt = unixPointer(value.Subscription.ClosedAt)
	response.HWIDEnabled = &value.Subscription.HWIDEnabled
	response.HWIDLimit = &value.Subscription.HWIDLimit
	response.BoundDevices = value.BoundDevices
	response.SubscriptionEntitlement = &value.Entitlement
	response.Plan = map[string]any{"id": value.Order.PlanID, "name": value.PlanName}
	response.DistributorEmail = &value.DistributorEmail
	response.DistributorName = &value.DistributorName
	return response
}

func optionalDistributorSettlementStatus(w http.ResponseWriter, raw string) (*store.DistributorSettlementStatus, bool) {
	if raw == "" {
		return nil, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > 1 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "结算状态格式无效")
		return nil, false
	}
	status := store.DistributorSettlementStatus(value)
	return &status, true
}

func legacyOrderPlanResponse(plan *store.Plan) map[string]any {
	response := map[string]any{
		"id": plan.ID, "group_id": plan.GroupID, "name": plan.Name, "tags": plan.Tags, "content": plan.Content,
		"capacity_limit": plan.CapacityLimit, "transfer_enable": plan.TransferEnableGiB,
		"speed_limit": plan.SpeedLimit, "device_limit": plan.DeviceLimit, "show": plan.Show, "sell": plan.Sell,
		"renew": plan.Renew, "reset_traffic_method": plan.ResetTrafficMethod, "sort": plan.SortPosition,
		"created_at": plan.CreatedAt.Unix(), "updated_at": plan.UpdatedAt.Unix(),
	}
	for legacy, current := range map[string]string{
		"month_price": "monthly", "quarter_price": "quarterly", "half_year_price": "half_yearly",
		"year_price": "yearly", "two_year_price": "two_yearly", "three_year_price": "three_yearly",
		"onetime_price": "onetime", "reset_price": "reset_traffic",
	} {
		if price, exists := plan.Prices[current]; exists {
			response[legacy] = price
		} else {
			response[legacy] = nil
		}
	}
	return response
}

func handleOrderError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCouponInvalid), errors.Is(err, store.ErrCouponNotStarted), errors.Is(err, store.ErrCouponExpired),
		errors.Is(err, store.ErrCouponExhausted), errors.Is(err, store.ErrCouponPlanRestricted),
		errors.Is(err, store.ErrCouponPeriodRestricted), errors.Is(err, store.ErrCouponUserLimit):
		handleCouponError(w, err)
	case errors.Is(err, store.ErrActiveOrderExists):
		writeAPIError(w, http.StatusConflict, "active_order_exists", "存在待支付或开通中的订单，请先处理该订单", nil)
	case errors.Is(err, store.ErrPaymentInProgress):
		writeAPIError(w, http.StatusConflict, "payment_in_progress", "支付订单已创建，不能取消；请完成支付或等待订单超时", nil)
	case errors.Is(err, store.ErrOrderState):
		writeAPIError(w, http.StatusConflict, "order_state_conflict", "当前订单状态不允许此操作", nil)
	case errors.Is(err, store.ErrPlanUnavailable):
		writeAPIError(w, http.StatusUnprocessableEntity, "plan_unavailable", "该套餐或购买周期当前不可用", nil)
	default:
		handleStoreError(w, err)
	}
}

func writeLegacyOrderStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCouponInvalid), errors.Is(err, store.ErrCouponNotStarted), errors.Is(err, store.ErrCouponExpired),
		errors.Is(err, store.ErrCouponExhausted), errors.Is(err, store.ErrCouponPlanRestricted),
		errors.Is(err, store.ErrCouponPeriodRestricted), errors.Is(err, store.ErrCouponUserLimit):
		writeLegacyCouponError(w, err)
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusBadRequest, "订单不存在")
	case errors.Is(err, store.ErrActiveOrderExists):
		writeLegacyOrderFail(w, http.StatusBadRequest, "您有未支付或开通中的订单，请稍后重试或取消订单")
	case errors.Is(err, store.ErrPaymentInProgress):
		writeLegacyOrderFail(w, http.StatusBadRequest, "支付订单已创建，不能取消，请完成支付或等待订单超时")
	case errors.Is(err, store.ErrOrderState):
		writeLegacyOrderFail(w, http.StatusBadRequest, "只能取消待支付的订单")
	case errors.Is(err, store.ErrPlanUnavailable), errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusBadRequest, "套餐或购买周期不可用")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "请求失败，请稍后重试")
	}
}

func writeLegacyOrderFail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"status": "fail", "message": message, "data": nil, "error": nil})
}

func (s *server) allowOrderMutation(w http.ResponseWriter, r *http.Request, userID int64) bool {
	if s.orderRequests.allow(r, userID, s.now()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeAPIError(w, http.StatusTooManyRequests, "order_rate_limited", "订单操作过于频繁，请稍后重试", nil)
	return false
}

func optionalOrderStatus(w http.ResponseWriter, r *http.Request, name string) (*store.OrderStatus, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < int(store.OrderStatusPending) || value > int(store.OrderStatusDiscounted) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 格式无效", nil)
		return nil, false
	}
	result := store.OrderStatus(value)
	return &result, true
}

func optionalOrderType(w http.ResponseWriter, r *http.Request, name string) (*store.OrderType, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < int(store.OrderTypeNew) || value > int(store.OrderTypeResetTraffic) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 格式无效", nil)
		return nil, false
	}
	result := store.OrderType(value)
	return &result, true
}

func orderQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback, maximum int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 超出允许范围", nil)
		return 0, false
	}
	return value, true
}

func legacyOrderPeriod(period string) string {
	for legacy, current := range map[string]string{
		"month_price": "monthly", "quarter_price": "quarterly", "half_year_price": "half_yearly",
		"year_price": "yearly", "two_year_price": "two_yearly", "three_year_price": "three_yearly",
		"onetime_price": "onetime", "reset_price": "reset_traffic",
	} {
		if period == current {
			return legacy
		}
	}
	return period
}

func orderTypeLabel(value store.OrderType) string {
	switch value {
	case store.OrderTypeRenewal:
		return "续费"
	case store.OrderTypeUpgrade:
		return "升级"
	case store.OrderTypeResetTraffic:
		return "流量重置"
	default:
		return "新购"
	}
}

func unixPointer(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	result := value.Unix()
	return &result
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
