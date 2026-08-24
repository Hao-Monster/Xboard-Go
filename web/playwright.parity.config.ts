import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./parity",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: [["list"], ["html", { outputFolder: "playwright-report/parity", open: "never" }]],
  use: {
    ...devices["Desktop Chrome"],
    locale: "zh-CN",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure"
  }
});
