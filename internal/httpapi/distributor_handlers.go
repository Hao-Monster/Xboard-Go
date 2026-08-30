package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listAdminDistributorOptions(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListDistributorOptions(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, users)
}

func (s *server) listDistributorOrders(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 200)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	filter := store.DistributorOrderFilter{
		Page: page, PageSize: pageSize, DistributorUserID: &session.UserID, Search: r.URL.Query().Get("search"),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("settlement_status")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "settlement_status 格式无效", nil)
			return
		}
		status := store.DistributorSettlementStatus(value)
		filter.SettlementStatus = &status
	}
	result, err := s.store.ListDistributorOrders(r.Context(), filter, s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) createDistributorOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PlanID       int64   `json:"plan_id"`
		Period       string  `json:"period"`
		CustomerName *string `json:"customer_name,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	value, err := s.store.CreateDistributorOrder(r.Context(), store.CreateDistributorOrderInput{
		DistributorUserID: session.UserID, PlanID: input.PlanID, Period: input.Period, CustomerName: input.CustomerName,
	}, s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	value, err = s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, value.Order.TradeNo, s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, value)
}

func (s *server) getDistributorOrder(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, r.PathValue("tradeNo"), s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) renewDistributorOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Period         string `json:"period"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowOrderMutation(w, r, session.UserID) {
		return
	}
	value, err := s.store.RenewDistributorOrder(r.Context(), store.RenewDistributorOrderInput{
		DistributorUserID: session.UserID, TradeNo: r.PathValue("tradeNo"), Period: input.Period, IdempotencyKey: input.IdempotencyKey,
	}, s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	value, err = s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, value.Order.TradeNo, s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) getDistributorOrderQR(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, r.PathValue("tradeNo"), s.now())
	if err != nil {
		handleModernDistributorError(w, err)
		return
	}
	if !value.IsSubscriptionOrigin {
		writeAPIError(w, http.StatusConflict, "subscription_origin_required", "请从原始订阅订单查看二维码", nil)
		return
	}
	qrCode, err := s.distributorSubscriptionQR(r, value)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"trade_no": value.Subscription.OriginalTradeNo, "customer_name": value.Subscription.CustomerName,
		"qr_code": qrCode, "hwid_enabled": value.Subscription.HWIDEnabled, "hwid_devices": value.BoundDevices,
	})
}

func handleModernDistributorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "distributor_order_not_found", "分销订单不存在", nil)
	case errors.Is(err, store.ErrDistributorUnavailable):
		writeAPIError(w, http.StatusForbidden, "distributor_unavailable", "当前分销商账号不可用", nil)
	case errors.Is(err, store.ErrDistributorSubscriptionClosed), errors.Is(err, store.ErrDistributorRenewalUnavailable):
		writeAPIError(w, http.StatusConflict, "distributor_renewal_unavailable", "该分销订阅不能续费", nil)
	case errors.Is(err, store.ErrDistributorRenewalMismatch):
		writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "幂等标识已用于其他续费请求", nil)
	case errors.Is(err, store.ErrPlanUnavailable), errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "套餐、周期或请求参数无效", nil)
	default:
		handleStoreError(w, err)
	}
}

func (s *server) listAdminDistributorOrders(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 200)
	if !ok {
		return
	}
	filter := store.DistributorOrderFilter{
		Page: page, PageSize: pageSize, Search: r.URL.Query().Get("search"), IncludeTokenSearch: true,
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("distributor_user_id")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "distributor_user_id 必须是正整数", nil)
			return
		}
		filter.DistributorUserID = &value
	}
	settlementStatus, ok := optionalDistributorSettlementStatus(w, r.URL.Query().Get("settlement_status"))
	if !ok {
		return
	}
	filter.SettlementStatus = settlementStatus
	result, err := s.store.ListDistributorOrders(r.Context(), filter, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) getAdminDistributorOrder(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	value, err := s.store.GetDistributorOrderByID(r.Context(), orderID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	hwid, err := s.store.GetDistributorHWIDSettings(r.Context(), orderID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	subscribeURL, err := s.distributorSubscriptionURL(r, value)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"order": value, "hwid": hwid, "subscribe_url": subscribeURL})
}

func (s *server) updateAdminDistributorRemark(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	var input struct {
		Remark *string `json:"remark"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.store.UpdateDistributorRemark(r.Context(), orderID, input.Remark, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"order_id": orderID, "remark": value})
}

func (s *server) updateAdminDistributorEntitlement(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	var input struct {
		TransferEnable int64      `json:"transfer_enable"`
		ExpiredAt      *time.Time `json:"expired_at"`
		SpeedLimit     int        `json:"speed_limit"`
		DeviceLimit    int        `json:"device_limit"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.store.UpdateDistributorEntitlement(r.Context(), orderID, store.UpdateDistributorEntitlementInput{
		TransferEnable: input.TransferEnable, ExpiredAt: input.ExpiredAt, SpeedLimit: input.SpeedLimit, DeviceLimit: input.DeviceLimit,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) updateAdminDistributorHWID(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	var input struct {
		Enabled bool `json:"enabled"`
		Limit   int  `json:"limit"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.store.UpdateDistributorHWIDSettings(r.Context(), orderID, input.Enabled, input.Limit, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) listAdminDistributorHWIDDevices(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	devices, err := s.store.ListDistributorHWIDDevices(r.Context(), orderID, r.URL.Query().Get("search"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, devices)
}

func (s *server) deleteAdminDistributorHWIDDevice(w http.ResponseWriter, r *http.Request) {
	orderID, ok := pathID(w, r, "orderID")
	if !ok {
		return
	}
	deviceID, ok := pathID(w, r, "deviceID")
	if !ok {
		return
	}
	deleted, err := s.store.DeleteDistributorHWIDDevice(r.Context(), orderID, deviceID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !deleted {
		handleStoreError(w, store.ErrNotFound)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) previewAdminDistributorSettlement(w http.ResponseWriter, r *http.Request) {
	distributorID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	value, err := s.store.PreviewDistributorSettlement(r.Context(), distributorID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) settleAdminDistributorOrders(w http.ResponseWriter, r *http.Request) {
	distributorID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct{}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.SettleDistributorOrders(r.Context(), distributorID, session.UserID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, value)
}

func (s *server) legacyAdminDistributorOptions(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListDistributorOptions(r.Context())
	if err != nil {
		writeLegacyAdminOrderError(w, err)
		return
	}
	options := make([]map[string]any, 0, len(users))
	for _, user := range users {
		options = append(options, map[string]any{
			"id": user.ID, "email": user.Email, "distributor_name": user.DistributorName, "banned": user.Banned,
		})
	}
	writeLegacySuccess(w, http.StatusOK, options)
}

func legacySettlementSummary(value store.DistributorSettlementSummary) map[string]any {
	return map[string]any{
		"count": value.Count, "total_amount": value.TotalAmount,
		"total_amount_yuan": float64(value.TotalAmount) / 100, "settled_at": unixPointer(value.SettledAt),
	}
}

func (s *server) legacyAdminDistributorSettlementPreview(w http.ResponseWriter, r *http.Request) {
	distributorID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("distributor_user_id")), 10, 64)
	if err != nil || distributorID < 1 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "请选择有效的分销商")
		return
	}
	value, err := s.store.PreviewDistributorSettlement(r.Context(), distributorID)
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacySettlementSummary(value))
}

func (s *server) legacyAdminSettleDistributorOrders(w http.ResponseWriter, r *http.Request) {
	var input struct {
		DistributorUserID int64 `json:"distributor_user_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.SettleDistributorOrders(r.Context(), input.DistributorUserID, session.UserID, s.now())
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, legacySettlementSummary(value))
}

func (s *server) legacyAdminUpdateDistributorRemark(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrderID int64   `json:"order_id"`
		Remark  *string `json:"remark"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	remark, err := s.store.UpdateDistributorRemark(r.Context(), input.OrderID, input.Remark, s.now())
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	value, err := s.store.GetDistributorOrderByID(r.Context(), input.OrderID, s.now())
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"order_id": input.OrderID, "subscription_trade_no": value.Subscription.OriginalTradeNo, "remark": remark,
	})
}

func (s *server) legacyAdminUpdateDistributorEntitlement(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrderID        int64  `json:"order_id"`
		TransferEnable int64  `json:"transfer_enable"`
		ExpiredAt      *int64 `json:"expired_at"`
		SpeedLimit     int    `json:"speed_limit"`
		DeviceLimit    int    `json:"device_limit"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	var expiredAt *time.Time
	if input.ExpiredAt != nil {
		value := time.Unix(*input.ExpiredAt, 0).UTC()
		expiredAt = &value
	}
	value, err := s.store.UpdateDistributorEntitlement(r.Context(), input.OrderID, store.UpdateDistributorEntitlementInput{
		TransferEnable: input.TransferEnable, ExpiredAt: expiredAt, SpeedLimit: input.SpeedLimit, DeviceLimit: input.DeviceLimit,
	}, s.now())
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"plan_id": value.PlanID, "plan_name": value.PlanName, "transfer_enable": value.TransferEnable,
		"used_traffic": value.UsedTraffic, "remaining_traffic": value.RemainingTraffic,
		"expired_at": unixPointer(value.ExpiredAt), "speed_limit": value.SpeedLimit, "device_limit": value.DeviceLimit,
	})
}

func (s *server) legacyAdminUpdateDistributorHWID(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrderID int64 `json:"order_id"`
		Enabled bool  `json:"enabled"`
		Limit   int   `json:"limit"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	value, err := s.store.UpdateDistributorHWIDSettings(r.Context(), input.OrderID, input.Enabled, input.Limit, s.now())
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, value)
}

func (s *server) legacyAdminDistributorHWIDDevices(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("order_id")), 10, 64)
	if err != nil || orderID < 1 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "订单编号无效")
		return
	}
	devices, err := s.store.ListDistributorHWIDDevices(r.Context(), orderID, r.URL.Query().Get("search"))
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, devices)
}

func (s *server) legacyAdminDeleteDistributorHWIDDevice(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OrderID  int64 `json:"order_id"`
		DeviceID int64 `json:"device_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	deleted, err := s.store.DeleteDistributorHWIDDevice(r.Context(), input.OrderID, input.DeviceID)
	if err != nil {
		writeLegacyAdminDistributorError(w, err)
		return
	}
	if !deleted {
		writeLegacyOrderFail(w, http.StatusNotFound, "HWID 设备不存在")
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func writeLegacyAdminDistributorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrDistributorUnavailable):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "分销商或分销订单不存在")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "分销订单参数格式无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "更新失败")
	}
}

func (s *server) legacyRenewDistributorOrder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TradeNo        string `json:"trade_no"`
		Period         string `json:"period"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	order, err := s.store.RenewDistributorOrder(r.Context(), store.RenewDistributorOrderInput{
		DistributorUserID: session.UserID, TradeNo: input.TradeNo, Period: input.Period, IdempotencyKey: input.IdempotencyKey,
	}, s.now())
	if err != nil {
		writeLegacyDistributorError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"trade_no": order.Order.TradeNo, "subscription_trade_no": order.Subscription.OriginalTradeNo,
		"period": legacyOrderPeriod(order.Order.Period), "total_amount": order.Order.TotalAmount,
		"expired_at_before": unixPointer(order.Order.EntitlementExpiredAtBefore),
		"expired_at_after":  unixPointer(order.Order.EntitlementExpiredAtAfter),
		"settlement_status": store.DistributorSettlementUnsettled,
	})
}

func (s *server) legacyDistributorDelivery(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	tradeNo := strings.TrimSpace(r.URL.Query().Get("trade_no"))
	var value store.DistributorOrder
	var err error
	if tradeNo != "" {
		value, err = s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, tradeNo, s.now())
	} else {
		page, listErr := s.store.ListDistributorOrders(r.Context(), store.DistributorOrderFilter{
			Page: 1, PageSize: 200, DistributorUserID: &session.UserID,
		}, s.now())
		if listErr != nil {
			err = listErr
		} else {
			err = store.ErrNotFound
			for _, candidate := range page.Items {
				if candidate.IsSubscriptionOrigin && (candidate.Subscription.DeliveryStatus == store.DistributorDeliveryPending ||
					candidate.Subscription.DeliveryStatus == store.DistributorDeliveryClaimed && candidate.Subscription.ConfigIssuedAt == nil) {
					value, err = candidate, nil
					break
				}
			}
		}
	}
	if err != nil {
		writeLegacyDistributorError(w, err)
		return
	}
	response, err := s.distributorDeliveryResponse(r, value, value.Subscription.DeliveryStatus == store.DistributorDeliveryPending)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, response)
}

func (s *server) legacyDistributorSubscriptionQR(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.GetDistributorOrderByTradeNo(r.Context(), session.UserID, r.URL.Query().Get("trade_no"), s.now())
	if err != nil {
		writeLegacyDistributorError(w, err)
		return
	}
	if !value.IsSubscriptionOrigin || value.Subscription.SubscriptionToken == "" {
		writeLegacyOrderFail(w, http.StatusConflict, "订阅尚未生成")
		return
	}
	qrCode, err := s.distributorSubscriptionQR(r, value)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"trade_no": value.Subscription.OriginalTradeNo, "customer_name": value.Subscription.CustomerName,
		"qr_code": qrCode, "hwid_enabled": value.Subscription.HWIDEnabled, "hwid_devices": value.BoundDevices,
	})
}

func (s *server) legacyCloseDistributorDelivery(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TradeNo string `json:"trade_no"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if !input.Confirm {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "请确认关闭交付记录")
		return
	}
	session, _ := sessionFromContext(r.Context())
	value, err := s.store.CloseDistributorDelivery(r.Context(), session.UserID, input.TradeNo, s.now())
	if err != nil {
		writeLegacyDistributorError(w, err)
		return
	}
	response, err := s.distributorDeliveryResponse(r, value, false)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, response)
}

func (s *server) distributorDeliveryResponse(r *http.Request, value store.DistributorOrder, includeQR bool) (map[string]any, error) {
	response := map[string]any{
		"trade_no": value.Subscription.OriginalTradeNo, "customer_name": value.Subscription.CustomerName,
		"plan_id": value.Order.PlanID, "plan_name": value.PlanName, "period": legacyOrderPeriod(value.Order.Period),
		"delivery_status": value.Subscription.DeliveryStatus, "settlement_status": value.Subscription.SettlementStatus,
		"config_issued_at": unixPointer(value.Subscription.ConfigIssuedAt), "connected_at": unixPointer(value.Subscription.ConnectedAt),
		"connected_node_id": value.Subscription.ConnectedNodeID, "connected_node_name": value.Subscription.ConnectedNodeName,
		"claimed_at": unixPointer(value.Subscription.ClaimedAt), "closed_at": unixPointer(value.Subscription.ClosedAt),
		"hwid_enabled": value.Subscription.HWIDEnabled, "hwid_limit": value.Subscription.HWIDLimit,
		"hwid_devices": value.BoundDevices, "can_open": value.Subscription.DeliveryStatus == store.DistributorDeliveryPending,
	}
	if includeQR {
		qrCode, err := s.distributorSubscriptionQR(r, value)
		if err != nil {
			return nil, err
		}
		response["qr_code"] = qrCode
	}
	return response, nil
}

func (s *server) distributorSubscriptionQR(r *http.Request, value store.DistributorOrder) (string, error) {
	subscribeURL, err := s.distributorSubscriptionURL(r, value)
	if err != nil {
		return "", err
	}
	return clientcatalog.QRDataURL(subscribeURL)
}

func (s *server) distributorSubscriptionURL(r *http.Request, value store.DistributorOrder) (string, error) {
	config, err := s.store.GetSubscriptionRenderConfig(r.Context(), "")
	if err != nil {
		return "", err
	}
	return s.publicSubscriptionURLFromConfig(config, value.Subscription.SubscriptionToken, value.Subscription.OriginalTradeNo)
}

func (s *server) claimDistributorSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if subscriptionPrefetch(r) {
		w.WriteHeader(http.StatusTooEarly)
		return
	}
	claim, err := s.store.ClaimDistributorSubscription(r.Context(), r.PathValue("claimToken"), requestIP(r), r.UserAgent(), s.now())
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Invalid claim token", http.StatusNotFound)
		return
	}
	if errors.Is(err, store.ErrDistributorClaimConsumed) {
		http.Error(w, "This subscription QR code has already been used or closed.", http.StatusGone)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	config, err := s.store.GetSubscriptionRenderConfig(r.Context(), "")
	if err != nil {
		handleStoreError(w, err)
		return
	}
	targetURL, err := s.publicSubscriptionURLFromConfig(config, claim.SubscriptionToken, claim.OriginalTradeNo)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	target, err := url.Parse(targetURL)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	query := r.URL.Query()
	query.Del("token")
	target.RawQuery = query.Encode()
	w.Header().Set("Pragma", "no-cache")
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func writeLegacyDistributorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusNotFound, "分销订单不存在")
	case errors.Is(err, store.ErrDistributorUnavailable):
		writeLegacyOrderFail(w, http.StatusForbidden, "当前分销商账号不可用")
	case errors.Is(err, store.ErrDistributorSubscriptionClosed), errors.Is(err, store.ErrDistributorRenewalUnavailable):
		writeLegacyOrderFail(w, http.StatusConflict, "该分销订阅不能续费")
	case errors.Is(err, store.ErrDistributorRenewalMismatch):
		writeLegacyOrderFail(w, http.StatusConflict, "续费请求标识已用于其他续费操作")
	case errors.Is(err, store.ErrPlanUnavailable), errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "套餐、周期或请求参数无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "请求失败，请稍后重试")
	}
}
