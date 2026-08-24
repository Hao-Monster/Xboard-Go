package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const maxSMTPPasswordBytes = 4 << 10

func (s *server) getTicketSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetTicketSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, settings)
}

func (s *server) updateTicketSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision            int64   `json:"revision"`
		AppName             string  `json:"app_name"`
		AppURL              string  `json:"app_url"`
		TicketMustWaitReply bool    `json:"ticket_must_wait_reply"`
		SMTPEnabled         bool    `json:"smtp_enabled"`
		SMTPHost            string  `json:"smtp_host"`
		SMTPPort            int     `json:"smtp_port"`
		SMTPUsername        string  `json:"smtp_username"`
		SMTPPassword        *string `json:"smtp_password"`
		SMTPEncryption      string  `json:"smtp_encryption"`
		SMTPFromAddress     string  `json:"smtp_from_address"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.SMTPEnabled && strings.EqualFold(strings.TrimSpace(input.SMTPEncryption), "none") && !s.smtpAllowInsecure {
		writeAPIError(w, http.StatusUnprocessableEntity, "insecure_smtp_disabled", "未加密 SMTP 仅允许在显式启用的本地测试环境使用", map[string]string{"smtp_encryption": "必须使用 STARTTLS 或 TLS"})
		return
	}
	current, err := s.store.GetTicketSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	save := store.SaveTicketSettingsInput{
		AppName: input.AppName, AppURL: input.AppURL, TicketMustWaitReply: input.TicketMustWaitReply,
		SMTPEnabled: input.SMTPEnabled, SMTPHost: input.SMTPHost, SMTPPort: input.SMTPPort,
		SMTPUsername: input.SMTPUsername, SMTPEncryption: input.SMTPEncryption, SMTPFromAddress: input.SMTPFromAddress,
	}
	passwordAvailable := current.SMTPPasswordSet
	if input.SMTPPassword != nil {
		password := *input.SMTPPassword
		if len(password) > maxSMTPPasswordBytes || strings.ContainsRune(password, 0) {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "SMTP 密码格式无效", map[string]string{"smtp_password": "不得超过 4096 字节或包含空字符"})
			return
		}
		save.ReplaceSMTPPassword = true
		passwordAvailable = password != ""
		if password != "" {
			if s.settingsCipher == nil {
				writeAPIError(w, http.StatusServiceUnavailable, "settings_encryption_unavailable", "服务器未配置设置加密密钥", nil)
				return
			}
			save.SMTPPasswordCipher, err = s.settingsCipher.Encrypt([]byte(password))
			if err != nil {
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误", nil)
				return
			}
		}
	}
	if input.SMTPEnabled && strings.TrimSpace(input.SMTPUsername) != "" && !passwordAvailable {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "启用 SMTP 用户名时必须设置密码", map[string]string{"smtp_password": "请输入 SMTP 密码"})
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.UpdateTicketSettings(r.Context(), session.UserID, input.Revision, save, s.now())
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "settings_conflict", "设置已被其他管理员修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrRegistrationEmailVerificationNeedsMail) {
		writeAPIError(w, http.StatusConflict, "registration_email_requires_smtp", "注册邮箱验证启用期间不能关闭 SMTP 邮件服务", nil)
		return
	}
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}
