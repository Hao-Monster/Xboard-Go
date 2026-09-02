import { expect, test, type Page, type Response } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

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
}

test("public registration enforces legacy email policies and successful-IP quota", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const password = `policy-password-${unique}`;
  let original: SiteSettings | null = null;

  try {
    await loginAdministrator(page);
    original = await getSiteSettings(page);

    // Disabling first guarantees that this browser scenario starts without a
    // successful-IP counter left by an earlier local test run.
    let current = await saveSiteSettings(page, {
      ...original,
      stop_register: false,
      email_verify: false,
      email_whitelist_enable: true,
      email_whitelist_suffix: ["allowed.test", "gmail.com"],
      email_gmail_limit_enable: true,
      register_limit_by_ip_enable: false,
      register_limit_count: 2,
      register_limit_expire: 1
    });
    current = await saveSiteSettings(page, { ...current, register_limit_by_ip_enable: true });
    expect(current.register_limit_by_ip_enable).toBe(true);
    await logoutAndWait(page);

    await openRegistration(page);
    await expect(page.getByText("允许邮箱后缀：allowed.test、gmail.com", { exact: true })).toBeVisible();
    let response = await submitRegistration(page, `blocked-${unique}@example.test`, password);
    expect(response.status()).toBe(400);
    await expect(page.getByRole("alert")).toHaveText("邮箱后缀不处于白名单中");

    response = await submitRegistration(page, `first.last+${unique}@gmail.com`, password);
    expect(response.status()).toBe(400);
    await expect(page.getByRole("alert")).toHaveText("不支持 Gmail 别名邮箱");

    response = await submitRegistration(page, `first.last+${unique}@allowed.test`, password);
    expect(response.status()).toBe(200);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await logoutAndWait(page);

    await openRegistration(page);
    response = await submitRegistration(page, `second-${unique}@allowed.test`, password);
    expect(response.status()).toBe(200);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await logoutAndWait(page);

    await openRegistration(page);
    response = await submitRegistration(page, `third-${unique}@allowed.test`, password);
    expect(response.status()).toBe(429);
    expect(Number(response.headers()["retry-after"])).toBeGreaterThan(0);
    await expect(page.getByRole("alert")).toHaveText("注册频繁，请等待 1 分钟后再次尝试");
  } finally {
    if (original !== null) {
      await ensureAdministrator(page);
      const current = await getSiteSettings(page);
      const cleared = await saveSiteSettings(page, { ...current, register_limit_by_ip_enable: false });
      await saveSiteSettings(page, { ...original, revision: cleared.revision });
    }
  }

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function submitRegistration(page: Page, email: string, password: string): Promise<Response> {
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByLabel("再次输入密码").fill(password);
  const response = page.waitForResponse((candidate) => candidate.url().endsWith("/api/v1/auth/register"));
  await page.getByRole("button", { name: "注册", exact: true }).click();
  return response;
}

async function openRegistration(page: Page) {
  await page.goto("/#/register");
  await page.reload();
  await expect(page.getByRole("heading", { name: /注册 / })).toBeVisible();
}

async function logoutAndWait(page: Page) {
  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: /登录 / })).toBeVisible();
}

async function loginAdministrator(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出" }).count() > 0) {
    await logoutAndWait(page);
  }
  await loginAdministrator(page);
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(response.status, response.body).toBe(200);
  return decodeSiteSettings(response.body);
}

async function saveSiteSettings(page: Page, settings: SiteSettings): Promise<SiteSettings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
    revision: settings.revision,
    app_name: settings.app_name,
    app_description: settings.app_description,
    app_url: settings.app_url,
    tos_url: settings.tos_url,
    logo: settings.logo,
    stop_register: settings.stop_register,
    email_verify: settings.email_verify,
    email_whitelist_enable: settings.email_whitelist_enable,
    email_whitelist_suffix: settings.email_whitelist_suffix,
    email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable,
    register_limit_count: settings.register_limit_count,
    register_limit_expire: settings.register_limit_expire,
    password_limit_enable: settings.password_limit_enable,
    password_limit_count: settings.password_limit_count,
    password_limit_expire: settings.password_limit_expire
  });
  expect(response.status, response.body).toBe(200);
  return decodeSiteSettings(response.body);
}

function decodeSiteSettings(body: string): SiteSettings {
  const payload: unknown = JSON.parse(body);
  if (typeof payload !== "object" || payload === null) throw new Error("site settings envelope is invalid");
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) throw new Error("site settings data is invalid");
  return data as SiteSettings;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: adminAPIPath(path), method, body });
}
