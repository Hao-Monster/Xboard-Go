package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDIFFNODE004LegacyHTTPBearerAndAliases(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-runtime-token-1234567890"
	if _, err := database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &legacyToken, PullInterval: 23, PushInterval: 17, DeviceLimitMode: 1,
	}, now); err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "legacy-vless", Type: "vless", Host: "legacy.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7},
		Config: []byte(`{"protocol":"vless","listen_ip":"0.0.0.0","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "legacy-runtime-user@example.test", PasswordHash: "test-password-hash",
		UUID: "0f7b9f23-c46f-4f30-8d38-6cc8ca23b50f", GroupID: 7, TransferEnable: 1_000_000, DeviceLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v2/server/handshake", `{}`},
		{http.MethodPost, "/api/v2/server/handshake", fmt.Sprintf(`{"node_id":%d,"node_type":"vless"}`, node.ID)},
		{http.MethodGet, fmt.Sprintf("/api/v2/server/handshake?node_id=%d", node.ID), ""},
	} {
		response := agentRequest(api, test.method, test.path, legacyToken, test.body)
		if response.Code != http.StatusOK || !legacyBodyContainsAll(response.Body.String(), `"push_interval":17`, `"pull_interval":23`) {
			t.Fatalf("legacy handshake %s %s status=%d body=%s", test.method, test.path, response.Code, response.Body)
		}
	}

	for _, path := range []string{
		fmt.Sprintf("/api/v2/server/config?node_id=%d", node.ID),
		fmt.Sprintf("/api/v1/server/UniProxy/config?node_id=%d&node_type=vless", node.ID),
	} {
		response := agentRequest(api, http.MethodGet, path, legacyToken, "")
		if response.Code != http.StatusOK || !legacyBodyContainsAll(response.Body.String(), `"protocol":"vless"`, `"push_interval":17`, `"pull_interval":23`) || response.Header().Get("ETag") == "" {
			t.Fatalf("legacy config %s status=%d body=%s", path, response.Code, response.Body)
		}
	}
	for _, path := range []string{
		fmt.Sprintf("/api/v2/server/user?node_id=%d", node.ID),
		fmt.Sprintf("/api/v1/server/UniProxy/user?node_id=%d&node_type=vless", node.ID),
	} {
		response := agentRequest(api, http.MethodGet, path, legacyToken, "")
		if response.Code != http.StatusOK || !legacyBodyContainsAll(response.Body.String(), fmt.Sprintf(`"id":%d`, user.ID), user.UUID) {
			t.Fatalf("legacy users %s status=%d body=%s", path, response.Code, response.Body)
		}
	}

	report := agentRequest(api, http.MethodPost, "/api/v2/server/report", legacyToken, fmt.Sprintf(`{
		"node_id":%d,"node_type":"vless","report_id":"6f21386c-abed-45af-8ec2-b6a774f2095a",
		"traffic":{"%d":[5,7]},"alive":{"%d":["192.0.2.41"]}
	}`, node.ID, user.ID, user.ID))
	if report.Code != http.StatusOK {
		t.Fatalf("legacy v2 report status=%d body=%s", report.Code, report.Body)
	}
	push := agentRequest(api, http.MethodPost, fmt.Sprintf("/api/v1/server/UniProxy/push?node_id=%d", node.ID), legacyToken,
		fmt.Sprintf(`{"%d":[11,13]}`, user.ID))
	if push.Code != http.StatusOK {
		t.Fatalf("legacy v1 push status=%d body=%s", push.Code, push.Body)
	}
	traffic, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || traffic.Upload != 16 || traffic.Download != 20 {
		t.Fatalf("legacy traffic=%#v err=%v", traffic, err)
	}

	alive := agentRequest(api, http.MethodPost, fmt.Sprintf("/api/v1/server/UniProxy/alive?node_id=%d", node.ID), legacyToken,
		fmt.Sprintf(`{"%d":["192.0.2.42"]}`, user.ID))
	if alive.Code != http.StatusOK {
		t.Fatalf("legacy alive status=%d body=%s", alive.Code, alive.Body)
	}
	aliveList := agentRequest(api, http.MethodGet, fmt.Sprintf("/api/v1/server/UniProxy/alivelist?node_id=%d", node.ID), legacyToken, "")
	if aliveList.Code != http.StatusOK || !legacyBodyContainsAll(aliveList.Body.String(), fmt.Sprintf(`"%d"`, user.ID), "192.0.2.42") {
		t.Fatalf("legacy alivelist status=%d body=%s", aliveList.Code, aliveList.Body)
	}
	status := agentRequest(api, http.MethodPost, fmt.Sprintf("/api/v1/server/UniProxy/status?node_id=%d", node.ID), legacyToken,
		`{"cpu":12.5,"mem":{"total":100,"used":20},"swap":{"total":0,"used":0},"disk":{"total":1000,"used":100}}`)
	if status.Code != http.StatusOK {
		t.Fatalf("legacy status status=%d body=%s", status.Code, status.Body)
	}

	invalid := agentRequest(api, http.MethodGet, fmt.Sprintf("/api/v2/server/config?node_id=%d", node.ID), "wrong-legacy-token-1234", "")
	if invalid.Code != http.StatusUnauthorized || invalid.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("invalid legacy auth status=%d challenge=%q body=%s", invalid.Code, invalid.Header().Get("WWW-Authenticate"), invalid.Body)
	}
	queryToken := agentRequest(api, http.MethodGet, fmt.Sprintf("/api/v2/server/config?node_id=%d&token=%s", node.ID, legacyToken), "", "")
	if queryToken.Code != http.StatusUnauthorized {
		t.Fatalf("query token status=%d body=%s", queryToken.Code, queryToken.Body)
	}
}

func TestLegacyNodeAuthenticationFailureLimitCannotBeBypassedWithNodeIDs(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-rate-limit-token-1234567890"
	if _, err := database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &legacyToken, PullInterval: 60, PushInterval: 60,
	}, now); err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "legacy-rate-limit", Type: "vless", Host: "rate-limit.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7}, Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 60; index++ {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", "invalid-legacy-token-1234",
			fmt.Sprintf(`{"node_id":%d}`, 10_000+index))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid attempt %d status=%d body=%s", index+1, response.Code, response.Body)
		}
	}
	limited := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", legacyToken,
		fmt.Sprintf(`{"node_id":%d}`, node.ID))
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("post-limit valid request status=%d retry-after=%q body=%s", limited.Code, limited.Header().Get("Retry-After"), limited.Body)
	}
}

func TestLegacyNodePOSTEndpointsRejectNegativeMachineID(t *testing.T) {
	api, database := newTestAPI(t)
	ctx := context.Background()
	now := fixedNow()
	settings, err := database.GetNodeAgentSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	legacyToken := "legacy-negative-machine-token-123456"
	if _, err := database.UpdateNodeAgentSettings(ctx, store.UpdateNodeAgentSettingsInput{
		Revision: settings.Revision, ServerToken: &legacyToken, PullInterval: 60, PushInterval: 60,
	}, now); err != nil {
		t.Fatal(err)
	}
	node, err := database.CreateNode(ctx, store.CreateNodeInput{
		Name: "negative-machine", Type: "vless", Host: "negative.example.test", Port: "443", Show: true, Enabled: true,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveNodeRuntime(ctx, node.ID, store.SaveNodeRuntimeInput{
		RateMicros: 1_000_000, GroupIDs: []int64{7}, Config: []byte(`{"protocol":"vless","server_port":443}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, store.CreateRuntimeUserInput{
		Email: "negative-machine@example.test", PasswordHash: "hash", UUID: "4a909c03-6bf0-42f4-aeec-25d2adfc3c44",
		GroupID: 7, TransferEnable: 1_000_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "handshake", body: `{"machine_id":-1}`},
		{name: "report", body: fmt.Sprintf(`{
			"machine_id":-1,"node_id":%d,"report_id":"5f6e61f4-c2ec-49c9-9445-a2fb47d0e15e",
			"traffic":{"%d":[1,1]}
		}`, node.ID, user.ID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := "/api/v2/server/" + test.name
			response := agentRequest(api, http.MethodPost, path, legacyToken, test.body)
			if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "machine_id") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
	traffic, err := database.GetRuntimeUserTraffic(ctx, user.ID)
	if err != nil || traffic.Upload != 0 || traffic.Download != 0 {
		t.Fatalf("negative machine report mutated traffic=%#v err=%v", traffic, err)
	}
}

func legacyBodyContainsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
