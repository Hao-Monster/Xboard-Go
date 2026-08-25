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

	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	SessionCookieName = "xboard_session"
	CSRFCookieName    = "xboard_csrf"
	maxJSONBody       = 1 << 20
)

type Dependencies struct {
	Context                    context.Context
	Store                      *store.Store
	PasswordHasher             security.PasswordHasher
	Now                        func() time.Time
	PanelURL                   string
	NodeRelease                string
	CookieSecure               bool
	AllowedOrigins             []string
	Logger                     *slog.Logger
	WebSocketEnabled           bool
	WebSocketURL               string
	NodePushInterval           int
	NodePullInterval           int
	CatalogHTTPClient          clientcatalog.HTTPDoer
	SettingsCipher             *appsettings.Cipher
	PasswordResetProtector     *security.PasswordResetProtector
	RegistrationEmailProtector *security.RegistrationEmailProtector
	InvitationProtector        *security.InvitationProtector
	LoginLinkProtector         *security.LoginLinkProtector
	SMTPAllowInsecure          bool
	RuntimeTracker             *operations.Tracker
	CaptchaVerifier            captcha.Verifier
}

type passwordService interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) bool
}

type server struct {
	store                      *store.Store
	passwordHasher             passwordService
	dummyHash                  string
	now                        func() time.Time
	panelURL                   string
	panelHostname              string
	nodeRelease                string
	cookieSecure               bool
	allowedOrigins             map[string]struct{}
	logger                     *slog.Logger
	loginAttempts              *attemptLimiter
	registrationRequests       *requestLimiter
	passwordResetRequests      *requestLimiter
	passwordResetConfirmations *requestLimiter
	registrationEmailRequests  *requestLimiter
	passportEmailRequests      *requestLimiter
	invitationViewRequests     *requestLimiter
	mailLoginRequests          *requestLimiter
	passwordHashSlots          chan struct{}
	enrollAttempts             *attemptLimiter
	machineAuthFailures        *attemptLimiter
	handshakeRequests          *requestLimitGroup
	pullRequests               *requestLimitGroup
	reportRequests             *requestLimitGroup
	machineRequests            *requestLimitGroup
	ticketRequests             *requestLimitGroup
	hub                        *wsHub
	webSocketEnabled           bool
	webSocketURL               string
	nodePushInterval           int
	nodePullInterval           int
	clientCatalog              *clientcatalog.Service
	settingsCipher             *appsettings.Cipher
	passwordResetProtector     *security.PasswordResetProtector
	registrationEmailProtector *security.RegistrationEmailProtector
	invitationProtector        *security.InvitationProtector
	loginLinkProtector         *security.LoginLinkProtector
	smtpAllowInsecure          bool
	runtimeTracker             *operations.Tracker
	captchaVerifier            captcha.Verifier
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
	if dependencies.RuntimeTracker == nil {
		dependencies.RuntimeTracker = operations.NewTracker(dependencies.Now())
	}
	dummyHash, err := dependencies.PasswordHasher.Hash("xboard-dummy-login-password")
	if err != nil {
		panic(fmt.Sprintf("httpapi: create dummy password hash: %v", err))
	}

	allowedOrigins := make(map[string]struct{})
	for _, origin := range dependencies.AllowedOrigins {
		allowedOrigins[strings.TrimRight(origin, "/")] = struct{}{}
	}
	panelHostname := ""
	if parsed, err := url.Parse(dependencies.PanelURL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		allowedOrigins[parsed.Scheme+"://"+parsed.Host] = struct{}{}
		panelHostname = parsed.Hostname()
	}

	api := &server{
		store:                      dependencies.Store,
		passwordHasher:             dependencies.PasswordHasher,
		dummyHash:                  dummyHash,
		now:                        dependencies.Now,
		panelURL:                   strings.TrimRight(dependencies.PanelURL, "/"),
		panelHostname:              panelHostname,
		nodeRelease:                dependencies.NodeRelease,
		cookieSecure:               dependencies.CookieSecure,
		allowedOrigins:             allowedOrigins,
		logger:                     dependencies.Logger,
		loginAttempts:              newAttemptLimiter(100, time.Minute),
		registrationRequests:       newRequestLimiter(20, 15*time.Minute),
		passwordResetRequests:      newRequestLimiter(10, 15*time.Minute),
		passwordResetConfirmations: newRequestLimiter(20, 15*time.Minute),
		registrationEmailRequests:  newRequestLimiter(10, 15*time.Minute),
		passportEmailRequests:      newRequestLimiter(10, 15*time.Minute),
		invitationViewRequests:     newRequestLimiter(60, 15*time.Minute),
		mailLoginRequests:          newRequestLimiter(10, 15*time.Minute),
		passwordHashSlots:          make(chan struct{}, 2),
		enrollAttempts:             newAttemptLimiter(20, 15*time.Minute),
		machineAuthFailures:        newAttemptLimiter(60, time.Minute),
		handshakeRequests:          newRequestLimitGroup(60, 20),
		pullRequests:               newRequestLimitGroup(2_400, 600),
		reportRequests:             newRequestLimitGroup(1_200, 240),
		machineRequests:            newRequestLimitGroup(1_200, 240),
		ticketRequests:             newRequestLimitGroup(240, 60),
		webSocketEnabled:           dependencies.WebSocketEnabled,
		webSocketURL:               strings.TrimRight(dependencies.WebSocketURL, "/"),
		nodePushInterval:           dependencies.NodePushInterval,
		nodePullInterval:           dependencies.NodePullInterval,
		clientCatalog: clientcatalog.New(clientcatalog.Options{
			Store: dependencies.Store, PanelURL: dependencies.PanelURL, HTTPClient: dependencies.CatalogHTTPClient, Now: dependencies.Now,
		}),
		settingsCipher:             dependencies.SettingsCipher,
		passwordResetProtector:     dependencies.PasswordResetProtector,
		registrationEmailProtector: dependencies.RegistrationEmailProtector,
		invitationProtector:        dependencies.InvitationProtector,
		loginLinkProtector:         dependencies.LoginLinkProtector,
		smtpAllowInsecure:          dependencies.SMTPAllowInsecure,
		runtimeTracker:             dependencies.RuntimeTracker,
		captchaVerifier:            dependencies.CaptchaVerifier,
	}
	if dependencies.WebSocketEnabled {
		api.hub = newWSHub(dependencies.Store, dependencies.Now, dependencies.Logger, allowedOrigins, dependencies.NodePushInterval, dependencies.NodePullInterval)
		go api.hub.runUntil(dependencies.Context)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", api.health)
	root.HandleFunc("GET /ws", api.webSocket)
	root.HandleFunc("GET /api/v1/guest/comm/config", api.getGuestConfig)
	root.HandleFunc("POST /api/v1/auth/login", api.login)
	root.HandleFunc("POST /api/v1/passport/auth/login", api.legacyLogin)
	root.Handle("POST /api/v1/auth/mail-link/request", api.requireTrustedOrigin(http.HandlerFunc(api.requestMailLoginLink)))
	root.Handle("POST /api/v1/auth/login-link/exchange", api.requireTrustedOrigin(http.HandlerFunc(api.exchangeLoginLink)))
	root.Handle("POST /api/v1/auth/quick-link", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createQuickLoginLink))))
	root.Handle("POST /api/v1/passport/auth/loginWithMailLink", api.requireTrustedOrigin(http.HandlerFunc(api.requestMailLoginLink)))
	root.HandleFunc("POST /api/v1/passport/auth/getQuickLoginUrl", api.legacyPassportQuickLoginLink)
	root.Handle("POST /api/v1/user/getQuickLoginUrl", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createQuickLoginLink))))
	root.HandleFunc("GET /api/v1/passport/auth/token2Login", api.legacyTokenToLogin)
	root.Handle("POST /api/v1/auth/register", api.requireTrustedOrigin(http.HandlerFunc(api.register)))
	root.HandleFunc("POST /api/v1/passport/auth/register", api.legacyRegister)
	root.Handle("POST /api/v1/auth/registration-email/request", api.requireTrustedOrigin(http.HandlerFunc(api.requestRegistrationEmailVerification)))
	root.Handle("POST /api/v1/auth/password-reset/request", api.requireTrustedOrigin(http.HandlerFunc(api.requestPasswordReset)))
	root.Handle("POST /api/v1/auth/password-reset/confirm", api.requireTrustedOrigin(http.HandlerFunc(api.confirmPasswordReset)))
	root.Handle("POST /api/v1/passport/comm/sendEmailVerify", api.requireTrustedOrigin(http.HandlerFunc(api.legacySendEmailVerify)))
	root.Handle("POST /api/v2/passport/comm/sendEmailVerify", api.requireTrustedOrigin(http.HandlerFunc(api.legacySendEmailVerify)))
	root.Handle("POST /api/v1/passport/auth/forget", api.requireTrustedOrigin(http.HandlerFunc(api.legacyForgetPassword)))
	root.Handle("POST /api/v2/passport/auth/forget", api.requireTrustedOrigin(http.HandlerFunc(api.legacyForgetPassword)))
	root.Handle("GET /api/v1/auth/session", api.requireSession(http.HandlerFunc(api.session)))
	root.Handle("POST /api/v1/auth/logout", api.requireSession(api.requireCSRF(http.HandlerFunc(api.logout))))
	root.Handle("GET /api/v1/auth/sessions", api.requireSession(http.HandlerFunc(api.listAccountSessions)))
	root.Handle("DELETE /api/v1/auth/sessions/{sessionID}", api.requireSession(api.requireCSRF(http.HandlerFunc(api.revokeAccountSession))))
	root.Handle("GET /api/v1/auth/access-tokens", api.requireSession(http.HandlerFunc(api.listAccessTokens)))
	root.Handle("POST /api/v1/auth/access-tokens", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createAccessToken))))
	root.Handle("DELETE /api/v1/auth/access-tokens", api.requireSession(api.requireCSRF(http.HandlerFunc(api.revokeAllAccessTokens))))
	root.Handle("DELETE /api/v1/auth/access-tokens/{tokenID}", api.requireSession(api.requireCSRF(http.HandlerFunc(api.revokeAccessToken))))
	root.Handle("PUT /api/v1/auth/password", api.requireSession(api.requireCSRF(http.HandlerFunc(api.changePassword))))
	root.Handle("GET /api/v1/user/getActiveSession", api.requireLegacyBearer(http.HandlerFunc(api.legacyListAccessTokens)))
	root.Handle("POST /api/v1/user/removeActiveSession", api.requireLegacyBearer(http.HandlerFunc(api.legacyRemoveAccessToken)))
	root.Handle("POST /api/v1/user/logout", api.requireLegacyBearer(http.HandlerFunc(api.legacyLogout)))
	root.Handle("GET /api/v1/invitations", api.requireSession(http.HandlerFunc(api.getInvitations)))
	root.Handle("POST /api/v1/invitations", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createInvitation))))
	root.Handle("POST /api/v1/invitations/view", api.requireTrustedOrigin(http.HandlerFunc(api.recordInvitationView)))
	root.Handle("GET /api/v1/notices", api.requireSession(http.HandlerFunc(api.listVisibleNotices)))
	root.Handle("GET /api/v1/knowledge", api.requireSession(http.HandlerFunc(api.listUserKnowledge)))
	root.Handle("GET /api/v1/knowledge/{knowledgeID}", api.requireSession(http.HandlerFunc(api.getUserKnowledge)))
	root.Handle("GET /api/v1/client-catalog", api.requireSession(http.HandlerFunc(api.listUserClientCatalog)))
	root.Handle("GET /api/v1/client-catalog/qr", api.requireSession(http.HandlerFunc(api.clientCatalogQR)))
	root.Handle("GET /api/v1/tickets", api.requireSession(http.HandlerFunc(api.listUserTickets)))
	root.Handle("POST /api/v1/tickets", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createTicket))))
	root.Handle("GET /api/v1/tickets/{ticketID}", api.requireSession(http.HandlerFunc(api.getUserTicket)))
	root.Handle("POST /api/v1/tickets/{ticketID}/messages", api.requireSession(api.requireCSRF(http.HandlerFunc(api.replyUserTicket))))
	root.Handle("POST /api/v1/tickets/{ticketID}/close", api.requireSession(api.requireCSRF(http.HandlerFunc(api.closeUserTicket))))
	root.HandleFunc("GET /client-download/{clientID}/{platform}", api.clientDownloadRedirect)
	root.HandleFunc("GET /client-link/{clientID}/{platform}/{action}", api.clientActionRedirect)
	root.HandleFunc("GET /guide/{knowledgeID}", api.publicKnowledge)
	root.HandleFunc("GET /guide/{knowledgeID}/{tail}", api.publicKnowledge)
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
	admin.HandleFunc("GET /api/v1/admin/server-groups", api.listServerGroups)
	admin.HandleFunc("POST /api/v1/admin/server-groups", api.createServerGroup)
	admin.HandleFunc("PATCH /api/v1/admin/server-groups/{groupID}", api.updateServerGroup)
	admin.HandleFunc("DELETE /api/v1/admin/server-groups/{groupID}", api.deleteServerGroup)
	admin.HandleFunc("GET /api/v1/admin/routing-rules", api.listRoutingRules)
	admin.HandleFunc("POST /api/v1/admin/routing-rules", api.createRoutingRule)
	admin.HandleFunc("PATCH /api/v1/admin/routing-rules/{routeID}", api.updateRoutingRule)
	admin.HandleFunc("DELETE /api/v1/admin/routing-rules/{routeID}", api.deleteRoutingRule)
	admin.HandleFunc("GET /api/v1/admin/notices", api.listAdminNotices)
	admin.HandleFunc("POST /api/v1/admin/notices", api.createNotice)
	admin.HandleFunc("PUT /api/v1/admin/notices/order", api.reorderNotices)
	admin.HandleFunc("PATCH /api/v1/admin/notices/{noticeID}", api.updateNotice)
	admin.HandleFunc("PATCH /api/v1/admin/notices/{noticeID}/visibility", api.setNoticeVisibility)
	admin.HandleFunc("DELETE /api/v1/admin/notices/{noticeID}", api.deleteNotice)
	admin.HandleFunc("GET /api/v1/admin/knowledge", api.listAdminKnowledge)
	admin.HandleFunc("POST /api/v1/admin/knowledge", api.createKnowledge)
	admin.HandleFunc("PUT /api/v1/admin/knowledge/order", api.reorderKnowledge)
	admin.HandleFunc("GET /api/v1/admin/knowledge/categories", api.listKnowledgeCategories)
	admin.HandleFunc("GET /api/v1/admin/knowledge/{knowledgeID}", api.getAdminKnowledge)
	admin.HandleFunc("PATCH /api/v1/admin/knowledge/{knowledgeID}", api.updateKnowledge)
	admin.HandleFunc("PATCH /api/v1/admin/knowledge/{knowledgeID}/visibility", api.setKnowledgeVisibility)
	admin.HandleFunc("DELETE /api/v1/admin/knowledge/{knowledgeID}", api.deleteKnowledge)
	admin.HandleFunc("GET /api/v1/admin/client-catalog", api.listAdminClientCatalog)
	admin.HandleFunc("PUT /api/v1/admin/client-catalog", api.saveClientCatalog)
	admin.HandleFunc("GET /api/v1/admin/tickets", api.listAdminTickets)
	admin.HandleFunc("GET /api/v1/admin/ticket-settings", api.getTicketSettings)
	admin.HandleFunc("PUT /api/v1/admin/ticket-settings", api.updateTicketSettings)
	admin.HandleFunc("GET /api/v1/admin/site-settings", api.getSiteSettings)
	admin.HandleFunc("PUT /api/v1/admin/site-settings", api.updateSiteSettings)
	admin.HandleFunc("GET /api/v1/admin/tickets/{ticketID}", api.getAdminTicket)
	admin.HandleFunc("POST /api/v1/admin/tickets/{ticketID}/messages", api.replyAdminTicket)
	admin.HandleFunc("POST /api/v1/admin/tickets/{ticketID}/close", api.closeAdminTicket)
	admin.HandleFunc("GET /api/v1/admin/users", api.listAdminUsers)
	admin.HandleFunc("POST /api/v1/admin/users", api.createAdminUser)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}", api.getAdminUser)
	admin.HandleFunc("PATCH /api/v1/admin/users/{userID}", api.updateAdminUser)
	admin.HandleFunc("PUT /api/v1/admin/users/{userID}/password", api.resetAdminUserPassword)
	admin.HandleFunc("GET /api/v1/admin/nodes/{nodeID}/activation-schedule", api.getActivationSchedule)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/activation-schedule", api.saveActivationSchedule)
	admin.HandleFunc("DELETE /api/v1/admin/nodes/{nodeID}/activation-schedule", api.deleteActivationSchedule)
	admin.HandleFunc("GET /api/v1/admin/system/status", api.getSystemStatus)
	admin.HandleFunc("GET /api/v1/admin/system/audit", api.listAdminAudit)
	admin.HandleFunc("GET /api/v1/admin/system/mail-failures", api.listTicketMailFailures)
	root.Handle("/api/v1/admin/", api.requireSession(api.requireAdmin(api.auditAdminMutations(api.requireCSRF(api.recoverPanic(admin))))))

	return api.securityHeaders(api.recoverPanic(root))
}

func (s *server) auditAdminMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		route := r.Pattern
		if route == "" || route == "/api/v1/admin/" {
			route = r.URL.Path
		} else if _, patternRoute, found := strings.Cut(route, " "); found {
			route = patternRoute
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
		defer cancel()
		if err := s.store.RecordAdminAudit(ctx, store.AdminAuditInput{
			AdministratorID: session.UserID, AdministratorEmail: session.Email,
			Method: r.Method, Route: route, StatusCode: recorder.statusCode(),
		}, s.now()); err != nil {
			s.logger.Warn("record administrator audit", "administrator_id", session.UserID, "method", r.Method, "route", route, "error", err)
		}
	})
}

type responseStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusRecorder) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusRecorder) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseStatusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseStatusRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
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
		if r.Header.Get("Authorization") != "" {
			session, err := s.authenticateBearer(r)
			if err != nil {
				writeAPIError(w, http.StatusUnauthorized, "unauthenticated", "登录凭证无效或已撤销", nil)
				return
			}
			ctx := context.WithValue(r.Context(), sessionContextKey, session)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
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

func (s *server) requireLegacyBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := s.authenticateBearer(r)
		if err != nil {
			writeAPIError(w, http.StatusForbidden, "unauthenticated", "登录凭证无效或已撤销", nil)
			return
		}
		ctx := context.WithValue(r.Context(), sessionContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *server) authenticateBearer(r *http.Request) (store.SessionUser, error) {
	return s.authenticateBearerValue(r.Context(), r.Header.Get("Authorization"))
}

func (s *server) authenticateBearerValue(ctx context.Context, authorization string) (store.SessionUser, error) {
	header := strings.TrimSpace(authorization)
	if len(header) < 8 || len(header) > 256 {
		return store.SessionUser{}, store.ErrNotFound
	}
	scheme, plaintext, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || len(plaintext) != 48 || strings.ContainsAny(plaintext, " \t\r\n") {
		return store.SessionUser{}, store.ErrNotFound
	}
	return s.store.AuthenticateAccessToken(ctx, security.DigestToken(plaintext), s.now())
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
		if session, ok := sessionFromContext(r.Context()); ok && session.CredentialKind == store.CredentialKindAccessToken {
			next.ServeHTTP(w, r)
			return
		}
		if !s.originTrusted(r) {
			writeAPIError(w, http.StatusForbidden, "invalid_origin", "请求来源不受信任", nil)
			return
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

func (s *server) requireTrustedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.originTrusted(r) {
			writeAPIError(w, http.StatusForbidden, "invalid_origin", "请求来源不受信任", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) originTrusted(r *http.Request) bool {
	origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
	if origin == "" {
		return true
	}
	_, allowed := s.allowedOrigins[origin]
	return allowed
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
	writeJSON(w, status, map[string]any{"status": "fail", "message": message, "error": payload})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
