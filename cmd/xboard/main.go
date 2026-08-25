package main

import (
	"context"
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

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/captcha"
	"github.com/Hao-Monster/Xboard-Go/internal/config"
	"github.com/Hao-Monster/Xboard-Go/internal/httpapi"
	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/maintenance"
	"github.com/Hao-Monster/Xboard-Go/internal/operations"
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
	mailWorker := mailer.NewWorker(database, settingsCipher, passwordResetProtector, registrationEmailProtector, loginLinkProtector, mailer.NewSMTPSender(10*time.Second, settings.SMTPAllowInsecure), settings.MailPollInterval, logger, runtimeTracker)
	go mailWorker.Run(ctx)

	var handler http.Handler = httpapi.New(httpapi.Dependencies{
		Store:                      database,
		PasswordHasher:             passwordHasher,
		PanelURL:                   settings.PanelURL,
		NodeRelease:                settings.NodeRelease,
		CookieSecure:               settings.CookieSecure,
		AllowedOrigins:             settings.AllowedOrigins,
		Logger:                     logger,
		Context:                    ctx,
		WebSocketEnabled:           settings.WebSocketEnabled,
		WebSocketURL:               settings.WebSocketURL,
		NodePushInterval:           settings.NodePushInterval,
		NodePullInterval:           settings.NodePullInterval,
		SettingsCipher:             settingsCipher,
		PasswordResetProtector:     passwordResetProtector,
		RegistrationEmailProtector: registrationEmailProtector,
		InvitationProtector:        invitationProtector,
		LoginLinkProtector:         loginLinkProtector,
		SMTPAllowInsecure:          settings.SMTPAllowInsecure,
		RuntimeTracker:             runtimeTracker,
		CaptchaVerifier:            captchaVerifier,
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

type commandResult struct {
	Status   string          `json:"status"`
	Action   string          `json:"action"`
	Path     string          `json:"path"`
	Manifest backup.Manifest `json:"manifest"`
}

type maintenanceCommandResult struct {
	Status string                    `json:"status"`
	Action string                    `json:"action"`
	AsOf   time.Time                 `json:"as_of"`
	Limit  int                       `json:"limit"`
	Result maintenance.CleanupResult `json:"result"`
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
		if err := flags.Parse(arguments[2:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, errors.New("backup create does not accept positional arguments")
		}
		manifest, err := backup.Create(ctx, config.DatabaseDSN(), *output, buildRevision, now())
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
		if err := flags.Parse(arguments[2:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*input) == "" || strings.TrimSpace(*output) == "" {
			return true, errors.New("backup restore requires --input and --output and accepts no positional arguments")
		}
		manifest, err := backup.Restore(ctx, *input, *output)
		if err != nil {
			return true, err
		}
		absolute, err := filepath.Abs(*output)
		if err != nil {
			return true, err
		}
		return true, encodeCommandResult(stdout, commandResult{Status: "success", Action: "backup.restore", Path: absolute, Manifest: manifest})

	default:
		return true, fmt.Errorf("unknown backup subcommand %q", arguments[1])
	}
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
	settingsSecretsExist := len(ciphertext) > 0 || len(captchaSecrets.Recaptcha) > 0 || len(captchaSecrets.RecaptchaV3) > 0 || len(captchaSecrets.Turnstile) > 0
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
