package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestXboardNodeRuntimeConfigContract(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "runtime-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "runtime-vless", Type: "vless", Host: "runtime.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatalf("ExchangeEnrollment() error = %v", err)
	}

	admin := loginAdmin(t, api)
	runtimePath := fmt.Sprintf("/api/v1/admin/nodes/%d/runtime", node.ID)
	configured := admin.request(t, api, http.MethodPut, runtimePath, `{
		"rate":1.5,
		"group_ids":[7],
		"config":{
			"protocol":"vless",
			"listen_ip":"0.0.0.0",
			"server_port":443,
			"network":"tcp",
			"networkSettings":{},
			"tls":0,
			"flow":"",
			"decryption":"none"
		}
	}`)
	if configured.Code != http.StatusOK {
		t.Fatalf("configure runtime status = %d, want %d; body=%s", configured.Code, http.StatusOK, configured.Body)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "runtime-user@example.test", PasswordHash: "test-password-hash",
		UUID: "89b8625e-41f9-4cc8-ae2a-df533cff9bcf", GroupID: 7,
		TransferEnable: 1_000_000, SpeedLimit: 100, DeviceLimit: 2,
	}, now)
	if err != nil {
		t.Fatalf("CreateRuntimeUser() error = %v", err)
	}

	configPath := fmt.Sprintf("/api/v2/server/config?machine_id=%d&node_id=%d", machine.ID, node.ID)
	first := agentRequest(api, http.MethodGet, configPath, credential.Token, "")
	if first.Code != http.StatusOK {
		t.Fatalf("config status = %d, want %d; body=%s", first.Code, http.StatusOK, first.Body)
	}
	if got := first.Header().Get("ETag"); got == "" {
		t.Fatal("config response is missing ETag")
	}

	secondRequest := httptest.NewRequest(http.MethodGet, configPath, nil)
	secondRequest.Header.Set("Authorization", "Bearer "+credential.Token)
	secondRequest.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := httptest.NewRecorder()
	api.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusNotModified {
		t.Fatalf("unchanged config status = %d, want %d; body=%s", second.Code, http.StatusNotModified, second.Body)
	}

	usersPath := fmt.Sprintf("/api/v2/server/user?machine_id=%d&node_id=%d", machine.ID, node.ID)
	users := agentRequest(api, http.MethodGet, usersPath, credential.Token, "")
	if users.Code != http.StatusOK {
		t.Fatalf("users status = %d, want %d; body=%s", users.Code, http.StatusOK, users.Body)
	}
	var usersPayload struct {
		Users []store.RuntimeUser `json:"users"`
	}
	decodeResponse(t, users, &usersPayload)
	if len(usersPayload.Users) != 1 || usersPayload.Users[0].ID != user.ID || usersPayload.Users[0].UUID == "" {
		t.Fatalf("unexpected runtime users: %#v", usersPayload.Users)
	}
	foreignUser, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "foreign-runtime-user@example.test", PasswordHash: "test-password-hash",
		UUID: "1d23e04c-6ae5-4d4e-ad54-bdc100aa70f2", GroupID: 8, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	reportBody := fmt.Sprintf(`{
		"machine_id":%d,
		"node_id":%d,
		"report_id":"f0402358-4b0f-4f9b-92da-e6a9011001d4",
		"traffic":{"%d":[10,20]},
		"alive":{"%d":["192.0.2.10","[2001:db8::10]:443"]},
		"online":{"%d":2},
		"status":{"cpu":20,"mem":{"total":100,"used":20},"swap":{"total":0,"used":0},"disk":{"total":1000,"used":100}},
		"metrics":{"active_connections":2,"cpu_per_core":[10,20]}
	}`, machine.ID, node.ID, user.ID, user.ID, user.ID)
	for attempt := 1; attempt <= 2; attempt++ {
		report := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, reportBody)
		if report.Code != http.StatusOK {
			t.Fatalf("report attempt %d status = %d, want %d; body=%s", attempt, report.Code, http.StatusOK, report.Body)
		}
	}
	uppercaseReportID := strings.Replace(reportBody,
		"f0402358-4b0f-4f9b-92da-e6a9011001d4",
		"F0402358-4B0F-4F9B-92DA-E6A9011001D4", 1)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, uppercaseReportID); response.Code != http.StatusOK {
		t.Fatalf("uppercase report_id retry status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	userTraffic, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || userTraffic.Upload != 15 || userTraffic.Download != 30 {
		t.Fatalf("idempotent user traffic = %#v, err=%v", userTraffic, err)
	}
	missingReportID := fmt.Sprintf(`{"machine_id":%d,"node_id":%d,"traffic":{"%d":[1,1]}}`, machine.ID, node.ID, user.ID)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, missingReportID); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing report_id status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}
	// The dynamic user ID makes a direct string replacement clearer and keeps
	// every non-traffic field identical to the accepted request.
	tampered := strings.Replace(reportBody,
		fmt.Sprintf(`"%d":[10,20]`, user.ID),
		fmt.Sprintf(`"%d":[11,20]`, user.ID), 1)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, tampered); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered report status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}
	foreignReport := fmt.Sprintf(`{
		"machine_id":%d,"node_id":%d,
		"report_id":"7529534e-2c3e-44ab-9140-cd71890dfa8c",
		"traffic":{"%d":[10,20]},"alive":{"%d":["192.0.2.80"]}
	}`, machine.ID, node.ID, foreignUser.ID, foreignUser.ID)
	if response := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token, foreignReport); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-group report status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body)
	}
	foreignTraffic, err := database.GetRuntimeUserTraffic(ctx, foreignUser.ID)
	if err != nil || foreignTraffic.Upload != 0 || foreignTraffic.Download != 0 {
		t.Fatalf("foreign user traffic = %#v, err=%v", foreignTraffic, err)
	}
	oversized := agentRequest(api, http.MethodPost, "/api/v2/server/report", credential.Token,
		fmt.Sprintf(`{"machine_id":%d,"node_id":%d,"metrics":{"padding":"%s"}}`, machine.ID, node.ID, strings.Repeat("x", maxReportBody)))
	if oversized.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized report status = %d, want %d; body=%s", oversized.Code, http.StatusRequestEntityTooLarge, oversized.Body)
	}
}

func TestXboardNodeRuntimeEndpointsEnforceNodeOwnershipAndConfiguration(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "owner", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(ctx, machine.ID, enrollment.Code, now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "unconfigured", Type: "vless", Host: "unconfigured.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v2/server/config?machine_id=%d&node_id=%d", machine.ID, node.ID)
	unconfigured := agentRequest(api, http.MethodGet, path, credential.Token, "")
	if unconfigured.Code != http.StatusConflict {
		t.Fatalf("unconfigured status = %d, want %d; body=%s", unconfigured.Code, http.StatusConflict, unconfigured.Body)
	}

	otherMachine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "other", IsActive: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	otherNode, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "other-node", Type: "vless", Host: "other.example.test", Port: "443",
		Show: true, Enabled: true, MachineID: &otherMachine.ID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	crossPath := fmt.Sprintf("/api/v2/server/user?machine_id=%d&node_id=%d", machine.ID, otherNode.ID)
	cross := agentRequest(api, http.MethodGet, crossPath, credential.Token, "")
	if cross.Code != http.StatusForbidden {
		t.Fatalf("cross-machine status = %d, want %d; body=%s", cross.Code, http.StatusForbidden, cross.Body)
	}
}

func TestValidateNodeReportCanonicalizesIPv4MappedAddresses(t *testing.T) {
	report, err := validateNodeReport(nodeReportPayload{Alive: map[string][]string{
		"1": {"192.0.2.9", "::ffff:192.0.2.9", "[::ffff:192.0.2.9]:443"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Alive[1]) != 1 || report.Alive[1][0] != "192.0.2.9" {
		t.Fatalf("canonical alive addresses = %#v", report.Alive[1])
	}
}
