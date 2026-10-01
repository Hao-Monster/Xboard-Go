package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/observability"
	"github.com/gorilla/websocket"
)

func diagnosticRecords(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var rows []map[string]any
	for {
		var row map[string]any
		err := dec.Decode(&row)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}
func TestDiagnosticsCorrelatesServerErrorsWithoutRequestSecrets(t *testing.T) {
	var out bytes.Buffer
	s := server{logger: observability.New(&out, slog.LevelDebug, "test", "revision"), diagnosticAdminPath: "SECRET-admin"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/SECRET-admin/user/{userID}", func(w http.ResponseWriter, r *http.Request) {
		handleStoreError(&responseStatusRecorder{ResponseWriter: w}, errors.New("SECRET-sql-payload"))
	})
	req := httptest.NewRequest("POST", "https://panel.test/api/v2/SECRET-admin/user/SECRET-user?token=SECRET-query", strings.NewReader("SECRET-body"))
	req.Header.Set("X-Request-ID", "SECRET-attacker-id")
	req.Header.Set("Authorization", "Bearer SECRET-bearer")
	w := httptest.NewRecorder()
	s.requestDiagnostics(s.recoverPanic(mux)).ServeHTTP(w, req)
	id := w.Header().Get("X-Request-ID")
	if len(id) != 32 || w.Code != 500 {
		t.Fatalf("status/id: %d %s", w.Code, id)
	}
	if strings.Contains(out.String(), "SECRET") {
		t.Fatalf("secret in logs: %s", out.String())
	}
	rows := diagnosticRecords(t, &out)
	if len(rows) != 2 {
		t.Fatalf("records: %v", rows)
	}
	for _, row := range rows {
		if row["request_id"] != id {
			t.Fatalf("missing correlation: %v", row)
		}
	}
	if rows[0]["error_type"] != "*errors.errorString" || !strings.Contains(rows[0]["stack"].(string), "diagnostics_test.go") {
		t.Fatalf("missing source: %v", rows[0])
	}
	if rows[1]["route"] != "POST /api/v2/{secure_admin}/user/{userID}" || rows[1]["status"] != float64(500) {
		t.Fatal(rows[1])
	}
}
func TestDiagnosticsPanicCapturesTypeAndFramesOnly(t *testing.T) {
	var out bytes.Buffer
	s := server{logger: observability.New(&out, slog.LevelDebug, "test", "rev")}
	h := s.requestDiagnostics(s.recoverPanic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("SECRET-panic-password") })))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/SECRET-path?secret=SECRET-query", nil))
	if w.Code != 500 || strings.Contains(out.String(), "SECRET") {
		t.Fatalf("status %d logs %s", w.Code, out.String())
	}
	rows := diagnosticRecords(t, &out)
	if rows[0]["error_type"] != "string" || rows[1]["route"] != "unmatched" {
		t.Fatal(rows)
	}
}
func TestDiagnosticsStreamingPreservesFlushAndStatus(t *testing.T) {
	var out bytes.Buffer
	s := server{logger: observability.New(&out, slog.LevelInfo, "test", "rev")}
	h := s.requestDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		if _, err := io.WriteString(w, "chunk"); err != nil {
			t.Fatal(err)
		}
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stream", nil))
	if !w.Flushed || w.Code != 201 || w.Body.String() != "chunk" {
		t.Fatalf("stream failed %+v", w)
	}
	rows := diagnosticRecords(t, &out)
	if rows[0]["status"] != float64(201) || rows[0]["bytes"] != float64(5) {
		t.Fatal(rows)
	}
}
func TestDiagnosticsWebSocketUpgradePreservesHijacking(t *testing.T) {
	var out bytes.Buffer
	s := server{logger: observability.New(&out, slog.LevelInfo, "test", "rev")}
	done := make(chan struct{})
	h := s.requestDiagnostics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, http.Header{"X-Request-ID": w.Header().Values("X-Request-ID")})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if err := conn.WriteMessage(websocket.TextMessage, []byte("connected")); err != nil {
			t.Error(err)
		}
	}))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ServeHTTP(w, r); close(done) }))
	defer ts.Close()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "connected" {
		t.Fatalf("message %s error %v", data, err)
	}
	<-done
	if len(resp.Header.Get("X-Request-ID")) != 32 {
		t.Fatal("missing upgrade correlation")
	}
	rows := diagnosticRecords(t, &out)
	if rows[0]["status"] != float64(101) {
		t.Fatal(rows)
	}
}
func TestDiagnosticsUnknownAndSubscriptionRoutesDoNotLogValues(t *testing.T) {
	var out bytes.Buffer
	s := server{logger: observability.New(&out, slog.LevelInfo, "test", "rev")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{subscriptionPath}/{subscriptionToken}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := s.requestDiagnostics(mux)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/SECRET-path/SECRET-token", nil))
	if strings.Contains(out.String(), "SECRET") {
		t.Fatal(out.String())
	}
}

func TestDiagnosticsAdminMutationIsCorrelatedAndPrivate(t *testing.T) {
	var out bytes.Buffer
	api, _ := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(d *Dependencies) { d.Logger = observability.New(&out, slog.LevelDebug, "test", "revision") })
	admin := loginAdmin(t, api)
	out.Reset()
	response := admin.request(t, api, "POST", "/api/v1/admin/admin/notices", `{"title":"SECRET-title","content":"SECRET-body","image_url":"","tags":[],"show":false}`)
	if response.Code != 201 {
		t.Fatalf("create status %d", response.Code)
	}
	var found bool
	for _, row := range diagnosticRecords(t, &out) {
		if row["msg"] == "admin.mutation" {
			found = true
			if row["request_id"] != response.Header().Get("X-Request-ID") || row["administrator_id"] == nil {
				t.Fatal(row)
			}
		}
	}
	if !found || strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), admin.csrf) {
		t.Fatalf("missing or unsafe audit: %s", out.String())
	}
}
func TestDiagnosticsNestedWrapperEmitsOneCompletion(t *testing.T) {
	var out bytes.Buffer
	l := observability.New(&out, slog.LevelInfo, "test", "rev")
	h := RequestDiagnostics(l, "", RequestDiagnostics(l, "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if len(diagnosticRecords(t, &out)) != 1 {
		t.Fatal(out.String())
	}
}
