package attachments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const privateAttachmentPath = "/knowledge-attachments/"

func (s *Service) SignedURL(attachment Attachment, requestedDisposition string, now time.Time) (string, error) {
	if _, valid := canonicalAttachmentUUID(attachment.UUID); !valid || attachment.Status != AttachmentReady || attachment.DeletedAt != nil {
		return "", ErrInvalidInput
	}
	disposition := effectiveDisposition(attachment, requestedDisposition)
	expires := now.Add(s.signedURLTTL).Unix()
	path := privateAttachmentPath + attachment.UUID
	signature := s.sign(path, disposition, expires)
	result := *s.panelURL
	result.Path = strings.TrimRight(result.Path, "/") + path
	result.RawQuery = url.Values{
		"disposition": {disposition}, "expires": {strconv.FormatInt(expires, 10)}, "signature": {signature},
	}.Encode()
	return result.String(), nil
}

func (s *Service) AuthorizeSigned(ctx context.Context, attachmentUUID, disposition, expiresValue, signature string, now time.Time) (Attachment, error) {
	if _, valid := canonicalAttachmentUUID(attachmentUUID); !valid || (disposition != "inline" && disposition != "attachment") || !sha256Pattern.MatchString(signature) {
		return Attachment{}, ErrNotFound
	}
	expires, err := strconv.ParseInt(expiresValue, 10, 64)
	if err != nil || expires <= now.Unix() || expires > now.Add(s.signedURLTTL).Unix() {
		return Attachment{}, ErrNotFound
	}
	path := privateAttachmentPath + attachmentUUID
	expected := s.sign(path, disposition, expires)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return Attachment{}, ErrNotFound
	}
	attachment, err := s.database.GetReadyKnowledgeAttachment(ctx, attachmentUUID)
	if err != nil {
		return Attachment{}, mapStoreError(err)
	}
	if effectiveDisposition(attachment, disposition) != disposition {
		return Attachment{}, ErrNotFound
	}
	return attachment, nil
}

func (s *Service) OpenPublic(ctx context.Context, attachmentUUID string) (*os.File, Attachment, error) {
	if _, valid := canonicalAttachmentUUID(attachmentUUID); !valid {
		return nil, Attachment{}, ErrNotFound
	}
	attachment, err := s.database.GetPublicKnowledgeAttachment(ctx, attachmentUUID)
	if err != nil {
		return nil, Attachment{}, mapStoreError(err)
	}
	return s.openAttachment(attachment)
}

func (s *Service) Serve(w http.ResponseWriter, r *http.Request, attachment Attachment, file *os.File, disposition string) {
	defer file.Close()
	setAttachmentHeaders(w.Header(), attachment, disposition)
	start, end, partial, valid := parseAttachmentRange(r.Header.Get("Range"), attachment.Size)
	if !valid {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", attachment.Size))
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	length := end - start + 1
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, attachment.Size))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return
	}
	_, _ = io.CopyN(w, file, length)
}

func parseAttachmentRange(value string, size int64) (start, end int64, partial, valid bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, size - 1, false, size > 0
	}
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, false, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return 0, 0, false, false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix < 1 {
			return 0, 0, false, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, size > 0
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, false
	}
	end = size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, true
}

func (s *Service) OpenKnown(attachment Attachment) (*os.File, Attachment, error) {
	return s.openAttachment(attachment)
}

func (s *Service) sign(path, disposition string, expires int64) string {
	mac := hmac.New(sha256.New, s.signingKey)
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%d", path, disposition, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) openAttachment(attachment Attachment) (*os.File, Attachment, error) {
	path, err := s.safePath(attachment.StoragePath)
	if err != nil {
		return nil, Attachment{}, err
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, Attachment{}, ErrNotFound
	}
	if err != nil {
		return nil, Attachment{}, fmt.Errorf("open attachment object: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, Attachment{}, ErrNotFound
	}
	if info.Size() != attachment.Size {
		_ = file.Close()
		return nil, Attachment{}, ErrConflict
	}
	return file, attachment, nil
}

func effectiveDisposition(attachment store.KnowledgeAttachment, requested string) string {
	if requested != "inline" {
		return "attachment"
	}
	allowed := map[string]map[string]struct{}{
		"image/jpeg": {"jpg": {}, "jpeg": {}}, "image/png": {"png": {}}, "image/gif": {"gif": {}},
		"image/webp": {"webp": {}}, "image/avif": {"avif": {}}, "video/mp4": {"mp4": {}},
		"video/webm": {"webm": {}}, "video/ogg": {"ogg": {}},
	}
	if attachment.Extension == nil {
		return "attachment"
	}
	if extensions, ok := allowed[strings.ToLower(attachment.MIMEType)]; ok {
		if _, ok := extensions[*attachment.Extension]; ok {
			return "inline"
		}
	}
	return "attachment"
}

func setAttachmentHeaders(header http.Header, attachment Attachment, disposition string) {
	disposition = effectiveDisposition(attachment, disposition)
	contentType := attachment.MIMEType
	if disposition != "inline" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	header.Set("Content-Disposition", contentDisposition(disposition, attachment.OriginalName))
	header.Set("Content-Length", strconv.FormatInt(attachment.Size, 10))
	header.Set("Accept-Ranges", "bytes")
	header.Set("Vary", "Range")
	header.Set("X-Knowledge-Attachment-Size", strconv.FormatInt(attachment.Size, 10))
	header.Set("ETag", `"`+attachment.SHA256+`"`)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	header.Set("Cross-Origin-Resource-Policy", "same-site")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Download-Options", "noopen")
	header.Set("Cache-Control", "private, no-store")
}

func contentDisposition(disposition, originalName string) string {
	safeName := safeResponseFilename(originalName)
	encoded := url.PathEscape(strings.ReplaceAll(originalName, "\\", "_"))
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disposition, safeName, encoded)
}

func safeResponseFilename(value string) string {
	value = filepath.Base(strings.ReplaceAll(strings.ReplaceAll(value, "\\", "_"), "/", "_"))
	var result strings.Builder
	for _, character := range value {
		switch {
		case character >= 0x21 && character <= 0x7e && character != '"' && character != '\\' && character != ';':
			result.WriteRune(character)
		case character == ' ':
			result.WriteByte(' ')
		case unicode.IsControl(character):
			continue
		default:
			result.WriteByte('_')
		}
	}
	safe := strings.TrimSpace(result.String())
	if safe == "" || safe == "." || safe == ".." {
		return "download"
	}
	return safe
}
