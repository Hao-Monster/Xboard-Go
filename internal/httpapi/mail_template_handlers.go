package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

type mailTemplateSaveRequest struct {
	Revision int64  `json:"revision"`
	Subject  string `json:"subject"`
	Content  string `json:"content"`
}

type mailTemplatePreviewRequest struct {
	Subject string `json:"subject"`
	Content string `json:"content"`
}

type mailTemplateTestRequest struct {
	Email string `json:"email"`
}

type legacyMailTemplateRequest struct {
	Name    mailtemplate.Name `json:"name"`
	Subject string            `json:"subject"`
	Content string            `json:"content"`
	Email   string            `json:"email"`
}

func (s *server) listMailTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.store.ListMailTemplateSummaries(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, templates)
}

func (s *server) getMailTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := s.store.GetMailTemplate(r.Context(), mailtemplate.Name(r.PathValue("name")))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, template)
}

func (s *server) updateMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input mailTemplateSaveRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	template, err := s.store.UpdateMailTemplate(r.Context(), session.UserID, mailtemplate.Name(r.PathValue("name")), input.Revision, store.SaveMailTemplateInput{
		Subject: input.Subject, Content: input.Content,
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, template)
}

func (s *server) resetMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	template, err := s.store.ResetMailTemplate(r.Context(), session.UserID, mailtemplate.Name(r.PathValue("name")), input.Revision, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, template)
}

func (s *server) previewMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input mailTemplatePreviewRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	name := mailtemplate.Name(r.PathValue("name"))
	site, err := s.store.GetSiteSettings(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	rendered, err := mailtemplate.Render(mailtemplate.Template{Name: name, Subject: input.Subject, Content: input.Content}, s.mailTemplateTestValues(name, site))
	if err != nil {
		handleStoreError(w, fmtInvalidInput(err))
		return
	}
	writeSuccess(w, http.StatusOK, rendered)
}

func (s *server) testMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input mailTemplateTestRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	name := mailtemplate.Name(r.PathValue("name"))
	session, _ := sessionFromContext(r.Context())
	if input.Email == "" {
		input.Email = session.Email
	}
	if !validMailTemplateRecipient(input.Email) {
		handleStoreError(w, store.ErrInvalidInput)
		return
	}
	if !s.smtpTestRequests.take("template:"+string(name)+":"+strconv.FormatInt(session.UserID, 10), s.now()) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "测试邮件发送过于频繁，请稍后重试", nil)
		return
	}
	if err := s.sendMailTemplateTest(r.Context(), name, input.Email); err != nil {
		s.writeSMTPTestError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"recipient": input.Email})
}

func (s *server) sendMailTemplateTest(ctx context.Context, name mailtemplate.Name, recipient string) error {
	if s.mailSender == nil {
		return errSMTPTestUnavailable
	}
	template, err := s.store.GetMailTemplate(ctx, name)
	if err != nil {
		return err
	}
	site, err := s.store.GetSiteSettings(ctx)
	if err != nil {
		return err
	}
	subject := template.Subject
	if !template.Customized {
		var ok bool
		subject, ok = mailtemplate.TestSubject(name, strings.TrimSpace(site.AppName))
		if !ok {
			return store.ErrNotFound
		}
	}
	rendered, err := mailtemplate.Render(mailtemplate.Template{Name: template.Name, Subject: subject, Content: template.Content}, s.mailTemplateTestValues(name, site))
	if err != nil {
		return err
	}
	configuration, err := s.loadSMTPConfiguration(ctx)
	if err != nil {
		return err
	}
	defer func() { configuration.Password = "" }()
	if err := s.mailSender.Send(ctx, configuration, mailer.Message{To: recipient, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML}); err != nil {
		s.logger.Warn("SMTP template test delivery failed", "template", name, "reason", "delivery_failed")
		return errSMTPTestDelivery
	}
	return nil
}

func (s *server) loadSMTPConfiguration(ctx context.Context) (mailer.SMTPConfig, error) {
	settings, err := s.store.GetMailSettings(ctx)
	if err != nil {
		return mailer.SMTPConfig{}, err
	}
	if !settings.SMTPEnabled || settings.SMTPHost == "" || settings.SMTPFromAddress == "" {
		return mailer.SMTPConfig{}, errSMTPTestNotConfigured
	}
	configuration := mailer.SMTPConfig{
		Host: settings.SMTPHost, Port: settings.SMTPPort, Username: settings.SMTPUsername,
		Encryption: settings.SMTPEncryption, FromAddress: settings.SMTPFromAddress,
	}
	ciphertext, err := s.store.GetSMTPPasswordCipher(ctx)
	if err != nil {
		return mailer.SMTPConfig{}, err
	}
	if len(ciphertext) > 0 {
		if s.settingsCipher == nil {
			return mailer.SMTPConfig{}, errSMTPTestUnavailable
		}
		plaintext, err := s.settingsCipher.Decrypt(ciphertext)
		if err != nil {
			return mailer.SMTPConfig{}, errSMTPTestUnavailable
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	return configuration, nil
}

func (s *server) mailTemplateTestValues(name mailtemplate.Name, site store.SiteSettings) map[string]string {
	appURL := strings.TrimRight(strings.TrimSpace(site.AppURL), "/")
	if appURL == "" {
		appURL = s.panelURL
	}
	values := map[string]string{"name": site.AppName, "url": appURL}
	switch name {
	case mailtemplate.Verify:
		values["code"] = "123456"
	case mailtemplate.Notify:
		values["content"] = "这是一封测试通知邮件。"
	case mailtemplate.MailLogin:
		values["link"] = appURL + "/login?token=test-token"
	}
	return values
}

func validMailTemplateRecipient(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 320 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(address.Address, "@")
}

func fmtInvalidInput(err error) error {
	return errors.Join(store.ErrInvalidInput, err)
}

func (s *server) legacyListMailTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.store.ListMailTemplates(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(templates))
	for _, template := range templates {
		var subject any
		var updatedAt any
		if template.Customized {
			subject = template.Subject
			updatedAt = template.UpdatedAt.Unix()
		}
		result = append(result, map[string]any{
			"name": template.Name, "label": template.Label, "customized": template.Customized,
			"subject": subject, "updated_at": updatedAt,
		})
	}
	writeLegacySuccess(w, http.StatusOK, result)
}

func (s *server) legacyGetMailTemplate(w http.ResponseWriter, r *http.Request) {
	template, err := s.store.GetMailTemplate(r.Context(), mailtemplate.Name(r.URL.Query().Get("name")))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, map[string]any{
		"name": template.Name, "label": template.Label,
		"required_vars": template.Required, "optional_vars": template.Optional,
		"customized": template.Customized, "subject": template.Subject, "content": template.Content,
	})
}

func (s *server) legacySaveMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input legacyMailTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetMailTemplate(r.Context(), input.Name)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.UpdateMailTemplate(r.Context(), session.UserID, input.Name, current.Revision, store.SaveMailTemplateInput{Subject: input.Subject, Content: input.Content}, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyResetMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input legacyMailTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.GetMailTemplate(r.Context(), input.Name)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	session, _ := sessionFromContext(r.Context())
	if _, err := s.store.ResetMailTemplate(r.Context(), session.UserID, input.Name, current.Revision, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}

func (s *server) legacyTestMailTemplate(w http.ResponseWriter, r *http.Request) {
	var input legacyMailTemplateRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if input.Email == "" {
		input.Email = session.Email
	}
	if !validMailTemplateRecipient(input.Email) {
		handleStoreError(w, store.ErrInvalidInput)
		return
	}
	if !s.smtpTestRequests.take("legacy-template:"+string(input.Name)+":"+session.Email, s.now()) {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "测试邮件发送过于频繁，请稍后重试", nil)
		return
	}
	if err := s.sendMailTemplateTest(r.Context(), input.Name, input.Email); err != nil {
		s.writeSMTPTestError(w, err)
		return
	}
	writeLegacySuccess(w, http.StatusOK, true)
}
