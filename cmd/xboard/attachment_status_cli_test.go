package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestKnowledgeAttachmentStatusCommandReportsHealthyCapacity(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "xboard.db")
	database, err := store.OpenSQLite("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+databasePath)
	t.Setenv("XBOARD_ATTACHMENT_ROOT", filepath.Join(directory, "attachments"))
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))

	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{"knowledge-attachments", "status"}, &stdout, &stderr, func() time.Time {
		return time.Date(2026, 8, 27, 2, 0, 0, 0, time.UTC)
	})
	if err != nil || !handled || stderr.Len() != 0 {
		t.Fatalf("status command = handled %v error %v stderr=%q", handled, err, stderr.String())
	}
	var result attachmentStatusCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Action != "knowledge-attachments.status" || !result.Result.Healthy ||
		result.Result.UsedBytes != 0 || result.Result.ReservedBytes != 0 || result.Result.QuotaAvailableBytes != result.Result.QuotaBytes {
		t.Fatalf("status result = %#v", result)
	}
}
