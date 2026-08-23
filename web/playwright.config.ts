import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "http://127.0.0.1:4173",
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
  webServer: [
    {
      command: "go run ./cmd/xboard",
      cwd: "..",
      url: "http://127.0.0.1:8080/healthz",
      timeout: 120_000,
      reuseExistingServer: false,
      env: {
        XBOARD_ADDRESS: "127.0.0.1:8080",
        XBOARD_DATABASE_DSN: "file:./tmp/e2e.db",
        XBOARD_PANEL_URL: "http://127.0.0.1:4173",
        XBOARD_ALLOWED_ORIGINS: "http://127.0.0.1:4173",
        XBOARD_COOKIE_SECURE: "false",
        XBOARD_BOOTSTRAP_ADMIN_EMAIL: "admin@e2e.test",
        XBOARD_BOOTSTRAP_ADMIN_PASSWORD: "e2e-admin-password-123"
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
