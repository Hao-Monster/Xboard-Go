import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, expectAuthPage, logoutAndWait } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

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

interface TicketSettings {
  revision: number;
  app_name: string;
  app_url: string;
  ticket_must_wait_reply: boolean;
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_encryption: "starttls" | "tls" | "none";
  smtp_from_address: string;
}

test("visitor registers with the legacy one-time email code through Mailpit", async ({ page, request }, testInfo) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(90_000);
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `registration-email-${unique}@example.test`;
  const password = `registration-email-password-${unique}`;
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  let originalSite: SiteSettings | null = null;
  let originalTicket: TicketSettings | null = null;

  try {
    await loginAdministrator(page);
    originalSite = await getSiteSettings(page);
    originalTicket = await getTicketSettings(page);
    await saveTicketSettings(page, {
      ...originalTicket,
      app_name: "Xboard-Go",
      app_url: new URL(page.url()).origin,
      smtp_enabled: true,
      smtp_host: "mailpit",
      smtp_port: 1025,
      smtp_username: "",
      smtp_encryption: "none",
      smtp_from_address: "support@xboard-go.local"
    });

    await page.getByRole("button", { name: "系统设置" }).click();
    await expect(page.getByRole("heading", { name: "系统设置" })).toBeVisible();
    const emailVerification = page.getByRole("checkbox", { name: "邮箱验证" });
    if (!(await emailVerification.isChecked())) await emailVerification.click();
    const saveResponse = page.waitForResponse((response) => response.url().endsWith(adminAPIPath("/api/v1/admin/site-settings")) && response.request().method() === "PUT");
    await page.getByRole("button", { name: "保存站点设置" }).click();
    expect((await saveResponse).status()).toBe(200);
    await expect(page.getByRole("status")).toHaveText("站点设置已保存");
    await logoutAndWait(page);

    await page.goto("/#/register");
    await expectAuthPage(page, "注册");
    await expect(page.getByLabel("邮箱验证码", { exact: true })).toBeVisible();
    await page.getByLabel("邮箱", { exact: true }).fill(email);
    const sendResponse = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/registration-email/request"));
    await page.getByRole("button", { name: "发送", exact: true }).click();
    expect((await sendResponse).status()).toBe(202);
    await expect(page.getByRole("status")).toHaveText("验证码已发送，请检查邮箱");
    await expect(page.getByRole("button", { name: /\d+ 秒/ })).toBeDisabled();

    const code = await waitForEmailCode(request, email, "Xboard-Go邮箱验证码");
    await page.getByLabel("邮箱验证码", { exact: true }).fill(code);
    await page.getByLabel("密码", { exact: true }).fill(password);
    await page.getByLabel("再次输入密码").fill(password);
    const registration = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/register"));
    await page.getByRole("button", { name: "注册", exact: true }).click();
    expect((await registration).status()).toBe(200);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();

    await logoutAndWait(page);
    await page.goto("/#/register");
    await page.getByLabel("邮箱", { exact: true }).fill(email);
    await page.getByLabel("邮箱验证码", { exact: true }).fill(code);
    await page.getByLabel("密码", { exact: true }).fill(password);
    await page.getByLabel("再次输入密码").fill(password);
    const reused = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/register"));
    await page.getByRole("button", { name: "注册", exact: true }).click();
    expect((await reused).status()).toBe(400);
    await expect(page.getByRole("alert")).toHaveText("邮箱验证码有误");

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    if (originalSite !== null && originalTicket !== null) {
      await ensureAdministrator(page);
      const currentSite = await getSiteSettings(page);
      await saveSiteSettings(page, { ...originalSite, revision: currentSite.revision });
      const currentTicket = await getTicketSettings(page);
      await saveTicketSettings(page, { ...originalTicket, revision: currentTicket.revision });
    }
  }
});

async function waitForEmailCode(request: APIRequestContext, recipient: string, subject: string): Promise<string> {
  let code = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages`);
    if (!response.ok()) return 0;
    const payload = await response.json() as { messages?: Array<{ ID?: string; Subject?: string; To?: Array<{ Address?: string }> }> };
    const message = payload.messages?.find((item) => item.Subject === subject && item.To?.some((address) => address.Address === recipient));
    if (message?.ID === undefined) return 0;
    const detail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(message.ID)}`);
    if (!detail.ok()) return 0;
    code = JSON.stringify(await detail.json()).match(/\b(\d{6})\b/)?.[1] ?? "";
    return code === "" ? 0 : 1;
  }, { timeout: 15_000, intervals: [250, 500, 1_000] }).toBe(1);
  return code;
}

async function loginAdministrator(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出" }).count() > 0) await logoutAndWait(page);
  await loginAdministrator(page);
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "GET"));
}

async function saveSiteSettings(page: Page, settings: SiteSettings): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
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
  }));
}

async function getTicketSettings(page: Page): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "GET"));
}

async function saveTicketSettings(page: Page, settings: TicketSettings): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "PUT", {
    revision: settings.revision,
    app_name: settings.app_name,
    app_url: settings.app_url,
    ticket_must_wait_reply: settings.ticket_must_wait_reply,
    smtp_enabled: settings.smtp_enabled,
    smtp_host: settings.smtp_host,
    smtp_port: settings.smtp_port,
    smtp_username: settings.smtp_username,
    smtp_encryption: settings.smtp_encryption,
    smtp_from_address: settings.smtp_from_address
  }));
}

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
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
