package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	SessionCookieName = "xboard_session"
	CSRFCookieName    = "xboard_csrf"
	maxJSONBody       = 1 << 20
)

type Dependencies struct {
	Context          context.Context
	Store            *store.Store
	PasswordHasher   security.PasswordHasher
	Now              func() time.Time
	PanelURL         string
	NodeRelease      string
	CookieSecure     bool
	AllowedOrigins   []string
	Logger           *slog.Logger
	WebSocketEnabled bool
	WebSocketURL     string
	NodePushInterval int
	NodePullInterval int
}

type server struct {
	store               *store.Store
	passwordHasher      security.PasswordHasher
	dummyHash           string
	now                 func() time.Time
	panelURL            string
	nodeRelease         string
	cookieSecure        bool
	allowedOrigins      map[string]struct{}
	logger              *slog.Logger
	loginAttempts       *attemptLimiter
	enrollAttempts      *attemptLimiter
	machineAuthFailures *attemptLimiter
	handshakeRequests   *requestLimitGroup
	pullRequests        *requestLimitGroup
	reportRequests      *requestLimitGroup
	machineRequests     *requestLimitGroup
	hub                 *wsHub
	webSocketEnabled    bool
	webSocketURL        string
	nodePushInterval    int
	nodePullInterval    int
}

type contextKey int

const sessionContextKey contextKey = iota

func New(dependencies Dependencies) http.Handler {
	if dependencies.Store == nil {
		panic("httpapi: Store is required")
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.Context == nil {
		dependencies.Context = context.Background()
	}
	if dependencies.PanelURL == "" {
		dependencies.PanelURL = "http://127.0.0.1:8080"
	}
	if dependencies.NodeRelease == "" {
		dependencies.NodeRelease = "v1.14.3"
	}
	if dependencies.NodePushInterval == 0 {
		dependencies.NodePushInterval = 60
	}
	if dependencies.NodePullInterval == 0 {
		dependencies.NodePullInterval = 60
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	dummyHash, err := dependencies.PasswordHasher.Hash("xboard-dummy-login-password")
	if err != nil {
		panic(fmt.Sprintf("httpapi: create dummy password hash: %v", err))
	}

	allowedOrigins := make(map[string]struct{})
	for _, origin := range dependencies.AllowedOrigins {
		allowedOrigins[strings.TrimRight(origin, "/")] = struct{}{}
	}
	if parsed, err := url.Parse(dependencies.PanelURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		allowedOrigins[parsed.Scheme+"://"+parsed.Host] = struct{}{}
	}

	api := &server{
		store:               dependencies.Store,
		passwordHasher:      dependencies.PasswordHasher,
		dummyHash:           dummyHash,
		now:                 dependencies.Now,
		panelURL:            strings.TrimRight(dependencies.PanelURL, "/"),
		nodeRelease:         dependencies.NodeRelease,
		cookieSecure:        dependencies.CookieSecure,
		allowedOrigins:      allowedOrigins,
		logger:              dependencies.Logger,
		loginAttempts:       newAttemptLimiter(5, 15*time.Minute),
		enrollAttempts:      newAttemptLimiter(20, 15*time.Minute),
		machineAuthFailures: newAttemptLimiter(60, time.Minute),
		handshakeRequests:   newRequestLimitGroup(60, 20),
		pullRequests:        newRequestLimitGroup(2_400, 600),
		reportRequests:      newRequestLimitGroup(1_200, 240),
		machineRequests:     newRequestLimitGroup(1_200, 240),
		webSocketEnabled:    dependencies.WebSocketEnabled,
		webSocketURL:        strings.TrimRight(dependencies.WebSocketURL, "/"),
		nodePushInterval:    dependencies.NodePushInterval,
		nodePullInterval:    dependencies.NodePullInterval,
	}
	if dependencies.WebSocketEnabled {
		api.hub = newWSHub(dependencies.Store, dependencies.Now, dependencies.Logger, allowedOrigins, dependencies.NodePushInterval, dependencies.NodePullInterval)
		go api.hub.runUntil(dependencies.Context)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", api.health)
	root.HandleFunc("GET /ws", api.webSocket)
	root.HandleFunc("POST /api/v1/auth/login", api.login)
	root.Handle("GET /api/v1/auth/session", api.requireSession(http.HandlerFunc(api.session)))
	root.Handle("POST /api/v1/auth/logout", api.requireSession(api.requireCSRF(http.HandlerFunc(api.logout))))
	root.HandleFunc("POST /api/v1/machines/enroll", api.exchangeEnrollment)
	root.HandleFunc("GET /api/v1/machines/{machineID}/nodes", api.agentNodes)
	root.HandleFunc("POST /api/v1/machines/{machineID}/status", api.agentStatus)
	root.HandleFunc("POST /api/v2/server/machine/enroll", api.exchangeEnrollment)
	root.HandleFunc("POST /api/v2/server/machine/nodes", api.xboardNodeMachineNodes)
	root.HandleFunc("POST /api/v2/server/machine/status", api.xboardNodeMachineStatus)
	root.HandleFunc("POST /api/v2/server/handshake", api.xboardNodeHandshake)
	root.HandleFunc("GET /api/v2/server/config", api.xboardNodeConfig)
	root.HandleFunc("GET /api/v2/server/user", api.xboardNodeUsers)
	root.HandleFunc("POST /api/v2/server/report", api.xboardNodeReport)

	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/v1/admin/machines", api.listMachines)
	admin.HandleFunc("POST /api/v1/admin/machines", api.createMachine)
	admin.HandleFunc("GET /api/v1/admin/machines/{machineID}", api.getMachine)
	admin.HandleFunc("PATCH /api/v1/admin/machines/{machineID}", api.updateMachine)
	admin.HandleFunc("DELETE /api/v1/admin/machines/{machineID}", api.deleteMachine)
	admin.HandleFunc("POST /api/v1/admin/machines/{machineID}/enrollments", api.createEnrollment)
	admin.HandleFunc("GET /api/v1/admin/machines/{machineID}/nodes", api.listMachineNodes)
	admin.HandleFunc("PUT /api/v1/admin/machines/{machineID}/nodes/{nodeID}", api.assignNode)
	admin.HandleFunc("DELETE /api/v1/admin/machines/{machineID}/nodes/{nodeID}", api.unassignNode)
	admin.HandleFunc("PATCH /api/v1/admin/machines/{machineID}/nodes/{nodeID}/enabled", api.setNodeEnabled)
	admin.HandleFunc("GET /api/v1/admin/machines/{machineID}/history", api.listHistory)
	admin.HandleFunc("GET /api/v1/admin/nodes/unassigned", api.listUnassignedNodes)
	admin.HandleFunc("POST /api/v1/admin/nodes", api.createNode)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/runtime", api.saveNodeRuntime)
	admin.HandleFunc("GET /api/v1/admin/nodes/{nodeID}/activation-schedule", api.getActivationSchedule)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/activation-schedule", api.saveActivationSchedule)
	admin.HandleFunc("DELETE /api/v1/admin/nodes/{nodeID}/activation-schedule", api.deleteActivationSchedule)
	root.Handle("/api/v1/admin/", api.requireSession(api.requireAdmin(api.requireCSRF(admin))))

	return api.securityHeaders(api.recoverPanic(root))
}

func (s *server) allowServerRequest(w http.ResponseWriter, r *http.Request, limiter *requestLimitGroup, machineID int64) bool {
	if limiter.allow(r, machineID, s.now()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeAPIError(w, http.StatusTooManyRequests, "server_rate_limited", "节点请求过于频繁，请稍后重试", nil)
	return false
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeSuccess(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(SessionCookieName)
		if err != nil || cookie.Value == "" {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "请先登录", nil)
			return
		}
		session, err := s.store.AuthenticateSession(r.Context(), security.DigestToken(cookie.Value), s.now())
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "登录已过期，请重新登录", nil)
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok || !session.IsAdmin {
			writeAPIError(w, http.StatusForbidden, "forbidden", "需要管理员权限", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if origin := strings.TrimRight(r.Header.Get("Origin"), "/"); origin != "" {
			if _, allowed := s.allowedOrigins[origin]; !allowed {
				writeAPIError(w, http.StatusForbidden, "invalid_origin", "请求来源不受信任", nil)
				return
			}
		}
		session, ok := sessionFromContext(r.Context())
		csrfCookie, err := r.Cookie(CSRFCookieName)
		header := r.Header.Get("X-CSRF-Token")
		if !ok || err != nil || header == "" || csrfCookie.Value == "" ||
			subtle.ConstantTimeCompare([]byte(header), []byte(csrfCookie.Value)) != 1 ||
			subtle.ConstantTimeCompare([]byte(security.DigestToken(header)), []byte(session.CSRFHash)) != 1 {
			writeAPIError(w, http.StatusForbidden, "csrf_failed", "安全令牌无效，请刷新页面后重试", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving request", "method", r.Method, "path", r.URL.Path)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func sessionFromContext(ctx context.Context) (store.SessionUser, bool) {
	session, ok := ctx.Value(sessionContextKey).(store.SessionUser)
	return session, ok
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "请求必须使用 application/json", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求不得超过 %d 字节", maxJSONBody), nil)
			return false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求格式无效", nil)
		return false
	}
	if err := decoder.Decode(&struct{}{}); errors.Is(err, io.EOF) {
		return true
	} else {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("请求不得超过 %d 字节", maxJSONBody), nil)
			return false
		}
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_json", "请求只能包含一个 JSON 对象", nil)
	return false
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	value, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || value < 1 {
		writeAPIError(w, http.StatusBadRequest, "invalid_id", "资源编号无效", nil)
		return 0, false
	}
	return value, true
}

func handleStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "资源不存在", nil)
	case errors.Is(err, store.ErrNodeNotLinked):
		writeAPIError(w, http.StatusConflict, "node_not_linked", "节点尚未关联服务器", nil)
	case errors.Is(err, store.ErrRuntimeNotConfigured):
		writeAPIError(w, http.StatusConflict, "runtime_not_configured", "节点运行时配置尚未建立", nil)
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "conflict", "资源状态冲突", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), nil)
	default:
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
	}
}

func writeSuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"status": "success", "data": data})
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	payload := map[string]any{"code": code, "message": message}
	if len(fields) > 0 {
		payload["fields"] = fields
	}
	writeJSON(w, status, map[string]any{"status": "fail", "error": payload})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
