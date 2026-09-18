package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestUSER001RandomBatchCredentialsPersistAndPasswordResetReauthenticates(t *testing.T) {
	api, database := newTestAPI(t)
	admin := loginAdmin(t, api)
	generated := admin.request(t, api, http.MethodPost, "/api/v1/admin/admin/users/generate", `{"mode":"random_batch","email_domain":"example.test","count":2}`)
	if generated.Code != http.StatusCreated {
		t.Fatalf("generate status=%d", generated.Code)
	}
	var payload struct {
		Data struct {
			Items []struct {
				Email    string `json:"email"`
				Password string `json:"password"`
			} `json:"items"`
		} `json:"data"`
	}
	decodeResponse(t, generated, &payload)
	if len(payload.Data.Items) != 2 {
		t.Fatalf("generated count=%d", len(payload.Data.Items))
	}
	for index, item := range payload.Data.Items {
		t.Run(fmt.Sprintf("account_%d", index), func(t *testing.T) {
			stored, err := database.FindUserByEmail(context.Background(), item.Email)
			if err != nil {
				t.Fatalf("generated account lookup: %v", err)
			}
			oldSession := loginAccount(t, api, item.Email, item.Password)
			detail, err := database.GetAdminUser(context.Background(), stored.ID)
			if err != nil {
				t.Fatal(err)
			}
			const replacement = "closeout-replacement-password-123"
			reset := admin.request(t, api, http.MethodPut, fmt.Sprintf("/api/v1/admin/admin/users/%d/password", stored.ID), fmt.Sprintf(`{"revision":%d,"new_password":%q}`, detail.Revision, replacement))
			if reset.Code != http.StatusOK {
				t.Fatalf("reset status=%d", reset.Code)
			}
			if response := loginAccountResponse(api, item.Email, item.Password); response.Code != http.StatusUnauthorized {
				t.Fatalf("old password status=%d", response.Code)
			}
			if response := oldSession.request(t, api, http.MethodGet, "/api/v1/auth/session", ""); response.Code != http.StatusUnauthorized {
				t.Fatalf("old session status=%d", response.Code)
			}
			freshSession := loginAccount(t, api, item.Email, replacement)
			response := freshSession.request(t, api, http.MethodGet, "/api/v1/auth/session", "")
			if response.Code != http.StatusOK {
				t.Fatalf("fresh session status=%d", response.Code)
			}
			var session struct {
				Data struct {
					ID    int64  `json:"id"`
					Email string `json:"email"`
				} `json:"data"`
			}
			decodeResponse(t, response, &session)
			if session.Data.ID != stored.ID || session.Data.Email != item.Email {
				t.Fatal("replacement credential authenticated a different account")
			}
		})
	}
}
