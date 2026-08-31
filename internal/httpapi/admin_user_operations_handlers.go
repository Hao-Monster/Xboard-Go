package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
)

func (s *server) getAdminUserSubscriptionURL(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	token, err := s.store.GetAdminUserSubscriptionToken(r.Context(), userID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	subscribeURL, err := s.publicSubscriptionURL(r.Context(), token, "")
	if err != nil {
		handleStoreError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeSuccess(w, http.StatusOK, map[string]string{"subscribe_url": subscribeURL})
}

func (s *server) resetAdminUserSubscriptionSecurity(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须是正整数", map[string]string{"revision": "格式无效"})
		return
	}
	if err := s.rotateAdminUserSubscriptionSecurity(r, userID, input.Revision); err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) legacyResetAdminUserSubscriptionSecurity(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	account, err := s.store.GetAdminUser(r.Context(), input.ID)
	if err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	if err := s.rotateAdminUserSubscriptionSecurity(r, input.ID, account.Revision); err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) rotateAdminUserSubscriptionSecurity(r *http.Request, userID, revision int64) error {
	_, mutation, err := s.store.ResetSubscriptionSecurityAtRevision(r.Context(), userID, revision, s.now())
	if err != nil {
		return err
	}
	if s.hub != nil {
		s.hub.NotifyUserMutation(r.Context(), userID, mutation.PreviousUUID, mutation.GroupID, mutation.GroupID, true)
	}
	return nil
}

func (s *server) listAdminUserOrders(w http.ResponseWriter, r *http.Request) {
	userID, page, pageSize, ok := adminUserOperationPage(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetAdminUser(r.Context(), userID); err != nil {
		handleStoreError(w, err)
		return
	}
	result, err := s.store.ListAdminOrders(r.Context(), store.AdminOrderFilter{Page: page, PageSize: pageSize, UserID: &userID})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) assignAdminUserOrder(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		PlanID      int64  `json:"plan_id"`
		Period      string `json:"period"`
		TotalAmount int64  `json:"total_amount"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	order, err := s.store.AssignOrder(r.Context(), store.AssignOrderInput{
		UserID: &userID, PlanID: input.PlanID, Period: input.Period, TotalAmount: input.TotalAmount,
	}, s.now())
	if err != nil {
		handleOrderError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, order)
}

func (s *server) listAdminUserInvitations(w http.ResponseWriter, r *http.Request) {
	userID, page, pageSize, ok := adminUserOperationPage(w, r)
	if !ok {
		return
	}
	result, err := s.store.ListAdminUserInvitations(r.Context(), userID, page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) listAdminUserTraffic(w http.ResponseWriter, r *http.Request) {
	userID, page, pageSize, ok := adminUserOperationPage(w, r)
	if !ok {
		return
	}
	result, err := s.store.ListAdminUserTrafficStats(r.Context(), userID, page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) listAdminUserTrafficResets(w http.ResponseWriter, r *http.Request) {
	userID, page, pageSize, ok := adminUserOperationPage(w, r)
	if !ok {
		return
	}
	result, err := s.store.ListAdminUserTrafficResets(r.Context(), userID, page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) resetAdminUserTraffic(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "idempotency_key_required", "流量重置必须提供 Idempotency-Key", map[string]string{"Idempotency-Key": "缺少幂等键"})
		return
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ResetAdminUserTraffic(r.Context(), store.AdminUserTrafficResetInput{
		UserID: userID, AdministratorID: session.UserID, Reason: input.Reason, IdempotencyKey: idempotencyKey,
	}, s.now())
	if err != nil {
		writeAdminUserTrafficResetError(w, err)
		return
	}
	if s.hub != nil && !result.Idempotent {
		s.hub.NotifyUserMutation(r.Context(), result.UserID, result.UUID, result.GroupID, result.GroupID, false)
	}
	writeSuccess(w, http.StatusOK, result)
}

func adminUserOperationPage(w http.ResponseWriter, r *http.Request) (int64, int, int, bool) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return 0, 0, 0, false
	}
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return 0, 0, 0, false
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 100)
	return userID, page, pageSize, ok
}

func writeAdminUserTrafficResetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTrafficResetUnavailable):
		writeAPIError(w, http.StatusConflict, "traffic_reset_unavailable", "该用户当前不能重置流量", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "idempotency_conflict", "该幂等键已用于不同的流量重置请求", nil)
	default:
		handleStoreError(w, err)
	}
}

func (s *server) legacyResetAdminUserTraffic(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID int64  `json:"user_id"`
		Reason string `json:"reason"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = "legacy-" + uuid.NewString()
	}
	session, _ := sessionFromContext(r.Context())
	result, err := s.store.ResetAdminUserTraffic(r.Context(), store.AdminUserTrafficResetInput{
		UserID: input.UserID, AdministratorID: session.UserID, Reason: input.Reason, IdempotencyKey: idempotencyKey,
	}, s.now())
	if err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	if s.hub != nil && !result.Idempotent {
		s.hub.NotifyUserMutation(r.Context(), result.UserID, result.UUID, result.GroupID, result.GroupID, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "流量重置成功",
		"data":    legacyTrafficResetResult(result),
	})
}

func (s *server) legacyListAdminUserTrafficResets(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	pageSize := 10
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 50 {
			writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "limit 格式无效")
			return
		}
		pageSize = value
	}
	account, err := s.store.GetAdminUser(r.Context(), userID)
	if err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	result, err := s.store.ListAdminUserTrafficResets(r.Context(), userID, 1, pageSize)
	if err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	history := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		triggerSource, triggerSourceName := legacyTrafficResetSource(item.TriggerSource)
		var metadata any
		if item.Reason != "" || item.AdministratorID != nil || item.AdministratorEmail != nil {
			metadata = map[string]any{
				"reason": item.Reason, "admin_id": item.AdministratorID, "admin_email": item.AdministratorEmail,
			}
		}
		total := legacyTrafficTotal(item.UploadBefore, item.DownloadBefore)
		resetType, resetTypeName := legacyTrafficResetType(item.ResetMethod)
		history = append(history, map[string]any{
			"id": item.ID, "reset_type": resetType, "reset_type_name": resetTypeName,
			"reset_time": item.ResetAt,
			"old_traffic": map[string]any{
				"upload": item.UploadBefore, "download": item.DownloadBefore,
				"total": total, "formatted": legacyFormattedTraffic(total),
			},
			"trigger_source": triggerSource, "trigger_source_name": triggerSourceName,
			"metadata": metadata,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
		"user": map[string]any{
			"id": account.ID, "email": account.Email, "reset_count": account.ResetCount,
			"last_reset_at": unixPointer(account.LastResetAt), "next_reset_at": unixPointer(account.NextResetAt),
		},
		"history": history,
	}})
}

func (s *server) legacyListAdminUserTraffic(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UserID   int64 `json:"user_id"`
		PageSize int   `json:"pageSize"`
		Page     int   `json:"page"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = 10
	}
	result, err := s.store.ListAdminUserTrafficStats(r.Context(), input.UserID, input.Page, input.PageSize)
	if err != nil {
		writeLegacyAdminUserOperationError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, map[string]any{
			"record_at": item.RecordAt.Unix(), "u": item.Upload, "d": item.Download,
			"rate": float64(item.RateMicros) / 1_000_000, "total": item.Upload + item.Download,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "total": result.Total})
}

func legacyTrafficResetResult(result store.AdminUserTrafficResetResult) map[string]any {
	return map[string]any{
		"user_id": result.UserID, "email": result.Email, "reset_time": result.ResetAt,
		"next_reset_at": unixPointer(result.NextResetAt),
	}
}

func legacyTrafficResetType(method int) (string, string) {
	switch method {
	case 0:
		return "first_day_month", "每月1号重置"
	case 1:
		return "monthly", "按月重置"
	case 3:
		return "first_day_year", "每年1月1日重置"
	case 4:
		return "yearly", "按年重置"
	default:
		return "manual", "手动重置"
	}
}

func legacyTrafficResetSource(source string) (string, string) {
	if source == "manual" {
		return "manual", "手动触发"
	}
	return "cron", "定时任务"
}

func legacyTrafficTotal(upload, download int64) int64 {
	maximum := int64(^uint64(0) >> 1)
	if upload < 0 || download < 0 || upload > maximum-download {
		return maximum
	}
	return upload + download
}

func legacyFormattedTraffic(value int64) string {
	units := [...]string{"B", "KB", "MB", "GB", "TB"}
	amount := float64(max(value, 0))
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	formatted := strconv.FormatFloat(amount, 'f', 2, 64)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	return formatted + " " + units[unit]
}

func writeLegacyAdminUserOperationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusNotFound, "用户不存在")
	case errors.Is(err, store.ErrTrafficResetUnavailable):
		writeLegacyOrderFail(w, http.StatusBadRequest, "该用户当前不能重置流量")
	case errors.Is(err, store.ErrRevisionConflict):
		writeLegacyOrderFail(w, http.StatusConflict, "用户状态已被其他管理员修改，请刷新后重试")
	case errors.Is(err, store.ErrConflict):
		writeLegacyOrderFail(w, http.StatusConflict, "重复请求与原请求不一致")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "请求参数格式无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "用户关联操作失败")
	}
}
