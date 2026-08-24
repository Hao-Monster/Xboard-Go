package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

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
	sender                Sender
	interval              time.Duration
	logger                *slog.Logger
	tracker               *operations.Tracker
}

func NewWorker(database *store.Store, cipherBox *appsettings.Cipher, resetProtector *security.PasswordResetProtector, registrationProtector *security.RegistrationEmailProtector, sender Sender, interval time.Duration, logger *slog.Logger, trackers ...*operations.Tracker) *Worker {
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
		registrationProtector: registrationProtector, sender: sender, interval: interval, logger: logger, tracker: tracker,
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
	job, claimed, err := worker.store.ClaimTicketMail(ctx, claimToken, now, mailClaimLease)
	if err != nil || !claimed {
		return false, err
	}
	configuration := SMTPConfig{
		Host: job.SMTPHost, Port: job.SMTPPort, Username: job.SMTPUsername,
		Encryption: job.SMTPEncryption, FromAddress: job.SMTPFromAddress,
	}
	if len(job.SMTPPasswordCipher) > 0 {
		if worker.cipher == nil {
			return true, worker.recordFailure(ctx, job, claimToken, now, errors.New("settings encryption key is unavailable"))
		}
		plaintext, decryptErr := worker.cipher.Decrypt(job.SMTPPasswordCipher)
		if decryptErr != nil {
			return true, worker.recordFailure(ctx, job, claimToken, now, errors.New("decrypt SMTP credential"))
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	message := Message{
		To:      job.Recipient,
		Subject: fmt.Sprintf("您在%s的工单得到了回复", strings.TrimSpace(job.AppName)),
		Text:    fmt.Sprintf("主题：%s\r\n回复内容：%s", job.Subject, job.Message),
	}
	if strings.TrimSpace(job.AppURL) != "" {
		message.Text += "\r\n查看工单：" + strings.TrimRight(strings.TrimSpace(job.AppURL), "/")
	}
	if err := worker.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		return true, worker.recordFailure(ctx, job, claimToken, now, err)
	}
	configuration.Password = ""
	if err := worker.store.CompleteTicketMail(ctx, job.ID, claimToken, now); err != nil {
		return true, fmt.Errorf("complete ticket email job: %w", err)
	}
	return true, nil
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
	message := Message{
		To: job.Recipient, Subject: fmt.Sprintf("%s邮箱验证码", strings.TrimSpace(job.AppName)),
		Text: fmt.Sprintf("您的邮箱验证码是：%s\r\n验证码 5 分钟内有效，请勿泄露给他人。", string(code)),
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
	message := Message{
		To: job.Recipient, Subject: fmt.Sprintf("%s邮箱验证码", strings.TrimSpace(job.AppName)),
		Text: fmt.Sprintf("您的邮箱验证码是：%s\r\n验证码 5 分钟内有效，请勿泄露给他人。", string(code)),
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
