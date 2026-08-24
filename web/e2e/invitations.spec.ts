import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

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
  invite_force: boolean;
  invite_gen_limit: number;
  invite_never_expire: boolean;
  login_with_mail_link_enable: boolean;
}

test("invitation registration matches forced, single-use, reusable, and referral behavior", async ({ page, request }, testInfo) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const password = `invite-password-${unique}`;
  const emails = {
    owner: `io-${unique}@example.test`, linked: `il-${unique}@example.test`, reused: `ir-${unique}@example.test`,
    reusableOne: `ir1-${unique}@example.test`, reusableTwo: `ir2-${unique}@example.test`
  };
  let original: SiteSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword, true);
    original = await getSiteSettings(page);
    let current = await saveSiteSettings(page, {
      ...original, stop_register: false, email_verify: false, email_whitelist_enable: false,
      email_gmail_limit_enable: false, register_limit_by_ip_enable: false,
      invite_force: false, invite_gen_limit: 1, invite_never_expire: false
    });
    await logout(page);

    await openRegistration(page);
    await expect(page.getByLabel("邀请码")).toHaveAttribute("placeholder", "邀请码,（选填）");
    await registerThroughUI(page, emails.owner, password);
    await page.getByRole("button", { name: "我的邀请" }).click();
    await expect(page.getByRole("heading", { name: "我的邀请" })).toBeVisible();
    await expect(page.getByText("暂无可用邀请码", { exact: true })).toBeVisible();
    const generatedResponse = page.waitForResponse((response) => response.url().endsWith("/api/v1/invitations") && response.request().method() === "POST");
    await page.getByRole("button", { name: "生成邀请码" }).click();
    expect((await generatedResponse).status()).toBe(200);
    const codeCell = page.locator("code.monospace");
    await expect(codeCell).toHaveText(/^[A-Za-z0-9]{8}$/);
    const singleUseCode = (await codeCell.textContent()) ?? "";
    expect(singleUseCode).toMatch(/^[A-Za-z0-9]{8}$/);
    await page.getByRole("button", { name: "生成邀请码" }).click();
    await expect(page.getByRole("alert")).toHaveText("已达到创建数量上限");

    const viewed = await request.post("/api/v1/invitations/view", { data: { invite_code: singleUseCode } });
    const unknown = await request.post("/api/v1/invitations/view", { data: { invite_code: "Badc1234" } });
    expect(viewed.status()).toBe(200);
    expect(await unknown.text()).toBe(await viewed.text());
    await page.reload();
    await page.getByRole("button", { name: "我的邀请" }).click();
    await expect(page.locator("tbody tr")).toContainText("1");
    await logout(page);

    await login(page, adminEmail, adminPassword, true);
    current = await getSiteSettings(page);
    await saveSiteSettings(page, { ...current, invite_force: true, invite_never_expire: false });
    await logout(page);
    await openRegistration(page);
    await expect(page.getByLabel("邀请码")).toHaveAttribute("placeholder", "邀请码,（必填）");
    await expect(page.getByLabel("邀请码")).toHaveAttribute("required", "");
    const missing = await request.post("/api/v1/auth/register", { data: {
      email: `missing-${unique}@example.test`, password, password_confirmation: password
    } });
    expect(missing.status()).toBe(422);
    expect((await missing.json() as { error: { message: string } }).error.message).toBe("必须使用邀请码才可以注册");
    const invalid = await request.post("/api/v1/auth/register", { data: {
      email: `invalid-${unique}@example.test`, password, password_confirmation: password, invite_code: "Badc1234"
    } });
    expect(invalid.status()).toBe(400);
    expect((await invalid.json() as { error: { message: string } }).error.message).toBe("邀请码无效");

    await page.goto(`/#/register?code=${singleUseCode}`);
    await expect(page.getByLabel("邀请码")).toHaveValue(singleUseCode);
    await expect(page.getByLabel("邀请码")).toBeDisabled();
    await registerThroughUI(page, emails.linked, password, true);
    await logout(page);
    const reuse = await request.post("/api/v1/auth/register", { data: {
      email: emails.reused, password, password_confirmation: password, invite_code: singleUseCode
    } });
    expect(reuse.status()).toBe(400);
    expect((await reuse.json() as { error: { message: string } }).error.message).toBe("邀请码无效");

    await login(page, emails.owner, password, false);
    await page.getByRole("button", { name: "我的邀请" }).click();
    await expect(page.getByText("暂无可用邀请码", { exact: true })).toBeVisible();
    await expect(page.locator(".invitation-overview .overview-metric").first()).toContainText("1");
    await logout(page);

    await login(page, adminEmail, adminPassword, true);
    current = await getSiteSettings(page);
    await saveSiteSettings(page, { ...current, invite_force: false, invite_never_expire: false });
    await logout(page);
    await login(page, emails.owner, password, false);
    await page.getByRole("button", { name: "我的邀请" }).click();
    await page.getByRole("button", { name: "生成邀请码" }).click();
    await expect(page.locator("code.monospace")).toHaveText(/^[A-Za-z0-9]{8}$/);
    const reusableCode = (await page.locator("code.monospace").textContent()) ?? "";
    await logout(page);

    await login(page, adminEmail, adminPassword, true);
    current = await getSiteSettings(page);
    await saveSiteSettings(page, { ...current, invite_force: true, invite_never_expire: true });
    await logout(page);
    for (const email of [emails.reusableOne, emails.reusableTwo]) {
      const response = await request.post("/api/v1/auth/register", { data: {
        email, password, password_confirmation: password, invite_code: reusableCode
      } });
      expect(response.status(), await response.text()).toBe(200);
    }
    await login(page, emails.owner, password, false);
    await page.getByRole("button", { name: "我的邀请" }).click();
    await expect(page.locator("code.monospace")).toHaveText(reusableCode);
    await expect(page.locator(".invitation-overview .overview-metric").first()).toContainText("3");
  } finally {
    if (original !== null) {
      await ensureAdministrator(page);
      const current = await getSiteSettings(page);
      await saveSiteSettings(page, { ...original, revision: current.revision });
    }
  }
  expect(errors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function registerThroughUI(page: Page, email: string, password: string, alreadyOpen = false) {
  if (!alreadyOpen) await expect(page.getByRole("heading", { name: /注册 / })).toBeVisible();
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByLabel("再次输入密码").fill(password);
  const response = page.waitForResponse((candidate) => candidate.url().endsWith("/api/v1/auth/register"));
  await page.getByRole("button", { name: "注册", exact: true }).click();
  expect((await response).status()).toBe(200);
  await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
}

async function openRegistration(page: Page) {
  await page.goto("/#/register");
  await page.reload();
  await expect(page.getByRole("heading", { name: /注册 / })).toBeVisible();
}

async function login(page: Page, email: string, password: string, administrator: boolean) {
  await page.goto("/#/login");
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  if (administrator) await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  else await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
}

async function logout(page: Page) {
  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: /登录 / })).toBeVisible();
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出" }).count() > 0) await logout(page);
  await login(page, adminEmail, adminPassword, true);
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "GET"));
}

async function saveSiteSettings(page: Page, settings: SiteSettings): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
    revision: settings.revision, app_name: settings.app_name, app_description: settings.app_description,
    app_url: settings.app_url, tos_url: settings.tos_url, logo: settings.logo,
    stop_register: settings.stop_register, email_verify: settings.email_verify,
    email_whitelist_enable: settings.email_whitelist_enable, email_whitelist_suffix: settings.email_whitelist_suffix,
    email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable,
    register_limit_count: settings.register_limit_count, register_limit_expire: settings.register_limit_expire,
    invite_force: settings.invite_force, invite_gen_limit: settings.invite_gen_limit,
    invite_never_expire: settings.invite_never_expire,
    login_with_mail_link_enable: settings.login_with_mail_link_enable
  }));
}

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
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
