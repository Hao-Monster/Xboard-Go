package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestHTTPReplicaUploadDownloadRoundTripWithoutRedirects(t *testing.T) {
	directory := t.TempDir()
	archivePath := createTestBackup(t, directory)
	archiveContent, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveDigest := sha256.Sum256(archiveContent)

	var mutex sync.Mutex
	var stored []byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.Method {
		case http.MethodPut:
			if request.Header.Get("User-Agent") != "xboard-go-backup-replica" || request.ContentLength != int64(len(archiveContent)) {
				http.Error(response, "unexpected upload metadata", http.StatusBadRequest)
				return
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(response, "read body", http.StatusInternalServerError)
				return
			}
			mutex.Lock()
			stored = append(stored[:0], body...)
			mutex.Unlock()
			response.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			mutex.Lock()
			body := append([]byte(nil), stored...)
			mutex.Unlock()
			if len(body) == 0 {
				http.NotFound(response, request)
				return
			}
			response.Header().Set("Content-Type", "application/octet-stream")
			response.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = response.Write(body)
		default:
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	uploaded, err := UploadHTTP(t.Context(), archivePath, server.URL+"/object", true)
	if err != nil {
		t.Fatalf("UploadHTTP() error = %v", err)
	}
	if uploaded.Size != int64(len(archiveContent)) || uploaded.SHA256 != hex.EncodeToString(archiveDigest[:]) {
		t.Fatalf("UploadHTTP() = %#v, want size=%d sha=%s", uploaded, len(archiveContent), hex.EncodeToString(archiveDigest[:]))
	}
	mutex.Lock()
	if !bytes.Equal(stored, archiveContent) {
		t.Fatal("uploaded replica bytes differ from the source archive")
	}
	mutex.Unlock()

	downloadPath := filepath.Join(directory, "downloaded.xbbackup")
	downloaded, err := DownloadHTTP(t.Context(), server.URL+"/object", downloadPath, true)
	if err != nil {
		t.Fatalf("DownloadHTTP() error = %v", err)
	}
	if downloaded != uploaded {
		t.Fatalf("DownloadHTTP() = %#v, want %#v", downloaded, uploaded)
	}
	if verified, err := Verify(t.Context(), downloadPath); err != nil || verified.AppRevision != "test" {
		t.Fatalf("Verify(downloaded) = (%#v, %v)", verified, err)
	}

	redirectServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, server.URL+"/object", http.StatusTemporaryRedirect)
	}))
	defer redirectServer.Close()
	if _, err := UploadHTTP(t.Context(), archivePath, redirectServer.URL, true); err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("UploadHTTP(redirect) error = %v", err)
	}
}

func TestHTTPReplicaRejectsUnsafeURLsAndDownloadFailures(t *testing.T) {
	directory := t.TempDir()
	archivePath := createTestBackup(t, directory)
	for _, rawURL := range []string{
		"http://127.0.0.1/replica",
		"http://example.test/replica",
		"ftp://example.test/replica",
		"https://user@example.test/replica",
		"https://example.test/replica#fragment",
	} {
		if _, err := UploadHTTP(t.Context(), archivePath, rawURL, false); err == nil {
			t.Fatalf("UploadHTTP(%q) accepted an unsafe URL", rawURL)
		}
	}
	if _, err := UploadHTTP(t.Context(), archivePath, "http://example.test/replica", true); err == nil || !strings.Contains(err.Error(), "loopback host") {
		t.Fatalf("UploadHTTP(remote HTTP) error = %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "missing", http.StatusNotFound)
	}))
	defer server.Close()
	output := filepath.Join(directory, "failed.xbbackup")
	if _, err := DownloadHTTP(t.Context(), server.URL, output, true); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("DownloadHTTP(404) error = %v", err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed download left output behind: %v", err)
	}
	if err := os.WriteFile(output, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DownloadHTTP(t.Context(), server.URL, output, true); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("DownloadHTTP(existing output) error = %v", err)
	}

	targetDirectory := filepath.Join(directory, "target")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkDirectory := filepath.Join(directory, "symlink-directory")
	if err := os.Symlink(targetDirectory, symlinkDirectory); err != nil {
		t.Skipf("filesystem does not support symlink creation: %v", err)
	}
	unsafeOutput := filepath.Join(symlinkDirectory, "downloaded.xbbackup")
	if _, err := DownloadHTTP(t.Context(), server.URL, unsafeOutput, true); err == nil || !strings.Contains(err.Error(), "destination directory is unsafe") {
		t.Fatalf("DownloadHTTP(symlink directory) error = %v", err)
	}
}
