package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.TrimSpace(input.Email)
	attemptKey := requestIP(r)
	if !s.loginAttempts.allowed(attemptKey, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "login_rate_limited", "登录尝试过多，请稍后重试", nil)
		return
	}
	if input.Email == "" || len(input.Email) > 320 || input.Password == "" || len(input.Password) > 1024 {
		s.loginAttempts.failed(attemptKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return
	}

	user, err := s.store.FindUserByEmail(r.Context(), input.Email)
	passwordHash := s.dummyHash
	if err == nil {
		passwordHash = user.PasswordHash
	}
	passwordValid := s.passwordHasher.Verify(input.Password, passwordHash)
	if err != nil || !passwordValid || user.Banned {
		s.loginAttempts.failed(attemptKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return
	}

	sessionToken, err := security.NewOpaqueToken(32)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	csrfToken, err := security.NewOpaqueToken(32)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	now := s.now()
	expiresAt := now.Add(12 * time.Hour)
	if err := s.store.CreateSession(r.Context(), user.ID, sessionToken.Digest, csrfToken.Digest, expiresAt, now); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	s.loginAttempts.reset(attemptKey)

	http.SetCookie(w, s.sessionCookie(sessionToken.Plaintext, expiresAt))
	http.SetCookie(w, s.csrfCookie(csrfToken.Plaintext, expiresAt))
	writeSuccess(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"email":    user.Email,
		"is_admin": user.IsAdmin,
	})
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	writeSuccess(w, http.StatusOK, map[string]any{
		"id":       session.UserID,
		"email":    session.Email,
		"is_admin": session.IsAdmin,
	})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeSession(r.Context(), session.SessionID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	http.SetCookie(w, s.expiredCookie(SessionCookieName, true))
	http.SetCookie(w, s.expiredCookie(CSRFCookieName, false))
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) exchangeEnrollment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MachineID      int64  `json:"machine_id"`
		EnrollmentCode string `json:"enrollment_code"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.EnrollmentCode) > 256 {
		writeAPIError(w, http.StatusUnauthorized, "invalid_enrollment", "接入码无效或已过期", nil)
		return
	}
	attemptKey := requestIP(r)
	if !s.enrollAttempts.allowed(attemptKey, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "enrollment_rate_limited", "接入尝试过多，请稍后重试", nil)
		return
	}
	credential, err := s.store.ExchangeEnrollment(r.Context(), input.MachineID, input.EnrollmentCode, s.now())
	if errors.Is(err, store.ErrInvalidEnrollment) {
		s.enrollAttempts.failed(attemptKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_enrollment", "接入码无效或已过期", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	s.enrollAttempts.reset(attemptKey)
	if s.hub != nil {
		s.hub.DisconnectMachine(input.MachineID, "machine credential changed")
	}
	writeSuccess(w, http.StatusOK, map[string]string{"token": credential.Token, "token_type": "Bearer"})
}

func (s *server) sessionCookie(value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int((12 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *server) csrfCookie(value string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int((12 * time.Hour).Seconds()),
		HttpOnly: false,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *server) expiredCookie(name string, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	}
}
