import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, adminSecurePath, adminEntryPath, expectLoginPage } from "./support";

test("administrator path rotation isolates the UI and V1 admin API", async ({ page }) => {
  test.setTimeout(90_000);
  const rotatedPath = `e2e-rotated-${Date.now()}`;

  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(adminEmail);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "管理端导航" })).toBeVisible();

  const original = await readSiteSettings(page, adminSecurePath);
  try {
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await page.getByLabel("管理员安全路径").fill(rotatedPath);
    await page.getByRole("button", { name: "保存站点设置", exact: true }).click();
    await page.waitForURL((url) => url.pathname === `/${rotatedPath}/` && url.hash === "#/", { timeout: 15_000 });
    await expect(page.getByRole("navigation", { name: "管理端导航" })).toBeVisible();
    expect((await readSiteSettings(page, rotatedPath)).secure_path).toBe(rotatedPath);
    expect((await page.request.get(adminAPIPath(adminSecurePath, "/site-settings"))).status()).toBe(404);
    expect((await page.request.get("/api/v1/admin/site-settings")).status()).toBe(404);
    expect((await page.request.get(adminAPIPath("wrong-admin-path", "/site-settings"))).status()).toBe(404);

    await page.goto("/");
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "管理端导航" })).toHaveCount(0);
  } finally {
    const current = await tryReadSiteSettings(page, rotatedPath) ?? await tryReadSiteSettings(page, adminSecurePath);
    if (current !== null && current.secure_path !== original.secure_path) {
      const csrf = decodeURIComponent((await page.context().cookies()).find((cookie) => cookie.name === "xboard_csrf")?.value ?? "");
      const restored = await page.request.put(adminAPIPath(current.secure_path, "/site-settings"), {
        headers: { "X-CSRF-Token": csrf },
        data: writableSiteSettings(current, original.secure_path)
      });
      expect(restored.status(), await restored.text()).toBe(200);
    }
  }
});

function adminAPIPath(path: string, suffix: string): string {
  return `/api/v1/admin/${path}${suffix}`;
}

async function tryReadSiteSettings(page: Page, path: string): Promise<Record<string, unknown> & { revision: number; secure_path: string } | null> {
  const response = await page.request.get(adminAPIPath(path, "/site-settings"));
  if (response.status() === 404) return null;
  const body = await response.text();
  expect(response.status(), body).toBe(200);
  const payload = JSON.parse(body) as { data?: Record<string, unknown> & { revision: number; secure_path: string } };
  if (payload.data === undefined) throw new Error("site settings response is missing data");
  return payload.data;
}

async function readSiteSettings(page: Page, path: string) {
  const settings = await tryReadSiteSettings(page, path);
  if (settings === null) throw new Error(`administrator path ${path} is not active`);
  return settings;
}

function writableSiteSettings(settings: Record<string, unknown>, securePath: string): Record<string, unknown> {
  const input: Record<string, unknown> = { ...settings, secure_path: securePath };
  delete input.updated_at;
  delete input.recaptcha_secret_configured;
  delete input.recaptcha_v3_secret_configured;
  delete input.turnstile_secret_configured;
  return input;
}
