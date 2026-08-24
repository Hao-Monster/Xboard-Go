package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareSQLiteDirectory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nested", "xboard.db")
	if err := prepareSQLiteDirectory("file:" + databasePath); err != nil {
		t.Fatalf("prepareSQLiteDirectory() error = %v", err)
	}
	info, err := os.Stat(filepath.Dir(databasePath))
	if err != nil || !info.IsDir() {
		t.Fatalf("database directory was not created: info=%v err=%v", info, err)
	}
	if err := prepareSQLiteDirectory("file:memory?mode=memory&cache=shared"); err != nil {
		t.Fatalf("memory DSN should be a no-op: %v", err)
	}
}

func TestRunHealthcheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	t.Setenv("XBOARD_HEALTH_URL", server.URL)
	if err := runHealthcheck(); err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}
}
