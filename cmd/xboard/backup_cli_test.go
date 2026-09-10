package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	for _, path := range []string{putURLFile, getURLFile} {
		if err := os.WriteFile(path, []byte(server.URL+"/replica"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	uploaded := runBackupCommand(t, []string{
		"backup", "upload-http", "--input", archivePath, "--put-url-file", putURLFile,
		"--allow-insecure-http", "--confirm-independent-storage",
	}, now)
	if uploaded.Action != "backup.upload-http" || uploaded.Path != archivePath || uploaded.Bytes <= 0 || len(uploaded.SHA256) != 64 {
		t.Fatalf("upload-http output = %#v", uploaded)
	}
	if uploaded.RemoteVerification != nil {
		t.Fatalf("upload-http without readback unexpectedly returned verification: %#v", uploaded.RemoteVerification)
	}
	readbackUpload := runBackupCommand(t, []string{
		"backup", "upload-http", "--input", archivePath, "--put-url-file", putURLFile,
		"--verify-get-url-file", getURLFile, "--allow-insecure-http", "--confirm-independent-storage",
	}, now)
	if readbackUpload.Action != "backup.upload-http" || readbackUpload.RemoteVerification == nil ||
		readbackUpload.RemoteVerification.Bytes != readbackUpload.Bytes ||
		readbackUpload.RemoteVerification.SHA256 != readbackUpload.SHA256 ||
		readbackUpload.RemoteVerification.Manifest != created.Manifest {
		t.Fatalf("upload-http readback output = %#v", readbackUpload)
	}
	downloadedPath := filepath.Join(directory, "remote.xbbackup")
	downloaded := runBackupCommand(t, []string{
		"backup", "download-http", "--get-url-file", getURLFile, "--output", downloadedPath,
		"--allow-insecure-http",
	}, now)
	if downloaded.Action != "backup.download-http" || downloaded.Path != downloadedPath ||
		downloaded.Bytes != uploaded.Bytes || downloaded.SHA256 != uploaded.SHA256 ||
		downloaded.Manifest != created.Manifest {
		t.Fatalf("download-http output = %#v, want transfer %#v", downloaded, uploaded)
	}
	if verified, err := backup.Verify(context.Background(), downloadedPath); err != nil || verified != created.Manifest {
		t.Fatalf("Verify(downloaded) = (%#v, %v), want %#v", verified, err, created.Manifest)
	}
	remoteRestoredPath := filepath.Join(directory, "remote-restored.db")
	remoteRestored := runBackupCommand(t, []string{
		"backup", "restore", "--input", downloadedPath, "--output", remoteRestoredPath,
	}, now)
	if remoteRestored.Action != "backup.restore" || remoteRestored.Path != remoteRestoredPath ||
		remoteRestored.Manifest != created.Manifest {
		t.Fatalf("restore(downloaded) output = %#v, want manifest %#v", remoteRestored, created.Manifest)
	}
}

func TestRunCommandBackupUploadHTTPReadbackFailsWithoutRemoteObject(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.xbbackup")
	if err := os.WriteFile(sourcePath, []byte("source archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var putCount int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			putCount++
			_, _ = io.Copy(io.Discard, request.Body)
			response.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			http.NotFound(response, request)
		default:
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	putURLFile := filepath.Join(directory, "put-url.txt")
	getURLFile := filepath.Join(directory, "get-url.txt")
	if err := os.WriteFile(putURLFile, []byte(server.URL+"/put"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(getURLFile, []byte(server.URL+"/get"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"backup", "upload-http", "--input", sourcePath, "--put-url-file", putURLFile,
		"--verify-get-url-file", getURLFile, "--allow-insecure-http", "--confirm-independent-storage",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC) })
	if !handled || err == nil || !strings.Contains(err.Error(), "remote backup readback failed") || stdout.Len() != 0 {
		t.Fatalf("upload readback missing object = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if putCount != 1 {
		t.Fatalf("PUT count = %d, want 1", putCount)
	}
}

func TestRunCommandBackupUploadHTTPReadbackRejectsChangedRemoteBytes(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.xbbackup")
	if err := os.WriteFile(sourcePath, []byte("source archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, request.Body)
			response.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			_, _ = response.Write([]byte("changed remote bytes"))
		default:
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	putURLFile := filepath.Join(directory, "put-url.txt")
	getURLFile := filepath.Join(directory, "get-url.txt")
	for _, path := range []string{putURLFile, getURLFile} {
		if err := os.WriteFile(path, []byte(server.URL), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"backup", "upload-http", "--input", sourcePath, "--put-url-file", putURLFile,
		"--verify-get-url-file", getURLFile, "--allow-insecure-http", "--confirm-independent-storage",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC) })
	if !handled || err == nil || !strings.Contains(err.Error(), "does not match") || stdout.Len() != 0 {
		t.Fatalf("upload readback changed bytes = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
}

func TestRunCommandBackupUploadHTTPReadbackValidatesURLBeforePUT(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.xbbackup")
	if err := os.WriteFile(sourcePath, []byte("source archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var putCount int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			putCount++
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	putURLFile := filepath.Join(directory, "put-url.txt")
	if err := os.WriteFile(putURLFile, []byte(server.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"backup", "upload-http", "--input", sourcePath, "--put-url-file", putURLFile,
		"--verify-get-url-file", filepath.Join(directory, "missing-get-url.txt"),
		"--allow-insecure-http", "--confirm-independent-storage",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC) })
	if !handled || err == nil || !strings.Contains(err.Error(), "inspect backup replica URL file") || stdout.Len() != 0 {
		t.Fatalf("upload readback missing URL = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if putCount != 0 {
		t.Fatalf("PUT count = %d, want 0 when GET URL validation fails", putCount)
	}

	invalidGetURLFile := filepath.Join(directory, "invalid-get-url.txt")
	if err := os.WriteFile(invalidGetURLFile, []byte("file:///tmp/not-http"), 0o600); err != nil {
		t.Fatal(err)
	}
	handled, err = runCommand(context.Background(), []string{
		"backup", "upload-http", "--input", sourcePath, "--put-url-file", putURLFile,
		"--verify-get-url-file", invalidGetURLFile,
		"--allow-insecure-http", "--confirm-independent-storage",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC) })
	if !handled || err == nil || !strings.Contains(err.Error(), "validate backup replica GET URL") || stdout.Len() != 0 {
		t.Fatalf("upload readback invalid URL = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if putCount != 0 {
		t.Fatalf("PUT count = %d, want 0 when GET URL syntax validation fails", putCount)
	}
}

func TestRunCommandBackupDownloadRejectsNonArchivePayload(t *testing.T) {
	directory := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		_, _ = response.Write([]byte("not a backup archive"))
	}))
	defer server.Close()
	urlFile := filepath.Join(directory, "get-url.txt")
	if err := os.WriteFile(urlFile, []byte(server.URL+"/invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "invalid.xbbackup")
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+filepath.Join(directory, "unused.db"))
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"backup", "download-http", "--get-url-file", urlFile, "--output", output, "--allow-insecure-http",
	}, &stdout, &stderr, func() time.Time { return time.Date(2026, 8, 25, 13, 0, 0, 0, time.UTC) })
	if !handled || err == nil || !strings.Contains(err.Error(), "verify downloaded backup replica") || stdout.Len() != 0 {
		t.Fatalf("invalid download = handled %v error %v stdout=%q", handled, err, stdout.String())
	}
	if _, statErr := os.Lstat(output); !os.IsNotExist(statErr) {
		t.Fatalf("invalid download left output behind: %v", statErr)
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
		{"backup", "upload-http", "--input", result.Path, "--put-url-file", filepath.Join(directory, "url.txt")},
		{"backup", "download-http", "--output", filepath.Join(directory, "copy.xbbackup")},
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
	Status             string                    `json:"status"`
	Action             string                    `json:"action"`
	Path               string                    `json:"path"`
	Bytes              int64                     `json:"bytes"`
	SHA256             string                    `json:"sha256"`
	Manifest           backup.Manifest           `json:"manifest"`
	RemoteVerification *remoteVerificationOutput `json:"remote_verification,omitempty"`
}

type remoteVerificationOutput struct {
	Bytes    int64           `json:"bytes"`
	SHA256   string          `json:"sha256"`
	Manifest backup.Manifest `json:"manifest"`
}
