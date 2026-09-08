import { expect, test } from "@playwright/test";
import { adminAPIPath, adminEmail, adminEntryPath, adminPassword, createAdminUserFixture } from "./support";

test("[USER-001][USER-003] explicit deactivation and recovery preserve the user and revoke old access", async ({ page, browser }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  const email = `lifecycle-${Date.now()}@example.test`;
  const password = "lifecycle-test-password-123";
  await createAdminUserFixture(page, { email, password, transferEnable: 1024 });

  const userContext = await browser.newContext({ baseURL: new URL(page.url()).origin });
  try {
    const userPage = await userContext.newPage();
    await userPage.goto("/");
    await userPage.getByLabel("邮箱").fill(email);
    await userPage.getByLabel("密码").fill(password);
    await userPage.getByRole("button", { name: "登录", exact: true }).click();
    await expect(userPage.getByRole("button", { name: "退出" })).toBeVisible();

    await page.getByRole("button", { name: "用户管理", exact: true }).click();
    await page.getByRole("searchbox", { name: "邮箱前缀" }).fill(email);
    await page.getByRole("button", { name: "查询用户" }).click();
    const operations = page.getByRole("button", { name: `用户操作：${email}` });
    await operations.click();
    await page.getByRole("dialog", { name: "用户操作", exact: true }).getByRole("button", { name: "停用用户", exact: true }).click();
    let dialog = page.getByRole("dialog", { name: "停用用户", exact: true });
    await expect(dialog).toContainText("订单、余额与佣金记录保留");
    await expect(dialog.getByRole("button", { name: "确认执行" })).toBeDisabled();
    await dialog.getByRole("checkbox").check();
    const deactivation = page.waitForResponse(response => response.request().method() === "POST" && /\/users\/\d+\/deactivate$/.test(new URL(response.url()).pathname));
    await dialog.getByRole("button", { name: "确认执行" }).click();
    const response = await deactivation;
    expect(response.status(), await response.text()).toBe(200);
    const deactivated = (await response.json()).data as { id: number; revision: number; lifecycle_status: string; email: string; restore_until: string };
    expect(deactivated.lifecycle_status).toBe("deactivated");
    expect(deactivated.email).toBe(email);
    await expect(dialog).toBeHidden();

    const expiredSession = await userContext.request.get("/api/v1/auth/session");
    expect(expiredSession.status()).toBe(401);
    await operations.click();
    dialog = page.getByRole("dialog", { name: "用户操作", exact: true });
    await expect(dialog.getByRole("button", { name: "不可逆匿名化" })).toBeDisabled();
    await expect(dialog).toContainText("不会自动清除");
    await dialog.getByRole("button", { name: "恢复用户", exact: true }).click();
    dialog = page.getByRole("dialog", { name: "恢复用户", exact: true });
    await dialog.getByRole("checkbox").check();
    const restoration = page.waitForResponse(response => response.request().method() === "POST" && new URL(response.url()).pathname === adminAPIPath(`/api/v1/admin/users/${deactivated.id}/restore`));
    await dialog.getByRole("button", { name: "确认执行" }).click();
    const restoredResponse = await restoration;
    expect(restoredResponse.status(), await restoredResponse.text()).toBe(200);
    expect((await restoredResponse.json()).data).toMatchObject({ id: deactivated.id, email, lifecycle_status: "active", banned: false });
    expect((await userContext.request.get("/api/v1/auth/session")).status()).toBe(401);
    await userPage.goto("/");
    await userPage.getByLabel("邮箱").fill(email);
    await userPage.getByLabel("密码").fill(password);
    await userPage.getByRole("button", { name: "登录", exact: true }).click();
    await expect(userPage.getByRole("button", { name: "退出" })).toBeVisible();
    expect(errors).toEqual([]);
  } finally {
    await userContext.close();
  }
});
