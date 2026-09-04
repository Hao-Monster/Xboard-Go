import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, expectLoginPage  } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

interface MailSettings {
  revision: number;
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_password_set: boolean;
  smtp_encryption: "starttls" | "tls" | "none";
  smtp_from_address: string;
  remind_mail_enable: boolean;
}

test("administrator mail settings persist and the saved SMTP sends a real Mailpit test message", async ({ page, request }) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(90_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page);
  const original = await getMailSettings(page);
  test.skip(original.smtp_password_set, "requires an isolated test database without an unrecoverable existing SMTP secret");
  try {
    await page.getByRole("button", { name: "邮件设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "邮件设置" })).toBeVisible();
    await expect(page.getByLabel("SMTP 密码")).toHaveValue("");
    await page.getByRole("checkbox", { name: "启用 SMTP 邮件服务" }).check();
    await page.getByLabel("SMTP 主机").fill("mailpit");
    await page.getByLabel("SMTP 端口").fill("1025");
    await page.getByLabel("SMTP 用户名").fill("");
    await page.getByLabel("加密方式").selectOption("none");
    await page.getByLabel("发件人地址").fill("support@xboard-go.local");
    await page.getByRole("checkbox", { name: "启用订阅到期和流量提醒" }).check();
    await page.getByRole("button", { name: "保存邮件设置" }).click();
    await expect(page.getByRole("status")).toContainText("邮件设置已保存");

    await page.reload();
    await page.getByRole("button", { name: "邮件设置", exact: true }).click();
    await expect(page.getByLabel("SMTP 主机")).toHaveValue("mailpit");
    await expect(page.getByLabel("SMTP 端口")).toHaveValue("1025");
    await expect(page.getByLabel("加密方式")).toHaveValue("none");
    await expect(page.getByRole("checkbox", { name: "启用订阅到期和流量提醒" })).toBeChecked();

    await page.getByRole("button", { name: "发送测试邮件" }).click();
    await expect(page.getByRole("status")).toContainText(`测试邮件已发送至 ${adminEmail}`);
    const messageID = await waitForCapturedMail(request, adminEmail, "This is xboard test email");
    const detail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(messageID)}`);
    expect(detail.ok()).toBeTruthy();
    const captured = JSON.stringify(await detail.json());
    expect(captured).toContain(adminEmail);
    expect(captured).toContain("Site: Xboard-Go");
    expect(captured).not.toContain("smtp_password");
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    const current = await getMailSettings(page);
    const restored = await adminRequest(page, "/api/v1/admin/mail-settings", "PUT", {
      revision: current.revision,
      smtp_enabled: original.smtp_enabled,
      smtp_host: original.smtp_host,
      smtp_port: original.smtp_port,
      smtp_username: original.smtp_username,
      smtp_encryption: original.smtp_encryption,
      smtp_from_address: original.smtp_from_address,
      remind_mail_enable: original.remind_mail_enable
    });
    expect(restored.status, restored.body).toBe(200);
  }
});

async function login(page: Page) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function getMailSettings(page: Page): Promise<MailSettings> {
  const response = await adminRequest(page, "/api/v1/admin/mail-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("mail settings response is invalid");
  return data as MailSettings;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  path = adminAPIPath(path);
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
  }, { path, method, body });
}

async function waitForCapturedMail(request: APIRequestContext, recipient: string, subject: string): Promise<string> {
  let identifier = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages`);
    if (!response.ok()) return 0;
    const payload = await response.json() as { messages?: Array<{ ID?: string; Subject?: string; To?: Array<{ Address?: string }> }> };
    const match = payload.messages?.find((message) => message.Subject === subject && message.To?.some((address) => address.Address === recipient));
    identifier = match?.ID ?? "";
    return identifier === "" ? 0 : 1;
  }, { timeout: 15_000, intervals: [250, 500, 1_000] }).toBe(1);
  return identifier;
}
