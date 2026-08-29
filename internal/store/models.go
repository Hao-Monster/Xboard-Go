package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound                               = errors.New("not found")
	ErrConflict                               = errors.New("conflict")
	ErrAttachmentQuotaExceeded                = errors.New("attachment quota exceeded")
	ErrRevisionConflict                       = fmt.Errorf("%w: revision conflict", ErrConflict)
	ErrEmailInUse                             = fmt.Errorf("%w: email already in use", ErrConflict)
	ErrAdminInviteUserNotFound                = errors.New("administrator invite user not found")
	ErrAdminUserPlanNotFound                  = errors.New("administrator user plan not found")
	ErrRegistrationClosed                     = errors.New("registration is closed")
	ErrEmailDomainNotAllowed                  = errors.New("email domain is not allowed")
	ErrGmailAliasNotAllowed                   = errors.New("Gmail alias is not allowed")
	ErrRegistrationIPLimited                  = errors.New("registration IP limit reached")
	ErrPasswordResetInvalid                   = errors.New("password reset code is invalid")
	ErrPasswordResetLocked                    = errors.New("password reset confirmation is locked")
	ErrPasswordResetLimited                   = errors.New("password reset request is limited")
	ErrMailUnavailable                        = errors.New("mail service is unavailable")
	ErrRegistrationEmailVerificationInvalid   = errors.New("registration email code is invalid")
	ErrRegistrationEmailVerificationLocked    = errors.New("registration email verification is locked")
	ErrRegistrationEmailVerificationLimited   = errors.New("registration email verification request is limited")
	ErrRegistrationEmailVerificationDisabled  = errors.New("registration email verification is disabled")
	ErrRegistrationEmailVerificationNeedsMail = errors.New("registration email verification requires mail")
	ErrInvitationCodeRequired                 = errors.New("invitation code is required")
	ErrInvitationCodeInvalid                  = errors.New("invitation code is invalid")
	ErrInvitationCodeLimit                    = errors.New("invitation code generation limit reached")
	ErrInvitationCodeCollision                = fmt.Errorf("%w: invitation code collision", ErrConflict)
	ErrInsufficientCommission                 = fmt.Errorf("%w: insufficient commission balance", ErrConflict)
	ErrLoginLinkInvalid                       = errors.New("login link is invalid")
	ErrMailLoginLimited                       = errors.New("mail login request is limited")
	ErrMailLoginDisabled                      = errors.New("mail login is disabled")
	ErrMailLoginNeedsMail                     = errors.New("mail login requires mail")
	ErrOpenTicketExists                       = fmt.Errorf("%w: an open ticket already exists", ErrConflict)
	ErrTicketClosed                           = fmt.Errorf("%w: ticket is closed", ErrConflict)
	ErrTicketReplyPending                     = fmt.Errorf("%w: ticket reply is pending administrator response", ErrConflict)
	ErrTicketMessageLimit                     = fmt.Errorf("%w: ticket message limit reached", ErrConflict)
	ErrInvalidInput                           = errors.New("invalid input")
	ErrInvalidEnrollment                      = errors.New("invalid or expired enrollment")
	ErrInvalidCredential                      = errors.New("invalid machine credential")
	ErrNodeNotLinked                          = errors.New("node is not linked to a machine")
	ErrRuntimeNotConfigured                   = errors.New("node runtime is not configured")
	ErrActiveOrderExists                      = fmt.Errorf("%w: an unpaid or processing order already exists", ErrConflict)
	ErrOrderState                             = fmt.Errorf("%w: order state does not allow this operation", ErrConflict)
	ErrPlanUnavailable                        = fmt.Errorf("%w: subscription plan is unavailable", ErrConflict)
	ErrTrafficResetUnavailable                = fmt.Errorf("%w: user traffic reset is unavailable", ErrConflict)
	ErrCouponInvalid                          = errors.New("invalid coupon")
	ErrCouponNotStarted                       = errors.New("coupon has not started")
	ErrCouponExpired                          = errors.New("coupon has expired")
	ErrCouponExhausted                        = errors.New("coupon is exhausted")
	ErrCouponPlanRestricted                   = errors.New("coupon is not valid for this plan")
	ErrCouponPeriodRestricted                 = errors.New("coupon is not valid for this period")
	ErrCouponUserLimit                        = errors.New("coupon user limit reached")
	ErrCouponReferenced                       = fmt.Errorf("%w: coupon is referenced by an order", ErrConflict)
	ErrPaymentReferenced                      = fmt.Errorf("%w: payment is referenced by an order or receipt", ErrConflict)
	ErrPaymentConfigInUse                     = fmt.Errorf("%w: payment provider configuration is used by an active checkout", ErrPaymentReferenced)
	ErrPaymentUnavailable                     = fmt.Errorf("%w: payment method is unavailable", ErrConflict)
	ErrPaymentInProgress                      = fmt.Errorf("%w: payment checkout is already in progress", ErrConflict)
	ErrPaymentMismatch                        = fmt.Errorf("%w: payment callback does not match the order", ErrConflict)
	ErrGiftCardUnavailable                    = errors.New("gift card is unavailable")
	ErrGiftCardExpired                        = errors.New("gift card has expired")
	ErrGiftCardExhausted                      = errors.New("gift card is exhausted")
	ErrGiftCardUserLimit                      = errors.New("gift card user limit reached")
	ErrGiftCardCooldown                       = errors.New("gift card cooldown is active")
	ErrGiftCardCondition                      = errors.New("gift card conditions are not satisfied")
	ErrGiftCardActivePlan                     = errors.New("gift card plan reward requires no active plan")
	ErrGiftCardReferenced                     = fmt.Errorf("%w: gift card is referenced by usage history", ErrConflict)
	ErrDistributorUnavailable                 = errors.New("distributor account is unavailable")
	ErrDistributorSubscriptionClosed          = fmt.Errorf("%w: distributor subscription is closed", ErrConflict)
	ErrDistributorRenewalMismatch             = fmt.Errorf("%w: distributor renewal idempotency key was reused", ErrConflict)
	ErrDistributorRenewalUnavailable          = fmt.Errorf("%w: distributor subscription cannot be renewed", ErrConflict)
	ErrDistributorHWIDLimit                   = fmt.Errorf("%w: distributor subscription device limit reached", ErrConflict)
	ErrDistributorClaimConsumed               = fmt.Errorf("%w: distributor claim has already been consumed", ErrConflict)
	ErrAdminUserBulkLimit                     = fmt.Errorf("%w: administrator user bulk target limit exceeded", ErrInvalidInput)
	ErrAdminUserBulkExpired                   = errors.New("administrator user bulk output expired")
)

const (
	CredentialKindCookieSession = "cookie_session"
	CredentialKindAccessToken   = "access_token"
)

type User struct {
	ID                int64
	Email             string
	PasswordHash      string
	IsAdmin           bool
	IsStaff           bool
	IsDistributor     bool
	DistributorName   *string
	Banned            bool
	AccountKind       string
	SubscriptionToken string
}

type TicketStatus int

const (
	TicketStatusOpen TicketStatus = iota
	TicketStatusClosed
)

type TicketReplyStatus int

const (
	TicketReplyWaiting TicketReplyStatus = iota
	TicketReplyAnswered
)

type TicketLevel int

const (
	TicketLevelLow TicketLevel = iota
	TicketLevelMedium
	TicketLevelHigh
)

type TicketMessage struct {
	ID        int64     `json:"id"`
	TicketID  int64     `json:"ticket_id"`
	UserID    int64     `json:"-"`
	IsMe      bool      `json:"is_me"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Ticket struct {
	ID              int64             `json:"id"`
	UserID          int64             `json:"user_id"`
	UserEmail       string            `json:"user_email,omitempty"`
	Subject         string            `json:"subject"`
	Level           TicketLevel       `json:"level"`
	Status          TicketStatus      `json:"status"`
	ReplyStatus     TicketReplyStatus `json:"reply_status"`
	LastReplyUserID int64             `json:"-"`
	Messages        []TicketMessage   `json:"messages,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type SaveTicketInput struct {
	Subject string
	Level   TicketLevel
	Message string
}

type TicketFilter struct {
	Page        int
	PageSize    int
	Status      *TicketStatus
	ReplyStatus *TicketReplyStatus
	Level       *TicketLevel
	Query       string
}

type TicketPage struct {
	Items    []Ticket `json:"items"`
	Total    int64    `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

type TicketSettings struct {
	Revision            int64     `json:"revision"`
	AppName             string    `json:"app_name"`
	AppURL              string    `json:"app_url"`
	TicketMustWaitReply bool      `json:"ticket_must_wait_reply"`
	SMTPEnabled         bool      `json:"smtp_enabled"`
	SMTPHost            string    `json:"smtp_host"`
	SMTPPort            int       `json:"smtp_port"`
	SMTPUsername        string    `json:"smtp_username"`
	SMTPPasswordSet     bool      `json:"smtp_password_set"`
	SMTPPasswordCipher  []byte    `json:"-"`
	SMTPEncryption      string    `json:"smtp_encryption"`
	SMTPFromAddress     string    `json:"smtp_from_address"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SaveTicketSettingsInput struct {
	AppName             string
	AppURL              string
	TicketMustWaitReply bool
	SMTPEnabled         bool
	SMTPHost            string
	SMTPPort            int
	SMTPUsername        string
	ReplaceSMTPPassword bool
	SMTPPasswordCipher  []byte
	SMTPEncryption      string
	SMTPFromAddress     string
}

type SiteSettings struct {
	Revision                    int64     `json:"revision"`
	AppName                     string    `json:"app_name"`
	AppDescription              string    `json:"app_description"`
	AppURL                      string    `json:"app_url"`
	TOSURL                      string    `json:"tos_url"`
	Logo                        string    `json:"logo"`
	StopRegister                bool      `json:"stop_register"`
	EmailVerificationEnabled    bool      `json:"email_verify"`
	EmailWhitelistEnabled       bool      `json:"email_whitelist_enable"`
	EmailWhitelistSuffixes      []string  `json:"email_whitelist_suffix"`
	GmailAliasLimitEnabled      bool      `json:"email_gmail_limit_enable"`
	RegistrationIPLimitEnabled  bool      `json:"register_limit_by_ip_enable"`
	RegistrationIPLimitCount    int       `json:"register_limit_count"`
	RegistrationIPLimitMinutes  int       `json:"register_limit_expire"`
	PasswordLimitEnabled        bool      `json:"password_limit_enable"`
	PasswordLimitCount          int       `json:"password_limit_count"`
	PasswordLimitMinutes        int       `json:"password_limit_expire"`
	InvitationForceEnabled      bool      `json:"invite_force"`
	InvitationCodeLimit         int       `json:"invite_gen_limit"`
	InvitationNeverExpire       bool      `json:"invite_never_expire"`
	MailLoginEnabled            bool      `json:"login_with_mail_link_enable"`
	TrialPlanID                 int64     `json:"try_out_plan_id"`
	TrialHours                  int       `json:"try_out_hour"`
	TrafficResetMethod          int       `json:"traffic_reset_method"`
	CouponEnabled               bool      `json:"coupon_enabled"`
	CaptchaEnabled              bool      `json:"captcha_enable"`
	CaptchaType                 string    `json:"captcha_type"`
	RecaptchaSiteKey            string    `json:"recaptcha_site_key"`
	RecaptchaSecretConfigured   bool      `json:"recaptcha_secret_configured"`
	RecaptchaV3SiteKey          string    `json:"recaptcha_v3_site_key"`
	RecaptchaV3ScoreThreshold   float64   `json:"recaptcha_v3_score_threshold"`
	RecaptchaV3SecretConfigured bool      `json:"recaptcha_v3_secret_configured"`
	TurnstileSiteKey            string    `json:"turnstile_site_key"`
	TurnstileSecretConfigured   bool      `json:"turnstile_secret_configured"`
	UpdatedAt                   time.Time `json:"updated_at"`
}

type SaveSiteSettingsInput struct {
	AppName                    string
	AppDescription             string
	AppURL                     string
	TOSURL                     string
	Logo                       string
	StopRegister               bool
	EmailVerificationEnabled   bool
	EmailWhitelistEnabled      bool
	EmailWhitelistSuffixes     []string
	GmailAliasLimitEnabled     bool
	RegistrationIPLimitEnabled bool
	RegistrationIPLimitCount   int
	RegistrationIPLimitMinutes int
	PasswordLimitEnabled       bool
	PasswordLimitCount         int
	PasswordLimitMinutes       int
	InvitationForceEnabled     bool
	InvitationCodeLimit        int
	InvitationNeverExpire      bool
	MailLoginEnabled           bool
	TrialPlanID                *int64
	TrialHours                 *int
	TrafficResetMethod         *int
	CouponEnabled              *bool
	CaptchaEnabled             bool
	CaptchaType                string
	RecaptchaSiteKey           string
	ReplaceRecaptchaSecret     bool
	RecaptchaSecretCipher      []byte
	RecaptchaV3SiteKey         string
	RecaptchaV3ScoreThreshold  float64
	ReplaceRecaptchaV3Secret   bool
	RecaptchaV3SecretCipher    []byte
	TurnstileSiteKey           string
	ReplaceTurnstileSecret     bool
	TurnstileSecretCipher      []byte
}

type CaptchaSecretCiphers struct {
	Recaptcha   []byte
	RecaptchaV3 []byte
	Turnstile   []byte
}

type LoginFailureStatus struct {
	Enabled  bool
	Failures int
	Maximum  int
	Window   time.Duration
	Limited  bool
	ResetAt  *time.Time
}

type CreateInvitationCodeInput struct {
	CodeDigest []byte
	CodeCipher []byte
}

type InvitationCode struct {
	ID         int64     `json:"id"`
	OwnerID    int64     `json:"-"`
	CodeDigest []byte    `json:"-"`
	CodeCipher []byte    `json:"-"`
	PV         int64     `json:"pv"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type InvitationSummary struct {
	Codes                         []InvitationCode
	InvitedCount                  int64
	ValidCommission               int64
	PendingCommission             int64
	CommissionRate                int
	CommissionDistributionEnabled bool
	CommissionDistributionRates   []int
	AvailableCommission           int64
}

type CommissionSettings struct {
	Revision            int64     `json:"revision"`
	InviteCommission    int       `json:"invite_commission"`
	FirstTimeEnabled    bool      `json:"commission_first_time_enable"`
	AutoCheckEnabled    bool      `json:"commission_auto_check_enable"`
	WithdrawClosed      bool      `json:"withdraw_close_enable"`
	DistributionEnabled bool      `json:"commission_distribution_enable"`
	DistributionL1      int       `json:"commission_distribution_l1"`
	DistributionL2      int       `json:"commission_distribution_l2"`
	DistributionL3      int       `json:"commission_distribution_l3"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type SaveCommissionSettingsInput struct {
	InviteCommission    int
	FirstTimeEnabled    bool
	AutoCheckEnabled    bool
	WithdrawClosed      bool
	DistributionEnabled bool
	DistributionL1      int
	DistributionL2      int
	DistributionL3      int
}

type CommissionLog struct {
	ID           int64     `json:"id"`
	OrderID      int64     `json:"-"`
	InviteUserID int64     `json:"-"`
	UserID       int64     `json:"-"`
	TradeNo      string    `json:"trade_no"`
	OrderAmount  int64     `json:"order_amount"`
	GetAmount    int64     `json:"get_amount"`
	CreatedAt    time.Time `json:"created_at"`
}

type CommissionLogPage struct {
	Items    []CommissionLog `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

type CommissionTransferResult struct {
	CommissionBalance int64 `json:"commission_balance"`
	Balance           int64 `json:"balance"`
}

type CommissionProcessingResult struct {
	Checked   int64 `json:"checked"`
	Paid      int64 `json:"paid"`
	Remaining int64 `json:"remaining"`
}

type TicketMailJob struct {
	ID                 int64
	Attempt            int
	Recipient          string
	Subject            string
	Message            string
	AppName            string
	AppURL             string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPasswordCipher []byte
	SMTPEncryption     string
	SMTPFromAddress    string
}

type PasswordResetRequestInput struct {
	Email       string
	EmailDigest []byte
	CodeDigest  []byte
	CodeCipher  []byte
}

type PasswordResetChallenge struct {
	UserID       int64
	PasswordHash string
}

type PasswordResetMailJob struct {
	ID                 int64
	Attempt            int
	EmailDigest        []byte
	Recipient          string
	CodeCipher         []byte
	AppName            string
	AppURL             string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPasswordCipher []byte
	SMTPEncryption     string
	SMTPFromAddress    string
}

type RegistrationEmailVerificationRequestInput struct {
	Email       string
	SourceIP    string
	EmailDigest []byte
	CodeDigest  []byte
	CodeCipher  []byte
}

type RegistrationEmailVerificationMailJob struct {
	ID                 int64
	Attempt            int
	EmailDigest        []byte
	Recipient          string
	CodeCipher         []byte
	AppName            string
	AppURL             string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPasswordCipher []byte
	SMTPEncryption     string
	SMTPFromAddress    string
}

type LoginLinkExchangeInput struct {
	TokenDigest          []byte
	AlternateTokenDigest []byte
	SessionTokenHash     string
	CSRFHash             string
	SessionExpiresAt     time.Time
	AccessTokenHash      string
	AccessTokenName      string
}

type LoginLinkExchange struct {
	User     User
	Redirect string
}

type MailLoginLinkRequestInput struct {
	Email          string
	ExpectedUserID int64
	EmailDigest    []byte
	TokenDigest    []byte
	TokenCipher    []byte
	Redirect       string
	LinkBaseURL    string
}

type LoginLinkMailJob struct {
	ID                 int64
	Attempt            int
	UserID             int64
	TokenDigest        []byte
	Recipient          string
	TokenCipher        []byte
	Redirect           string
	AppName            string
	AppURL             string
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPasswordCipher []byte
	SMTPEncryption     string
	SMTPFromAddress    string
}

type MailLoginLimitError struct {
	RetryAfterSeconds int64
}

func (e *MailLoginLimitError) Error() string { return ErrMailLoginLimited.Error() }
func (e *MailLoginLimitError) Unwrap() error { return ErrMailLoginLimited }

type RegistrationEmailVerificationLimitError struct {
	RetryAfterSeconds int64
}

func (e *RegistrationEmailVerificationLimitError) Error() string {
	return ErrRegistrationEmailVerificationLimited.Error()
}
func (e *RegistrationEmailVerificationLimitError) Unwrap() error {
	return ErrRegistrationEmailVerificationLimited
}

type RegistrationEmailVerificationLockedError struct {
	RetryAfterSeconds int64
}

func (e *RegistrationEmailVerificationLockedError) Error() string {
	return ErrRegistrationEmailVerificationLocked.Error()
}
func (e *RegistrationEmailVerificationLockedError) Unwrap() error {
	return ErrRegistrationEmailVerificationLocked
}

type PasswordResetLimitError struct {
	RetryAfterSeconds int64
}

func (e *PasswordResetLimitError) Error() string { return ErrPasswordResetLimited.Error() }
func (e *PasswordResetLimitError) Unwrap() error { return ErrPasswordResetLimited }

type PasswordResetLockedError struct {
	RetryAfterSeconds int64
}

func (e *PasswordResetLockedError) Error() string { return ErrPasswordResetLocked.Error() }
func (e *PasswordResetLockedError) Unwrap() error { return ErrPasswordResetLocked }

type SystemQueueStats struct {
	Pending         int64      `json:"pending"`
	Claimed         int64      `json:"claimed"`
	Sent            int64      `json:"sent"`
	Failed          int64      `json:"failed"`
	OldestPendingAt *time.Time `json:"oldest_pending_at"`
}

type TicketMailFailure struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	Recipient     string    `json:"recipient"`
	TicketSubject string    `json:"ticket_subject"`
	AttemptCount  int       `json:"attempt_count"`
	LastError     string    `json:"last_error"`
	CreatedAt     time.Time `json:"created_at"`
	FailedAt      time.Time `json:"failed_at"`
}

type TicketMailFailurePage struct {
	Items    []TicketMailFailure `json:"items"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

type AdminAuditLog struct {
	ID                 int64     `json:"id"`
	AdministratorID    *int64    `json:"administrator_id"`
	AdministratorEmail string    `json:"administrator_email"`
	Method             string    `json:"method"`
	Route              string    `json:"route"`
	StatusCode         int       `json:"status_code"`
	CreatedAt          time.Time `json:"created_at"`
}

type AdminAuditInput struct {
	AdministratorID    int64
	AdministratorEmail string
	Method             string
	Route              string
	StatusCode         int
}

type AdminAuditFilter struct {
	Page     int
	PageSize int
	Method   string
	Query    string
}

type AdminAuditPage struct {
	Items    []AdminAuditLog `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

type Knowledge struct {
	ID           int64     `json:"id"`
	Language     string    `json:"language"`
	Category     string    `json:"category"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	SortPosition int       `json:"sort"`
	Visible      bool      `json:"show"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SaveKnowledgeInput struct {
	Language string
	Category string
	Title    string
	Body     string
	Visible  bool
}

type KnowledgeViewer struct {
	SubscriptionToken string
	SubscriptionValid bool
}

var SubscriptionTemplateNames = [...]string{"singbox", "clash", "clashmeta", "stash", "surge", "surfboard"}

type SubscriptionSettings struct {
	Revision     int64             `json:"revision"`
	Path         string            `json:"path"`
	ShowInfo     bool              `json:"show_info"`
	ShowProtocol bool              `json:"show_protocol"`
	Templates    map[string]string `json:"templates"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type SubscriptionRenderConfig struct {
	Path         string
	ShowInfo     bool
	ShowProtocol bool
	AppName      string
	AppURL       string
	Templates    map[string]string
}

type SaveSubscriptionSettingsInput struct {
	Path         string
	ShowInfo     bool
	ShowProtocol bool
	Templates    map[string]string
}

type SubscriptionAccount struct {
	ID                int64
	Email             string
	UUID              string
	GroupID           *int64
	PlanID            *int64
	TransferEnable    int64
	TrafficUpload     int64
	TrafficDownload   int64
	ExpiredAt         *time.Time
	NextResetAt       *time.Time
	SpeedLimit        int
	DeviceLimit       int
	Banned            bool
	SubscriptionToken string
	CreatedAt         time.Time
}

type SubscriptionSecurityMutation struct {
	PreviousUUID string
	GroupID      *int64
}

func (account SubscriptionAccount) AvailableAt(now time.Time) bool {
	return !account.Banned && account.TransferEnable != 0 && (account.ExpiredAt == nil || account.ExpiredAt.After(now))
}

const (
	AccountKindHuman                = "human"
	AccountKindInternalSubscription = "internal_subscription"
)

type AdminUser struct {
	ID                int64      `json:"id"`
	Email             string     `json:"email"`
	IsAdmin           bool       `json:"is_admin"`
	IsStaff           bool       `json:"is_staff"`
	IsDistributor     bool       `json:"is_distributor"`
	DistributorName   *string    `json:"distributor_name"`
	Banned            bool       `json:"banned"`
	GroupID           *int64     `json:"group_id"`
	GroupName         *string    `json:"group_name"`
	PlanID            *int64     `json:"plan_id"`
	PlanName          *string    `json:"plan_name"`
	InviteUserID      *int64     `json:"invite_user_id"`
	InviteUserEmail   *string    `json:"invite_user_email"`
	TransferEnable    int64      `json:"transfer_enable"`
	TrafficUpload     int64      `json:"traffic_upload"`
	TrafficDownload   int64      `json:"traffic_download"`
	TrafficUsed       int64      `json:"traffic_used"`
	ExpiredAt         *time.Time `json:"expired_at"`
	SpeedLimit        int        `json:"speed_limit"`
	DeviceLimit       int        `json:"device_limit"`
	OnlineCount       int        `json:"online_count"`
	LastOnlineAt      *time.Time `json:"last_online_at"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	Balance           int64      `json:"balance"`
	CommissionType    int        `json:"commission_type"`
	CommissionRate    *int       `json:"commission_rate"`
	CommissionBalance int64      `json:"commission_balance"`
	Discount          *int       `json:"discount"`
	NextResetAt       *time.Time `json:"next_reset_at"`
	LastResetAt       *time.Time `json:"last_reset_at"`
	ResetCount        int        `json:"reset_count"`
	TelegramID        *int64     `json:"telegram_id"`
	RemindExpire      bool       `json:"remind_expire"`
	RemindTraffic     bool       `json:"remind_traffic"`
	Remarks           *string    `json:"remarks"`
	Revision          int64      `json:"revision"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type AdminUserPage struct {
	Items      []AdminUser `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
	Total      int64       `json:"total"`
	Page       int         `json:"page"`
	PageSize   int         `json:"page_size"`
}

const (
	AdminUserBulkKindMail = "mail"
	AdminUserBulkKindCSV  = "csv"
	AdminUserBulkKindBan  = "ban"

	AdminUserBulkScopeSelected = "selected"
	AdminUserBulkScopeFiltered = "filtered"
	AdminUserBulkScopeAll      = "all"

	AdminUserBulkStatusQueued     = "queued"
	AdminUserBulkStatusRunning    = "running"
	AdminUserBulkStatusCancelling = "cancelling"
	AdminUserBulkStatusCancelled  = "cancelled"
	AdminUserBulkStatusSucceeded  = "succeeded"
	AdminUserBulkStatusFailed     = "failed"

	AdminUserBulkTargetPending    = "pending"
	AdminUserBulkTargetProcessing = "processing"
	AdminUserBulkTargetSucceeded  = "succeeded"
	AdminUserBulkTargetFailed     = "failed"
	AdminUserBulkTargetSkipped    = "skipped"
	AdminUserBulkTargetCancelled  = "cancelled"
)

type AdminUserBulkScope struct {
	Scope   string
	UserIDs []int64
	Filter  AdminUserFilter
}

type CreateAdminUserBulkJobInput struct {
	Kind            string
	AdministratorID int64
	Scope           AdminUserBulkScope
	Subject         string
	Content         string
}

type BanAdminUsersInput struct {
	AdministratorID int64
	IdempotencyKey  string
	Scope           AdminUserBulkScope
}

type AdminUserBulkJob struct {
	ID                 string     `json:"id"`
	Kind               string     `json:"kind"`
	Scope              string     `json:"scope"`
	AdministratorID    *int64     `json:"administrator_id"`
	AdministratorEmail string     `json:"administrator_email"`
	Status             string     `json:"status"`
	Subject            string     `json:"subject,omitempty"`
	Content            string     `json:"-"`
	AppName            string     `json:"app_name,omitempty"`
	AppURL             string     `json:"app_url,omitempty"`
	TotalCount         int        `json:"total_count"`
	ProcessedCount     int        `json:"processed_count"`
	SuccessCount       int        `json:"success_count"`
	FailureCount       int        `json:"failure_count"`
	SkippedCount       int        `json:"skipped_count"`
	CancelledCount     int        `json:"cancelled_count"`
	OutputFilename     string     `json:"output_filename,omitempty"`
	OutputRelativePath string     `json:"-"`
	OutputSize         *int64     `json:"output_size,omitempty"`
	OutputSHA256       string     `json:"output_sha256,omitempty"`
	OutputExpiresAt    *time.Time `json:"output_expires_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	CancelledAt        *time.Time `json:"cancelled_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type AdminUserBulkTarget struct {
	JobID             string     `json:"job_id"`
	Sequence          int64      `json:"sequence"`
	UserID            int64      `json:"user_id"`
	Email             string     `json:"email"`
	UUID              string     `json:"-"`
	PlanName          string     `json:"plan_name,omitempty"`
	GroupID           *int64     `json:"-"`
	ExpiredAt         *time.Time `json:"expired_at,omitempty"`
	TransferEnable    int64      `json:"-"`
	TransferUsed      int64      `json:"-"`
	Balance           int64      `json:"-"`
	CommissionBalance int64      `json:"-"`
	SubscriptionToken string     `json:"-"`
	Status            string     `json:"status"`
	AttemptCount      int        `json:"attempt_count"`
	LastError         string     `json:"last_error,omitempty"`
	ProcessedAt       *time.Time `json:"processed_at,omitempty"`
}

type AdminUserBulkMail struct {
	AdminUserBulkTarget
	JobID              string
	Attempt            int
	Subject            string
	Content            string
	AppName            string
	AppURL             string
	CreatedAt          time.Time
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPasswordCipher []byte
	SMTPEncryption     string
	SMTPFromAddress    string
}

type AdminUserBulkJobPage struct {
	Items    []AdminUserBulkJob `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

type AdminUserBulkExpiredOutput struct {
	JobID        string
	RelativePath string
}

type AdminUserFilter struct {
	Limit          int
	Cursor         string
	EmailPrefix    string
	Banned         *bool
	GroupID        *int64
	Page           int
	PageSize       int
	SortBy         AdminUserSort
	SortDescending bool
	Sorts          []AdminUserSortRule
	Rules          []AdminUserFilterRule
}

type AdminUserSortRule struct {
	Field      AdminUserSort
	Descending bool
}

type AdminUserSort string

const (
	AdminUserSortID                AdminUserSort = "id"
	AdminUserSortOnlineCount       AdminUserSort = "online_count"
	AdminUserSortBanned            AdminUserSort = "banned"
	AdminUserSortTrafficUsed       AdminUserSort = "traffic_used"
	AdminUserSortTransferEnable    AdminUserSort = "transfer_enable"
	AdminUserSortExpiredAt         AdminUserSort = "expired_at"
	AdminUserSortBalance           AdminUserSort = "balance"
	AdminUserSortCommissionBalance AdminUserSort = "commission_balance"
	AdminUserSortCreatedAt         AdminUserSort = "created_at"
)

type AdminUserFilterRule struct {
	Field    string
	Operator string
	Values   []string
}

const (
	AdminUserFieldID                = "id"
	AdminUserFieldEmail             = "email"
	AdminUserFieldPlanID            = "plan_id"
	AdminUserFieldGroupID           = "group_id"
	AdminUserFieldTransferEnable    = "transfer_enable"
	AdminUserFieldTrafficUsed       = "traffic_used"
	AdminUserFieldOnlineCount       = "online_count"
	AdminUserFieldExpiredAt         = "expired_at"
	AdminUserFieldUUID              = "uuid"
	AdminUserFieldSubscriptionToken = "subscription_token"
	AdminUserFieldBanned            = "banned"
	AdminUserFieldRemarks           = "remarks"
	AdminUserFieldInviteUserID      = "invite_user_id"
	AdminUserFieldInviteUserEmail   = "invite_user_email"
	AdminUserFieldIsAdmin           = "is_admin"
	AdminUserFieldIsStaff           = "is_staff"
	AdminUserFieldIsDistributor     = "is_distributor"
	AdminUserFieldBalance           = "balance"
	AdminUserFieldCommissionBalance = "commission_balance"
	AdminUserFieldCreatedAt         = "created_at"
)

const (
	AdminUserOperatorEqual          = "eq"
	AdminUserOperatorNotEqual       = "neq"
	AdminUserOperatorContains       = "contains"
	AdminUserOperatorGreater        = "gt"
	AdminUserOperatorGreaterOrEqual = "gte"
	AdminUserOperatorLess           = "lt"
	AdminUserOperatorLessOrEqual    = "lte"
	AdminUserOperatorIn             = "in"
	AdminUserOperatorIsNull         = "is_null"
	AdminUserOperatorNotNull        = "not_null"
)

type CreateAdminUserInput struct {
	Email           string
	PasswordHash    string
	IsAdmin         bool
	IsStaff         bool
	IsDistributor   bool
	DistributorName string
	GroupID         *int64
	PlanID          *int64
	TransferEnable  int64
	ExpiredAt       *time.Time
	SpeedLimit      int
	DeviceLimit     int
	Banned          bool
}

// CreatedAdminUser keeps one-time subscription credentials separate from the
// ordinary administrator directory DTO, which must never expose them.
type CreatedAdminUser struct {
	User              AdminUser
	UUID              string
	SubscriptionToken string
}

type UpdateAdminUserInput struct {
	Revision           int64
	Email              string
	PasswordHash       *string
	IsAdmin            *bool
	IsStaff            *bool
	IsDistributor      *bool
	DistributorName    *string
	GroupID            *int64
	PlanIDSet          bool
	PlanID             *int64
	InviteUserEmailSet bool
	InviteUserEmail    *string
	TransferEnable     int64
	TrafficUpload      *int64
	TrafficDownload    *int64
	ExpiredAt          *time.Time
	SpeedLimit         int
	DeviceLimit        int
	Banned             bool
	Balance            *int64
	CommissionType     *int
	CommissionRateSet  bool
	CommissionRate     *int
	CommissionBalance  *int64
	DiscountSet        bool
	Discount           *int
	TelegramIDSet      bool
	TelegramID         *int64
	RemindExpire       *bool
	RemindTraffic      *bool
	RemarksSet         bool
	Remarks            *string
}

type AdminUserMutation struct {
	OldGroupID         *int64
	NewGroupID         *int64
	UUID               string
	RuntimeChanged     bool
	AccessStateCleared bool
}

type AdminUserTrafficResetInput struct {
	UserID          int64
	AdministratorID int64
	Reason          string
	IdempotencyKey  string
}

type AdminUserTrafficResetResult struct {
	UserID         int64      `json:"user_id"`
	Email          string     `json:"email"`
	UploadBefore   int64      `json:"upload_before"`
	DownloadBefore int64      `json:"download_before"`
	UploadAfter    int64      `json:"upload_after"`
	DownloadAfter  int64      `json:"download_after"`
	ResetCount     int        `json:"reset_count"`
	ResetAt        time.Time  `json:"reset_at"`
	NextResetAt    *time.Time `json:"next_reset_at"`
	Reason         string     `json:"reason"`
	Idempotent     bool       `json:"idempotent"`
	UUID           string     `json:"-"`
	GroupID        *int64     `json:"-"`
	ResetMethod    int        `json:"-"`
}

type AdminUserTrafficResetLog struct {
	ID                 int64      `json:"id"`
	UserID             int64      `json:"user_id"`
	PlanID             *int64     `json:"plan_id"`
	ScheduledFor       *time.Time `json:"scheduled_for"`
	ResetAt            time.Time  `json:"reset_at"`
	UploadBefore       int64      `json:"upload_before"`
	DownloadBefore     int64      `json:"download_before"`
	UploadAfter        int64      `json:"upload_after"`
	DownloadAfter      int64      `json:"download_after"`
	ResetCount         int        `json:"reset_count"`
	TriggerSource      string     `json:"trigger_source"`
	Reason             string     `json:"reason"`
	AdministratorID    *int64     `json:"administrator_id"`
	AdministratorEmail *string    `json:"administrator_email"`
	ResetMethod        int        `json:"-"`
}

type AdminUserTrafficResetPage struct {
	Items    []AdminUserTrafficResetLog `json:"items"`
	Total    int64                      `json:"total"`
	Page     int                        `json:"page"`
	PageSize int                        `json:"page_size"`
}

type AdminUserTrafficStat struct {
	RateMicros int64     `json:"rate_micros"`
	RecordAt   time.Time `json:"record_at"`
	RecordType string    `json:"record_type"`
	Upload     int64     `json:"upload"`
	Download   int64     `json:"download"`
}

type AdminUserTrafficStatPage struct {
	Items    []AdminUserTrafficStat `json:"items"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
}

type SessionUser struct {
	UserID          int64
	Email           string
	IsAdmin         bool
	IsStaff         bool
	IsDistributor   bool
	DistributorName *string
	Banned          bool
	CSRFHash        string
	ExpiresAt       time.Time
	SessionID       int64
	LastUsedAt      *time.Time
	CredentialKind  string
}

type AccountSession struct {
	ID         int64      `json:"id"`
	IsCurrent  bool       `json:"is_current"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
}

type CreateAccessTokenInput struct {
	UserID    int64
	TokenHash string
	Name      string
	ExpiresAt *time.Time
}

type AccountAccessToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"-"`
	Name       string     `json:"name"`
	IsCurrent  bool       `json:"is_current"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

type Machine struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Notes        string          `json:"notes"`
	IsActive     bool            `json:"is_active"`
	LastSeenAt   *time.Time      `json:"last_seen_at"`
	LoadStatus   json.RawMessage `json:"load_status"`
	ServersCount int64           `json:"servers_count"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type CreateMachineInput struct {
	Name     string
	Notes    string
	IsActive bool
}

type UpdateMachineInput = CreateMachineInput

type EnrollmentSecret struct {
	MachineID      int64     `json:"machine_id"`
	Code           string    `json:"token"`
	TokenType      string    `json:"token_type"`
	ExpiresAt      time.Time `json:"expires_at"`
	RevokeExisting bool      `json:"-"`
}

type MachineCredential struct {
	MachineID int64  `json:"machine_id"`
	Token     string `json:"token"`
	Prefix    string `json:"token_prefix"`
}

// NodeAgentSettings contains administrator-owned compatibility settings. The
// legacy server token is deliberately represented only by non-secret metadata.
type NodeAgentSettings struct {
	Revision              int64     `json:"revision"`
	ServerTokenConfigured bool      `json:"server_token_configured"`
	ServerTokenPrefix     string    `json:"server_token_prefix"`
	PullInterval          int       `json:"server_pull_interval"`
	PushInterval          int       `json:"server_push_interval"`
	DeviceLimitMode       int       `json:"device_limit_mode"`
	WebSocketEnabled      bool      `json:"server_ws_enable"`
	WebSocketURL          string    `json:"server_ws_url"`
	UpdatedBy             *int64    `json:"updated_by,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type NodeAgentSettingsDefaults struct {
	PullInterval     int
	PushInterval     int
	DeviceLimitMode  int
	WebSocketEnabled bool
	WebSocketURL     string
}

type UpdateNodeAgentSettingsInput struct {
	Revision         int64
	ServerToken      *string
	PullInterval     int
	PushInterval     int
	DeviceLimitMode  int
	WebSocketEnabled bool
	WebSocketURL     string
	UpdatedBy        *int64
	Audit            *AdminAuditInput
}

type Node struct {
	ID                int64      `json:"id"`
	Revision          int64      `json:"revision"`
	Name              string     `json:"name"`
	Type              string     `json:"type"`
	Host              string     `json:"host"`
	Port              string     `json:"port"`
	Show              bool       `json:"show"`
	Enabled           bool       `json:"enabled"`
	Sort              int        `json:"sort"`
	Rate              float64    `json:"rate"`
	TrafficUpload     int64      `json:"traffic_upload"`
	TrafficDownload   int64      `json:"traffic_download"`
	RuntimeConfigured bool       `json:"runtime_configured"`
	LastCheckAt       *time.Time `json:"last_check_at"`
	LastPushAt        *time.Time `json:"last_push_at"`
	MachineID         *int64     `json:"machine_id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type SubscriptionNode struct {
	ID                int64
	Type              string
	ExternalCode      string
	ParentID          *int64
	Name              string
	Tags              []string
	Host              string
	Port              string
	ServerPort        int
	ProtocolSettings  json.RawMessage
	Show              bool
	Enabled           bool
	Sort              int
	TransferEnable    int64
	TrafficUpload     int64
	TrafficDownload   int64
	ConfiguredRate    float64
	CreatedAt         time.Time
	ParentCreatedAt   *time.Time
	RateTimeEnabled   bool
	RateTimeRanges    json.RawMessage
	CustomOutbounds   json.RawMessage
	CustomRoutes      json.RawMessage
	CertificateConfig json.RawMessage
}

type NodeRuntime struct {
	NodeID     int64
	RateMicros int64
	GroupIDs   []int64
	RouteIDs   []int64
	Routes     []RoutingRule
	Config     json.RawMessage
	UpdatedAt  time.Time
}

type SaveNodeRuntimeInput struct {
	RateMicros int64
	GroupIDs   []int64
	RouteIDs   []int64
	Config     json.RawMessage
}

type ServerGroup struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	UsersCount   int64     `json:"users_count"`
	ServersCount int64     `json:"server_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type PlanPrices map[string]int64

type CouponType int

const (
	CouponTypeFixed CouponType = iota + 1
	CouponTypePercentage
)

type Coupon struct {
	ID               int64      `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Type             CouponType `json:"type"`
	Value            int64      `json:"value"`
	Show             bool       `json:"show"`
	LimitUse         *int       `json:"limit_use"`
	LimitUseWithUser *int       `json:"limit_use_with_user"`
	LimitPlanIDs     []int64    `json:"limit_plan_ids"`
	LimitPeriods     []string   `json:"limit_period"`
	StartedAt        time.Time  `json:"started_at"`
	EndedAt          time.Time  `json:"ended_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type SaveCouponInput struct {
	Code             string
	Name             string
	Type             CouponType
	Value            int64
	Show             bool
	LimitUse         *int
	LimitUseWithUser *int
	LimitPlanIDs     []int64
	LimitPeriods     []string
	StartedAt        time.Time
	EndedAt          time.Time
}

type CouponCheckInput struct {
	UserID int64
	PlanID int64
	Period string
	Code   string
}

type CouponQuote struct {
	Coupon               Coupon `json:"coupon"`
	OriginalAmount       int64  `json:"original_amount"`
	CouponDiscountAmount int64  `json:"coupon_discount_amount"`
	TotalAfterCoupon     int64  `json:"total_after_coupon"`
}

type CouponFilter struct {
	Page     int
	PageSize int
	Query    string
	Type     *CouponType
	Show     *bool
	Sort     string
	Desc     bool
}

type CouponPage struct {
	Items    []Coupon `json:"items"`
	Total    int64    `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

type PaymentProvider string

const (
	PaymentProviderAlipayF2F    PaymentProvider = "AlipayF2F"
	PaymentProviderBTCPay       PaymentProvider = "BTCPay"
	PaymentProviderCoinPayments PaymentProvider = "CoinPayments"
	PaymentProviderCoinbase     PaymentProvider = "Coinbase"
	PaymentProviderEPay         PaymentProvider = "EPay"
	PaymentProviderMGate        PaymentProvider = "MGate"
)

type Payment struct {
	ID                     int64           `json:"id"`
	UUID                   string          `json:"uuid"`
	Provider               PaymentProvider `json:"payment"`
	Name                   string          `json:"name"`
	Icon                   string          `json:"icon,omitempty"`
	ConfigCiphertext       []byte          `json:"-"`
	NotifyDomain           string          `json:"notify_domain,omitempty"`
	HandlingFeeFixed       int64           `json:"handling_fee_fixed"`
	HandlingFeeBasisPoints int64           `json:"handling_fee_basis_points"`
	Enabled                bool            `json:"enable"`
	SortPosition           int             `json:"sort"`
	Revision               int64           `json:"revision"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

type SavePaymentInput struct {
	Provider               PaymentProvider
	Name                   string
	Icon                   string
	ConfigCiphertext       []byte
	NotifyDomain           string
	HandlingFeeFixed       int64
	HandlingFeeBasisPoints int64
	Enabled                bool
}

type PaymentFilter struct {
	Page     int
	PageSize int
	Query    string
	Provider PaymentProvider
}

type PaymentPage struct {
	Items    []Payment `json:"items"`
	Total    int64     `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}

type PaymentCheckoutStatus int

const (
	PaymentCheckoutCreating PaymentCheckoutStatus = iota
	PaymentCheckoutCreated
	PaymentCheckoutFailed
)

type PaymentCheckoutAttempt struct {
	ID             int64                 `json:"id"`
	OrderID        int64                 `json:"order_id"`
	PaymentID      int64                 `json:"payment_id"`
	IdempotencyKey string                `json:"-"`
	ExpectedAmount int64                 `json:"expected_amount"`
	Currency       string                `json:"currency"`
	Status         PaymentCheckoutStatus `json:"status"`
	ExternalID     string                `json:"external_id,omitempty"`
	ResponseType   *int                  `json:"type,omitempty"`
	ResponseData   string                `json:"data,omitempty"`
	ErrorCode      string                `json:"-"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type StartPaymentCheckoutInput struct {
	UserID    int64
	TradeNo   string
	PaymentID int64
}

type PaymentCheckoutStart struct {
	Attempt PaymentCheckoutAttempt
	Payment Payment
	Order   Order
	Cached  bool
}

type CompletePaymentWebhookInput struct {
	PaymentID     int64
	Provider      PaymentProvider
	ExternalID    string
	TradeNo       string
	Amount        int64
	Currency      string
	PayloadSHA256 string
}

type StoredPaymentConfig struct {
	Provider   PaymentProvider
	Ciphertext []byte
}

type OrderStatus int

const (
	OrderStatusPending OrderStatus = iota
	OrderStatusProcessing
	OrderStatusCancelled
	OrderStatusCompleted
	OrderStatusDiscounted
)

type OrderType int

const (
	OrderTypeNew OrderType = iota + 1
	OrderTypeRenewal
	OrderTypeUpgrade
	OrderTypeResetTraffic
)

type Order struct {
	ID                         int64       `json:"id"`
	UserID                     int64       `json:"user_id"`
	PlanID                     int64       `json:"plan_id"`
	PaymentID                  *int64      `json:"payment_id"`
	Period                     string      `json:"period"`
	TradeNo                    string      `json:"trade_no"`
	OriginalAmount             int64       `json:"original_amount"`
	TotalAmount                int64       `json:"total_amount"`
	HandlingAmount             *int64      `json:"handling_amount"`
	BalanceAmount              int64       `json:"balance_amount"`
	SurplusCredit              int64       `json:"surplus_credit"`
	SurplusAmount              int64       `json:"surplus_amount"`
	Type                       OrderType   `json:"type"`
	Status                     OrderStatus `json:"status"`
	SurplusOrderIDs            []int64     `json:"surplus_order_ids"`
	CouponID                   *int64      `json:"coupon_id"`
	CommissionStatus           *int        `json:"commission_status"`
	InviteUserID               *int64      `json:"invite_user_id"`
	ActualCommissionBalance    *int64      `json:"actual_commission_balance"`
	CommissionRate             *int        `json:"commission_rate"`
	CommissionAutoCheck        *bool       `json:"commission_auto_check"`
	CommissionBalance          int64       `json:"commission_balance"`
	DiscountAmount             int64       `json:"discount_amount"`
	PaidAt                     *time.Time  `json:"paid_at"`
	CallbackNo                 string      `json:"callback_no"`
	DistributorOrderID         *int64      `json:"-"`
	EntitlementExpiredAtBefore *time.Time  `json:"entitlement_expired_at_before"`
	EntitlementExpiredAtAfter  *time.Time  `json:"entitlement_expired_at_after"`
	CreatedAt                  time.Time   `json:"created_at"`
	UpdatedAt                  time.Time   `json:"updated_at"`
	Plan                       *Plan       `json:"plan,omitempty"`
}

type CreateOrderInput struct {
	UserID     int64
	PlanID     int64
	Period     string
	CouponCode string
}

type DistributorDeliveryStatus int

const (
	DistributorDeliveryPending DistributorDeliveryStatus = iota
	DistributorDeliveryClaimed
	DistributorDeliveryClosed
)

type DistributorSettlementStatus int

const (
	DistributorSettlementUnsettled DistributorSettlementStatus = iota
	DistributorSettlementSettled
)

type DistributorSubscription struct {
	ID                int64                       `json:"id"`
	OriginalOrderID   int64                       `json:"original_order_id"`
	OriginalTradeNo   string                      `json:"trade_no"`
	DistributorUserID int64                       `json:"distributor_user_id"`
	SubscriberUserID  int64                       `json:"-"`
	CustomerName      *string                     `json:"customer_name"`
	Remark            *string                     `json:"remark"`
	DeliveryStatus    DistributorDeliveryStatus   `json:"delivery_status"`
	SettlementStatus  DistributorSettlementStatus `json:"settlement_status"`
	ConfigIssuedAt    *time.Time                  `json:"config_issued_at"`
	ConnectedAt       *time.Time                  `json:"connected_at"`
	ConnectedNodeID   *int64                      `json:"connected_node_id"`
	ConnectedNodeName *string                     `json:"connected_node_name"`
	ClaimedAt         *time.Time                  `json:"claimed_at"`
	ClosedAt          *time.Time                  `json:"closed_at"`
	HWIDEnabled       bool                        `json:"hwid_enabled"`
	HWIDLimit         int                         `json:"hwid_limit"`
	Revision          int64                       `json:"revision"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
	SubscriptionToken string                      `json:"-"`
	SubscriberUUID    string                      `json:"-"`
	ClaimToken        string                      `json:"-"`
}

type DistributorOrder struct {
	Order                 Order                       `json:"order"`
	PlanName              string                      `json:"plan_name"`
	DistributorEmail      string                      `json:"distributor_email,omitempty"`
	DistributorName       string                      `json:"distributor_name,omitempty"`
	Subscription          DistributorSubscription     `json:"subscription"`
	SettlementStatus      DistributorSettlementStatus `json:"settlement_status"`
	Entitlement           DistributorEntitlement      `json:"subscription_entitlement"`
	BoundDevices          []string                    `json:"bound_devices"`
	IsSubscriptionOrigin  bool                        `json:"is_subscription_origin"`
	CanViewSubscriptionQR bool                        `json:"can_view_subscription_qr"`
	CanRenew              bool                        `json:"can_renew"`
}

type DistributorOrderFilter struct {
	Page               int
	PageSize           int
	DistributorUserID  *int64
	Status             *OrderStatus
	SettlementStatus   *DistributorSettlementStatus
	Search             string
	IncludeTokenSearch bool
}

type DistributorOrderPage struct {
	Items    []DistributorOrder `json:"items"`
	Total    int64              `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

type DistributorOrderExportRow struct {
	TradeNo             string
	Type                OrderType
	SubscriptionTradeNo string
	CustomerName        *string
	DistributorEmail    string
	DistributorName     string
	PlanName            string
	Period              string
	TotalAmount         int64
	SettlementStatus    DistributorSettlementStatus
	Remark              *string
}

type CreateDistributorOrderInput struct {
	DistributorUserID int64
	PlanID            int64
	Period            string
	CustomerName      *string
}

type RenewDistributorOrderInput struct {
	DistributorUserID int64
	TradeNo           string
	Period            string
	IdempotencyKey    string
}

type DistributorEntitlement struct {
	PlanID           int64      `json:"plan_id"`
	PlanName         string     `json:"plan_name"`
	TransferEnable   int64      `json:"transfer_enable"`
	UsedTraffic      int64      `json:"used_traffic"`
	RemainingTraffic int64      `json:"remaining_traffic"`
	ExpiredAt        *time.Time `json:"expired_at"`
	SpeedLimit       int        `json:"speed_limit"`
	DeviceLimit      int        `json:"device_limit"`
}

type UpdateDistributorEntitlementInput struct {
	TransferEnable int64
	ExpiredAt      *time.Time
	SpeedLimit     int
	DeviceLimit    int
}

type DistributorSettlementSummary struct {
	Count       int64      `json:"count"`
	TotalAmount int64      `json:"total_amount"`
	SettledAt   *time.Time `json:"settled_at"`
}

type DistributorHWIDSettings struct {
	Enabled         bool `json:"enabled"`
	Limit           int  `json:"limit"`
	RegisteredCount int  `json:"registered_count"`
}

type DistributorHWIDDevice struct {
	ID          int64     `json:"id"`
	HWID        string    `json:"hwid"`
	DeviceOS    *string   `json:"device_os"`
	OSVersion   *string   `json:"os_version"`
	DeviceModel *string   `json:"device_model"`
	UserAgent   *string   `json:"user_agent"`
	IPAddress   *string   `json:"ip"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type AuthorizeDistributorHWIDInput struct {
	SubscriberUserID int64
	HWID             string
	DeviceOS         string
	OSVersion        string
	DeviceModel      string
	UserAgent        string
	IPAddress        string
}

type DistributorHWIDAuthorization struct {
	SubscriptionID  int64
	OriginalTradeNo string
	Enabled         bool
	Allowed         bool
	LimitReached    bool
	NotSupported    bool
}

type DistributorClaim struct {
	SubscriptionID    int64  `json:"-"`
	SubscriptionToken string `json:"-"`
	OriginalTradeNo   string `json:"-"`
}

type StaleOrderBatchResult struct {
	Cancelled int `json:"cancelled"`
	Completed int `json:"completed"`
	Remaining int `json:"remaining"`
}

type AdminOrder struct {
	Order
	UserEmail string `json:"user_email"`
	PlanName  string `json:"plan_name"`
}

type AdminOrderInviteUser struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

type AdminOrderDetail struct {
	AdminOrder
	InviteUser    *AdminOrderInviteUser `json:"invite_user"`
	CommissionLog []CommissionLog       `json:"commission_log"`
	SubscribeURL  *string               `json:"subscribe_url"`
}

type AdminOrderSortField string

const (
	AdminOrderSortCreatedAt         AdminOrderSortField = "created_at"
	AdminOrderSortTotalAmount       AdminOrderSortField = "total_amount"
	AdminOrderSortStatus            AdminOrderSortField = "status"
	AdminOrderSortCommissionBalance AdminOrderSortField = "commission_balance"
	AdminOrderSortCommissionStatus  AdminOrderSortField = "commission_status"
)

type AdminOrderFilter struct {
	Page               int
	PageSize           int
	UserID             *int64
	Status             *OrderStatus
	Type               *OrderType
	Period             string
	Statuses           []OrderStatus
	Types              []OrderType
	Periods            []string
	CommissionStatuses []int
	Query              string
	SortBy             AdminOrderSortField
	SortDescending     bool
}

type AdminOrderPage struct {
	Items    []AdminOrder `json:"items"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

type AssignOrderInput struct {
	UserID      *int64
	Email       string
	PlanID      int64
	Period      string
	TotalAmount int64
}

type Plan struct {
	ID                 int64      `json:"id"`
	GroupID            *int64     `json:"group_id"`
	TransferEnableGiB  int64      `json:"transfer_enable"`
	Name               string     `json:"name"`
	SpeedLimit         *int       `json:"speed_limit"`
	Show               bool       `json:"show"`
	SortPosition       int        `json:"sort"`
	Renew              bool       `json:"renew"`
	Content            string     `json:"content"`
	ResetTrafficMethod *int       `json:"reset_traffic_method"`
	CapacityLimit      *int       `json:"capacity_limit"`
	Prices             PlanPrices `json:"prices"`
	Sell               bool       `json:"sell"`
	DeviceLimit        *int       `json:"device_limit"`
	Tags               []string   `json:"tags"`
	UsersCount         int64      `json:"-"`
	ActiveUsersCount   int64      `json:"-"`
	CapacityUsersCount int64      `json:"-"`
	Revision           int64      `json:"revision"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type SavePlanInput struct {
	GroupID            *int64
	TransferEnableGiB  int64
	Name               string
	SpeedLimit         *int
	Content            string
	ResetTrafficMethod *int
	CapacityLimit      *int
	Prices             PlanPrices
	DeviceLimit        *int
	Tags               []string
}

type PlanState struct {
	Show  bool `json:"show"`
	Sell  bool `json:"sell"`
	Renew bool `json:"renew"`
}

type PlanOffer struct {
	Plan
	CapacityRemaining *int64 `json:"capacity_remaining"`
	CanPurchase       bool   `json:"can_purchase"`
	CanRenew          bool   `json:"can_renew"`
}

type TrafficResetBatchResult struct {
	Processed int `json:"processed"`
	Remaining int `json:"remaining"`
}

type RoutingRule struct {
	ID          int64     `json:"id"`
	Remarks     string    `json:"remarks"`
	Match       []string  `json:"match"`
	Action      string    `json:"action"`
	ActionValue string    `json:"action_value,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Notice struct {
	ID           int64     `json:"id"`
	SortPosition int       `json:"sort"`
	Title        string    `json:"title"`
	Content      string    `json:"content"`
	ImageURL     *string   `json:"image_url"`
	Tags         []string  `json:"tags"`
	Visible      bool      `json:"show"`
	Revision     int64     `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SaveNoticeInput struct {
	Title    string
	Content  string
	ImageURL string
	Tags     []string
	Visible  bool
}

type NoticePage struct {
	Items    []Notice `json:"items"`
	Total    int64    `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

type ClientCatalogOverride struct {
	ClientID string `json:"client_id"`
	Platform string `json:"platform"`
	Action   string `json:"action"`
	URL      string `json:"url"`
}

type ClientCatalogConfig struct {
	Revision  int64                   `json:"revision"`
	Links     []ClientCatalogOverride `json:"links"`
	UpdatedAt time.Time               `json:"updated_at"`
}

type SaveRoutingRuleInput struct {
	Remarks     string
	Match       []string
	Action      string
	ActionValue string
}

type NodeRuntimeTarget struct {
	NodeID    int64
	MachineID int64
}

type NodeRuntimeGroupTarget struct {
	NodeID    int64
	MachineID int64
	GroupID   int64
}

type RuntimeUser struct {
	ID          int64  `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`
	DeviceLimit int    `json:"device_limit"`
}

type CreateRuntimeUserInput struct {
	Email           string
	PasswordHash    string
	AccountKind     string
	UUID            string
	GroupID         int64
	TransferEnable  int64
	TrafficUpload   int64
	TrafficDownload int64
	ExpiredAt       *time.Time
	SpeedLimit      int
	DeviceLimit     int
	Banned          bool
}

type TrafficUsage struct {
	Upload   int64
	Download int64
}

type NodeReportInput struct {
	MachineID         int64
	LegacyAuth        bool
	NodeID            int64
	ReportID          string
	Traffic           map[int64]TrafficUsage
	Alive             map[int64][]string
	ReplaceAllDevices bool
	Online            map[int64]int64
	Status            json.RawMessage
	Metrics           json.RawMessage
	Now               time.Time
}

type NodeReportResult struct {
	DuplicateTraffic bool
	DeviceUserIDs    []int64
}

type NodeRuntimeState struct {
	NodeID    int64
	Status    json.RawMessage
	Metrics   json.RawMessage
	UpdatedAt time.Time
}

type CreateNodeInput struct {
	Name      string
	Type      string
	Host      string
	Port      string
	Show      bool
	Enabled   bool
	Sort      int
	MachineID *int64
}

type AdminNode struct {
	Node
	MachineName *string `json:"machine_name"`
	GroupIDs    []int64 `json:"group_ids"`
	OnlineCount int64   `json:"online_count"`
}

type AdminNodeDefinition struct {
	Node
	ExternalCode      string          `json:"external_code"`
	ParentID          *int64          `json:"parent_id"`
	ServerPort        int             `json:"server_port"`
	ListenAddress     string          `json:"listen_address"`
	Tags              []string        `json:"tags"`
	ProtocolSettings  json.RawMessage `json:"protocol_settings"`
	GroupIDs          []int64         `json:"group_ids"`
	RouteIDs          []int64         `json:"route_ids"`
	RateTimeEnabled   bool            `json:"rate_time_enabled"`
	RateTimeRanges    json.RawMessage `json:"rate_time_ranges"`
	CustomOutbounds   json.RawMessage `json:"custom_outbounds"`
	CustomRoutes      json.RawMessage `json:"custom_routes"`
	CertificateConfig json.RawMessage `json:"certificate_config"`
	TransferEnable    int64           `json:"transfer_enable"`
}

type SaveAdminNodeDefinitionInput struct {
	Revision          int64
	Type              string
	ExternalCode      string
	ParentID          *int64
	Name              string
	RateMicros        int64
	Tags              []string
	Host              string
	Port              string
	ServerPort        int
	ListenAddress     string
	ProtocolSettings  json.RawMessage
	Show              bool
	Enabled           bool
	Sort              int
	MachineID         *int64
	GroupIDs          []int64
	RouteIDs          []int64
	RateTimeEnabled   bool
	RateTimeRanges    json.RawMessage
	CustomOutbounds   json.RawMessage
	CustomRoutes      json.RawMessage
	CertificateConfig json.RawMessage
	TransferEnable    int64
}

type AdminNodePage struct {
	Items    []AdminNode `json:"items"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

type AdminNodeFilter struct {
	Page       int
	PageSize   int
	Query      string
	Type       string
	Show       *bool
	Enabled    *bool
	MachineID  *int64
	Unassigned bool
}

type UpdateAdminNodeInput struct {
	Revision     int64
	Name         string
	Host         string
	Port         string
	Show         bool
	Enabled      bool
	Sort         int
	MachineIDSet bool
	MachineID    *int64
}

type AdminNodeRevision struct {
	ID       int64 `json:"id"`
	Revision int64 `json:"revision"`
}

type AdminNodeStateInput struct {
	Targets      []AdminNodeRevision
	Show         *bool
	Enabled      *bool
	MachineIDSet bool
	MachineID    *int64
}

type AdminNodeMutation struct {
	NodeIDs         []int64             `json:"node_ids"`
	MachineIDs      []int64             `json:"-"`
	FullSyncs       []AdminNodeFullSync `json:"-"`
	ClearNodeIDs    []int64             `json:"-"`
	AffectedUserIDs []int64             `json:"-"`
}

type AdminNodeFullSync struct {
	MachineID int64
	NodeID    int64
}

type LoadHistory struct {
	ID          int64     `json:"id"`
	MachineID   int64     `json:"machine_id"`
	CPUPercent  float64   `json:"cpu"`
	MemoryTotal int64     `json:"mem_total"`
	MemoryUsed  int64     `json:"mem_used"`
	DiskTotal   int64     `json:"disk_total"`
	DiskUsed    int64     `json:"disk_used"`
	NetworkIn   float64   `json:"net_in_speed"`
	NetworkOut  float64   `json:"net_out_speed"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type MachineStatusInput struct {
	CPUPercent  float64
	MemoryTotal int64
	MemoryUsed  int64
	SwapTotal   int64
	SwapUsed    int64
	DiskTotal   int64
	DiskUsed    int64
	NetworkIn   *float64
	NetworkOut  *float64
}

type ActivationSchedule struct {
	NodeID            int64      `json:"server_id"`
	ScheduleType      string     `json:"schedule_type"`
	Timezone          string     `json:"timezone"`
	EnableTime        string     `json:"enable_time"`
	DisableTime       string     `json:"disable_time"`
	EnableAt          *time.Time `json:"enable_at,omitempty"`
	DisableAt         *time.Time `json:"disable_at,omitempty"`
	Revision          string     `json:"revision"`
	NextTransitionAt  time.Time  `json:"next_transition_at"`
	NextTargetEnabled bool       `json:"next_target_enabled"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type DueSchedule struct {
	NodeID           int64
	Revision         string
	NextTransitionAt time.Time
}
