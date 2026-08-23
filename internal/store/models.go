package store

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrConflict             = errors.New("conflict")
	ErrInvalidInput         = errors.New("invalid input")
	ErrInvalidEnrollment    = errors.New("invalid or expired enrollment")
	ErrInvalidCredential    = errors.New("invalid machine credential")
	ErrNodeNotLinked        = errors.New("node is not linked to a machine")
	ErrRuntimeNotConfigured = errors.New("node runtime is not configured")
)

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	IsAdmin      bool
	Banned       bool
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
