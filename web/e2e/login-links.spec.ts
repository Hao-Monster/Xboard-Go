import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminEmail, adminPassword, logoutAndWait } from "./support";

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
  invite_force: boolean;
  invite_gen_limit: number;
  invite_never_expire: boolean;
  login_with_mail_link_enable: boolean;
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

test("quick and mail login links are safe, one-time, and land on the legacy destination", async ({ page, request }, testInfo) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(120_000);
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `login-link-${unique}@example.test`;
  const unknownEmail = `unknown-login-link-${unique}@example.test`;
  const password = `login-link-password-${unique}`;
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  let originalSite: SiteSettings | null = null;
  let originalTicket: TicketSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword, true);
    originalSite = await getSiteSettings(page);
    originalTicket = await getTicketSettings(page);
    const configuredTicket = await saveTicketSettings(page, {
      ...originalTicket, app_name: "Xboard-Go", app_url: page.url().split("/#")[0].replace(/\/$/, ""),
      smtp_enabled: true, smtp_host: "mailpit", smtp_port: 1025, smtp_username: "",
      smtp_encryption: "none", smtp_from_address: "support@xboard-go.local"
    });
    const currentSite = await getSiteSettings(page);
    await saveSiteSettings(page, { ...currentSite, login_with_mail_link_enable: true });
    expect(configuredTicket.smtp_enabled).toBe(true);

    const created = await adminRequest(page, "/api/v1/admin/users", "POST", {
      email, password, group_id: null, transfer_enable: 1_073_741_824, expired_at: null,
      speed_limit: 0, device_limit: 0, banned: false
    });
    expect(created.status, created.body).toBe(201);
    await logoutAndWait(page);

    await login(page, email, password, false);
    const quick = await authenticatedPost(page, "/api/v1/auth/quick-link", { redirect: "invite" });
    expect(quick.status, quick.body).toBe(200);
    const quickURL = (JSON.parse(quick.body) as { data: { url: string } }).data.url;
    expect(new URL(quickURL).hash).toMatch(/^#\/login\?verify=[0-9a-f]{32}&redirect=invite$/);
    await logoutAndWait(page);

    await page.goto(quickURL);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await expect(page.getByRole("button", { name: "我的邀请" })).toHaveAttribute("aria-current", "page");
    await logoutAndWait(page);
    await page.goto(quickURL);
    await expect(page.getByRole("alert")).toHaveText("登录链接无效或已过期");
    expect(page.url()).not.toContain("verify=");

    const mailRequest = await publicPost(page, "/api/v1/auth/mail-link/request", { email, redirect: "ticket" });
    expect({ status: mailRequest.status, retryAfter: mailRequest.retryAfter, body: JSON.parse(mailRequest.body) }).toEqual({
      status: 202, retryAfter: null, body: { status: "success", data: true }
    });
    const cooledKnown = await publicPost(page, "/api/v1/auth/mail-link/request", { email });
    expect(cooledKnown.status).toBe(429);
    expect(cooledKnown.retryAfter).toMatch(/^[1-9]\d?$/);
    const mailURL = await waitForLoginURL(request, email, "登录到Xboard-Go");
    expect(new URL(mailURL).hash).toMatch(/^#\/login\?verify=[0-9a-f]{32}&redirect=ticket$/);
    await page.goto(mailURL);
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
    await expect(page.getByRole("button", { name: "我的工单" })).toHaveAttribute("aria-current", "page");

    const unsafe = await authenticatedPost(page, "/api/v1/user/getQuickLoginUrl", { redirect: "https://attacker.example.test/steal" });
    expect(unsafe.status, unsafe.body).toBe(200);
    const unsafeURL = (JSON.parse(unsafe.body) as { data: string }).data;
    expect(new URL(unsafeURL).hash).toMatch(/^#\/login\?verify=[0-9a-f]{32}&redirect=dashboard$/);
    await logoutAndWait(page);
    await page.goto(unsafeURL);
    await expect(page.getByRole("button", { name: "公告" })).toHaveAttribute("aria-current", "page");
    expect(page.url()).not.toContain("attacker.example.test");
    await logoutAndWait(page);

    const firstUnknown = await publicPost(page, "/api/v1/auth/mail-link/request", { email: unknownEmail });
    const cooledUnknown = await publicPost(page, "/api/v1/auth/mail-link/request", { email: unknownEmail });
    expect({ status: firstUnknown.status, retryAfter: firstUnknown.retryAfter, body: JSON.parse(mailRequest.body) }).toEqual({
      status: 202, retryAfter: null, body: JSON.parse(mailRequest.body)
    });
    expect(cooledUnknown.status).toBe(cooledKnown.status);
    expect(JSON.parse(cooledUnknown.body)).toEqual(JSON.parse(cooledKnown.body));
    expect(cooledUnknown.retryAfter).toBe("60");

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    if (originalSite !== null && originalTicket !== null) {
      await ensureAdministrator(page);
      const latestSite = await getSiteSettings(page);
      await saveSiteSettings(page, { ...originalSite, revision: latestSite.revision });
      const latestTicket = await getTicketSettings(page);
      await saveTicketSettings(page, { ...originalTicket, revision: latestTicket.revision });
    }
  }
});

async function waitForLoginURL(request: APIRequestContext, recipient: string, subject: string): Promise<string> {
  let loginURL = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages`);
    if (!response.ok()) return 0;
    const payload = await response.json() as { messages?: Array<{ ID?: string; Subject?: string; To?: Array<{ Address?: string }> }> };
    const message = payload.messages?.find((item) => item.Subject === subject && item.To?.some((address) => address.Address === recipient));
    if (message?.ID === undefined) return 0;
    const detail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(message.ID)}`);
    if (!detail.ok()) return 0;
    const match = JSON.stringify(await detail.json()).match(/https?:\/\/[^\s"<>]+\/#\/login\?verify=[0-9a-f]{32}&redirect=[a-z]+/);
    loginURL = match?.[0] ?? "";
    return loginURL === "" ? 0 : 1;
  }, { timeout: 15_000, intervals: [250, 500, 1_000] }).toBe(1);
  return loginURL;
}

async function login(page: Page, email: string, password: string, administrator: boolean) {
  await page.goto("/#/login");
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  if (administrator) await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  else await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
}

async function ensureAdministrator(page: Page) {
  const logout = page.getByRole("button", { name: "退出" });
  if (await logout.count() > 0) await logoutAndWait(page);
  else await page.goto("/#/login");
  await login(page, adminEmail, adminPassword, true);
}

async function publicPost(page: Page, path: string, body: unknown) {
  return page.evaluate(async ({ requestPath, requestBody }) => {
    const response = await fetch(requestPath, {
      method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(requestBody)
    });
    return { status: response.status, retryAfter: response.headers.get("Retry-After"), body: await response.text() };
  }, { requestPath: path, requestBody: body });
}

async function authenticatedPost(page: Page, path: string, body: unknown) {
  return adminRequest(page, path, "POST", body);
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "GET"));
}

async function saveSiteSettings(page: Page, settings: SiteSettings): Promise<SiteSettings> {
  return decodeData<SiteSettings>(await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
    revision: settings.revision, app_name: settings.app_name, app_description: settings.app_description,
    app_url: settings.app_url, tos_url: settings.tos_url, logo: settings.logo, stop_register: settings.stop_register,
    email_verify: settings.email_verify, email_whitelist_enable: settings.email_whitelist_enable,
    email_whitelist_suffix: settings.email_whitelist_suffix, email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable, register_limit_count: settings.register_limit_count,
    register_limit_expire: settings.register_limit_expire, invite_force: settings.invite_force,
    invite_gen_limit: settings.invite_gen_limit, invite_never_expire: settings.invite_never_expire,
    login_with_mail_link_enable: settings.login_with_mail_link_enable
  }));
}

async function getTicketSettings(page: Page): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "GET"));
}

async function saveTicketSettings(page: Page, settings: TicketSettings): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "PUT", {
    revision: settings.revision, app_name: settings.app_name, app_url: settings.app_url,
    ticket_must_wait_reply: settings.ticket_must_wait_reply, smtp_enabled: settings.smtp_enabled,
    smtp_host: settings.smtp_host, smtp_port: settings.smtp_port, smtp_username: settings.smtp_username,
    smtp_encryption: settings.smtp_encryption, smtp_from_address: settings.smtp_from_address
  }));
}

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestMethod: method, requestBody: body });
}
