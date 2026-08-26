package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	page, err := s.store.ListAdminUsers(r.Context(), filter)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, page)
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
	user, mutation, err := s.store.UpdateAdminUser(r.Context(), userID, store.UpdateAdminUserInput{
		Revision: *input.Revision, Email: *input.Email, GroupID: input.GroupID.Value, TransferEnable: *input.TransferEnable,
		ExpiredAt: input.ExpiredAt.Value, SpeedLimit: *input.SpeedLimit, DeviceLimit: *input.DeviceLimit, Banned: *input.Banned,
		IsAdmin: input.IsAdmin, IsStaff: input.IsStaff, IsDistributor: input.IsDistributor, DistributorName: input.DistributorName,
	}, s.now())
	if errors.Is(err, store.ErrEmailInUse) {
		writeAPIError(w, http.StatusConflict, "email_in_use", "邮箱已被使用", map[string]string{"email": "邮箱已被使用"})
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "user_revision_conflict", "用户状态已被其他管理员修改，请刷新后重试", nil)
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
	Revision        *int64        `json:"revision"`
	Email           *string       `json:"email"`
	IsAdmin         *bool         `json:"is_admin"`
	IsStaff         *bool         `json:"is_staff"`
	IsDistributor   *bool         `json:"is_distributor"`
	DistributorName *string       `json:"distributor_name"`
	GroupID         nullableInt64 `json:"group_id"`
	TransferEnable  *int64        `json:"transfer_enable"`
	ExpiredAt       nullableTime  `json:"expired_at"`
	SpeedLimit      *int          `json:"speed_limit"`
	DeviceLimit     *int          `json:"device_limit"`
	Banned          *bool         `json:"banned"`
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
	if input.DistributorName != nil || (input.IsDistributor != nil && *input.IsDistributor) {
		for field, message := range validateDistributorRoleFields(input.IsDistributor != nil && *input.IsDistributor, input.DistributorName, false) {
			fields[field] = message
		}
	}
	return fields
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
