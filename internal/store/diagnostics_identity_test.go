package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
)

func TestInitializeDiagnosticsScope(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// 1. Before initialization, GetDiagnosticsScope must return ErrDiagnosticsNotInitialized
	_, err := database.GetDiagnosticsScope(ctx)
	if !errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized) {
		t.Fatalf("expected ErrDiagnosticsNotInitialized, got %v", err)
	}

	// 2. Invalid format candidate is rejected
	_, _, err = database.InitializeDiagnosticsScope(ctx, "invalid-not-a-uuid", now)
	if !errors.Is(err, diagnostics.ErrInvalidPanelScope) {
		t.Fatalf("expected ErrInvalidPanelScope, got %v", err)
	}

	// 3. Concurrent initialization must be idempotent and produce the identical scope
	candidate := "00000000-0000-4000-8000-000000000001"
	const concurrency = 8
	var wg sync.WaitGroup
	scopes := make([]string, concurrency)
	createdFlags := make([]bool, concurrency)
	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s, created, e := database.InitializeDiagnosticsScope(ctx, candidate, now)
			scopes[idx] = s
			createdFlags[idx] = created
			errs[idx] = e
		}(i)
	}
	wg.Wait()

	var createdCount int
	for i := 0; i < concurrency; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent init %d failed: %v", i, errs[i])
		}
		if scopes[i] != candidate {
			t.Errorf("scope %d mismatch: got %q, want %q", i, scopes[i], candidate)
		}
		if createdFlags[i] {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Errorf("expected exactly 1 created=true flag, got %d", createdCount)
	}

	// 4. Subsequent initialization with different candidate returns the original scope and created=false
	scope, created, err := database.InitializeDiagnosticsScope(ctx, "00000000-0000-4000-8000-000000000099", now)
	if err != nil {
		t.Fatalf("subsequent init failed: %v", err)
	}
	if created {
		t.Errorf("expected created=false for subsequent call")
	}
	if scope != candidate {
		t.Errorf("scope overwritten: got %q, want %q", scope, candidate)
	}

	// 5. GetDiagnosticsScope now returns initialized scope
	activeScope, err := database.GetDiagnosticsScope(ctx)
	if err != nil {
		t.Fatalf("GetDiagnosticsScope failed: %v", err)
	}
	if activeScope != candidate {
		t.Errorf("activeScope: got %q, want %q", activeScope, candidate)
	}
}

func TestResolveDiagnosticsCustomerByOrder(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	// Set known scope matching contract-fixtures.json
	scope := "00000000-0000-4000-8000-000000000001"
	_, _, err := database.InitializeDiagnosticsScope(ctx, scope, now)
	if err != nil {
		t.Fatalf("init scope failed: %v", err)
	}

	// Create plan
	plan, _ := createDistributorFixture(t, database, now)
	planID := plan.ID

	// Create users:
	// user 1001: standard direct customer
	// user 50: distributor
	// user 2002: subscriber under distributor
	_, err = database.db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, account_kind, is_distributor, distributor_name, subscription_token, created_at, updated_at)
		VALUES
			(1001, 'direct_customer@example.com', 'hash', 'human', 0, NULL, '11111111111111111111111111111111', 100, 100),
			(50,   'distributor@example.com',     'hash', 'human', 1, 'Distributor Partner', '22222222222222222222222222222222', 100, 100),
			(2002, 'subscriber@example.com',      'hash', 'internal_subscription', 0, NULL, '33333333333333333333333333333333', 100, 100)
	`)
	if err != nil {
		t.Fatalf("insert users: %v", err)
	}

	// Create orders matching contract-fixtures.json:
	// Order 9001: Direct order by user 1001
	// Order 9002: Distributor initial order by distributor 50, subscriber 2002
	// Order 9003: Distributor renewal order by distributor 50, subscriber 2002
	// Order 9004: Broken distributor order (missing subscriber relation)
	_, err = database.db.ExecContext(ctx, `
		INSERT INTO orders (id, user_id, plan_id, period, trade_no, original_amount, total_amount, type, status, created_at, updated_at)
		VALUES
			(9001, 1001, ?, 'monthly', '2026101800010000000000001', 10000, 10000, 1, 3, 100, 100),
			(9002, 50,   ?, 'monthly', '2026101800010000000000002', 10000, 10000, 1, 3, 100, 100)
	`, planID, planID)
	if err != nil {
		t.Fatalf("insert initial orders: %v", err)
	}

	// Create distributor subscription (relies on order 9002 being type=1 status=3)
	_, err = database.db.ExecContext(ctx, `
		INSERT INTO distributor_subscriptions (id, original_order_id, distributor_user_id, subscriber_user_id, customer_name, claim_token_hash, delivery_status, created_at, updated_at)
		VALUES (301, 9002, 50, 2002, 'VIP Subscriber', '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', 1, 100, 100)
	`)
	if err != nil {
		t.Fatalf("insert distributor subscription: %v", err)
	}

	// Link initial order 9002 to subscription 301
	_, err = database.db.ExecContext(ctx, `UPDATE orders SET distributor_order_id = 301 WHERE id = 9002`)
	if err != nil {
		t.Fatalf("link order 9002: %v", err)
	}

	// Distributor renewal order 9003
	_, err = database.db.ExecContext(ctx, `
		INSERT INTO orders (id, user_id, plan_id, period, trade_no, original_amount, total_amount, type, status, distributor_order_id, created_at, updated_at)
		VALUES (9003, 50, ?, 'monthly', '2026101800010000000000003', 10000, 10000, 2, 3, 301, 100, 100)
	`, planID)
	if err != nil {
		t.Fatalf("insert renewal order 9003: %v", err)
	}

	// Insert broken distributor order 9004 by temporarily dropping the insert trigger to simulate corrupt/orphaned reference
	_, err = database.db.ExecContext(ctx, `DROP TRIGGER trg_orders_distributor_insert_guard;`)
	if err != nil {
		t.Fatalf("drop trigger for corrupt fixture: %v", err)
	}
	_, err = database.db.ExecContext(ctx, `
		INSERT INTO orders (id, user_id, plan_id, period, trade_no, original_amount, total_amount, type, status, distributor_order_id, created_at, updated_at)
		VALUES (9004, 50, ?, 'monthly', '2026101800010000000000004', 10000, 10000, 1, 3, 9999, 100, 100)
	`, planID)
	if err != nil {
		t.Fatalf("insert corrupt order 9004: %v", err)
	}

	// 1. Direct Order 9001 lookup by TradeNo
	tradeNo9001 := " 2026101800010000000000001 "
	ident9001, err := database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{TradeNo: &tradeNo9001})
	if err != nil {
		t.Fatalf("Resolve 9001 failed: %v", err)
	}
	if ident9001.OrderID != 9001 || ident9001.SubscriberUserID != 1001 {
		t.Errorf("ident 9001 user mismatch: got order=%d sub=%d", ident9001.OrderID, ident9001.SubscriberUserID)
	}
	if ident9001.OrderType != "direct" {
		t.Errorf("ident 9001 order_type: got %q, want 'direct'", ident9001.OrderType)
	}
	// Expected diagnostic_id from contract-fixtures.json: 66f4f8087e40081074ff37ce
	if ident9001.DiagnosticID != "66f4f8087e40081074ff37ce" {
		t.Errorf("ident 9001 diagnostic_id: got %q, want '66f4f8087e40081074ff37ce'", ident9001.DiagnosticID)
	}

	// 2. Distributor Initial Order 9002 lookup by OrderID
	orderID9002 := int64(9002)
	ident9002, err := database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{OrderID: &orderID9002})
	if err != nil {
		t.Fatalf("Resolve 9002 failed: %v", err)
	}
	if ident9002.SubscriberUserID != 2002 {
		t.Fatalf("CRITICAL: distributor order resolved to user %d instead of subscriber 2002", ident9002.SubscriberUserID)
	}
	if ident9002.OrderType != "distributor_initial" {
		t.Errorf("ident 9002 order_type: got %q, want 'distributor_initial'", ident9002.OrderType)
	}
	if ident9002.CustomerDisplayName != "VIP Subscriber" {
		t.Errorf("ident 9002 customer_display_name: got %q, want 'VIP Subscriber'", ident9002.CustomerDisplayName)
	}
	// Expected diagnostic_id from contract-fixtures.json: d3ea8cbf6fa6d103a2a6ee89
	if ident9002.DiagnosticID != "d3ea8cbf6fa6d103a2a6ee89" {
		t.Errorf("ident 9002 diagnostic_id: got %q, want 'd3ea8cbf6fa6d103a2a6ee89'", ident9002.DiagnosticID)
	}
	// Forbidden distributor diagnostic_id: 9874cfb47ec65522ad4b60ba
	if ident9002.DiagnosticID == "9874cfb47ec65522ad4b60ba" {
		t.Fatalf("CRITICAL: resolved diagnostic_id matches forbidden distributor ID!")
	}

	// 3. Distributor Renewal Order 9003 resolves to the exact same diagnostic_id
	tradeNo9003 := "2026101800010000000000003"
	ident9003, err := database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{TradeNo: &tradeNo9003})
	if err != nil {
		t.Fatalf("Resolve 9003 failed: %v", err)
	}
	if ident9003.DiagnosticID != ident9002.DiagnosticID {
		t.Errorf("same subscriber got different diagnostic_ids: 9002=%q 9003=%q", ident9002.DiagnosticID, ident9003.DiagnosticID)
	}
	if ident9003.OrderType != "distributor_renewal" {
		t.Errorf("ident 9003 order_type: got %q, want 'distributor_renewal'", ident9003.OrderType)
	}

	// 4. Broken distributor relation 9004 must return ErrSubscriptionUserMissing, NEVER fall back to distributor!
	orderID9004 := int64(9004)
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{OrderID: &orderID9004})
	if !errors.Is(err, diagnostics.ErrSubscriptionUserMissing) {
		t.Fatalf("expected ErrSubscriptionUserMissing for broken distributor order, got %v", err)
	}

	// 5. Non-existent order returns ErrOrderNotFound
	orderIDMissing := int64(999999)
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{OrderID: &orderIDMissing})
	if !errors.Is(err, diagnostics.ErrOrderNotFound) {
		t.Fatalf("expected ErrOrderNotFound, got %v", err)
	}

	// 6. Invalid inputs
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{})
	if !errors.Is(err, diagnostics.ErrInvalidLookupInput) {
		t.Fatalf("expected ErrInvalidLookupInput for empty input, got %v", err)
	}
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{OrderID: &orderID9002, TradeNo: &tradeNo9003})
	if !errors.Is(err, diagnostics.ErrInvalidLookupInput) {
		t.Fatalf("expected ErrInvalidLookupInput for dual input, got %v", err)
	}
	zeroID := int64(0)
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{OrderID: &zeroID})
	if !errors.Is(err, diagnostics.ErrInvalidLookupInput) {
		t.Fatalf("expected ErrInvalidLookupInput for order_id=0, got %v", err)
	}
	emptyTradeNo := "   "
	_, err = database.ResolveDiagnosticsCustomerByOrder(ctx, DiagnosticsOrderLookupInput{TradeNo: &emptyTradeNo})
	if !errors.Is(err, diagnostics.ErrInvalidLookupInput) {
		t.Fatalf("expected ErrInvalidLookupInput for whitespace trade_no, got %v", err)
	}
}

func TestDiagnosticsServerConfig(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	scope := "00000000-0000-4000-8000-000000000001"
	_, _, err := database.InitializeDiagnosticsScope(ctx, scope, now)
	if err != nil {
		t.Fatalf("init scope failed: %v", err)
	}

	// Update server config
	err = database.UpdateDiagnosticsServerConfig(
		ctx,
		"http://127.0.0.1:9428",
		"vl_viewer",
		[]byte("cipher_pw"),
		[]byte("cipher_cursor_key"),
		2,
		now,
	)
	if err != nil {
		t.Fatalf("update config failed: %v", err)
	}

	cfg, err := database.GetDiagnosticsServerConfig(ctx)
	if err != nil {
		t.Fatalf("get server config failed: %v", err)
	}
	if cfg.PanelScope != scope {
		t.Errorf("panel_scope modified by config update: got %q, want %q", cfg.PanelScope, scope)
	}
	if cfg.QueryEndpoint != "http://127.0.0.1:9428" || cfg.QueryUsername != "vl_viewer" || cfg.StorageGeneration != 2 {
		t.Errorf("config fields mismatch: %+v", cfg)
	}
	if string(cfg.QueryPasswordCipher) != "cipher_pw" || string(cfg.CursorSigningSecretCipher) != "cipher_cursor_key" {
		t.Errorf("ciphers mismatch: %+v", cfg)
	}
}

// TestDiagnosticsScopeLookupIsReadOnlyWhenUninitialized guards the node sync hot path: once the schema
// exists, an uninitialized scope lookup must be a pure read. It must neither need the store write lock nor
// the SQLite RESERVED lock, so it succeeds while another connection holds an IMMEDIATE write transaction.
func TestDiagnosticsScopeLookupIsReadOnlyWhenUninitialized(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()

	if _, err := database.GetDiagnosticsScope(ctx); !errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized) {
		t.Fatalf("warm-up lookup error = %v, want ErrDiagnosticsNotInitialized", err)
	}

	writer, err := database.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = writer.ExecContext(context.Background(), `ROLLBACK`) }()

	unlock := database.lockWrite()
	defer unlock()

	lookupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	for range 50 {
		if _, err := database.GetDiagnosticsScope(lookupCtx); !errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized) {
			t.Fatalf("lookup under concurrent writer error = %v, want ErrDiagnosticsNotInitialized", err)
		}
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("50 uninitialized lookups took %s; hot path must not wait for write locks", elapsed)
	}
}

func TestDiagnosticsScopeLookupSurfacesDatabaseErrors(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if err := database.EnsureDiagnosticsSettingsTable(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := database.GetDiagnosticsScope(ctx)
	if err == nil || errors.Is(err, diagnostics.ErrDiagnosticsNotInitialized) {
		t.Fatalf("closed database lookup error = %v, want a real database error", err)
	}
}

func TestListAllRuntimeNodeTargetsOnlyReturnsRunnableNodes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	runnable, err := database.CreateNode(ctx, CreateNodeInput{Name: "runnable", Type: "vless", Host: "a.example.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, runnable.ID, SaveNodeRuntimeInput{
		RateMicros: 1_000_000, Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	unconfigured, err := database.CreateNode(ctx, CreateNodeInput{Name: "unconfigured", Type: "vless", Host: "b.example.test", Port: "443", Show: true, Enabled: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	// CreateNode backfills a default runtime_config; force the "never configured" state explicitly.
	if _, err := database.db.ExecContext(ctx, `UPDATE nodes SET runtime_config = NULL WHERE id = ?`, unconfigured.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateNode(ctx, CreateNodeInput{Name: "disabled", Type: "vless", Host: "c.example.test", Port: "443", Show: true, Enabled: false}, now); err != nil {
		t.Fatal(err)
	}

	targets, err := database.ListAllRuntimeNodeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].NodeID != runnable.ID || targets[0].MachineID != 0 {
		t.Fatalf("targets = %#v, want only runnable legacy node %d", targets, runnable.ID)
	}
}
