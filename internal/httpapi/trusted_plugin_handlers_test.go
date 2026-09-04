package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTrustedPluginAdministratorAPIAndPaymentProviderGate(t *testing.T) {
	api, _ := newTestAPI(t)
	admin := loginAdmin(t, api)

	listed := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/plugins", "")
	if listed.Code != http.StatusOK || !containsAll(listed.Body.String(),
		`"code":"telegram"`, `"code":"alipay_f2f"`, `"code":"epay"`, `"version":"1.0.1"`) {
		t.Fatalf("trusted plugin list status=%d body=%s", listed.Code, listed.Body)
	}
	var payload struct {
		Data []struct {
			Code     string         `json:"code"`
			Enabled  bool           `json:"enabled"`
			Config   map[string]any `json:"config"`
			Revision int64          `json:"revision"`
		} `json:"data"`
	}
	decodeResponse(t, listed, &payload)
	var epay struct {
		Code     string
		Enabled  bool
		Config   map[string]any
		Revision int64
	}
	for _, plugin := range payload.Data {
		if plugin.Code == "epay" {
			epay.Code, epay.Enabled, epay.Config, epay.Revision = plugin.Code, plugin.Enabled, plugin.Config, plugin.Revision
		}
	}
	if epay.Code == "" || !epay.Enabled || len(epay.Config) != 0 {
		t.Fatalf("EPay registry item = %#v", epay)
	}
	body, err := json.Marshal(map[string]any{"revision": epay.Revision, "enabled": false, "config": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	updated := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/plugins/epay", string(body))
	if updated.Code != http.StatusOK || !containsAll(updated.Body.String(), `"code":"epay"`, `"enabled":false`, `"revision":2`) {
		t.Fatalf("disable EPay status=%d body=%s", updated.Code, updated.Body)
	}
	providers := admin.request(t, api, http.MethodGet, "/api/v1/admin/admin/payment-providers", "")
	if providers.Code != http.StatusOK || containsAll(providers.Body.String(), `"provider":"EPay"`) || !containsAll(providers.Body.String(), `"provider":"BTCPay"`) {
		t.Fatalf("payment providers after plugin disable status=%d body=%s", providers.Code, providers.Body)
	}
	create := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/payments", `{
		"payment":"EPay","name":"disabled","handling_fee_fixed":0,"handling_fee_basis_points":0,"enable":true,
		"config":{"url":"https://epay.example.test","pid":"1001","key":"secret","type":"alipay"}
	}`)
	if create.Code != http.StatusConflict || !containsAll(create.Body.String(), `"code":"plugin_disabled"`) {
		t.Fatalf("create disabled provider status=%d body=%s", create.Code, create.Body)
	}
	stale := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/plugins/epay", string(body))
	if stale.Code != http.StatusConflict || !containsAll(stale.Body.String(), `"code":"plugin_revision_conflict"`) {
		t.Fatalf("stale plugin update status=%d body=%s", stale.Code, stale.Body)
	}
	unknown := admin.request(t, api, http.MethodPatch, "/api/v1/admin/admin/plugins/shell", `{"revision":1,"enabled":true,"config":{}}`)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin update status=%d body=%s", unknown.Code, unknown.Body)
	}
}
