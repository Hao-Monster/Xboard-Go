package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) createQuickLoginLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Redirect string `json:"redirect"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	s.createQuickLoginLinkForUser(w, r, session.UserID, input.Redirect)
}

func (s *server) legacyPassportQuickLoginLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Authorization string `json:"auth_data"`
		Redirect      string `json:"redirect"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	if input.Authorization == "" {
		input.Authorization = r.Header.Get("Authorization")
	}
	if input.Authorization == "" {
		writeLegacyPassportAuthorizationError(w, false)
		return
	}
	session, err := s.authenticateBearerValue(r.Context(), input.Authorization)
	if err != nil {
		writeLegacyPassportAuthorizationError(w, true)
		return
	}
	s.createQuickLoginLinkForUser(w, r, session.UserID, input.Redirect)
}

func writeLegacyPassportAuthorizationError(w http.ResponseWriter, expired bool) {
	code := 401001
	message := "授权失败，请先登录"
	if expired {
		code = 401200
		message = "账号信息已过期，请重新登录"
	}
	writeJSON(w, http.StatusUnauthorized, map[string]any{"message": []any{code, message}})
}

func (s *server) createQuickLoginLinkForUser(w http.ResponseWriter, r *http.Request, userID int64, requestedRedirect string) {
	if s.loginLinkProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "login_link_unavailable", "登录链接服务暂不可用", nil)
		return
	}
	redirect := normalizeLoginLinkRedirect(requestedRedirect)
	token, err := s.loginLinkProtector.NewToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	digest, err := s.loginLinkProtector.TokenDigest(security.LoginLinkPurposeQuick, token)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	if err := s.store.CreateQuickLoginLink(r.Context(), userID, digest, redirect, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	loginURL := s.loginLinkURL(token, redirect)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.URL.Path == "/api/v1/passport/auth/getQuickLoginUrl" || r.URL.Path == "/api/v2/passport/auth/getQuickLoginUrl" {
		writeLegacySuccess(w, http.StatusOK, loginURL)
		return
	}
	if r.URL.Path == "/api/v1/user/getQuickLoginUrl" {
		writeSuccess(w, http.StatusOK, loginURL)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"url": loginURL, "expires_in": 60})
}

func (s *server) requestMailLoginLink(w http.ResponseWriter, r *http.Request) {
	legacyRoute := r.URL.Path == "/api/v1/passport/auth/loginWithMailLink" || r.URL.Path == "/api/v2/passport/auth/loginWithMailLink"
	if !s.mailLoginRequests.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "mail_login_rate_limited", "请求过于频繁，请稍后重试", nil)
		return
	}
	var input struct {
		Email    string `json:"email"`
		Redirect string `json:"redirect"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	rawEmail := input.Email
	email := strings.ToLower(strings.TrimSpace(rawEmail))
	if !validPasswordResetEmail(email) {
		if legacyRoute {
			message := "validation.email"
			if strings.TrimSpace(rawEmail) == "" {
				message = "validation.required"
			}
			writeLegacyValidationErrors(w, []legacyValidationField{{name: "email", message: message}})
			return
		}
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查邮箱输入", map[string]string{"email": "邮箱格式无效"})
		return
	}
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !settings.MailLoginEnabled {
		writeAPIError(w, http.StatusNotFound, "mail_login_disabled", "邮件链接登录未启用", nil)
		return
	}
	if s.loginLinkProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "login_link_unavailable", "登录链接服务暂不可用", nil)
		return
	}
	redirect := normalizeLoginLinkRedirect(input.Redirect)
	token, err := s.loginLinkProtector.NewToken()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	emailDigest, err := s.loginLinkProtector.EmailDigest(email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	tokenDigest, err := s.loginLinkProtector.TokenDigest(security.LoginLinkPurposeEmail, token)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	ownerID := int64(1)
	expectedUserID := int64(0)
	user, findErr := s.store.FindUserByEmail(r.Context(), email)
	if findErr != nil && !errors.Is(findErr, store.ErrNotFound) {
		handleStoreError(w, findErr)
		return
	}
	if findErr == nil && !user.Banned && user.AccountKind == store.AccountKindHuman {
		ownerID = user.ID
		expectedUserID = user.ID
	}
	tokenCipher, err := s.loginLinkProtector.EncryptToken(ownerID, token)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	_, err = s.store.RequestMailLoginLink(r.Context(), store.MailLoginLinkRequestInput{
		Email: email, ExpectedUserID: expectedUserID, EmailDigest: emailDigest, TokenDigest: tokenDigest, TokenCipher: tokenCipher,
		Redirect: redirect, LinkBaseURL: s.panelURL,
	}, s.now())
	for index := range tokenCipher {
		tokenCipher[index] = 0
	}
	switch {
	case errors.Is(err, store.ErrMailLoginDisabled):
		writeAPIError(w, http.StatusNotFound, "mail_login_disabled", "邮件链接登录未启用", nil)
		return
	case errors.Is(err, store.ErrMailUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	case errors.Is(err, store.ErrMailLoginLimited):
		var limited *store.MailLoginLimitError
		retryAfter := int64(60)
		if errors.As(err, &limited) && limited.RetryAfterSeconds > 0 {
			retryAfter = limited.RetryAfterSeconds
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		message := "登录邮件已发送，请过一会儿再请求"
		if legacyRoute {
			message = "发送频繁，请稍后再试"
		}
		writeAPIError(w, http.StatusTooManyRequests, "mail_login_cooldown", message, nil)
		return
	case err != nil:
		handleStoreError(w, err)
		return
	}
	if legacyRoute {
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	writeSuccess(w, http.StatusAccepted, true)
}

func (s *server) exchangeLoginLink(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	s.exchangeLoginLinkToken(w, r, input.Token, false)
}

func (s *server) legacyTokenToLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if token := r.URL.Query().Get("token"); token != "" {
		if s.loginLinkProtector == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "login_link_unavailable", "登录链接服务暂不可用", nil)
			return
		}
		if _, err := s.loginLinkProtector.TokenDigest(security.LoginLinkPurposeQuick, token); err != nil {
			writeAPIError(w, http.StatusBadRequest, "login_link_invalid", "登录链接无效或已过期", nil)
			return
		}
		http.Redirect(w, r, s.loginLinkURL(token, normalizeLoginLinkRedirect(r.URL.Query().Get("redirect"))), http.StatusFound)
		return
	}
	verify := r.URL.Query().Get("verify")
	if verify == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Invalid request"})
		return
	}
	s.exchangeLoginLinkToken(w, r, verify, true)
}

func (s *server) exchangeLoginLinkToken(w http.ResponseWriter, r *http.Request, token string, issueLegacyAccessToken bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if s.loginLinkProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "login_link_unavailable", "登录链接服务暂不可用", nil)
		return
	}
	quickDigest, err := s.loginLinkProtector.TokenDigest(security.LoginLinkPurposeQuick, token)
	if err != nil {
		if issueLegacyAccessToken {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "令牌有误"})
			return
		}
		writeAPIError(w, http.StatusBadRequest, "login_link_invalid", "登录链接无效或已过期", nil)
		return
	}
	emailDigest, err := s.loginLinkProtector.TokenDigest(security.LoginLinkPurposeEmail, token)
	if err != nil {
		if issueLegacyAccessToken {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "令牌有误"})
			return
		}
		writeAPIError(w, http.StatusBadRequest, "login_link_invalid", "登录链接无效或已过期", nil)
		return
	}
	credentials, err := s.newSessionCredentials()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	var accessToken security.OpaqueToken
	var accessTokenName string
	if issueLegacyAccessToken {
		accessToken, accessTokenName, err = newAccessTokenCredentials("")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
	}
	exchangeInput := store.LoginLinkExchangeInput{
		TokenDigest: quickDigest, AlternateTokenDigest: emailDigest, SessionTokenHash: credentials.token.Digest,
		CSRFHash: credentials.csrf.Digest, SessionExpiresAt: credentials.expiresAt,
		AccessTokenHash: accessToken.Digest, AccessTokenName: accessTokenName,
	}
	exchanged, err := s.store.ExchangeLoginLink(r.Context(), exchangeInput, s.now())
	if errors.Is(err, store.ErrLoginLinkInvalid) {
		if issueLegacyAccessToken {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "令牌有误"})
			return
		}
		writeAPIError(w, http.StatusBadRequest, "login_link_invalid", "登录链接无效或已过期", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	s.setSessionCookies(w, credentials)
	if issueLegacyAccessToken {
		writeJSON(w, http.StatusOK, map[string]any{"data": legacyAuthData(exchanged.User, accessToken.Plaintext)})
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"id": exchanged.User.ID, "email": exchanged.User.Email, "is_admin": exchanged.User.IsAdmin,
		"redirect": exchanged.Redirect,
	})
}

func (s *server) loginLinkURL(token, redirect string) string {
	return s.panelURL + "/#/login?verify=" + url.QueryEscape(token) + "&redirect=" + url.QueryEscape(redirect)
}

func normalizeLoginLinkRedirect(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "#")
	value = strings.TrimPrefix(value, "/")
	value = strings.TrimPrefix(value, "#/")
	value = strings.TrimPrefix(value, "/")
	switch value {
	case "dashboard", "invite", "knowledge", "ticket", "subscribe":
		return value
	default:
		return "dashboard"
	}
}
