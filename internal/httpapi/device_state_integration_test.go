package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/devicestate"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestTIMENODE006HTTPReportUsesRedisAuthorityAndTrailingDatabaseFlush(t *testing.T) {
	rawURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("XBOARD_TEST_REDIS_URL is required for Redis device-state integration tests")
	}
	prefix := "xboard-go:test-http-device:" + uuid.NewString() + ":"
	var clockMu sync.Mutex
	current := time.Now().UTC()
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return current
	}
	setNow := func(value time.Time) {
		clockMu.Lock()
		current = value
		clockMu.Unlock()
	}
	var deviceState *devicestate.RedisService
	api, database := newTestAPIWithAllOptionsAndModifier(t, nil, true, nil, nil, false, nil, nil, func(dependencies *Dependencies) {
		dependencies.Now = now
		service, err := devicestate.NewRedis(context.Background(), devicestate.Options{
			URL: rawURL, Prefix: prefix, DatabaseThrottle: 80 * time.Millisecond,
			WriteSummaries: func(ctx context.Context, summaries []devicestate.Summary) error {
				values := make([]store.UserDeviceSummary, len(summaries))
				for index, summary := range summaries {
					values[index] = store.UserDeviceSummary{
						UserID: summary.UserID, OnlineCount: summary.OnlineCount, ObservedAt: summary.ObservedAt,
					}
				}
				return dependencies.Store.UpdateUserDeviceSummaries(ctx, values)
			},
		})
		if err != nil {
			t.Fatalf("devicestate.NewRedis() error = %v", err)
		}
		deviceState = service
		dependencies.DeviceState = service
		dependencies.WebSocketEnabled = true
		dependencies.AllowedOrigins = []string{"https://panel.example.test"}
	})
	t.Cleanup(func() {
		_ = deviceState.Close()
		removeTIMENODE006RedisKeys(t, rawURL, prefix)
	})

	ctx := context.Background()
	started := now()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "redis-device-machine", IsActive: true}, started)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "redis-device-node", Type: "vless", Host: "redis-device.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, started); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "redis-device-user@example.test", PasswordHash: "test-password-hash",
		UUID: "fe8e5b3e-c5c7-4cc1-a948-bfc776c66e61", GroupID: 7, TransferEnable: 1_000_000, DeviceLimit: 3,
	}, started)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, started)
	if err != nil {
		t.Fatal(err)
	}

	reportID := "0ecf13c1-e069-460a-a1bc-e54867394491"
	firstBody := fmt.Sprintf(`{
		"machine_id":%d,"node_id":%d,"report_id":%q,
		"traffic":{"%d":[10,20]},
		"alive":{"%d":["192.0.2.70:443","[2001:db8::70]:8443"]}
	}`, machine.ID, node.ID, reportID, user.ID, user.ID)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, firstBody); response.Code != http.StatusOK {
		t.Fatalf("first report status=%d body=%s", response.Code, response.Body)
	}
	devices, err := deviceState.ListUserDevices(ctx, []int64{user.ID}, now())
	if err != nil || !reflect.DeepEqual(devices[user.ID], []string{"192.0.2.70", "2001:db8::70"}) {
		t.Fatalf("first Redis devices = (%#v, %v)", devices, err)
	}
	account, err := database.GetAdminUser(ctx, user.ID)
	if err != nil || account.OnlineCount != 2 {
		t.Fatalf("first database summary = %d, err=%v; want 2", account.OnlineCount, err)
	}
	sqliteState, err := database.ListUserDevices(ctx, []int64{user.ID}, now())
	if err != nil || len(sqliteState[user.ID]) != 0 {
		t.Fatalf("external state leaked to node_device_ips: state=%#v err=%v", sqliteState, err)
	}

	setNow(started.Add(10 * time.Millisecond))
	secondBody := fmt.Sprintf(`{
		"machine_id":%d,"node_id":%d,"report_id":%q,
		"traffic":{"%d":[10,20]},"alive":{"%d":["198.51.100.70"]}
	}`, machine.ID, node.ID, reportID, user.ID, user.ID)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, secondBody); response.Code != http.StatusOK {
		t.Fatalf("retried report status=%d body=%s", response.Code, response.Body)
	}
	devices, err = deviceState.ListUserDevices(ctx, []int64{user.ID}, now())
	if err != nil || !reflect.DeepEqual(devices[user.ID], []string{"198.51.100.70"}) {
		t.Fatalf("retried Redis devices = (%#v, %v)", devices, err)
	}
	account, err = database.GetAdminUser(ctx, user.ID)
	if err != nil || account.OnlineCount != 2 {
		t.Fatalf("throttled database summary = %d, err=%v; want previous 2", account.OnlineCount, err)
	}
	alivePath := fmt.Sprintf("/api/v2/server/alivelist?machine_id=%d&node_id=%d", machine.ID, node.ID)
	alive := agentRequest(api, http.MethodGet, alivePath, credential.Token, "")
	if alive.Code != http.StatusOK || !legacyBodyContainsAll(alive.Body.String(), fmt.Sprintf(`"%d"`, user.ID), "198.51.100.70") {
		t.Fatalf("Redis alivelist status=%d body=%s", alive.Code, alive.Body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		setNow(time.Now().UTC())
		_, flushErr := deviceState.FlushPending(ctx, now(), devicestate.DefaultFlushLimit)
		if flushErr != nil {
			t.Fatal(flushErr)
		}
		account, err = database.GetAdminUser(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if account.OnlineCount == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("HTTP report trailing database summary did not flush")
		}
		time.Sleep(10 * time.Millisecond)
	}
	account, err = database.GetAdminUser(ctx, user.ID)
	if err != nil || account.OnlineCount != 1 {
		t.Fatalf("trailing database summary = %d, err=%v; want 1", account.OnlineCount, err)
	}
	traffic, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || traffic.Upload != 10 || traffic.Download != 20 {
		t.Fatalf("retried report traffic = (%#v, %v), want exactly once", traffic, err)
	}
	account, err = database.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	admin := loginAdmin(t, api)
	reset := admin.request(t, api, http.MethodPost,
		fmt.Sprintf("/api/v1/admin/users/%d/subscription-security/reset", user.ID),
		fmt.Sprintf(`{"revision":%d}`, account.Revision))
	if reset.Code != http.StatusOK {
		t.Fatalf("subscription-security reset status=%d body=%s", reset.Code, reset.Body)
	}
	devices, err = deviceState.ListUserDevices(ctx, []int64{user.ID}, now())
	if err != nil || len(devices[user.ID]) != 0 {
		t.Fatalf("access mutation retained Redis device state: (%#v, %v)", devices, err)
	}

	server := httptest.NewServer(api)
	connection := dialMachineWebSocket(t, server.URL, machine.ID, credential.Token, "")
	assertInitialMachineSync(t, connection, machine.ID, node.ID, user.ID)
	if err := connection.WriteJSON(map[string]any{
		"event": "report.devices",
		"data": map[string]any{
			"node_id": node.ID,
			"devices": map[string]any{fmt.Sprint(user.ID): []string{"203.0.113.70:443"}},
		},
	}); err != nil {
		t.Fatalf("write WebSocket device report: %v", err)
	}
	if event := readWSEvent(t, connection); event.Event != "sync.devices" {
		t.Fatalf("WebSocket device report event=%q, want sync.devices", event.Event)
	}
	waitFor(t, 2*time.Second, func() bool {
		currentDevices, listErr := deviceState.ListUserDevices(ctx, []int64{user.ID}, now())
		return listErr == nil && reflect.DeepEqual(currentDevices[user.ID], []string{"203.0.113.70"})
	})
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		currentDevices, listErr := deviceState.ListUserDevices(ctx, []int64{user.ID}, now())
		return listErr == nil && len(currentDevices[user.ID]) == 0
	})
	server.Close()

	if err := deviceState.Close(); err != nil {
		t.Fatal(err)
	}
	unavailable := agentRequest(api, http.MethodGet, alivePath, credential.Token, "")
	if unavailable.Code != http.StatusInternalServerError || unavailable.Body.String() == `{"alive":{}}` {
		t.Fatalf("Redis outage was presented as empty state: status=%d body=%s", unavailable.Code, unavailable.Body)
	}
}

func removeTIMENODE006RedisKeys(t *testing.T, rawURL, prefix string) {
	t.Helper()
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Errorf("parse Redis cleanup URL: %v", err)
		return
	}
	client := redis.NewClient(options)
	defer client.Close()
	var cursor uint64
	for {
		keys, next, scanErr := client.Scan(context.Background(), cursor, prefix+"*", 500).Result()
		if scanErr != nil {
			t.Errorf("scan Redis cleanup keys: %v", scanErr)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(context.Background(), keys...).Err(); err != nil {
				t.Errorf("delete Redis cleanup keys: %v", err)
				return
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}
