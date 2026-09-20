package httpapi

import (
	"context"
	"fmt"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMachineTokenAdministratorBoundary(t *testing.T) {
	api, db := newTestAPI(t)
	client := loginAdmin(t, api)
	m, _, err := db.CreateMachine(context.Background(), store.CreateMachineInput{Name: "token-api", IsActive: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/admin/admin/machines/%d/token", m.ID)
	for _, endpoint := range []struct{ method, path string }{{http.MethodGet, path}, {http.MethodPost, path + "/reset"}} {
		res := httptest.NewRecorder()
		api.ServeHTTP(res, httptest.NewRequest(endpoint.method, endpoint.path, nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status=%d", res.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path+"/reset", nil)
	client.addCookies(req)
	res := httptest.NewRecorder()
	api.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", res.Code)
	}
	reset := client.request(t, api, http.MethodPost, path+"/reset", `{}`)
	if reset.Code != http.StatusOK || reset.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset status=%d", reset.Code)
	}
	var payload struct {
		Data struct {
			Token     string
			Available bool
		}
	}
	decodeResponse(t, reset, &payload)
	if !payload.Data.Available || payload.Data.Token == "" {
		t.Fatal("reset omitted token")
	}
	read := client.request(t, api, http.MethodGet, path, "")
	var got struct {
		Data struct {
			Token     string
			Available bool
		}
	}
	decodeResponse(t, read, &got)
	if read.Code != http.StatusOK || read.Header().Get("Cache-Control") != "no-store" || got.Data.Token != payload.Data.Token {
		t.Fatal("token read mismatch")
	}
	if _, err := db.AuthenticateMachine(context.Background(), m.ID, got.Data.Token, time.Now()); err != nil {
		t.Fatal("returned token invalid")
	}
}
