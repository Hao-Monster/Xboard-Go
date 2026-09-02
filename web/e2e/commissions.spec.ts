import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

interface CommissionSettings {
  revision: number;
  invite_commission: number;
  commission_first_time_enable: boolean;
  commission_auto_check_enable: boolean;
  commission_withdraw_limit: number;
  commission_withdraw_method: string[];
  withdraw_close_enable: boolean;
  commission_distribution_enable: boolean;
  commission_distribution_l1: number;
  commission_distribution_l2: number;
  commission_distribution_l3: number;
}

interface InvitationSummary {
  valid_commission: number;
  pending_commission: number;
  commission_rate: number;
  commission_distribution_enabled: boolean;
  commission_distribution_rates: number[];
  available_commission: number;
}

test("administrator commission rules drive the invited order, history, and positive transfer flow", async ({ page }, testInfo) => {
  test.setTimeout(180_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const ownerEmail = `commission-owner-${unique}@example.test`;
  const buyerEmail = `commission-buyer-${unique}@example.test`;
  const password = "commission-e2e-password-123";
  const planName = `佣金测试套餐 ${unique}`;
  let original: CommissionSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword);
    original = await getCommissionSettings(page);
    await page.getByRole("button", { name: "佣金设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "邀请佣金设置" })).toBeVisible();
    await page.getByLabel("全局邀请佣金比例（%）").fill("20");
    await page.getByRole("checkbox", { name: "仅首次有效订单返佣" }).check();
    await page.getByRole("checkbox", { name: "自动确认到期佣金" }).check();
    const withdrawLimit = page.locator("label").filter({ hasText: "最低提现金额" }).locator("input");
    const withdrawMethods = page.locator("label").filter({ hasText: "允许的提现方式（每行一个）" }).locator("textarea");
    await withdrawLimit.fill("125.25");
    await withdrawMethods.fill("支付宝\nUSDT\n银行转账");
    await page.getByRole("checkbox", { name: "佣金直接计入账户余额" }).uncheck();
    await page.getByRole("checkbox", { name: "启用三级分佣" }).check();
    await page.getByLabel("一级分佣比例（%）").fill("50");
    await page.getByLabel("二级分佣比例（%）").fill("30");
    await page.getByLabel("三级分佣比例（%）").fill("20");
    await expect(page.getByText("当前用户侧有效比例：10% / 6% / 4%", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "保存佣金设置" }).click();
    await expect(page.getByRole("status")).toHaveText("佣金设置已保存");
    await page.reload();
    await page.getByRole("button", { name: "佣金设置", exact: true }).click();
    await expect(page.getByLabel("全局邀请佣金比例（%）")).toHaveValue("20");
    await expect(page.locator("label").filter({ hasText: "最低提现金额" }).locator("input")).toHaveValue("125.25");
    await expect(page.locator("label").filter({ hasText: "允许的提现方式（每行一个）" }).locator("textarea")).toHaveValue("支付宝\nUSDT\n银行转账");
    await expect(page.getByRole("checkbox", { name: "启用三级分佣" })).toBeChecked();
    await expect(page.getByText("当前用户侧有效比例：10% / 6% / 4%", { exact: true })).toBeVisible();

    await createAdminUserFixture(page, { email: ownerEmail, password });
    await logoutAndWait(page);
    await login(page, ownerEmail, password);
    await page.getByRole("button", { name: "我的邀请", exact: true }).click();
    await expect(page.getByRole("heading", { name: "我的邀请" })).toBeVisible();
    await expect(page.getByText("10% / 6% / 4%", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "生成邀请码", exact: true }).click();
    const code = (await page.locator("code.monospace").textContent())?.trim() ?? "";
    expect(code).toMatch(/^[A-Za-z0-9]{8}$/);

    await logoutAndWait(page);
    await login(page, adminEmail, adminPassword);
    await createAdminUserFixture(page, { email: buyerEmail, password, inviteUserEmail: ownerEmail });
    await createFreePlan(page, planName);
    await page.getByRole("button", { name: "订单管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "订单管理", exact: true })).toBeVisible();
    const tradeNo = await assignOrder(page, buyerEmail, planName, "100.00");
    expect(tradeNo).toMatch(/^[0-9a-f]{32}$/);
    const orderDialog = page.getByRole("dialog", { name: "订单详情" });
    await orderDialog.getByRole("button", { name: "标记已支付并开通", exact: true }).click();
    await expect(orderDialog.getByText("已完成", { exact: true })).toBeVisible();
    await expect(orderDialog.getByText(ownerEmail, { exact: true })).toBeVisible();
    await orderDialog.getByRole("button", { name: "设为发放中", exact: true }).click();
    await expect(orderDialog.getByText("发放中", { exact: true })).toBeVisible();
    await orderDialog.getByRole("button", { name: "关闭", exact: true }).click();
    await expect(orderDialog).toBeHidden();

    await logoutAndWait(page);
    await login(page, ownerEmail, password);
    await expect.poll(async () => (await getInvitationSummary(page)).available_commission, {
      message: "commission scheduler did not credit the inviter", timeout: 75_000, intervals: [1_000, 2_000, 5_000]
    }).toBe(1_000);
    await page.getByRole("button", { name: "我的邀请", exact: true }).click();
    await expect(page.getByText("10% / 6% / 4%", { exact: true })).toBeVisible();
    await expect(page.getByText(tradeNo, { exact: true })).toBeVisible();
    await expect(page.getByText("¥100.00", { exact: true })).toBeVisible();
    await expect(page.getByText("¥10.00", { exact: true }).first()).toBeVisible();
    await page.getByLabel("划转金额（CNY）").fill("10.00");
    await page.getByRole("button", { name: "佣金划转余额", exact: true }).click();
    await expect(page.getByRole("status")).toHaveText("操作成功");
    const availableCommissionMetric = page.locator(".invitation-overview .overview-metric")
      .filter({ hasText: "可用佣金" })
      .first();
    await expect(availableCommissionMetric.locator("strong")).toHaveText("¥0.00");
    const transferred = await getInvitationSummary(page);
    expect(transferred).toMatchObject({
      valid_commission: 1_000, pending_commission: 0, commission_rate: 20,
      commission_distribution_enabled: true, commission_distribution_rates: [10, 6, 4], available_commission: 0
    });
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(1);
  } finally {
    if (original !== null) {
      await ensureAdministrator(page);
      const current = await getCommissionSettings(page);
      const restored = await adminRequest(page, "/api/v1/admin/commission-settings", "PUT", {
        revision: current.revision,
        invite_commission: original.invite_commission,
        commission_first_time_enable: original.commission_first_time_enable,
        commission_auto_check_enable: original.commission_auto_check_enable,
        commission_withdraw_limit: original.commission_withdraw_limit,
        commission_withdraw_method: original.commission_withdraw_method,
        withdraw_close_enable: original.withdraw_close_enable,
        commission_distribution_enable: original.commission_distribution_enable,
        commission_distribution_l1: original.commission_distribution_l1,
        commission_distribution_l2: original.commission_distribution_l2,
        commission_distribution_l3: original.commission_distribution_l3
      });
      expect(restored.status, restored.body).toBe(200);
    }
  }
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible({ timeout: 60_000 });
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出", exact: true }).count() > 0) await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
}

async function getCommissionSettings(page: Page): Promise<CommissionSettings> {
  return decodeData<CommissionSettings>(await adminRequest(page, "/api/v1/admin/commission-settings", "GET"));
}

async function getInvitationSummary(page: Page): Promise<InvitationSummary> {
  return decodeData<InvitationSummary>(await page.evaluate(async () => {
    const response = await fetch("/api/v1/invitations", { credentials: "same-origin" });
    return { status: response.status, body: await response.text() };
  }));
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

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: adminAPIPath(path), method, body });
}
