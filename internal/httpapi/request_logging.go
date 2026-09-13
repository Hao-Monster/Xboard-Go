package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// requestIDKey is intentionally private so handlers cannot accidentally
// overwrite the correlation identifier used by the request logger.
type requestIDKey struct{}

// RequestID returns the correlation identifier assigned to the current HTTP
// request. It is safe for handlers and downstream services to use in logs.
func RequestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey{}).(string); ok {
		return value
	}
	return ""
}

// requestLogRecorder records the observable HTTP result while preserving the
// optional interfaces required by WebSocket upgrades and streaming handlers.
// It deliberately never buffers request or response bodies.
type requestLogRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// Unwrap lets http.ResponseController reach optional capabilities exposed by
// the original response writer.
func (w *requestLogRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *requestLogRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *requestLogRecorder) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(payload)
	w.bytes += int64(n)
	return n, err
}

// ReadFrom preserves the io.ReaderFrom fast path used by io.Copy (including
// sendfile-capable writers) while keeping the byte counter accurate.
func (w *requestLogRecorder) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := readerFrom.ReadFrom(src)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(struct{ io.Writer }{Writer: w}, src)
	return n, err
}

func (w *requestLogRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *requestLogRecorder) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *requestLogRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *requestLogRecorder) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (s *server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := uuid.NewString()
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		recording := &requestLogRecorder{ResponseWriter: w}
		started := time.Now()
		next.ServeHTTP(recording, r.WithContext(ctx))

		status := recording.statusCode()
		attrs := []any{
			"request_id", requestID,
			"method", r.Method,
			"path", requestPath(r),
			"request_bytes", requestContentLength(r),
			"status", status,
			"bytes", recording.bytes,
			"duration_ms", time.Since(started).Seconds() * 1000,
			"remote_ip", requestRemoteIP(r),
		}
		switch {
		case status >= http.StatusInternalServerError:
			s.logger.Error("http request completed", attrs...)
		case status >= http.StatusBadRequest:
			s.logger.Warn("http request completed", attrs...)
		default:
			s.logger.Debug("http request completed", attrs...)
		}
	})
}

func requestContentLength(r *http.Request) int64 {
	if r == nil {
		return 0
	}
	return r.ContentLength
}

// requestPath excludes query parameters because subscriptions, credentials
// and other sensitive values are frequently carried in URLs.
func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func requestRemoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
