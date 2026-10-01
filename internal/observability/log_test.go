package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestSafeLoggerRedactsErrorsAndSensitiveAttributes(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, slog.LevelDebug, "internal-test", "revision-fixture")
	logger.With("password", "SECRET-password").ErrorContext(WithRequestID(context.Background(), "server-generated-id"), "database.failure",
		"error", errors.New("SQL SELECT SECRET-query password=SECRET-value"), "email", "SECRET-email", "path", "/SECRET-admin", "headers", map[string]string{"Authorization": "SECRET-bearer"}, "body", []byte("SECRET-body"), "machine_id", int64(42))
	if strings.Contains(out.String(), "SECRET") {
		t.Fatalf("secret logged: %s", out.String())
	}
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{"service": "xboard-go", "environment": "internal-test", "revision": "revision-fixture", "request_id": "server-generated-id", "error_type": "*errors.errorString", "machine_id": float64(42)} {
		if record[k] != v {
			t.Fatalf("%s = %v", k, record[k])
		}
	}
}
func TestLogLevelFiltersDebugEvents(t *testing.T) {
	var out bytes.Buffer
	l := New(&out, slog.LevelWarn, "test", "local")
	l.Info("filtered")
	l.Warn("kept")
	if strings.Contains(out.String(), "filtered") || !strings.Contains(out.String(), "kept") {
		t.Fatal(out.String())
	}
}

func TestWebSocketNumericDiagnosticsSurviveFiltering(t *testing.T) {
	var out bytes.Buffer
	logger := New(&out, slog.LevelInfo, "test", "fixture")
	logger.Info("draining node websockets", "connections", 1, "active_connections", int64(1), "peak_connections", uint64(2), "replacements", int64(1))
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]float64{"connections": 1, "active_connections": 1, "peak_connections": 2, "replacements": 1} {
		if record[key] != want {
			t.Errorf("%s = %v, want %v", key, record[key], want)
		}
	}
	out.Reset()
	logger.Info("draining node websockets", "connections", "secret", "active_connections", "secret", "peak_connections", "secret", "replacements", "secret")
	if strings.Contains(out.String(), "secret") {
		t.Fatal("non-numeric diagnostics leaked")
	}
}
