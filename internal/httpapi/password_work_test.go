package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type passwordWorkHasher struct {
	hashes, verifies int
	hash             func() (string, error)
	verify           func() bool
}

func (h *passwordWorkHasher) Hash(string) (string, error) {
	h.hashes++
	if h.hash != nil {
		return h.hash()
	}
	return "qa-replacement-hash", nil
}

func (h *passwordWorkHasher) Verify(string, string) bool {
	h.verifies++
	if h.verify != nil {
		return h.verify()
	}
	return true
}

func newPasswordWorkAPI(t *testing.T) (*server, store.AdminUser, *passwordWorkHasher) {
	t.Helper()
	_, db := newTestAPI(t)
	user, err := db.CreateAdminUser(t.Context(), store.CreateAdminUserInput{Email: "qa-work@example.test", PasswordHash: "qa-original-hash"}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	hasher := &passwordWorkHasher{}
	return &server{store: db, passwordHasher: hasher, now: fixedNow, passwordHashSlots: make(chan struct{}, 2), loginAttempts: newAttemptLimiter(20, time.Minute)}, user, hasher
}

func passwordWorkRequest(user store.AdminUser, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("userID", fmt.Sprint(user.ID))
	return r.WithContext(context.WithValue(r.Context(), sessionContextKey, store.SessionUser{UserID: user.ID}))
}

func TestPasswordWorkSaturationRejectsEverySynchronousPath(t *testing.T) {
	for _, name := range []string{"login", "legacy-login", "change-password", "create-user", "update-user", "legacy-update-user", "admin-reset"} {
		t.Run(name, func(t *testing.T) {
			api, user, hasher := newPasswordWorkAPI(t)
			var handler http.HandlerFunc
			var body string
			switch name {
			case "login":
				handler = api.login
				body = `{"email":"qa-work@example.test","password":"qa-password-123"}`
			case "legacy-login":
				handler = api.legacyLogin
				body = `{"email":"qa-work@example.test","password":"qa-password-123"}`
			case "change-password":
				handler = api.changePassword
				body = `{"old_password":"qa-password-123","new_password":"qa-new-password-123"}`
			case "create-user":
				handler = api.createAdminUser
				body = `{"email":"qa-created@example.test","password":"qa-password-123"}`
			case "update-user":
				handler = api.updateAdminUser
				body = `{"revision":1,"email":"qa-work@example.test","password":"qa-password-123","group_id":null,"transfer_enable":0,"expired_at":null,"speed_limit":0,"device_limit":0,"banned":false}`
			case "legacy-update-user":
				handler = api.legacyUpdateAdminUser
				body = fmt.Sprintf(`{"id":%d,"password":"qa-password-123"}`, user.ID)
			case "admin-reset":
				handler = api.resetAdminUserPassword
				body = `{"revision":1,"new_password":"qa-password-123"}`
			}
			first, firstOK := api.beginPasswordHash()
			second, secondOK := api.beginPasswordHash()
			if !firstOK || !secondOK {
				t.Fatal("could not occupy the shared slots")
			}
			defer first()
			defer second()
			response := httptest.NewRecorder()
			request := passwordWorkRequest(user, body)
			if name == "legacy-update-user" {
				admin, err := api.store.FindUserByEmail(t.Context(), "admin@example.test")
				if err != nil {
					t.Fatal(err)
				}
				request = request.WithContext(context.WithValue(request.Context(), sessionContextKey, store.SessionUser{UserID: admin.ID, IsAdmin: true}))
			}
			handler(response, request)
			if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
				t.Errorf("busy status=%d retry=%q, want 429/1", response.Code, response.Header().Get("Retry-After"))
			}
			if hasher.hashes != 0 || hasher.verifies != 0 {
				t.Errorf("busy path called hasher: hash=%d verify=%d", hasher.hashes, hasher.verifies)
			}
			current, err := api.store.FindUserByID(t.Context(), user.ID)
			if err != nil || current.PasswordHash != "qa-original-hash" {
				t.Errorf("busy path mutated account: err=%v", err)
			}
			if _, err := api.store.FindUserByEmail(t.Context(), "qa-created@example.test"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("busy create inserted user: %v", err)
			}
		})
	}
}

func TestPasswordWorkLoginHoldsSlotAcrossUpgradeAndConflictRetry(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			api, user, hasher := newPasswordWorkAPI(t)
			legacyHash, err := security.EncodeLegacyPassword("md5", "", strings.Repeat("a", 32))
			if err != nil {
				t.Fatal(err)
			}
			if err := api.store.ChangePassword(t.Context(), user.ID, "qa-original-hash", legacyHash, fixedNow()); err != nil {
				t.Fatal(err)
			}
			hasher.verify = func() bool {
				if len(api.passwordHashSlots) != 1 {
					t.Errorf("Verify is outside the common work gate")
				}
				return true
			}
			hasher.hash = func() (string, error) {
				if len(api.passwordHashSlots) != 1 {
					t.Errorf("upgrade Hash is outside the common work gate")
				}
				// A controlled state change forces the legitimate compare-and-swap
				// retry without racing real hashes or allocating Argon2 memory.
				if err := api.store.ChangePassword(t.Context(), user.ID, legacyHash, "qa-concurrent-hash", fixedNow()); err != nil {
					t.Error(err)
				}
				return "qa-replacement-hash", nil
			}
			response := httptest.NewRecorder()
			request := passwordWorkRequest(user, `{"email":"qa-work@example.test","password":"qa-password-123"}`)
			if legacy {
				api.legacyLogin(response, request)
			} else {
				api.login(response, request)
			}
			if response.Code != http.StatusOK {
				t.Fatalf("login status=%d body=%s", response.Code, response.Body)
			}
			if hasher.hashes != 1 || hasher.verifies != 2 {
				t.Fatalf("retry not exercised: hash=%d verify=%d", hasher.hashes, hasher.verifies)
			}
			if len(api.passwordHashSlots) != 0 {
				t.Fatal("successful login leaked a slot")
			}
		})
	}
}

func TestPasswordWorkErrorsReleaseSlot(t *testing.T) {
	for _, failure := range []string{"verify", "hash", "decode"} {
		t.Run(failure, func(t *testing.T) {
			api, user, hasher := newPasswordWorkAPI(t)
			response := httptest.NewRecorder()
			body := `{"old_password":"qa-password-123","new_password":"qa-new-password-123"}`
			want := http.StatusUnprocessableEntity
			hasher.verify = func() bool {
				if len(api.passwordHashSlots) != 1 {
					t.Error("Verify not gated")
				}
				return failure != "verify"
			}
			hasher.hash = func() (string, error) {
				if len(api.passwordHashSlots) != 1 {
					t.Error("Hash not gated")
				}
				return "", errors.New("qa hash failure")
			}
			if failure == "hash" {
				want = http.StatusInternalServerError
			}
			if failure == "decode" {
				body = "{"
				want = http.StatusBadRequest
			}
			api.changePassword(response, passwordWorkRequest(user, body))
			if response.Code != want {
				t.Fatalf("error status=%d want=%d", response.Code, want)
			}
			if len(api.passwordHashSlots) != 0 {
				t.Fatal("failed request leaked a slot")
			}
		})
	}
}

func TestPasswordWorkBatchWaitingHonorsCancellation(t *testing.T) {
	api, _, hasher := newPasswordWorkAPI(t)
	first, _ := api.beginPasswordHash()
	defer first()
	second, _ := api.beginPasswordHash()
	defer second()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := api.hashAdminUserPasswords(ctx, []string{"qa-password-123"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("batch cancellation: %v", err)
	}
	if hasher.hashes != 0 {
		t.Fatal("cancelled waiting batch calculated a hash")
	}
}

func TestPasswordWorkLoginFailuresReleaseSlot(t *testing.T) {
	for _, failure := range []string{"verify", "upgrade"} {
		t.Run(failure, func(t *testing.T) {
			api, user, hasher := newPasswordWorkAPI(t)
			want := http.StatusUnauthorized
			hasher.verify = func() bool {
				if len(api.passwordHashSlots) != 1 {
					t.Error("login Verify not gated")
				}
				return failure != "verify"
			}
			if failure == "upgrade" {
				legacyHash, err := security.EncodeLegacyPassword("md5", "", strings.Repeat("a", 32))
				if err != nil {
					t.Fatal(err)
				}
				if err := api.store.ChangePassword(t.Context(), user.ID, "qa-original-hash", legacyHash, fixedNow()); err != nil {
					t.Fatal(err)
				}
				hasher.hash = func() (string, error) {
					if len(api.passwordHashSlots) != 1 {
						t.Error("login Hash not gated")
					}
					return "", errors.New("qa upgrade failure")
				}
				want = http.StatusInternalServerError
			}
			response := httptest.NewRecorder()
			api.login(response, passwordWorkRequest(user, `{"email":"qa-work@example.test","password":"qa-password-123"}`))
			if response.Code != want {
				t.Fatalf("login failure status=%d want=%d", response.Code, want)
			}
			if len(api.passwordHashSlots) != 0 {
				t.Fatal("login failure leaked a slot")
			}
		})
	}
}
