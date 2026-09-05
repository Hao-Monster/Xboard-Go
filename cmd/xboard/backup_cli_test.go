package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestRunCommandBackupCreateVerifyAndRestore(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	now := time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC)
	archivePath := filepath.Join(directory, "backup.xbbackup")
	restoredPath := filepath.Join(directory, "restored.db")

	created := runBackupCommand(t, []string{"backup", "create", "--output", archivePath}, now)
	if created.Action != "backup.create" || created.Path != archivePath || created.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("create output = %#v", created)
	}
	verified := runBackupCommand(t, []string{"backup", "verify", "--input", archivePath}, now)
	if verified.Action != "backup.verify" || verified.Manifest != created.Manifest {
		t.Fatalf("verify output = %#v, want manifest %#v", verified, created.Manifest)
	}
	restored := runBackupCommand(t, []string{"backup", "restore", "--input", archivePath, "--output", restoredPath}, now)
	if restored.Action != "backup.restore" || restored.Path != restoredPath || restored.Manifest != created.Manifest {
		t.Fatalf("restore output = %#v", restored)
	}
	replicaPath := filepath.Join(directory, "independent", "replica.xbbackup")
	replicated := runBackupCommand(t, []string{
		"backup", "replicate", "--input", archivePath, "--output", replicaPath, "--confirm-independent-storage",
	}, now)
	if replicated.Action != "backup.replicate" || replicated.Path != replicaPath ||
		replicated.Manifest != created.Manifest || replicated.Bytes <= 0 || len(replicated.SHA256) != 64 {
		t.Fatalf("replicate output = %#v, want manifest %#v", replicated, created.Manifest)
	}
}

func TestRunCommandBackupUsesPrivateTimestampedDefaultAndRejectsInvalidArguments(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	t.Setenv("XBOARD_BACKUP_DIRECTORY", filepath.Join(directory, "backups"))
	now := time.Date(2026, 8, 25, 13, 14, 15, 0, time.UTC)
	result := runBackupCommand(t, []string{"backup", "create"}, now)
	if filepath.Base(result.Path) != "xboard-20260825T131415Z.xbbackup" {
		t.Fatalf("default backup path = %q", result.Path)
	}

	for _, arguments := range [][]string{
		{"backup"}, {"backup", "unknown"}, {"backup", "verify"}, {"backup", "restore", "--input", result.Path},
		{"backup", "replicate"},
		{"backup", "replicate", "--input", result.Path, "--output", filepath.Join(directory, "copy.xbbackup")},
		{"backup", "create", "unexpected"}, {"unknown"},
	} {
		var stdout, stderr bytes.Buffer
		handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
		if !handled || err == nil {
			t.Fatalf("runCommand(%q) = handled %v error %v", arguments, handled, err)
		}
		if stdout.Len() != 0 {
			t.Fatalf("runCommand(%q) wrote success output on failure: %q", arguments, stdout.String())
		}
	}
}

func runBackupCommand(t *testing.T, arguments []string, now time.Time) commandOutput {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), arguments, &stdout, &stderr, func() time.Time { return now })
	if err != nil || !handled {
		t.Fatalf("runCommand(%q) = handled %v error %v stderr=%q", arguments, handled, err, stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("runCommand(%q) stderr = %q", arguments, stderr.String())
	}
	var output commandOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("decode runCommand(%q) output %q: %v", arguments, stdout.String(), err)
	}
	return output
}

type commandOutput struct {
	Status   string          `json:"status"`
	Action   string          `json:"action"`
	Path     string          `json:"path"`
	Bytes    int64           `json:"bytes"`
	SHA256   string          `json:"sha256"`
	Manifest backup.Manifest `json:"manifest"`
}
