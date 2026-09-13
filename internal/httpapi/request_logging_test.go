package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestLoggerAssignsCorrelationIDAndNeverLogsQuery(t *testing.T) {
	var logs bytes.Buffer
	server := &server{logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	handler := server.requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestID(r.Context()); got == "" {
			t.Fatal("request id missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/client/subscribe?token=do-not-log&uuid=do-not-log", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" {
		t.Fatal("response request id missing")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	logged := logs.String()
	if strings.Contains(logged, "do-not-log") || strings.Contains(logged, "token=") || strings.Contains(logged, "uuid=") {
		t.Fatalf("request query leaked into logs: %s", logged)
	}
	if !strings.Contains(logged, requestID) || !strings.Contains(logged, "path=/api/v1/client/subscribe") {
		t.Fatalf("request log missing correlation/path fields: %s", logged)
	}
}
