package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/Hao-Monster/Xboard-Go/internal/config"
	"github.com/Hao-Monster/Xboard-Go/internal/httpapi"
	"github.com/Hao-Monster/Xboard-Go/internal/mailer"
	"github.com/Hao-Monster/Xboard-Go/internal/scheduler"
	"github.com/Hao-Monster/Xboard-Go/internal/security"
	appsettings "github.com/Hao-Monster/Xboard-Go/internal/settings"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/webui"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := runHealthcheck(); err != nil {
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

	worker := scheduler.NewWorker(database, settings.SchedulerInterval, logger)
	go worker.Run(ctx)
	mailWorker := mailer.NewWorker(database, settingsCipher, mailer.NewSMTPSender(10*time.Second, settings.SMTPAllowInsecure), settings.MailPollInterval, logger)
	go mailWorker.Run(ctx)

	var handler http.Handler = httpapi.New(httpapi.Dependencies{
		Store:             database,
		PasswordHasher:    passwordHasher,
		PanelURL:          settings.PanelURL,
		NodeRelease:       settings.NodeRelease,
		CookieSecure:      settings.CookieSecure,
		AllowedOrigins:    settings.AllowedOrigins,
		Logger:            logger,
		Context:           ctx,
		WebSocketEnabled:  settings.WebSocketEnabled,
		WebSocketURL:      settings.WebSocketURL,
		NodePushInterval:  settings.NodePushInterval,
		NodePullInterval:  settings.NodePullInterval,
		SettingsCipher:    settingsCipher,
		SMTPAllowInsecure: settings.SMTPAllowInsecure,
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

func initializeSettingsCipher(ctx context.Context, database *store.Store, key []byte) (*appsettings.Cipher, error) {
	ciphertext, err := database.GetSMTPPasswordCipher(ctx)
	if err != nil {
		return nil, err
	}
	if len(key) == 0 {
		if len(ciphertext) > 0 {
			return nil, errors.New("settings encryption key is required for the stored SMTP credential")
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
	if !strings.HasPrefix(dsn, "file:") || strings.Contains(dsn, "mode=memory") {
		return nil
	}
	path := strings.TrimPrefix(strings.SplitN(dsn, "?", 2)[0], "file:")
	directory := filepath.Dir(path)
	if directory == "." || directory == "" {
		return nil
	}
	return os.MkdirAll(directory, 0o700)
}
