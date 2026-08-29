import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectAuthPage, expectLoginPage, logoutAndWait } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

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

test("visitor completes modern and Passport-compatible password recovery through Mailpit", async ({ page, request }) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(90_000);
  const unique = `${Date.now()}-${test.info().project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `password-recovery-${unique}@example.test`;
  const oldPassword = `old-password-${unique}`;
  const newPassword = `new-password-${unique}`;
  const compatibilityEmail = `passport-recovery-${unique}@example.test`;
  const compatibilityOldPassword = `passport-old-password-${unique}`;
  const compatibilityNewPassword = `passport-new-password-${unique}`;
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  let original: TicketSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword);
    original = await getTicketSettings(page);
    await saveTicketSettings(page, {
      ...original,
      app_name: "Xboard-Go",
      app_url: new URL(page.url()).origin,
      smtp_enabled: true,
      smtp_host: "mailpit",
      smtp_port: 1025,
      smtp_username: "",
      smtp_encryption: "none",
      smtp_from_address: "support@xboard-go.local"
    });
    const created = await adminRequest(page, "/api/v1/admin/users", "POST", {
      email,
      password: oldPassword,
      group_id: null,
      transfer_enable: 1_073_741_824,
      expired_at: null,
      speed_limit: 0,
      device_limit: 0,
      banned: false
    });
    expect(created.status, created.body).toBe(201);
    const compatibilityUser = await adminRequest(page, "/api/v1/admin/users", "POST", {
      email: compatibilityEmail,
      password: compatibilityOldPassword,
      group_id: null,
      transfer_enable: 1_073_741_824,
      expired_at: null,
      speed_limit: 0,
      device_limit: 0,
      banned: false
    });
    expect(compatibilityUser.status, compatibilityUser.body).toBe(201);
    await logoutAndWait(page);

    await page.goto("/#/forgetpassword");
    await expectAuthPage(page, "重置密码");
    await page.getByLabel("邮箱", { exact: true }).fill(email);
    const sent = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/password-reset/request"));
    await page.getByRole("button", { name: "发送", exact: true }).click();
    expect((await sent).status()).toBe(202);
    await expect(page.getByRole("status")).toHaveText("验证码已发送，请检查邮箱");
    await expect(page.getByRole("button", { name: /\d+ 秒/ })).toBeDisabled();

    const code = await waitForPasswordResetCode(request, email, "Xboard-Go邮箱验证码");
    await page.getByLabel("邮箱验证码", { exact: true }).fill(code);
    await page.getByLabel("密码", { exact: true }).fill(newPassword);
    await page.getByLabel("再次输入密码").fill(newPassword);
    const reset = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/password-reset/confirm"));
    await page.getByRole("button", { name: "重置密码", exact: true }).click();
    expect((await reset).status()).toBe(200);
    await expect(page.getByRole("status")).toHaveText("重置密码成功,正在返回登录");
    await expectLoginPage(page);

    await page.getByLabel("邮箱", { exact: true }).fill(email);
    await page.getByLabel("密码").fill(oldPassword);
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("邮箱或密码错误");
    await page.getByLabel("密码").fill(newPassword);
    await page.getByRole("button", { name: "登录", exact: true }).click();
    await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();

    const compatibilitySent = await request.post("/api/v2/passport/comm/sendEmailVerify", {
      data: { email: compatibilityEmail }
    });
    expect(compatibilitySent.status()).toBe(200);
    expect(await compatibilitySent.json()).toMatchObject({ status: "success", message: "操作成功", data: true, error: null });
    const compatibilityCode = await waitForPasswordResetCode(request, compatibilityEmail, "Xboard-Go邮箱验证码");
    const compatibilityRepeated = await request.post("/api/v1/passport/comm/sendEmailVerify", {
      data: { email: compatibilityEmail }
    });
    expect(compatibilityRepeated.status()).toBe(400);
    expect(await compatibilityRepeated.json()).toMatchObject({
      status: "fail", message: "验证码已发送，请过一会儿再请求", error: { code: "passport_email_cooldown" }
    });
    const compatibilityReset = await request.post("/api/v2/passport/auth/forget", {
      data: { email: compatibilityEmail, email_code: compatibilityCode, password: compatibilityNewPassword }
    });
    expect(compatibilityReset.status()).toBe(200);
    expect(await compatibilityReset.json()).toMatchObject({ status: "success", message: "操作成功", data: true, error: null });
    expect((await request.post("/api/v1/passport/auth/login", {
      data: { email: compatibilityEmail, password: compatibilityOldPassword }
    })).status()).toBe(401);
    expect((await request.post("/api/v1/passport/auth/login", {
      data: { email: compatibilityEmail, password: compatibilityNewPassword }
    })).status()).toBe(200);

    const privacy = await page.evaluate(async ({ knownEmail, unknownEmail }) => {
      const post = async (path: string, body: unknown) => {
        const response = await fetch(path, {
          method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body)
        });
        const payload = await response.json() as { status?: string; error?: { code?: string } };
        return { status: response.status, retryAfter: response.headers.get("Retry-After"), payload };
      };
      const knownRequest = await post("/api/v1/auth/password-reset/request", { email: knownEmail });
      const unknownRequest = await post("/api/v1/auth/password-reset/request", { email: unknownEmail });
      const confirmations: Record<string, Array<Awaited<ReturnType<typeof post>>>> = {};
      for (const address of [knownEmail, unknownEmail]) {
        confirmations[address] = [];
        for (let attempt = 0; attempt < 4; attempt += 1) {
          confirmations[address].push(await post("/api/v1/auth/password-reset/confirm", {
            email: address, email_code: "000000", password: "privacy-check-password-123"
          }));
        }
      }
      return { knownRequest, unknownRequest, confirmations };
    }, { knownEmail: email, unknownEmail: `unknown-${unique}@example.test` });
    expect(privacy.knownRequest).toEqual(privacy.unknownRequest);
    for (const results of Object.values(privacy.confirmations)) {
      expect(results.map((result) => [result.status, result.payload.error?.code ?? null])).toEqual([
        [400, "password_reset_invalid"], [400, "password_reset_invalid"],
        [400, "password_reset_invalid"], [429, "password_reset_locked"]
      ]);
      const retryAfter = Number(results[3].retryAfter);
      expect(retryAfter).toBeGreaterThanOrEqual(299);
      expect(retryAfter).toBeLessThanOrEqual(300);
    }

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    if (original !== null) {
      await ensureAdministrator(page);
      const current = await getTicketSettings(page);
      await saveTicketSettings(page, { ...original, revision: current.revision });
    }
  }
});

async function waitForPasswordResetCode(request: APIRequestContext, recipient: string, subject: string): Promise<string> {
  let code = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages`);
    if (!response.ok()) return 0;
    const payload = await response.json() as { messages?: Array<{ ID?: string; Subject?: string; To?: Array<{ Address?: string }> }> };
    const message = payload.messages?.find((item) => item.Subject === subject && item.To?.some((address) => address.Address === recipient));
    if (message?.ID === undefined) return 0;
    const detail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(message.ID)}`);
    if (!detail.ok()) return 0;
    const match = JSON.stringify(await detail.json()).match(/\b(\d{6})\b/);
    code = match?.[1] ?? "";
    return code === "" ? 0 : 1;
  }, { timeout: 15_000, intervals: [250, 500, 1_000] }).toBe(1);
  return code;
}

async function login(page: Page, email: string, password: string) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出" }).count() > 0) await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
}

async function getTicketSettings(page: Page): Promise<TicketSettings> {
  const response = await adminRequest(page, "/api/v1/admin/ticket-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload = JSON.parse(response.body) as { data: TicketSettings };
  return payload.data;
}

async function saveTicketSettings(page: Page, settings: TicketSettings): Promise<TicketSettings> {
  const response = await adminRequest(page, "/api/v1/admin/ticket-settings", "PUT", {
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
  });
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: TicketSettings }).data;
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
