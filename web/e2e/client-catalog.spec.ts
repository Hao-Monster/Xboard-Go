import { expect, test, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

test("administrator configures client actions and a user browses secure platform downloads", async ({ page }) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  const unique = Date.now();
  const email = `client-user-${unique}@example.test`;
  const password = "client-user-password-123";

  await login(page, adminEmail, adminPassword);
  await createAdminUserFixture(page, { email, password });

  await page.getByRole("button", { name: "客户端管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "客户端管理" })).toBeVisible();
  const android = page.getByRole("region", { name: "Karing Android" });
  await android.getByLabel("直接下载").fill("https://downloads.example.test/karing.apk");
  await android.getByLabel("扫码下载").fill("https://qr.example.test/karing");
  await android.getByLabel("网盘下载").fill("https://cloud.example.test/karing");
  await android.getByLabel("使用教程").fill("/guide/12/karing");
  await page.getByRole("button", { name: "保存全部配置" }).click();
  await expect(page.getByText("客户端按钮配置已保存。", { exact: true })).toBeVisible();

  await logoutAndWait(page);
  await login(page, email, password);
  await page.getByRole("button", { name: "客户端下载", exact: true }).click();
  await expect(page.getByRole("heading", { name: "客户端下载" })).toBeVisible();
  await page.getByRole("button", { name: "Android", exact: true }).click();
  const card = page.getByRole("article", { name: "Karing" });
  const directLink = card.getByRole("link", { name: "直接下载" });
  await expect(directLink).toHaveAttribute("href", /\/client-download\/karing\/android$/);
  const directHref = await directLink.getAttribute("href");
  if (directHref === null) throw new Error("direct download link is missing its href");
  const redirect = await page.request.get(directHref, { maxRedirects: 0 });
  expect(redirect.status()).toBe(302);
  expect(redirect.headers().location).toBe("https://downloads.example.test/karing.apk");
  expect(redirect.headers()["cache-control"]).toBe("private, no-store");
  expect(redirect.headers()["referrer-policy"]).toBe("no-referrer");
  await expect(card.getByRole("link", { name: "网盘下载" })).toHaveAttribute("rel", "noopener noreferrer");
  await card.getByRole("button", { name: "扫码下载" }).click();
  const dialog = page.getByRole("dialog", { name: "扫码下载 Karing" });
  await expect(dialog.getByRole("img", { name: "Karing 下载二维码" })).toHaveAttribute("src", /^data:image\/svg\+xml;base64,/);
  await dialog.getByRole("button", { name: "关闭扫码下载 Karing" }).click();

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "客户端管理", exact: true }).click();
  for (const label of ["直接下载", "扫码下载", "网盘下载", "使用教程"]) await android.getByLabel(label).fill("");
  await page.getByRole("button", { name: "保存全部配置" }).click();
  await expect(page.getByText("客户端按钮配置已保存。", { exact: true })).toBeVisible();
  expect(pageErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}
