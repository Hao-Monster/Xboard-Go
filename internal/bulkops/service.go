package bulkops

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	defaultPollInterval = 2 * time.Second
	mailClaimLease      = 30 * time.Second
	csvClaimLease       = 2 * time.Minute
	csvBatchSize        = 500
	maxCSVBytes         = 32 << 20
	csvRetention        = 24 * time.Hour
	mailWorkerCount     = 4
	legacyCSVWait       = 30 * time.Second
)

var templateTokenPattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)(?:\|([^}]*))?\s*\}\}`)

type Options struct {
	Cipher       *appsettings.Cipher
	Sender       mailer.Sender
	ExportRoot   string
	PanelURL     string
	PollInterval time.Duration
	Logger       *slog.Logger
}

type Service struct {
	store       *store.Store
	cipher      *appsettings.Cipher
	sender      mailer.Sender
	exportRoot  string
	panelURL    string
	interval    time.Duration
	logger      *slog.Logger
	cleanupMu   sync.Mutex
	nextCleanup time.Time
}

func New(database *store.Store, options Options) (*Service, error) {
	if database == nil {
		return nil, errors.New("bulk operations store is required")
	}
	root, err := secureExportRoot(options.ExportRoot)
	if err != nil {
		return nil, err
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store: database, cipher: options.Cipher, sender: options.Sender, exportRoot: root,
		panelURL: strings.TrimRight(strings.TrimSpace(options.PanelURL), "/"), interval: interval, logger: logger,
	}, nil
}

func (service *Service) Run(ctx context.Context) {
	if service == nil {
		return
	}
	var workers sync.WaitGroup
	workers.Add(mailWorkerCount)
	for range mailWorkerCount {
		go func() {
			defer workers.Done()
			service.runMailLoop(ctx)
		}()
	}
	service.runCSVLoop(ctx)
	workers.Wait()
}

func (service *Service) runMailLoop(ctx context.Context) {
	ticker := time.NewTicker(service.interval)
	defer ticker.Stop()
	for {
		worked, err := service.RunMailOnce(ctx, time.Now().UTC())
		if err != nil {
			service.logger.Warn("process administrator bulk mail", "error", err)
		}
		if worked && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (service *Service) runCSVLoop(ctx context.Context) {
	ticker := time.NewTicker(service.interval)
	defer ticker.Stop()
	for {
		worked, err := service.runCSVOnce(ctx, time.Now().UTC())
		if err != nil {
			service.logger.Warn("process administrator CSV export", "error", err)
		}
		if worked && ctx.Err() == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (service *Service) RunOnce(ctx context.Context, now time.Time) (bool, error) {
	worked, err := service.RunMailOnce(ctx, now)
	if worked || err != nil {
		return worked, err
	}
	return service.runCSVOnce(ctx, now)
}

func (service *Service) runCSVOnce(ctx context.Context, now time.Time) (bool, error) {
	claimToken := uuid.NewString()
	job, claimed, err := service.store.ClaimAdminUserBulkCSV(ctx, "", claimToken, now, csvClaimLease)
	if err != nil {
		return false, err
	}
	if !claimed {
		return false, service.cleanupIfDue(ctx, now)
	}
	_, err = service.processClaimedCSVWithToken(ctx, job, claimToken, now)
	return true, err
}

func (service *Service) cleanupIfDue(ctx context.Context, now time.Time) error {
	service.cleanupMu.Lock()
	defer service.cleanupMu.Unlock()
	if now.Before(service.nextCleanup) {
		return nil
	}
	service.nextCleanup = now.Add(time.Hour)
	_, err := service.CleanupExpired(ctx, now, 100)
	return err
}

func (service *Service) CleanupExpired(ctx context.Context, now time.Time, limit int) (int, error) {
	outputs, err := service.store.ListExpiredAdminUserBulkOutputs(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(service.exportRoot)
	if err != nil {
		return 0, fmt.Errorf("open administrator export root: %w", err)
	}
	defer root.Close()
	cleaned := 0
	for _, output := range outputs {
		path, err := service.resolveExportPath(output.RelativePath)
		if err != nil {
			return cleaned, err
		}
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return cleaned, fmt.Errorf("remove expired administrator CSV: %w", err)
		}
		cleared, err := service.store.ClearExpiredAdminUserBulkOutput(ctx, output.JobID, output.RelativePath, now)
		if err != nil {
			return cleaned, err
		}
		if cleared {
			cleaned++
		}
	}
	return cleaned, nil
}

func (service *Service) RunMailOnce(ctx context.Context, now time.Time) (bool, error) {
	if service == nil || service.store == nil {
		return false, errors.New("bulk operations service is unavailable")
	}
	claimToken := uuid.NewString()
	claimed, ok, err := service.store.ClaimAdminUserBulkMail(ctx, claimToken, now, mailClaimLease)
	if err != nil || !ok {
		return false, err
	}
	if service.sender == nil {
		err := errors.New("bulk mail sender is unavailable")
		return true, service.failMail(ctx, claimed, claimToken, err, now)
	}
	configuration := mailer.SMTPConfig{
		Host: claimed.SMTPHost, Port: claimed.SMTPPort, Username: claimed.SMTPUsername,
		Encryption: claimed.SMTPEncryption, FromAddress: claimed.SMTPFromAddress,
	}
	if len(claimed.SMTPPasswordCipher) > 0 {
		if service.cipher == nil {
			err := errors.New("settings encryption key is unavailable")
			return true, service.failMail(ctx, claimed, claimToken, err, now)
		}
		plaintext, decryptErr := service.cipher.Decrypt(claimed.SMTPPasswordCipher)
		if decryptErr != nil {
			return true, service.failMail(ctx, claimed, claimToken, errors.New("decrypt SMTP credential"), now)
		}
		configuration.Password = string(plaintext)
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	variables := mailVariables(claimed)
	message := mailer.Message{
		To: claimed.Email, Subject: RenderTemplate(claimed.Subject, variables), Text: RenderTemplate(claimed.Content, variables),
	}
	if !utf8.ValidString(message.Subject) || strings.TrimSpace(message.Subject) == "" || len(message.Subject) > 998 ||
		!utf8.ValidString(message.Text) || len(message.Text) > 128<<10 {
		configuration.Password = ""
		return true, service.failMail(ctx, claimed, claimToken, errors.New("rendered bulk mail exceeds transport limits"), now)
	}
	if err := service.sender.Send(ctx, configuration, message); err != nil {
		configuration.Password = ""
		service.logger.Warn("administrator bulk SMTP delivery failed",
			"job_id", claimed.JobID,
			"sequence", claimed.Sequence,
			"attempt", claimed.Attempt,
			"error_type", fmt.Sprintf("%T", err),
		)
		return true, service.failMail(ctx, claimed, claimToken, errors.New("SMTP delivery failed"), now)
	}
	configuration.Password = ""
	if err := service.store.CompleteAdminUserBulkMail(ctx, claimed.JobID, claimed.Sequence, claimToken, now); err != nil {
		return true, fmt.Errorf("complete administrator bulk mail: %w", err)
	}
	return true, nil
}

func (service *Service) failMail(ctx context.Context, claimed store.AdminUserBulkMail, claimToken string, deliveryError error, now time.Time) error {
	retryAt := now
	switch claimed.Attempt {
	case 1:
		retryAt = now.Add(10 * time.Second)
	case 2:
		retryAt = now.Add(30 * time.Second)
	}
	if err := service.store.FailAdminUserBulkMail(ctx, claimed.JobID, claimed.Sequence, claimToken, deliveryError.Error(), retryAt, now); err != nil {
		return fmt.Errorf("record administrator bulk mail failure after %v: %w", deliveryError, err)
	}
	return fmt.Errorf("administrator bulk mail attempt %d failed: %w", claimed.Attempt, deliveryError)
}

func mailVariables(claimed store.AdminUserBulkMail) map[string]string {
	expiredAt := ""
	if claimed.ExpiredAt != nil {
		expiredAt = claimed.ExpiredAt.UTC().Format("2006-01-02 15:04:05")
	}
	return map[string]string{
		"app.name":             claimed.AppName,
		"app.url":              claimed.AppURL,
		"now":                  claimed.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		"user.id":              strconv.FormatInt(claimed.UserID, 10),
		"user.email":           claimed.Email,
		"user.uuid":            claimed.UUID,
		"user.plan_name":       claimed.PlanName,
		"user.expired_at":      expiredAt,
		"user.transfer_enable": strconv.FormatInt(claimed.TransferEnable, 10),
		"user.transfer_used":   strconv.FormatInt(claimed.TransferUsed, 10),
		"user.transfer_left":   strconv.FormatInt(claimed.TransferEnable-claimed.TransferUsed, 10),
	}
}

func RenderTemplate(input string, variables map[string]string) string {
	return templateTokenPattern.ReplaceAllStringFunc(input, func(token string) string {
		parts := templateTokenPattern.FindStringSubmatch(token)
		if len(parts) != 3 {
			return token
		}
		if value, exists := variables[parts[1]]; exists && value != "" {
			return value
		}
		if strings.Contains(token, "|") {
			return strings.TrimSpace(parts[2])
		}
		return token
	})
}

func (service *Service) ProcessCSVJob(ctx context.Context, jobID string, now time.Time) (store.AdminUserBulkJob, error) {
	if service == nil || service.store == nil {
		return store.AdminUserBulkJob{}, errors.New("bulk operations service is unavailable")
	}
	claimToken := uuid.NewString()
	job, claimed, err := service.store.ClaimAdminUserBulkCSV(ctx, jobID, claimToken, now, csvClaimLease)
	if err != nil {
		return store.AdminUserBulkJob{}, err
	}
	if !claimed {
		return service.waitForCSVJob(ctx, jobID)
	}
	return service.processClaimedCSVWithToken(ctx, job, claimToken, now)
}

func (service *Service) waitForCSVJob(ctx context.Context, jobID string) (store.AdminUserBulkJob, error) {
	deadline := time.NewTimer(legacyCSVWait)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := service.store.GetAdminUserBulkJob(ctx, jobID)
		if err != nil {
			return store.AdminUserBulkJob{}, err
		}
		switch job.Status {
		case store.AdminUserBulkStatusSucceeded:
			return job, nil
		case store.AdminUserBulkStatusQueued, store.AdminUserBulkStatusRunning:
		case store.AdminUserBulkStatusCancelling, store.AdminUserBulkStatusCancelled, store.AdminUserBulkStatusFailed:
			return job, store.ErrConflict
		default:
			return job, fmt.Errorf("unknown administrator CSV job status %q", job.Status)
		}
		select {
		case <-ctx.Done():
			return store.AdminUserBulkJob{}, ctx.Err()
		case <-deadline.C:
			return job, store.ErrConflict
		case <-ticker.C:
		}
	}
}

func (service *Service) processClaimedCSVWithToken(ctx context.Context, job store.AdminUserBulkJob, claimToken string, now time.Time) (store.AdminUserBulkJob, error) {
	startedClock := time.Now()
	root, err := os.OpenRoot(service.exportRoot)
	if err != nil {
		_ = service.store.FailAdminUserBulkCSV(ctx, job.ID, claimToken, "open protected export directory", now)
		return store.AdminUserBulkJob{}, fmt.Errorf("open administrator export root: %w", err)
	}
	defer root.Close()
	const directory = "admin-user-csv"
	if err := root.MkdirAll(directory, 0o700); err != nil {
		_ = service.store.FailAdminUserBulkCSV(ctx, job.ID, claimToken, "create protected export directory", now)
		return store.AdminUserBulkJob{}, fmt.Errorf("create administrator CSV directory: %w", err)
	}
	if err := root.Chmod(directory, 0o700); err != nil {
		_ = service.store.FailAdminUserBulkCSV(ctx, job.ID, claimToken, "protect export directory", now)
		return store.AdminUserBulkJob{}, fmt.Errorf("protect administrator CSV directory: %w", err)
	}
	temporaryRelative := filepath.Join(directory, "."+job.ID+"-"+uuid.NewString()+".tmp")
	temporary, err := root.OpenFile(temporaryRelative, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = service.store.FailAdminUserBulkCSV(ctx, job.ID, claimToken, "create export file", now)
		return store.AdminUserBulkJob{}, fmt.Errorf("create administrator CSV file: %w", err)
	}
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = root.Remove(temporaryRelative)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "protect export file", err, now)
	}
	hash := sha256.New()
	limited := &boundedWriter{writer: io.MultiWriter(temporary, hash), maximum: maxCSVBytes}
	if _, err := limited.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "write export BOM", err, now)
	}
	writer := csv.NewWriter(limited)
	writer.UseCRLF = true
	if err := writer.Write([]string{"邮箱", "余额", "推广佣金", "总流量", "剩余流量", "套餐到期时间", "订阅计划", "订阅地址"}); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "write export header", err, now)
	}
	after := int64(0)
	for {
		targets, err := service.store.ListAdminUserBulkTargets(ctx, job.ID, after, csvBatchSize)
		if err != nil {
			return service.failCSVFile(ctx, job, claimToken, temporary, "read export targets", err, now)
		}
		if len(targets) == 0 {
			break
		}
		for _, target := range targets {
			if err := writer.Write(service.csvRow(job, target)); err != nil {
				return service.failCSVFile(ctx, job, claimToken, temporary, "write export row", err, now)
			}
			after = target.Sequence
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return service.failCSVFile(ctx, job, claimToken, temporary, "flush export rows", err, now)
		}
		active, err := service.store.RefreshAdminUserBulkCSVClaim(ctx, job.ID, claimToken, now.Add(time.Since(startedClock)))
		if err != nil {
			return service.failCSVFile(ctx, job, claimToken, temporary, "refresh export lease", err, now)
		}
		if !active {
			return store.AdminUserBulkJob{}, store.ErrConflict
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "finish export rows", err, now)
	}
	if err := temporary.Sync(); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "sync export file", err, now)
	}
	if err := temporary.Close(); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "close export file", err, now)
	}
	filename := "users_" + now.UTC().Format("2006-01-02_150405") + ".csv"
	relativePath := filepath.ToSlash(filepath.Join("admin-user-csv", job.ID+".csv"))
	finalRelative := filepath.FromSlash(relativePath)
	if err := root.Rename(temporaryRelative, finalRelative); err != nil {
		return service.failCSVFile(ctx, job, claimToken, temporary, "publish export file", err, now)
	}
	keep = true
	digest := hex.EncodeToString(hash.Sum(nil))
	if err := service.store.CompleteAdminUserBulkCSV(ctx, job.ID, claimToken, filename, relativePath, limited.written, digest, now.Add(csvRetention), now); err != nil {
		_ = root.Remove(finalRelative)
		keep = false
		return store.AdminUserBulkJob{}, fmt.Errorf("complete administrator CSV job: %w", err)
	}
	return service.store.GetAdminUserBulkJob(ctx, job.ID)
}

func (service *Service) failCSVFile(ctx context.Context, job store.AdminUserBulkJob, claimToken string, file *os.File, publicReason string, cause error, now time.Time) (store.AdminUserBulkJob, error) {
	_ = file.Close()
	if err := service.store.FailAdminUserBulkCSV(ctx, job.ID, claimToken, publicReason, now); err != nil && !errors.Is(err, store.ErrConflict) {
		return store.AdminUserBulkJob{}, fmt.Errorf("record administrator CSV failure after %v: %w", cause, err)
	}
	return store.AdminUserBulkJob{}, fmt.Errorf("%s: %w", publicReason, cause)
}

func (service *Service) csvRow(job store.AdminUserBulkJob, target store.AdminUserBulkTarget) []string {
	expiry := "长期有效"
	if target.ExpiredAt != nil {
		expiry = target.ExpiredAt.UTC().Format("2006-01-02 15:04:05")
	}
	planName := target.PlanName
	if planName == "" {
		planName = "无订阅"
	}
	baseURL := strings.TrimRight(job.AppURL, "/")
	if baseURL == "" {
		baseURL = service.panelURL
	}
	subscriptionURL := baseURL + "/api/v1/client/subscribe?token=" + url.QueryEscape(target.SubscriptionToken)
	return []string{
		safeSpreadsheetCell(target.Email), money(target.Balance), money(target.CommissionBalance),
		trafficConvert(target.TransferEnable), trafficConvert(target.TransferEnable - target.TransferUsed),
		expiry, safeSpreadsheetCell(planName), safeSpreadsheetCell(subscriptionURL),
	}
}

func (service *Service) OpenCSV(ctx context.Context, jobID string, now time.Time) (*os.File, store.AdminUserBulkJob, error) {
	job, err := service.store.GetAdminUserBulkJob(ctx, jobID)
	if err != nil {
		return nil, store.AdminUserBulkJob{}, err
	}
	if job.Kind != store.AdminUserBulkKindCSV || job.Status != store.AdminUserBulkStatusSucceeded {
		return nil, job, store.ErrConflict
	}
	if job.OutputExpiresAt == nil || !job.OutputExpiresAt.After(now) {
		return nil, job, store.ErrAdminUserBulkExpired
	}
	if job.OutputRelativePath == "" {
		return nil, job, store.ErrAdminUserBulkExpired
	}
	path, err := service.resolveExportPath(job.OutputRelativePath)
	if err != nil {
		return nil, job, err
	}
	root, err := os.OpenRoot(service.exportRoot)
	if err != nil {
		return nil, job, fmt.Errorf("open administrator export root: %w", err)
	}
	file, err := root.Open(path)
	closeErr := root.Close()
	if errors.Is(err, os.ErrNotExist) {
		return nil, job, store.ErrAdminUserBulkExpired
	}
	if err != nil {
		return nil, job, fmt.Errorf("open administrator CSV: %w", err)
	}
	if closeErr != nil {
		_ = file.Close()
		return nil, job, fmt.Errorf("close administrator export root: %w", closeErr)
	}
	return file, job, nil
}

func (service *Service) resolveExportPath(relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, `\`) || strings.Contains(relative, "..") {
		return "", errors.New("invalid administrator export path")
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if !filepath.IsLocal(clean) || filepath.ToSlash(clean) != relative {
		return "", errors.New("administrator export path escapes root")
	}
	return clean, nil
}

func secureExportRoot(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("bulk operations export root is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve administrator export root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	volumeRoot := filepath.Clean(filepath.VolumeName(absolute) + string(filepath.Separator))
	if absolute == volumeRoot {
		return "", errors.New("administrator export root must not be a filesystem root")
	}
	if info, statErr := os.Lstat(absolute); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("administrator export root must not be a symbolic link")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect administrator export root: %w", statErr)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create administrator export root: %w", err)
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil || filepath.Clean(real) != absolute {
		return "", errors.New("administrator export root must not resolve through symbolic links")
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return "", fmt.Errorf("protect administrator export root: %w", err)
	}
	probe, err := os.CreateTemp(absolute, ".xboard-export-probe-*")
	if err != nil {
		return "", fmt.Errorf("verify administrator export root is writable: %w", err)
	}
	probeName := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probeName)
		return "", fmt.Errorf("verify administrator export root is writable: %w", closeErr)
	}
	if err := os.Remove(probeName); err != nil {
		return "", fmt.Errorf("remove administrator export root probe: %w", err)
	}
	return absolute, nil
}

type boundedWriter struct {
	writer  io.Writer
	maximum int64
	written int64
}

func (writer *boundedWriter) Write(payload []byte) (int, error) {
	if writer.written+int64(len(payload)) > writer.maximum {
		return 0, errors.New("administrator CSV exceeds 32 MiB limit")
	}
	written, err := writer.writer.Write(payload)
	writer.written += int64(written)
	return written, err
}

func safeSpreadsheetCell(value string) string {
	first := strings.TrimLeft(value, " ")
	if first != "" && strings.ContainsRune("=+-@\t\r\n", rune(first[0])) {
		return "'" + value
	}
	return value
}

func money(cents int64) string {
	if cents < 0 {
		cents = 0
	}
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}

func trafficConvert(bytes int64) string {
	if bytes < 0 {
		return "0"
	}
	const (
		kib = int64(1 << 10)
		mib = int64(1 << 20)
		gib = int64(1 << 30)
	)
	value := float64(bytes)
	unit := " B"
	if bytes > gib {
		value /= float64(gib)
		unit = " GB"
	} else if bytes > mib {
		value /= float64(mib)
		unit = " MB"
	} else if bytes > kib {
		value /= float64(kib)
		unit = " KB"
	}
	value = math.Round(value*100) / 100
	return strconv.FormatFloat(value, 'f', -1, 64) + unit
}
