package diagnostics_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
)

func TestQueryFiltersNormalize(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	t.Run("default_15_minutes", func(t *testing.T) {
		var f diagnostics.QueryFilters
		if err := f.Normalize(now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !f.From.Equal(now.Add(-15 * time.Minute)) {
			t.Errorf("expected from 15m ago, got %v", f.From)
		}
		if !f.To.Equal(now) {
			t.Errorf("expected to now, got %v", f.To)
		}
	})

	t.Run("from_after_to_rejected", func(t *testing.T) {
		f := diagnostics.QueryFilters{
			From: now,
			To:   now.Add(-1 * time.Minute),
		}
		if err := f.Normalize(now); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("window_exceeds_72_hours", func(t *testing.T) {
		f := diagnostics.QueryFilters{
			From: now.Add(-73 * time.Hour),
			To:   now,
		}
		if err := f.Normalize(now); err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("domain_validation_and_clean", func(t *testing.T) {
		dom := "  EXAMPLE.COM  "
		f := diagnostics.QueryFilters{
			Domain: &dom,
		}
		if err := f.Normalize(now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *f.Domain != "example.com" {
			t.Errorf("got %q, want 'example.com'", *f.Domain)
		}
		if f.DomainMatch != "contains" {
			t.Errorf("default match should be contains, got %q", f.DomainMatch)
		}

		badDom := "example.com; drop table"
		fBad := diagnostics.QueryFilters{Domain: &badDom}
		if err := fBad.Normalize(now); err == nil {
			t.Fatal("expected error for forbidden characters, got nil")
		}
	})

	t.Run("trace_id_validation", func(t *testing.T) {
		validTrace := "abcdef0123456789abcdef0123456789"
		f := diagnostics.QueryFilters{TraceID: &validTrace}
		if err := f.Normalize(now); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		invalidTrace := "not-a-hex"
		fInv := diagnostics.QueryFilters{TraceID: &invalidTrace}
		if err := fInv.Normalize(now); err == nil {
			t.Fatal("expected error for invalid trace_id, got nil")
		}
	})
}

func TestEncodeLogsQL(t *testing.T) {
	panelScope := "00000000-0000-4000-8000-000000000001"
	diagID := "111111111111111111111111"
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	nodeID := int64(102)
	traceID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	f := diagnostics.QueryFilters{
		From:    now.Add(-10 * time.Minute),
		To:      now,
		NodeID:  &nodeID,
		TraceID: &traceID,
	}

	query, err := diagnostics.EncodeLogsQL(panelScope, diagID, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(query, `panel_scope:"00000000-0000-4000-8000-000000000001"`) {
		t.Errorf("missing panel_scope in query: %s", query)
	}
	if !strings.Contains(query, `diagnostic_id:"111111111111111111111111"`) {
		t.Errorf("missing diagnostic_id in query: %s", query)
	}
	if !strings.Contains(query, `node_id:102`) {
		t.Errorf("missing node_id in query: %s", query)
	}
	if !strings.Contains(query, `trace_id:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`) {
		t.Errorf("missing trace_id in query: %s", query)
	}
	if !strings.Contains(query, fmt.Sprintf("limit %d", diagnostics.MaxQueryReadLines+1)) {
		t.Errorf("missing limit in query: %s", query)
	}
}

func TestAggregateConnectionEvents(t *testing.T) {
	// Synthesize an NDJSON event stream with 2 connections:
	// Conn 1: TCP connection to www.example.com:443 (success, has DNS resolver op)
	// Conn 2: TCP connection to api.fail.test:443 (failure, has_error=true, dial stage, connect_refused)
	stream := `
{"event_time":"2026-10-24T12:00:00.000000Z","event_id":"ev1","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":"diag1","connection_id":"conn1","target_id":0,"seq":1,"inbound_protocol":"vless","network":"tcp","destination_host":"www.example.com","destination_port":443}
{"event_time":"2026-10-24T12:00:00.050000Z","event_id":"ev2","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.dns.result","diagnostic_id":"diag1","connection_id":"conn1","target_id":0,"seq":2,"resolver_operation_id":"resop1","span_links":["dns.exchange:resop1"]}
{"event_time":"2026-10-24T12:00:00.200000Z","event_id":"ev3","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.relay.result","diagnostic_id":"diag1","connection_id":"conn1","target_id":0,"seq":3,"upload_bytes":1024,"download_bytes":4096,"transport_state":"transport_established"}
{"event_time":"2026-10-24T12:00:01.000000Z","event_id":"ev4","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.close","diagnostic_id":"diag1","connection_id":"conn1","target_id":0,"seq":4}
{"event_time":"2026-10-24T12:01:00.000000Z","event_id":"ev5","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.connection.open","diagnostic_id":"diag1","connection_id":"conn2","target_id":0,"seq":1,"inbound_protocol":"vless","network":"tcp","destination_host":"api.fail.test","destination_port":443}
{"event_time":"2026-10-24T12:01:00.100000Z","event_id":"ev6","event_family":"connection","panel_scope":"00000000-0000-4000-8000-000000000001","node_id":1,"runtime_id":"rt1","event":"diag.dial.result","diagnostic_id":"diag1","connection_id":"conn2","target_id":0,"seq":2,"has_error":true,"error_stages":["dial"],"reason_codes":["connect_refused"],"last_error_stage":"dial","last_reason_code":"connect_refused"}
`

	t.Run("aggregate_all", func(t *testing.T) {
		res, err := diagnostics.AggregateConnectionEvents(strings.NewReader(stream), diagnostics.QueryFilters{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Summaries) != 2 {
			t.Fatalf("expected 2 summaries, got %d", len(res.Summaries))
		}
		if res.ScanTruncated {
			t.Error("expected scan_truncated = false")
		}

		// Most recent connection (conn2) should be first
		first := res.Summaries[0]
		if first.ConnectionID != "conn2" {
			t.Errorf("expected first to be conn2, got %s", first.ConnectionID)
		}
		if !first.HasError || first.LastErrorStage != "dial" || first.LastReasonCode != "connect_refused" {
			t.Errorf("unexpected error state for conn2: %+v", first)
		}

		second := res.Summaries[1]
		if second.ConnectionID != "conn1" {
			t.Errorf("expected second to be conn1, got %s", second.ConnectionID)
		}
		if second.HasError {
			t.Errorf("expected conn1 has_error=false")
		}
		if len(second.DNSResolverOperationIDs) != 1 || second.DNSResolverOperationIDs[0] != "resop1" {
			t.Errorf("expected DNS resolver op 'resop1', got %v", second.DNSResolverOperationIDs)
		}
		if second.ClosedAt == nil {
			t.Error("expected closed_at to be set on conn1")
		}
		if second.UploadBytes != 1024 || second.DownloadBytes != 4096 {
			t.Errorf("unexpected bytes on conn1: up=%d down=%d", second.UploadBytes, second.DownloadBytes)
		}
	})

	t.Run("filter_domain", func(t *testing.T) {
		dom := "fail.test"
		f := diagnostics.QueryFilters{
			Domain:      &dom,
			DomainMatch: "suffix",
		}
		res, err := diagnostics.AggregateConnectionEvents(strings.NewReader(stream), f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Summaries) != 1 || res.Summaries[0].ConnectionID != "conn2" {
			t.Fatalf("expected only conn2, got %d summaries", len(res.Summaries))
		}
	})

	t.Run("filter_error_only", func(t *testing.T) {
		hasErr := true
		f := diagnostics.QueryFilters{
			HasError: &hasErr,
		}
		res, err := diagnostics.AggregateConnectionEvents(strings.NewReader(stream), f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Summaries) != 1 || res.Summaries[0].ConnectionID != "conn2" {
			t.Fatalf("expected only conn2, got %d summaries", len(res.Summaries))
		}
	})
}

func TestCursorGenerateAndVerify(t *testing.T) {
	secret := []byte("super-secret-key-for-cursor-signing-32bytes!")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	adminID := int64(1)
	orderID := int64(42)
	diagID := "111111111111111111111111"
	scope := "00000000-0000-4000-8000-000000000001"
	gen := int64(1)

	payload := diagnostics.CursorPayload{
		AdminID:           adminID,
		OrderID:           &orderID,
		DiagnosticID:      diagID,
		PanelScope:        scope,
		SnapshotID:        "snap1",
		StorageGeneration: gen,
		Offset:            100,
		CreatedAt:         now,
	}

	cursorStr, err := diagnostics.GenerateCursor(secret, payload)
	if err != nil {
		t.Fatalf("failed to generate cursor: %v", err)
	}

	t.Run("valid_cursor", func(t *testing.T) {
		parsed, err := diagnostics.ParseAndVerifyCursor(secret, cursorStr, adminID, scope, diagID, gen, now.Add(2*time.Minute))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if parsed.Offset != 100 || parsed.SnapshotID != "snap1" {
			t.Errorf("unexpected parsed payload: %+v", parsed)
		}
	})

	t.Run("expired_cursor", func(t *testing.T) {
		_, err := diagnostics.ParseAndVerifyCursor(secret, cursorStr, adminID, scope, diagID, gen, now.Add(6*time.Minute))
		if err == nil || err != diagnostics.ErrCursorExpired {
			t.Fatalf("expected ErrCursorExpired, got %v", err)
		}
	})

	t.Run("tampered_cursor", func(t *testing.T) {
		tampered := cursorStr[:len(cursorStr)-4] + "AAAA"
		_, err := diagnostics.ParseAndVerifyCursor(secret, tampered, adminID, scope, diagID, gen, now)
		if err == nil || err != diagnostics.ErrCursorInvalid {
			t.Fatalf("expected ErrCursorInvalid, got %v", err)
		}
	})

	t.Run("mismatched_scope_or_admin", func(t *testing.T) {
		_, err := diagnostics.ParseAndVerifyCursor(secret, cursorStr, 999, scope, diagID, gen, now)
		if err == nil || err != diagnostics.ErrCursorInvalid {
			t.Fatalf("expected ErrCursorInvalid for wrong admin, got %v", err)
		}
	})
}

func TestSnapshotCache(t *testing.T) {
	cache := diagnostics.NewSnapshotCache()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	adminID := int64(1)

	// Put 3 snapshots
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("snap_%d", i)
		cache.Put(adminID, id, []diagnostics.ConnectionSummary{{ConnectionID: id}}, false, now)
	}

	// Verify all 3 exist
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("snap_%d", i)
		_, _, ok := cache.Get(adminID, id, now)
		if !ok {
			t.Fatalf("expected %s to exist", id)
		}
	}

	// Put a 4th snapshot -> oldest (snap_1) should be evicted
	cache.Put(adminID, "snap_4", []diagnostics.ConnectionSummary{{ConnectionID: "snap_4"}}, false, now)

	_, _, ok1 := cache.Get(adminID, "snap_1", now)
	if ok1 {
		t.Error("snap_1 should have been evicted")
	}
	_, _, ok4 := cache.Get(adminID, "snap_4", now)
	if !ok4 {
		t.Error("snap_4 should exist")
	}

	// Query with expired time -> should be gone
	_, _, okAfter5m := cache.Get(adminID, "snap_4", now.Add(6*time.Minute))
	if okAfter5m {
		t.Error("snap_4 should have expired after 5m")
	}
}

func TestStorageClient(t *testing.T) {
	// Spin up a mock VictoriaLogs server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/select/logsql/query" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		r.ParseForm()
		q := r.FormValue("query")
		if strings.Contains(q, "trigger_503") {
			http.Error(w, "storage degraded", http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/stream+json")
		fmt.Fprintf(w, `{"event_time":"2026-10-24T12:00:00Z","event_id":"e1","event_family":"connection","connection_id":"c1"}`+"\n")
	}))
	defer server.Close()

	client := diagnostics.NewStorageClient()
	ctx := context.Background()

	t.Run("successful_query", func(t *testing.T) {
		rc, err := client.Query(ctx, server.URL, "user", "pass", "panel_scope:test")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer rc.Close()
		data, _ := io.ReadAll(rc)
		if !bytes.Contains(data, []byte(`"connection_id":"c1"`)) {
			t.Errorf("unexpected response: %s", string(data))
		}
	})

	t.Run("storage_error_mapping", func(t *testing.T) {
		_, err := client.Query(ctx, server.URL, "", "", "trigger_503")
		if err == nil || !strings.Contains(err.Error(), "upstream status 503") {
			t.Fatalf("expected 503 upstream error, got: %v", err)
		}
	})
}

func TestStorageClient_Health(t *testing.T) {
	mux := http.NewServeMux()
	var (
		state       = "ready"
		latched     = false
		checkAgeSec = int64(10)
		panelScope  = "scope-valid"
		returnCode  = http.StatusOK
	)

	mux.HandleFunc("/diagnostics/health", func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		if user != "user" || pass != "pass" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(returnCode)
		json.NewEncoder(w).Encode(diagnostics.StorageHealthResponse{
			SchemaVersion:      1,
			PanelScope:         panelScope,
			StorageID:          "store-1234",
			State:              state,
			ReasonCodes:        []string{"healthy"},
			CheckedAtUTC:       "2026-10-25T00:00:00Z",
			CheckAgeSeconds:    checkAgeSec,
			Latched:            latched,
			DataUsedBytes:      10 * 1024 * 1024 * 1024,
			DataAvailableBytes: 20 * 1024 * 1024 * 1024,
			RootAvailableBytes: 80 * 1024 * 1024 * 1024,
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client := diagnostics.NewStorageClient()
	ctx := context.Background()

	t.Run("get_health_and_check_health_ok", func(t *testing.T) {
		h, err := client.GetHealth(ctx, server.URL, "user", "pass")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if h.State != "ready" || h.PanelScope != "scope-valid" || h.DataUsedBytes == 0 || h.Latched {
			t.Errorf("unexpected health: %+v", h)
		}

		if err := client.CheckHealth(ctx, server.URL, "user", "pass", "scope-valid"); err != nil {
			t.Fatalf("expected CheckHealth to succeed, got: %v", err)
		}
	})

	t.Run("check_health_latched_rejected", func(t *testing.T) {
		latched = true
		defer func() { latched = false }()
		err := client.CheckHealth(ctx, server.URL, "user", "pass", "scope-valid")
		if !errors.Is(err, diagnostics.ErrDiagnosticsStorageNotReady) {
			t.Fatalf("expected ErrDiagnosticsStorageNotReady, got: %v", err)
		}
	})

	t.Run("check_health_stale_rejected", func(t *testing.T) {
		checkAgeSec = 65
		defer func() { checkAgeSec = 10 }()
		err := client.CheckHealth(ctx, server.URL, "user", "pass", "scope-valid")
		if !errors.Is(err, diagnostics.ErrDiagnosticsStorageNotReady) {
			t.Fatalf("expected ErrDiagnosticsStorageNotReady, got: %v", err)
		}
	})

	t.Run("check_health_scope_mismatch_rejected", func(t *testing.T) {
		err := client.CheckHealth(ctx, server.URL, "user", "pass", "other-scope")
		if !errors.Is(err, diagnostics.ErrDiagnosticsStorageNotReady) {
			t.Fatalf("expected ErrDiagnosticsStorageNotReady on scope mismatch, got: %v", err)
		}
	})

	t.Run("check_health_auth_failure", func(t *testing.T) {
		err := client.CheckHealth(ctx, server.URL, "wrong", "pass", "scope-valid")
		if !errors.Is(err, diagnostics.ErrDiagnosticsStorageNotReady) {
			t.Fatalf("expected ErrDiagnosticsStorageNotReady on 401, got: %v", err)
		}
	})
}
