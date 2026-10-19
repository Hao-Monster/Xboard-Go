package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/subscription"
	yaml "go.yaml.in/yaml/v3"
)

const legacyV1ConfigPath = "/api/v1/client/app/getConfig?token="
const legacyTestHWID = "ABCDEFGHIJ123456"

func TestBL08LegacyV1HWIDAndBL09DeliveryLifecycle(t *testing.T) {
	now := fixedNow()
	api, db := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(d *Dependencies) {
		d.Now = func() time.Time { return now }
	})
	plan, dealer := createHTTPDistributorFixture(t, db)
	created := createLegacyAdmissionOrder(t, db, plan, dealer)
	if !created.Subscription.HWIDEnabled || created.Subscription.HWIDLimit != 1 {
		t.Fatalf("fixture HWID settings = %#v", created.Subscription)
	}
	node := createLegacyAdmissionNode(t, db, *plan.GroupID)
	path := legacyV1ConfigPath + created.Subscription.SubscriptionToken
	read := func() store.DistributorSubscription {
		t.Helper()
		got, err := db.GetDistributorOrderByTradeNo(t.Context(), dealer.ID, created.Order.TradeNo, now)
		if err != nil {
			t.Fatal(err)
		}
		return got.Subscription
	}
	for _, hwid := range []string{"", "invalid!"} {
		got := httptestRequest(api, http.MethodGet, path, map[string]string{"x-hwid": hwid})
		if got.Code != http.StatusNotFound || got.Body.Len() != 0 || got.Header().Get("x-hwid-active") != "true" || got.Header().Get("x-hwid-not-supported") != "true" {
			t.Errorf("HWID %q: status=%d headers=%v body bytes=%d", hwid, got.Code, got.Header(), got.Body.Len())
		}
	}
	before := read()
	devices, err := db.ListDistributorHWIDDevices(t.Context(), created.Order.ID, "")
	if err != nil || len(devices) != 0 || before.ClaimedAt != nil || before.ConfigIssuedAt != nil {
		t.Fatalf("rejection changed state: devices=%v subscription=%#v err=%v", devices, before, err)
	}
	first := httptestRequest(api, http.MethodGet, path, map[string]string{"x-hwid": legacyTestHWID, "x-device-model": "QA desktop"})
	assertLegacyNodeConfig(t, first, 1)
	if first.Header().Get("x-hwid-active") != "true" {
		t.Error("missing active HWID header")
	}
	issued := read()
	if issued.ClaimedAt == nil || !issued.ClaimedAt.Equal(now) || issued.ConfigIssuedAt == nil || !issued.ConfigIssuedAt.Equal(now) || issued.DeliveryStatus != store.DistributorDeliveryClaimed {
		t.Fatalf("V1 delivery markers = %#v", issued)
	}
	now = now.Add(time.Minute)
	second := httptestRequest(api, http.MethodGet, path, map[string]string{"x-hwid": "ZYXWVUTSRQ654321"})
	if second.Code != http.StatusNotFound || second.Body.Len() != 0 || second.Header().Get("x-hwid-max-devices-reached") != "true" || second.Header().Get("x-hwid-limit") != "true" {
		t.Fatalf("second device: status=%d headers=%v bytes=%d", second.Code, second.Header(), second.Body.Len())
	}
	assertLegacyNodeConfig(t, httptestRequest(api, http.MethodGet, path, map[string]string{"x-hwid": legacyTestHWID}), 1)
	repeated := read()
	devices, err = db.ListDistributorHWIDDevices(t.Context(), created.Order.ID, "")
	if err != nil || len(devices) != 1 || devices[0].HWID != legacyTestHWID || repeated.ClaimedAt == nil || !repeated.ClaimedAt.Equal(*issued.ClaimedAt) || repeated.ConfigIssuedAt == nil || !repeated.ConfigIssuedAt.Equal(*issued.ConfigIssuedAt) {
		t.Fatalf("repeat delivery changed first markers/devices: %#v devices=%v err=%v", repeated, devices, err)
	}
	for i, id := range []string{"18c9ce5d-15c6-49c1-8e65-fe2d8ccaf732", "8a4645ab-106d-45b2-a441-0d6b8b7cd845"} {
		reportAt := now.Add(time.Duration(i+1) * time.Minute)
		if _, err := db.ApplyNodeReport(t.Context(), store.NodeReportInput{
			MachineID: *node.MachineID, NodeID: node.ID, ReportID: id,
			Traffic: map[int64]store.TrafficUsage{created.Subscription.SubscriberUserID: {Download: 1}}, Now: reportAt,
		}); err != nil {
			t.Fatal(err)
		}
		connected := read()
		if connected.ConnectedAt == nil || !connected.ConnectedAt.Equal(now.Add(time.Minute)) || connected.ConnectedNodeID == nil || *connected.ConnectedNodeID != node.ID || connected.ConnectedNodeName == nil || *connected.ConnectedNodeName != node.Name {
			t.Fatalf("connection %d = %#v", i, connected)
		}
	}
}

func TestBL10LegacyV1SharedAccountQuota(t *testing.T) {
	for _, firstV1 := range []bool{true, false} {
		name := "ordinary_then_v1"
		if firstV1 {
			name = "v1_then_ordinary"
		}
		t.Run(name, func(t *testing.T) {
			db := cloneHTTPAPITestDatabase(t)
			first := createSubscriptionTestAccount(t, db, "QA-quota@example.test", false, nil)
			second := createSubscriptionTestAccount(t, db, "QA-independent@example.test", false, nil)
			s := newLegacyAdmissionServer(t, db, 1)
			if got := requestLegacyAdmission(s, first.SubscriptionToken, firstV1, ""); got.Code != http.StatusOK {
				t.Fatalf("first status=%d", got.Code)
			}
			got := requestLegacyAdmission(s, first.SubscriptionToken, !firstV1, "")
			if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") != "60" || got.Header().Get("Cache-Control") != "no-store, private" || !strings.Contains(got.Body.String(), `"code":"subscription_rate_limited"`) {
				t.Fatalf("shared quota status=%d headers=%v", got.Code, got.Header())
			}
			if got := requestLegacyAdmission(s, second.SubscriptionToken, true, ""); got.Code != http.StatusOK {
				t.Fatalf("independent status=%d", got.Code)
			}
			assertLegacyAdmissionReleased(t, s)
			load := s.runtimeTracker.Snapshot(fixedNow(), time.Minute).Subscription
			if load.RateLimited != 1 || load.Busy != 0 || load.PeakInFlight != 1 {
				t.Fatalf("metrics=%#v", load)
			}
		})
	}
}

func TestBL10LegacyV1BusyAndExcludedBranches(t *testing.T) {
	db := cloneHTTPAPITestDatabase(t)
	account := createSubscriptionTestAccount(t, db, "QA-busy@example.test", false, nil)
	banned := createSubscriptionTestAccount(t, db, "QA-banned@example.test", true, nil)
	user, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "QA-groupless@example.test", PasswordHash: "opaque", TransferEnable: 10 << 30}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	groupless, err := db.GetSubscriptionAccount(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := newLegacyAdmissionServer(t, db, 2)
	s.subscriptionRenderSlots <- struct{}{}
	for i := 0; i < 2; i++ {
		got := requestLegacyAdmission(s, account.SubscriptionToken, true, "")
		if got.Code != http.StatusServiceUnavailable || got.Header().Get("Retry-After") != "1" || !strings.Contains(got.Body.String(), `"code":"subscription_busy"`) {
			t.Fatalf("busy status=%d headers=%v", got.Code, got.Header())
		}
	}
	if got := requestLegacyAdmission(s, account.SubscriptionToken, true, ""); got.Code != http.StatusTooManyRequests {
		t.Fatalf("quota before slot status=%d", got.Code)
	}
	for _, token := range []string{banned.SubscriptionToken, groupless.SubscriptionToken} {
		for i := 0; i < 3; i++ {
			assertLegacyNodeConfig(t, requestLegacyAdmission(s, token, true, ""), 0)
		}
	}
	v2 := httptest.NewRecorder()
	s.legacyClientAppConfigV2(v2, httptest.NewRequest(http.MethodGet, "/api/v2/client/app/getConfig?token="+account.SubscriptionToken, nil))
	if v2.Code != http.StatusOK || !strings.Contains(v2.Body.String(), `"config_hash"`) {
		t.Fatalf("V2 status=%d", v2.Code)
	}
	if len(s.subscriptionRenderSlots) != 1 {
		t.Fatal("borrowed occupied slot")
	}
	<-s.subscriptionRenderSlots
	s.now = func() time.Time { return fixedNow().Add(2 * time.Minute) }
	assertLegacyNodeConfig(t, requestLegacyAdmission(s, account.SubscriptionToken, true, ""), 0)
	assertLegacyAdmissionReleased(t, s)
	load := s.runtimeTracker.Snapshot(s.now(), time.Minute).Subscription
	if load.Busy != 2 || load.RateLimited != 1 || load.PeakInFlight != 1 {
		t.Fatalf("metrics=%#v", load)
	}
}

func TestBL10LegacyV1FailureReleasesResources(t *testing.T) {
	for _, scenario := range []string{"hwid_denied", "hwid_store", "claim", "list", "prepare", "render", "issued"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "QA-legacy-v1.db")
			dsn := sqliteHTTPAPITestDSN(path)
			db, err := store.OpenSQLite(dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := db.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := raw.Close(); err != nil {
					t.Error(err)
				}
			})
			plan, dealer := createHTTPDistributorFixture(t, db)
			created := createLegacyAdmissionOrder(t, db, plan, dealer)
			node := createLegacyAdmissionNode(t, db, *plan.GroupID)
			s := newLegacyAdmissionServer(t, db, 10)
			hwid := legacyTestHWID
			query, restore := "", ""
			wantStatus, wantClaimed := http.StatusInternalServerError, true
			switch scenario {
			case "hwid_denied":
				hwid = ""
				wantStatus = http.StatusNotFound
				wantClaimed = false
			case "hwid_store":
				query = `CREATE TRIGGER qa_fail BEFORE INSERT ON distributor_hwid_devices BEGIN SELECT RAISE(ABORT,'QA HWID failure'); END`
				restore = `DROP TRIGGER qa_fail`
				wantClaimed = false
			case "claim":
				query = `CREATE TRIGGER qa_fail BEFORE UPDATE OF claimed_at ON distributor_subscriptions BEGIN SELECT RAISE(ABORT,'QA claim failure'); END`
				restore = `DROP TRIGGER qa_fail`
				wantClaimed = false
			case "list":
				query = `ALTER TABLE node_protocol_definitions RENAME TO qa_unavailable_protocol_definitions`
				restore = `ALTER TABLE qa_unavailable_protocol_definitions RENAME TO node_protocol_definitions`
			case "prepare":
				query = `UPDATE nodes SET port='invalid'`
				restore = `UPDATE nodes SET port='443'`
			case "render":
				s.legacyAppClashRenderer = nil
			case "issued":
				query = `CREATE TRIGGER qa_fail BEFORE UPDATE OF config_issued_at ON distributor_subscriptions BEGIN SELECT RAISE(ABORT,'QA issue failure'); END`
				restore = `DROP TRIGGER qa_fail`
			}
			if query != "" {
				if _, err := raw.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "prepare" {
				nodes, err := db.ListSubscriptionNodes(t.Context(), *plan.GroupID)
				if err != nil || len(nodes) != 1 || nodes[0].ID != node.ID {
					t.Fatalf("must fail in Prepare, not List: %v %v", nodes, err)
				}
				account, err := db.FindSubscriptionAccount(t.Context(), created.Subscription.SubscriptionToken)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := subscription.PrepareNodes(account, nodes, subscription.PrepareOptions{Now: fixedNow()}); err == nil {
					t.Fatal("invalid port did not fail Prepare")
				}
			}
			got := requestLegacyAdmission(s, created.Subscription.SubscriptionToken, true, hwid)
			if got.Code != wantStatus {
				t.Fatalf("status=%d want=%d body bytes=%d", got.Code, wantStatus, got.Body.Len())
			}
			assertLegacyAdmissionReleased(t, s)
			current, err := db.GetDistributorOrderByTradeNo(t.Context(), dealer.ID, created.Order.TradeNo, fixedNow())
			if err != nil || current.Subscription.ConfigIssuedAt != nil || (current.Subscription.ClaimedAt != nil) != wantClaimed {
				t.Fatalf("failure state=%#v err=%v", current.Subscription, err)
			}
			if restore != "" {
				if _, err := raw.ExecContext(t.Context(), restore); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "render" {
				s.legacyAppClashRenderer, err = subscription.NewLegacyAppClashRenderer("")
				if err != nil {
					t.Fatal(err)
				}
			}
			assertLegacyNodeConfig(t, requestLegacyAdmission(s, created.Subscription.SubscriptionToken, true, legacyTestHWID), 1)
			assertLegacyAdmissionReleased(t, s)
			current, err = db.GetDistributorOrderByTradeNo(t.Context(), dealer.ID, created.Order.TradeNo, fixedNow())
			if err != nil || current.Subscription.ConfigIssuedAt == nil {
				t.Fatalf("recovery did not issue: %#v err=%v", current.Subscription, err)
			}
		})
	}
}

func TestBL10LegacyV1Golden(t *testing.T) {
	api, db := newTestAPI(t)
	user, err := db.CreateRuntimeUser(t.Context(), store.CreateRuntimeUserInput{
		Email: "QA-golden@example.test", PasswordHash: "opaque", GroupID: 3, TransferEnable: 10 << 30,
		UUID: "bb859498-67a8-461f-a451-144c14b37113",
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.GetSubscriptionAccount(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	createLegacyAdmissionNode(t, db, 3)
	got := requestSubscription(api, legacyV1ConfigPath+account.SubscriptionToken)
	assertLegacyNodeConfig(t, got, 1)
	want, err := os.ReadFile("testdata/legacy_v1_admission.golden.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Body.Bytes(), want) {
		t.Fatalf("legacy V1 bytes changed: got %d want %d", got.Body.Len(), len(want))
	}
}

func newLegacyAdmissionServer(t *testing.T, db *store.Store, quota int) *server {
	t.Helper()
	renderer, err := subscription.NewLegacyAppClashRenderer("")
	if err != nil {
		t.Fatal(err)
	}
	return &server{store: db, now: fixedNow, panelURL: "https://panel.example.test", logger: slog.Default(),
		subscriptionFailures: newAttemptLimiter(100, time.Minute), subscriptionRequests: newRequestLimiter(quota, time.Minute),
		subscriptionRenderSlots: make(chan struct{}, 1), runtimeTracker: operations.NewTracker(fixedNow()), legacyAppClashRenderer: renderer}
}

func createLegacyAdmissionOrder(t *testing.T, db *store.Store, plan store.Plan, dealer store.AdminUser) store.DistributorOrder {
	t.Helper()
	got, err := db.CreateDistributorOrder(t.Context(), store.CreateDistributorOrderInput{DistributorUserID: dealer.ID, PlanID: plan.ID, Period: "monthly"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func createLegacyAdmissionNode(t *testing.T, db *store.Store, groupID int64) store.Node {
	t.Helper()
	machine, _, err := db.CreateMachine(t.Context(), store.CreateMachineInput{Name: "QA legacy machine", IsActive: true}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.NewBasicAdminNodeDefinitionInput(store.CreateNodeInput{Type: "shadowsocks", Name: "QA Legacy SS", Host: "edge.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID})
	if err != nil {
		t.Fatal(err)
	}
	input.GroupIDs = []int64{groupID}
	input.ProtocolSettings = json.RawMessage(`{"cipher":"aes-256-gcm","plugin":"","plugin_opts":""}`)
	node, _, err := db.CreateAdminNodeDefinition(t.Context(), input, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	return node.Node
}

func requestLegacyAdmission(s *server, token string, v1 bool, hwid string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, legacyV1ConfigPath+token, nil)
	r.Header.Set("x-hwid", hwid)
	w := httptest.NewRecorder()
	if v1 {
		s.legacyClientAppConfigV1(w, r)
	} else {
		s.clientSubscription(w, r)
	}
	return w
}

func assertLegacyAdmissionReleased(t *testing.T, s *server) {
	t.Helper()
	load := s.runtimeTracker.Snapshot(s.now(), time.Minute).Subscription
	if len(s.subscriptionRenderSlots) != 0 || load.InFlight != 0 || load.PeakInFlight != 1 {
		t.Fatalf("render resources: slots=%d metrics=%#v", len(s.subscriptionRenderSlots), load)
	}
}

func assertLegacyNodeConfig(t *testing.T, got *httptest.ResponseRecorder, count int) {
	t.Helper()
	if got.Code != http.StatusOK || got.Header().Get("Content-Type") != "text/html; charset=utf-8" || got.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatalf("V1 response status=%d headers=%v", got.Code, got.Header())
	}
	for _, header := range []string{"Subscription-Userinfo", "Content-Disposition", "Profile-Update-Interval", "Profile-Web-Page-Url", "profile-title", "x-order-no"} {
		if got.Header().Get(header) != "" {
			t.Fatalf("V1 gained normal subscription header %s", header)
		}
	}
	var body struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Proxies) != count {
		t.Fatalf("proxies=%d want=%d", len(body.Proxies), count)
	}
}
