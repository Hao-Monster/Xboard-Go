package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxReplicaArchiveBytes = maxArchiveBytes

// ValidateHTTPReplicaURL validates an operator-provided HTTP(S) replica URL
// without making a network request.
func ValidateHTTPReplicaURL(rawURL string, allowInsecureHTTP bool) error {
	_, err := parseReplicaURL(rawURL, allowInsecureHTTP)
	return err
}

// HTTPReplication records the bytes transferred to or from an independent
// backup location. The digest lets an operator compare both ends before
// running the archive verifier.
type HTTPReplication struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// UploadHTTP streams a backup archive to an operator-provided HTTP(S) endpoint.
// The endpoint is expected to be a pre-signed URL whose access material stays
// outside command arguments and logs.
func UploadHTTP(ctx context.Context, inputPath, rawURL string, allowInsecureHTTP bool) (HTTPReplication, error) {
	if err := ctx.Err(); err != nil {
		return HTTPReplication{}, err
	}
	endpoint, err := parseReplicaURL(rawURL, allowInsecureHTTP)
	if err != nil {
		return HTTPReplication{}, err
	}
	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return HTTPReplication{}, errors.New("backup replica input path is required")
	}
	inputInfo, err := os.Lstat(inputPath)
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("inspect backup replica input: %w", err)
	}
	if !inputInfo.Mode().IsRegular() || inputInfo.Size() <= 0 || inputInfo.Size() > maxReplicaArchiveBytes {
		return HTTPReplication{}, errors.New("backup replica input must be a non-empty regular file within the size limit")
	}

	input, err := os.Open(inputPath)
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("open backup replica input: %w", err)
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("inspect opened backup replica input: %w", err)
	}
	if !os.SameFile(inputInfo, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Size() != inputInfo.Size() {
		return HTTPReplication{}, errors.New("backup replica input changed while it was opened")
	}

	reader := &hashingReadTracker{
		ctx:    ctx,
		reader: input,
		hash:   sha256.New(),
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), reader)
	if err != nil {
		return HTTPReplication{}, errors.New("create backup replica upload request")
	}
	request.ContentLength = inputInfo.Size()
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("User-Agent", "xboard-go-backup-replica")

	response, err := noRedirectHTTPClient().Do(request)
	if err != nil {
		return HTTPReplication{}, replicaHTTPError("upload backup replica", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return HTTPReplication{}, fmt.Errorf("upload backup replica returned HTTP status %d", response.StatusCode)
	}
	if reader.read != inputInfo.Size() {
		return HTTPReplication{}, errors.New("backup replica upload did not read the complete source archive")
	}
	var extra [1]byte
	if count, readErr := input.Read(extra[:]); count != 0 || !errors.Is(readErr, io.EOF) {
		return HTTPReplication{}, errors.New("backup replica input changed while it was uploaded")
	}
	return HTTPReplication{Size: reader.read, SHA256: hex.EncodeToString(reader.hash.Sum(nil))}, nil
}

// DownloadHTTP streams a backup archive from an operator-provided HTTP(S)
// endpoint into a new local path and publishes it only after a complete
// bounded transfer and digest check.
func DownloadHTTP(ctx context.Context, rawURL, outputPath string, allowInsecureHTTP bool) (HTTPReplication, error) {
	if err := ctx.Err(); err != nil {
		return HTTPReplication{}, err
	}
	endpoint, err := parseReplicaURL(rawURL, allowInsecureHTTP)
	if err != nil {
		return HTTPReplication{}, err
	}
	outputPath, err = prepareReplicaOutputPath(outputPath)
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("prepare backup replica download output: %w", err)
	}
	temporaryPath, err := unusedTemporaryPath(filepath.Dir(outputPath), ".xboard-backup-replica-download-*")
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("reserve backup replica download path: %w", err)
	}
	defer os.Remove(temporaryPath)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return HTTPReplication{}, errors.New("create backup replica download request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "xboard-go-backup-replica")
	response, err := noRedirectHTTPClient().Do(request)
	if err != nil {
		return HTTPReplication{}, replicaHTTPError("download backup replica", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return HTTPReplication{}, fmt.Errorf("download backup replica returned HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > maxReplicaArchiveBytes {
		return HTTPReplication{}, errors.New("backup replica download exceeds the supported size limit")
	}

	output, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, backupFileMode)
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("create backup replica download: %w", err)
	}
	outputOpen := true
	defer func() {
		if outputOpen {
			_ = output.Close()
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(output, hash), io.LimitReader(&contextReader{ctx: ctx, reader: response.Body}, maxReplicaArchiveBytes+1))
	if err != nil {
		return HTTPReplication{}, fmt.Errorf("write backup replica download: %w", err)
	}
	if written <= 0 || written > maxReplicaArchiveBytes {
		return HTTPReplication{}, errors.New("backup replica download is empty or exceeds the supported size limit")
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return HTTPReplication{}, errors.New("backup replica download size does not match Content-Length")
	}
	if err := output.Sync(); err != nil {
		return HTTPReplication{}, fmt.Errorf("sync backup replica download: %w", err)
	}
	if err := output.Close(); err != nil {
		return HTTPReplication{}, fmt.Errorf("close backup replica download: %w", err)
	}
	outputOpen = false

	digest := hex.EncodeToString(hash.Sum(nil))
	if err := publishNoReplace(temporaryPath, outputPath); err != nil {
		return HTTPReplication{}, fmt.Errorf("publish backup replica download: %w", err)
	}
	publishedDigest, err := fileSHA256(ctx, outputPath)
	if err != nil || publishedDigest != digest {
		_ = os.Remove(outputPath)
		if err != nil {
			return HTTPReplication{}, fmt.Errorf("hash published backup replica download: %w", err)
		}
		return HTTPReplication{}, errors.New("published backup replica download SHA-256 does not match")
	}
	return HTTPReplication{Size: written, SHA256: digest}, nil
}

func prepareReplicaOutputPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("output path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("destination already exists: %s", absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	directory := filepath.Dir(absolute)
	directoryInfo, err := os.Lstat(directory)
	switch {
	case err == nil:
		if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("backup replica destination directory is unsafe")
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(directory, backupDirectoryMode); err != nil {
			return "", err
		}
		directoryInfo, err = os.Lstat(directory)
		if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("backup replica destination directory is unsafe")
		}
		if err := os.Chmod(directory, backupDirectoryMode); err != nil {
			return "", err
		}
	default:
		return "", err
	}

	if _, err := os.Lstat(absolute); err == nil {
		return "", fmt.Errorf("destination already exists: %s", absolute)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return absolute, nil
}

func parseReplicaURL(raw string, allowInsecureHTTP bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("backup replica URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("backup replica URL is invalid")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("backup replica URL must not contain user info or fragments")
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecureHTTP {
			return nil, errors.New("backup replica URL must use HTTPS unless insecure HTTP is explicitly allowed")
		}
		hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
		if hostname != "localhost" {
			ip := net.ParseIP(hostname)
			if ip == nil || !ip.IsLoopback() {
				return nil, errors.New("insecure HTTP backup replica URL is limited to a loopback host")
			}
		}
	default:
		return nil, errors.New("backup replica URL must use HTTP or HTTPS")
	}
	return parsed, nil
}

func replicaHTTPError(action string, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s request failed: %w", action, urlErr.Err)
	}
	return fmt.Errorf("%s request failed", action)
}

func noRedirectHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type hashingReadTracker struct {
	ctx    context.Context
	reader io.Reader
	hash   hash.Hash
	read   int64
}

func (reader *hashingReadTracker) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		if _, hashErr := reader.hash.Write(buffer[:count]); hashErr != nil {
			return count, hashErr
		}
		reader.read += int64(count)
	}
	return count, err
}
