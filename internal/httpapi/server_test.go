package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminAPIRequiresSessionAndCSRF(t *testing.T) {
	api, _ := newTestAPI(t)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/machines", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	client := loginAdmin(t, api)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/machines", strings.NewReader(`{"name":"edge-01","is_active":true}`))
	request.Header.Set("Content-Type", "application/json")
	client.addCookies(request)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/machines", strings.NewReader(`{"name":"edge-01","is_active":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://attacker.example.test")
	client.addCookies(request)
	request.Header.Set("X-CSRF-Token", client.csrf)
	response = httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin status = %d, want %d; body=%s", response.Code, http.StatusForbidden, response.Body)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/machines", strings.NewReader(`{"name":"edge-01","is_active":true}`))
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
	bind := client.request(t, api, http.MethodPut, bindPath, `{}`)
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
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"admin-password-123"}`))
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
	t.Helper()
	database, err := store.OpenSQLite(fmt.Sprintf("file:http-%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	hasher := security.NewPasswordHasher(security.PasswordParams{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	passwordHash, err := hasher.Hash("admin-password-123")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if _, err := database.BootstrapAdmin(context.Background(), "admin@example.test", passwordHash, fixedNow()); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	handler := New(Dependencies{
		Store:          database,
		PasswordHasher: hasher,
		Now:            fixedNow,
		PanelURL:       "https://panel.example.test",
		NodeRelease:    "v1.13",
		CookieSecure:   false,
	})
	return handler, database
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
