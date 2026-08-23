package config

import (
	"testing"
	"time"
)

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
	t.Setenv("XBOARD_ALLOWED_ORIGINS", "https://panel.example.test, https://admin.example.test/")
	t.Setenv("XBOARD_COOKIE_SECURE", "true")
	t.Setenv("XBOARD_SCHEDULER_INTERVAL", "2s")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Address != "127.0.0.1:9090" || settings.DatabaseDSN != "file:test.db" || !settings.CookieSecure || settings.SchedulerInterval != 2*time.Second {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	if len(settings.AllowedOrigins) != 2 || settings.AllowedOrigins[1] != "https://admin.example.test" {
		t.Fatalf("allowed origins = %#v", settings.AllowedOrigins)
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
	if settings.NodeRelease != "v1.14.2" {
		t.Fatalf("default NodeRelease = %q, want v1.14.2", settings.NodeRelease)
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
