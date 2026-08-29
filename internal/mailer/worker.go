package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Hao-Monster/Xboard-Go/internal/mailtemplate"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	mailClaimLease    = 30 * time.Second
	maxMailBatch      = 100
	defaultPollPeriod = 5 * time.Second
)

type Worker struct {
	store                 *store.Store
	cipher                *appsettings.Cipher
	resetProtector        *security.PasswordResetProtector
	registrationProtector *security.RegistrationEmailProtector
	loginLinkProtector    *security.LoginLinkProtector
	sender                Sender
	interval              time.Duration
	logger                *slog.Logger
	tracker               *operations.Tracker
}

func NewWorker(database *store.Store, cipherBox *appsettings.Cipher, resetProtector *security.PasswordResetProtector, registrationProtector *security.RegistrationEmailProtector, loginLinkProtector *security.LoginLinkProtector, sender Sender, interval time.Duration, logger *slog.Logger, trackers ...*operations.Tracker) *Worker {
	if interval <= 0 {
		interval = defaultPollPeriod
	}
	if logger == nil {
		logger = slog.Default()
	}
	var tracker *operations.Tracker
	if len(trackers) > 0 {
		tracker = trackers[0]
	}
	return &Worker{
		store: database, cipher: cipherBox, resetProtector: resetProtector,
		registrationProtector: registrationProtector, loginLinkProtector: loginLinkProtector,
		sender: sender, interval: interval, logger: logger, tracker: tracker,
	}
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.store == nil || worker.sender == nil {
		return
	}
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		worker.runBatch(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (worker *Worker) runBatch(ctx context.Context, now time.Time) {
	worker.markHeartbeat()
	defer worker.markHeartbeat()
	for index := 0; index < maxMailBatch; index++ {
		worked, err := worker.RunOnce(ctx, now)
		worker.markHeartbeat()
		if err != nil {
			worker.logger.Warn("deliver queued email", "error", err)
		}
		if !worked || ctx.Err() != nil {
			return
		}
	}
	worker.logger.Warn("email batch limit reached", "limit", maxMailBatch)
}

func (worker *Worker) markHeartbeat() {
	if worker.tracker != nil {
		worker.tracker.MarkMailRun(time.Now())
	}
}

func (worker *Worker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if worker == nil || worker.store == nil || worker.sender == nil {
		return false, errors.New("email worker is not configured")
	}
	claimToken := uuid.NewString()
	resetJob, resetClaimed, err := worker.store.ClaimPasswordResetMail(ctx, claimToken, now, mailClaimLease)
	if err != nil {
		return false, err
	}
	if resetClaimed {
		return true, worker.deliverPasswordReset(ctx, resetJob, claimToken, now)
	}
	registrationJob, registrationClaimed, err := worker.store.ClaimRegistrationEmailVerificationMail(ctx, claimToken, now, mailClaimLease)
	if err != nil {
		return false, err
	}
	if registrationClaimed {
		return true, worker.deliverRegistrationEmailVerification(ctx, registrationJob, claimToken, now)
	}
	loginLinkJob, loginLinkClaimed, err := worker.store.ClaimLoginLinkMail(ctx, claimToken, now, mailClaimLease)
	if err != nil {
		return false, err
	}
	if loginLinkClaimed {
		return true, worker.deliverLoginLink(ctx, loginLinkJob, claimToken, now)
	}
	job, claimed, err := worker.store.ClaimTicketMail(ctx, claimToken, now, mailClaimLease)
	if err != nil {
		return false, err
	}
	if claimed {
		return true, worker.deliverTicket(ctx, job, claimToken, now)
	}
	reminderJob, reminderClaimed, err := worker.store.ClaimSubscriptionReminder(ctx, claimToken, now, mailClaimLease)
	if err != nil || !reminderClaimed {
		return false, err
	}
	return true, worker.deliverSubscriptionReminder(ctx, reminderJob, claimToken, now)
}

func (worker *Worker) deliverTicket(ctx context.Context, job store.TicketMailJob, claimToken string, now time.Time) error {
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return worker.recordFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return worker.recordFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	message, err := worker.renderMailTemplate(ctx, mailtemplate.Notify, job.Recipient, job.AppName, job.AppURL, map[string]string{
		"content": fmt.Sprintf("主题：%s\r\n回复内容：%s", job.Subject, job.Message),
	})
	if err != nil {
		configuration.Password = ""
		return worker.recordFailure(ctx, job, claimToken, now, errors.New("render mail template"))
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return worker.recordFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompleteTicketMail(ctx, job.ID, claimToken, now); err != nil {
		return fmt.Errorf("complete ticket email job: %w", err)
	}
	return nil
}

func (worker *Worker) deliverSubscriptionReminder(ctx context.Context, job store.SubscriptionReminderJob, claimToken string, now time.Time) error {
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return worker.recordSubscriptionReminderFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return worker.recordSubscriptionReminderFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	var templateName mailtemplate.Name
	switch job.Kind {
	case store.SubscriptionReminderExpire:
		templateName = mailtemplate.RemindExpire
	case store.SubscriptionReminderTraffic:
		templateName = mailtemplate.RemindTraffic
	default:
		configuration.Password = ""
		return worker.recordSubscriptionReminderFailure(ctx, job, claimToken, now, errors.New("unsupported subscription reminder kind"))
	}
	message, err := worker.renderMailTemplate(ctx, templateName, job.Recipient, job.AppName, job.AppURL, nil)
	if err != nil {
		configuration.Password = ""
		return worker.recordSubscriptionReminderFailure(ctx, job, claimToken, now, errors.New("render mail template"))
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return worker.recordSubscriptionReminderFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompleteSubscriptionReminder(ctx, job.ID, claimToken, now); err != nil {
		return fmt.Errorf("complete subscription reminder email job: %w", err)
	}
	return nil
}

func (worker *Worker) deliverLoginLink(ctx context.Context, job store.LoginLinkMailJob, claimToken string, now time.Time) error {
	if worker.loginLinkProtector == nil {
		return worker.recordLoginLinkFailure(ctx, job, claimToken, now, errors.New("login link encryption key is unavailable"))
	}
	token, err := worker.loginLinkProtector.DecryptToken(job.UserID, job.TokenCipher)
	if err != nil {
		return worker.recordLoginLinkFailure(ctx, job, claimToken, now, errors.New("decrypt login link token"))
	}
	defer func() {
		for index := range token {
			token[index] = 0
		}
	}()
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return worker.recordLoginLinkFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return worker.recordLoginLinkFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	loginURL := strings.TrimRight(strings.TrimSpace(job.AppURL), "/") + "/#/login?verify=" +
		url.QueryEscape(string(token)) + "&redirect=" + url.QueryEscape(job.Redirect)
	message, err := worker.renderMailTemplate(ctx, mailtemplate.MailLogin, job.Recipient, job.AppName, job.AppURL, map[string]string{
		"link": loginURL,
	})
	if err != nil {
		configuration.Password = ""
		return worker.recordLoginLinkFailure(ctx, job, claimToken, now, errors.New("render mail template"))
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return worker.recordLoginLinkFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompleteLoginLinkMail(ctx, job.ID, claimToken, now); err != nil {
		return fmt.Errorf("complete login link email job: %w", err)
	}
	return nil
}

func (worker *Worker) deliverRegistrationEmailVerification(ctx context.Context, job store.RegistrationEmailVerificationMailJob, claimToken string, now time.Time) error {
	if worker.registrationProtector == nil {
		return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, errors.New("registration email encryption key is unavailable"))
	}
	code, err := worker.registrationProtector.DecryptCode(job.Recipient, job.CodeCipher)
	if err != nil {
		return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, errors.New("decrypt registration email code"))
	}
	defer func() {
		for index := range code {
			code[index] = 0
		}
	}()
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	message, err := worker.renderMailTemplate(ctx, mailtemplate.Verify, job.Recipient, job.AppName, job.AppURL, map[string]string{
		"code": string(code),
	})
	if err != nil {
		configuration.Password = ""
		return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, errors.New("render mail template"))
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return worker.recordRegistrationEmailFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompleteRegistrationEmailVerificationMail(ctx, job.ID, claimToken, now); err != nil {
		return fmt.Errorf("complete registration verification email job: %w", err)
	}
	return nil
}

func (worker *Worker) deliverPasswordReset(ctx context.Context, job store.PasswordResetMailJob, claimToken string, now time.Time) error {
	if worker.resetProtector == nil {
		return worker.recordPasswordResetFailure(ctx, job, claimToken, now, errors.New("password reset encryption key is unavailable"))
	}
	code, err := worker.resetProtector.DecryptCode(job.Recipient, job.CodeCipher)
	if err != nil {
		return worker.recordPasswordResetFailure(ctx, job, claimToken, now, errors.New("decrypt password reset code"))
	}
	defer func() {
		for index := range code {
			code[index] = 0
		}
	}()
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return worker.recordPasswordResetFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return worker.recordPasswordResetFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	message, err := worker.renderMailTemplate(ctx, mailtemplate.Verify, job.Recipient, job.AppName, job.AppURL, map[string]string{
		"code": string(code),
	})
	if err != nil {
		configuration.Password = ""
		return worker.recordPasswordResetFailure(ctx, job, claimToken, now, errors.New("render mail template"))
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return worker.recordPasswordResetFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompletePasswordResetMail(ctx, job.ID, claimToken, now); err != nil {
		return fmt.Errorf("complete password reset email job: %w", err)
	}
	return nil
}

func (worker *Worker) renderMailTemplate(ctx context.Context, name mailtemplate.Name, recipient, appName, appURL string, values map[string]string) (Message, error) {
	stored, err := worker.store.GetMailTemplate(ctx, name)
	if err != nil {
		return Message{}, err
	}
	if values == nil {
		values = make(map[string]string, 2)
	}
	values["name"] = strings.TrimSpace(appName)
	values["url"] = strings.TrimRight(strings.TrimSpace(appURL), "/")
	subject := stored.Subject
	if !stored.Customized {
		var ok bool
		subject, ok = mailtemplate.DeliverySubject(name, values["name"])
		if !ok {
			return Message{}, errors.New("unknown default mail delivery subject")
		}
	}
	rendered, err := mailtemplate.Render(mailtemplate.Template{Name: stored.Name, Subject: subject, Content: stored.Content}, values)
	if err != nil {
		return Message{}, err
	}
	return Message{To: recipient, Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML}, nil
}

func (worker *Worker) recordFailure(ctx context.Context, job store.TicketMailJob, claimToken string, now time.Time, deliveryError error) error {
	retryDelay := mailRetryDelay(job.Attempt)
	if err := worker.store.FailTicketMail(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(retryDelay), now); err != nil {
		return fmt.Errorf("record ticket email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("ticket email attempt %d failed: %w", job.Attempt, deliveryError)
}

func (worker *Worker) recordPasswordResetFailure(ctx context.Context, job store.PasswordResetMailJob, claimToken string, now time.Time, deliveryError error) error {
	if err := worker.store.FailPasswordResetMail(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(passwordResetMailRetryDelay(job.Attempt)), now); err != nil {
		return fmt.Errorf("record password reset email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("password reset email attempt %d failed: %w", job.Attempt, deliveryError)
}

func (worker *Worker) recordRegistrationEmailFailure(ctx context.Context, job store.RegistrationEmailVerificationMailJob, claimToken string, now time.Time, deliveryError error) error {
	if err := worker.store.FailRegistrationEmailVerificationMail(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(passwordResetMailRetryDelay(job.Attempt)), now); err != nil {
		return fmt.Errorf("record registration verification email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("registration verification email attempt %d failed: %w", job.Attempt, deliveryError)
}

func (worker *Worker) recordLoginLinkFailure(ctx context.Context, job store.LoginLinkMailJob, claimToken string, now time.Time, deliveryError error) error {
	if err := worker.store.FailLoginLinkMail(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(passwordResetMailRetryDelay(job.Attempt)), now); err != nil {
		return fmt.Errorf("record login link email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("login link email attempt %d failed: %w", job.Attempt, deliveryError)
}

func (worker *Worker) recordSubscriptionReminderFailure(ctx context.Context, job store.SubscriptionReminderJob, claimToken string, now time.Time, deliveryError error) error {
	if err := worker.store.FailSubscriptionReminder(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(mailRetryDelay(job.Attempt)), now); err != nil {
		return fmt.Errorf("record subscription reminder email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("subscription reminder email attempt %d failed: %w", job.Attempt, deliveryError)
}

func passwordResetMailRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 10 * time.Second
	case 2:
		return 30 * time.Second
	default:
		return 0
	}
}

func mailRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	default:
		return 0
	}
}
