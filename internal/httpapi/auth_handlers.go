package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatePasswordLogin(w, r)
	if !ok {
		return
	}
	if !s.issueSession(w, r, user) {
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"id":       user.ID,
		"email":    user.Email,
		"is_admin": user.IsAdmin,
	})
}

func (s *server) legacyLogin(w http.ResponseWriter, r *http.Request) {
	user, ok := s.authenticatePasswordLogin(w, r)
	if !ok {
		return
	}
	credential, ok := s.issueAccessToken(w, r, user.ID, "", nil)
	if !ok {
		return
	}
	writeSuccess(w, http.StatusOK, legacyAuthData(user, credential.token.Plaintext))
}

func (s *server) authenticatePasswordLogin(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return store.User{}, false
	}
	input.Email = strings.TrimSpace(input.Email)
	attemptKey := requestIP(r)
	if !s.loginAttempts.allowed(attemptKey, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "login_rate_limited", "登录尝试过多，请稍后重试", nil)
		return store.User{}, false
	}
	if input.Email == "" || len(input.Email) > 320 || input.Password == "" || len(input.Password) > 1024 {
		s.loginAttempts.failed(attemptKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return store.User{}, false
	}

	user, err := s.store.FindUserByEmail(r.Context(), input.Email)
	passwordHash := s.dummyHash
	if err == nil {
		passwordHash = user.PasswordHash
	}
	passwordValid := s.passwordHasher.Verify(input.Password, passwordHash)
	if err != nil || !passwordValid || user.Banned || user.AccountKind != store.AccountKindHuman {
		s.loginAttempts.failed(attemptKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "邮箱或密码错误", nil)
		return store.User{}, false
	}
	s.loginAttempts.reset(attemptKey)
	return user, true
}

func (s *server) issueSession(w http.ResponseWriter, r *http.Request, user store.User) bool {
	credentials, err := s.newSessionCredentials()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return false
	}
	if err := s.store.CreateSession(r.Context(), user.ID, credentials.token.Digest, credentials.csrf.Digest, credentials.expiresAt, s.now()); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return false
	}
	s.setSessionCookies(w, credentials)
	return true
}

type sessionCredentials struct {
	token     security.OpaqueToken
	csrf      security.OpaqueToken
	expiresAt time.Time
}

type issuedAccessToken struct {
	token security.OpaqueToken
	item  store.AccountAccessToken
}

func (s *server) issueAccessToken(w http.ResponseWriter, r *http.Request, userID int64, name string, expiresAt *time.Time) (issuedAccessToken, bool) {
	token, name, err := newAccessTokenCredentials(name)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return issuedAccessToken{}, false
	}
	item, err := s.store.CreateAccessToken(r.Context(), store.CreateAccessTokenInput{
		UserID: userID, TokenHash: token.Digest, Name: name, ExpiresAt: expiresAt,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return issuedAccessToken{}, false
	}
	return issuedAccessToken{token: token, item: item}, true
}

func newAccessTokenCredentials(name string) (security.OpaqueToken, string, error) {
	token, err := security.NewOpaqueToken(36)
	if err != nil {
		return security.OpaqueToken{}, "", err
	}
	if name == "" {
		name, err = security.NewRandomHex(10)
		if err != nil {
			return security.OpaqueToken{}, "", err
		}
	}
	return token, name, nil
}

func legacyAuthData(user store.User, plaintext string) map[string]any {
	return map[string]any{
		"token":          user.SubscriptionToken,
		"auth_data":      "Bearer " + plaintext,
		"is_admin":       user.IsAdmin,
		"is_distributor": false,
	}
}

func (s *server) newSessionCredentials() (sessionCredentials, error) {
	sessionToken, err := security.NewOpaqueToken(32)
	if err != nil {
		return sessionCredentials{}, err
	}
	csrfToken, err := security.NewOpaqueToken(32)
	if err != nil {
		return sessionCredentials{}, err
	}
	return sessionCredentials{token: sessionToken, csrf: csrfToken, expiresAt: s.now().Add(12 * time.Hour)}, nil
}

func (s *server) setSessionCookies(w http.ResponseWriter, credentials sessionCredentials) {
	http.SetCookie(w, s.sessionCookie(credentials.token.Plaintext, credentials.expiresAt))
	http.SetCookie(w, s.csrfCookie(credentials.csrf.Plaintext, credentials.expiresAt))
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
	var err error
	if session.CredentialKind == store.CredentialKindAccessToken {
		err = s.store.RevokeAccessToken(r.Context(), session.SessionID, s.now())
	} else {
		err = s.store.RevokeSession(r.Context(), session.SessionID, s.now())
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if session.CredentialKind == store.CredentialKindCookieSession {
		s.clearAuthCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listAccountSessions(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	sessions, err := s.store.ListActiveSessions(r.Context(), session.UserID, session.SessionID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, sessions)
}

func (s *server) revokeAccountSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := pathID(w, r, "sessionID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeUserSession(r.Context(), session.UserID, sessionID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	if sessionID == session.SessionID {
		s.clearAuthCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) createAccessToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		ExpiresAt string `json:"expires_at"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	fields := make(map[string]string)
	if input.Name == "" {
		fields["name"] = "请输入凭证名称"
	} else if !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) > 80 || strings.IndexFunc(input.Name, unicode.IsControl) >= 0 {
		fields["name"] = "凭证名称不得超过 80 个字符或包含控制字符"
	}
	var expiresAt *time.Time
	if input.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.ExpiresAt)
		now := s.now()
		if err != nil || parsed.Before(now.Add(5*time.Minute)) || parsed.After(now.Add(366*24*time.Hour)) {
			fields["expires_at"] = "过期时间必须在 5 分钟到 366 天之间"
		} else {
			value := parsed.UTC()
			expiresAt = &value
		}
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查长期凭证设置", fields)
		return
	}
	session, _ := sessionFromContext(r.Context())
	credential, ok := s.issueAccessToken(w, r, session.UserID, input.Name, expiresAt)
	if !ok {
		return
	}
	writeSuccess(w, http.StatusCreated, map[string]any{
		"id": credential.item.ID, "name": credential.item.Name, "token": credential.token.Plaintext,
		"token_type": "Bearer", "created_at": credential.item.CreatedAt, "expires_at": credential.item.ExpiresAt,
	})
}

func (s *server) listAccessTokens(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	currentID := int64(0)
	if session.CredentialKind == store.CredentialKindAccessToken {
		currentID = session.SessionID
	}
	tokens, err := s.store.ListActiveAccessTokens(r.Context(), session.UserID, currentID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, tokens)
}

func (s *server) revokeAccessToken(w http.ResponseWriter, r *http.Request) {
	tokenID, ok := pathID(w, r, "tokenID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeUserAccessToken(r.Context(), session.UserID, tokenID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) revokeAllAccessTokens(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeAllUserAccessTokens(r.Context(), session.UserID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) legacyListAccessTokens(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	tokens, err := s.store.ListActiveAccessTokens(r.Context(), session.UserID, session.SessionID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, map[string]any{
			"id": token.ID, "tokenable_type": "App\\Models\\User", "tokenable_id": token.UserID,
			"name": token.Name, "abilities": []string{"*"}, "last_used_at": token.LastUsedAt,
			"expires_at": token.ExpiresAt, "created_at": token.CreatedAt, "updated_at": token.UpdatedAt,
		})
	}
	writeSuccess(w, http.StatusOK, items)
}

func (s *server) legacyRemoveAccessToken(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID int64 `json:"session_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if input.SessionID > 0 {
		err := s.store.RevokeUserAccessToken(r.Context(), session.UserID, input.SessionID, s.now())
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			handleStoreError(w, err)
			return
		}
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) legacyLogout(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if err := s.store.RevokeAccessToken(r.Context(), session.SessionID, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) changePassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	fields := make(map[string]string)
	if input.OldPassword == "" {
		fields["old_password"] = "请输入当前密码"
	} else if len(input.OldPassword) > 1024 {
		fields["old_password"] = "当前密码过长"
	}
	if len(input.NewPassword) < 12 {
		fields["new_password"] = "新密码至少需要 12 个字符"
	} else if len(input.NewPassword) > 1024 {
		fields["new_password"] = "新密码不得超过 1024 个字符"
	}
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查密码输入", fields)
		return
	}

	session, _ := sessionFromContext(r.Context())
	user, err := s.store.FindUserByID(r.Context(), session.UserID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !s.passwordHasher.Verify(input.OldPassword, user.PasswordHash) {
		writeAPIError(w, http.StatusUnprocessableEntity, "current_password_invalid", "当前密码不正确", map[string]string{"old_password": "当前密码不正确"})
		return
	}
	newHash, err := s.passwordHasher.Hash(input.NewPassword)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	if err := s.store.ChangePassword(r.Context(), user.ID, user.PasswordHash, newHash, s.now()); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeAPIError(w, http.StatusConflict, "credentials_changed", "账号凭据已变化，请重新登录", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	s.clearAuthCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, s.expiredCookie(SessionCookieName, true))
	http.SetCookie(w, s.expiredCookie(CSRFCookieName, false))
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
