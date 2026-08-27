package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listAdminUsers(w http.ResponseWriter, r *http.Request) {
	filter := store.AdminUserFilter{Cursor: r.URL.Query().Get("cursor"), EmailPrefix: r.URL.Query().Get("email_prefix")}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "limit 必须是整数", map[string]string{"limit": "格式无效"})
			return
		}
		filter.Limit = value
	}
	if raw := r.URL.Query().Get("banned"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "banned 必须是 true 或 false", map[string]string{"banned": "格式无效"})
			return
		}
		filter.Banned = &value
	}
	if raw := r.URL.Query().Get("group_id"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "group_id 必须是正整数", map[string]string{"group_id": "格式无效"})
			return
		}
		filter.GroupID = &value
	}
	if !parseAdminUserPageQuery(w, r, &filter) {
		return
	}
	page, err := s.store.ListAdminUsers(r.Context(), filter)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, page)
}

type adminUserFilterWire struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

func parseAdminUserPageQuery(w http.ResponseWriter, r *http.Request, filter *store.AdminUserFilter) bool {
	query := r.URL.Query()
	paged := query.Get("page") != "" || query.Get("page_size") != "" || query.Get("sort_by") != "" || query.Get("filters") != "" || query.Get("sort_desc") != ""
	if !paged {
		return true
	}
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return false
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 50, 200)
	if !ok {
		return false
	}
	filter.Page, filter.PageSize = page, pageSize
	filter.SortBy = store.AdminUserSort(strings.TrimSpace(query.Get("sort_by")))
	if raw := query.Get("sort_desc"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "sort_desc 必须是 true 或 false", map[string]string{"sort_desc": "格式无效"})
			return false
		}
		filter.SortDescending = value
	}
	raw := query.Get("filters")
	if raw == "" {
		return true
	}
	if len(raw) > 8192 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "筛选条件过长", map[string]string{"filters": "最多 8192 字节"})
		return false
	}
	var wires []adminUserFilterWire
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wires); err != nil || len(wires) > 10 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "筛选条件格式无效", map[string]string{"filters": "格式无效或条件过多"})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "筛选条件格式无效", map[string]string{"filters": "只能提交一个 JSON 数组"})
		return false
	}
	for _, wire := range wires {
		if wire.Field == store.AdminUserFieldUUID || wire.Field == store.AdminUserFieldSubscriptionToken {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "敏感凭据筛选必须使用 POST 查询接口", map[string]string{"filters": "请使用 /api/v1/admin/users/query"})
			return false
		}
	}
	return appendAdminUserWireFilters(w, filter, wires)
}

func appendAdminUserWireFilters(w http.ResponseWriter, filter *store.AdminUserFilter, wires []adminUserFilterWire) bool {
	for _, wire := range wires {
		values, ok := adminUserWireValues(wire.Value, wire.Operator)
		if !ok || strings.TrimSpace(wire.Field) == "" || strings.TrimSpace(wire.Operator) == "" {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "筛选条件格式无效", map[string]string{"filters": "字段、操作符或值无效"})
			return false
		}
		filter.Rules = append(filter.Rules, store.AdminUserFilterRule{
			Field: strings.TrimSpace(wire.Field), Operator: strings.TrimSpace(wire.Operator), Values: values,
		})
	}
	return true
}

func (s *server) queryAdminUsers(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Page        int                   `json:"page"`
		PageSize    int                   `json:"page_size"`
		SortBy      store.AdminUserSort   `json:"sort_by"`
		SortDesc    bool                  `json:"sort_desc"`
		EmailPrefix string                `json:"email_prefix"`
		Banned      *bool                 `json:"banned"`
		GroupID     *int64                `json:"group_id"`
		Filters     []adminUserFilterWire `json:"filters"`
	}
	if !decodeJSONLimit(w, r, &input, 16*1024) {
		return
	}
	if len(input.Filters) > 10 || input.Page < 1 || input.Page > 1_000_000 || input.PageSize < 1 || input.PageSize > 200 ||
		input.GroupID != nil && *input.GroupID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "用户查询参数超出允许范围", nil)
		return
	}
	filter := store.AdminUserFilter{
		Page: input.Page, PageSize: input.PageSize, SortBy: input.SortBy, SortDescending: input.SortDesc,
		EmailPrefix: input.EmailPrefix, Banned: input.Banned, GroupID: input.GroupID,
	}
	if !appendAdminUserWireFilters(w, &filter, input.Filters) {
		return
	}
	page, err := s.store.ListAdminUsers(r.Context(), filter)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, page)
}

func adminUserWireValues(raw json.RawMessage, operator string) ([]string, bool) {
	if operator == store.AdminUserOperatorIsNull || operator == store.AdminUserOperatorNotNull {
		return nil, len(raw) == 0 || string(raw) == "null"
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		if len(list) < 1 || len(list) > 20 {
			return nil, false
		}
		values := make([]string, 0, len(list))
		for _, item := range list {
			value, ok := adminUserWireScalar(item)
			if !ok {
				return nil, false
			}
			values = append(values, value)
		}
		return values, true
	}
	value, ok := adminUserWireScalar(raw)
	if !ok {
		return nil, false
	}
	return []string{value}, true
}

func adminUserWireScalar(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, true
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return number.String(), true
	}
	var boolean bool
	if json.Unmarshal(raw, &boolean) == nil {
		return strconv.FormatBool(boolean), true
	}
	return "", false
}

func (s *server) legacyListAdminUsers(w http.ResponseWriter, r *http.Request) {
	filter := store.AdminUserFilter{Page: 1, PageSize: 10}
	if r.Method == http.MethodGet {
		filter.Page = legacyPositiveInt(r.URL.Query().Get("current"), 1)
		filter.PageSize = legacyPositiveInt(r.URL.Query().Get("pageSize"), 10)
		if search := strings.TrimSpace(r.URL.Query().Get("search")); search != "" {
			filter.Rules = append(filter.Rules, store.AdminUserFilterRule{
				Field: store.AdminUserFieldEmail, Operator: store.AdminUserOperatorContains, Values: []string{search},
			})
		}
	} else {
		var input map[string]json.RawMessage
		if !decodeJSON(w, r, &input) {
			return
		}
		filter.Page = legacyRawPositiveInt(input["current"], 1)
		filter.PageSize = legacyRawPositiveInt(input["pageSize"], 10)
		var filters []struct {
			ID    string          `json:"id"`
			Value json.RawMessage `json:"value"`
		}
		if raw := input["filter"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &filters) != nil || len(filters) > 10 {
				writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "筛选条件格式无效")
				return
			}
		}
		for _, item := range filters {
			rule, ok := legacyAdminUserRule(item.ID, item.Value)
			if !ok {
				writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "不支持的用户筛选条件")
				return
			}
			filter.Rules = append(filter.Rules, rule)
		}
		var sorts []struct {
			ID   string `json:"id"`
			Desc bool   `json:"desc"`
		}
		if raw := input["sort"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &sorts) != nil || len(sorts) > 3 {
				writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "排序条件格式无效")
				return
			}
		}
		for _, sortInput := range sorts {
			sortBy, ok := legacyAdminUserSort(sortInput.ID)
			if !ok {
				writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "不支持的用户排序字段")
				return
			}
			filter.Sorts = append(filter.Sorts, store.AdminUserSortRule{Field: sortBy, Descending: sortInput.Desc})
		}
	}
	if filter.PageSize < 1 || filter.PageSize > 200 || filter.Page < 1 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "分页参数格式无效")
		return
	}
	page, err := s.store.ListAdminUsers(r.Context(), filter)
	if err != nil {
		if errors.Is(err, store.ErrInvalidInput) {
			writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "用户筛选或排序参数无效")
		} else {
			writeLegacyOrderFail(w, http.StatusInternalServerError, "用户列表请求失败")
		}
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, user := range page.Items {
		items = append(items, legacyAdminUserResponse(user))
	}
	lastPage := int((page.Total + int64(page.PageSize) - 1) / int64(page.PageSize))
	if lastPage < 1 {
		lastPage = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": page.Total, "current_page": page.Page, "per_page": page.PageSize, "last_page": lastPage, "data": items,
	})
}

func legacyAdminUserRule(field string, raw json.RawMessage) (store.AdminUserFilterRule, bool) {
	fieldMap := map[string]string{
		"id": store.AdminUserFieldID, "email": store.AdminUserFieldEmail, "plan_id": store.AdminUserFieldPlanID,
		"plan.id":  store.AdminUserFieldPlanID,
		"group_id": store.AdminUserFieldGroupID, "group_ids": store.AdminUserFieldGroupID,
		"group.id":        store.AdminUserFieldGroupID,
		"transfer_enable": store.AdminUserFieldTransferEnable, "d": store.AdminUserFieldTrafficUsed,
		"total_used": store.AdminUserFieldTrafficUsed, "online_count": store.AdminUserFieldOnlineCount,
		"expired_at": store.AdminUserFieldExpiredAt, "uuid": store.AdminUserFieldUUID,
		"token": store.AdminUserFieldSubscriptionToken, "banned": store.AdminUserFieldBanned,
		"remarks": store.AdminUserFieldRemarks, "invite_user_id": store.AdminUserFieldInviteUserID, "invite_user.id": store.AdminUserFieldInviteUserID,
		"invite_by_email": store.AdminUserFieldInviteUserEmail, "invite_user.email": store.AdminUserFieldInviteUserEmail,
		"is_admin": store.AdminUserFieldIsAdmin, "is_staff": store.AdminUserFieldIsStaff,
		"is_distributor": store.AdminUserFieldIsDistributor, "balance": store.AdminUserFieldBalance,
		"commission_balance": store.AdminUserFieldCommissionBalance, "created_at": store.AdminUserFieldCreatedAt,
	}
	mapped, exists := fieldMap[strings.TrimSpace(field)]
	if !exists {
		return store.AdminUserFilterRule{}, false
	}
	values, ok := legacyAdminUserValues(raw)
	if !ok || len(values) < 1 || len(values) > 20 {
		return store.AdminUserFilterRule{}, false
	}
	operator := store.AdminUserOperatorContains
	if len(values) > 1 {
		operator = store.AdminUserOperatorIn
	} else if separator := strings.IndexByte(values[0], ':'); separator > 0 {
		legacyOperator := strings.ToLower(strings.TrimSpace(values[0][:separator]))
		values[0] = strings.TrimSpace(values[0][separator+1:])
		operatorMap := map[string]string{
			"is": store.AdminUserOperatorEqual, "eq": store.AdminUserOperatorEqual, "=": store.AdminUserOperatorEqual,
			"not": store.AdminUserOperatorNotEqual, "neq": store.AdminUserOperatorNotEqual, "!=": store.AdminUserOperatorNotEqual,
			"gt": store.AdminUserOperatorGreater, ">": store.AdminUserOperatorGreater,
			"gte": store.AdminUserOperatorGreaterOrEqual, ">=": store.AdminUserOperatorGreaterOrEqual,
			"lt": store.AdminUserOperatorLess, "<": store.AdminUserOperatorLess,
			"lte": store.AdminUserOperatorLessOrEqual, "<=": store.AdminUserOperatorLessOrEqual,
			"like": store.AdminUserOperatorContains, "模糊": store.AdminUserOperatorContains,
		}
		var ok bool
		operator, ok = operatorMap[legacyOperator]
		if !ok {
			return store.AdminUserFilterRule{}, false
		}
	} else if legacyAdminUserNumericOrBooleanField(mapped) {
		operator = store.AdminUserOperatorEqual
	}
	if (mapped == store.AdminUserFieldUUID || mapped == store.AdminUserFieldSubscriptionToken) && operator != store.AdminUserOperatorEqual && operator != store.AdminUserOperatorIn {
		return store.AdminUserFilterRule{}, false
	}
	return store.AdminUserFilterRule{Field: mapped, Operator: operator, Values: values}, true
}

func legacyAdminUserValues(raw json.RawMessage) ([]string, bool) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil {
		if len(list) < 1 || len(list) > 20 {
			return nil, false
		}
		values := make([]string, 0, len(list))
		for _, item := range list {
			value, ok := adminUserWireScalar(item)
			if !ok {
				return nil, false
			}
			values = append(values, value)
		}
		return values, true
	}
	value, ok := adminUserWireScalar(raw)
	if !ok {
		return nil, false
	}
	return []string{value}, true
}

func legacyAdminUserNumericOrBooleanField(field string) bool {
	switch field {
	case store.AdminUserFieldID, store.AdminUserFieldPlanID, store.AdminUserFieldGroupID, store.AdminUserFieldTransferEnable,
		store.AdminUserFieldTrafficUsed, store.AdminUserFieldOnlineCount, store.AdminUserFieldExpiredAt,
		store.AdminUserFieldBanned, store.AdminUserFieldInviteUserID, store.AdminUserFieldIsAdmin,
		store.AdminUserFieldIsStaff, store.AdminUserFieldIsDistributor, store.AdminUserFieldBalance,
		store.AdminUserFieldCommissionBalance, store.AdminUserFieldCreatedAt:
		return true
	default:
		return false
	}
}

func legacyAdminUserSort(value string) (store.AdminUserSort, bool) {
	sorts := map[string]store.AdminUserSort{
		"id": store.AdminUserSortID, "online_count": store.AdminUserSortOnlineCount, "banned": store.AdminUserSortBanned,
		"total_used": store.AdminUserSortTrafficUsed, "d": store.AdminUserSortTrafficUsed,
		"transfer_enable": store.AdminUserSortTransferEnable, "expired_at": store.AdminUserSortExpiredAt,
		"balance": store.AdminUserSortBalance, "commission_balance": store.AdminUserSortCommissionBalance,
		"created_at": store.AdminUserSortCreatedAt,
	}
	sortBy, ok := sorts[strings.TrimSpace(value)]
	return sortBy, ok
}

func legacyAdminUserResponse(user store.AdminUser) map[string]any {
	result := map[string]any{
		"id": user.ID, "email": user.Email, "is_admin": user.IsAdmin, "is_staff": user.IsStaff,
		"is_distributor": user.IsDistributor, "distributor_name": user.DistributorName, "banned": user.Banned,
		"group_id": user.GroupID, "plan_id": user.PlanID, "invite_user_id": user.InviteUserID,
		"transfer_enable": user.TransferEnable, "u": user.TrafficUpload, "d": user.TrafficDownload, "total_used": user.TrafficUsed,
		"expired_at": legacyAdminUserUnix(user.ExpiredAt), "speed_limit": user.SpeedLimit, "device_limit": user.DeviceLimit,
		"online_count": user.OnlineCount, "last_online_at": legacyAdminUserUnix(user.LastOnlineAt), "last_login_at": legacyAdminUserUnix(user.LastLoginAt),
		"balance": float64(user.Balance) / 100, "commission_type": user.CommissionType, "commission_rate": user.CommissionRate,
		"commission_balance": float64(user.CommissionBalance) / 100, "discount": user.Discount,
		"next_reset_at": legacyAdminUserUnix(user.NextResetAt), "last_reset_at": legacyAdminUserUnix(user.LastResetAt), "reset_count": user.ResetCount,
		"telegram_id": user.TelegramID, "remind_expire": user.RemindExpire, "remind_traffic": user.RemindTraffic, "remarks": user.Remarks,
		"created_at": user.CreatedAt.Unix(), "updated_at": user.UpdatedAt.Unix(),
	}
	if user.GroupID != nil && user.GroupName != nil {
		result["group"] = map[string]any{"id": *user.GroupID, "name": *user.GroupName}
	} else {
		result["group"] = nil
	}
	if user.PlanID != nil && user.PlanName != nil {
		result["plan"] = map[string]any{"id": *user.PlanID, "name": *user.PlanName}
	} else {
		result["plan"] = nil
	}
	if user.InviteUserID != nil && user.InviteUserEmail != nil {
		result["invite_user"] = map[string]any{"id": *user.InviteUserID, "email": *user.InviteUserEmail}
	} else {
		result["invite_user"] = nil
	}
	return result
}

func legacyAdminUserUnix(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.Unix()
}

type legacyAdminUserUpdateRequest struct {
	ID                int64                 `json:"id"`
	Revision          *int64                `json:"revision"`
	Email             *string               `json:"email"`
	Password          *string               `json:"password"`
	IsAdmin           *bool                 `json:"is_admin"`
	IsStaff           *bool                 `json:"is_staff"`
	IsDistributor     legacyOptionalBool    `json:"is_distributor"`
	DistributorName   *string               `json:"distributor_name"`
	GroupID           nullableInt64         `json:"group_id"`
	PlanID            nullableInt64         `json:"plan_id"`
	InviteUserEmail   nullableString        `json:"invite_user_email"`
	TransferEnable    *int64                `json:"transfer_enable"`
	TrafficUpload     *int64                `json:"u"`
	TrafficDownload   *int64                `json:"d"`
	ExpiredAt         nullableUnixTimestamp `json:"expired_at"`
	SpeedLimit        nullableInt           `json:"speed_limit"`
	DeviceLimit       nullableInt           `json:"device_limit"`
	Banned            *bool                 `json:"banned"`
	Balance           json.RawMessage       `json:"balance"`
	CommissionType    *int                  `json:"commission_type"`
	CommissionRate    nullableInt           `json:"commission_rate"`
	CommissionBalance json.RawMessage       `json:"commission_balance"`
	Discount          nullableInt           `json:"discount"`
	TelegramID        nullableInt64         `json:"telegram_id"`
	RemindExpire      *bool                 `json:"remind_expire"`
	RemindTraffic     *bool                 `json:"remind_traffic"`
	Remarks           nullableString        `json:"remarks"`
}

func (s *server) legacyUpdateAdminUser(w http.ResponseWriter, r *http.Request) {
	var input legacyAdminUserUpdateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ID < 1 || input.Revision != nil && *input.Revision < 1 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "用户 ID 或版本号格式无效")
		return
	}
	existing, err := s.store.GetAdminUser(r.Context(), input.ID)
	if err != nil {
		writeLegacyAdminUserError(w, err)
		return
	}

	revision := existing.Revision
	if input.Revision != nil {
		revision = *input.Revision
	}
	email := existing.Email
	if input.Email != nil {
		email = strings.TrimSpace(*input.Email)
	}
	groupID := existing.GroupID
	if input.GroupID.Set {
		groupID = input.GroupID.Value
	}
	transferEnable := existing.TransferEnable
	if input.TransferEnable != nil {
		transferEnable = *input.TransferEnable
	}
	expiredAt := existing.ExpiredAt
	if input.ExpiredAt.Set {
		expiredAt = input.ExpiredAt.Value
	}
	speedLimit := existing.SpeedLimit
	if input.SpeedLimit.Set {
		speedLimit = 0
		if input.SpeedLimit.Value != nil {
			speedLimit = *input.SpeedLimit.Value
		}
	}
	deviceLimit := existing.DeviceLimit
	if input.DeviceLimit.Set {
		deviceLimit = 0
		if input.DeviceLimit.Value != nil {
			deviceLimit = *input.DeviceLimit.Value
		}
	}
	banned := existing.Banned
	if input.Banned != nil {
		banned = *input.Banned
	}
	isAdmin, isDistributor := existing.IsAdmin, existing.IsDistributor
	if input.IsAdmin != nil {
		isAdmin = *input.IsAdmin
	}
	if input.IsDistributor.Set {
		isDistributor = input.IsDistributor.Value
	}
	distributorName := existing.DistributorName
	if input.DistributorName != nil {
		distributorName = input.DistributorName
	}

	fields := validateAdminUserFields(email, legacyPassword(input.Password), groupID, transferEnable, expiredAt, speedLimit, deviceLimit, input.Password != nil && *input.Password != "")
	for field, message := range validateDistributorRoleFields(isDistributor, distributorName, isDistributor) {
		fields[field] = message
	}
	if input.PlanID.Set && input.PlanID.Value != nil && *input.PlanID.Value < 1 {
		fields["plan_id"] = "必须是正整数或 null"
	}
	if input.InviteUserEmail.Set && input.InviteUserEmail.Value != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.InviteUserEmail.Value))
		if normalized != "" {
			address, parseErr := mail.ParseAddress(normalized)
			if parseErr != nil || address.Address != normalized || len(normalized) > 320 {
				fields["invite_user_email"] = "邮箱格式无效"
			}
		}
	}
	if input.TrafficUpload != nil && (*input.TrafficUpload < 0 || *input.TrafficUpload > 9_007_199_254_740_991) {
		fields["u"] = "必须是安全范围内的非负整数"
	}
	if input.TrafficDownload != nil && (*input.TrafficDownload < 0 || *input.TrafficDownload > 9_007_199_254_740_991) {
		fields["d"] = "必须是安全范围内的非负整数"
	}
	validateAdminUserRangeField(fields, "commission_type", input.CommissionType, 0, 2)
	if input.CommissionRate.Set {
		validateAdminUserRangeField(fields, "commission_rate", input.CommissionRate.Value, 0, 100)
	}
	if input.Discount.Set {
		validateAdminUserRangeField(fields, "discount", input.Discount.Value, 0, 100)
	}
	if input.TelegramID.Set && input.TelegramID.Value != nil && *input.TelegramID.Value < 1 {
		fields["telegram_id"] = "必须是正整数或 null"
	}
	if input.Remarks.Set && input.Remarks.Value != nil && (!utf8.ValidString(*input.Remarks.Value) || len(*input.Remarks.Value) > 4096 || strings.IndexByte(*input.Remarks.Value, 0) >= 0) {
		fields["remarks"] = "不得超过 4096 字节且必须是有效文本"
	}
	balance, balanceErr := legacyMoneyCents(input.Balance)
	if balanceErr != nil {
		fields["balance"] = "金额格式无效，最多保留两位小数"
	}
	commissionBalance, commissionBalanceErr := legacyMoneyCents(input.CommissionBalance)
	if commissionBalanceErr != nil {
		fields["commission_balance"] = "金额格式无效，最多保留两位小数"
	}
	if len(fields) > 0 {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "请检查用户信息")
		return
	}

	session, _ := sessionFromContext(r.Context())
	if input.ID == session.UserID && banned {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "管理员不能封禁自己的当前账号")
		return
	}
	if input.ID == session.UserID && !isAdmin {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "不能撤销当前登录账号的管理员权限")
		return
	}
	var passwordHash *string
	if input.Password != nil && *input.Password != "" {
		hashed, hashErr := s.passwordHasher.Hash(*input.Password)
		if hashErr != nil {
			writeLegacyOrderFail(w, http.StatusInternalServerError, "用户更新失败")
			return
		}
		passwordHash = &hashed
	}
	updated, mutation, err := s.store.UpdateAdminUser(r.Context(), input.ID, store.UpdateAdminUserInput{
		Revision: revision, Email: email, PasswordHash: passwordHash,
		IsAdmin: input.IsAdmin, IsStaff: input.IsStaff, IsDistributor: input.IsDistributor.Pointer(), DistributorName: input.DistributorName,
		GroupID: groupID, PlanIDSet: input.PlanID.Set, PlanID: input.PlanID.Value,
		InviteUserEmailSet: input.InviteUserEmail.Set, InviteUserEmail: input.InviteUserEmail.Value,
		TransferEnable: transferEnable, TrafficUpload: input.TrafficUpload, TrafficDownload: input.TrafficDownload,
		ExpiredAt: expiredAt, SpeedLimit: speedLimit, DeviceLimit: deviceLimit, Banned: banned,
		Balance: balance, CommissionType: input.CommissionType,
		CommissionRateSet: input.CommissionRate.Set, CommissionRate: input.CommissionRate.Value,
		CommissionBalance: commissionBalance, DiscountSet: input.Discount.Set, Discount: input.Discount.Value,
		TelegramIDSet: input.TelegramID.Set, TelegramID: input.TelegramID.Value,
		RemindExpire: input.RemindExpire, RemindTraffic: input.RemindTraffic,
		RemarksSet: input.Remarks.Set, Remarks: input.Remarks.Value,
	}, s.now())
	if err != nil {
		writeLegacyAdminUserError(w, err)
		return
	}
	if s.hub != nil && mutation.RuntimeChanged {
		s.hub.NotifyUserMutation(r.Context(), updated.ID, mutation.UUID, mutation.OldGroupID, mutation.NewGroupID, mutation.AccessStateCleared)
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func legacyPassword(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func legacyMoneyCents(raw json.RawMessage) (*int64, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		return nil, fmt.Errorf("money cannot be null")
	}
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, err
		}
		text = strings.TrimSpace(text)
	}
	if text == "" || strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") || strings.ContainsAny(text, "eE") {
		return nil, fmt.Errorf("invalid money")
	}
	parts := strings.Split(text, ".")
	if len(parts) > 2 || parts[0] == "" || len(parts) == 2 && len(parts[1]) > 2 {
		return nil, fmt.Errorf("invalid money")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, err
	}
	fraction := int64(0)
	if len(parts) == 2 {
		if parts[1] == "" {
			return nil, fmt.Errorf("invalid money")
		}
		fractionText := parts[1]
		if len(fractionText) == 1 {
			fractionText += "0"
		}
		fraction, err = strconv.ParseInt(fractionText, 10, 64)
		if err != nil {
			return nil, err
		}
	}
	const maximum = int64(9_000_000_000_000_000)
	if whole > (maximum-fraction)/100 {
		return nil, fmt.Errorf("money exceeds range")
	}
	cents := whole*100 + fraction
	return &cents, nil
}

func writeLegacyAdminUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeLegacyOrderFail(w, http.StatusNotFound, "用户不存在")
	case errors.Is(err, store.ErrEmailInUse):
		writeLegacyOrderFail(w, http.StatusConflict, "邮箱已被使用")
	case errors.Is(err, store.ErrConflict):
		writeLegacyOrderFail(w, http.StatusConflict, "用户状态已被其他管理员修改，请刷新后重试")
	case errors.Is(err, store.ErrAdminUserPlanNotFound):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "订阅计划不存在")
	case errors.Is(err, store.ErrAdminInviteUserNotFound):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "邀请用户不存在")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "用户参数格式无效")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, "用户更新失败")
	}
}

func (s *server) getAdminUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	user, err := s.store.GetAdminUser(r.Context(), userID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, user)
}

func (s *server) createAdminUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email           string     `json:"email"`
		Password        string     `json:"password"`
		IsAdmin         bool       `json:"is_admin"`
		IsStaff         bool       `json:"is_staff"`
		IsDistributor   bool       `json:"is_distributor"`
		DistributorName *string    `json:"distributor_name"`
		GroupID         *int64     `json:"group_id"`
		TransferEnable  int64      `json:"transfer_enable"`
		ExpiredAt       *time.Time `json:"expired_at"`
		SpeedLimit      int        `json:"speed_limit"`
		DeviceLimit     int        `json:"device_limit"`
		Banned          bool       `json:"banned"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	fields := validateAdminUserFields(input.Email, input.Password, input.GroupID, input.TransferEnable, input.ExpiredAt, input.SpeedLimit, input.DeviceLimit, true)
	for field, message := range validateDistributorRoleFields(input.IsDistributor, input.DistributorName, true) {
		fields[field] = message
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查用户信息", fields)
		return
	}
	passwordHash, err := s.passwordHasher.Hash(input.Password)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	distributorName := ""
	if input.DistributorName != nil {
		distributorName = *input.DistributorName
	}
	user, err := s.store.CreateAdminUser(r.Context(), store.CreateAdminUserInput{
		Email: input.Email, PasswordHash: passwordHash, GroupID: input.GroupID, TransferEnable: input.TransferEnable,
		ExpiredAt: input.ExpiredAt, SpeedLimit: input.SpeedLimit, DeviceLimit: input.DeviceLimit, Banned: input.Banned,
		IsAdmin: input.IsAdmin, IsStaff: input.IsStaff, IsDistributor: input.IsDistributor, DistributorName: distributorName,
	}, s.now())
	if errors.Is(err, store.ErrEmailInUse) {
		writeAPIError(w, http.StatusConflict, "email_in_use", "邮箱已被使用", map[string]string{"email": "邮箱已被使用"})
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		s.hub.NotifyUserMutation(r.Context(), user.ID, "", nil, user.GroupID, false)
	}
	writeSuccess(w, http.StatusCreated, user)
}

func (s *server) updateAdminUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input adminUserUpdateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	fields := input.validationFields()
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请提交完整且有效的用户状态", fields)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if userID == session.UserID && *input.Banned {
		writeAPIError(w, http.StatusUnprocessableEntity, "cannot_ban_self", "管理员不能封禁自己的当前账号", map[string]string{"banned": "不能封禁当前账号"})
		return
	}
	if userID == session.UserID && input.IsAdmin != nil && !*input.IsAdmin {
		writeAPIError(w, http.StatusUnprocessableEntity, "cannot_remove_admin_self", "不能撤销当前登录账号的管理员权限，请使用另一个管理员账号操作", map[string]string{"is_admin": "不能撤销当前账号的管理员权限"})
		return
	}
	var passwordHash *string
	if input.Password != nil && *input.Password != "" {
		hashed, err := s.passwordHasher.Hash(*input.Password)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		passwordHash = &hashed
	}
	user, mutation, err := s.store.UpdateAdminUser(r.Context(), userID, store.UpdateAdminUserInput{
		Revision: *input.Revision, Email: *input.Email, PasswordHash: passwordHash,
		GroupID: input.GroupID.Value, PlanIDSet: input.PlanID.Set, PlanID: input.PlanID.Value,
		InviteUserEmailSet: input.InviteUserEmail.Set, InviteUserEmail: input.InviteUserEmail.Value,
		TransferEnable: *input.TransferEnable, TrafficUpload: input.TrafficUpload, TrafficDownload: input.TrafficDownload,
		ExpiredAt: input.ExpiredAt.Value, SpeedLimit: *input.SpeedLimit, DeviceLimit: *input.DeviceLimit, Banned: *input.Banned,
		IsAdmin: input.IsAdmin, IsStaff: input.IsStaff, IsDistributor: input.IsDistributor, DistributorName: input.DistributorName,
		Balance: input.Balance, CommissionType: input.CommissionType,
		CommissionRateSet: input.CommissionRate.Set, CommissionRate: input.CommissionRate.Value,
		CommissionBalance: input.CommissionBalance, DiscountSet: input.Discount.Set, Discount: input.Discount.Value,
		TelegramIDSet: input.TelegramID.Set, TelegramID: input.TelegramID.Value,
		RemindExpire: input.RemindExpire, RemindTraffic: input.RemindTraffic,
		RemarksSet: input.Remarks.Set, Remarks: input.Remarks.Value,
	}, s.now())
	if errors.Is(err, store.ErrEmailInUse) {
		writeAPIError(w, http.StatusConflict, "email_in_use", "邮箱已被使用", map[string]string{"email": "邮箱已被使用"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "user_revision_conflict", "用户状态已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrAdminUserPlanNotFound) {
		writeAPIError(w, http.StatusUnprocessableEntity, "plan_not_found", "订阅计划不存在", map[string]string{"plan_id": "订阅计划不存在"})
		return
	}
	if errors.Is(err, store.ErrAdminInviteUserNotFound) {
		writeAPIError(w, http.StatusUnprocessableEntity, "invite_user_not_found", "邀请用户不存在", map[string]string{"invite_user_email": "邀请用户不存在"})
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil && mutation.RuntimeChanged {
		s.hub.NotifyUserMutation(r.Context(), user.ID, mutation.UUID, mutation.OldGroupID, mutation.NewGroupID, mutation.AccessStateCleared)
	}
	writeSuccess(w, http.StatusOK, user)
}

func (s *server) resetAdminUserPassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Revision    int64  `json:"revision"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	fields := map[string]string{}
	if input.Revision < 1 {
		fields["revision"] = "必须是正整数"
	}
	if len(input.NewPassword) < 12 {
		fields["new_password"] = "至少需要 12 个字符"
	} else if len(input.NewPassword) > 1024 {
		fields["new_password"] = "不得超过 1024 个字符"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查密码输入", fields)
		return
	}
	passwordHash, err := s.passwordHasher.Hash(input.NewPassword)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	user, err := s.store.ResetAdminUserPassword(r.Context(), userID, input.Revision, passwordHash, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "user_revision_conflict", "用户状态已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, user)
}

type adminUserUpdateRequest struct {
	Revision          *int64         `json:"revision"`
	Email             *string        `json:"email"`
	Password          *string        `json:"password"`
	IsAdmin           *bool          `json:"is_admin"`
	IsStaff           *bool          `json:"is_staff"`
	IsDistributor     *bool          `json:"is_distributor"`
	DistributorName   *string        `json:"distributor_name"`
	GroupID           nullableInt64  `json:"group_id"`
	PlanID            nullableInt64  `json:"plan_id"`
	InviteUserEmail   nullableString `json:"invite_user_email"`
	TransferEnable    *int64         `json:"transfer_enable"`
	TrafficUpload     *int64         `json:"traffic_upload"`
	TrafficDownload   *int64         `json:"traffic_download"`
	ExpiredAt         nullableTime   `json:"expired_at"`
	SpeedLimit        *int           `json:"speed_limit"`
	DeviceLimit       *int           `json:"device_limit"`
	Banned            *bool          `json:"banned"`
	Balance           *int64         `json:"balance"`
	CommissionType    *int           `json:"commission_type"`
	CommissionRate    nullableInt    `json:"commission_rate"`
	CommissionBalance *int64         `json:"commission_balance"`
	Discount          nullableInt    `json:"discount"`
	TelegramID        nullableInt64  `json:"telegram_id"`
	RemindExpire      *bool          `json:"remind_expire"`
	RemindTraffic     *bool          `json:"remind_traffic"`
	Remarks           nullableString `json:"remarks"`
}

func (input adminUserUpdateRequest) validationFields() map[string]string {
	fields := map[string]string{}
	if input.Revision == nil || *input.Revision < 1 {
		fields["revision"] = "必须是正整数"
	}
	if input.Email == nil {
		fields["email"] = "必填"
	}
	if !input.GroupID.Set {
		fields["group_id"] = "必填，可为 null"
	}
	if input.TransferEnable == nil {
		fields["transfer_enable"] = "必填"
	}
	if !input.ExpiredAt.Set {
		fields["expired_at"] = "必填，可为 null"
	}
	if input.SpeedLimit == nil {
		fields["speed_limit"] = "必填"
	}
	if input.DeviceLimit == nil {
		fields["device_limit"] = "必填"
	}
	if input.Banned == nil {
		fields["banned"] = "必填"
	}
	if len(fields) > 0 {
		return fields
	}
	fields = validateAdminUserFields(*input.Email, "", input.GroupID.Value, *input.TransferEnable, input.ExpiredAt.Value, *input.SpeedLimit, *input.DeviceLimit, false)
	if input.Password != nil && *input.Password != "" {
		if len(*input.Password) < 12 {
			fields["password"] = "至少需要 12 个字符"
		} else if len(*input.Password) > 1024 {
			fields["password"] = "不得超过 1024 个字符"
		}
	}
	if input.PlanID.Set && input.PlanID.Value != nil && *input.PlanID.Value < 1 {
		fields["plan_id"] = "必须是正整数或 null"
	}
	if input.InviteUserEmail.Set && input.InviteUserEmail.Value != nil {
		normalized := strings.ToLower(strings.TrimSpace(*input.InviteUserEmail.Value))
		address, err := mail.ParseAddress(normalized)
		if err != nil || address.Address != normalized || len(normalized) > 320 {
			fields["invite_user_email"] = "邮箱格式无效"
		}
	}
	if input.TrafficUpload != nil && (*input.TrafficUpload < 0 || *input.TrafficUpload > 9_007_199_254_740_991) {
		fields["traffic_upload"] = "必须是安全范围内的非负整数"
	}
	if input.TrafficDownload != nil && (*input.TrafficDownload < 0 || *input.TrafficDownload > 9_007_199_254_740_991) {
		fields["traffic_download"] = "必须是安全范围内的非负整数"
	}
	if input.TrafficUpload != nil && input.TrafficDownload != nil && *input.TrafficUpload > 9_007_199_254_740_991-*input.TrafficDownload {
		fields["traffic_download"] = "上行与下行总和超出安全范围"
	}
	validateAdminUserMoneyField(fields, "balance", input.Balance)
	validateAdminUserMoneyField(fields, "commission_balance", input.CommissionBalance)
	validateAdminUserRangeField(fields, "commission_type", input.CommissionType, 0, 2)
	if input.CommissionRate.Set {
		validateAdminUserRangeField(fields, "commission_rate", input.CommissionRate.Value, 0, 100)
	}
	if input.Discount.Set {
		validateAdminUserRangeField(fields, "discount", input.Discount.Value, 0, 100)
	}
	if input.TelegramID.Set && input.TelegramID.Value != nil && *input.TelegramID.Value < 1 {
		fields["telegram_id"] = "必须是正整数或 null"
	}
	if input.Remarks.Set && input.Remarks.Value != nil && (!utf8.ValidString(*input.Remarks.Value) || len(*input.Remarks.Value) > 4096 || strings.IndexByte(*input.Remarks.Value, 0) >= 0) {
		fields["remarks"] = "不得超过 4096 字节且必须是有效文本"
	}
	if input.DistributorName != nil || (input.IsDistributor != nil && *input.IsDistributor) {
		for field, message := range validateDistributorRoleFields(input.IsDistributor != nil && *input.IsDistributor, input.DistributorName, false) {
			fields[field] = message
		}
	}
	return fields
}

func validateAdminUserMoneyField(fields map[string]string, field string, value *int64) {
	if value != nil && (*value < 0 || *value > 9_000_000_000_000_000) {
		fields[field] = "必须是允许范围内的非负金额"
	}
}

func validateAdminUserRangeField(fields map[string]string, field string, value *int, minimum, maximum int) {
	if value != nil && (*value < minimum || *value > maximum) {
		fields[field] = fmt.Sprintf("必须在 %d 到 %d 之间", minimum, maximum)
	}
}

func validateAdminUserFields(email, password string, groupID *int64, transferEnable int64, expiredAt *time.Time, speedLimit, deviceLimit int, validatePassword bool) map[string]string {
	fields := map[string]string{}
	normalized := strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized || len(normalized) > 320 {
		fields["email"] = "邮箱格式无效"
	}
	if validatePassword {
		if len(password) < 12 {
			fields["password"] = "至少需要 12 个字符"
		} else if len(password) > 1024 {
			fields["password"] = "不得超过 1024 个字符"
		}
	}
	if groupID != nil && *groupID < 1 {
		fields["group_id"] = "必须是正整数或 null"
	}
	if transferEnable < 0 {
		fields["transfer_enable"] = "不得小于 0"
	}
	if expiredAt != nil && expiredAt.Year() > 9999 {
		fields["expired_at"] = "时间超出允许范围"
	}
	if speedLimit < 0 {
		fields["speed_limit"] = "不得小于 0"
	}
	if deviceLimit < 0 || deviceLimit > 1_000 {
		fields["device_limit"] = "必须在 0 到 1000 之间"
	}
	return fields
}

func validateDistributorRoleFields(enabled bool, name *string, requireName bool) map[string]string {
	fields := map[string]string{}
	if !enabled {
		return fields
	}
	if name == nil {
		if requireName {
			fields["distributor_name"] = "启用分销商时必须填写分销商名称"
		}
		return fields
	}
	normalized := strings.TrimSpace(*name)
	if normalized == "" {
		fields["distributor_name"] = "启用分销商时必须填写分销商名称"
	} else if !utf8.ValidString(normalized) || utf8.RuneCountInString(normalized) > 100 {
		fields["distributor_name"] = "分销商名称不能超过100个字符"
	}
	return fields
}

type nullableInt64 struct {
	Set   bool
	Value *int64
}

type legacyOptionalBool struct {
	Set   bool
	Value bool
}

func (value *legacyOptionalBool) UnmarshalJSON(data []byte) error {
	value.Set = true
	switch strings.TrimSpace(string(data)) {
	case "true", "1":
		value.Value = true
	case "false", "0":
		value.Value = false
	default:
		return errors.New("must be a JSON boolean or 0/1")
	}
	return nil
}

func (value legacyOptionalBool) Pointer() *bool {
	if !value.Set {
		return nil
	}
	result := value.Value
	return &result
}

type nullableInt struct {
	Set   bool
	Value *int
}

func (value *nullableInt) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed int
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("integer: %w", err)
	}
	value.Value = &parsed
	return nil
}

type nullableString struct {
	Set   bool
	Value *string
}

func (value *nullableString) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed string
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("string: %w", err)
	}
	value.Value = &parsed
	return nil
}

func (value *nullableInt64) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed int64
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("group_id: %w", err)
	}
	value.Value = &parsed
	return nil
}

type nullableTime struct {
	Set   bool
	Value *time.Time
}

type nullableUnixTimestamp struct {
	Set   bool
	Value *time.Time
}

func (value *nullableUnixTimestamp) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var seconds int64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("unix timestamp: %w", err)
	}
	parsed := time.Unix(seconds, 0).UTC()
	value.Value = &parsed
	return nil
}

func (value *nullableTime) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(data, []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed time.Time
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("expired_at: %w", err)
	}
	parsed = parsed.UTC()
	value.Value = &parsed
	return nil
}
