import { expect, type Page } from "@playwright/test";

export const adminEmail = process.env.XBOARD_E2E_ADMIN_EMAIL ?? "admin@e2e.test";
export const adminPassword = process.env.XBOARD_E2E_ADMIN_PASSWORD ?? "e2e-admin-password-123";
export const adminSecurePath = process.env.XBOARD_E2E_ADMIN_PATH?.trim() || "e2e-admin-secure";
export const adminEntryPath = `/${adminSecurePath}/`;

export function adminAPIPath(path: string): string {
  const prefix = "/api/v1/admin/";
  if (!path.startsWith(prefix)) return path;
  return `${prefix}${adminSecurePath}/${path.slice(prefix.length)}`;
}

export async function createAdminUserFixture(page: Page, input: {
  email: string;
  password: string;
  transferEnable?: number;
  isDistributor?: boolean;
  distributorName?: string;
  inviteUserEmail?: string;
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
  const result = await page.evaluate(async ({ body, path }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: JSON.stringify(body)
    });
    return { status: response.status, body: await response.text() };
  }, { body: payload, path: adminAPIPath("/api/v1/admin/users") });
  expect(result.status, result.body).toBe(201);
  if (input.inviteUserEmail === undefined) return;

  const decoded: unknown = JSON.parse(result.body);
  const created: unknown = typeof decoded === "object" && decoded !== null ? Reflect.get(decoded, "data") : null;
  if (typeof created !== "object" || created === null) throw new Error("created user response is invalid");
  const userID = Number(Reflect.get(created, "id"));
  const revision = Number(Reflect.get(created, "revision"));
  if (!Number.isSafeInteger(userID) || userID < 1 || !Number.isSafeInteger(revision) || revision < 1) {
    throw new Error("created user identity is invalid");
  }
  const update = await page.evaluate(async ({ body, path }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(path, {
      method: "PATCH",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: JSON.stringify(body)
    });
    return { status: response.status, body: await response.text() };
  }, { path: adminAPIPath(`/api/v1/admin/users/${userID}`), body: {
    revision,
    email: input.email,
    group_id: null,
    invite_user_email: input.inviteUserEmail,
    transfer_enable: input.transferEnable ?? 0,
    expired_at: null,
    speed_limit: 0,
    device_limit: 0,
    banned: false
  } });
  expect(update.status, update.body).toBe(200);
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
  await page.goto("/");
  await expectLoginPage(page);
}
