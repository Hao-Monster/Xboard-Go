package httpapi

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestAdminNodeRealityGeneration(t *testing.T) {
	api, database := newTestAPI(t)
	const path = "/api/v1/admin/admin/nodes/reality-key"
	anonymous := httptest.NewRecorder()
	api.ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", anonymous.Code)
	}
	admin := loginAdmin(t, api)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
	admin.addCookies(request)
	noCSRF := httptest.NewRecorder()
	api.ServeHTTP(noCSRF, request)
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", noCSRF.Code)
	}
	hash, err := newHTTPAPITestPasswordHasher().Hash("ordinary-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{Email: "reality-user@example.test", PasswordHash: hash}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	ordinary := loginAs(t, api, "reality-user@example.test", "ordinary-password-123")
	if response := ordinary.request(t, api, http.MethodPost, path, `{}`); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user status=%d", response.Code)
	}
	var previous string
	for range 2 {
		response := admin.request(t, api, http.MethodPost, path, `{}`)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d cache=%s", response.Code, response.Header().Get("Cache-Control"))
		}
		var result struct {
			Data struct {
				Private string `json:"private_key"`
				Public  string `json:"public_key"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		private, err := base64.RawURLEncoding.DecodeString(result.Data.Private)
		if err != nil || len(private) != 32 || len(result.Data.Private) != 43 {
			t.Fatal("invalid private key encoding")
		}
		public, err := base64.RawURLEncoding.DecodeString(result.Data.Public)
		if err != nil || len(public) != 32 || len(result.Data.Public) != 43 {
			t.Fatal("invalid public key encoding")
		}
		key, err := ecdh.X25519().NewPrivateKey(private)
		if err != nil || !bytes.Equal(key.PublicKey().Bytes(), public) {
			t.Fatal("key pair mismatch")
		}
		if result.Data.Private == previous {
			t.Fatal("key reused")
		}
		previous = result.Data.Private
	}
}

func TestAdminNodeECHGenerationUsesMatchingFreshKeysAndRequiresAdmin(t *testing.T) {
	api, _ := newTestAPI(t)
	unauthenticated := httptest.NewRecorder()
	api.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/admin/admin/nodes/ech-key", strings.NewReader(`{}`)))
	if unauthenticated.Code == http.StatusOK {
		t.Fatal("anonymous key endpoint accepted")
	}
	admin := loginAdmin(t, api)
	var previous string
	for range 2 {
		response := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/nodes/ech-key", `{"public_name":"ech.example.test"}`)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d cache=%s", response.Code, response.Header().Get("Cache-Control"))
		}
		var result struct {
			Data struct {
				Key    string `json:"key"`
				Config string `json:"config"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Data.Key == previous {
			t.Fatal("key reused")
		}
		previous = result.Data.Key
		keys, _ := pem.Decode([]byte(result.Data.Key))
		configs, _ := pem.Decode([]byte(result.Data.Config))
		if keys == nil || configs == nil || keys.Type != "ECH KEYS" || configs.Type != "ECH CONFIGS" {
			t.Fatal("invalid PEM")
		}
		if binary.BigEndian.Uint16(keys.Bytes[:2]) != 32 {
			t.Fatal("invalid private key vector")
		}
		private, err := ecdh.X25519().NewPrivateKey(keys.Bytes[2:34])
		if err != nil {
			t.Fatal(err)
		}
		config := configs.Bytes[2:]
		if !bytes.Equal(keys.Bytes[36:], config) || int(binary.BigEndian.Uint16(configs.Bytes[:2])) != len(config) {
			t.Fatal("config mismatch")
		}
		if binary.BigEndian.Uint16(config[:2]) != 0xfe0d || !bytes.Equal(config[9:41], private.PublicKey().Bytes()) {
			t.Fatal("key pair mismatch")
		}
		if !bytes.Contains(config, []byte("ech.example.test")) {
			t.Fatal("public name missing")
		}
	}
	for _, name := range []string{"bad\nname", "-bad.example", "bad-.example", "bad..example", strings.Repeat("a", 64) + ".test"} {
		body, _ := json.Marshal(map[string]string{"public_name": name})
		invalid := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/nodes/ech-key", string(body))
		if invalid.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid name %q status=%d", name, invalid.Code)
		}
	}
}
