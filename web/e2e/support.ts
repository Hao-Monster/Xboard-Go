import { expect, type Page } from "@playwright/test";

export const adminEmail = process.env.XBOARD_E2E_ADMIN_EMAIL ?? "admin@e2e.test";
export const adminPassword = process.env.XBOARD_E2E_ADMIN_PASSWORD ?? "e2e-admin-password-123";

export async function createAdminUserFixture(page: Page, input: {
  email: string;
  password: string;
  transferEnable?: number;
  isDistributor?: boolean;
  distributorName?: string;
}) {
  await expect(page.getByRole("button", { name: "退出" })).toBeVisible();
  const payload = {
    email: input.email,
    password: input.password,
    is_admin: false,
    is_staff: false,
    is_distributor: input.isDistributor ?? false,
    distributor_name: input.isDistributor ? (input.distributorName ?? "") : null,
    group_id: null,
    transfer_enable: input.transferEnable ?? 0,
    expired_at: null,
    speed_limit: 0,
    device_limit: 0,
    banned: false
  };
  const result = await page.evaluate(async (body) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch("/api/v1/admin/users", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: JSON.stringify(body)
    });
    return { status: response.status, body: await response.text() };
  }, payload);
  expect(result.status, result.body).toBe(201);
}

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
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(new RegExp(`^${action} .+$`));
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
