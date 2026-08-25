import { expect, test, type Page } from "@playwright/test";

test("public user can register, receives a session, and enters the user portal", async ({ page, context, request }, testInfo) => {
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) failures.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `register-${unique}@example.test`;
  const password = `register-password-${unique}`;

  await page.goto("/#/register");
  await expect(page.getByRole("heading", { name: /注册 / })).toBeVisible();
  await expect(page).toHaveTitle(/注册 \| /);
  await page.getByLabel("邮箱").fill(email.toUpperCase());
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByLabel("再次输入密码").fill(password);
  const registration = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/register"));
  await page.getByRole("button", { name: "注册", exact: true }).click();
  expect((await registration).status()).toBe(200);

  await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "公告", exact: true })).toBeVisible();
  await expectCurrentSession(page, email);
  const cookies = await context.cookies();
  const session = cookies.find((cookie) => cookie.name === "xboard_session");
  const csrf = cookies.find((cookie) => cookie.name === "xboard_csrf");
  expect(session?.httpOnly).toBe(true);
  expect(session?.sameSite).toBe("Strict");
  expect(csrf?.httpOnly).toBe(false);
  expect(csrf?.sameSite).toBe("Strict");

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: /登录 / })).toBeVisible();
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
  await expectCurrentSession(page, email);

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: /登录 / })).toBeVisible();
  const loggedInV2 = await browserPost(page, "/api/v2/passport/auth/login", { email, password });
  expect(loggedInV2.status, loggedInV2.body).toBe(200);
  const loginCredential = expectLegacyCredential(loggedInV2.body, false);
  const v2Session = await request.get("/api/v1/auth/session", {
    headers: { Authorization: loginCredential }
  });
  const v2SessionBody = await v2Session.text();
  expect(v2Session.status(), v2SessionBody).toBe(200);
  expect((JSON.parse(v2SessionBody) as { data: { email: string } }).data.email).toBe(email);

  const rejectedOrigin = await request.post("/api/v2/passport/auth/login", {
    headers: { Origin: "https://attacker.example.test" }, data: { email, password }
  });
  expect({ status: rejectedOrigin.status(), body: await rejectedOrigin.json() }).toEqual({
    status: 403,
    body: {
      status: "fail", message: "请求来源不受信任",
      error: { code: "invalid_origin", message: "请求来源不受信任" }
    }
  });
  expect(failures).toEqual([]);
});

async function browserPost(page: Page, path: string, body: unknown) {
  return page.evaluate(async ({ requestPath, requestBody }) => {
    const response = await fetch(requestPath, {
      method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" },
      body: JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestBody: body });
}

function expectLegacyCredential(body: string, isAdmin: boolean): string {
  const payload = JSON.parse(body) as {
    status?: string; message?: string; error?: unknown;
    data?: { token?: string; auth_data?: string; is_admin?: boolean; is_distributor?: boolean };
  };
  expect(payload).toMatchObject({
    status: "success", message: "操作成功", error: null,
    data: { is_admin: isAdmin, is_distributor: false }
  });
  expect(payload.data?.token).toMatch(/^[0-9a-f]{32}$/);
  expect(payload.data?.auth_data).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);
  return payload.data?.auth_data ?? "";
}

async function expectCurrentSession(page: Page, email: string) {
  const response = await page.request.get("/api/v1/auth/session");
  expect(response.status()).toBe(200);
  const payload = await response.json() as { data?: { email?: string; is_admin?: boolean } };
  expect(payload.data).toMatchObject({ email, is_admin: false });
}
