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
	ErrEmailInUse                             = fmt.Errorf("%w: email already in use", ErrConflict)
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
	ErrOpenTicketExists                       = fmt.Errorf("%w: an open ticket already exists", ErrConflict)
	ErrTicketClosed                           = fmt.Errorf("%w: ticket is closed", ErrConflict)
	ErrTicketReplyPending                     = fmt.Errorf("%w: ticket reply is pending administrator response", ErrConflict)
	ErrTicketMessageLimit                     = fmt.Errorf("%w: ticket message limit reached", ErrConflict)
	ErrInvalidInput                           = errors.New("invalid input")
	ErrInvalidEnrollment                      = errors.New("invalid or expired enrollment")
	ErrInvalidCredential                      = errors.New("invalid machine credential")
	ErrNodeNotLinked                          = errors.New("node is not linked to a machine")
	ErrRuntimeNotConfigured                   = errors.New("node runtime is not configured")
)

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	IsAdmin      bool
	Banned       bool
	AccountKind  string
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
	Revision                   int64     `json:"revision"`
	AppName                    string    `json:"app_name"`
	AppDescription             string    `json:"app_description"`
	AppURL                     string    `json:"app_url"`
	TOSURL                     string    `json:"tos_url"`
	Logo                       string    `json:"logo"`
	StopRegister               bool      `json:"stop_register"`
	EmailVerificationEnabled   bool      `json:"email_verify"`
	EmailWhitelistEnabled      bool      `json:"email_whitelist_enable"`
	EmailWhitelistSuffixes     []string  `json:"email_whitelist_suffix"`
	GmailAliasLimitEnabled     bool      `json:"email_gmail_limit_enable"`
	RegistrationIPLimitEnabled bool      `json:"register_limit_by_ip_enable"`
	RegistrationIPLimitCount   int       `json:"register_limit_count"`
	RegistrationIPLimitMinutes int       `json:"register_limit_expire"`
	InvitationForceEnabled     bool      `json:"invite_force"`
	InvitationCodeLimit        int       `json:"invite_gen_limit"`
	InvitationNeverExpire      bool      `json:"invite_never_expire"`
	UpdatedAt                  time.Time `json:"updated_at"`
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
	InvitationForceEnabled     bool
	InvitationCodeLimit        int
	InvitationNeverExpire      bool
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
	Codes        []InvitationCode
	InvitedCount int64
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

const (
	AccountKindHuman                = "human"
	AccountKindInternalSubscription = "internal_subscription"
)

type AdminUser struct {
	ID              int64      `json:"id"`
	Email           string     `json:"email"`
	IsAdmin         bool       `json:"is_admin"`
	Banned          bool       `json:"banned"`
	GroupID         *int64     `json:"group_id"`
	TransferEnable  int64      `json:"transfer_enable"`
	TrafficUpload   int64      `json:"traffic_upload"`
	TrafficDownload int64      `json:"traffic_download"`
	ExpiredAt       *time.Time `json:"expired_at"`
	SpeedLimit      int        `json:"speed_limit"`
	DeviceLimit     int        `json:"device_limit"`
	OnlineCount     int        `json:"online_count"`
	LastOnlineAt    *time.Time `json:"last_online_at"`
	Revision        int64      `json:"revision"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type AdminUserPage struct {
	Items      []AdminUser `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

type AdminUserFilter struct {
	Limit       int
	Cursor      string
	EmailPrefix string
	Banned      *bool
	GroupID     *int64
}

type CreateAdminUserInput struct {
	Email          string
	PasswordHash   string
	GroupID        *int64
	TransferEnable int64
	ExpiredAt      *time.Time
	SpeedLimit     int
	DeviceLimit    int
	Banned         bool
}

type UpdateAdminUserInput struct {
	Revision       int64
	Email          string
	GroupID        *int64
	TransferEnable int64
	ExpiredAt      *time.Time
	SpeedLimit     int
	DeviceLimit    int
	Banned         bool
}

type AdminUserMutation struct {
	OldGroupID         *int64
	NewGroupID         *int64
	UUID               string
	RuntimeChanged     bool
	AccessStateCleared bool
}

type SessionUser struct {
	UserID     int64
	Email      string
	IsAdmin    bool
	Banned     bool
	CSRFHash   string
	ExpiresAt  time.Time
	SessionID  int64
	LastUsedAt *time.Time
}

type AccountSession struct {
	ID         int64      `json:"id"`
	IsCurrent  bool       `json:"is_current"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
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

type Node struct {
	ID                int64      `json:"id"`
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
