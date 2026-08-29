package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type mailSettingsRequest struct {
	Revision          int64   `json:"revision"`
	SMTPEnabled       bool    `json:"smtp_enabled"`
	SMTPHost          string  `json:"smtp_host"`
	SMTPPort          int     `json:"smtp_port"`
	SMTPUsername      string  `json:"smtp_username"`
	SMTPPassword      *string `json:"smtp_password"`
	SMTPEncryption    string  `json:"smtp_encryption"`
	SMTPFromAddress   string  `json:"smtp_from_address"`
	RemindMailEnabled bool    `json:"remind_mail_enable"`
}

func (s *server) getMailSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetMailSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateMailSettings(w http.ResponseWriter, r *http.Request) {
	var input mailSettingsRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetMailSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	save, err := s.prepareMailSettingsSave(input, current)
	if err != nil {
		s.writeMailSettingsPreparationError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateMailSettings(r.Context(), session.UserID, input.Revision, save, s.now())
	if writeMailSettingsStoreError(w, err) {
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

var (
	errSMTPPasswordInvalid       = errors.New("SMTP password is invalid")
	errSMTPPasswordRequired      = errors.New("SMTP password is required")
	errSMTPEncryptionUnavailable = errors.New("settings encryption is unavailable")
	errSMTPInsecureDisabled      = errors.New("cleartext SMTP is disabled")
)

func (s *server) prepareMailSettingsSave(input mailSettingsRequest, current store.MailSettings) (store.SaveMailSettingsInput, error) {
	if input.SMTPEnabled && strings.EqualFold(strings.TrimSpace(input.SMTPEncryption), "none") && !s.smtpAllowInsecure {
		return store.SaveMailSettingsInput{}, errSMTPInsecureDisabled
	}
	save := store.SaveMailSettingsInput{
		SMTPEnabled: input.SMTPEnabled, SMTPHost: input.SMTPHost, SMTPPort: input.SMTPPort,
		SMTPUsername: input.SMTPUsername, SMTPEncryption: input.SMTPEncryption,
		SMTPFromAddress: input.SMTPFromAddress, RemindMailEnabled: input.RemindMailEnabled,
	}
	passwordAvailable := current.SMTPPasswordSet
	if input.SMTPPassword != nil {
		password := *input.SMTPPassword
		if len(password) > maxSMTPPasswordBytes || strings.ContainsRune(password, 0) {
			return store.SaveMailSettingsInput{}, errSMTPPasswordInvalid
		}
		save.ReplaceSMTPPassword = true
		passwordAvailable = password != ""
		if password != "" {
			if s.settingsCipher == nil {
				return store.SaveMailSettingsInput{}, errSMTPEncryptionUnavailable
			}
			ciphertext, err := s.settingsCipher.Encrypt([]byte(password))
			if err != nil {
				return store.SaveMailSettingsInput{}, errSMTPEncryptionUnavailable
			}
			save.SMTPPasswordCipher = ciphertext
		}
	}
	if input.SMTPEnabled && strings.TrimSpace(input.SMTPUsername) != "" && !passwordAvailable {
		return store.SaveMailSettingsInput{}, errSMTPPasswordRequired
	}
	return save, nil
}

func (s *server) writeMailSettingsPreparationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSMTPInsecureDisabled):
		writeAPIError(w, http.StatusUnprocessableEntity, "insecure_smtp_disabled", "未加密 SMTP 仅允许在显式启用的本地测试环境使用", map[string]string{"smtp_encryption": "必须使用 STARTTLS 或 TLS"})
	case errors.Is(err, errSMTPPasswordInvalid):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "SMTP 密码格式无效", map[string]string{"smtp_password": "不得超过 4096 字节或包含空字符"})
	case errors.Is(err, errSMTPPasswordRequired):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "启用 SMTP 用户名时必须设置密码", map[string]string{"smtp_password": "请输入 SMTP 密码"})
	default:
		writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置设置加密密钥", nil)
	}
}

func writeMailSettingsStoreError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrConflict):
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
	case errors.Is(err, store.ErrRegistrationEmailVerificationNeedsMail):
		writeAPIError(w, http.StatusConflict, "registration_email_requires_smtp", "注册邮箱验证启用期间不能关闭 SMTP 邮件服务", nil)
	case errors.Is(err, store.ErrMailLoginNeedsMail):
		writeAPIError(w, http.StatusConflict, "mail_login_requires_smtp", "邮件链接登录启用期间不能关闭 SMTP 邮件服务", nil)
	case errors.Is(err, store.ErrInvalidInput):
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "邮件设置无效", nil)
	default:
		handleStoreError(w, err)
	}
	return true
}

func (s *server) testMailSettings(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	if !s.smtpTestRequests.take(strconv.FormatInt(session.UserID, 10), s.now()) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "测试邮件发送过于频繁，请稍后重试", nil)
		return
	}
	if err := s.sendTestMail(r.Context(), session.Email); err != nil {
		s.writeSMTPTestError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"recipient": session.Email})
}

var (
	errSMTPTestNotConfigured = errors.New("SMTP is not configured")
	errSMTPTestUnavailable   = errors.New("SMTP test service is unavailable")
	errSMTPTestDelivery      = errors.New("SMTP test delivery failed")
)

func (s *server) sendTestMail(ctx context.Context, recipient string) error {
	if s.mailSender == nil {
		return errSMTPTestUnavailable
	}
	siteSettings, err := s.store.GetSiteSettings(ctx)
	if err != nil {
		return err
	}
	configuration, err := s.loadSMTPConfiguration(ctx)
	if err != nil {
		return err
	}
	defer func() { configuration.Password = "" }()
	body := "This is xboard test email\r\nSite: " + siteSettings.AppName
	if siteSettings.AppURL != "" {
		body += "\r\nURL: " + siteSettings.AppURL
	}
	if err := s.mailSender.Send(ctx, configuration, mailer.Message{
		To: recipient, Subject: "This is xboard test email", Text: body,
	}); err != nil {
		// SMTP servers may echo recipient addresses or attacker-controlled response
		// text. Keep the operational event observable without persisting that data.
		s.logger.Warn("SMTP test delivery failed", "reason", "delivery_failed")
		return errSMTPTestDelivery
	}
	return nil
}

func (s *server) writeSMTPTestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSMTPTestNotConfigured):
		writeAPIError(w, http.StatusConflict, "smtp_not_configured", "请先保存并启用 SMTP 邮件服务", nil)
	case errors.Is(err, errSMTPTestUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, "smtp_test_unavailable", "测试邮件服务暂时不可用", nil)
	case errors.Is(err, errSMTPTestDelivery):
		writeAPIError(w, http.StatusBadGateway, "smtp_test_failed", "测试邮件发送失败，请检查 SMTP 配置", nil)
	default:
		handleStoreError(w, err)
	}
}

func legacyMailSettings(settings store.MailSettings) map[string]any {
	var host, port, username, encryption, fromAddress any
	if settings.SMTPHost != "" {
		host = settings.SMTPHost
		port = fmt.Sprintf("%d", settings.SMTPPort)
		username = settings.SMTPUsername
		encryption = legacySMTPEncryption(settings.SMTPEncryption)
		fromAddress = settings.SMTPFromAddress
	}
	return map[string]any{
		"email_host": host, "email_port": port, "email_username": username,
		"email_password": "", "email_encryption": encryption,
		"email_from_address": fromAddress, "remind_mail_enable": settings.RemindMailEnabled,
	}
}

func legacySMTPEncryption(value string) string {
	switch value {
	case mailer.EncryptionTLS:
		return "ssl"
	case mailer.EncryptionStartTLS:
		return "tls"
	default:
		return ""
	}
}

func modernSMTPEncryption(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ssl":
		return mailer.EncryptionTLS, true
	case "tls":
		return mailer.EncryptionStartTLS, true
	case "", "none":
		return mailer.EncryptionNone, true
	case mailer.EncryptionStartTLS:
		return mailer.EncryptionStartTLS, true
	default:
		return "", false
	}
}
