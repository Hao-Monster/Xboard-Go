package config

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadReadsAndValidatesSettingsEncryptionKey(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", encoded)
	t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY_FILE", "")
	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.SettingsEncryptionKey) != 32 || settings.SettingsEncryptionKey[0] != 0x42 {
		t.Fatal("Load() did not decode the 256-bit settings key")
	}

	for _, invalid := range []string{"not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		t.Setenv("XBOARD_SETTINGS_ENCRYPTION_KEY", invalid)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted invalid settings encryption key %q", invalid)
		}
	}
}

func TestLoadValidatesBootstrapPairAndInterval(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "admin@example.test")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted incomplete bootstrap credentials")
	}

	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "strong-password-123")
	t.Setenv("XBOARD_SCHEDULER_INTERVAL", "10ms")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an unsafe scheduler interval")
	}
}

func TestLoadReadsExplicitConfiguration(t *testing.T) {
	t.Setenv("XBOARD_ADDRESS", "127.0.0.1:9090")
	t.Setenv("XBOARD_DATABASE_DSN", "file:test.db")
	t.Setenv("XBOARD_PANEL_URL", "https://panel.example.test")
	t.Setenv("XBOARD_LEGACY_ADMIN_PATH", "53815c85")
	t.Setenv("XBOARD_ALLOWED_ORIGINS", "https://panel.example.test, https://admin.example.test/")
	t.Setenv("XBOARD_COOKIE_SECURE", "true")
	t.Setenv("XBOARD_SCHEDULER_INTERVAL", "2s")
	t.Setenv("XBOARD_WEBSOCKET_ENABLED", "true")
	t.Setenv("XBOARD_WEBSOCKET_URL", "wss://panel.example.test/ws")
	t.Setenv("XBOARD_NODE_PUSH_INTERVAL", "15")
	t.Setenv("XBOARD_NODE_PULL_INTERVAL", "30")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("XBOARD_WEB_ROOT", filepath.Join(t.TempDir(), "web"))

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Address != "127.0.0.1:9090" || settings.DatabaseDSN != "file:test.db" || !settings.CookieSecure || settings.SchedulerInterval != 2*time.Second ||
		settings.LegacyAdminPath != "53815c85" || !settings.WebSocketEnabled || settings.WebSocketURL != "wss://panel.example.test/ws" || settings.NodePushInterval != 15 || settings.NodePullInterval != 30 || settings.WebRoot == "" {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	if len(settings.AllowedOrigins) != 2 || settings.AllowedOrigins[1] != "https://admin.example.test" {
		t.Fatalf("allowed origins = %#v", settings.AllowedOrigins)
	}
}

func TestLoadRejectsUnsafeLegacyAdminPath(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	for _, value := range []string{"/admin", "admin/order", "..", "admin path"} {
		t.Setenv("XBOARD_LEGACY_ADMIN_PATH", value)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted unsafe legacy administrator path %q", value)
		}
	}
}

func TestLoadReadsBootstrapPasswordFromFile(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "bootstrap-password")
	if err := os.WriteFile(secretPath, []byte("strong-password-from-file-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "admin@example.test")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", secretPath)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.BootstrapAdminPassword != "strong-password-from-file-123" {
		t.Fatal("Load() did not read the bootstrap secret file")
	}

	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "strong-direct-password")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted both direct and file-backed bootstrap passwords")
	}
}

func TestLoadRejectsRelativeWebRoot(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("XBOARD_WEB_ROOT", "web/dist")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a relative web root")
	}
}

func TestLoadRejectsInvalidWebSocketAndNodeIntervals(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_WEBSOCKET_URL", "https://panel.example.test/ws")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a non-WebSocket URL")
	}
	t.Setenv("XBOARD_WEBSOCKET_URL", "wss://panel.example.test/ws")
	t.Setenv("XBOARD_NODE_PUSH_INTERVAL", "4")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a node push interval below five seconds")
	}
}

func TestLoadGatesCaptchaVerificationEndpointOverrides(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL", "http://captcha-stub.test/verify")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a CAPTCHA endpoint override without the explicit test gate")
	}
	t.Setenv("XBOARD_CAPTCHA_ALLOW_INSECURE", "true")
	settings, err := Load()
	if err != nil || settings.CaptchaRecaptchaURL != "http://captcha-stub.test/verify" || !settings.CaptchaAllowInsecure {
		t.Fatalf("Load() test CAPTCHA endpoint = %#v err=%v", settings, err)
	}
	t.Setenv("XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL", "http://user:password@captcha-stub.test/verify")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a CAPTCHA endpoint with userinfo")
	}
}

func TestLoadDefaultsToSecureCookiesForHTTPSPanel(t *testing.T) {
	t.Setenv("XBOARD_PANEL_URL", "https://panel.example.test")
	t.Setenv("XBOARD_COOKIE_SECURE", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settings.CookieSecure {
		t.Fatal("HTTPS panel must default to secure cookies")
	}

	t.Setenv("XBOARD_COOKIE_SECURE", "false")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted insecure cookies for an HTTPS panel")
	}
}

func TestLoadRequiresImmutableNodeRelease(t *testing.T) {
	t.Setenv("XBOARD_PANEL_URL", "http://127.0.0.1:5173")
	t.Setenv("XBOARD_COOKIE_SECURE", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_NODE_RELEASE", "")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.NodeRelease != "v1.14.3" {
		t.Fatalf("default NodeRelease = %q, want v1.14.3", settings.NodeRelease)
	}

	for _, invalid := range []string{"latest", "v1.14", "../../v1.14.0"} {
		t.Setenv("XBOARD_NODE_RELEASE", invalid)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted mutable or invalid node release %q", invalid)
		}
	}

	t.Setenv("XBOARD_NODE_RELEASE", "v1.14.0")
	if _, err := Load(); err != nil {
		t.Fatalf("Load() rejected immutable node release: %v", err)
	}
}
