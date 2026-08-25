import { expect, type Page } from "@playwright/test";

export const adminEmail = process.env.XBOARD_E2E_ADMIN_EMAIL ?? "admin@e2e.test";
export const adminPassword = process.env.XBOARD_E2E_ADMIN_PASSWORD ?? "e2e-admin-password-123";

export async function publicAppName(page: Page): Promise<string> {
  const response = await page.request.get("/api/v1/guest/comm/config");
  expect(response.status()).toBe(200);
  const payload: unknown = await response.json();
  if (typeof payload !== "object" || payload === null) throw new Error("guest config is not an object");
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) throw new Error("guest config data is not an object");
  const appName: unknown = Reflect.get(data, "app_name");
  if (typeof appName !== "string" || appName.trim() === "") throw new Error("guest config app_name is missing");
  return appName;
}

export async function expectAuthPage(page: Page, action: "登录" | "注册" | "重置密码") {
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${action} ${await publicAppName(page)}`);
}

export async function expectLoginPage(page: Page) {
  await expectAuthPage(page, "登录");
  await expect(page.getByLabel("邮箱", { exact: true })).toBeVisible();
  await expect(page.getByLabel("密码", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "登录", exact: true })).toBeVisible();
}

export async function logoutAndWait(page: Page) {
  await page.getByRole("button", { name: "退出" }).click();
  await expectLoginPage(page);
}
