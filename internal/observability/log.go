package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}
func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }

// ErrorType deliberately never calls Error or LogValue: database errors and
// transport failures may contain SQL values, credentials or complete URLs.
func ErrorType(value any) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%T", value)
}

func New(output io.Writer, level slog.Level, environment, revision string) *slog.Logger {
	base := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})
	return slog.New(SafeHandler(base)).With("service", "xboard-go", "environment", environment, "revision", revision)
}

func SafeHandler(base slog.Handler) slog.Handler {
	if _, ok := base.(*safeHandler); ok {
		return base
	}
	return &safeHandler{base}
}

type safeHandler struct{ base slog.Handler }

func (h *safeHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.base.Enabled(ctx, l) }
func (h *safeHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		if a, ok := safeAttr(a); ok {
			clean.AddAttrs(a)
		}
		return true
	})
	if r.Level >= slog.LevelWarn && r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := frames.Next()
		clean.AddAttrs(slog.String("source", fmt.Sprintf("%s %s:%d", frame.Function, filepath.Base(frame.File), frame.Line)))
	}
	if id := RequestID(ctx); id != "" {
		clean.AddAttrs(slog.String("request_id", id))
	}
	return h.base.Handle(ctx, clean)
}
func (h *safeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var safe []slog.Attr
	for _, a := range attrs {
		if a, ok := safeAttr(a); ok {
			safe = append(safe, a)
		}
	}
	return &safeHandler{h.base.WithAttrs(safe)}
}
func (h *safeHandler) WithGroup(name string) slog.Handler {
	return &safeHandler{h.base.WithGroup(name)}
}
func safeAttr(a slog.Attr) (slog.Attr, bool) {
	if a.Key == "revision" && (a.Value.Kind() == slog.KindInt64 || a.Value.Kind() == slog.KindUint64) {
		a.Key = "revision_number"
	}
	if a.Key == "error" {
		return slog.String("error_type", ErrorType(a.Value.Any())), true
	}
	// Allowlist metadata, never request/response content, addresses or arbitrary
	// custom objects. Dropping unknown attributes makes new call sites fail closed.
	switch a.Key {
	case "service", "environment", "revision", "request_id", "method", "route", "error_type", "event", "outcome", "stack":
		if a.Value.Kind() == slog.KindString {
			return a, true
		}
	}
	switch a.Key {
	case "status", "duration_ms", "bytes", "machine_id", "node_id", "administrator_id", "user_id", "job_id", "route_id", "knowledge_id", "payment_id", "revision_number", "count", "expired_uploads", "deleted_objects", "failed_objects", "limit", "expire", "traffic", "checked", "paid", "remaining", "cancelled", "completed", "processed":
		switch a.Value.Kind() {
		case slog.KindInt64, slog.KindUint64, slog.KindFloat64, slog.KindBool, slog.KindDuration:
			return a, true
		}
	}
	// Existing categorical diagnostics are retained only from fixed vocabularies.
	if a.Key == "reason" && strings.HasSuffix(a.Value.String(), "_failed") {
		return slog.String("outcome", "failed"), true
	}
	return slog.Attr{}, false
}
