package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

	keyPath := filepath.Join(directory, "backup-key.txt")
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	encryptedPath := filepath.Join(directory, "offsite", "backup.xbbackup.enc")
	encrypted := runBackupCommand(t, []string{"backup", "encrypt", "--input", archivePath, "--output", encryptedPath, "--key-file", keyPath}, now)
	if encrypted.Action != "backup.encrypt" || encrypted.Path != encryptedPath || encrypted.Manifest != created.Manifest ||
		encrypted.EncryptedManifest == nil || encrypted.EncryptedManifest.BackupManifest != created.Manifest {
		t.Fatalf("encrypt output = %#v, want manifest %#v", encrypted, created.Manifest)
	}
	verifiedEncrypted := runBackupCommand(t, []string{"backup", "verify-encrypted", "--input", encryptedPath, "--key-file", keyPath}, now)
	if verifiedEncrypted.Action != "backup.verify-encrypted" || verifiedEncrypted.Manifest != created.Manifest ||
		verifiedEncrypted.EncryptedManifest == nil || *verifiedEncrypted.EncryptedManifest != *encrypted.EncryptedManifest {
		t.Fatalf("verify-encrypted output = %#v, want encrypted manifest %#v", verifiedEncrypted, encrypted.EncryptedManifest)
	}
	decryptedPath := filepath.Join(directory, "decrypted.xbbackup")
	decrypted := runBackupCommand(t, []string{"backup", "decrypt", "--input", encryptedPath, "--output", decryptedPath, "--key-file", keyPath}, now)
	if decrypted.Action != "backup.decrypt" || decrypted.Path != decryptedPath || decrypted.Manifest != created.Manifest ||
		decrypted.EncryptedManifest == nil || *decrypted.EncryptedManifest != *encrypted.EncryptedManifest {
		t.Fatalf("decrypt output = %#v, want encrypted manifest %#v", decrypted, encrypted.EncryptedManifest)
	}

	var remoteReplica []byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			body, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(response, "read body", http.StatusInternalServerError)
				return
			}
			remoteReplica = append(remoteReplica[:0], body...)
			response.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			if len(remoteReplica) == 0 {
				http.NotFound(response, request)
				return
			}
			_, _ = response.Write(remoteReplica)
		default:
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	putURLFile := filepath.Join(directory, "put-url.txt")
	getURLFile := filepath.Join(directory, "get-url.txt")
	for _, file := range []string{putURLFile, getURLFile} {
		if err := os.WriteFile(file, []byte(server.URL+"/replica"), 0o600); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(file, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	uploaded := runBackupCommand(t, []string{
		"backup", "upload-http", "--input", encryptedPath, "--put-url-file", putURLFile,
		"--allow-insecure-http", "--confirm-independent-storage",
	}, now)
	if uploaded.Action != "backup.upload-http" || uploaded.Path != encryptedPath || uploaded.Bytes <= 0 || len(uploaded.SHA256) != 64 {
		t.Fatalf("upload-http output = %#v", uploaded)
	}
	downloadedPath := filepath.Join(directory, "remote.xbbackup.enc")
	downloaded := runBackupCommand(t, []string{
		"backup", "download-http", "--get-url-file", getURLFile, "--output", downloadedPath, "--allow-insecure-http",
	}, now)
	if downloaded.Action != "backup.download-http" || downloaded.Path != downloadedPath ||
		downloaded.Bytes != uploaded.Bytes || downloaded.SHA256 != uploaded.SHA256 {
		t.Fatalf("download-http output = %#v, want transfer %#v", downloaded, uploaded)
	}
	downloadVerified := runBackupCommand(t, []string{"backup", "verify-encrypted", "--input", downloadedPath, "--key-file", keyPath}, now)
	if downloadVerified.EncryptedManifest == nil || *downloadVerified.EncryptedManifest != *encrypted.EncryptedManifest {
		t.Fatalf("downloaded encrypted manifest = %#v, want %#v", downloadVerified.EncryptedManifest, encrypted.EncryptedManifest)
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
		{"backup", "upload-http", "--input", result.Path, "--put-url-file", filepath.Join(directory, "url.txt")},
		{"backup", "download-http", "--output", filepath.Join(directory, "copy.xbbackup")},
		{"backup", "encrypt", "--input", result.Path, "--output", filepath.Join(directory, "copy.xbbackup.enc")},
		{"backup", "verify-encrypted", "--input", filepath.Join(directory, "copy.xbbackup.enc")},
		{"backup", "decrypt", "--input", filepath.Join(directory, "copy.xbbackup.enc"), "--output", filepath.Join(directory, "copy.xbbackup")},
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
	Status            string                    `json:"status"`
	Action            string                    `json:"action"`
	Path              string                    `json:"path"`
	Bytes             int64                     `json:"bytes"`
	SHA256            string                    `json:"sha256"`
	Manifest          backup.Manifest           `json:"manifest"`
	EncryptedManifest *backup.EncryptedManifest `json:"encrypted_manifest"`
}
