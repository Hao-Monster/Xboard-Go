package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) requestRegistrationEmailVerification(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	sourceIP := requestIP(r)
	if !s.registrationEmailRequests.take(sourceIP, now) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "registration_email_rate_limited", "请求过于频繁，请稍后重试", nil)
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
	s.issueRegistrationEmailVerification(w, r, email, sourceIP, http.StatusAccepted, http.StatusTooManyRequests, "registration_email_cooldown", false)
}

func (s *server) issueRegistrationEmailVerification(w http.ResponseWriter, r *http.Request, email, sourceIP string, successStatus, cooldownStatus int, cooldownCode string, legacy bool) {
	now := s.now()
	if s.registrationEmailProtector == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	}
	code, err := s.registrationEmailProtector.NewCode()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	emailDigest, err := s.registrationEmailProtector.EmailDigest(email)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	codeDigest, err := s.registrationEmailProtector.CodeDigest(email, code)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	codeCipher, err := s.registrationEmailProtector.EncryptCode(email, code)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
		return
	}
	_, err = s.store.RequestRegistrationEmailVerification(r.Context(), store.RegistrationEmailVerificationRequestInput{
		Email: email, SourceIP: sourceIP, EmailDigest: emailDigest, CodeDigest: codeDigest, CodeCipher: codeCipher,
	}, now)
	for index := range codeCipher {
		codeCipher[index] = 0
	}
	switch {
	case errors.Is(err, store.ErrRegistrationEmailVerificationLimited):
		var limited *store.RegistrationEmailVerificationLimitError
		retryAfter := int64(60)
		if errors.As(err, &limited) && limited.RetryAfterSeconds > 0 {
			retryAfter = limited.RetryAfterSeconds
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeAPIError(w, cooldownStatus, cooldownCode, "验证码已发送，请过一会儿再请求", nil)
		return
	case errors.Is(err, store.ErrRegistrationEmailVerificationDisabled):
		writeAPIError(w, http.StatusConflict, "registration_email_disabled", "注册邮箱验证未启用", nil)
		return
	case errors.Is(err, store.ErrRegistrationClosed):
		writeAPIError(w, http.StatusBadRequest, "registration_closed", "本站已关闭注册", nil)
		return
	case errors.Is(err, store.ErrMailUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
		return
	case errors.Is(err, store.ErrEmailDomainNotAllowed), errors.Is(err, store.ErrGmailAliasNotAllowed), errors.Is(err, store.ErrRegistrationIPLimited):
		s.writeRegistrationPolicyError(w, err)
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
