import { defineConfig, devices } from "@playwright/test";

const browserChannel = process.env.XBOARD_E2E_BROWSER_CHANNEL?.trim();

export default defineConfig({
  testDir: "./parity",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [["list"], ["html", { outputFolder: "playwright-report/parity", open: "never" }]],
  use: {
    ...devices["Desktop Chrome"],
    ...(browserChannel ? { channel: browserChannel } : {}),
    locale: "zh-CN",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure"
  }
});
