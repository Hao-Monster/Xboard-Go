package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Hao-Monster/Xboard-Go/internal/attachments"
	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/bulkops"
	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	"github.com/Hao-Monster/Xboard-Go/internal/config"
	"github.com/Hao-Monster/Xboard-Go/internal/httpapi"
	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/maintenance"
	"github.com/Hao-Monster/Xboard-Go/internal/nodecoord"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
	"github.com/Hao-Monster/Xboard-Go/internal/payment"
	"github.com/Hao-Monster/Xboard-Go/internal/scheduler"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/webui"
)

var buildRevision = "local"

func main() {
	commandContext, stopCommand := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	handled, commandErr := runCommand(commandContext, os.Args[1:], os.Stdout, os.Stderr, time.Now)
	stopCommand()
	if handled {
		if commandErr != nil {
			fmt.Fprintln(os.Stderr, commandErr)
			os.Exit(1)
		}
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	settings, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	if err := prepareSQLiteDirectory(settings.DatabaseDSN); err != nil {
		logger.Error("prepare database directory", "error", err)
		os.Exit(1)
	}

	database, err := store.OpenSQLite(settings.DatabaseDSN)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	if err := secureSQLiteFiles(settings.DatabaseDSN); err != nil {
		logger.Error("secure database files", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var nodeCoordinator nodecoord.Coordinator
	if settings.NodeCoordinationMode == "redis" {
		redisCoordinator, err := nodecoord.NewRedis(ctx, nodecoord.Options{
			URL: settings.RedisURL, Prefix: settings.RedisKeyPrefix, Revision: buildRevision, Logger: logger,
		})
		if err != nil {
			logger.Error("initialize node coordination", "error", err)
			os.Exit(1)
		}
		nodeCoordinator = redisCoordinator
		defer func() {
			if err := redisCoordinator.Close(); err != nil {
				logger.Warn("close node coordination", "error", err)
			}
		}()
	}
	if err := database.Migrate(ctx); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}
	settingsCipher, err := initializeSettingsCipher(ctx, database, settings.SettingsEncryptionKey)
	if err != nil {
		logger.Error("initialize settings encryption", "error", err)
		os.Exit(1)
	}
	var passwordResetProtector *security.PasswordResetProtector
	var registrationEmailProtector *security.RegistrationEmailProtector
	if len(settings.SettingsEncryptionKey) == 32 {
		passwordResetProtector, err = security.NewPasswordResetProtector(settings.SettingsEncryptionKey)
		if err != nil {
			logger.Error("initialize password reset encryption", "error", err)
			os.Exit(1)
		}
		registrationEmailProtector, err = security.NewRegistrationEmailProtector(settings.SettingsEncryptionKey)
		if err != nil {
			logger.Error("initialize registration email encryption", "error", err)
			os.Exit(1)
		}
	}
	invitationProtector, err := initializeInvitationProtector(ctx, database, settings.SettingsEncryptionKey)
	if err != nil {
		logger.Error("initialize invitation encryption", "error", err)
		os.Exit(1)
	}
	loginLinkProtector, err := initializeLoginLinkProtector(ctx, database, settings.SettingsEncryptionKey)
	if err != nil {
		logger.Error("initialize login link encryption", "error", err)
		os.Exit(1)
	}
	captchaVerifier, err := captcha.New(captcha.Options{
		RecaptchaEndpoint: settings.CaptchaRecaptchaURL, RecaptchaV3Endpoint: settings.CaptchaRecaptchaV3URL,
		TurnstileEndpoint: settings.CaptchaTurnstileURL,
	})
	if err != nil {
		logger.Error("initialize CAPTCHA verifier", "error", err)
		os.Exit(1)
	}
	var attachmentService *attachments.Service
	if settings.AttachmentRoot != "" {
		if len(settings.SettingsEncryptionKey) != 32 {
			logger.Error("initialize knowledge attachments", "error", "XBOARD_SETTINGS_ENCRYPTION_KEY is required when attachments are enabled")
			os.Exit(1)
		}
		attachmentService, err = attachments.New(database, attachments.Options{
			Root: settings.AttachmentRoot, SigningKey: settings.SettingsEncryptionKey, PanelURL: settings.PanelURL,
			ChunkSize: settings.AttachmentChunkSize, MaxFileSize: settings.AttachmentMaxFileSize,
			TotalQuota: settings.AttachmentTotalQuota, SignedURLTTL: settings.AttachmentSignedURLTTL,
			DraftTTL: settings.AttachmentDraftTTL, TrashRetention: settings.AttachmentTrashTTL,
			MaxPerArticle: settings.AttachmentMaxPerArticle,
		})
		if err != nil {
			logger.Error("initialize knowledge attachments", "error", err)
			os.Exit(1)
		}
	}
	for index := range settings.SettingsEncryptionKey {
		settings.SettingsEncryptionKey[index] = 0
	}

	passwordHasher := security.DefaultPasswordHasher()
	if settings.BootstrapAdminEmail != "" {
		passwordHash, err := passwordHasher.Hash(settings.BootstrapAdminPassword)
		if err != nil {
			logger.Error("hash bootstrap password", "error", err)
			os.Exit(1)
		}
		created, err := database.BootstrapAdmin(ctx, settings.BootstrapAdminEmail, passwordHash, time.Now())
		if err != nil {
			logger.Error("bootstrap administrator", "error", err)
			os.Exit(1)
		}
		if created {
			logger.Info("bootstrap administrator created", "email", settings.BootstrapAdminEmail)
		}
	}

	runtimeTracker := operations.NewTracker(time.Now())
	worker := scheduler.NewWorker(database, settings.SchedulerInterval, logger, runtimeTracker)
	go worker.Run(ctx)
	if attachmentService != nil {
		go runAttachmentCleanup(ctx, attachmentService, logger, time.Hour)
	}
	smtpSender := mailer.NewSMTPSender(10*time.Second, settings.SMTPAllowInsecure)
	mailWorker := mailer.NewWorker(database, settingsCipher, passwordResetProtector, registrationEmailProtector, loginLinkProtector, smtpSender, settings.MailPollInterval, logger, runtimeTracker)
	go mailWorker.Run(ctx)
	bulkService, err := bulkops.New(database, bulkops.Options{
		Cipher: settingsCipher, Sender: smtpSender, ExportRoot: settings.AdminExportRoot,
		PanelURL: settings.PanelURL, PollInterval: settings.BulkPollInterval, Logger: logger,
	})
	if err != nil {
		logger.Error("initialize administrator bulk operations", "error", err)
		os.Exit(1)
	}
	go bulkService.Run(ctx)

	var handler http.Handler = httpapi.New(httpapi.Dependencies{
		Store:                      database,
		PasswordHasher:             passwordHasher,
		PanelURL:                   settings.PanelURL,
		LegacyAdminPath:            settings.LegacyAdminPath,
		NodeRelease:                settings.NodeRelease,
		CookieSecure:               settings.CookieSecure,
		AllowedOrigins:             settings.AllowedOrigins,
		Logger:                     logger,
		Context:                    ctx,
		WebSocketEnabled:           settings.WebSocketEnabled,
		WebSocketURL:               settings.WebSocketURL,
		NodePushInterval:           settings.NodePushInterval,
		NodePullInterval:           settings.NodePullInterval,
		NodeCoordinator:            nodeCoordinator,
		SettingsCipher:             settingsCipher,
		PasswordResetProtector:     passwordResetProtector,
		RegistrationEmailProtector: registrationEmailProtector,
		InvitationProtector:        invitationProtector,
		LoginLinkProtector:         loginLinkProtector,
		SMTPAllowInsecure:          settings.SMTPAllowInsecure,
		RuntimeTracker:             runtimeTracker,
		CaptchaVerifier:            captchaVerifier,
		Attachments:                attachmentService,
		BulkOperations:             bulkService,
	})
	if settings.WebRoot != "" {
		handler, err = webui.New(settings.WebRoot, handler)
		if err != nil {
			logger.Error("load web frontend", "error", err)
			os.Exit(1)
		}
	}
	server := &http.Server{
		Addr:              settings.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("shutdown HTTP server", "error", err)
		}
	}()

	logger.Info("Xboard-Go API listening", "address", settings.Address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve HTTP", "error", err)
		os.Exit(1)
	}
}

func runAttachmentCleanup(ctx context.Context, service *attachments.Service, logger *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			report, err := service.Cleanup(ctx, now.UTC(), 100)
			if err != nil {
				logger.Warn("knowledge attachment cleanup incomplete", "expired_uploads", report.ExpiredUploads,
					"soft_deleted_drafts", report.SoftDeletedDrafts, "purged_attachments", report.PurgedAttachments,
					"orphan_files", report.OrphanFiles, "failures", report.Failures, "error", err)
			}
		}
	}
}

type commandResult struct {
	Status         string          `json:"status"`
	Action         string          `json:"action"`
	Path           string          `json:"path"`
	AttachmentPath string          `json:"attachment_path,omitempty"`
	Manifest       backup.Manifest `json:"manifest"`
}

type maintenanceCommandResult struct {
	Status string                    `json:"status"`
	Action string                    `json:"action"`
	AsOf   time.Time                 `json:"as_of"`
	Limit  int                       `json:"limit"`
	Result maintenance.CleanupResult `json:"result"`
}

type attachmentStatusCommandResult struct {
	Status string                   `json:"status"`
	Action string                   `json:"action"`
	Result attachments.StatusReport `json:"result"`
}

type legacyMigrationSourceResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type legacyMigrationBackupResult struct {
	Path     string          `json:"path"`
	SHA256   string          `json:"sha256"`
	Manifest backup.Manifest `json:"manifest"`
}

type legacyMigrationCommandResult struct {
	Status         string                          `json:"status"`
	Action         string                          `json:"action"`
	Source         legacyMigrationSourceResult     `json:"source"`
	RollbackBackup legacyMigrationBackupResult     `json:"rollback_backup"`
	Result         store.LegacyContentImportReport `json:"result"`
}

type legacyGroupsRoutesMigrationCommandResult struct {
	Status         string                               `json:"status"`
	Action         string                               `json:"action"`
	Source         legacyMigrationSourceResult          `json:"source"`
	RollbackBackup legacyMigrationBackupResult          `json:"rollback_backup"`
	Result         store.LegacyGroupsRoutesImportReport `json:"result"`
}

type legacyKnowledgeMigrationCommandResult struct {
	Status         string                            `json:"status"`
	Action         string                            `json:"action"`
	Source         legacyMigrationSourceResult       `json:"source"`
	RollbackBackup legacyMigrationBackupResult       `json:"rollback_backup"`
	Result         store.LegacyKnowledgeImportReport `json:"result"`
}

type legacyHumanUsersMigrationCommandResult struct {
	Status         string                             `json:"status"`
	Action         string                             `json:"action"`
	Source         legacyMigrationSourceResult        `json:"source"`
	RollbackBackup legacyMigrationBackupResult        `json:"rollback_backup"`
	Result         store.LegacyHumanUsersImportReport `json:"result"`
}

type legacyNodesMigrationCommandResult struct {
	Status         string                        `json:"status"`
	Action         string                        `json:"action"`
	Source         legacyMigrationSourceResult   `json:"source"`
	RollbackBackup legacyMigrationBackupResult   `json:"rollback_backup"`
	Result         store.LegacyNodesImportReport `json:"result"`
}

type legacyPlansMigrationCommandResult struct {
	Status         string                        `json:"status"`
	Action         string                        `json:"action"`
	Source         legacyMigrationSourceResult   `json:"source"`
	RollbackBackup legacyMigrationBackupResult   `json:"rollback_backup"`
	Result         store.LegacyPlansImportReport `json:"result"`
}

type legacyOrdersMigrationCommandResult struct {
	Status         string                         `json:"status"`
	Action         string                         `json:"action"`
	Source         legacyMigrationSourceResult    `json:"source"`
	RollbackBackup legacyMigrationBackupResult    `json:"rollback_backup"`
	Result         store.LegacyOrdersImportReport `json:"result"`
}

type legacyDistributorsMigrationCommandResult struct {
	Status         string                               `json:"status"`
	Action         string                               `json:"action"`
	Source         legacyMigrationSourceResult          `json:"source"`
	RollbackBackup legacyMigrationBackupResult          `json:"rollback_backup"`
	Result         store.LegacyDistributorsImportReport `json:"result"`
}

type legacyCouponsMigrationCommandResult struct {
	Status         string                          `json:"status"`
	Action         string                          `json:"action"`
	Source         legacyMigrationSourceResult     `json:"source"`
	RollbackBackup legacyMigrationBackupResult     `json:"rollback_backup"`
	Result         store.LegacyCouponsImportReport `json:"result"`
}

type legacyGiftCardsMigrationCommandResult struct {
	Status         string                            `json:"status"`
	Action         string                            `json:"action"`
	Source         legacyMigrationSourceResult       `json:"source"`
	RollbackBackup legacyMigrationBackupResult       `json:"rollback_backup"`
	Result         store.LegacyGiftCardsImportReport `json:"result"`
}

type legacyPaymentsMigrationCommandResult struct {
	Status         string                           `json:"status"`
	Action         string                           `json:"action"`
	Source         legacyMigrationSourceResult      `json:"source"`
	RollbackBackup legacyMigrationBackupResult      `json:"rollback_backup"`
	Result         store.LegacyPaymentsImportReport `json:"result"`
}

type legacySubscriptionConfigMigrationCommandResult struct {
	Status         string                                     `json:"status"`
	Action         string                                     `json:"action"`
	Source         legacyMigrationSourceResult                `json:"source"`
	RollbackBackup legacyMigrationBackupResult                `json:"rollback_backup"`
	Result         store.LegacySubscriptionConfigImportReport `json:"result"`
}

func runCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	if len(arguments) == 0 {
		return false, nil
	}
	if len(arguments) == 1 && arguments[0] == "healthcheck" {
		return true, runHealthcheck()
	}
	if arguments[0] == "maintenance" {
		return runMaintenanceCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "knowledge-attachments" {
		return runKnowledgeAttachmentsCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "migration" {
		return runMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] != "backup" {
		return true, fmt.Errorf("unknown command %q", arguments[0])
	}
	if len(arguments) < 2 {
		return true, errors.New("backup subcommand is required: create, verify, or restore")
	}

	switch arguments[1] {
	case "create":
		flags := flag.NewFlagSet("backup create", flag.ContinueOnError)
		flags.SetOutput(stderr)
		defaultDirectory := strings.TrimSpace(os.Getenv("XBOARD_BACKUP_DIRECTORY"))
		if defaultDirectory == "" {
			defaultDirectory = "/var/lib/xboard-backups"
		}
		defaultOutput := filepath.Join(defaultDirectory, "xboard-"+now().UTC().Format("20060102T150405Z")+".xbbackup")
		output := flags.String("output", defaultOutput, "new backup archive path")
		attachmentRoot := flags.String("attachment-root", strings.TrimSpace(os.Getenv("XBOARD_ATTACHMENT_ROOT")), "private attachment root to include")
		if err := flags.Parse(arguments[2:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, errors.New("backup create does not accept positional arguments")
		}
		manifest, err := backup.Create(ctx, config.DatabaseDSN(), *output, buildRevision, now(), *attachmentRoot)
		if err != nil {
			return true, err
		}
		absolute, err := filepath.Abs(*output)
		if err != nil {
			return true, err
		}
		return true, encodeCommandResult(stdout, commandResult{Status: "success", Action: "backup.create", Path: absolute, Manifest: manifest})

	case "verify":
		flags := flag.NewFlagSet("backup verify", flag.ContinueOnError)
		flags.SetOutput(stderr)
		input := flags.String("input", "", "backup archive path")
		if err := flags.Parse(arguments[2:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*input) == "" {
			return true, errors.New("backup verify requires --input and no positional arguments")
		}
		manifest, err := backup.Verify(ctx, *input)
		if err != nil {
			return true, err
		}
		absolute, err := filepath.Abs(*input)
		if err != nil {
			return true, err
		}
		return true, encodeCommandResult(stdout, commandResult{Status: "success", Action: "backup.verify", Path: absolute, Manifest: manifest})

	case "restore":
		flags := flag.NewFlagSet("backup restore", flag.ContinueOnError)
		flags.SetOutput(stderr)
		input := flags.String("input", "", "backup archive path")
		output := flags.String("output", "", "new restored database path")
		attachmentOutput := flags.String("attachment-output", "", "new restored attachment root for format-v2 bundles")
		if err := flags.Parse(arguments[2:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
			return true, errors.New("backup restore requires --input and --output and accepts no positional arguments")
		}
		manifest, err := backup.Restore(ctx, *input, *output, *attachmentOutput)
		if err != nil {
			return true, err
		}
		absolute, err := filepath.Abs(*output)
		if err != nil {
			return true, err
		}
		var absoluteAttachment string
		if strings.TrimSpace(*attachmentOutput) != "" {
			absoluteAttachment, err = filepath.Abs(*attachmentOutput)
			if err != nil {
				return true, err
			}
		}
		return true, encodeCommandResult(stdout, commandResult{Status: "success", Action: "backup.restore", Path: absolute, AttachmentPath: absoluteAttachment, Manifest: manifest})

	default:
		return true, fmt.Errorf("unknown backup subcommand %q", arguments[1])
	}
}

func runKnowledgeAttachmentsCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	if len(arguments) == 0 || arguments[0] != "status" {
		return true, errors.New("knowledge-attachments subcommand is required: status")
	}
	flags := flag.NewFlagSet("knowledge-attachments status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(arguments[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, errors.New("knowledge-attachments status does not accept positional arguments")
	}
	settings, err := config.Load()
	if err != nil {
		return true, err
	}
	defer func() {
		for index := range settings.SettingsEncryptionKey {
			settings.SettingsEncryptionKey[index] = 0
		}
	}()
	if settings.AttachmentRoot == "" || len(settings.SettingsEncryptionKey) != 32 {
		return true, errors.New("knowledge-attachments status requires XBOARD_ATTACHMENT_ROOT and XBOARD_SETTINGS_ENCRYPTION_KEY")
	}
	database, err := store.OpenSQLite(settings.DatabaseDSN)
	if err != nil {
		return true, err
	}
	defer database.Close()
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		return true, err
	}
	service, err := attachments.New(database, attachments.Options{
		Root: settings.AttachmentRoot, SigningKey: settings.SettingsEncryptionKey, PanelURL: settings.PanelURL,
		ChunkSize: settings.AttachmentChunkSize, MaxFileSize: settings.AttachmentMaxFileSize,
		TotalQuota: settings.AttachmentTotalQuota, SignedURLTTL: settings.AttachmentSignedURLTTL,
		DraftTTL: settings.AttachmentDraftTTL, TrashRetention: settings.AttachmentTrashTTL,
		MaxPerArticle: settings.AttachmentMaxPerArticle,
	})
	if err != nil {
		return true, err
	}
	report := service.StatusReport(ctx, now().UTC())
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(attachmentStatusCommandResult{Status: "success", Action: "knowledge-attachments.status", Result: report}); err != nil {
		return true, err
	}
	if !report.Healthy {
		return true, errors.New("knowledge attachment storage health check failed")
	}
	return true, nil
}

func runMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	if len(arguments) == 0 {
		return true, errors.New("migration subcommand is required: import-legacy-content, import-legacy-groups-routes, import-legacy-knowledge, import-legacy-human-users, import-legacy-nodes, import-legacy-node-agent-settings, import-legacy-plans, import-legacy-coupons, import-legacy-gift-cards, import-legacy-payments, import-legacy-orders, import-legacy-tickets, import-legacy-commissions, import-legacy-distributors, or import-legacy-subscription-config")
	}
	if arguments[0] == "import-legacy-node-agent-settings" {
		return runLegacyNodeAgentSettingsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-subscription-config" {
		return runLegacySubscriptionConfigMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-plans" {
		return runLegacyPlansMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-coupons" {
		return runLegacyCouponsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-gift-cards" {
		return runLegacyGiftCardsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-payments" {
		return runLegacyPaymentsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-orders" {
		return runLegacyOrdersMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-tickets" {
		return runLegacyTicketsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-commissions" {
		return runLegacyCommissionsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-distributors" {
		return runLegacyDistributorsMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-nodes" {
		return runLegacyNodesMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-human-users" {
		return runLegacyHumanUsersMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-knowledge" {
		return runLegacyKnowledgeMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] == "import-legacy-groups-routes" {
		return runLegacyGroupsRoutesMigrationCommand(ctx, arguments[1:], stdout, stderr, now)
	}
	if arguments[0] != "import-legacy-content" {
		return true, fmt.Errorf("unknown migration subcommand %q", arguments[0])
	}
	flags := flag.NewFlagSet("migration import-legacy-content", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-content requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-content requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadContentSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy content migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy content migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyContentImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy migration rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy migration rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}
	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy content migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy migration rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyContentImport{
		Slice: store.LegacyContentSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		SiteSettings: snapshot.SiteSettings, Notices: snapshot.Notices,
		ClientCatalogPresent: snapshot.ClientCatalogPresent, ClientCatalogLinks: snapshot.ClientCatalogLinks,
		Checksums: snapshot.Checksums, RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyContent(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func runLegacyKnowledgeMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-knowledge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	sourceAttachmentRoot := flags.String("source-attachment-root", "", "legacy private knowledge attachment root")
	targetAttachmentRoot := flags.String("target-attachment-root", configuredAttachmentRoot(), "new private knowledge attachment root")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-knowledge requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-knowledge requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadKnowledgeSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy knowledge migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy knowledge migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy knowledge migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy knowledge migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy knowledge migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy knowledge migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy knowledge migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyKnowledgeImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy knowledge migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed knowledge migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy knowledge rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy knowledge rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyKnowledgeMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}
	if len(snapshot.Attachments) != 0 || len(snapshot.Uploads) != 0 {
		if strings.TrimSpace(*sourceAttachmentRoot) == "" || strings.TrimSpace(*targetAttachmentRoot) == "" {
			return true, errors.New("non-empty legacy knowledge attachments or uploads require --source-attachment-root and --target-attachment-root")
		}
		if err := attachments.PrepareStorageRoot(*targetAttachmentRoot); err != nil {
			return true, fmt.Errorf("prepare target knowledge attachment root: %w", err)
		}
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy knowledge migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy knowledge rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), *targetAttachmentRoot)
	if err != nil {
		return true, fmt.Errorf("create pre-import knowledge rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import knowledge rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import knowledge rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}
	rollbackFiles := func() {}
	legacyUploads := snapshot.Uploads
	if len(snapshot.Attachments) != 0 || len(snapshot.Uploads) != 0 {
		settings, err := config.Load()
		if err != nil {
			return true, fmt.Errorf("load attachment migration limits: %w", err)
		}
		legacyUploads, rollbackFiles, err = attachments.ImportLegacySnapshotFiles(ctx, *sourceAttachmentRoot, *targetAttachmentRoot,
			snapshot.Attachments, snapshot.Uploads, settings.AttachmentTotalQuota)
		if err != nil {
			return true, fmt.Errorf("copy legacy knowledge attachments: %w", err)
		}
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		rollbackFiles()
		return true, err
	}
	input := store.LegacyKnowledgeImport{
		Slice: store.LegacyKnowledgeSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Articles: snapshot.Articles, Checksum: snapshot.Checksum,
		Attachments: snapshot.Attachments, AttachmentsChecksum: snapshot.AttachmentsChecksum,
		Uploads: legacyUploads, UploadsChecksum: store.LegacyKnowledgeUploadsChecksum(legacyUploads),
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyKnowledge(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		rollbackFiles()
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyKnowledgeMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyKnowledgeMigrationResult(output io.Writer, snapshot legacymigration.KnowledgeSnapshot, rollback legacyMigrationBackupResult, report store.LegacyKnowledgeImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyKnowledgeMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-knowledge",
		Source:         legacyMigrationSourceResult{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256},
		RollbackBackup: rollback, Result: report,
	})
}

func runLegacyHumanUsersMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-human-users", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	replaceBootstrapAdmin := flags.Bool("replace-bootstrap-admin", false, "confirm replacement of the only pristine bootstrap administrator")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-human-users requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-human-users requires --confirm-offline after the target application is stopped")
	}
	if !*replaceBootstrapAdmin {
		return true, errors.New("migration import-legacy-human-users requires --replace-bootstrap-admin")
	}

	snapshot, err := legacymigration.ReadHumanUsersSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy human user migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy human user migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy human user migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy human user migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy human user migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy human user migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy human user migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyHumanUsersImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy human user migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed human user migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy human user rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy human user rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyHumanUsersMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy human user migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy human user rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import human user rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import human user rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import human user rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyHumanUsersImport{
		Slice: store.LegacyHumanUsersSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Users: snapshot.Users, Checksum: snapshot.Checksum, ReplaceBootstrapAdmin: true,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyHumanUsers(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyHumanUsersMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyHumanUsersMigrationResult(output io.Writer, snapshot legacymigration.HumanUsersSnapshot, rollback legacyMigrationBackupResult, report store.LegacyHumanUsersImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyHumanUsersMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-human-users",
		Source:         legacyMigrationSourceResult{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256},
		RollbackBackup: rollback, Result: report,
	})
}

func runLegacyGroupsRoutesMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-groups-routes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-groups-routes requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-groups-routes requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadGroupsRoutesSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy groups/routes migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy groups/routes migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy groups/routes migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy groups/routes migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy groups/routes migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy groups/routes migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy groups/routes migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyGroupsRoutesImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy groups/routes migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed groups/routes migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy groups/routes rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy groups/routes rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyGroupsRoutesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy groups/routes migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy groups/routes rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import groups/routes rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import groups/routes rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import groups/routes rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyGroupsRoutesImport{
		Slice: store.LegacyGroupsRoutesSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Groups: snapshot.Groups, Routes: snapshot.Routes, Checksums: snapshot.Checksums,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyGroupsRoutes(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyGroupsRoutesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyGroupsRoutesMigrationResult(output io.Writer, snapshot legacymigration.GroupsRoutesSnapshot, rollback legacyMigrationBackupResult, report store.LegacyGroupsRoutesImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyGroupsRoutesMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-groups-routes",
		Source:         legacyMigrationSourceResult{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256},
		RollbackBackup: rollback, Result: report,
	})
}

func runLegacyNodesMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-nodes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application and legacy report workers are stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-nodes requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-nodes requires --confirm-offline after the target application and legacy report workers are stopped")
	}

	snapshot, err := legacymigration.ReadNodesSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy node migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy node migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy node migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy node migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy node migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy node migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy node migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyNodesImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy node migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed node migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy node rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy node rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyNodesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy node migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy node rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import node rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import node rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import node rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyNodesImport{
		Slice: store.LegacyNodesSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Machines: snapshot.Machines, Credentials: snapshot.Credentials, Enrollments: snapshot.Enrollments,
		LoadHistory: snapshot.LoadHistory, Nodes: snapshot.Nodes, Schedules: snapshot.Schedules, Traffic: snapshot.Traffic,
		Checksums: snapshot.Checksums, RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyNodes(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyNodesMigrationResult(stdout, snapshot, legacyMigrationBackupResult{Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest}, report)
}

func encodeLegacyNodesMigrationResult(output io.Writer, snapshot legacymigration.NodesSnapshot, rollback legacyMigrationBackupResult, report store.LegacyNodesImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyNodesMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-nodes",
		Source: legacyMigrationSourceResult{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256}, RollbackBackup: rollback, Result: report,
	})
}

func runLegacySubscriptionConfigMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-subscription-config", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-subscription-config requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-subscription-config requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadSubscriptionConfigSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy subscription config migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy subscription config migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy subscription config migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy subscription config migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy subscription config migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy subscription config migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy subscription config migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacySubscriptionConfigImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy subscription config migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed subscription config migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy subscription config rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy subscription config rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacySubscriptionConfigMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy subscription config migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy subscription config rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import subscription config rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import subscription config rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import subscription config rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacySubscriptionConfigImport{
		Slice: store.LegacySubscriptionConfigSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Config: snapshot.Config, Checksum: snapshot.Checksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacySubscriptionConfig(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacySubscriptionConfigMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacySubscriptionConfigMigrationResult(output io.Writer, snapshot legacymigration.SubscriptionConfigSnapshot, rollback legacyMigrationBackupResult, report store.LegacySubscriptionConfigImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacySubscriptionConfigMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-subscription-config",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func runLegacyOrdersMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-orders", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-orders requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-orders requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadOrdersSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy order migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy order migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy order migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy order migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy order migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy order migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy order migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyOrdersImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy order migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed order migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy order rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy order rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyOrdersMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy order migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy order rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import order rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import order rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import order rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyOrdersImport{
		Slice: store.LegacyOrdersSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Orders: snapshot.Orders, Checksum: snapshot.Checksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyOrders(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyOrdersMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyOrdersMigrationResult(output io.Writer, snapshot legacymigration.OrdersSnapshot, rollback legacyMigrationBackupResult, report store.LegacyOrdersImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyOrdersMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-orders",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func runLegacyPlansMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-plans", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-plans requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-plans requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadPlansSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy plan migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy plan migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy plan migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy plan migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy plan migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy plan migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy plan migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyPlansImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy plan migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed plan migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy plan rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy plan rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyPlansMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy plan migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy plan rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import plan rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import plan rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import plan rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyPlansImport{
		Slice: store.LegacyPlansSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Plans: snapshot.Plans, Checksum: snapshot.Checksum, TrafficResetMethod: snapshot.TrafficResetMethod,
		SettingsChecksum:   snapshot.SettingsChecksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyPlans(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyPlansMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyPlansMigrationResult(output io.Writer, snapshot legacymigration.PlansSnapshot, rollback legacyMigrationBackupResult, report store.LegacyPlansImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyPlansMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-plans",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func runLegacyCouponsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-coupons", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-coupons requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-coupons requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadCouponsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy coupon migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy coupon migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy coupon migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy coupon migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy coupon migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy coupon migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy coupon migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyCouponsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy coupon migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed coupon migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy coupon rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy coupon rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyCouponsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy coupon migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy coupon rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import coupon rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import coupon rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import coupon rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyCouponsImport{
		Slice: store.LegacyCouponsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Coupons: snapshot.Coupons, CouponsChecksum: snapshot.CouponsChecksum,
		CouponEnabled: snapshot.CouponEnabled, SettingsChecksum: snapshot.SettingsChecksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyCoupons(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyCouponsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyCouponsMigrationResult(output io.Writer, snapshot legacymigration.CouponsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyCouponsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyCouponsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-coupons",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func runLegacyGiftCardsMigrationCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	flags := flag.NewFlagSet("migration import-legacy-gift-cards", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourcePath := flags.String("source", "", "standalone legacy Xboard SQLite snapshot path")
	backupOutput := flags.String("backup-output", "", "new pre-import Xboard-Go rollback archive path")
	confirmOffline := flags.Bool("confirm-offline", false, "confirm the target application is stopped")
	if err := flags.Parse(arguments); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*sourcePath) == "" {
		return true, errors.New("migration import-legacy-gift-cards requires --source and accepts no positional arguments")
	}
	if !*confirmOffline {
		return true, errors.New("migration import-legacy-gift-cards requires --confirm-offline after the target application is stopped")
	}

	snapshot, err := legacymigration.ReadGiftCardsSnapshot(ctx, *sourcePath)
	if err != nil {
		return true, err
	}
	targetDSN := config.DatabaseDSN()
	targetPath, ok := sqliteFilePath(targetDSN)
	if !ok {
		return true, errors.New("legacy gift card migration requires a file-backed Xboard-Go SQLite target")
	}
	targetPath, err = filepath.Abs(targetPath)
	if err != nil {
		return true, fmt.Errorf("resolve legacy gift card migration target: %w", err)
	}
	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return true, fmt.Errorf("inspect legacy gift card migration target: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return true, errors.New("legacy gift card migration target must be a regular file")
	}
	sourceInfo, err := os.Lstat(snapshot.Path)
	if err != nil {
		return true, fmt.Errorf("reinspect legacy gift card migration source: %w", err)
	}
	if os.SameFile(sourceInfo, targetInfo) {
		return true, errors.New("legacy gift card migration source and Xboard-Go target must be different files")
	}

	database, err := store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		_ = database.Close()
		return true, fmt.Errorf("legacy gift card migration target validation failed: %w", err)
	}
	existing, found, err := database.LookupLegacyGiftCardsImport(ctx, snapshot.SHA256)
	closeErr := database.Close()
	if err != nil {
		return true, err
	}
	if closeErr != nil {
		return true, fmt.Errorf("close legacy gift card migration target: %w", closeErr)
	}
	if found {
		if strings.TrimSpace(*backupOutput) != "" {
			requested, err := filepath.Abs(*backupOutput)
			if err != nil {
				return true, err
			}
			recorded, err := filepath.Abs(existing.RollbackBackupPath)
			if err != nil || requested != recorded {
				return true, errors.New("--backup-output does not match the rollback backup recorded by the completed gift card migration")
			}
		}
		manifest, err := backup.Verify(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, fmt.Errorf("verify recorded legacy gift card rollback backup: %w", err)
		}
		digest, _, err := hashMigrationArtifact(ctx, existing.RollbackBackupPath)
		if err != nil {
			return true, err
		}
		if digest != existing.RollbackBackupSHA256 {
			return true, errors.New("recorded legacy gift card rollback backup digest does not match")
		}
		if err := secureSQLiteFiles(targetDSN); err != nil {
			return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
		}
		return true, encodeLegacyGiftCardsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
			Path: existing.RollbackBackupPath, SHA256: digest, Manifest: manifest,
		}, existing)
	}

	if strings.TrimSpace(*backupOutput) == "" {
		return true, errors.New("a new legacy gift card migration requires --backup-output")
	}
	rollbackPath, err := filepath.Abs(*backupOutput)
	if err != nil {
		return true, fmt.Errorf("resolve legacy gift card rollback backup: %w", err)
	}
	if rollbackPath == targetPath || rollbackPath == snapshot.Path {
		return true, errors.New("rollback backup path must differ from the source and target databases")
	}
	createdManifest, err := backup.Create(ctx, targetDSN, rollbackPath, buildRevision, now().UTC(), configuredAttachmentRoot())
	if err != nil {
		return true, fmt.Errorf("create pre-import gift card rollback backup: %w", err)
	}
	verifiedManifest, err := backup.Verify(ctx, rollbackPath)
	if err != nil {
		return true, fmt.Errorf("verify pre-import gift card rollback backup: %w", err)
	}
	if createdManifest != verifiedManifest {
		return true, errors.New("pre-import gift card rollback backup manifest changed during verification")
	}
	rollbackDigest, _, err := hashMigrationArtifact(ctx, rollbackPath)
	if err != nil {
		return true, err
	}

	database, err = store.OpenSQLite(targetDSN)
	if err != nil {
		return true, err
	}
	input := store.LegacyGiftCardsImport{
		Slice: store.LegacyGiftCardsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Templates: snapshot.Templates, Codes: snapshot.Codes, Usages: snapshot.Usages,
		TemplatesChecksum: snapshot.TemplatesChecksum, CodesChecksum: snapshot.CodesChecksum, UsagesChecksum: snapshot.UsagesChecksum,
		RollbackBackupPath: rollbackPath, RollbackBackupSHA256: rollbackDigest,
	}
	report, importErr := database.ImportLegacyGiftCards(ctx, input, now().UTC())
	closeErr = database.Close()
	if importErr != nil {
		return true, importErr
	}
	if closeErr != nil {
		return true, fmt.Errorf("close imported Xboard-Go database: %w", closeErr)
	}
	if err := secureSQLiteFiles(targetDSN); err != nil {
		return true, fmt.Errorf("secure imported Xboard-Go database: %w", err)
	}
	return true, encodeLegacyGiftCardsMigrationResult(stdout, snapshot, legacyMigrationBackupResult{
		Path: rollbackPath, SHA256: rollbackDigest, Manifest: verifiedManifest,
	}, report)
}

func encodeLegacyGiftCardsMigrationResult(output io.Writer, snapshot legacymigration.GiftCardsSnapshot, rollback legacyMigrationBackupResult, report store.LegacyGiftCardsImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyGiftCardsMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-gift-cards",
		Source: snapshotSource(snapshot.Path, snapshot.Size, snapshot.SHA256), RollbackBackup: rollback, Result: report,
	})
}

func snapshotSource(path string, size int64, sha256 string) legacyMigrationSourceResult {
	return legacyMigrationSourceResult{Path: path, Size: size, SHA256: sha256}
}

func encodeLegacyMigrationResult(output io.Writer, snapshot legacymigration.ContentSnapshot, rollback legacyMigrationBackupResult, report store.LegacyContentImportReport) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(legacyMigrationCommandResult{
		Status: "success", Action: "migration.import-legacy-content",
		Source:         legacyMigrationSourceResult{Path: snapshot.Path, Size: snapshot.Size, SHA256: snapshot.SHA256},
		RollbackBackup: rollback, Result: report,
	})
}

func hashMigrationArtifact(ctx context.Context, path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, fmt.Errorf("inspect migration artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, errors.New("migration artifact must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open migration artifact: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return "", 0, errors.New("migration artifact changed before hashing")
	}
	hash := sha256.New()
	buffer := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", 0, fmt.Errorf("hash migration artifact: %w", readErr)
		}
	}
	finalInfo, err := file.Stat()
	if err != nil || finalInfo.Size() != openedInfo.Size() || !finalInfo.ModTime().Equal(openedInfo.ModTime()) {
		return "", 0, errors.New("migration artifact changed while hashing")
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), openedInfo.Size(), nil
}

func runMaintenanceCommand(ctx context.Context, arguments []string, stdout, stderr io.Writer, now func() time.Time) (bool, error) {
	if len(arguments) == 0 {
		return true, errors.New("maintenance subcommand is required: cleanup-expired")
	}
	if arguments[0] != "cleanup-expired" {
		return true, fmt.Errorf("unknown maintenance subcommand %q", arguments[0])
	}
	flags := flag.NewFlagSet("maintenance cleanup-expired", flag.ContinueOnError)
	flags.SetOutput(stderr)
	limit := flags.Int("limit", maintenance.DefaultCleanupLimit, "maximum rows processed per cleanup category")
	if err := flags.Parse(arguments[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, errors.New("maintenance cleanup-expired does not accept positional arguments")
	}
	if *limit < 1 || *limit > maintenance.MaxCleanupLimit {
		return true, fmt.Errorf("maintenance cleanup-expired --limit must be between 1 and %d", maintenance.MaxCleanupLimit)
	}

	dsn := config.DatabaseDSN()
	path, ok := sqliteFilePath(dsn)
	if !ok {
		return true, errors.New("maintenance cleanup-expired requires a file-backed SQLite database")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return true, fmt.Errorf("inspect maintenance database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return true, errors.New("maintenance database must be a regular file")
	}
	database, err := store.OpenSQLite(dsn)
	if err != nil {
		return true, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = database.Close()
		}
	}()
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		return true, fmt.Errorf("maintenance cleanup-expired schema validation failed: %w; run the versioned migration workflow first", err)
	}
	asOf := now().UTC()
	result, err := maintenance.CleanupExpired(ctx, database, asOf, *limit)
	if err != nil {
		return true, err
	}
	if err := database.Close(); err != nil {
		return true, fmt.Errorf("close maintenance database: %w", err)
	}
	closed = true
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	return true, encoder.Encode(maintenanceCommandResult{
		Status: "success", Action: "maintenance.cleanup-expired", AsOf: asOf, Limit: *limit, Result: result,
	})
}

func encodeCommandResult(output io.Writer, result commandResult) error {
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}

func configuredAttachmentRoot() string {
	return strings.TrimSpace(os.Getenv("XBOARD_ATTACHMENT_ROOT"))
}

func initializeInvitationProtector(ctx context.Context, database *store.Store, key []byte) (*security.InvitationProtector, error) {
	required, err := database.InvitationProtectionRequired(ctx)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		if required {
			return nil, errors.New("settings encryption key is required for invitation codes")
		}
		return nil, nil
	}
	protector, err := security.NewInvitationProtector(key)
	if err != nil {
		return nil, err
	}
	ownerID, ciphertext, exists, err := database.InvitationProtectionProbe(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return protector, nil
	}
	plaintext, err := protector.DecryptCode(ownerID, ciphertext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	if err != nil {
		return nil, errors.New("settings encryption key cannot decrypt the stored invitation code")
	}
	return protector, nil
}

func initializeLoginLinkProtector(ctx context.Context, database *store.Store, key []byte) (*security.LoginLinkProtector, error) {
	required, err := database.LoginLinkProtectionRequired(ctx)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		if required {
			return nil, errors.New("settings encryption key is required for login links")
		}
		return nil, nil
	}
	protector, err := security.NewLoginLinkProtector(key)
	if err != nil {
		return nil, err
	}
	ownerID, ciphertext, exists, err := database.LoginLinkProtectionProbe(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		return protector, nil
	}
	plaintext, err := protector.DecryptToken(ownerID, ciphertext)
	for index := range plaintext {
		plaintext[index] = 0
	}
	if err != nil {
		return nil, errors.New("settings encryption key cannot decrypt the queued login link")
	}
	return protector, nil
}

func initializeSettingsCipher(ctx context.Context, database *store.Store, key []byte) (*appsettings.Cipher, error) {
	ciphertext, err := database.GetSMTPPasswordCipher(ctx)
	if err != nil {
		return nil, err
	}
	captchaSecrets, err := database.GetCaptchaSecretCiphers(ctx)
	if err != nil {
		return nil, err
	}
	paymentConfigs, err := database.ListStoredPaymentConfigs(ctx)
	if err != nil {
		return nil, err
	}
	settingsSecretsExist := len(ciphertext) > 0 || len(captchaSecrets.Recaptcha) > 0 || len(captchaSecrets.RecaptchaV3) > 0 || len(captchaSecrets.Turnstile) > 0 || len(paymentConfigs) > 0
	if len(key) == 0 {
		if settingsSecretsExist {
			return nil, errors.New("settings encryption key is required for stored credentials")
		}
		return nil, nil
	}
	cipherBox, err := appsettings.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) > 0 {
		plaintext, err := cipherBox.Decrypt(ciphertext)
		if err != nil {
			return nil, errors.New("settings encryption key cannot decrypt the stored SMTP credential")
		}
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	for purpose, secretCiphertext := range map[appsettings.SecretPurpose][]byte{
		appsettings.RecaptchaSecretPurpose:   captchaSecrets.Recaptcha,
		appsettings.RecaptchaV3SecretPurpose: captchaSecrets.RecaptchaV3,
		appsettings.TurnstileSecretPurpose:   captchaSecrets.Turnstile,
	} {
		if len(secretCiphertext) == 0 {
			continue
		}
		plaintext, err := cipherBox.DecryptFor(purpose, secretCiphertext)
		if err != nil {
			return nil, errors.New("settings encryption key cannot decrypt a stored CAPTCHA credential")
		}
		for index := range plaintext {
			plaintext[index] = 0
		}
	}
	for _, config := range paymentConfigs {
		if _, err := payment.OpenConfig(cipherBox, config.Provider, config.Ciphertext); err != nil {
			return nil, errors.New("settings encryption key cannot decrypt a stored payment credential")
		}
	}
	return cipherBox, nil
}

func runHealthcheck() error {
	address := strings.TrimSpace(os.Getenv("XBOARD_HEALTH_URL"))
	if address == "" {
		address = "http://127.0.0.1:8080/healthz"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", response.Status)
	}
	return nil
}

func prepareSQLiteDirectory(dsn string) error {
	path, ok := sqliteFilePath(dsn)
	if !ok {
		return nil
	}
	directory := filepath.Dir(path)
	if directory == "." || directory == "" {
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.Chmod(directory, 0o700)
}

func secureSQLiteFiles(dsn string) error {
	path, ok := sqliteFilePath(dsn)
	if !ok {
		return nil
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", filepath.Base(candidate), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", filepath.Base(candidate))
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("restrict %s permissions: %w", filepath.Base(candidate), err)
		}
	}
	return nil
}

func sqliteFilePath(dsn string) (string, bool) {
	if !strings.HasPrefix(dsn, "file:") || strings.Contains(strings.ToLower(dsn), "mode=memory") {
		return "", false
	}
	path := strings.TrimPrefix(strings.SplitN(dsn, "?", 2)[0], "file:")
	if path == "" || path == ":memory:" {
		return "", false
	}
	return path, true
}
