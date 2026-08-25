package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type legacyEmailVerificationInput struct {
	Email            string `json:"email"`
	RecaptchaData    string `json:"recaptcha_data"`
	RecaptchaV3Token string `json:"recaptcha_v3_token"`
	TurnstileToken   string `json:"turnstile_token"`
}

func (s *server) legacySendEmailVerify(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.passportEmailRequests.take(requestIP(r), now) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "passport_email_rate_limited", "请求过于频繁，请稍后重试", nil)
		return
	}
	var input legacyEmailVerificationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validPasswordResetEmail(email) {
		writeLegacyValidationErrors(w, []legacyValidationField{{name: "email", message: "邮箱格式不正确"}})
		return
	}
	settings, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if !s.verifyCaptcha(w, r, settings, captchaTokens{
		Recaptcha: input.RecaptchaData, RecaptchaV3: input.RecaptchaV3Token, Turnstile: input.TurnstileToken,
	}, "sendEmailVerify") {
		return
	}
	if _, err := s.store.FindUserByEmail(r.Context(), email); err == nil {
		s.issuePasswordReset(w, r, email, http.StatusOK, http.StatusBadRequest, "passport_email_cooldown", true)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		handleStoreError(w, err)
		return
	}
	registrationEligible := settings.EmailVerificationEnabled && !settings.StopRegister
	if registrationEligible {
		if err := store.CheckRegistrationEmailPolicy(settings, email); err != nil {
			if !errors.Is(err, store.ErrEmailDomainNotAllowed) && !errors.Is(err, store.ErrGmailAliasNotAllowed) {
				handleStoreError(w, err)
				return
			}
			registrationEligible = false
		}
	}
	if registrationEligible {
		if err := s.store.CheckRegistrationIPLimit(r.Context(), settings, requestIP(r), now); err != nil {
			if !errors.Is(err, store.ErrRegistrationIPLimited) {
				handleStoreError(w, err)
				return
			}
			registrationEligible = false
		}
	}
	if !registrationEligible {
		// Keep the public response and persistent cooldown indistinguishable from
		// an existing account without sending mail to an unusable address.
		s.issuePasswordReset(w, r, email, http.StatusOK, http.StatusBadRequest, "passport_email_cooldown", true)
		return
	}
	s.issueRegistrationEmailVerification(w, r, email, requestIP(r), http.StatusOK, http.StatusBadRequest, "passport_email_cooldown", true)
}

func (s *server) legacyForgetPassword(w http.ResponseWriter, r *http.Request) {
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
	fields := make([]legacyValidationField, 0, 3)
	if !validPasswordResetEmail(email) {
		fields = append(fields, legacyValidationField{name: "email", message: "邮箱格式不正确"})
	}
	if utf8.RuneCountInString(input.Password) < 8 {
		fields = append(fields, legacyValidationField{name: "password", message: "密码必须大于 8 个字符"})
	} else if len(input.Password) > 1024 {
		fields = append(fields, legacyValidationField{name: "password", message: "密码不得超过 1024 个字节"})
	}
	if !validSixDigitEmailCode(input.EmailCode) {
		fields = append(fields, legacyValidationField{name: "email_code", message: "邮箱验证码有误"})
	}
	if len(fields) > 0 {
		writeLegacyValidationErrors(w, fields)
		return
	}
	s.confirmPasswordResetInput(w, r, email, input.EmailCode, input.Password, true)
}

type legacyValidationField struct {
	name    string
	message string
}

func validateLegacyEmailPassword(email, password string) []legacyValidationField {
	fields := make([]legacyValidationField, 0, 2)
	if email == "" {
		fields = append(fields, legacyValidationField{name: "email", message: "邮箱不能为空"})
	} else if !validPasswordResetEmail(email) {
		fields = append(fields, legacyValidationField{name: "email", message: "邮箱格式不正确"})
	}
	if password == "" {
		fields = append(fields, legacyValidationField{name: "password", message: "密码不能为空"})
	} else if utf8.RuneCountInString(password) < 8 {
		fields = append(fields, legacyValidationField{name: "password", message: "密码必须大于 8 个字符"})
	} else if len(password) > 1024 {
		fields = append(fields, legacyValidationField{name: "password", message: "密码不得超过 1024 个字节"})
	}
	return fields
}

func writeLegacyValidationErrors(w http.ResponseWriter, fields []legacyValidationField) {
	errorsByField := make(map[string][]string, len(fields))
	for _, field := range fields {
		errorsByField[field.name] = []string{field.message}
	}
	message := fields[0].message
	if len(fields) > 1 {
		remaining := len(fields) - 1
		noun := "errors"
		if remaining == 1 {
			noun = "error"
		}
		message = fmt.Sprintf("%s (and %d more %s)", message, remaining, noun)
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"message": message,
		"errors":  errorsByField,
	})
}

func writeLegacySuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{
		"status":  "success",
		"message": "操作成功",
		"data":    data,
		"error":   nil,
	})
}
