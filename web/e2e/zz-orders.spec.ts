import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage, logoutAndWait } from "./support";

test("free checkout and administrator order lifecycle work on every supported viewport", async ({ page }) => {
  test.setTimeout(90_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  const unique = `${Date.now()}-${test.info().project.name}`;
  const userEmail = `order-user-${unique}@example.test`;
  const userPassword = "order-user-password-123";
  const planName = `E2E 免费订单套餐 ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createUser(page, userEmail, userPassword);
  await createFreePlan(page, planName);

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "订阅套餐", exact: true }).click();
  const planCard = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await expect(planCard).toContainText("月付 ¥0.00");
  await planCard.getByRole("button", { name: "立即订阅", exact: true }).click();

  let dialog = page.getByRole("dialog", { name: "配置订阅" });
  await expect(dialog.getByLabel("付款周期")).toHaveValue("monthly");
  await expect(dialog).toContainText("套餐标价¥0.00");
  await dialog.getByRole("button", { name: "下单", exact: true }).click();

  await expect(page.getByRole("heading", { name: "我的订单", exact: true })).toBeVisible();
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await expect(dialog).toContainText(planName);
  await expect(dialog).toContainText("待支付");
  await expect(dialog).toContainText("该订单无需在线支付，可直接开通。");
  const userTradeNo = (await dialog.locator("strong.monospace").textContent())?.trim() ?? "";
  expect(userTradeNo).toMatch(/^\d{25}$/);
  await dialog.getByRole("button", { name: "立即开通", exact: true }).click();
  await expect(dialog.getByText("已完成", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "立即开通", exact: true })).toBeHidden();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(orderRow(page, userTradeNo)).toContainText("已完成");

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "订单管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "订单管理", exact: true })).toBeVisible();
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(userTradeNo);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  await expect(orderRow(page, userTradeNo)).toContainText(userEmail);
  await expect(orderRow(page, userTradeNo)).toContainText("已完成");

  const assignedTradeNo = await assignOrder(page, userEmail, planName, "12.34");
  expect(assignedTradeNo).toMatch(/^[0-9a-f]{32}$/);
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await expect(dialog).toContainText("订单金额¥12.34");
  await dialog.getByRole("button", { name: "标记已支付并开通", exact: true }).click();
  await expect(dialog.getByText("已完成", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "标记已支付并开通", exact: true })).toBeHidden();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();

  const cancelledTradeNo = await assignOrder(page, userEmail, planName, "5.00");
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await dialog.getByRole("button", { name: "取消订单", exact: true }).click();
  await expect(dialog.getByText("已取消", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("combobox", { name: "订单状态", exact: true }).selectOption("2");
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(cancelledTradeNo);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  await expect(orderRow(page, cancelledTradeNo)).toContainText("已取消");

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function createUser(page: Page, email: string, password: string) {
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("邮箱", { exact: true }).fill(email);
  await dialog.getByLabel("初始密码", { exact: true }).fill(password);
  await dialog.getByLabel("流量额度（字节）", { exact: true }).fill("0");
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();
}

async function createFreePlan(page: Page, name: string) {
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称", { exact: true }).fill(name);
  await dialog.getByLabel("流量（GiB）", { exact: true }).fill("100");
  await dialog.getByLabel("月付", { exact: true }).fill("0.00");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const row = planRow(page, name);
  await expect(row).toContainText("月付 ¥0.00");
  await row.getByLabel("展示", { exact: true }).check();
  await row.getByLabel("销售", { exact: true }).check();
}

async function assignOrder(page: Page, email: string, planName: string, amount: string): Promise<string> {
  await page.getByRole("button", { name: "添加订单", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加订单" });
  await dialog.getByLabel("用户邮箱", { exact: true }).fill(email);
  await dialog.getByRole("combobox", { name: "订阅套餐", exact: true }).selectOption({ label: planName });
  await dialog.getByRole("combobox", { name: "付款周期", exact: true }).selectOption("monthly");
  await dialog.getByLabel("支付金额（CNY）", { exact: true }).fill(amount);
  await dialog.getByRole("button", { name: "创建订单", exact: true }).click();
  await page.getByRole("combobox", { name: "订单状态", exact: true }).selectOption("0");
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(email);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: email }).filter({ hasText: amount }).filter({ hasText: "待支付" }).first();
  await expect(row).toBeVisible();
  const tradeNo = (await row.locator("strong.monospace").textContent())?.trim() ?? "";
  await row.getByRole("button", { name: `查看订单：${tradeNo}`, exact: true }).click();
  return tradeNo;
}

function planRow(page: Page, name: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
}

function orderRow(page: Page, tradeNo: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(tradeNo, { exact: true }) });
}
