package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	mailClaimLease    = 30 * time.Second
	maxMailBatch      = 100
	defaultPollPeriod = 5 * time.Second
)

type Worker struct {
	store    *store.Store
	cipher   *appsettings.Cipher
	sender   Sender
	interval time.Duration
	logger   *slog.Logger
}

func NewWorker(database *store.Store, cipherBox *appsettings.Cipher, sender Sender, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = defaultPollPeriod
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: database, cipher: cipherBox, sender: sender, interval: interval, logger: logger}
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
	for index := 0; index < maxMailBatch; index++ {
		worked, err := worker.RunOnce(ctx, now)
		if err != nil {
			worker.logger.Warn("deliver ticket email", "error", err)
		}
		if !worked || ctx.Err() != nil {
			return
		}
	}
	worker.logger.Warn("ticket email batch limit reached", "limit", maxMailBatch)
}

func (worker *Worker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if worker == nil || worker.store == nil || worker.sender == nil {
		return false, errors.New("ticket email worker is not configured")
	}
	claimToken := uuid.NewString()
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

func (worker *Worker) recordFailure(ctx context.Context, job store.TicketMailJob, claimToken string, now time.Time, deliveryError error) error {
	retryDelay := time.Minute
	if job.Attempt == 2 {
		retryDelay = 5 * time.Minute
	}
	if job.Attempt >= 3 {
		retryDelay = 0
	}
	if err := worker.store.FailTicketMail(ctx, job.ID, claimToken, deliveryError.Error(), now.Add(retryDelay), now); err != nil {
		return fmt.Errorf("record ticket email failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("ticket email attempt %d failed: %w", job.Attempt, deliveryError)
}
