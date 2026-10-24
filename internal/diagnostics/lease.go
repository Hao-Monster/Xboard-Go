package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	MaxConcurrentSessions = 10
	MaxSessionDuration    = 900 * time.Second // 15 minutes
	SnapshotBindingTTL    = 60 * time.Second
	IdempotencyTTL        = 24 * time.Hour
)

var (
	requestIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// SessionState represents the lifecycle stage of a 15-minute diagnostic session.
type SessionState string

const (
	SessionStatePending  SessionState = "pending"
	SessionStatePartial  SessionState = "partial"
	SessionStateActive   SessionState = "active"
	SessionStateStopping SessionState = "stopping"
	SessionStateStopped  SessionState = "stopped"
	SessionStateExpired  SessionState = "expired"
)

// Session represents an authoritative 15-minute diagnostic session for a single customer.
type Session struct {
	SessionID     string                  `json:"session_id"`
	DiagnosticID  string                  `json:"diagnostic_id"`
	OperatorID    int64                   `json:"operator_id"`
	CreatedAt     time.Time               `json:"created_at"`
	IssuedAt      time.Time               `json:"issued_at"`
	ExpiresAt     time.Time               `json:"expires_at"`
	Revision      int64                   `json:"revision"`
	Enabled       bool                    `json:"enabled"`
	State         SessionState            `json:"state"`
	TargetNodeIDs []int64                 `json:"target_node_ids"`
	NodeAcks      map[int64]NodeAckRecord `json:"node_acks"`
}

// NodeAckRecord records the acknowledgement status reported by a node for a session.
type NodeAckRecord struct {
	NodeID          int64     `json:"node_id"`
	RuntimeID       string    `json:"runtime_id"`
	Effective       bool      `json:"effective"`
	Revision        int64     `json:"revision"`
	AcknowledgedAt  time.Time `json:"acknowledged_at"`
	LocalDeadlineMS int64     `json:"local_deadline_ms,omitempty"`
	RejectReason    string    `json:"reject_reason,omitempty"`
}

// StartSessionInput specifies arguments for initiating a diagnostic session.
type StartSessionInput struct {
	DiagnosticID   string
	OperatorID     int64
	IdempotencyKey string
	TargetNodeIDs  []int64
	Now            time.Time
}

// StopSessionInput specifies arguments for explicitly terminating a diagnostic session.
type StopSessionInput struct {
	DiagnosticID   string
	SessionID      string
	OperatorID     int64
	IdempotencyKey string
	Now            time.Time
}

// NodeSnapshotSession is the per-session entry included in authoritative node snapshots.
type NodeSnapshotSession struct {
	SessionID          string `json:"session_id"`
	DiagnosticID       string `json:"diagnostic_id"`
	Revision           int64  `json:"revision"`
	IssuedAt           int64  `json:"issued_at"`
	ExpiresAt          int64  `json:"expires_at"`
	MaxDurationSeconds int64  `json:"max_duration_seconds"`
}

// NodeSnapshotResponse is the complete authoritative snapshot returned to a node.
type NodeSnapshotResponse struct {
	SchemaVersion  int                   `json:"schema_version"`
	PanelScope     string                `json:"panel_scope"`
	ControlEpoch   string                `json:"control_epoch"`
	ControlVersion string                `json:"control_version"`
	RequestID      string                `json:"request_id"`
	RuntimeID      string                `json:"runtime_id"`
	ServerTime     int64                 `json:"server_time"`
	Complete       bool                  `json:"complete"`
	Sessions       []NodeSnapshotSession `json:"sessions"`
}

// NodeAckSessionPayload is a single session entry in a node's ACK report.
type NodeAckSessionPayload struct {
	SessionID       string `json:"session_id"`
	Revision        int64  `json:"revision"`
	Effective       bool   `json:"effective"`
	LocalDeadlineMS int64  `json:"local_deadline_ms,omitempty"`
	RejectReason    string `json:"reject_reason,omitempty"`
}

// NodeAckPayload is the payload sent by a node acknowledging a diagnostics snapshot.
type NodeAckPayload struct {
	RequestID      string                  `json:"request_id"`
	RuntimeID      string                  `json:"runtime_id"`
	ControlEpoch   string                  `json:"control_epoch"`
	ControlVersion string                  `json:"control_version"`
	Sessions       []NodeAckSessionPayload `json:"sessions"`
}

type snapshotBinding struct {
	nodeID         int64
	runtimeID      string
	requestID      string
	controlEpoch   string
	controlVersion string
	sessions       map[string]int64 // session_id -> revision
	boundAt        time.Time
}

type idempotencyEntry struct {
	diagnosticID string
	sessionID    string
	expiresAt    time.Time
	createdAt    time.Time
}

// LeaseService defines the authoritative interface for managing diagnostic sessions.
type LeaseService interface {
	ControlEpoch() string
	ControlVersion() int64
	StartSession(ctx context.Context, input StartSessionInput) (Session, bool, error)
	StopSession(ctx context.Context, input StopSessionInput) (Session, error)
	GetSession(ctx context.Context, diagnosticID string) (Session, bool, error)
	BuildNodeSnapshot(ctx context.Context, panelScope string, nodeID int64, runtimeID, requestID string, now time.Time) (NodeSnapshotResponse, error)
	ProcessNodeAck(ctx context.Context, nodeID int64, runtimeID string, ack NodeAckPayload, now time.Time) error
}

// MemoryLeaseService provides a thread-safe, in-memory authoritative lease manager.
type MemoryLeaseService struct {
	mu             sync.Mutex
	controlEpoch   string
	controlVersion int64

	// Active and recent sessions: diagnostic_id -> *Session
	sessions map[string]*Session
	// Idempotency: operator_id:key -> idempotencyEntry
	idempotency map[string]idempotencyEntry
	// Snapshot bindings: (nodeID, requestID) -> snapshotBinding
	bindings map[string]snapshotBinding
}

// NewMemoryLeaseService instantiates an authoritative memory-backed lease service.
func NewMemoryLeaseService() *MemoryLeaseService {
	return &MemoryLeaseService{
		controlEpoch:   uuid.NewString(),
		controlVersion: 1,
		sessions:       make(map[string]*Session),
		idempotency:    make(map[string]idempotencyEntry),
		bindings:       make(map[string]snapshotBinding),
	}
}

func (s *MemoryLeaseService) ControlEpoch() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlEpoch
}

func (s *MemoryLeaseService) ControlVersion() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlVersion
}

// StartSession initiates or retrieves an existing diagnostic session for a customer.
// Returns (session, isNew, error).
func (s *MemoryLeaseService) StartSession(ctx context.Context, input StartSessionInput) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	s.cleanupExpired(now)

	// Check Idempotency Key
	idempKey := fmt.Sprintf("%d:%s", input.OperatorID, input.IdempotencyKey)
	if entry, exists := s.idempotency[idempKey]; exists {
		if entry.diagnosticID != input.DiagnosticID {
			return Session{}, false, ErrIdempotencyConflict
		}
		if existing, ok := s.sessions[input.DiagnosticID]; ok && existing.SessionID == entry.sessionID {
			return *existing, false, nil
		}
	}

	// Check if the customer already has an active session
	if existing, ok := s.sessions[input.DiagnosticID]; ok {
		if existing.Enabled && now.Before(existing.ExpiresAt) {
			// Idempotent start: return existing session without extending expires_at
			return *existing, false, nil
		}
	}

	// Count currently active sessions
	activeCount := 0
	for _, sess := range s.sessions {
		if sess.Enabled && now.Before(sess.ExpiresAt) {
			activeCount++
		}
	}
	if activeCount >= MaxConcurrentSessions {
		return Session{}, false, ErrLeaseLimitReached
	}

	s.controlVersion++
	sessionID := uuid.NewString()
	expiresAt := now.Add(MaxSessionDuration)

	targetNodes := append([]int64(nil), input.TargetNodeIDs...)
	sort.Slice(targetNodes, func(i, j int) bool { return targetNodes[i] < targetNodes[j] })

	sess := &Session{
		SessionID:     sessionID,
		DiagnosticID:  input.DiagnosticID,
		OperatorID:    input.OperatorID,
		CreatedAt:     now,
		IssuedAt:      now,
		ExpiresAt:     expiresAt,
		Revision:      1,
		Enabled:       true,
		State:         SessionStatePending,
		TargetNodeIDs: targetNodes,
		NodeAcks:      make(map[int64]NodeAckRecord),
	}

	s.sessions[input.DiagnosticID] = sess
	if input.IdempotencyKey != "" {
		s.idempotency[idempKey] = idempotencyEntry{
			diagnosticID: input.DiagnosticID,
			sessionID:    sessionID,
			expiresAt:    expiresAt,
			createdAt:    now,
		}
	}

	return *sess, true, nil
}

// StopSession explicitly terminates a running diagnostic session.
func (s *MemoryLeaseService) StopSession(ctx context.Context, input StopSessionInput) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}

	sess, ok := s.sessions[input.DiagnosticID]
	if !ok || sess.SessionID != input.SessionID {
		return Session{}, errors.New("diagnostic session not found")
	}

	if !sess.Enabled {
		return *sess, nil
	}

	s.controlVersion++
	sess.Revision++
	sess.Enabled = false
	sess.State = SessionStateStopping

	// Check if already acknowledged as stopped by all targets
	allStopped := true
	for _, nodeID := range sess.TargetNodeIDs {
		ack, ok := sess.NodeAcks[nodeID]
		if !ok || ack.Effective || ack.Revision < sess.Revision {
			allStopped = false
			break
		}
	}
	if allStopped || len(sess.TargetNodeIDs) == 0 {
		sess.State = SessionStateStopped
	}

	return *sess, nil
}

// GetSession returns the current session status for a diagnostic ID.
func (s *MemoryLeaseService) GetSession(ctx context.Context, diagnosticID string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[diagnosticID]
	if !ok {
		return Session{}, false, nil
	}
	return *sess, true, nil
}

// BuildNodeSnapshot generates an authoritative snapshot tailored for a specific node.
func (s *MemoryLeaseService) BuildNodeSnapshot(ctx context.Context, panelScope string, nodeID int64, runtimeID, requestID string, now time.Time) (NodeSnapshotResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.IsZero() {
		now = time.Now()
	}
	s.cleanupExpired(now)

	activeSessions := make([]NodeSnapshotSession, 0)
	boundSessions := make(map[string]int64)

	for _, sess := range s.sessions {
		if !sess.Enabled || now.After(sess.ExpiresAt) {
			continue
		}
		// Check if this node is targeted
		targeted := false
		for _, tid := range sess.TargetNodeIDs {
			if tid == nodeID {
				targeted = true
				break
			}
		}
		if !targeted {
			continue
		}

		activeSessions = append(activeSessions, NodeSnapshotSession{
			SessionID:          sess.SessionID,
			DiagnosticID:       sess.DiagnosticID,
			Revision:           sess.Revision,
			IssuedAt:           sess.IssuedAt.Unix(),
			ExpiresAt:          sess.ExpiresAt.Unix(),
			MaxDurationSeconds: int64(MaxSessionDuration.Seconds()),
		})
		boundSessions[sess.SessionID] = sess.Revision
	}

	sort.Slice(activeSessions, func(i, j int) bool {
		return activeSessions[i].SessionID < activeSessions[j].SessionID
	})

	versionStr := strconv.FormatInt(s.controlVersion, 10)
	resp := NodeSnapshotResponse{
		SchemaVersion:  1,
		PanelScope:     panelScope,
		ControlEpoch:   s.controlEpoch,
		ControlVersion: versionStr,
		RequestID:      requestID,
		RuntimeID:      runtimeID,
		ServerTime:     now.Unix(),
		Complete:       true,
		Sessions:       activeSessions,
	}

	// Record 60-second snapshot binding to validate future ACKs
	bindingKey := fmt.Sprintf("%d:%s", nodeID, requestID)
	s.bindings[bindingKey] = snapshotBinding{
		nodeID:         nodeID,
		runtimeID:      runtimeID,
		requestID:      requestID,
		controlEpoch:   s.controlEpoch,
		controlVersion: versionStr,
		sessions:       boundSessions,
		boundAt:        now,
	}

	return resp, nil
}

// ProcessNodeAck validates and incorporates a node's ACK report.
func (s *MemoryLeaseService) ProcessNodeAck(ctx context.Context, nodeID int64, runtimeID string, ack NodeAckPayload, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.IsZero() {
		now = time.Now()
	}

	// Validate 60-second snapshot binding
	bindingKey := fmt.Sprintf("%d:%s", nodeID, ack.RequestID)
	binding, ok := s.bindings[bindingKey]
	if !ok || now.Sub(binding.boundAt) > SnapshotBindingTTL {
		return ErrSnapshotConflict
	}

	if binding.controlEpoch != ack.ControlEpoch || binding.controlVersion != ack.ControlVersion {
		return ErrSnapshotConflict
	}

	// Validate each reported session: must be strictly present in the authoritative binding
	for _, report := range ack.Sessions {
		expectedRev, inBinding := binding.sessions[report.SessionID]
		if !inBinding || report.Revision != expectedRev {
			// Forgery or stale revision attempt: reject ACK
			return fmt.Errorf("%w: session %s not in authoritative snapshot binding", ErrSnapshotConflict, report.SessionID)
		}

		// Find the active session and apply ACK
		for _, sess := range s.sessions {
			if sess.SessionID == report.SessionID {
				sess.NodeAcks[nodeID] = NodeAckRecord{
					NodeID:          nodeID,
					RuntimeID:       runtimeID,
					Effective:       report.Effective,
					Revision:        report.Revision,
					AcknowledgedAt:  now,
					LocalDeadlineMS: report.LocalDeadlineMS,
					RejectReason:    report.RejectReason,
				}

				// Recompute session state
				s.updateSessionState(sess)
				break
			}
		}
	}

	return nil
}

func (s *MemoryLeaseService) updateSessionState(sess *Session) {
	if !sess.Enabled {
		allStopped := true
		for _, nid := range sess.TargetNodeIDs {
			ack, ok := sess.NodeAcks[nid]
			if !ok || ack.Effective || ack.Revision < sess.Revision {
				allStopped = false
				break
			}
		}
		if allStopped {
			sess.State = SessionStateStopped
		} else {
			sess.State = SessionStateStopping
		}
		return
	}

	effectiveCount := 0
	for _, nid := range sess.TargetNodeIDs {
		ack, ok := sess.NodeAcks[nid]
		if ok && ack.Effective && ack.Revision == sess.Revision {
			effectiveCount++
		}
	}

	if len(sess.TargetNodeIDs) == 0 {
		sess.State = SessionStateActive
	} else if effectiveCount == len(sess.TargetNodeIDs) {
		sess.State = SessionStateActive
	} else if effectiveCount > 0 {
		sess.State = SessionStatePartial
	} else {
		sess.State = SessionStatePending
	}
}

func (s *MemoryLeaseService) cleanupExpired(now time.Time) {
	for _, sess := range s.sessions {
		if sess.Enabled && now.After(sess.ExpiresAt) {
			sess.Enabled = false
			sess.State = SessionStateExpired
		}
	}
	for k, b := range s.bindings {
		if now.Sub(b.boundAt) > SnapshotBindingTTL {
			delete(s.bindings, k)
		}
	}
	for k, idemp := range s.idempotency {
		if now.Sub(idemp.createdAt) > IdempotencyTTL {
			delete(s.idempotency, k)
		}
	}
}

// GenerateRandomHex produces a random lowercase hex string of length n.
func GenerateRandomHex(n int) string {
	bytes := make([]byte, n/2)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
