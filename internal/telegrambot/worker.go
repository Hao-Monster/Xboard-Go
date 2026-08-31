package telegrambot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	telegramClaimLease       = 30 * time.Second
	maxTelegramDeliveryBatch = 100
	defaultWorkerPollPeriod  = 5 * time.Second
)

type MessageSender interface {
	SendMessage(context.Context, []byte, int64, string) error
}

type Worker struct {
	store    *store.Store
	cipher   *appsettings.Cipher
	sender   MessageSender
	interval time.Duration
	logger   *slog.Logger
}

func NewWorker(database *store.Store, cipherBox *appsettings.Cipher, sender MessageSender, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = defaultWorkerPollPeriod
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
	for index := 0; index < maxTelegramDeliveryBatch; index++ {
		worked, err := worker.RunOnce(ctx, now)
		if err != nil {
			worker.logger.Warn("deliver queued Telegram message", "error", err)
		}
		if !worked || ctx.Err() != nil {
			return
		}
	}
	worker.logger.Warn("Telegram message batch limit reached", "limit", maxTelegramDeliveryBatch)
}

func (worker *Worker) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	if worker == nil || worker.store == nil || worker.sender == nil {
		return false, errors.New("Telegram worker is not configured")
	}
	claimToken := uuid.NewString()
	job, claimed, err := worker.store.ClaimTelegramMessage(ctx, claimToken, now, telegramClaimLease)
	if err != nil || !claimed {
		return false, err
	}
	if worker.cipher == nil {
		return true, worker.recordFailure(ctx, job, claimToken, now)
	}
	botToken, err := worker.cipher.DecryptFor(appsettings.TelegramBotTokenPurpose, job.BotTokenCipher)
	if err != nil || !ValidBotToken(botToken) {
		zeroSecret(botToken)
		return true, worker.recordFailure(ctx, job, claimToken, now)
	}
	err = worker.sender.SendMessage(ctx, botToken, job.ChatID, job.Text)
	zeroSecret(botToken)
	if err != nil {
		return true, worker.recordFailure(ctx, job, claimToken, now)
	}
	if err := worker.store.CompleteTelegramMessage(ctx, job.ID, claimToken, now); err != nil {
		return true, fmt.Errorf("complete Telegram delivery: %w", err)
	}
	return true, nil
}

func (worker *Worker) recordFailure(ctx context.Context, job store.TelegramDeliveryJob, claimToken string, now time.Time) error {
	if err := worker.store.FailTelegramMessage(ctx, job.ID, claimToken, "Telegram delivery failed", now.Add(telegramRetryDelay(job.Attempt)), now); err != nil {
		return fmt.Errorf("record Telegram delivery failure: %w", err)
	}
	return fmt.Errorf("Telegram delivery attempt %d failed", job.Attempt)
}

func telegramRetryDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 10 * time.Second
	case 2:
		return 30 * time.Second
	default:
		return 0
	}
}

func zeroSecret(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
