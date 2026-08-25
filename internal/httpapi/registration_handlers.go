package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	s.registerAccount(w, r, false)
}

func (s *server) legacyRegister(w http.ResponseWriter, r *http.Request) {
	s.registerAccount(w, r, true)
}

func (s *server) registerAccount(w http.ResponseWriter, r *http.Request, legacy bool) {
	sourceIP := requestIP(r)
	if !s.registrationRequests.take(sourceIP, s.now()) {
		w.Header().Set("Retry-After", "900")
		writeAPIError(w, http.StatusTooManyRequests, "registration_rate_limited", "注册请求过于频繁，请稍后重试", nil)
		return
	}
	var input struct {
		Email                string `json:"email"`
		EmailCode            string `json:"email_code"`
		Password             string `json:"password"`
		PasswordConfirmation string `json:"password_confirmation"`
		InvitationCode       string `json:"invite_code"`
		RecaptchaData        string `json:"recaptcha_data"`
		RecaptchaV3Token     string `json:"recaptcha_v3_token"`
		TurnstileToken       string `json:"turnstile_token"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if legacy && input.PasswordConfirmation == "" {
		input.PasswordConfirmation = input.Password
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
	if err := s.store.CheckRegistrationIPLimit(r.Context(), settings, sourceIP, s.now()); err != nil {
		if s.writeRegistrationPolicyError(w, err) {
			return
		}
		handleStoreError(w, err)
		return
	}
	if !s.verifyCaptcha(w, r, settings, captchaTokens{Recaptcha: input.RecaptchaData, RecaptchaV3: input.RecaptchaV3Token, Turnstile: input.TurnstileToken}, "register") {
		return
	}
	if err := store.CheckRegistrationEmailPolicy(settings, input.Email); err != nil {
		if s.writeRegistrationPolicyError(w, err) {
			return
		}
		handleStoreError(w, err)
		return
	}
	if settings.StopRegister {
		writeAPIError(w, http.StatusBadRequest, "registration_closed", "本站已关闭注册", nil)
		return
	}
	var invitationCodeDigest []byte
	if settings.InvitationForceEnabled && input.InvitationCode == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "invitation_code_required", "必须使用邀请码才可以注册", map[string]string{
			"invite_code": "必须使用邀请码才可以注册",
		})
		return
	}
	var emailDigest, emailCodeDigest []byte
	if settings.EmailVerificationEnabled {
		if !validSixDigitEmailCode(input.EmailCode) {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "请检查注册信息", map[string]string{
				"email_code": "邮箱验证码必须为 6 位数字",
			})
			return
		}
		if s.registrationEmailProtector == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "mail_unavailable", "邮件服务暂不可用", nil)
			return
		}
		emailDigest, err = s.registrationEmailProtector.EmailDigest(input.Email)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		emailCodeDigest, err = s.registrationEmailProtector.CodeDigest(input.Email, input.EmailCode)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
		if err := s.store.CheckRegistrationEmailVerification(r.Context(), emailDigest, emailCodeDigest, s.now()); err != nil {
			s.writeRegistrationEmailChallengeError(w, err)
			return
		}
	}
	if _, err := s.store.FindUserByEmail(r.Context(), input.Email); err == nil {
		writeAPIError(w, http.StatusBadRequest, "email_exists", "邮箱已在系统中存在", map[string]string{"email": "邮箱已在系统中存在"})
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		handleStoreError(w, err)
		return
	}
	if input.InvitationCode != "" {
		if !security.ValidInvitationCode(input.InvitationCode) {
			if settings.InvitationForceEnabled {
				writeAPIError(w, http.StatusBadRequest, "invitation_code_invalid", "邀请码无效", nil)
				return
			}
		} else {
			if s.invitationProtector == nil {
				if settings.InvitationForceEnabled {
					writeAPIError(w, http.StatusServiceUnavailable, "invitation_unavailable", "邀请码服务暂不可用", nil)
					return
				}
			} else {
				invitationCodeDigest, err = s.invitationProtector.CodeDigest(input.InvitationCode)
				if err != nil {
					writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
					return
				}
				if settings.InvitationForceEnabled {
					if err := s.store.CheckInvitationCode(r.Context(), invitationCodeDigest); err != nil {
						if errors.Is(err, store.ErrInvitationCodeInvalid) {
							writeAPIError(w, http.StatusBadRequest, "invitation_code_invalid", "邀请码无效", nil)
							return
						}
						handleStoreError(w, err)
						return
					}
				}
			}
		}
	}
	releaseHashSlot, ok := s.beginPasswordHash()
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
	var accessToken security.OpaqueToken
	var accessTokenName string
	if legacy {
		accessToken, accessTokenName, err = newAccessTokenCredentials("")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
			return
		}
	}
	now := s.now()
	user, err := s.store.RegisterUserWithSession(r.Context(), store.RegisterUserInput{
		Email: input.Email, PasswordHash: passwordHash, SourceIP: sourceIP,
		EmailDigest: emailDigest, EmailCodeDigest: emailCodeDigest, InvitationCodeDigest: invitationCodeDigest,
	}, store.RegistrationSessionInput{
		TokenHash: credentials.token.Digest, CSRFHash: credentials.csrf.Digest, ExpiresAt: credentials.expiresAt,
		AccessTokenHash: accessToken.Digest, AccessTokenName: accessTokenName,
	}, now)
	switch {
	case errors.Is(err, store.ErrRegistrationClosed):
		writeAPIError(w, http.StatusBadRequest, "registration_closed", "本站已关闭注册", nil)
		return
	case errors.Is(err, store.ErrEmailInUse):
		writeAPIError(w, http.StatusBadRequest, "email_exists", "邮箱已在系统中存在", map[string]string{"email": "邮箱已在系统中存在"})
		return
	case errors.Is(err, store.ErrInvitationCodeRequired):
		writeAPIError(w, http.StatusUnprocessableEntity, "invitation_code_required", "必须使用邀请码才可以注册", map[string]string{
			"invite_code": "必须使用邀请码才可以注册",
		})
		return
	case errors.Is(err, store.ErrInvitationCodeInvalid):
		writeAPIError(w, http.StatusBadRequest, "invitation_code_invalid", "邀请码无效", nil)
		return
	case errors.Is(err, store.ErrEmailDomainNotAllowed), errors.Is(err, store.ErrGmailAliasNotAllowed), errors.Is(err, store.ErrRegistrationIPLimited):
		s.writeRegistrationPolicyError(w, err)
		return
	case errors.Is(err, store.ErrRegistrationEmailVerificationInvalid), errors.Is(err, store.ErrRegistrationEmailVerificationLocked), errors.Is(err, store.ErrRegistrationEmailVerificationDisabled):
		s.writeRegistrationEmailChallengeError(w, err)
		return
	case err != nil:
		handleStoreError(w, err)
		return
	}
	s.setSessionCookies(w, credentials)
	if legacy {
		writeSuccess(w, http.StatusOK, legacyAuthData(user, accessToken.Plaintext))
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{
		"id": user.ID, "email": user.Email, "is_admin": user.IsAdmin,
	})
}

func (s *server) writeRegistrationEmailChallengeError(w http.ResponseWriter, err error) {
	var locked *store.RegistrationEmailVerificationLockedError
	if errors.As(err, &locked) {
		retryAfter := locked.RetryAfterSeconds
		if retryAfter < 1 {
			retryAfter = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		writeAPIError(w, http.StatusTooManyRequests, "registration_email_locked", "注册失败，请稍后再试", nil)
		return
	}
	if errors.Is(err, store.ErrRegistrationEmailVerificationDisabled) {
		writeAPIError(w, http.StatusConflict, "registration_email_disabled", "注册邮箱验证未启用", nil)
		return
	}
	writeAPIError(w, http.StatusBadRequest, "registration_email_invalid", "邮箱验证码有误", nil)
}

func (s *server) writeRegistrationPolicyError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrEmailDomainNotAllowed):
		writeAPIError(w, http.StatusBadRequest, "email_domain_not_allowed", "邮箱后缀不处于白名单中", map[string]string{"email": "邮箱后缀不处于白名单中"})
		return true
	case errors.Is(err, store.ErrGmailAliasNotAllowed):
		writeAPIError(w, http.StatusBadRequest, "gmail_alias_not_allowed", "不支持 Gmail 别名邮箱", map[string]string{"email": "不支持 Gmail 别名邮箱"})
		return true
	}
	var limited *store.RegistrationIPLimitError
	if errors.As(err, &limited) {
		w.Header().Set("Retry-After", strconv.FormatInt(limited.RetryAfterSeconds, 10))
		writeAPIError(w, http.StatusTooManyRequests, "registration_ip_limited", fmt.Sprintf("注册频繁，请等待 %d 分钟后再次尝试", limited.WindowMinutes), nil)
		return true
	}
	return false
}

func (s *server) beginPasswordHash() (func(), bool) {
	select {
	case s.passwordHashSlots <- struct{}{}:
		return func() { <-s.passwordHashSlots }, true
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

func validSixDigitEmailCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
