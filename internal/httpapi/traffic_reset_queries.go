package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listTrafficResetLogs(w http.ResponseWriter, r *http.Request) {
	page, ok := orderQueryInt(w, r, "page", 1, 1000000)
	if !ok {
		return
	}
	size, ok := orderQueryInt(w, r, "per_page", 20, 10000)
	if !ok {
		return
	}
	f := store.TrafficResetFilter{Page: page, PageSize: size, Email: r.URL.Query().Get("user_email"), ResetType: r.URL.Query().Get("reset_type"), Source: r.URL.Query().Get("trigger_source")}
	if raw := r.URL.Query().Get("user_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 {
			handleStoreError(w, store.ErrInvalidInput)
			return
		}
		f.UserID = id
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		handleStoreError(w, err)
		return
	}
	for key, target := range map[string]**time.Time{"start_date": &f.Start, "end_date": &f.End} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := time.ParseInLocation("2006-01-02", raw, location)
			if err != nil {
				handleStoreError(w, store.ErrInvalidInput)
				return
			}
			if key == "end_date" {
				value = value.AddDate(0, 0, 1)
			}
			*target = &value
		}
	}
	result, err := s.store.ListTrafficResetLogs(r.Context(), f)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	data := make([]map[string]any, 0, len(result.Items))
	names := map[string]string{"monthly": "按月重置", "first_day_month": "每月1号重置", "yearly": "按年重置", "first_day_year": "每年1月1日重置", "manual": "手动重置", "purchase": "购买重置"}
	for _, e := range result.Items {
		before, after := legacyTrafficTotal(e.UploadBefore, e.DownloadBefore), legacyTrafficTotal(e.UploadAfter, e.DownloadAfter)
		_, sourceName := legacyTrafficResetSource(e.Source)
		var metadata any
		if e.Reason != "" || e.AdministratorID != nil || e.AdministratorEmail != nil {
			metadata = map[string]any{"reason": e.Reason, "admin_id": e.AdministratorID, "admin_email": e.AdministratorEmail}
		}
		data = append(data, map[string]any{"id": e.ID, "user_id": e.UserID, "user_email": e.Email, "reset_type": e.ResetType, "reset_type_name": names[e.ResetType], "reset_time": e.ResetAt, "created_at": e.ResetAt, "old_traffic": map[string]any{"upload": e.UploadBefore, "download": e.DownloadBefore, "total": before, "formatted": legacyFormattedTraffic(before)}, "new_traffic": map[string]any{"upload": e.UploadAfter, "download": e.DownloadAfter, "total": after, "formatted": legacyFormattedTraffic(after)}, "trigger_source": e.Source, "trigger_source_name": sourceName, "metadata": metadata})
	}
	payload := map[string]any{"data": data, "pagination": map[string]any{"current_page": page, "last_page": max(int64(1), (result.Total+int64(size)-1)/int64(size)), "per_page": size, "total": result.Total}}
	if strings.HasPrefix(r.URL.Path, "/api/v2/") {
		writeJSON(w, http.StatusOK, payload)
	} else {
		writeSuccess(w, http.StatusOK, payload)
	}
}
func (s *server) trafficResetStats(w http.ResponseWriter, r *http.Request) {
	days, ok := orderQueryInt(w, r, "days", 30, 365)
	if !ok {
		return
	}
	result, err := s.store.GetTrafficResetStats(r.Context(), days, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}
