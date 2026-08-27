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
  const table = page.getByRole("table", { name: "用户列表" });
  const attachedHeaders = await table.locator("th").allTextContents();
  for (const heading of ["ID", "邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间", "操作"]) {
    const column = table.getByRole("columnheader", { name: heading });
    if ((page.viewportSize()?.width ?? 1280) >= 720) await expect(column).toBeVisible();
    else expect(attachedHeaders.some((value) => value.trim().startsWith(heading)), `mobile header ${heading}`).toBe(true);
  }
  await page.getByRole("searchbox", { name: "邮箱前缀" }).fill(adminEmail);
  await page.getByRole("button", { name: "查询用户" }).click();
  const administratorRow = page.getByRole("row").filter({ hasText: adminEmail });
  await expect(administratorRow.getByText(/最后登录/)).toBeVisible();
  await expect(administratorRow.getByText("最后登录 从未", { exact: true })).toHaveCount(0);

  await page.getByRole("searchbox", { name: "邮箱前缀" }).clear();
  await page.getByRole("button", { name: "查询用户" }).click();

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
  await expect(page.getByRole("row").filter({ hasText: email })).toContainText("0 / 2");

  await page.getByRole("button", { name: `查看详情：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "用户详情" });
  for (const label of ["角色", "套餐", "权限组", "邀请人", "Telegram", "备注", "已用流量", "余额", "佣金", "提醒", "注册 / 更新"]) {
    await expect(dialog.getByText(label, { exact: true })).toBeVisible();
  }
  await expect(dialog.getByText(/Token|UUID|订阅地址/)).toHaveCount(0);
  await dialog.getByRole("button", { name: "关闭" }).last().click();

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

  await page.getByRole("button", { name: "高级筛选" }).click();
  await page.getByRole("button", { name: "添加筛选条件" }).click();
  await page.getByLabel("筛选字段 1").selectOption("subscription_token");
  await expect(page.getByLabel("筛选操作符 1").getByRole("option")).toHaveCount(2);
  const secretProbe = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"; // gitleaks:allow -- deterministic transport-safety fixture
  await page.getByLabel("筛选值 1").fill(secretProbe);
  const secretRequestPromise = page.waitForRequest((request) => request.url().endsWith("/api/v1/admin/users/query"));
  await page.getByRole("button", { name: "查询用户" }).click();
  const secretRequest = await secretRequestPromise;
  expect(secretRequest.method()).toBe("POST");
  expect(secretRequest.url()).not.toContain(secretProbe);
  expect(secretRequest.postDataJSON()).toMatchObject({
    filters: [{ field: "subscription_token", operator: "eq", value: secretProbe }]
  });
  await expect(page.getByText("没有符合条件的用户。", { exact: true })).toBeVisible();

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});
