import { defineConfig, devices } from "@playwright/test";
import { tmpdir } from "node:os";
import { join } from "node:path";

const externalServer = process.env.XBOARD_E2E_EXTERNAL_SERVER === "true";
const baseURL = process.env.XBOARD_E2E_BASE_URL ?? "http://127.0.0.1:4173";
const backendPort = process.env.XBOARD_E2E_BACKEND_PORT ?? "8080";
const backendURL = `http://127.0.0.1:${backendPort}`;
const attachmentRoot = join(tmpdir(), `xboard-go-e2e-attachments-${process.pid}`);
const browserChannel = process.env.XBOARD_E2E_BROWSER_CHANNEL?.trim();
const adminSecurePath = process.env.XBOARD_E2E_ADMIN_PATH?.trim() || "e2e-admin-secure";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure"
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], ...(browserChannel ? { channel: browserChannel } : {}) }
    },
    {
      name: "mobile-chromium",
      use: { ...devices["Pixel 7"], ...(browserChannel ? { channel: browserChannel } : {}) }
    }
  ],
  webServer: externalServer ? undefined : [
    {
      command: "node e2e/captcha-stub.mjs",
      cwd: ".",
      url: "http://127.0.0.1:4199/healthz",
      timeout: 30_000,
      reuseExistingServer: false
    },
    {
      command: "go run ./cmd/xboard",
      cwd: "..",
      url: `${backendURL}/healthz`,
      timeout: 120_000,
      reuseExistingServer: false,
      env: {
        XBOARD_ADDRESS: `127.0.0.1:${backendPort}`,
        XBOARD_DATABASE_DSN: "file:xboard-e2e?mode=memory&cache=shared",
        XBOARD_PANEL_URL: baseURL,
        XBOARD_ALLOWED_ORIGINS: baseURL,
        XBOARD_COOKIE_SECURE: "false",
        XBOARD_LEGACY_ADMIN_PATH: adminSecurePath,
        XBOARD_BOOTSTRAP_ADMIN_EMAIL: "admin@e2e.test",
        XBOARD_BOOTSTRAP_ADMIN_PASSWORD: "e2e-admin-password-123",
        XBOARD_SETTINGS_ENCRYPTION_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
        XBOARD_ATTACHMENT_ROOT: attachmentRoot,
        XBOARD_CAPTCHA_ALLOW_INSECURE: "true",
        XBOARD_CAPTCHA_RECAPTCHA_VERIFY_URL: "http://127.0.0.1:4199/recaptcha",
        XBOARD_CAPTCHA_RECAPTCHA_V3_VERIFY_URL: "http://127.0.0.1:4199/recaptcha-v3",
        XBOARD_CAPTCHA_TURNSTILE_VERIFY_URL: "http://127.0.0.1:4199/turnstile"
      }
    },
    {
      command: "pnpm preview",
      cwd: ".",
      url: "http://127.0.0.1:4173",
      timeout: 120_000,
      reuseExistingServer: false,
      env: { XBOARD_API_PROXY_TARGET: backendURL }
    }
  ]
});
