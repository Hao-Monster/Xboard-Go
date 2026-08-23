package main

import (
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
