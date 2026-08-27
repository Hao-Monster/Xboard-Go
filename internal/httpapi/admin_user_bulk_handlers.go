package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type adminUserBulkScopeRequest struct {
	Scope       string                `json:"scope"`
	UserIDs     []int64               `json:"user_ids"`
	EmailPrefix string                `json:"email_prefix"`
	Banned      *bool                 `json:"banned"`
	GroupID     *int64                `json:"group_id"`
	Filters     []adminUserFilterWire `json:"filters"`
}

type adminUserBulkMailRequest struct {
	adminUserBulkScopeRequest
	Subject string `json:"subject"`
	Content string `json:"content"`
}

type adminUserBulkBanRequest struct {
	adminUserBulkScopeRequest
	IdempotencyKey string `json:"idempotency_key"`
}

func (input adminUserBulkScopeRequest) storeScope(w http.ResponseWriter) (store.AdminUserBulkScope, bool) {
	if len(input.Filters) > 10 || input.GroupID != nil && *input.GroupID < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "批量操作筛选参数超出允许范围", nil)
		return store.AdminUserBulkScope{}, false
	}
	filter := store.AdminUserFilter{EmailPrefix: input.EmailPrefix, Banned: input.Banned, GroupID: input.GroupID}
	if !appendAdminUserWireFilters(w, &filter, input.Filters) {
		return store.AdminUserBulkScope{}, false
	}
	return store.AdminUserBulkScope{Scope: input.Scope, UserIDs: input.UserIDs, Filter: filter}, true
}

func (s *server) createAdminUserBulkMail(w http.ResponseWriter, r *http.Request) {
	var input adminUserBulkMailRequest
	if !decodeJSONLimit(w, r, &input, 96*1024) {
		return
	}
	scope, ok := input.adminUserBulkScopeRequest.storeScope(w)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.CreateAdminUserBulkJob(r.Context(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: session.UserID, Scope: scope,
		Subject: input.Subject, Content: input.Content,
	}, s.now())
	if err != nil {
		s.writeAdminUserBulkError(w, err)
		return
	}
	writeSuccess(w, http.StatusAccepted, publicAdminUserBulkJob(job))
}

func (s *server) createAdminUserBulkCSV(w http.ResponseWriter, r *http.Request) {
	var input adminUserBulkScopeRequest
	if !decodeJSONLimit(w, r, &input, 32*1024) {
		return
	}
	scope, ok := input.storeScope(w)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.CreateAdminUserBulkJob(r.Context(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindCSV, AdministratorID: session.UserID, Scope: scope,
	}, s.now())
	if err != nil {
		s.writeAdminUserBulkError(w, err)
		return
	}
	writeSuccess(w, http.StatusAccepted, publicAdminUserBulkJob(job))
}

func (s *server) banAdminUsers(w http.ResponseWriter, r *http.Request) {
	var input adminUserBulkBanRequest
	if !decodeJSONLimit(w, r, &input, 32*1024) {
		return
	}
	scope, ok := input.adminUserBulkScopeRequest.storeScope(w)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.BanAdminUsers(r.Context(), store.BanAdminUsersInput{
		AdministratorID: session.UserID, IdempotencyKey: input.IdempotencyKey, Scope: scope,
	}, s.now())
	if err != nil {
		s.writeAdminUserBulkError(w, err)
		return
	}
	job = s.notifyAndRecordAdminUserBulkBan(r, job)
	writeSuccess(w, http.StatusOK, publicAdminUserBulkJob(job))
}

func (s *server) listAdminUserBulkJobs(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1_000_000)
	if !ok {
		return
	}
	pageSize, ok := orderQueryInt(w, r, "page_size", 20, 100)
	if !ok {
		return
	}
	result, err := s.store.ListAdminUserBulkJobs(r.Context(), page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	items := make([]store.AdminUserBulkJob, len(result.Items))
	for index, job := range result.Items {
		items[index] = publicAdminUserBulkJob(job)
	}
	result.Items = items
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) getAdminUserBulkJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetAdminUserBulkJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, publicAdminUserBulkJob(job))
}

func (s *server) cancelAdminUserBulkJob(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.CancelAdminUserBulkJob(r.Context(), r.PathValue("jobID"), session.UserID, s.now())
	if err != nil {
		s.writeAdminUserBulkError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, publicAdminUserBulkJob(job))
}

func (s *server) downloadAdminUserBulkCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.bulkOperations == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "bulk_service_unavailable", "批量导出服务暂不可用", nil)
		return
	}
	file, job, err := s.bulkOperations.OpenCSV(r.Context(), r.PathValue("jobID"), s.now())
	if errors.Is(err, store.ErrAdminUserBulkExpired) {
		writeAPIError(w, http.StatusGone, "bulk_export_expired", "导出文件已过期，请重新导出", nil)
		return
	}
	if err != nil {
		s.writeAdminUserBulkError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadFilename(job.OutputFilename)+`"`)
	if job.OutputSize != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(*job.OutputSize, 10))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, file); err != nil && s.logger != nil {
		s.logger.Warn("stream administrator user CSV", "job_id", job.ID, "error", err)
	}
}

func publicAdminUserBulkJob(job store.AdminUserBulkJob) store.AdminUserBulkJob {
	job.Content = ""
	job.OutputRelativePath = ""
	return job
}

func safeDownloadFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n\"") {
		return "users.csv"
	}
	return value
}

func (s *server) writeAdminUserBulkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAdminUserBulkLimit):
		writeAPIError(w, http.StatusUnprocessableEntity, "bulk_target_limit_exceeded", "批量目标超过 10000 个，请缩小筛选范围", nil)
	case errors.Is(err, store.ErrMailUnavailable):
		writeAPIError(w, http.StatusConflict, "smtp_not_configured", "请先配置并启用 SMTP 邮件服务", nil)
	case errors.Is(err, store.ErrAdminUserBulkExpired):
		writeAPIError(w, http.StatusGone, "bulk_export_expired", "导出文件已过期，请重新导出", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "批量操作参数无效", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "bulk_job_conflict", "批量任务状态已变化，请刷新后重试", nil)
	default:
		handleStoreError(w, err)
	}
}

func (s *server) legacyAdminUserBulkMail(w http.ResponseWriter, r *http.Request) {
	input, scope, ok := legacyAdminUserBulkInput(w, r)
	if !ok {
		return
	}
	var subject, content string
	if json.Unmarshal(input["subject"], &subject) != nil || json.Unmarshal(input["content"], &content) != nil {
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "邮件主题和正文不能为空")
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.CreateAdminUserBulkJob(r.Context(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindMail, AdministratorID: session.UserID, Scope: scope, Subject: subject, Content: content,
	}, s.now()); err != nil {
		writeLegacyAdminUserBulkError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": true})
}

func (s *server) legacyAdminUserBulkCSV(w http.ResponseWriter, r *http.Request) {
	_, scope, ok := legacyAdminUserBulkInput(w, r)
	if !ok {
		return
	}
	if s.bulkOperations == nil {
		writeLegacyOrderFail(w, http.StatusServiceUnavailable, "导出服务暂不可用")
		return
	}
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.CreateAdminUserBulkJob(r.Context(), store.CreateAdminUserBulkJobInput{
		Kind: store.AdminUserBulkKindCSV, AdministratorID: session.UserID, Scope: scope,
	}, s.now())
	if err != nil {
		writeLegacyAdminUserBulkError(w, err)
		return
	}
	job, err = s.bulkOperations.ProcessCSVJob(r.Context(), job.ID, s.now())
	if err != nil {
		writeLegacyAdminUserBulkError(w, err)
		return
	}
	file, job, err := s.bulkOperations.OpenCSV(r.Context(), job.ID, s.now())
	if err != nil {
		writeLegacyAdminUserBulkError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "text/csv; charset=UTF-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadFilename(job.OutputFilename)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if job.OutputSize != nil {
		w.Header().Set("Content-Length", strconv.FormatInt(*job.OutputSize, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (s *server) legacyAdminUserBulkBan(w http.ResponseWriter, r *http.Request) {
	_, scope, ok := legacyAdminUserBulkInput(w, r)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	job, err := s.store.BanAdminUsers(r.Context(), store.BanAdminUsersInput{
		AdministratorID: session.UserID, IdempotencyKey: "legacy-" + uuid.NewString(), Scope: scope,
	}, s.now())
	if err != nil {
		writeLegacyAdminUserBulkError(w, err)
		return
	}
	_ = s.notifyAndRecordAdminUserBulkBan(r, job)
	writeJSON(w, http.StatusOK, map[string]any{"data": true})
}

func (s *server) notifyAndRecordAdminUserBulkBan(r *http.Request, job store.AdminUserBulkJob) store.AdminUserBulkJob {
	if s.hub == nil || job.SuccessCount == 0 {
		return job
	}
	if err := s.notifyAdminUserBulkBan(r, job.ID); err == nil {
		return job
	} else if s.logger != nil {
		s.logger.Warn("notify administrator bulk ban runtime state", "job_id", job.ID, "error_type", fmt.Sprintf("%T", err))
	}
	warned, err := s.store.MarkAdminUserBulkRuntimeWarning(r.Context(), job.ID, s.now())
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("record administrator bulk ban runtime warning", "job_id", job.ID, "error_type", fmt.Sprintf("%T", err))
		}
		return job
	}
	return warned
}

func (s *server) notifyAdminUserBulkBan(r *http.Request, jobID string) error {
	if s.hub == nil {
		return nil
	}
	targets := make([]store.AdminUserBulkTarget, 0)
	after := int64(0)
	for {
		page, err := s.store.ListAdminUserBulkTargets(r.Context(), jobID, after, 500)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			break
		}
		for _, target := range page {
			if target.Status == store.AdminUserBulkTargetSucceeded {
				targets = append(targets, target)
			}
			after = target.Sequence
		}
	}
	return s.hub.NotifyBulkUserRemoval(r.Context(), targets)
}

func legacyAdminUserBulkInput(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, store.AdminUserBulkScope, bool) {
	var input map[string]json.RawMessage
	if !decodeJSON(w, r, &input) {
		return nil, store.AdminUserBulkScope{}, false
	}
	var scopeName string
	if raw := input["scope"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &scopeName)
	}
	var userIDs []int64
	if raw := input["user_ids"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &userIDs) != nil {
			writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "user_ids格式无效")
			return nil, store.AdminUserBulkScope{}, false
		}
	}
	filter := store.AdminUserFilter{}
	var filters []struct {
		ID    string          `json:"id"`
		Value json.RawMessage `json:"value"`
	}
	if raw := input["filter"]; len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &filters) != nil || len(filters) > 10 {
			writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "筛选条件格式无效")
			return nil, store.AdminUserBulkScope{}, false
		}
	}
	for _, item := range filters {
		rule, ok := legacyAdminUserRule(item.ID, item.Value)
		if !ok {
			writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "不支持的用户筛选条件")
			return nil, store.AdminUserBulkScope{}, false
		}
		filter.Rules = append(filter.Rules, rule)
	}
	if scopeName == "" {
		switch {
		case len(userIDs) > 0:
			scopeName = store.AdminUserBulkScopeSelected
		case len(filter.Rules) > 0:
			scopeName = store.AdminUserBulkScopeFiltered
		default:
			scopeName = store.AdminUserBulkScopeAll
		}
	}
	return input, store.AdminUserBulkScope{Scope: scopeName, UserIDs: userIDs, Filter: filter}, true
}

func writeLegacyAdminUserBulkError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAdminUserBulkLimit):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "批量目标超过10000个，请缩小范围")
	case errors.Is(err, store.ErrMailUnavailable):
		writeLegacyOrderFail(w, http.StatusConflict, "请先配置并启用邮件服务")
	case errors.Is(err, store.ErrInvalidInput):
		writeLegacyOrderFail(w, http.StatusUnprocessableEntity, "批量操作参数无效")
	case errors.Is(err, store.ErrConflict):
		writeLegacyOrderFail(w, http.StatusConflict, "批量任务状态已变化")
	default:
		writeLegacyOrderFail(w, http.StatusInternalServerError, fmt.Sprintf("批量操作失败"))
	}
}
