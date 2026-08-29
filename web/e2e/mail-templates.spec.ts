import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

interface MailTemplate {
  name: "verify" | "notify" | "remindExpire" | "remindTraffic" | "mailLogin";
  label: string;
  subject: string;
  content: string;
  customized: boolean;
  revision: number;
}

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

test("administrator safely customizes, previews and reloads a system mail template", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page);
  const original = await getMailTemplate(page, "notify");
  try {
    await openMailTemplates(page);
    for (const label of ["邮箱验证码", "站点通知", "到期提醒", "流量提醒", "邮件登录"]) {
      await expect(page.getByRole("button", { name: new RegExp(label) })).toBeVisible();
    }

    await page.getByRole("button", { name: /站点通知/ }).click();
    await expect(page.getByRole("heading", { name: "站点通知" })).toBeVisible();
    const unique = `E2E-${Date.now()}`;
    const subject = `{{name}} - ${unique}`;
    const content = `<p>${unique}: {{content}}</p><script>window.e2eUnsafe=true</script><img src="https://tracker.invalid/pixel"><a href="https://evil.invalid/collect">external</a>`;
    await page.getByLabel("邮件主题").fill(subject);
    await page.getByLabel("HTML 内容").fill(content);

    page.once("dialog", async (dialog) => {
      expect(dialog.message()).toContain("未保存的修改");
      await dialog.dismiss();
    });
    await page.getByRole("button", { name: /邮箱验证码/ }).click();
    await expect(page.getByRole("heading", { name: "站点通知" })).toBeVisible();

    await page.getByRole("button", { name: "预览" }).click();
    const preview = page.getByTitle("邮件 HTML 预览");
    await expect(preview).toHaveAttribute("sandbox", "");
    await expect(preview).toHaveAttribute("srcdoc", new RegExp(unique));
    const previewHTML = (await preview.getAttribute("srcdoc"))?.toLowerCase() ?? "";
    expect(previewHTML).not.toContain("<script");
    expect(previewHTML).not.toContain("tracker.invalid");
    expect(previewHTML).not.toContain("evil.invalid");

    await page.getByRole("button", { name: "保存模板" }).click();
    await expect(page.getByRole("status")).toContainText("邮件模板已保存");
    await expect(page.getByRole("button", { name: /站点通知/ })).toContainText("已自定义");

    await page.reload();
    await openMailTemplates(page);
    await page.getByRole("button", { name: /站点通知/ }).click();
    await expect(page.getByLabel("邮件主题")).toHaveValue(subject);
    await expect(page.getByLabel("HTML 内容")).toHaveValue(content);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    const current = await getMailTemplate(page, "notify");
    const restored = original.customized
      ? await adminRequest(page, "/api/v1/admin/mail-templates/notify", "PUT", {
          revision: current.revision, subject: original.subject, content: original.content
        })
      : await adminRequest(page, "/api/v1/admin/mail-templates/notify/reset", "POST", { revision: current.revision });
    expect(restored.status, restored.body).toBe(200);
  }
});

test("a saved template is delivered as sanitized HTML and plain text through Mailpit", async ({ page, request }) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(90_000);
  await login(page);
  const originalTemplate = await getMailTemplate(page, "notify");
  const originalSettings = await getMailSettings(page);
  test.skip(originalSettings.smtp_password_set, "requires an isolated test database without an unrecoverable existing SMTP secret");
  const marker = `template-delivery-${Date.now()}`;
  try {
    const templateUpdate = await adminRequest(page, "/api/v1/admin/mail-templates/notify", "PUT", {
      revision: originalTemplate.revision,
      subject: `{{name}} - ${marker}`,
      content: `<p>${marker}: {{content}}</p><script>unsafe()</script><img src="https://tracker.invalid/pixel">`
    });
    expect(templateUpdate.status, templateUpdate.body).toBe(200);
    const settingsUpdate = await adminRequest(page, "/api/v1/admin/mail-settings", "PUT", {
      revision: originalSettings.revision,
      smtp_enabled: true,
      smtp_host: "mailpit",
      smtp_port: 1025,
      smtp_username: "",
      smtp_password: "",
      smtp_encryption: "none",
      smtp_from_address: "support@xboard-go.local",
      remind_mail_enable: originalSettings.remind_mail_enable
    });
    expect(settingsUpdate.status, settingsUpdate.body).toBe(200);

    await openMailTemplates(page);
    await page.getByRole("button", { name: /站点通知/ }).click();
    await page.getByLabel("测试收件人").fill(adminEmail);
    await page.getByRole("button", { name: "发送测试邮件" }).click();
    await expect(page.getByRole("status")).toContainText(`测试邮件已发送至 ${adminEmail}`);

    const appName = await getAppName(page);
    const messageID = await waitForCapturedMail(request, adminEmail, `${appName} - ${marker}`);
    const detail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(messageID)}`);
    expect(detail.ok()).toBeTruthy();
    const captured = JSON.stringify(await detail.json());
    expect(captured).toContain(marker);
    expect(captured).toContain("这是一封测试通知邮件。");
    expect(captured.toLowerCase()).not.toContain("<script");
    expect(captured).not.toContain("tracker.invalid");
  } finally {
    const currentTemplate = await getMailTemplate(page, "notify");
    const templateRestore = originalTemplate.customized
      ? await adminRequest(page, "/api/v1/admin/mail-templates/notify", "PUT", {
          revision: currentTemplate.revision, subject: originalTemplate.subject, content: originalTemplate.content
        })
      : await adminRequest(page, "/api/v1/admin/mail-templates/notify/reset", "POST", { revision: currentTemplate.revision });
    expect(templateRestore.status, templateRestore.body).toBe(200);

    const currentSettings = await getMailSettings(page);
    const settingsRestore = await adminRequest(page, "/api/v1/admin/mail-settings", "PUT", {
      revision: currentSettings.revision,
      smtp_enabled: originalSettings.smtp_enabled,
      smtp_host: originalSettings.smtp_host,
      smtp_port: originalSettings.smtp_port,
      smtp_username: originalSettings.smtp_username,
      smtp_encryption: originalSettings.smtp_encryption,
      smtp_from_address: originalSettings.smtp_from_address,
      remind_mail_enable: originalSettings.remind_mail_enable
    });
    expect(settingsRestore.status, settingsRestore.body).toBe(200);
  }
});

async function login(page: Page) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function openMailTemplates(page: Page) {
  await page.getByRole("button", { name: "邮件设置", exact: true }).click();
  await page.getByRole("tab", { name: "邮件模板", exact: true }).click();
  await expect(page.getByRole("heading", { name: "邮件模板", exact: true })).toBeVisible();
}

async function getMailTemplate(page: Page, name: MailTemplate["name"]): Promise<MailTemplate> {
  const response = await adminRequest(page, `/api/v1/admin/mail-templates/${name}`, "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("mail template response is invalid");
  return data as MailTemplate;
}

async function getMailSettings(page: Page): Promise<MailSettings> {
  const response = await adminRequest(page, "/api/v1/admin/mail-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("mail settings response is invalid");
  return data as MailSettings;
}

async function getAppName(page: Page): Promise<string> {
  const response = await page.request.get("/api/v1/guest/comm/config");
  expect(response.status()).toBe(200);
  const payload: unknown = await response.json();
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  const appName = typeof data === "object" && data !== null ? Reflect.get(data, "app_name") : null;
  if (typeof appName !== "string" || appName === "") throw new Error("guest app name is invalid");
  return appName;
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
