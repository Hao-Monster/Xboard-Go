package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
	"github.com/google/uuid"
)

// DiagnosticsCustomerIdentity contains the non-sensitive identity metadata of a customer resolved from an order.
// NOTE: UUID, tokens, subscribe URLs, and passwords are strictly excluded from this struct.
type DiagnosticsCustomerIdentity struct {
	OrderID             int64  `json:"order_id"`
	TradeNo             string `json:"trade_no"`
	OrderStatus         int    `json:"order_status"`
	OrderType           string `json:"order_type"`
	CustomerDisplayName string `json:"customer_display_name"`
	SubscriptionName    string `json:"subscription_name"`
	DiagnosticID        string `json:"diagnostic_id"`
	SubscriberUserID    int64  `json:"subscriber_user_id"`
	PanelScope          string `json:"panel_scope"`
}

// DiagnosticsOrderLookupInput specifies the query parameters for looking up a customer by order.
// Exactly one of TradeNo or OrderID must be provided.
type DiagnosticsOrderLookupInput struct {
	TradeNo *string `json:"trade_no,omitempty"`
	OrderID *int64  `json:"order_id,omitempty"`
}

// DiagnosticsSettings represents the administrative view of diagnostics configuration.
type DiagnosticsSettings struct {
	PanelScope             string    `json:"panel_scope"`
	QueryEndpoint          string    `json:"query_endpoint"`
	QueryUsername          string    `json:"query_username"`
	HasQueryPassword       bool      `json:"has_query_password"`
	HasCursorSigningSecret bool      `json:"has_cursor_signing_secret"`
	StorageGeneration      int64     `json:"storage_generation"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

// DiagnosticsServerConfig represents internal server-only configuration.
type DiagnosticsServerConfig struct {
	PanelScope                string
	QueryEndpoint             string
	QueryUsername             string
	QueryPasswordCipher       []byte
	CursorSigningSecretCipher []byte
	StorageGeneration         int64
}

// NodeDiagnosticsCapability records the diagnostics capability reported by a node instance.
type NodeDiagnosticsCapability struct {
	NodeID        int64     `json:"node_id"`
	RuntimeID     string    `json:"runtime_id"`
	SchemaVersion int       `json:"schema_version"`
	Kernel        string    `json:"kernel"`
	Capabilities  []string  `json:"capabilities"`
	UpdatedAt     time.Time `json:"updated_at"`
}

const diagnosticsSettingsSchema = `
CREATE TABLE IF NOT EXISTS diagnostics_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    panel_scope TEXT CHECK (panel_scope IS NULL OR (length(panel_scope) = 36 AND panel_scope NOT GLOB '*[^0-9a-f-]*')),
    query_endpoint TEXT NOT NULL DEFAULT '',
    query_username TEXT NOT NULL DEFAULT '',
    query_password_cipher BLOB,
    cursor_signing_secret_cipher BLOB,
    storage_generation INTEGER NOT NULL DEFAULT 1 CHECK (storage_generation > 0),
    created_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO diagnostics_settings (id, storage_generation, created_at, updated_at) VALUES (1, 1, 0, 0);

CREATE TABLE IF NOT EXISTS node_diagnostics_capabilities (
    node_id INTEGER PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    runtime_id TEXT NOT NULL DEFAULT '',
    schema_version INTEGER NOT NULL DEFAULT 1,
    kernel TEXT NOT NULL DEFAULT '',
    capabilities TEXT NOT NULL DEFAULT '[]',
    updated_at INTEGER NOT NULL DEFAULT 0
);
`

// EnsureDiagnosticsSettingsTable creates the diagnostics settings and node capabilities tables once per Store.
// The DDL (including the seed INSERT) runs under the store write lock exactly once; afterwards the fast path
// is a single atomic load so node synchronization hot paths never issue writes.
// Callers must NOT hold the store write lock (lock order: diagnosticsSchemaMu -> writeMu).
func (s *Store) EnsureDiagnosticsSettingsTable(ctx context.Context) error {
	if s.diagnosticsSchemaReady.Load() {
		return nil
	}
	s.diagnosticsSchemaMu.Lock()
	defer s.diagnosticsSchemaMu.Unlock()
	if s.diagnosticsSchemaReady.Load() {
		return nil
	}
	unlock := s.lockWrite()
	_, err := s.db.ExecContext(ctx, diagnosticsSettingsSchema)
	unlock()
	if err != nil {
		return fmt.Errorf("ensure diagnostics_settings table: %w", err)
	}
	s.diagnosticsSchemaReady.Store(true)
	return nil
}

// InitializeDiagnosticsScope idempotently initializes the installation-wide panel_scope UUIDv4.
// If scopeCandidate is empty, a new UUIDv4 is generated.
// If panel_scope is already initialized, the existing scope is returned with created = false.
func (s *Store) InitializeDiagnosticsScope(ctx context.Context, scopeCandidate string, now time.Time) (string, bool, error) {
	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return "", false, err
	}

	scope := strings.TrimSpace(scopeCandidate)
	if scope == "" {
		scope = strings.ToLower(uuid.NewString())
	} else {
		scope = strings.ToLower(scope)
		if len(scope) != 36 {
			return "", false, diagnostics.ErrInvalidPanelScope
		}
		// Validate lowercase UUIDv4 format using ComputeDiagnosticID validator
		if _, err := diagnostics.ComputeDiagnosticID(scope, 1); err != nil {
			return "", false, diagnostics.ErrInvalidPanelScope
		}
	}

	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin initialize scope tx: %w", err)
	}
	defer tx.Rollback()

	var existingScope sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT panel_scope FROM diagnostics_settings WHERE id = 1`).Scan(&existingScope); err != nil {
		return "", false, fmt.Errorf("query existing panel_scope: %w", err)
	}

	if existingScope.Valid && existingScope.String != "" {
		s.diagnosticsScopeMu.Lock()
		s.diagnosticsScope = existingScope.String
		s.diagnosticsScopeMu.Unlock()
		return existingScope.String, false, nil
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE diagnostics_settings
		SET panel_scope = ?, created_at = ?, updated_at = ?
		WHERE id = 1 AND (panel_scope IS NULL OR panel_scope = '')
	`, scope, now.Unix(), now.Unix())
	if err != nil {
		return "", false, fmt.Errorf("set panel_scope: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("inspect set panel_scope rows: %w", err)
	}

	if rows == 0 {
		// Another writer (e.g. a different process sharing the database) won the race.
		// Re-read inside the same transaction; never call back into GetDiagnosticsScope while holding writeMu.
		var winner sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT panel_scope FROM diagnostics_settings WHERE id = 1`).Scan(&winner); err != nil {
			return "", false, fmt.Errorf("re-read panel_scope: %w", err)
		}
		if !winner.Valid || strings.TrimSpace(winner.String) == "" {
			return "", false, fmt.Errorf("%w: panel_scope initialization lost without a winner", ErrConflict)
		}
		s.diagnosticsScopeMu.Lock()
		s.diagnosticsScope = winner.String
		s.diagnosticsScopeMu.Unlock()
		return winner.String, false, nil
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit initialize scope tx: %w", err)
	}

	s.diagnosticsScopeMu.Lock()
	s.diagnosticsScope = scope
	s.diagnosticsScopeMu.Unlock()

	return scope, true, nil
}

// GetDiagnosticsScope returns the active panel_scope, or ErrDiagnosticsNotInitialized if unset.
// The scope is immutable once set, so a positive result is cached for the Store lifetime.
// An uninitialized state is intentionally NOT cached: the lookup is a single read-only primary-key
// query, and caching "absent" would hide a scope initialized by another panel replica sharing the database.
func (s *Store) GetDiagnosticsScope(ctx context.Context) (string, error) {
	s.diagnosticsScopeMu.RLock()
	cached := s.diagnosticsScope
	s.diagnosticsScopeMu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return "", err
	}

	var scope sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT panel_scope FROM diagnostics_settings WHERE id = 1`).Scan(&scope)
	if errors.Is(err, sql.ErrNoRows) {
		return "", diagnostics.ErrDiagnosticsNotInitialized
	}
	if err != nil {
		return "", fmt.Errorf("query panel_scope: %w", err)
	}
	val := strings.TrimSpace(scope.String)
	if !scope.Valid || val == "" {
		return "", diagnostics.ErrDiagnosticsNotInitialized
	}
	if len(val) != 36 {
		return "", diagnostics.ErrInvalidPanelScope
	}
	if _, err := diagnostics.ComputeDiagnosticID(val, 1); err != nil {
		return "", diagnostics.ErrInvalidPanelScope
	}

	s.diagnosticsScopeMu.Lock()
	s.diagnosticsScope = val
	s.diagnosticsScopeMu.Unlock()

	return val, nil
}

// diagnosticsScopeForSync returns the panel_scope for node user synchronization.
// Any failure (not initialized, invalid, or transient DB error) degrades to "no diagnostic metadata"
// so business proxy synchronization is never blocked by the diagnostics subsystem (design: pause
// customer diagnostics output, keep proxy running). Callers with a logger should use GetDiagnosticsScope
// directly to report non-ErrDiagnosticsNotInitialized failures.
func (s *Store) diagnosticsScopeForSync(ctx context.Context) string {
	scope, err := s.GetDiagnosticsScope(ctx)
	if err != nil {
		return ""
	}
	return scope
}

// attachDiagnosticIDs fills DiagnosticID for runtime users when panel_scope is initialized.
func (s *Store) attachDiagnosticIDs(ctx context.Context, users []RuntimeUser) {
	if len(users) == 0 {
		return
	}
	panelScope := s.diagnosticsScopeForSync(ctx)
	if panelScope == "" {
		return
	}
	for i := range users {
		if diagID, err := diagnostics.ComputeDiagnosticID(panelScope, users[i].ID); err == nil {
			users[i].DiagnosticID = &diagID
		}
	}
}

// ResolveDiagnosticsCustomerByOrder resolves an order to its actual subscriber customer identity.
func (s *Store) ResolveDiagnosticsCustomerByOrder(ctx context.Context, input DiagnosticsOrderLookupInput) (DiagnosticsCustomerIdentity, error) {
	hasTradeNo := input.TradeNo != nil
	hasOrderID := input.OrderID != nil

	if (hasTradeNo && hasOrderID) || (!hasTradeNo && !hasOrderID) {
		return DiagnosticsCustomerIdentity{}, diagnostics.ErrInvalidLookupInput
	}

	panelScope, err := s.GetDiagnosticsScope(ctx)
	if err != nil {
		return DiagnosticsCustomerIdentity{}, err
	}

	query := `
		SELECT 
			o.id,
			o.trade_no,
			o.status,
			o.type,
			o.user_id,
			o.distributor_order_id,
			COALESCE(p.name, ''),
			u.email,
			ds.id,
			ds.subscriber_user_id,
			COALESCE(sub.email, ''),
			COALESCE(ds.customer_name, '')
		FROM orders o
		JOIN users u ON u.id = o.user_id
		LEFT JOIN plans p ON p.id = o.plan_id
		LEFT JOIN distributor_subscriptions ds ON ds.id = o.distributor_order_id
		LEFT JOIN users sub ON sub.id = ds.subscriber_user_id
	`

	var row *sql.Row
	if hasOrderID {
		if *input.OrderID <= 0 {
			return DiagnosticsCustomerIdentity{}, diagnostics.ErrInvalidLookupInput
		}
		row = s.db.QueryRowContext(ctx, query+` WHERE o.id = ?`, *input.OrderID)
	} else {
		tradeNo := strings.TrimSpace(*input.TradeNo)
		if len(tradeNo) < 1 || len(tradeNo) > 36 {
			return DiagnosticsCustomerIdentity{}, diagnostics.ErrInvalidLookupInput
		}
		row = s.db.QueryRowContext(ctx, query+` WHERE o.trade_no = ?`, tradeNo)
	}

	var (
		orderID            int64
		tradeNo            string
		orderStatus        int
		orderTypeNum       int
		orderUserID        int64
		distributorOrderID sql.NullInt64
		planName           string
		userEmail          string
		dsID               sql.NullInt64
		dsSubscriberUserID sql.NullInt64
		subscriberEmail    string
		dsCustomerName     string
	)

	err = row.Scan(
		&orderID,
		&tradeNo,
		&orderStatus,
		&orderTypeNum,
		&orderUserID,
		&distributorOrderID,
		&planName,
		&userEmail,
		&dsID,
		&dsSubscriberUserID,
		&subscriberEmail,
		&dsCustomerName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DiagnosticsCustomerIdentity{}, diagnostics.ErrOrderNotFound
	}
	if err != nil {
		return DiagnosticsCustomerIdentity{}, fmt.Errorf("lookup order for diagnostics: %w", err)
	}

	var subscriberUserID int64
	var customerDisplayName string
	var orderType string

	if distributorOrderID.Valid && distributorOrderID.Int64 > 0 {
		// Distributor Order: subscriber must be present; NEVER fall back to distributor user
		if !dsID.Valid || !dsSubscriberUserID.Valid || dsSubscriberUserID.Int64 <= 0 || subscriberEmail == "" {
			return DiagnosticsCustomerIdentity{}, diagnostics.ErrSubscriptionUserMissing
		}

		subscriberUserID = dsSubscriberUserID.Int64
		if dsCustomerName != "" {
			customerDisplayName = dsCustomerName
		} else {
			customerDisplayName = subscriberEmail
		}

		if orderTypeNum == 1 {
			orderType = "distributor_initial"
		} else {
			orderType = "distributor_renewal"
		}
	} else {
		// Direct Order: the order user is the subscriber
		subscriberUserID = orderUserID
		customerDisplayName = userEmail
		orderType = "direct"
	}

	diagID, err := diagnostics.ComputeDiagnosticID(panelScope, subscriberUserID)
	if err != nil {
		return DiagnosticsCustomerIdentity{}, err
	}

	return DiagnosticsCustomerIdentity{
		OrderID:             orderID,
		TradeNo:             tradeNo,
		OrderStatus:         orderStatus,
		OrderType:           orderType,
		CustomerDisplayName: customerDisplayName,
		SubscriptionName:    planName,
		DiagnosticID:        diagID,
		SubscriberUserID:    subscriberUserID,
		PanelScope:          panelScope,
	}, nil
}

// GetDiagnosticsServerConfig retrieves the server-side configuration.
func (s *Store) GetDiagnosticsServerConfig(ctx context.Context) (DiagnosticsServerConfig, error) {
	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return DiagnosticsServerConfig{}, err
	}

	var (
		scope             sql.NullString
		endpoint          string
		username          string
		passwordCipher    []byte
		secretCipher      []byte
		storageGeneration int64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT panel_scope, query_endpoint, query_username, query_password_cipher, cursor_signing_secret_cipher, storage_generation
		FROM diagnostics_settings WHERE id = 1
	`).Scan(&scope, &endpoint, &username, &passwordCipher, &secretCipher, &storageGeneration)
	if err != nil {
		return DiagnosticsServerConfig{}, fmt.Errorf("get diagnostics server config: %w", err)
	}

	return DiagnosticsServerConfig{
		PanelScope:                scope.String,
		QueryEndpoint:             endpoint,
		QueryUsername:             username,
		QueryPasswordCipher:       passwordCipher,
		CursorSigningSecretCipher: secretCipher,
		StorageGeneration:         storageGeneration,
	}, nil
}

// UpdateDiagnosticsServerConfig updates endpoint, credentials, and generation without altering panel_scope.
func (s *Store) UpdateDiagnosticsServerConfig(
	ctx context.Context,
	endpoint, username string,
	passwordCipher, cursorSigningSecretCipher []byte,
	storageGeneration int64,
	now time.Time,
) error {
	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return err
	}
	if storageGeneration <= 0 {
		return diagnostics.ErrInvalidLookupInput
	}

	defer s.lockWrite()()
	_, err := s.db.ExecContext(ctx, `
		UPDATE diagnostics_settings
		SET query_endpoint = ?, query_username = ?, query_password_cipher = ?,
		    cursor_signing_secret_cipher = ?, storage_generation = ?, updated_at = ?
		WHERE id = 1
	`, endpoint, username, passwordCipher, cursorSigningSecretCipher, storageGeneration, now.Unix())
	if err != nil {
		return fmt.Errorf("update diagnostics server config: %w", err)
	}
	return nil
}

// SaveNodeDiagnosticsCapability saves or updates the diagnostics capability reported by a node.
func (s *Store) SaveNodeDiagnosticsCapability(ctx context.Context, cap NodeDiagnosticsCapability) error {
	if cap.NodeID <= 0 {
		return ErrInvalidInput
	}
	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return err
	}

	capJSON, err := json.Marshal(cap.Capabilities)
	if err != nil {
		return fmt.Errorf("marshal capabilities: %w", err)
	}

	defer s.lockWrite()()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO node_diagnostics_capabilities (node_id, runtime_id, schema_version, kernel, capabilities, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			runtime_id = excluded.runtime_id,
			schema_version = excluded.schema_version,
			kernel = excluded.kernel,
			capabilities = excluded.capabilities,
			updated_at = excluded.updated_at
	`, cap.NodeID, cap.RuntimeID, cap.SchemaVersion, cap.Kernel, string(capJSON), cap.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save node diagnostics capability: %w", err)
	}
	return nil
}

// GetNodeDiagnosticsCapability retrieves the recorded diagnostics capability for a node.
func (s *Store) GetNodeDiagnosticsCapability(ctx context.Context, nodeID int64) (NodeDiagnosticsCapability, bool, error) {
	if nodeID <= 0 {
		return NodeDiagnosticsCapability{}, false, ErrInvalidInput
	}
	if err := s.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		return NodeDiagnosticsCapability{}, false, err
	}

	var (
		runtimeID     string
		schemaVersion int
		kernel        string
		capabilities  string
		updatedAt     int64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT runtime_id, schema_version, kernel, capabilities, updated_at
		FROM node_diagnostics_capabilities
		WHERE node_id = ?
	`, nodeID).Scan(&runtimeID, &schemaVersion, &kernel, &capabilities, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NodeDiagnosticsCapability{NodeID: nodeID}, false, nil
	}
	if err != nil {
		return NodeDiagnosticsCapability{}, false, fmt.Errorf("query node diagnostics capability: %w", err)
	}

	var caps []string
	if err := json.Unmarshal([]byte(capabilities), &caps); err != nil {
		caps = []string{}
	}

	return NodeDiagnosticsCapability{
		NodeID:        nodeID,
		RuntimeID:     runtimeID,
		SchemaVersion: schemaVersion,
		Kernel:        kernel,
		Capabilities:  caps,
		UpdatedAt:     time.Unix(updatedAt, 0),
	}, true, nil
}
