import { expect, test } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("administrator creates and changes a user's access state", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  const administratorRow = page.getByRole("row").filter({ hasText: adminEmail });
  await expect(administratorRow.getByText(/最后登录/)).toBeVisible();
  await expect(administratorRow.getByText("最后登录 从未", { exact: true })).toHaveCount(0);

  const unique = Date.now();
  const email = `e2e-user-${unique}@example.test`;
  await page.getByRole("button", { name: "新增用户" }).click();
  let dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("邮箱").fill(email);
  await dialog.getByLabel("初始密码").fill("e2e-user-password-123");
  await dialog.getByLabel("流量额度（字节）").fill("1073741824");
  await dialog.getByLabel("限速（Mbps，0 为不限速）").fill("25");
  await dialog.getByLabel("设备数（0 为不限设备）").fill("2");
  await dialog.getByRole("button", { name: "创建" }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: email }).getByText("最后登录 从未", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: `编辑用户：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "编辑用户" });
  await dialog.getByLabel("限速（Mbps，0 为不限速）").fill("50");
  await dialog.getByLabel("封禁用户").check();
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByRole("row").filter({ hasText: email }).getByText("已封禁", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: `重置密码：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "重置用户密码" });
  await dialog.getByLabel("新密码").fill("e2e-rotated-password-456");
  await dialog.getByRole("button", { name: "确认重置" }).click();
  await expect(dialog).toBeHidden();

  await page.getByRole("searchbox", { name: "邮箱前缀" }).fill(`e2e-user-${unique}`);
  await page.getByLabel("用户状态").selectOption("banned");
  await page.getByRole("button", { name: "查询用户" }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});
