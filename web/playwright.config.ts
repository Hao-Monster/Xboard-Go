import { defineConfig, devices } from "@playwright/test";

const externalServer = process.env.XBOARD_E2E_EXTERNAL_SERVER === "true";
const baseURL = process.env.XBOARD_E2E_BASE_URL ?? "http://127.0.0.1:4173";

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
      use: { ...devices["Desktop Chrome"] }
    },
    {
      name: "mobile-chromium",
      use: { ...devices["Pixel 7"] }
    }
  ],
  webServer: externalServer ? undefined : [
    {
      command: "go run ./cmd/xboard",
      cwd: "..",
      url: "http://127.0.0.1:8080/healthz",
      timeout: 120_000,
      reuseExistingServer: false,
      env: {
        XBOARD_ADDRESS: "127.0.0.1:8080",
        XBOARD_DATABASE_DSN: "file:xboard-e2e?mode=memory&cache=shared",
        XBOARD_PANEL_URL: "http://127.0.0.1:4173",
        XBOARD_ALLOWED_ORIGINS: "http://127.0.0.1:4173",
        XBOARD_COOKIE_SECURE: "false",
        XBOARD_BOOTSTRAP_ADMIN_EMAIL: "admin@e2e.test",
        XBOARD_BOOTSTRAP_ADMIN_PASSWORD: "e2e-admin-password-123",
        XBOARD_SETTINGS_ENCRYPTION_KEY: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
      }
    },
    {
      command: "pnpm preview",
      cwd: ".",
      url: "http://127.0.0.1:4173",
      timeout: 120_000,
      reuseExistingServer: false
    }
  ]
});
