import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, adminSecurePath, expectLoginPage } from "./support";

type SiteSettings = Record<string, unknown> & { revision: number; secure_path: string };

test("administrator path rotation moves the UI and invalidates old and fixed V1 routes", { tag: "@fresh-server" }, async ({ page }) => {
  test.setTimeout(90_000);
  const rotatedPath = `e2e-rotated-${Date.now()}`;

  await page.goto(`/${adminSecurePath}/`);
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

    await page.goto(`/${rotatedPath}/`);
    await expect(page.getByRole("navigation", { name: "管理端导航" })).toBeVisible();
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

function adminAPIPath(securePath: string, suffix: string): string {
  return `/api/v1/admin/${securePath}${suffix}`;
}

function writableSiteSettings(settings: SiteSettings, securePath: string): Record<string, unknown> {
  const input: Record<string, unknown> = { ...settings, secure_path: securePath };
  delete input.updated_at;
  delete input.recaptcha_secret_configured;
  delete input.recaptcha_v3_secret_configured;
  delete input.turnstile_secret_configured;
  return input;
}

async function tryReadSiteSettings(page: Page, securePath: string): Promise<SiteSettings | null> {
  const response = await page.request.get(adminAPIPath(securePath, "/site-settings"));
  if (response.status() === 404) return null;
  const body = await response.text();
  expect(response.status(), body).toBe(200);
  const payload = JSON.parse(body) as { data?: SiteSettings };
  if (payload.data === undefined) throw new Error("site settings response is missing data");
  return payload.data;
}

async function readSiteSettings(page: Page, securePath: string): Promise<SiteSettings> {
  const settings = await tryReadSiteSettings(page, securePath);
  if (settings === null) throw new Error(`administrator path ${securePath} is not active`);
  return settings;
}
