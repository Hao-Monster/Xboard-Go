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
