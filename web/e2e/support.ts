import { expect, type Page } from "@playwright/test";

export const adminEmail = process.env.XBOARD_E2E_ADMIN_EMAIL ?? "admin@e2e.test";
export const adminPassword = process.env.XBOARD_E2E_ADMIN_PASSWORD ?? "e2e-admin-password-123";

export async function logoutAndWait(page: Page) {
  await page.getByRole("button", { name: "退出" }).click();
  await expect(page.getByRole("heading", { name: "登录 Xboard-Go" })).toBeVisible();
}
