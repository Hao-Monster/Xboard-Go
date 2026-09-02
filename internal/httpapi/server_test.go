package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/attachments"
	"github.com/Hao-Monster/Xboard-Go/internal/bulkops"
	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	"github.com/Hao-Monster/Xboard-Go/internal/clientcatalog"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/telegrambot"
)

const testAdminPath = "secure-admin-01"

func adminAPIPath(path string) string {
	if strings.HasPrefix(path, "/api/v1/admin/") && !strings.HasPrefix(path, "/api/v1/admin/"+testAdminPath+"/") {
		return "/api/v1/admin/" + testAdminPath + "/" + strings.TrimPrefix(path, "/api/v1/admin/")
	}
	if strings.HasPrefix(path, "/api/v2/admin/") {
		return "/api/v2/" + testAdminPath + "/" + strings.TrimPrefix(path, "/api/v2/admin/")
	}
	return path
}

func newTestRequest(method, path string, body io.Reader) *http.Request {
	return httptest.NewRequest(method, adminAPIPath(path), body)
}

func TestAdminAPIRequiresSessionAndCSRF(t *testing.T) {
	api, _ := newTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, adminAPIPath("/api/v1/admin/machines"), nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	client := loginAdmin(t, api)
	request = httptest.NewRequest(http.MethodPost, adminAPIPath("/api/v1/admin/machines"), strings.NewReader(`{"name":"edge-01","is_active":true}`))
	request.Header.Set("Content-Type", "application/json")
	client.addCookies(request)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, adminAPIPath("/api/v1/admin/machines"), strings.NewReader(`{"name":"edge-01","is_active":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example.test")
	client.addCookies(request)
	request.Header.Set("X-CSRF-Token", client.csrf)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, adminAPIPath("/api/v1/admin/machines"), strings.NewReader(`{"name":"edge-01","is_active":true}`))
	request.Header.Set("Content-Type", "application/json")
	client.addCookies(request)
	request.Header.Set("X-CSRF-Token", client.csrf)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("valid create status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ID             int64  `json:"id"`
			Token          string `json:"token"`
			TokenType      string `json:"token_type"`
			InstallCommand string `json:"install_command"`
		} `json:"data"`
	}
	decodeResponse(t, response, &payload)
	if payload.Status != "success" || payload.Data.ID == 0 || payload.Data.Token == "" || payload.Data.TokenType != "enrollment_code" {
		t.Fatalf("unexpected create payload: %#v", payload)
	}
	if !strings.Contains(payload.Data.InstallCommand, "--enrollment-code") || strings.Contains(payload.Data.InstallCommand, "--token") {
		t.Fatalf("unsafe or incomplete install command: %q", payload.Data.InstallCommand)
	}
	if !strings.Contains(payload.Data.InstallCommand, "v1.14.3") || strings.Contains(payload.Data.InstallCommand, "latest") {
		t.Fatalf("install command must pin the published node release: %q", payload.Data.InstallCommand)
	}
}

func TestAPIMACH002MachineEnrollmentCredentialLifecycleIsOneTimeAndNoStore(t *testing.T) {
	api, database := newTestAPI(t)
	client := loginAdmin(t, api)

	created := client.request(t, api, http.MethodPost, "/api/v1/admin/machines", `{"name":"credential-edge","is_active":true}`)
	if created.Code != http.StatusCreated || created.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create status=%d cache=%q", created.Code, created.Header().Get("Cache-Control"))
	}
	var createdPayload struct {
		Data struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, created, &createdPayload)
	if createdPayload.Data.ID == 0 || createdPayload.Data.Token == "" {
		t.Fatal("create omitted one-time enrollment data")
	}
	machineID := createdPayload.Data.ID
	initialEnrollment := createdPayload.Data.Token

	initialExchange := agentRequest(api, http.MethodPost, "/api/v2/server/machine/enroll", "", fmt.Sprintf(
		`{"machine_id":%d,"enrollment_code":%q}`, machineID, initialEnrollment,
	))
	if initialExchange.Code != http.StatusOK || initialExchange.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("initial exchange status=%d cache=%q", initialExchange.Code, initialExchange.Header().Get("Cache-Control"))
	}
	var initialCredentialPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, initialExchange, &initialCredentialPayload)
	oldCredential := initialCredentialPayload.Data.Token
	if oldCredential == "" || oldCredential == initialEnrollment {
		t.Fatal("initial exchange returned invalid credential data")
	}

	rotation := client.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/machines/%d/enrollments", machineID), `{"revoke_existing":true}`)
	if rotation.Code != http.StatusCreated || rotation.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotation status=%d cache=%q", rotation.Code, rotation.Header().Get("Cache-Control"))
	}
	var rotationPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, rotation, &rotationPayload)
	rotationEnrollment := rotationPayload.Data.Token
	if rotationEnrollment == "" || rotationEnrollment == initialEnrollment || rotationEnrollment == oldCredential {
		t.Fatal("rotation returned invalid one-time enrollment data")
	}
	if _, err := database.AuthenticateMachine(context.Background(), machineID, oldCredential, fixedNow()); err != nil {
		t.Fatalf("old credential was revoked before the replacement enrollment was exchanged: %v", err)
	}

	rotatedExchange := agentRequest(api, http.MethodPost, "/api/v2/server/machine/enroll", "", fmt.Sprintf(
		`{"machine_id":%d,"enrollment_code":%q}`, machineID, rotationEnrollment,
	))
	if rotatedExchange.Code != http.StatusOK || rotatedExchange.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rotated exchange status=%d cache=%q", rotatedExchange.Code, rotatedExchange.Header().Get("Cache-Control"))
	}
	var rotatedCredentialPayload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, rotatedExchange, &rotatedCredentialPayload)
	newCredential := rotatedCredentialPayload.Data.Token
	if newCredential == "" || newCredential == oldCredential || newCredential == rotationEnrollment {
		t.Fatal("rotated exchange returned invalid credential data")
	}

	replayed := agentRequest(api, http.MethodPost, "/api/v2/server/machine/enroll", "", fmt.Sprintf(
		`{"machine_id":%d,"enrollment_code":%q}`, machineID, rotationEnrollment,
	))
	if replayed.Code != http.StatusUnauthorized || replayed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("replayed enrollment status=%d cache=%q", replayed.Code, replayed.Header().Get("Cache-Control"))
	}
	if _, err := database.AuthenticateMachine(context.Background(), machineID, oldCredential, fixedNow()); !errors.Is(err, store.ErrInvalidCredential) {
		t.Fatalf("old credential error after exchange = %v, want ErrInvalidCredential", err)
	}
	if _, err := database.AuthenticateMachine(context.Background(), machineID, newCredential, fixedNow()); err != nil {
		t.Fatalf("new credential rejected after exchange: %v", err)
	}

	for _, response := range []*httptest.ResponseRecorder{
		client.request(t, api, http.MethodGet, "/api/v1/admin/machines", ""),
		client.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/machines/%d", machineID), ""),
	} {
		body := response.Body.String()
		if response.Code != http.StatusOK || strings.Contains(body, initialEnrollment) || strings.Contains(body, rotationEnrollment) || strings.Contains(body, oldCredential) || strings.Contains(body, newCredential) {
			t.Fatalf("ordinary machine response exposed one-time secret: status=%d", response.Code)
		}
	}
}

func TestValidLegacyAdminPath(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"admin":                 true,
		"53815c85":              true,
		"secure_path-01":        true,
		"":                      false,
		"../admin":              false,
		"admin/path":            false,
		"管理":                    false,
		strings.Repeat("a", 65): false,
	}
	for value, expected := range tests {
		if actual := validLegacyAdminPath(value); actual != expected {
			t.Errorf("validLegacyAdminPath(%q) = %t, want %t", value, actual, expected)
		}
	}
}

func TestLoginRateLimitBlocksRepeatedFailures(t *testing.T) {
	api, _ := newTestAPI(t)
	for attempt := 1; attempt <= 6; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"wrong-password"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		api.ServeHTTP(response, request)
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt %d status = %d, want %d; body=%s", attempt, response.Code, want, response.Body)
		}
	}
}

func TestMachineNodesAndDailyScheduleAPI(t *testing.T) {
	api, _ := newTestAPI(t)
	client := loginAdmin(t, api)
	createdNode := client.request(t, api, http.MethodPost, "/api/v1/admin/nodes", `{"name":"SG VLESS","type":"vless","host":"sg.example.test","port":443,"show":true,"enabled":false,"sort":0}`)
	if createdNode.Code != http.StatusCreated {
		t.Fatalf("create node status = %d; body=%s", createdNode.Code, createdNode.Body)
	}
	var nodePayload struct {
		Data store.Node `json:"data"`
	}
	decodeResponse(t, createdNode, &nodePayload)
	node := nodePayload.Data

	create := client.request(t, api, http.MethodPost, "/api/v1/admin/machines", `{"name":"edge-01","is_active":true}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create machine status = %d; body=%s", create.Code, create.Body)
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	decodeResponse(t, create, &created)

	bindPath := fmt.Sprintf("/api/v1/admin/machines/%d/nodes/%d", created.Data.ID, node.ID)
	bind := client.request(t, api, http.MethodPut, bindPath, `{"revision":1}`)
	if bind.Code != http.StatusNoContent {
		t.Fatalf("bind node status = %d; body=%s", bind.Code, bind.Body)
	}

	schedulePath := fmt.Sprintf("/api/v1/admin/nodes/%d/activation-schedule", node.ID)
	saved := client.request(t, api, http.MethodPut, schedulePath, `{"schedule_type":"daily","timezone":"Asia/Singapore","enable_time":"19:00","disable_time":"01:00"}`)
	if saved.Code != http.StatusOK {
		t.Fatalf("save schedule status = %d; body=%s", saved.Code, saved.Body)
	}
	var schedulePayload struct {
		Data struct {
			ServerID          int64  `json:"server_id"`
			ScheduleType      string `json:"schedule_type"`
			Phase             string `json:"phase"`
			NextTargetEnabled bool   `json:"next_target_enabled"`
		} `json:"data"`
	}
	decodeResponse(t, saved, &schedulePayload)
	if schedulePayload.Data.ServerID != node.ID || schedulePayload.Data.ScheduleType != "daily" || schedulePayload.Data.Phase != "active" || schedulePayload.Data.NextTargetEnabled {
		t.Fatalf("unexpected schedule response: %#v", schedulePayload)
	}

	nodes := client.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/machines/%d/nodes", created.Data.ID), "")
	if nodes.Code != http.StatusOK {
		t.Fatalf("list nodes status = %d; body=%s", nodes.Code, nodes.Body)
	}
	var nodesPayload struct {
		Data []store.Node `json:"data"`
	}
	decodeResponse(t, nodes, &nodesPayload)
	if len(nodesPayload.Data) != 1 || !nodesPayload.Data[0].Enabled {
		t.Fatalf("schedule did not immediately enable linked node: %#v", nodesPayload.Data)
	}

	invalid := client.request(t, api, http.MethodPut, schedulePath, `{"schedule_type":"daily","timezone":"Asia/Singapore","enable_time":"19:00","disable_time":"19:00"}`)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("equal boundary status = %d, want %d; body=%s", invalid.Code, http.StatusUnprocessableEntity, invalid.Body)
	}
}

func TestMachineAgentEnrollmentNodesAndStatus(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "agent-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "Agent node", Type: "hysteria", Host: "agent.example.test", Port: "8443", Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000,
		Config:     []byte(`{"protocol":"hysteria","listen_ip":"0.0.0.0","server_port":8443}`),
	}, now); err != nil {
		t.Fatalf("SaveNodeRuntime() error = %v", err)
	}
	if _, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "Disabled agent node", Type: "vless", Host: "disabled.example.test", Port: "443", Show: true, Enabled: false, MachineID: &machine.ID,
	}, now); err != nil {
		t.Fatalf("CreateNode(disabled) error = %v", err)
	}

	enrollBody := fmt.Sprintf(`{"machine_id":%d,"enrollment_code":%q}`, machine.ID, enrollment.Code)
	enrollRequest := httptest.NewRequest(http.MethodPost, "/api/v1/machines/enroll", strings.NewReader(enrollBody))
	enrollRequest.Header.Set("Content-Type", "application/json")
	enrollResponse := httptest.NewRecorder()
	api.ServeHTTP(enrollResponse, enrollRequest)
	if enrollResponse.Code != http.StatusOK {
		t.Fatalf("enroll status = %d; body=%s", enrollResponse.Code, enrollResponse.Body)
	}
	var enrolled struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, enrollResponse, &enrolled)

	nodesRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/machines/%d/nodes", machine.ID), nil)
	nodesRequest.Header.Set("Authorization", "Bearer "+enrolled.Data.Token)
	nodesResponse := httptest.NewRecorder()
	api.ServeHTTP(nodesResponse, nodesRequest)
	if nodesResponse.Code != http.StatusOK {
		t.Fatalf("agent nodes status = %d; body=%s", nodesResponse.Code, nodesResponse.Body)
	}
	var nodesPayload struct {
		Data struct {
			Nodes []struct {
				ID int64 `json:"id"`
			} `json:"nodes"`
		} `json:"data"`
	}
	decodeResponse(t, nodesResponse, &nodesPayload)
	if len(nodesPayload.Data.Nodes) != 1 || nodesPayload.Data.Nodes[0].ID != node.ID {
		t.Fatalf("unexpected agent nodes: %#v", nodesPayload)
	}

	invalidStatus := agentRequest(api, http.MethodPost, fmt.Sprintf("/api/v1/machines/%d/status", machine.ID), enrolled.Data.Token,
		`{"cpu":20,"mem":{"total":100,"used":101},"swap":{"total":0,"used":0},"disk":{"total":1000,"used":100},"net":{"in_speed":1.5,"out_speed":2.5}}`)
	if invalidStatus.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status code = %d, want %d; body=%s", invalidStatus.Code, http.StatusUnprocessableEntity, invalidStatus.Body)
	}

	validStatus := agentRequest(api, http.MethodPost, fmt.Sprintf("/api/v1/machines/%d/status", machine.ID), enrolled.Data.Token,
		`{"cpu":20.5,"mem":{"total":100,"used":80},"swap":{"total":20,"used":5},"disk":{"total":1000,"used":100},"net":{"in_speed":1.5,"out_speed":2.5}}`)
	if validStatus.Code != http.StatusOK {
		t.Fatalf("valid status code = %d; body=%s", validStatus.Code, validStatus.Body)
	}
	updated, err := database.GetMachine(ctx, machine.ID)
	if err != nil || updated.LastSeenAt == nil || string(updated.LoadStatus) == "null" {
		t.Fatalf("machine status was not persisted: machine=%#v err=%v", updated, err)
	}
	history, err := database.ListLoadHistory(ctx, machine.ID, now.Add(-time.Hour), 10)
	if err != nil || len(history) != 1 || history[0].NetworkIn != 1.5 {
		t.Fatalf("load history = %#v, err=%v", history, err)
	}
}

func TestXboardNodeV2MachineContract(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	machine, enrollment, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "contract-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	if len(enrollment.Code) != 48 {
		t.Fatalf("enrollment code length = %d, want 48", len(enrollment.Code))
	}
	enabledNode, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "enabled", Type: "vless", Host: "enabled.example.test", Port: "443", Show: true, Enabled: true, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode(enabled) error = %v", err)
	}
	if _, err := database.SaveNodeRuntime(ctx, enabledNode.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000,
		Config:     []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatalf("SaveNodeRuntime(enabled) error = %v", err)
	}
	disabledNodeRecord, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "disabled", Type: "vless", Host: "disabled.example.test", Port: "8443", Show: true, Enabled: false, MachineID: &machine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode(disabled) error = %v", err)
	}

	enrollBody := fmt.Sprintf(`{"machine_id":%d,"enrollment_code":%q}`, machine.ID, enrollment.Code)
	enrollRequest := httptest.NewRequest(http.MethodPost, "/api/v2/server/machine/enroll", strings.NewReader(enrollBody))
	enrollRequest.Header.Set("Content-Type", "application/json")
	enrollResponse := httptest.NewRecorder()
	api.ServeHTTP(enrollResponse, enrollRequest)
	if enrollResponse.Code != http.StatusOK {
		t.Fatalf("v2 enrollment status = %d; body=%s", enrollResponse.Code, enrollResponse.Body)
	}
	var enrolled struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	decodeResponse(t, enrollResponse, &enrolled)
	if enrolled.Data.Token == "" {
		t.Fatal("v2 enrollment returned an empty credential")
	}

	// v1.13 included the credential in the JSON body as well as the Bearer
	// header. The hardened node omits the body copy; the panel accepts both
	// shapes during staged deployment and authenticates the header.
	bodyOnlyAuth := httptest.NewRequest(http.MethodPost, "/api/v2/server/handshake", strings.NewReader(
		fmt.Sprintf(`{"machine_id":%d,"token":%q}`, machine.ID, enrolled.Data.Token),
	))
	bodyOnlyAuth.Header.Set("Content-Type", "application/json")
	bodyOnlyResponse := httptest.NewRecorder()
	api.ServeHTTP(bodyOnlyResponse, bodyOnlyAuth)
	if bodyOnlyResponse.Code != http.StatusUnauthorized {
		t.Fatalf("body-only machine credential status = %d, want %d", bodyOnlyResponse.Code, http.StatusUnauthorized)
	}
	if bodyOnlyResponse.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("body-only machine credential challenge = %q, want Bearer", bodyOnlyResponse.Header().Get("WWW-Authenticate"))
	}

	handshakeBody := fmt.Sprintf(`{"machine_id":%d,"token":"ignored-legacy-body-value"}`, machine.ID)
	handshake := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", enrolled.Data.Token, handshakeBody)
	if handshake.Code != http.StatusOK {
		t.Fatalf("v2 handshake status = %d; body=%s", handshake.Code, handshake.Body)
	}
	var handshakePayload struct {
		WebSocket struct {
			Enabled bool `json:"enabled"`
		} `json:"websocket"`
		Settings struct {
			PushInterval int `json:"push_interval"`
			PullInterval int `json:"pull_interval"`
		} `json:"settings"`
	}
	decodeResponse(t, handshake, &handshakePayload)
	if handshakePayload.WebSocket.Enabled || handshakePayload.Settings.PushInterval != 60 || handshakePayload.Settings.PullInterval != 60 {
		t.Fatalf("unexpected handshake payload: %#v", handshakePayload)
	}

	nodesBody := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	nodesResponse := agentRequest(api, http.MethodPost, "/api/v2/server/machine/nodes", enrolled.Data.Token, nodesBody)
	if nodesResponse.Code != http.StatusOK {
		t.Fatalf("v2 machine nodes status = %d; body=%s", nodesResponse.Code, nodesResponse.Body)
	}
	var nodesPayload struct {
		Nodes []struct {
			ID int64 `json:"id"`
		} `json:"nodes"`
		BaseConfig struct {
			PushInterval int `json:"push_interval"`
			PullInterval int `json:"pull_interval"`
		} `json:"base_config"`
	}
	decodeResponse(t, nodesResponse, &nodesPayload)
	if len(nodesPayload.Nodes) != 1 || nodesPayload.Nodes[0].ID != enabledNode.ID || nodesPayload.BaseConfig.PullInterval != 60 {
		t.Fatalf("unexpected machine nodes payload: %#v", nodesPayload)
	}

	statusBody := fmt.Sprintf(`{"machine_id":%d,"cpu":73.5,"mem":{"total":1000,"used":700},"swap":{"total":100,"used":10},"disk":{"total":2000,"used":500},"net":{"in_speed":1024,"out_speed":2048}}`, machine.ID)
	statusResponse := agentRequest(api, http.MethodPost, "/api/v2/server/machine/status", enrolled.Data.Token, statusBody)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("v2 machine status = %d; body=%s", statusResponse.Code, statusResponse.Body)
	}
	updated, err := database.GetMachine(ctx, machine.ID)
	if err != nil || !bytes.Contains(updated.LoadStatus, []byte(`"cpu":73.5`)) {
		t.Fatalf("machine status was not persisted: machine=%#v err=%v", updated, err)
	}

	otherMachine, _, err := database.CreateMachine(ctx, store.CreateMachineInput{Name: "other-edge", IsActive: true}, now)
	if err != nil {
		t.Fatalf("CreateMachine(other) error = %v", err)
	}
	otherNode, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "other", Type: "vless", Host: "other.example.test", Port: "443", Show: true, Enabled: true, MachineID: &otherMachine.ID,
	}, now)
	if err != nil {
		t.Fatalf("CreateNode(other) error = %v", err)
	}
	crossNodeBody := fmt.Sprintf(`{"machine_id":%d,"node_id":%d}`, machine.ID, otherNode.ID)
	crossNode := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", enrolled.Data.Token, crossNodeBody)
	if crossNode.Code != http.StatusForbidden {
		t.Fatalf("cross-machine node handshake status = %d, want %d; body=%s", crossNode.Code, http.StatusForbidden, crossNode.Body)
	}
	disabledNodeBody := fmt.Sprintf(`{"machine_id":%d,"node_id":%d}`, machine.ID, disabledNodeRecord.ID)
	disabledNode := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", enrolled.Data.Token, disabledNodeBody)
	if disabledNode.Code != http.StatusForbidden {
		t.Fatalf("disabled node handshake status = %d, want %d; body=%s", disabledNode.Code, http.StatusForbidden, disabledNode.Body)
	}
}

func TestMachineAuthenticationFailuresAreRateLimited(t *testing.T) {
	api, database := newTestAPI(t)
	machine, _, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "rate-limited-machine", IsActive: true,
	}, fixedNow())
	if err != nil {
		t.Fatalf("CreateMachine() error = %v", err)
	}
	body := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	for attempt := 0; attempt < 60; attempt++ {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", "invalid-machine-credential", body)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", attempt+1, response.Code, http.StatusUnauthorized)
		}
	}
	limited := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", "invalid-machine-credential", body)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("rate-limited response status=%d retry-after=%q", limited.Code, limited.Header().Get("Retry-After"))
	}
}

func TestAuthenticatedMachineHandshakeRequestsAreRateLimited(t *testing.T) {
	api, database := newTestAPI(t)
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "handshake-rate-limited-machine", IsActive: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	for attempt := 1; attempt <= 20; attempt++ {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token, body)
		if response.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d; body=%s", attempt, response.Code, http.StatusOK, response.Body)
		}
	}
	limited := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token, body)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("rate-limited status=%d retry-after=%q body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Body)
	}
}

type testClient struct {
	cookies []*http.Cookie
	csrf    string
}

func (c testClient) addCookies(request *http.Request) {
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
}

func (c testClient) request(t *testing.T, api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	path = adminAPIPath(path)
	return c.rawRequest(t, api, method, path, body)
}

func (c testClient) rawRequest(t *testing.T, api http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	c.addCookies(request)
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		request.Header.Set("X-CSRF-Token", c.csrf)
	}
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}

func loginAdmin(t *testing.T, api http.Handler) testClient {
	return loginAs(t, api, "admin@example.test", "admin-password-123")
}

func loginAs(t *testing.T, api http.Handler, email, password string) testClient {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", response.Code, response.Body)
	}

	result := response.Result()
	cookies := result.Cookies()
	var csrf string
	for _, cookie := range cookies {
		if cookie.Name == CSRFCookieName {
			csrf, _ = url.QueryUnescape(cookie.Value)
		}
	}
	if len(cookies) != 2 || csrf == "" {
		t.Fatalf("login cookies = %#v", cookies)
	}
	return testClient{cookies: cookies, csrf: csrf}
}

func newTestAPI(t *testing.T) (http.Handler, *store.Store) {
	return newTestAPIWithOptions(t, nil, true, nil, nil)
}

func newTestAPIWithCatalogHTTP(t *testing.T, function func(*http.Request) (*http.Response, error)) (http.Handler, *store.Store) {
	return newTestAPIWithOptions(t, function, true, nil, nil)
}

func newTestAPIWithoutInvitationProtection(t *testing.T) (http.Handler, *store.Store) {
	return newTestAPIWithOptions(t, nil, false, nil, nil)
}

func newTestAPIWithCaptcha(t *testing.T, verifier captcha.Verifier) (http.Handler, *store.Store) {
	return newTestAPIWithOptions(t, nil, true, verifier, nil)
}

func newTestAPIWithPaymentGateway(t *testing.T, gateway paymentGateway) (http.Handler, *store.Store) {
	return newTestAPIWithOptions(t, nil, true, nil, gateway)
}

func newTestAPIWithOptions(t *testing.T, function func(*http.Request) (*http.Response, error), protectInvitations bool, captchaVerifier captcha.Verifier, gateway paymentGateway) (http.Handler, *store.Store) {
	return newTestAPIWithAttachmentOptions(t, function, protectInvitations, captchaVerifier, gateway, false)
}

func newTestAPIWithAttachments(t *testing.T) (http.Handler, *store.Store) {
	return newTestAPIWithAttachmentOptions(t, nil, true, nil, nil, true)
}

func newTestAPIWithAttachmentOptions(t *testing.T, function func(*http.Request) (*http.Response, error), protectInvitations bool, captchaVerifier captcha.Verifier, gateway paymentGateway, enableAttachments bool) (http.Handler, *store.Store) {
	return newTestAPIWithExtendedOptions(t, function, protectInvitations, captchaVerifier, gateway, enableAttachments, nil)
}

func newTestAPIWithTelegram(t *testing.T, telegramClient telegrambot.Client) (http.Handler, *store.Store) {
	return newTestAPIWithExtendedOptions(t, nil, true, nil, nil, false, telegramClient)
}

func newTestAPIWithExtendedOptions(t *testing.T, function func(*http.Request) (*http.Response, error), protectInvitations bool, captchaVerifier captcha.Verifier, gateway paymentGateway, enableAttachments bool, telegramClient telegrambot.Client) (http.Handler, *store.Store) {
	return newTestAPIWithAllOptions(t, function, protectInvitations, captchaVerifier, gateway, enableAttachments, telegramClient, nil)
}

func newTestAPIWithTicketRegionResolver(t *testing.T, resolver ticketRegionResolver) (http.Handler, *store.Store) {
	return newTestAPIWithAllOptions(t, nil, true, nil, nil, false, nil, resolver)
}

func newTestAPIWithAllOptions(t *testing.T, function func(*http.Request) (*http.Response, error), protectInvitations bool, captchaVerifier captcha.Verifier, gateway paymentGateway, enableAttachments bool, telegramClient telegrambot.Client, regionResolver ticketRegionResolver) (http.Handler, *store.Store) {
	t.Helper()
	database := cloneHTTPAPITestDatabase(t)
	hasher := newHTTPAPITestPasswordHasher()

	var catalogHTTPClient clientcatalog.HTTPDoer
	if function != nil {
		catalogHTTPClient = catalogHTTPFunc(function)
	}
	settingsCipher, err := appsettings.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	passwordResetProtector, err := security.NewPasswordResetProtector(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewPasswordResetProtector() error = %v", err)
	}
	registrationEmailProtector, err := security.NewRegistrationEmailProtector(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewRegistrationEmailProtector() error = %v", err)
	}
	loginLinkProtector, err := security.NewLoginLinkProtector(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewLoginLinkProtector() error = %v", err)
	}
	var invitationProtector *security.InvitationProtector
	if protectInvitations {
		invitationProtector, err = security.NewInvitationProtector(make([]byte, 32))
		if err != nil {
			t.Fatalf("NewInvitationProtector() error = %v", err)
		}
	}
	runtimeTracker := operations.NewTracker(fixedNow().Add(-time.Hour))
	runtimeTracker.MarkSchedulerRun(fixedNow())
	runtimeTracker.MarkMailRun(fixedNow())
	var attachmentService *attachments.Service
	if enableAttachments {
		attachmentService, err = attachments.New(database, attachments.Options{
			Root: t.TempDir(), SigningKey: bytes.Repeat([]byte{0x42}, 32), PanelURL: "https://panel.example.test",
			ChunkSize: 4, MaxFileSize: 64, TotalQuota: 1 << 20, SignedURLTTL: 2 * time.Hour,
			DraftTTL: 24 * time.Hour, TrashRetention: 7 * 24 * time.Hour, MaxPerArticle: 100,
		})
		if err != nil {
			t.Fatalf("attachments.New() error = %v", err)
		}
	}
	bulkService, err := bulkops.New(database, bulkops.Options{
		Cipher: settingsCipher, ExportRoot: t.TempDir(), PanelURL: "https://panel.example.test",
	})
	if err != nil {
		t.Fatalf("bulkops.New() error = %v", err)
	}
	handler := New(Dependencies{
		Store:                      database,
		PasswordHasher:             hasher,
		Now:                        fixedNow,
		PanelURL:                   "https://panel.example.test",
		LegacyAdminPath:            testAdminPath,
		NodeRelease:                "v1.14.3",
		CookieSecure:               false,
		CatalogHTTPClient:          catalogHTTPClient,
		SettingsCipher:             settingsCipher,
		PasswordResetProtector:     passwordResetProtector,
		RegistrationEmailProtector: registrationEmailProtector,
		InvitationProtector:        invitationProtector,
		LoginLinkProtector:         loginLinkProtector,
		RuntimeTracker:             runtimeTracker,
		CaptchaVerifier:            captchaVerifier,
		PaymentGateway:             gateway,
		Attachments:                attachmentService,
		BulkOperations:             bulkService,
		TelegramBot:                telegramClient,
		TicketRegionResolver:       regionResolver,
	})
	return handler, database
}

type catalogHTTPFunc func(*http.Request) (*http.Response, error)

func (function catalogHTTPFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}

func fixedNow() time.Time {
	location, _ := time.LoadLocation("Asia/Singapore")
	return time.Date(2026, 8, 20, 20, 0, 0, 0, location)
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, output any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), output); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body)
	}
}

func agentRequest(api http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	return response
}
