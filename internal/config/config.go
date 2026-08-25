package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var immutableNodeReleaseRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$`)

type Config struct {
	Address                string
	DatabaseDSN            string
	PanelURL               string
	AllowedOrigins         []string
	CookieSecure           bool
	NodeRelease            string
	BootstrapAdminEmail    string
	BootstrapAdminPassword string
	SchedulerInterval      time.Duration
	MailPollInterval       time.Duration
	SMTPAllowInsecure      bool
	CaptchaAllowInsecure   bool
	CaptchaRecaptchaURL    string
	CaptchaRecaptchaV3URL  string
	CaptchaTurnstileURL    string
	SettingsEncryptionKey  []byte
	WebSocketEnabled       bool
	WebSocketURL           string
	NodePushInterval       int
	NodePullInterval       int
	WebRoot                string
}

func Load() (Config, error) {
	panelURL := envOrDefault("XBOARD_PANEL_URL", "http://127.0.0.1:5173")
	cookieSecure, err := parseBoolEnv("XBOARD_COOKIE_SECURE", strings.HasPrefix(strings.ToLower(panelURL), "https://"))
	if err != nil {
		return Config{}, err
	}
	interval, err := parseDurationEnv("XBOARD_SCHEDULER_INTERVAL", time.Second)
	if err != nil {
		return Config{}, err
	}
	mailPollInterval, err := parseDurationEnv("XBOARD_MAIL_POLL_INTERVAL", 5*time.Second)
	if err != nil {
		return Config{}, err
	}
	smtpAllowInsecure, err := parseBoolEnv("XBOARD_SMTP_ALLOW_INSECURE", false)
	if err != nil {
		return Config{}, err
	}
	captchaAllowInsecure, err := parseBoolEnv("XBOARD_CAPTCHA_ALLOW_INSECURE", false)
	if err != nil {
		return Config{}, err
	}
	webSocketEnabled, err := parseBoolEnv("XBOARD_WEBSOCKET_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	nodePushInterval, err := parseIntEnv("XBOARD_NODE_PUSH_INTERVAL", 60)
	if err != nil {
		return Config{}, err
	}
	nodePullInterval, err := parseIntEnv("XBOARD_NODE_PULL_INTERVAL", 60)
	if err != nil {
		return Config{}, err
	}

	bootstrapPassword, err := readSecretEnv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD")
	if err != nil {
		return Config{}, err
	}
	settingsKeyValue, err := readSecretEnv("XBOARD_SETTINGS_ENCRYPTION_KEY")
	if err != nil {
		return Config{}, err
	}
	var settingsEncryptionKey []byte
	if strings.TrimSpace(settingsKeyValue) != "" {
		settingsEncryptionKey, err = base64.StdEncoding.DecodeString(strings.TrimSpace(settingsKeyValue))
		if err != nil || len(settingsEncryptionKey) != 32 {
			return Config{}, errors.New("XBOARD_SETTINGS_ENCRYPTION_KEY must be a base64-encoded 256-bit key")
		}
	}

	config := Config{
		Address:                envOrDefault("XBOARD_ADDRESS", "127.0.0.1:8080"),
		DatabaseDSN:            envOrDefault("XBOARD_DATABASE_DSN", "file:./data/xboard.db"),
		PanelURL:               panelURL,
		CookieSecure:           cookieSecure,
		NodeRelease:            envOrDefault("XBOARD_NODE_RELEASE", "v1.14.3"),
		BootstrapAdminEmail:    strings.TrimSpace(os.Getenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL")),
		BootstrapAdminPassword: bootstrapPassword,
		SchedulerInterval:      interval,
		MailPollInterval:       mailPollInterval,
		SMTPAllowInsecure:      smtpAllowInsecure,
		CaptchaAllowInsecure:   captchaAllowInsecure,
		CaptchaRecaptchaURL:    strings.TrimSpace(os.Getenv("XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL")),
		CaptchaRecaptchaV3URL:  strings.TrimSpace(os.Getenv("XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL")),
		CaptchaTurnstileURL:    strings.TrimSpace(os.Getenv("XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL")),
		SettingsEncryptionKey:  append([]byte(nil), settingsEncryptionKey...),
		WebSocketEnabled:       webSocketEnabled,
		WebSocketURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("XBOARD_WEBSOCKET_URL")), "/"),
		NodePushInterval:       nodePushInterval,
		NodePullInterval:       nodePullInterval,
		WebRoot:                strings.TrimSpace(os.Getenv("XBOARD_WEB_ROOT")),
	}
	if origins := strings.TrimSpace(os.Getenv("XBOARD_ALLOWED_ORIGINS")); origins != "" {
		for _, origin := range strings.Split(origins, ",") {
			if value := strings.TrimRight(strings.TrimSpace(origin), "/"); value != "" {
				config.AllowedOrigins = append(config.AllowedOrigins, value)
			}
		}
	}

	hasEmail := config.BootstrapAdminEmail != ""
	hasPassword := config.BootstrapAdminPassword != ""
	if hasEmail != hasPassword {
		return Config{}, errors.New("XBOARD_BOOTSTRAP_ADMIN_EMAIL and XBOARD_BOOTSTRAP_ADMIN_PASSWORD must be set together")
	}
	if hasPassword && len(config.BootstrapAdminPassword) < 12 {
		return Config{}, errors.New("bootstrap administrator password must contain at least 12 characters")
	}
	if config.WebRoot != "" && !filepath.IsAbs(config.WebRoot) {
		return Config{}, errors.New("XBOARD_WEB_ROOT must be an absolute path")
	}
	if config.SchedulerInterval < 100*time.Millisecond || config.SchedulerInterval > time.Minute {
		return Config{}, errors.New("XBOARD_SCHEDULER_INTERVAL must be between 100ms and 1m")
	}
	if config.MailPollInterval < 250*time.Millisecond || config.MailPollInterval > time.Minute {
		return Config{}, errors.New("XBOARD_MAIL_POLL_INTERVAL must be between 250ms and 1m")
	}
	if config.NodePushInterval < 5 || config.NodePushInterval > 3_600 || config.NodePullInterval < 5 || config.NodePullInterval > 3_600 {
		return Config{}, errors.New("node push and pull intervals must be between 5 and 3600 seconds")
	}
	if !immutableNodeReleaseRE.MatchString(config.NodeRelease) {
		return Config{}, errors.New("XBOARD_NODE_RELEASE must be an immutable semantic version such as v1.14.3")
	}
	parsedPanelURL, err := url.Parse(config.PanelURL)
	if err != nil || parsedPanelURL.Host == "" || (parsedPanelURL.Scheme != "http" && parsedPanelURL.Scheme != "https") {
		return Config{}, errors.New("XBOARD_PANEL_URL must be an absolute http or https URL")
	}
	if parsedPanelURL.Scheme == "https" && !config.CookieSecure {
		return Config{}, errors.New("XBOARD_COOKIE_SECURE cannot be false when XBOARD_PANEL_URL uses https")
	}
	if config.WebSocketURL != "" {
		parsedWebSocketURL, err := url.Parse(config.WebSocketURL)
		if err != nil || parsedWebSocketURL.Host == "" || (parsedWebSocketURL.Scheme != "ws" && parsedWebSocketURL.Scheme != "wss") {
			return Config{}, errors.New("XBOARD_WEBSOCKET_URL must be an absolute ws or wss URL")
		}
	}
	for name, endpoint := range map[string]string{
		"XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL":    config.CaptchaRecaptchaURL,
		"XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL": config.CaptchaRecaptchaV3URL,
		"XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL":    config.CaptchaTurnstileURL,
	} {
		if endpoint == "" {
			continue
		}
		if !config.CaptchaAllowInsecure {
			return Config{}, fmt.Errorf("%s is test-only and requires XBOARD_CAPTCHA_ALLOW_INSECURE=true", name)
		}
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return Config{}, fmt.Errorf("%s must be an absolute http or https URL without userinfo", name)
		}
	}
	return config, nil
}

func readSecretEnv(name string) (string, error) {
	direct := os.Getenv(name)
	fileName := strings.TrimSpace(os.Getenv(name + "_FILE"))
	if direct != "" && fileName != "" {
		return "", fmt.Errorf("%s and %s_FILE cannot both be set", name, name)
	}
	if fileName == "" {
		return direct, nil
	}
	info, err := os.Stat(fileName)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<10 {
		return "", fmt.Errorf("%s_FILE must reference a regular file no larger than 4096 bytes", name)
	}
	value, err := os.ReadFile(fileName)
	if err != nil {
		return "", fmt.Errorf("read %s_FILE: %w", name, err)
	}
	return strings.TrimRight(string(value), "\r\n"), nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func parseBoolEnv(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func parseDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", name, err)
	}
	return parsed, nil
}

func parseIntEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}
