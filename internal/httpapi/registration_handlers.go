package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	if !s.registrationRequests.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "registration_rate_limited", "注册请求过于频繁，请稍后重试", nil)
		return
	}
	var input struct {
		Email                string `json:"email"`
		Password             string `json:"password"`
		PasswordConfirmation string `json:"password_confirmation"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	fields := validateRegistration(input.Email, input.Password, input.PasswordConfirmation)
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查注册信息", fields)
		return
	}

	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if settings.StopRegister {
		writeAPIError(w, http.StatusBadRequest, "registration_closed", "本站已关闭注册", nil)
		return
	}
	if _, err := s.store.FindUserByEmail(r.Context(), input.Email); err == nil {
		writeAPIError(w, http.StatusBadRequest, "email_exists", "邮箱已在系统中存在", map[string]string{"email": "邮箱已在系统中存在"})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		handleStoreError(w, err)
		return
	}
	releaseHashSlot, ok := s.beginRegistrationHash()
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "registration_busy", "注册服务繁忙，请稍后重试", nil)
		return
	}
	defer releaseHashSlot()
	passwordHash, err := s.passwordHasher.Hash(input.Password)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	credentials, err := s.newSessionCredentials()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	now := s.now()
	user, err := s.store.RegisterUserWithSession(r.Context(), store.RegisterUserInput{
		Email: input.Email, PasswordHash: passwordHash,
	}, store.RegistrationSessionInput{
		TokenHash: credentials.token.Digest, CSRFHash: credentials.csrf.Digest, ExpiresAt: credentials.expiresAt,
	}, now)
	switch {
	case errors.Is(err, store.ErrRegistrationClosed):
		writeAPIError(w, http.StatusBadRequest, "registration_closed", "本站已关闭注册", nil)
		return
	case errors.Is(err, store.ErrEmailInUse):
		writeAPIError(w, http.StatusBadRequest, "email_exists", "邮箱已在系统中存在", map[string]string{"email": "邮箱已在系统中存在"})
		return
	case err != nil:
		handleStoreError(w, err)
		return
	}
	s.setSessionCookies(w, credentials)
	writeSuccess(w, http.StatusOK, map[string]any{
		"id": user.ID, "email": user.Email, "is_admin": user.IsAdmin,
	})
}

func (s *server) beginRegistrationHash() (func(), bool) {
	select {
	case s.registrationHashSlots <- struct{}{}:
		return func() { <-s.registrationHashSlots }, true
	default:
		return func() {}, false
	}
}

func validateRegistration(email, password, confirmation string) map[string]string {
	fields := make(map[string]string)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 320 || !utf8.ValidString(email) {
		fields["email"] = "邮箱格式无效"
	}
	passwordRunes := utf8.RuneCountInString(password)
	if passwordRunes < 8 {
		fields["password"] = "密码至少需要 8 个字符"
	} else if len(password) > 1024 {
		fields["password"] = "密码不得超过 1024 个字节"
	}
	if confirmation != password {
		fields["password_confirmation"] = "两次输入的密码不一致"
	}
	return fields
}
