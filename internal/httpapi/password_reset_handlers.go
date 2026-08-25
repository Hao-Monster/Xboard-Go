package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !s.passwordResetRequests.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "password_reset_rate_limited", "请求过于频繁，请稍后重试", nil)
		return
	}
	var input struct {
		Email            string `json:"email"`
		RecaptchaData    string `json:"recaptcha_data"`
		RecaptchaV3Token string `json:"recaptcha_v3_token"`
		TurnstileToken   string `json:"turnstile_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validPasswordResetEmail(email) {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查邮箱输入", map[string]string{"email": "邮箱格式无效"})
		return
	}
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !s.verifyCaptcha(w, r, settings, captchaTokens{Recaptcha: input.RecaptchaData, RecaptchaV3: input.RecaptchaV3Token, Turnstile: input.TurnstileToken}, "sendEmailVerify") {
		return
	}
	s.issuePasswordReset(w, r, email, http.StatusAccepted, http.StatusTooManyRequests, "password_reset_cooldown", false)
}

func (s *server) issuePasswordReset(w http.ResponseWriter, r *http.Request, email string, successStatus, cooldownStatus int, cooldownCode string, legacy bool) {
	if s.passwordResetProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	}
	code, err := s.passwordResetProtector.NewCode()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	emailDigest, err := s.passwordResetProtector.EmailDigest(email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	codeDigest, err := s.passwordResetProtector.CodeDigest(email, code)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	codeCipher, err := s.passwordResetProtector.EncryptCode(email, code)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	_, err = s.store.RequestPasswordReset(r.Context(), store.PasswordResetRequestInput{
		Email: email, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, s.now())
	for index := range codeCipher {
		codeCipher[index] = 0
	}
	switch {
	case errors.Is(err, store.ErrMailUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	case errors.Is(err, store.ErrPasswordResetLimited):
		var limited *store.PasswordResetLimitError
		retryAfter := int64(60)
		if errors.As(err, &limited) && limited.RetryAfterSeconds > 0 {
			retryAfter = limited.RetryAfterSeconds
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeAPIError(w, cooldownStatus, cooldownCode, "验证码已发送，请过一会儿再请求", nil)
		return
	case err != nil:
		handleStoreError(w, err)
		return
	}
	if legacy {
		writeLegacySuccess(w, successStatus, true)
		return
	}
	writeSuccess(w, successStatus, true)
}

func (s *server) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	if !s.passwordResetConfirmations.take(requestIP(r), s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "password_reset_rate_limited", "重置失败，请稍后再试", nil)
		return
	}
	var input struct {
		Email     string `json:"email"`
		EmailCode string `json:"email_code"`
		Password  string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	fields := validatePasswordResetConfirmation(email, input.EmailCode, input.Password)
	if len(fields) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查重置信息", fields)
		return
	}
	s.confirmPasswordResetInput(w, r, email, input.EmailCode, input.Password, false)
}

func (s *server) confirmPasswordResetInput(w http.ResponseWriter, r *http.Request, email, emailCode, password string, legacy bool) {
	if s.passwordResetProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	}
	emailDigest, err := s.passwordResetProtector.EmailDigest(email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	codeDigest, err := s.passwordResetProtector.CodeDigest(email, emailCode)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	challenge, err := s.store.CheckPasswordResetChallenge(r.Context(), emailDigest, codeDigest, s.now())
	if err != nil {
		s.writePasswordResetChallengeError(w, err)
		return
	}
	releaseHashSlot, ok := s.beginPasswordHash()
	if !ok {
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusTooManyRequests, "password_reset_busy", "重置服务繁忙，请稍后重试", nil)
		return
	}
	defer releaseHashSlot()
	passwordHash, err := s.passwordHasher.Hash(password)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	err = s.store.ResetPasswordWithChallenge(r.Context(), emailDigest, codeDigest, challenge, passwordHash, s.now())
	if err != nil {
		if errors.Is(err, store.ErrPasswordResetInvalid) || errors.Is(err, store.ErrPasswordResetLocked) || errors.Is(err, store.ErrConflict) {
			s.writePasswordResetChallengeError(w, err)
			return
		}
		handleStoreError(w, err)
		return
	}
	s.clearAuthCookies(w)
	if legacy {
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}

func (s *server) writePasswordResetChallengeError(w http.ResponseWriter, err error) {
	var locked *store.PasswordResetLockedError
	if errors.As(err, &locked) {
		retryAfter := locked.RetryAfterSeconds
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeAPIError(w, http.StatusTooManyRequests, "password_reset_locked", "重置失败，请稍后再试", nil)
		return
	}
	writeAPIError(w, http.StatusBadRequest, "password_reset_invalid", "邮箱验证码有误", nil)
}

func validPasswordResetEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && len(email) <= 320 && utf8.ValidString(email)
}

func validatePasswordResetConfirmation(email, code, password string) map[string]string {
	fields := make(map[string]string)
	if !validPasswordResetEmail(email) {
		fields["email"] = "邮箱格式无效"
	}
	if len(code) != 6 {
		fields["email_code"] = "邮箱验证码必须为 6 位数字"
	} else {
		for _, character := range code {
			if character < '0' || character > '9' {
				fields["email_code"] = "邮箱验证码必须为 6 位数字"
				break
			}
		}
	}
	passwordRunes := utf8.RuneCountInString(password)
	if passwordRunes < 8 {
		fields["password"] = "密码至少需要 8 个字符"
	} else if len(password) > 1024 {
		fields["password"] = "密码不得超过 1024 个字节"
	}
	return fields
}
