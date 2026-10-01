package httpapi

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/observability"
)

type diagnosticWriter struct {
	http.ResponseWriter
	request     *http.Request
	logger      *slog.Logger
	status      int
	bytes       int64
	errorLogged bool
}

func (w *diagnosticWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *diagnosticWriter) WriteHeader(status int) {
	if status >= 500 {
		recordDiagnosticError(w, nil)
	}
	if status >= 100 && status < 200 && status != 101 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *diagnosticWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}
func (w *diagnosticWriter) Flush() { _ = w.FlushError() }
func (w *diagnosticWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *diagnosticWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.status = 101
	}
	return conn, rw, err
}
func diagnosticFor(w http.ResponseWriter) *diagnosticWriter {
	for {
		if d, ok := w.(*diagnosticWriter); ok {
			return d
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = u.Unwrap()
	}
}
func recordDiagnosticError(w http.ResponseWriter, err any) {
	if d := diagnosticFor(w); d != nil && !d.errorLogged {
		d.errorLogged = true
		d.logger.ErrorContext(d.request.Context(), "http.error", "error_type", observability.ErrorType(err), "stack", safeStack(3))
	}
}
func safeStack(skip int) string {
	pcs := make([]uintptr, 16)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var lines []string
	for {
		f, more := frames.Next()
		lines = append(lines, fmt.Sprintf("%s %s:%d", f.Function, filepath.Base(f.File), f.Line))
		if !more {
			break
		}
	}
	return strings.Join(lines, "\n")
}
func safeRoute(r *http.Request, adminPath string) string {
	route := r.Pattern
	if route == "" {
		return "unmatched"
	}
	if adminPath != "" {
		parts := strings.Split(route, "/")
		for i, part := range parts {
			if part == adminPath {
				parts[i] = "{secure_admin}"
			}
		}
		route = strings.Join(parts, "/")
	}
	return route
}
func (s *server) requestDiagnostics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observability.RequestID(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			http.Error(w, "internal error", 500)
			return
		}
		id := hex.EncodeToString(random[:])
		r = r.WithContext(observability.WithRequestID(r.Context(), id))
		w.Header().Set("X-Request-ID", id)
		d := &diagnosticWriter{ResponseWriter: w, request: r, logger: s.logger}
		start := time.Now()
		defer func() {
			status := d.status
			if status == 0 {
				status = 200
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			s.logger.Log(r.Context(), level, "http.request", "method", safeMethod(r.Method), "route", safeRoute(r, s.diagnosticAdminPath), "status", status, "duration_ms", float64(time.Since(start).Microseconds())/1000, "bytes", d.bytes)
		}()
		next.ServeHTTP(d, r)
	})
}
func safeMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "CONNECT", "TRACE":
		return method
	}
	return "OTHER"
}

// RequestDiagnostics covers frontend responses as well as APIs. An inner API
// wrapper detects the server-owned context so it does not duplicate events.
func RequestDiagnostics(logger *slog.Logger, adminPath string, next http.Handler) http.Handler {
	s := server{logger: slog.New(observability.SafeHandler(logger.Handler())), diagnosticAdminPath: adminPath}
	return s.requestDiagnostics(next)
}
