package observability

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

type fixtureSQLState struct{ state string }

func (e fixtureSQLState) Error() string    { panic("must not render error text") }
func (e fixtureSQLState) SQLState() string { return e.state }

type fixtureDatabaseCode struct{}

func (fixtureDatabaseCode) Error() string { panic("must not render error text") }
func (fixtureDatabaseCode) Code() int     { return 2067 }
func TestSafeErrorClassificationDoesNotRenderSensitiveFields(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		class, code string
	}{
		{"cancellation", context.Canceled, "context", "canceled"},
		{"wrapped deadline", fmt.Errorf("SECRET-url: %w", context.DeadlineExceeded), "context", "deadline_exceeded"},
		{"missing file", &os.PathError{Op: "open", Path: "SECRET-path", Err: os.ErrNotExist}, "filesystem", "not_exist"},
		{"permission", os.ErrPermission, "filesystem", "permission_denied"},
		{"no rows", sql.ErrNoRows, "database", "no_rows"},
		{"finished transaction", sql.ErrTxDone, "database", "transaction_done"},
		{"network", &net.OpError{Op: "dial", Addr: &net.TCPAddr{IP: net.IPv4(203, 0, 113, 123), Port: 443}, Err: syscall.ECONNREFUSED}, "network", fmt.Sprintf("errno_%d", uint64(syscall.ECONNREFUSED))},
		{"unsafe network operation", &net.OpError{Op: "SECRET-operation", Err: errors.New("SECRET-payload")}, "network", "operation_failed"},
		{"SQL state", fixtureSQLState{"23505"}, "database", "sqlstate_23505"},
		{"unsafe SQL state", fixtureSQLState{"SECRET-state"}, "internal", "unknown"},
		{"database code", fixtureDatabaseCode{}, "database", "code_2067"},
		{"unknown", errors.New("SECRET-SQL-password"), "internal", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			l := New(&out, slog.LevelDebug, "test", "revision")
			l.Error("known static event", "error", tt.err)
			if strings.Contains(out.String(), "SECRET") || strings.Contains(out.String(), "203.0.113.123") {
				t.Fatal(out.String())
			}
			var row map[string]any
			if err := json.Unmarshal(out.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			if row["error_class"] != tt.class || row["error_code"] != tt.code {
				t.Fatalf("classification %v", row)
			}
			if tt.name == "unsafe network operation" && row["network_op"] != "other" {
				t.Fatal(row)
			}
		})
	}
}
