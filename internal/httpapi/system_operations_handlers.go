package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const workerHealthyWindow = 2 * time.Minute

type systemStatusResponse struct {
	StartedAt     time.Time                  `json:"started_at"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	SchemaVersion int                        `json:"schema_version"`
	Scheduler     operations.ComponentStatus `json:"scheduler"`
	MailWorker    operations.ComponentStatus `json:"mail_worker"`
	MailQueue     store.SystemQueueStats     `json:"mail_queue"`
}

func (s *server) getSystemStatus(w http.ResponseWriter, r *http.Request) {
	schemaVersion, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	queue, err := s.store.GetSystemQueueStats(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	snapshot := s.runtimeTracker.Snapshot(s.now(), workerHealthyWindow)
	writeSuccess(w, http.StatusOK, systemStatusResponse{
		StartedAt: snapshot.StartedAt, UptimeSeconds: int64(snapshot.Uptime / time.Second),
		SchemaVersion: schemaVersion, Scheduler: snapshot.Scheduler, MailWorker: snapshot.MailWorker, MailQueue: queue,
	})
}

func (s *server) listAdminAudit(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := ticketPageQuery(w, r)
	if !ok {
		return
	}
	method := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("method")))
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	result, err := s.store.ListAdminAuditLogs(r.Context(), store.AdminAuditFilter{
		Page: page, PageSize: pageSize, Method: method, Query: query,
	})
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}

func (s *server) listTicketMailFailures(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := ticketPageQuery(w, r)
	if !ok {
		return
	}
	result, err := s.store.ListTicketMailFailures(r.Context(), page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}
