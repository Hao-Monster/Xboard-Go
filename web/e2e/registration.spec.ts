import { expect, test, type Page } from "@playwright/test";

test("public user can register, receives a session, and enters the user portal", async ({ page, context }, testInfo) => {
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
  expect(failures).toEqual([]);
});

async function expectCurrentSession(page: Page, email: string) {
  const response = await page.request.get("/api/v1/auth/session");
  expect(response.status()).toBe(200);
  const payload = await response.json() as { data?: { email?: string; is_admin?: boolean } };
  expect(payload.data).toMatchObject({ email, is_admin: false });
}
