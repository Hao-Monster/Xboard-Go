package config

import (
	"bytes"
	"encoding/base64"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
	t.Setenv("XBOARD_TRUSTED_PROXY_CIDRS", "10.0.0.7/8, 2001:db8::/32,10.0.0.0/8")
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
	legacyAppTemplate := filepath.Join(t.TempDir(), "legacy-app-clash.yaml")
	t.Setenv("XBOARD_LEGACY_APP_CLASH_TEMPLATE_FILE", legacyAppTemplate)
	ip2RegionFile := filepath.Join(t.TempDir(), "ip2region.xdb")
	t.Setenv("XBOARD_IP2REGION_XDB_FILE", ip2RegionFile)

	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settings.Address != "127.0.0.1:9090" || settings.DatabaseDSN != "file:test.db" || !settings.CookieSecure || settings.SchedulerInterval != 2*time.Second ||
		settings.LegacyAdminPath != "53815c85" || !settings.WebSocketEnabled || settings.WebSocketURL != "wss://panel.example.test/ws" || settings.NodePushInterval != 15 || settings.NodePullInterval != 30 || settings.WebRoot == "" ||
		settings.LegacyAppClashTemplateFile != legacyAppTemplate || settings.IP2RegionXDBFile != ip2RegionFile {
		t.Fatalf("unexpected settings: %#v", settings)
	}
	if len(settings.AllowedOrigins) != 2 || settings.AllowedOrigins[1] != "https://admin.example.test" {
		t.Fatalf("allowed origins = %#v", settings.AllowedOrigins)
	}
	if len(settings.TrustedProxyPrefixes) != 2 || settings.TrustedProxyPrefixes[0] != netip.MustParsePrefix("10.0.0.0/8") || settings.TrustedProxyPrefixes[1] != netip.MustParsePrefix("2001:db8::/32") {
		t.Fatalf("trusted proxy prefixes = %#v", settings.TrustedProxyPrefixes)
	}
}

func TestSECNODE005LoadRejectsInvalidTrustedProxyCIDRs(t *testing.T) {
	for _, value := range []string{
		"10.0.0.0/8,not-a-cidr",
		"10.0.0.0/8,",
		"::ffff:192.0.2.0/120",
		strings.Repeat("1", 4<<10+1),
		strings.Repeat("10.0.0.0/8,", 128) + "192.0.2.0/24",
	} {
		t.Setenv("XBOARD_TRUSTED_PROXY_CIDRS", value)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted invalid trusted proxy CIDRs with length %d", len(value))
		}
	}
}

func TestLoadRejectsRelativeIP2RegionFile(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("XBOARD_IP2REGION_XDB_FILE", "data/ip2region.xdb")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a relative Ip2Region XDB file")
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

func TestLoadRejectsRelativeLegacyAppClashTemplate(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("XBOARD_LEGACY_APP_CLASH_TEMPLATE_FILE", "config/app.clash.yaml")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a relative legacy app Clash template")
	}
}

func TestLoadValidatesPrivateAttachmentStorageAndLimits(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE", "")
	t.Setenv("XBOARD_ATTACHMENT_ROOT", filepath.Join(t.TempDir(), "attachments"))
	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AttachmentChunkSize != 5<<20 || settings.AttachmentMaxFileSize != 1<<30 || settings.AttachmentTotalQuota != 20<<30 ||
		settings.AttachmentSignedURLTTL != 2*time.Hour || settings.AttachmentDraftTTL != 24*time.Hour ||
		settings.AttachmentTrashTTL != 7*24*time.Hour || settings.AttachmentMaxPerArticle != 100 {
		t.Fatalf("attachment defaults=%#v", settings)
	}
	t.Setenv("XBOARD_ATTACHMENT_ROOT", "relative/attachments")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a relative attachment root")
	}
	t.Setenv("XBOARD_ATTACHMENT_ROOT", filepath.Join(t.TempDir(), "attachments"))
	t.Setenv("XBOARD_ATTACHMENT_TOTAL_QUOTA", "1024")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a quota below the maximum file size")
	}
}

func TestLoadValidatesPrivateAdministratorExportStorage(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	exportRoot := filepath.Join(t.TempDir(), "admin-exports")
	t.Setenv("XBOARD_ADMIN_EXPORT_ROOT", exportRoot)
	t.Setenv("XBOARD_BULK_POLL_INTERVAL", "750ms")
	settings, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminExportRoot != exportRoot || settings.BulkPollInterval != 750*time.Millisecond {
		t.Fatalf("administrator export settings=%#v", settings)
	}
	t.Setenv("XBOARD_ADMIN_EXPORT_ROOT", "relative/exports")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a relative administrator export root")
	}
	t.Setenv("XBOARD_ADMIN_EXPORT_ROOT", exportRoot)
	t.Setenv("XBOARD_BULK_POLL_INTERVAL", "10ms")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an unsafe bulk poll interval")
	}
	t.Setenv("XBOARD_BULK_POLL_INTERVAL", "750ms")
	webRoot := filepath.Join(t.TempDir(), "web")
	t.Setenv("XBOARD_WEB_ROOT", webRoot)
	t.Setenv("XBOARD_ADMIN_EXPORT_ROOT", filepath.Join(webRoot, "private-exports"))
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted an administrator export directory below the public web root")
	}
	t.Setenv("XBOARD_WEB_ROOT", "")
	attachmentRoot := filepath.Join(t.TempDir(), "attachments")
	t.Setenv("XBOARD_ATTACHMENT_ROOT", attachmentRoot)
	t.Setenv("XBOARD_ADMIN_EXPORT_ROOT", filepath.Join(attachmentRoot, "exports"))
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted overlapping attachment and administrator export roots")
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

func TestLoadValidatesNodeCoordinationConfiguration(t *testing.T) {
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("XBOARD_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("XBOARD_NODE_COORDINATION_MODE", "")
	t.Setenv("XBOARD_REDIS_URL", "")
	t.Setenv("XBOARD_REDIS_URL_FILE", "")
	t.Setenv("XBOARD_REDIS_KEY_PREFIX", "")
	settings, err := Load()
	if err != nil {
		t.Fatalf("Load() defaults error = %v", err)
	}
	if settings.NodeCoordinationMode != "local" || settings.RedisURL != "" || settings.RedisKeyPrefix != "xboard-go:" {
		t.Fatalf("node coordination defaults = %#v", settings)
	}

	secretPath := filepath.Join(t.TempDir(), "redis-url")
	if err := os.WriteFile(secretPath, []byte("rediss://user:secret@redis.example.test:6380/4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XBOARD_NODE_COORDINATION_MODE", "redis")
	t.Setenv("XBOARD_REDIS_URL_FILE", secretPath)
	t.Setenv("XBOARD_REDIS_KEY_PREFIX", "tenant_7:")
	settings, err = Load()
	if err != nil {
		t.Fatalf("Load() Redis mode error = %v", err)
	}
	if settings.NodeCoordinationMode != "redis" || settings.RedisURL != "rediss://user:secret@redis.example.test:6380/4" || settings.RedisKeyPrefix != "tenant_7:" {
		t.Fatalf("node coordination settings = %#v", settings)
	}

	for _, test := range []struct {
		name, mode, redisURL, prefix string
	}{
		{name: "unknown mode", mode: "fallback", redisURL: "", prefix: "xboard-go:"},
		{name: "missing URL", mode: "redis", redisURL: "", prefix: "xboard-go:"},
		{name: "wrong scheme", mode: "redis", redisURL: "https://redis.example.test", prefix: "xboard-go:"},
		{name: "fragment", mode: "redis", redisURL: "redis://redis.example.test/0#secret", prefix: "xboard-go:"},
		{name: "unsafe prefix", mode: "redis", redisURL: "redis://redis.example.test/0", prefix: "bad prefix"},
		{name: "misleading local URL", mode: "local", redisURL: "redis://redis.example.test/0", prefix: "xboard-go:"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XBOARD_NODE_COORDINATION_MODE", test.mode)
			t.Setenv("XBOARD_REDIS_URL", test.redisURL)
			t.Setenv("XBOARD_REDIS_URL_FILE", "")
			t.Setenv("XBOARD_REDIS_KEY_PREFIX", test.prefix)
			if _, err := Load(); err == nil {
				t.Fatal("Load() accepted invalid node coordination configuration")
			}
		})
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
