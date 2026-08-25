import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, logoutAndWait } from "./support";

interface SiteSettings {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  tos_url: string;
  logo: string;
  stop_register: boolean;
  email_verify: boolean;
  email_whitelist_enable: boolean;
  email_whitelist_suffix: string[];
  email_gmail_limit_enable: boolean;
  register_limit_by_ip_enable: boolean;
  register_limit_count: number;
  register_limit_expire: number;
  password_limit_enable: boolean;
  password_limit_count: number;
  password_limit_expire: number;
  invite_force: boolean;
  invite_gen_limit: number;
  invite_never_expire: boolean;
  login_with_mail_link_enable: boolean;
}

interface ManagedUser {
  id: number;
  revision: number;
  email: string;
  group_id: number | null;
  transfer_enable: number;
  expired_at: string | null;
  speed_limit: number;
  device_limit: number;
}

test("configurable password lockout persists successful attempts and resists identity bypasses", async ({ browser, page }, testInfo) => {
  test.setTimeout(90_000);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `login-limit-${suffix}@example.test`;
  const unknownEmail = `missing-login-limit-${suffix}@example.test`;
  const password = `login-limit-password-${suffix}`;
  const errors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  let original: SiteSettings | null = null;
  let created: ManagedUser | null = null;
  const cleanupContext = await browser.newContext();
  const cleanupPage = await cleanupContext.newPage();

  try {
    // Keep an authenticated administrator session outside the account under
    // test so cleanup remains possible after intentionally lowering the limit.
    await login(cleanupPage, adminEmail, adminPassword, true);
    await login(page, adminEmail, adminPassword, true);
    original = await getSiteSettings(cleanupPage);
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "系统设置" })).toBeVisible();
    await page.getByRole("checkbox", { name: "密码错误次数限制" }).check();
    await page.getByLabel("密码错误次数", { exact: true }).fill("2");
    await page.getByLabel("登录锁定时长（分钟）", { exact: true }).fill("1");
    await page.getByRole("button", { name: "保存站点设置" }).click();
    await expect(page.getByRole("status")).toHaveText("站点设置已保存");

    const createdResponse = await adminRequest(cleanupPage, "/api/v1/admin/users", "POST", {
      email, password, group_id: null, transfer_enable: 1_073_741_824, expired_at: null,
      speed_limit: 0, device_limit: 0, banned: false
    });
    expect(createdResponse.status, createdResponse.body).toBe(201);
    created = (JSON.parse(createdResponse.body) as { data: ManagedUser }).data;
    await logoutAndWait(page);

    await submitLogin(page, email, "wrong-password");
    await expect(page.getByRole("alert")).toHaveText("邮箱或密码错误");
    await submitLogin(page, email, password);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await logoutAndWait(page);

    await submitLogin(page, email, "still-wrong");
    await expect(page.getByRole("alert")).toHaveText("邮箱或密码错误");
    await submitLogin(page, email, password);
    const limitedMessage = "密码错误次数过多，请 1 分钟后再试";
    await expect(page.getByRole("alert")).toHaveText(limitedMessage);
    await submitLogin(page, `  ${email.toUpperCase()}  `, password);
    await expect(page.getByRole("alert")).toHaveText(limitedMessage);

    const unknown = await page.evaluate(async ({ first, second }) => {
      const attempt = async (address: string) => {
        const response = await fetch("/api/v1/auth/login", {
          method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email: address, password: "wrong-password" })
        });
        return {
          status: response.status,
          retryAfter: response.headers.get("Retry-After"),
          body: await response.json() as { error?: { code?: string; message?: string } }
        };
      };
      return [await attempt(first), await attempt(second.toUpperCase()), await attempt(` ${first} `)];
    }, { first: unknownEmail, second: unknownEmail });
    expect(unknown.slice(0, 2).map((item) => [item.status, item.body.error?.code, item.body.error?.message])).toEqual([
      [401, "invalid_credentials", "邮箱或密码错误"],
      [401, "invalid_credentials", "邮箱或密码错误"]
    ]);
    expect(unknown[2]).toMatchObject({
      status: 429,
      body: { error: { code: "login_rate_limited", message: limitedMessage } }
    });
    expect(unknown[2].retryAfter).toMatch(/^(?:[1-9]|[1-5]\d|60)$/);
  } finally {
    if (original !== null) {
      if (created !== null) {
        const banned = await adminRequest(cleanupPage, `/api/v1/admin/users/${created.id}`, "PATCH", {
          revision: created.revision, email: created.email, group_id: created.group_id,
          transfer_enable: created.transfer_enable, expired_at: created.expired_at,
          speed_limit: created.speed_limit, device_limit: created.device_limit, banned: true
        });
        expect(banned.status, banned.body).toBe(200);
      }
      const current = await getSiteSettings(cleanupPage);
      const restored = await adminRequest(cleanupPage, "/api/v1/admin/site-settings", "PUT", siteSettingsBody(original, current.revision));
      expect(restored.status, restored.body).toBe(200);
    }
    await cleanupContext.close();
  }
  expect(errors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string, administrator: boolean) {
  await page.goto("/");
  if (await page.getByRole("button", { name: "退出" }).count() > 0) {
    if (administrator && await page.getByRole("button", { name: "系统设置", exact: true }).count() > 0) return;
    await logoutAndWait(page);
  }
  await submitLogin(page, email, password);
  await expect(page.getByRole("heading", { name: administrator ? "服务器管理" : "用户中心" })).toBeVisible();
}

async function submitLogin(page: Page, email: string, password: string) {
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: SiteSettings }).data;
}

function siteSettingsBody(settings: SiteSettings, revision: number) {
  return {
    revision, app_name: settings.app_name, app_description: settings.app_description,
    app_url: settings.app_url, tos_url: settings.tos_url, logo: settings.logo,
    stop_register: settings.stop_register, email_verify: settings.email_verify,
    email_whitelist_enable: settings.email_whitelist_enable, email_whitelist_suffix: settings.email_whitelist_suffix,
    email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable,
    register_limit_count: settings.register_limit_count, register_limit_expire: settings.register_limit_expire,
    password_limit_enable: settings.password_limit_enable, password_limit_count: settings.password_limit_count,
    password_limit_expire: settings.password_limit_expire, invite_force: settings.invite_force,
    invite_gen_limit: settings.invite_gen_limit, invite_never_expire: settings.invite_never_expire,
    login_with_mail_link_enable: settings.login_with_mail_link_enable
  };
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path, method, body });
}
