package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/attachments"
	"github.com/Hao-Monster/Xboard-Go/internal/bulkops"
	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/telegrambot"
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
	LegacyAdminPath            string
	NodeRelease                string
	CookieSecure               bool
	AllowedOrigins             []string
	Logger                     *slog.Logger
	WebSocketEnabled           bool
	WebSocketURL               string
	NodePushInterval           int
	NodePullInterval           int
	NodeCoordinator            nodecoord.Coordinator
	CatalogHTTPClient          clientcatalog.HTTPDoer
	SettingsCipher             *appsettings.Cipher
	PasswordResetProtector     *security.PasswordResetProtector
	RegistrationEmailProtector *security.RegistrationEmailProtector
	InvitationProtector        *security.InvitationProtector
	LoginLinkProtector         *security.LoginLinkProtector
	SMTPAllowInsecure          bool
	MailSender                 mailer.Sender
	RuntimeTracker             *operations.Tracker
	CaptchaVerifier            captcha.Verifier
	PaymentGateway             paymentGateway
	Attachments                *attachments.Service
	BulkOperations             *bulkops.Service
	TelegramBot                telegrambot.Client
}

type paymentGateway interface {
	Checkout(context.Context, payment.CheckoutRequest) (payment.CheckoutResult, error)
	VerifyWebhook(context.Context, payment.WebhookRequest) (payment.VerifiedWebhook, error)
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
	subscriptionFailures       *attemptLimiter
	subscriptionResetRequests  *requestLimitGroup
	passwordHashSlots          chan struct{}
	adminUserGenerationSlots   chan struct{}
	enrollAttempts             *attemptLimiter
	machineAuthFailures        *attemptLimiter
	legacyNodeAuthFailures     *attemptLimiter
	handshakeRequests          *requestLimitGroup
	pullRequests               *requestLimitGroup
	reportRequests             *requestLimitGroup
	machineRequests            *requestLimitGroup
	ticketRequests             *requestLimitGroup
	orderRequests              *requestLimitGroup
	paymentWebhookRequests     *requestLimiter
	hub                        *wsHub
	webSocketEnabled           bool
	legacyHTTPAuthSuccess      atomic.Uint64
	legacyWebSocketAuthSuccess atomic.Uint64
	legacyLastUsedUnix         atomic.Int64
	clientCatalog              *clientcatalog.Service
	settingsCipher             *appsettings.Cipher
	passwordResetProtector     *security.PasswordResetProtector
	registrationEmailProtector *security.RegistrationEmailProtector
	invitationProtector        *security.InvitationProtector
	loginLinkProtector         *security.LoginLinkProtector
	smtpAllowInsecure          bool
	mailSender                 mailer.Sender
	smtpTestRequests           *requestLimiter
	runtimeTracker             *operations.Tracker
	captchaVerifier            captcha.Verifier
	paymentGateway             paymentGateway
	attachments                *attachments.Service
	bulkOperations             *bulkops.Service
	telegramBot                telegrambot.Client
	telegramProvisionRequests  *requestLimiter
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
	if dependencies.LegacyAdminPath == "" {
		dependencies.LegacyAdminPath = "admin"
	}
	if !validLegacyAdminPath(dependencies.LegacyAdminPath) {
		panic("httpapi: LegacyAdminPath must contain 1 to 64 ASCII letters, digits, underscores, or hyphens")
	}
	if dependencies.NodePushInterval == 0 {
		dependencies.NodePushInterval = 60
	}
	if dependencies.NodePullInterval == 0 {
		dependencies.NodePullInterval = 60
	}
	if err := dependencies.Store.EnsureNodeAgentSettings(dependencies.Context, store.NodeAgentSettingsDefaults{
		PullInterval: dependencies.NodePullInterval, PushInterval: dependencies.NodePushInterval,
		WebSocketEnabled: dependencies.WebSocketEnabled, WebSocketURL: strings.TrimRight(dependencies.WebSocketURL, "/"),
	}, dependencies.Now()); err != nil {
		panic(fmt.Sprintf("httpapi: ensure node agent settings: %v", err))
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	if dependencies.RuntimeTracker == nil {
		dependencies.RuntimeTracker = operations.NewTracker(dependencies.Now())
	}
	if dependencies.PaymentGateway == nil {
		dependencies.PaymentGateway = payment.NewService(payment.Options{})
	}
	if dependencies.TelegramBot == nil {
		telegramClient, err := telegrambot.New(telegrambot.Options{})
		if err != nil {
			panic(fmt.Sprintf("httpapi: initialize Telegram client: %v", err))
		}
		dependencies.TelegramBot = telegramClient
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
		subscriptionFailures:       newAttemptLimiter(1_200, 15*time.Minute),
		subscriptionResetRequests:  newRequestLimitGroup(60, 6),
		passwordHashSlots:          make(chan struct{}, 2),
		adminUserGenerationSlots:   make(chan struct{}, 1),
		enrollAttempts:             newAttemptLimiter(20, 15*time.Minute),
		machineAuthFailures:        newAttemptLimiter(60, time.Minute),
		legacyNodeAuthFailures:     newAttemptLimiter(60, time.Minute),
		handshakeRequests:          newRequestLimitGroup(60, 20),
		pullRequests:               newRequestLimitGroup(2_400, 600),
		reportRequests:             newRequestLimitGroup(1_200, 240),
		machineRequests:            newRequestLimitGroup(1_200, 240),
		ticketRequests:             newRequestLimitGroup(240, 60),
		orderRequests:              newRequestLimitGroup(240, 60),
		paymentWebhookRequests:     newRequestLimiter(600, time.Minute),
		smtpTestRequests:           newRequestLimiter(3, time.Minute),
		telegramProvisionRequests:  newRequestLimiter(3, time.Minute),
		webSocketEnabled:           dependencies.WebSocketEnabled,
		clientCatalog: clientcatalog.New(clientcatalog.Options{
			Store: dependencies.Store, PanelURL: dependencies.PanelURL, HTTPClient: dependencies.CatalogHTTPClient, Now: dependencies.Now,
		}),
		settingsCipher:             dependencies.SettingsCipher,
		passwordResetProtector:     dependencies.PasswordResetProtector,
		registrationEmailProtector: dependencies.RegistrationEmailProtector,
		invitationProtector:        dependencies.InvitationProtector,
		loginLinkProtector:         dependencies.LoginLinkProtector,
		smtpAllowInsecure:          dependencies.SMTPAllowInsecure,
		mailSender:                 dependencies.MailSender,
		runtimeTracker:             dependencies.RuntimeTracker,
		captchaVerifier:            dependencies.CaptchaVerifier,
		paymentGateway:             dependencies.PaymentGateway,
		attachments:                dependencies.Attachments,
		bulkOperations:             dependencies.BulkOperations,
		telegramBot:                dependencies.TelegramBot,
	}
	if dependencies.WebSocketEnabled {
		api.hub = newWSHub(dependencies.Store, dependencies.Now, dependencies.Logger, allowedOrigins, dependencies.NodeCoordinator)
		if dependencies.NodeCoordinator != nil {
			if err := dependencies.NodeCoordinator.Start(dependencies.Context, api.hub.handleCoordinationEvent); err != nil {
				panic(fmt.Sprintf("httpapi: start node coordinator: %v", err))
			}
		}
		go api.hub.runUntil(dependencies.Context)
	}

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", api.health)
	root.HandleFunc("GET /ws", api.webSocket)
	root.HandleFunc("GET /api/v1/guest/comm/config", api.getGuestConfig)
	root.HandleFunc("POST /api/v1/guest/telegram/webhook", api.telegramWebhook)
	root.HandleFunc("GET /api/v1/guest/plans", api.listGuestPlans)
	root.HandleFunc("GET /api/v1/guest/payment/notify/{method}/{uuid}", api.paymentWebhook)
	root.HandleFunc("POST /api/v1/guest/payment/notify/{method}/{uuid}", api.paymentWebhook)
	root.HandleFunc("GET /api/v1/client/subscribe", api.clientSubscription)
	root.HandleFunc("POST /api/v1/auth/login", api.login)
	root.Handle("POST /api/v1/passport/auth/login", api.requireTrustedOrigin(http.HandlerFunc(api.legacyLogin)))
	root.Handle("POST /api/v2/passport/auth/login", api.requireTrustedOrigin(http.HandlerFunc(api.legacyLogin)))
	root.Handle("POST /api/v1/auth/mail-link/request", api.requireTrustedOrigin(http.HandlerFunc(api.requestMailLoginLink)))
	root.Handle("POST /api/v1/auth/login-link/exchange", api.requireTrustedOrigin(http.HandlerFunc(api.exchangeLoginLink)))
	root.Handle("POST /api/v1/auth/quick-link", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createQuickLoginLink))))
	root.Handle("POST /api/v1/passport/auth/loginWithMailLink", api.requireTrustedOrigin(http.HandlerFunc(api.requestMailLoginLink)))
	root.Handle("POST /api/v2/passport/auth/loginWithMailLink", api.requireTrustedOrigin(http.HandlerFunc(api.requestMailLoginLink)))
	root.Handle("POST /api/v1/passport/auth/getQuickLoginUrl", api.requireTrustedOrigin(http.HandlerFunc(api.legacyPassportQuickLoginLink)))
	root.Handle("POST /api/v2/passport/auth/getQuickLoginUrl", api.requireTrustedOrigin(http.HandlerFunc(api.legacyPassportQuickLoginLink)))
	root.Handle("POST /api/v1/user/getQuickLoginUrl", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createQuickLoginLink))))
	root.HandleFunc("GET /api/v1/passport/auth/token2Login", api.legacyTokenToLogin)
	root.HandleFunc("GET /api/v2/passport/auth/token2Login", api.legacyTokenToLogin)
	root.Handle("POST /api/v1/auth/register", api.requireTrustedOrigin(http.HandlerFunc(api.register)))
	root.Handle("POST /api/v1/passport/auth/register", api.requireTrustedOrigin(http.HandlerFunc(api.legacyRegister)))
	root.Handle("POST /api/v2/passport/auth/register", api.requireTrustedOrigin(http.HandlerFunc(api.legacyRegister)))
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
	root.Handle("GET /api/v1/user/telegram/getBotInfo", api.requireLegacyBearer(http.HandlerFunc(api.legacyTelegramBotInfo)))
	root.Handle("GET /api/v1/invitations", api.requireSession(http.HandlerFunc(api.getInvitations)))
	root.Handle("GET /api/v1/invitations/commissions", api.requireSession(http.HandlerFunc(api.listCommissionLogs)))
	root.Handle("GET /api/v1/user/invite/fetch", api.requireSession(http.HandlerFunc(api.legacyGetInvitations)))
	root.Handle("GET /api/v1/user/invite/details", api.requireSession(http.HandlerFunc(api.legacyListCommissionLogs)))
	root.Handle("GET /api/v1/user/invite/save", api.requireSession(http.HandlerFunc(api.legacyCreateInvitation)))
	root.Handle("GET /api/v1/plans", api.requireSession(http.HandlerFunc(api.listUserPlans)))
	root.Handle("GET /api/v1/distributor/orders", api.requireSession(api.requireDistributor(http.HandlerFunc(api.listDistributorOrders))))
	root.Handle("POST /api/v1/distributor/orders", api.requireSession(api.requireDistributor(api.requireCSRF(http.HandlerFunc(api.createDistributorOrder)))))
	root.Handle("GET /api/v1/distributor/orders/export", api.requireSession(api.requireDistributor(http.HandlerFunc(api.exportDistributorOrders))))
	root.Handle("GET /api/v1/distributor/orders/{tradeNo}", api.requireSession(api.requireDistributor(http.HandlerFunc(api.getDistributorOrder))))
	root.Handle("POST /api/v1/distributor/orders/{tradeNo}/renew", api.requireSession(api.requireDistributor(api.requireCSRF(http.HandlerFunc(api.renewDistributorOrder)))))
	root.Handle("GET /api/v1/distributor/orders/{tradeNo}/qr", api.requireSession(api.requireDistributor(http.HandlerFunc(api.getDistributorOrderQR))))
	root.Handle("GET /api/v1/orders", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.listUserOrders))))
	root.Handle("POST /api/v1/orders", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.createOrder)))))
	root.Handle("POST /api/v1/user/coupons/check", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.checkUserCoupon)))))
	root.Handle("GET /api/v1/payments", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.listUserPayments))))
	root.Handle("GET /api/v1/orders/{tradeNo}", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.getUserOrder))))
	root.Handle("POST /api/v1/orders/{tradeNo}/checkout", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.checkoutUserOrder)))))
	root.Handle("POST /api/v1/orders/{tradeNo}/cancel", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.cancelUserOrder)))))
	root.Handle("GET /api/v1/user/order/fetch", api.requireLegacyBearer(http.HandlerFunc(api.legacyListUserOrders)))
	root.Handle("GET /api/v1/user/order/export", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.exportDistributorOrders))))
	root.Handle("POST /api/v1/user/order/save", api.requireLegacyBearer(http.HandlerFunc(api.legacyCreateOrder)))
	root.Handle("POST /api/v1/user/order/renew", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.legacyRenewDistributorOrder))))
	root.Handle("GET /api/v1/user/order/detail", api.requireLegacyBearer(http.HandlerFunc(api.legacyGetUserOrder)))
	root.Handle("GET /api/v1/user/order/check", api.requireLegacyBearer(http.HandlerFunc(api.legacyCheckUserOrder)))
	root.Handle("GET /api/v1/user/order/getPaymentMethod", api.requireLegacyBearer(http.HandlerFunc(api.legacyPaymentMethods)))
	root.Handle("POST /api/v1/user/order/checkout", api.requireLegacyBearer(http.HandlerFunc(api.legacyCheckoutUserOrder)))
	root.Handle("POST /api/v1/user/order/cancel", api.requireLegacyBearer(http.HandlerFunc(api.legacyCancelUserOrder)))
	root.Handle("GET /api/v1/user/distributor/delivery", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.legacyDistributorDelivery))))
	root.Handle("GET /api/v1/user/distributor/subscription-qr", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.legacyDistributorSubscriptionQR))))
	root.Handle("POST /api/v1/user/distributor/close", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.legacyCloseDistributorDelivery))))
	root.Handle("POST /api/v1/user/distributor/delivery/close", api.requireLegacyBearer(api.requireDistributor(http.HandlerFunc(api.legacyCloseDistributorDelivery))))
	root.Handle("POST /api/v1/user/coupon/check", api.requireLegacyBearer(api.requireNonDistributor(http.HandlerFunc(api.legacyCheckUserCoupon))))
	root.Handle("POST /api/v1/user/gift-card/check", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.giftCardCheckCompat)))))
	root.Handle("POST /api/v1/user/gift-card/redeem", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.giftCardRedeemCompat)))))
	root.Handle("GET /api/v1/user/gift-card/history", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.giftCardHistoryCompat))))
	root.Handle("GET /api/v1/user/gift-card/history/{usageID}", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.giftCardHistoryDetail))))
	root.Handle("GET /api/v1/user/gift-card/detail", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.legacyGiftCardDetail))))
	root.Handle("GET /api/v1/user/gift-card/types", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.giftCardTypesCompat))))
	root.Handle("GET /api/v1/subscription", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.getUserSubscription))))
	root.Handle("GET /api/v1/subscription/qr", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.getUserSubscriptionQR))))
	root.Handle("POST /api/v1/subscription/security/reset", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.resetUserSubscriptionSecurity)))))
	root.Handle("GET /api/v1/user/getSubscribe", api.requireLegacyBearer(api.requireNonDistributor(http.HandlerFunc(api.legacyGetUserSubscription))))
	root.Handle("GET /api/v1/user/resetSecurity", api.requireLegacyBearer(api.requireNonDistributor(http.HandlerFunc(api.legacyResetUserSubscriptionSecurity))))
	root.Handle("POST /api/v1/invitations", api.requireSession(api.requireCSRF(http.HandlerFunc(api.createInvitation))))
	root.Handle("POST /api/v1/invitations/transfer", api.requireSession(api.requireCSRF(http.HandlerFunc(api.transferCommission))))
	root.Handle("POST /api/v1/user/transfer", api.requireSession(api.requireCSRF(http.HandlerFunc(api.legacyTransferCommission))))
	root.Handle("POST /api/v1/invitations/view", api.requireTrustedOrigin(http.HandlerFunc(api.recordInvitationView)))
	root.Handle("POST /api/v1/passport/comm/pv", api.requireTrustedOrigin(http.HandlerFunc(api.legacyRecordInvitationView)))
	root.Handle("POST /api/v2/passport/comm/pv", api.requireTrustedOrigin(http.HandlerFunc(api.legacyRecordInvitationView)))
	root.Handle("GET /api/v1/notices", api.requireSession(http.HandlerFunc(api.listVisibleNotices)))
	root.Handle("GET /api/v1/knowledge", api.requireSession(http.HandlerFunc(api.listUserKnowledge)))
	root.Handle("GET /api/v1/knowledge/{knowledgeID}", api.requireSession(http.HandlerFunc(api.getUserKnowledge)))
	root.Handle("GET /api/v1/client-catalog", api.requireSession(http.HandlerFunc(api.listUserClientCatalog)))
	root.Handle("GET /api/v1/client-catalog/qr", api.requireSession(http.HandlerFunc(api.clientCatalogQR)))
	root.Handle("GET /api/v1/tickets", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.listUserTickets))))
	root.Handle("POST /api/v1/tickets", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.createTicket)))))
	root.Handle("GET /api/v1/tickets/{ticketID}", api.requireSession(api.requireNonDistributor(http.HandlerFunc(api.getUserTicket))))
	root.Handle("POST /api/v1/tickets/{ticketID}/messages", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.replyUserTicket)))))
	root.Handle("POST /api/v1/tickets/{ticketID}/close", api.requireSession(api.requireNonDistributor(api.requireCSRF(http.HandlerFunc(api.closeUserTicket)))))
	root.HandleFunc("GET /client-download/{clientID}/{platform}", api.clientDownloadRedirect)
	root.HandleFunc("GET /client-link/{clientID}/{platform}/{action}", api.clientActionRedirect)
	root.HandleFunc("GET /guide/{knowledgeID}", api.publicKnowledge)
	root.HandleFunc("GET /guide/{knowledgeID}/{tail}", api.publicKnowledge)
	root.HandleFunc("GET /knowledge-attachments/{attachmentUUID}", api.readSignedKnowledgeAttachment)
	root.HandleFunc("HEAD /knowledge-attachments/{attachmentUUID}", api.readSignedKnowledgeAttachment)
	root.HandleFunc("GET /guide-attachments/{attachmentUUID}", api.readPublicKnowledgeAttachment)
	root.HandleFunc("HEAD /guide-attachments/{attachmentUUID}", api.readPublicKnowledgeAttachment)
	root.HandleFunc("GET /api/v1/client/distributor/claim/{claimToken}", api.claimDistributorSubscription)
	root.HandleFunc("HEAD /api/v1/client/distributor/claim/{claimToken}", api.claimDistributorSubscription)
	root.HandleFunc("POST /api/v1/machines/enroll", api.exchangeEnrollment)
	root.HandleFunc("GET /api/v1/machines/{machineID}/nodes", api.agentNodes)
	root.HandleFunc("POST /api/v1/machines/{machineID}/status", api.agentStatus)
	root.HandleFunc("POST /api/v2/server/machine/enroll", api.exchangeEnrollment)
	root.HandleFunc("POST /api/v2/server/machine/nodes", api.xboardNodeMachineNodes)
	root.HandleFunc("POST /api/v2/server/machine/status", api.xboardNodeMachineStatus)
	root.HandleFunc("GET /api/v2/server/handshake", api.xboardNodeHandshake)
	root.HandleFunc("POST /api/v2/server/handshake", api.xboardNodeHandshake)
	root.HandleFunc("GET /api/v2/server/config", api.xboardNodeConfig)
	root.HandleFunc("GET /api/v2/server/user", api.xboardNodeUsers)
	root.HandleFunc("POST /api/v2/server/push", api.xboardNodePush)
	root.HandleFunc("POST /api/v2/server/alive", api.xboardNodeAlive)
	root.HandleFunc("GET /api/v2/server/alivelist", api.xboardNodeAliveList)
	root.HandleFunc("POST /api/v2/server/status", api.xboardNodeStatus)
	root.HandleFunc("POST /api/v2/server/report", api.xboardNodeReport)
	root.HandleFunc("GET /api/v1/server/UniProxy/config", api.xboardNodeConfig)
	root.HandleFunc("GET /api/v1/server/UniProxy/user", api.xboardNodeUsers)
	root.HandleFunc("POST /api/v1/server/UniProxy/push", api.xboardNodePush)
	root.HandleFunc("POST /api/v1/server/UniProxy/alive", api.xboardNodeAlive)
	root.HandleFunc("GET /api/v1/server/UniProxy/alivelist", api.xboardNodeAliveList)
	root.HandleFunc("POST /api/v1/server/UniProxy/status", api.xboardNodeStatus)
	root.HandleFunc("GET /{subscriptionPath}/{subscriptionToken}", api.dynamicClientSubscription)
	legacyAdmin := http.NewServeMux()
	legacyAdminOrder := http.NewServeMux()
	legacyAdminOrder.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/order/fetch", api.legacyListAdminOrders)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/fetch", api.legacyListAdminOrders)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/assign", api.legacyAssignOrder)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/detail", api.legacyGetAdminOrder)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/update", api.legacyUpdateAdminOrderCommissionStatus)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/paid", api.legacyPaidAdminOrder)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/cancel", api.legacyCancelAdminOrder)
	legacyAdminOrder.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/order/export", api.exportAdminDistributorOrders)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/remark/update", api.legacyAdminUpdateDistributorRemark)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/entitlement/update", api.legacyAdminUpdateDistributorEntitlement)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/hwid/update", api.legacyAdminUpdateDistributorHWID)
	legacyAdminOrder.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/order/hwid/devices", api.legacyAdminDistributorHWIDDevices)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/hwid/device/delete", api.legacyAdminDeleteDistributorHWIDDevice)
	legacyAdminOrder.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/order/settlement/preview", api.legacyAdminDistributorSettlementPreview)
	legacyAdminOrder.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/order/settlement/settle", api.legacyAdminSettleDistributorOrders)
	legacyAdmin.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/order/", api.auditLegacyAdminOrderMutations(api.recoverPanic(legacyAdminOrder)))
	legacyAdmin.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/user/distributor/options", api.legacyAdminDistributorOptions)
	legacyAdmin.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/user/fetch", api.legacyListAdminUsers)
	legacyAdmin.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/fetch", api.legacyListAdminUsers)
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/update", api.auditLegacyAdminUserMutations(http.HandlerFunc(api.legacyUpdateAdminUser)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/generate", api.auditLegacyAdminUserMutations(http.HandlerFunc(api.legacyGenerateAdminUsers)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/sendMail", api.auditLegacyAdminUserMutations(http.HandlerFunc(api.legacyAdminUserBulkMail)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/dumpCSV", api.auditLegacyAdminUserMutations(http.HandlerFunc(api.legacyAdminUserBulkCSV)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/user/ban", api.auditLegacyAdminUserMutations(http.HandlerFunc(api.legacyAdminUserBulkBan)))
	legacyAdminTrafficReset := http.NewServeMux()
	legacyAdminTrafficReset.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/traffic-reset/reset-user", api.legacyResetAdminUserTraffic)
	legacyAdminTrafficReset.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/traffic-reset/user/{userID}/history", api.legacyListAdminUserTrafficResets)
	legacyAdmin.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/traffic-reset/", api.auditLegacyAdminTrafficResetMutations(api.recoverPanic(legacyAdminTrafficReset)))
	legacyAdmin.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/stat/getStatUser", api.legacyListAdminUserTraffic)
	legacyAdminCoupon := http.NewServeMux()
	legacyAdminCoupon.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/coupon/fetch", api.legacyListAdminCoupons)
	legacyAdminCoupon.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/coupon/fetch", api.legacyListAdminCoupons)
	legacyAdminCoupon.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/coupon/generate", api.legacyGenerateAdminCoupon)
	legacyAdminCoupon.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/coupon/show", api.legacyToggleAdminCoupon)
	legacyAdminCoupon.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/coupon/update", api.legacyUpdateAdminCoupon)
	legacyAdminCoupon.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/coupon/drop", api.legacyDeleteAdminCoupon)
	legacyAdmin.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/coupon/", api.auditLegacyAdminCouponMutations(api.recoverPanic(legacyAdminCoupon)))
	legacyAdminPayment := http.NewServeMux()
	legacyAdminPayment.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/payment/fetch", api.legacyFetchPayments)
	legacyAdminPayment.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/payment/getPaymentMethods", api.legacyListPaymentProviders)
	legacyAdminPayment.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/payment/getPaymentForm", api.legacyPaymentForm)
	legacyAdminPayment.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/payment/save", api.legacySavePayment)
	legacyAdminPayment.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/payment/drop", api.legacyDeletePayment)
	legacyAdminPayment.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/payment/show", api.legacyTogglePayment)
	legacyAdminPayment.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/payment/sort", api.legacyReorderPayments)
	legacyAdmin.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/payment/", api.auditLegacyAdminPaymentMutations(api.recoverPanic(legacyAdminPayment)))
	legacyAdminGiftCard := http.NewServeMux()
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/templates", api.legacyGiftCardTemplates)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/templates", api.legacyGiftCardTemplates)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/create-template", api.legacyCreateGiftCardTemplate)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/update-template", api.legacyUpdateGiftCardTemplate)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/delete-template", api.legacyDeleteGiftCardTemplate)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/generate-codes", api.legacyGenerateGiftCardCodes)
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/codes", api.legacyGiftCardCodes)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/codes", api.legacyGiftCardCodes)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/toggle-code", api.legacyToggleGiftCardCode)
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/export-codes", api.legacyExportGiftCardCodes)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/update-code", api.legacyUpdateGiftCardCode)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/delete-code", api.legacyDeleteGiftCardCode)
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/usages", api.legacyGiftCardUsages)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/usages", api.legacyGiftCardUsages)
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/statistics", api.legacyGiftCardStatistics)
	legacyAdminGiftCard.HandleFunc("POST /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/statistics", api.legacyGiftCardStatistics)
	legacyAdminGiftCard.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/gift-card/types", api.legacyGiftCardTypes)
	legacyAdmin.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/gift-card/", api.auditLegacyAdminGiftCardMutations(api.recoverPanic(legacyAdminGiftCard)))
	legacyAdmin.HandleFunc("GET /api/v2/"+dependencies.LegacyAdminPath+"/config/fetch", api.legacyFetchConfigSettings)
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/config/save", api.auditLegacyAdminConfigMutations(http.HandlerFunc(api.legacySaveConfigSettings)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/config/testSendMail", api.auditLegacyAdminConfigMutations(http.HandlerFunc(api.legacyTestSendMail)))
	legacyAdmin.Handle("POST /api/v2/"+dependencies.LegacyAdminPath+"/config/setTelegramWebhook", api.auditLegacyAdminConfigMutations(http.HandlerFunc(api.legacyProvisionTelegramWebhook)))
	legacyMailTemplates := http.NewServeMux()
	legacyMailTemplatePrefix := "/api/v2/" + dependencies.LegacyAdminPath + "/mail/template"
	legacyMailTemplates.HandleFunc("GET "+legacyMailTemplatePrefix+"/list", api.legacyListMailTemplates)
	legacyMailTemplates.HandleFunc("GET "+legacyMailTemplatePrefix+"/get", api.legacyGetMailTemplate)
	legacyMailTemplates.HandleFunc("POST "+legacyMailTemplatePrefix+"/save", api.legacySaveMailTemplate)
	legacyMailTemplates.HandleFunc("POST "+legacyMailTemplatePrefix+"/reset", api.legacyResetMailTemplate)
	legacyMailTemplates.HandleFunc("POST "+legacyMailTemplatePrefix+"/test", api.legacyTestMailTemplate)
	legacyAdmin.Handle(legacyMailTemplatePrefix+"/", api.auditLegacyAdminMailTemplateMutations(api.recoverPanic(legacyMailTemplates)))
	legacyKnowledgeAttachments := http.NewServeMux()
	legacyAttachmentPrefix := "/api/v2/" + dependencies.LegacyAdminPath + "/knowledge/attachment"
	registerKnowledgeAttachmentRoutes(legacyKnowledgeAttachments, legacyAttachmentPrefix, api)
	legacyAdmin.Handle(legacyAttachmentPrefix+"/", api.auditLegacyKnowledgeAttachmentMutations(api.recoverPanic(legacyKnowledgeAttachments)))
	root.Handle("/api/v2/"+dependencies.LegacyAdminPath+"/", api.requireLegacyBearer(api.requireAdmin(api.recoverPanic(legacyAdmin))))

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
	admin.HandleFunc("GET /api/v1/admin/nodes", api.listAdminNodes)
	admin.HandleFunc("POST /api/v1/admin/nodes", api.createNode)
	admin.HandleFunc("GET /api/v1/admin/nodes/{nodeID}", api.getAdminNodeDefinition)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}", api.replaceAdminNodeDefinition)
	admin.HandleFunc("PATCH /api/v1/admin/nodes/{nodeID}", api.updateAdminNode)
	admin.HandleFunc("POST /api/v1/admin/nodes/{nodeID}/copy", api.copyAdminNode)
	admin.HandleFunc("PUT /api/v1/admin/nodes/order", api.reorderAdminNodes)
	admin.HandleFunc("POST /api/v1/admin/nodes/bulk-state", api.updateAdminNodeStates)
	admin.HandleFunc("POST /api/v1/admin/nodes/bulk-reset-traffic", api.resetAdminNodeTraffic)
	admin.HandleFunc("POST /api/v1/admin/nodes/bulk-delete", api.deleteAdminNodes)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/runtime", api.saveNodeRuntime)
	admin.HandleFunc("GET /api/v1/admin/server-groups", api.listServerGroups)
	admin.HandleFunc("POST /api/v1/admin/server-groups", api.createServerGroup)
	admin.HandleFunc("PATCH /api/v1/admin/server-groups/{groupID}", api.updateServerGroup)
	admin.HandleFunc("DELETE /api/v1/admin/server-groups/{groupID}", api.deleteServerGroup)
	admin.HandleFunc("GET /api/v1/admin/plans", api.listAdminPlans)
	admin.HandleFunc("POST /api/v1/admin/plans", api.createPlan)
	admin.HandleFunc("PUT /api/v1/admin/plans/order", api.reorderPlans)
	admin.HandleFunc("PATCH /api/v1/admin/plans/{planID}", api.updatePlan)
	admin.HandleFunc("PATCH /api/v1/admin/plans/{planID}/state", api.setPlanState)
	admin.HandleFunc("DELETE /api/v1/admin/plans/{planID}", api.deletePlan)
	admin.HandleFunc("GET /api/v1/admin/orders", api.listAdminOrders)
	admin.HandleFunc("POST /api/v1/admin/orders", api.assignOrder)
	admin.HandleFunc("GET /api/v1/admin/orders/{tradeNo}", api.getAdminOrder)
	admin.HandleFunc("PATCH /api/v1/admin/orders/{tradeNo}/commission", api.updateAdminOrderCommissionStatus)
	admin.HandleFunc("POST /api/v1/admin/orders/{tradeNo}/paid", api.paidAdminOrder)
	admin.HandleFunc("POST /api/v1/admin/orders/{tradeNo}/cancel", api.cancelAdminOrder)
	admin.HandleFunc("GET /api/v1/admin/distributors/options", api.listAdminDistributorOptions)
	admin.HandleFunc("GET /api/v1/admin/distributor-orders", api.listAdminDistributorOrders)
	admin.HandleFunc("GET /api/v1/admin/distributor-orders/export", api.exportAdminDistributorOrders)
	admin.HandleFunc("GET /api/v1/admin/distributor-orders/{orderID}", api.getAdminDistributorOrder)
	admin.HandleFunc("PATCH /api/v1/admin/distributor-orders/{orderID}/remark", api.updateAdminDistributorRemark)
	admin.HandleFunc("PATCH /api/v1/admin/distributor-orders/{orderID}/entitlement", api.updateAdminDistributorEntitlement)
	admin.HandleFunc("PATCH /api/v1/admin/distributor-orders/{orderID}/hwid", api.updateAdminDistributorHWID)
	admin.HandleFunc("GET /api/v1/admin/distributor-orders/{orderID}/hwid/devices", api.listAdminDistributorHWIDDevices)
	admin.HandleFunc("DELETE /api/v1/admin/distributor-orders/{orderID}/hwid/devices/{deviceID}", api.deleteAdminDistributorHWIDDevice)
	admin.HandleFunc("GET /api/v1/admin/distributors/{userID}/settlement", api.previewAdminDistributorSettlement)
	admin.HandleFunc("POST /api/v1/admin/distributors/{userID}/settlement", api.settleAdminDistributorOrders)
	admin.HandleFunc("GET /api/v1/admin/coupons", api.listAdminCoupons)
	admin.HandleFunc("POST /api/v1/admin/coupons", api.createAdminCoupon)
	admin.HandleFunc("POST /api/v1/admin/coupons/batch", api.batchAdminCoupons)
	admin.HandleFunc("PUT /api/v1/admin/coupons/{couponID}", api.updateAdminCoupon)
	admin.HandleFunc("PATCH /api/v1/admin/coupons/{couponID}/visibility", api.setAdminCouponVisibility)
	admin.HandleFunc("DELETE /api/v1/admin/coupons/{couponID}", api.deleteAdminCoupon)
	admin.HandleFunc("GET /api/v1/admin/payment-providers", api.listPaymentProviders)
	admin.HandleFunc("GET /api/v1/admin/payments", api.listAdminPayments)
	admin.HandleFunc("POST /api/v1/admin/payments", api.createAdminPayment)
	admin.HandleFunc("PUT /api/v1/admin/payments/order", api.reorderAdminPayments)
	admin.HandleFunc("PUT /api/v1/admin/payments/{paymentID}", api.updateAdminPayment)
	admin.HandleFunc("PATCH /api/v1/admin/payments/{paymentID}/enabled", api.setAdminPaymentEnabled)
	admin.HandleFunc("DELETE /api/v1/admin/payments/{paymentID}", api.deleteAdminPayment)
	admin.HandleFunc("GET /api/v1/admin/gift-card/templates", api.listGiftCardTemplates)
	admin.HandleFunc("POST /api/v1/admin/gift-card/templates", api.createGiftCardTemplate)
	admin.HandleFunc("PUT /api/v1/admin/gift-card/templates/{templateID}", api.updateGiftCardTemplate)
	admin.HandleFunc("DELETE /api/v1/admin/gift-card/templates/{templateID}", api.deleteGiftCardTemplate)
	admin.HandleFunc("POST /api/v1/admin/gift-card/codes/generate", api.generateGiftCardCodes)
	admin.HandleFunc("GET /api/v1/admin/gift-card/codes", api.listGiftCardCodes)
	admin.HandleFunc("GET /api/v1/admin/gift-card/codes/export", api.exportGiftCardCodes)
	admin.HandleFunc("PATCH /api/v1/admin/gift-card/codes/{codeID}", api.updateGiftCardCode)
	admin.HandleFunc("POST /api/v1/admin/gift-card/codes/{codeID}/toggle", api.toggleGiftCardCode)
	admin.HandleFunc("DELETE /api/v1/admin/gift-card/codes/{codeID}", api.deleteGiftCardCode)
	admin.HandleFunc("GET /api/v1/admin/gift-card/usages", api.listGiftCardUsages)
	admin.HandleFunc("GET /api/v1/admin/gift-card/statistics", api.giftCardStatistics)
	admin.HandleFunc("GET /api/v1/admin/gift-card/types", api.giftCardTypes)
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
	registerKnowledgeAttachmentRoutes(admin, "/api/v1/admin/knowledge-attachments", api)
	admin.HandleFunc("GET /api/v1/admin/client-catalog", api.listAdminClientCatalog)
	admin.HandleFunc("PUT /api/v1/admin/client-catalog", api.saveClientCatalog)
	admin.HandleFunc("GET /api/v1/admin/tickets", api.listAdminTickets)
	admin.HandleFunc("GET /api/v1/admin/ticket-settings", api.getTicketSettings)
	admin.HandleFunc("PUT /api/v1/admin/ticket-settings", api.updateTicketSettings)
	admin.HandleFunc("GET /api/v1/admin/mail-settings", api.getMailSettings)
	admin.HandleFunc("PUT /api/v1/admin/mail-settings", api.updateMailSettings)
	admin.HandleFunc("POST /api/v1/admin/mail-settings/test", api.testMailSettings)
	admin.HandleFunc("GET /api/v1/admin/mail-templates", api.listMailTemplates)
	admin.HandleFunc("GET /api/v1/admin/mail-templates/{name}", api.getMailTemplate)
	admin.HandleFunc("PUT /api/v1/admin/mail-templates/{name}", api.updateMailTemplate)
	admin.HandleFunc("POST /api/v1/admin/mail-templates/{name}/reset", api.resetMailTemplate)
	admin.HandleFunc("POST /api/v1/admin/mail-templates/{name}/preview", api.previewMailTemplate)
	admin.HandleFunc("POST /api/v1/admin/mail-templates/{name}/test", api.testMailTemplate)
	admin.HandleFunc("GET /api/v1/admin/site-settings", api.getSiteSettings)
	admin.HandleFunc("PUT /api/v1/admin/site-settings", api.updateSiteSettings)
	admin.HandleFunc("GET /api/v1/admin/telegram-settings", api.getTelegramSettings)
	admin.HandleFunc("PUT /api/v1/admin/telegram-settings", api.updateTelegramSettings)
	admin.HandleFunc("POST /api/v1/admin/telegram-settings/webhook", api.provisionTelegramWebhook)
	admin.HandleFunc("GET /api/v1/admin/commission-settings", api.getCommissionSettings)
	admin.HandleFunc("PUT /api/v1/admin/commission-settings", api.updateCommissionSettings)
	admin.HandleFunc("GET /api/v1/admin/node-agent-settings", api.getNodeAgentSettings)
	admin.HandleFunc("PUT /api/v1/admin/node-agent-settings", api.updateNodeAgentSettings)
	admin.HandleFunc("GET /api/v1/admin/subscription-settings", api.getSubscriptionSettings)
	admin.HandleFunc("PUT /api/v1/admin/subscription-settings", api.updateSubscriptionSettings)
	admin.HandleFunc("GET /api/v1/admin/subscription-policy-settings", api.getSubscriptionPolicySettings)
	admin.HandleFunc("PUT /api/v1/admin/subscription-policy-settings", api.updateSubscriptionPolicySettings)
	admin.HandleFunc("GET /api/v1/admin/tickets/{ticketID}", api.getAdminTicket)
	admin.HandleFunc("POST /api/v1/admin/tickets/{ticketID}/messages", api.replyAdminTicket)
	admin.HandleFunc("POST /api/v1/admin/tickets/{ticketID}/close", api.closeAdminTicket)
	admin.HandleFunc("GET /api/v1/admin/users", api.listAdminUsers)
	admin.HandleFunc("POST /api/v1/admin/users", api.createAdminUser)
	admin.HandleFunc("POST /api/v1/admin/users/generate", api.generateAdminUsers)
	admin.HandleFunc("POST /api/v1/admin/users/query", api.queryAdminUsers)
	admin.HandleFunc("POST /api/v1/admin/users/bulk/mail", api.createAdminUserBulkMail)
	admin.HandleFunc("POST /api/v1/admin/users/bulk/csv", api.createAdminUserBulkCSV)
	admin.HandleFunc("POST /api/v1/admin/users/bulk/ban", api.banAdminUsers)
	admin.HandleFunc("GET /api/v1/admin/user-bulk-jobs", api.listAdminUserBulkJobs)
	admin.HandleFunc("GET /api/v1/admin/user-bulk-jobs/{jobID}", api.getAdminUserBulkJob)
	admin.HandleFunc("POST /api/v1/admin/user-bulk-jobs/{jobID}/cancel", api.cancelAdminUserBulkJob)
	admin.HandleFunc("GET /api/v1/admin/user-bulk-jobs/{jobID}/download", api.downloadAdminUserBulkCSV)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}", api.getAdminUser)
	admin.HandleFunc("PATCH /api/v1/admin/users/{userID}", api.updateAdminUser)
	admin.HandleFunc("PUT /api/v1/admin/users/{userID}/password", api.resetAdminUserPassword)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}/subscription-url", api.getAdminUserSubscriptionURL)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}/orders", api.listAdminUserOrders)
	admin.HandleFunc("POST /api/v1/admin/users/{userID}/orders", api.assignAdminUserOrder)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}/invitations", api.listAdminUserInvitations)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}/traffic", api.listAdminUserTraffic)
	admin.HandleFunc("GET /api/v1/admin/users/{userID}/traffic-resets", api.listAdminUserTrafficResets)
	admin.HandleFunc("POST /api/v1/admin/users/{userID}/traffic-reset", api.resetAdminUserTraffic)
	admin.HandleFunc("GET /api/v1/admin/nodes/{nodeID}/activation-schedule", api.getActivationSchedule)
	admin.HandleFunc("PUT /api/v1/admin/nodes/{nodeID}/activation-schedule", api.saveActivationSchedule)
	admin.HandleFunc("DELETE /api/v1/admin/nodes/{nodeID}/activation-schedule", api.deleteActivationSchedule)
	admin.HandleFunc("GET /api/v1/admin/system/status", api.getSystemStatus)
	admin.HandleFunc("GET /api/v1/admin/system/audit", api.listAdminAudit)
	admin.HandleFunc("GET /api/v1/admin/system/mail-failures", api.listTicketMailFailures)
	root.Handle("/api/v1/admin/", api.requireSession(api.requireAdmin(api.auditAdminMutations(api.requireCSRF(api.recoverPanic(admin))))))

	return api.securityHeaders(api.recoverPanic(root))
}

func validLegacyAdminPath(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
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
		statusCode := recorder.statusCode()
		if r.Method == http.MethodPut && r.URL.Path == "/api/v1/admin/node-agent-settings" && statusCode >= 200 && statusCode < 300 {
			return
		}
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
		s.recordAdminAudit(r.Context(), session, r.Method, route, statusCode)
	})
}

func (s *server) auditLegacyAdminOrderMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := ""
		switch {
		case strings.HasSuffix(r.URL.Path, "/order/remark/update"):
			action = "remark/update"
		case strings.HasSuffix(r.URL.Path, "/order/entitlement/update"):
			action = "entitlement/update"
		case strings.HasSuffix(r.URL.Path, "/order/hwid/update"):
			action = "hwid/update"
		case strings.HasSuffix(r.URL.Path, "/order/hwid/device/delete"):
			action = "hwid/device/delete"
		case strings.HasSuffix(r.URL.Path, "/order/settlement/settle"):
			action = "settlement/settle"
		case strings.HasSuffix(r.URL.Path, "/order/assign"):
			action = "assign"
		case strings.HasSuffix(r.URL.Path, "/order/paid"):
			action = "paid"
		case strings.HasSuffix(r.URL.Path, "/order/update"):
			action = "update"
		case strings.HasSuffix(r.URL.Path, "/order/cancel"):
			action = "cancel"
		}
		if r.Method != http.MethodPost || action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/order/"+action, recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminUserMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := ""
		for _, candidate := range []string{"update", "generate", "sendMail", "dumpCSV", "ban"} {
			if strings.HasSuffix(r.URL.Path, "/user/"+candidate) {
				action = candidate
				break
			}
		}
		if r.Method != http.MethodPost || action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/user/"+action, recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminTrafficResetMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/traffic-reset/reset-user") {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/traffic-reset/reset-user", recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminCouponMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := ""
		for _, candidate := range []string{"generate", "show", "update", "drop"} {
			if strings.HasSuffix(r.URL.Path, "/coupon/"+candidate) {
				action = candidate
				break
			}
		}
		if r.Method != http.MethodPost || action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/coupon/"+action, recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminPaymentMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := ""
		for _, candidate := range []string{"save", "drop", "show", "sort"} {
			if strings.HasSuffix(r.URL.Path, "/payment/"+candidate) {
				action = candidate
				break
			}
		}
		if r.Method != http.MethodPost || action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/payment/"+action, recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminGiftCardMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutation := r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/templates") &&
			!strings.HasSuffix(r.URL.Path, "/codes") && !strings.HasSuffix(r.URL.Path, "/usages") &&
			!strings.HasSuffix(r.URL.Path, "/statistics")
		if !mutation {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		action := r.URL.Path
		if index := strings.LastIndex(action, "/gift-card/"); index >= 0 {
			action = action[index+len("/gift-card/"):]
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/gift-card/"+action, recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminConfigMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/config/save") {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if !ok {
			return
		}
		s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/config/save", recorder.statusCode())
	})
}

func (s *server) auditLegacyAdminMailTemplateMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		action := ""
		for _, candidate := range []string{"save", "reset", "test"} {
			if strings.HasSuffix(r.URL.Path, "/"+candidate) {
				action = candidate
				break
			}
		}
		if action == "" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &responseStatusRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		session, ok := sessionFromContext(r.Context())
		if ok {
			s.recordAdminAudit(r.Context(), session, r.Method, "/api/v2/{secure_admin}/mail/template/"+action, recorder.statusCode())
		}
	})
}

func (s *server) recordAdminAudit(parent context.Context, session store.SessionUser, method, route string, statusCode int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
	defer cancel()
	if err := s.store.RecordAdminAudit(ctx, store.AdminAuditInput{
		AdministratorID: session.UserID, AdministratorEmail: session.Email,
		Method: method, Route: route, StatusCode: statusCode,
	}, s.now()); err != nil {
		s.logger.Warn("record administrator audit", "administrator_id", session.UserID, "method", method, "route", route, "error", err)
	}
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

func (s *server) requireDistributor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok || !session.IsDistributor {
			writeAPIError(w, http.StatusForbidden, "distributor_required", "当前账号不是可用的分销商账号", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) requireNonDistributor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessionFromContext(r.Context())
		if !ok || session.IsDistributor {
			writeAPIError(w, http.StatusForbidden, "distributor_route_forbidden", "分销商账号不能访问普通订阅功能", nil)
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
	return decodeJSONLimit(w, r, target, maxJSONBody)
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
	case errors.Is(err, store.ErrRevisionConflict):
		writeAPIError(w, http.StatusConflict, "revision_conflict", "资源已被其他管理员修改，请刷新后重试", nil)
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
