import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

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

test("M1 commission withdrawal freezes, reveals, approves, and pays through the UI", async ({ page }, testInfo) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `m1-withdrawal-${unique}@example.test`;
  const password = "m1-withdrawal-password-123";
  const account = `wallet-${unique}-5678`;
  const paymentReference = `M1-PAY-${unique}`;
  let original: CommissionSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword);
    original = await getCommissionSettings(page);
    const configured = await saveCommissionSettings(page, original, {
      commission_withdraw_limit: 1,
      commission_withdraw_method: ["USDT"]
    });
    expect(configured.commission_withdraw_limit).toBe(1);

    await createAdminUserFixture(page, { email, password });
    await page.getByRole("button", { name: "用户管理", exact: true }).click();
    await filterUser(page, email);
    await page.getByRole("button", { name: `编辑用户：${email}` }).click();
    const editor = page.getByRole("dialog", { name: "编辑用户" });
    await editor.getByLabel("佣金余额（CNY）", { exact: true }).fill("20.00");
    const updateResponsePromise = page.waitForResponse((response) => response.request().method() === "PATCH" && /\/api\/v1\/admin\/users\/\d+$/.test(new URL(response.url()).pathname));
    await editor.getByRole("button", { name: "保存", exact: true }).click();
    const updateResponse = await updateResponsePromise;
    expect(updateResponse.status(), await updateResponse.text()).toBe(200);

    await logoutAndWait(page);
    await login(page, email, password);
    await page.getByRole("button", { name: "我的邀请", exact: true }).click();
    const withdrawalSection = page.locator("section[aria-labelledby='commission-withdrawal-heading']");
    await expect(withdrawalSection.getByRole("heading", { name: "佣金提现", exact: true })).toBeVisible();
    await withdrawalSection.locator("select").selectOption("USDT");
    await withdrawalSection.locator("input").fill(account);
    await withdrawalSection.getByRole("button", { name: "提交提现申请", exact: true }).click();
    await expect(page.getByText("提现申请已提交", { exact: true })).toBeVisible();
    const history = page.locator("section[aria-labelledby='withdrawal-history-heading']");
    await expect(history.getByRole("row").filter({ hasText: "USDT" }).filter({ hasText: "待审核" })).toBeVisible();

    await logoutAndWait(page);
    await login(page, adminEmail, adminPassword);
    await page.getByRole("button", { name: "佣金设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "提现审核", exact: true })).toBeVisible();
    const row = page.getByRole("row").filter({ hasText: email }).first();
    await expect(row).toContainText("待审核");
    await row.getByRole("button", { name: "查看账户", exact: true }).click();
    await expect(page.getByRole("status").filter({ hasText: account })).toBeVisible();

    page.once("dialog", (dialog) => void dialog.accept());
    await row.getByRole("button", { name: "批准", exact: true }).click();
    await expect(row).toContainText("已批准");
    page.once("dialog", (dialog) => void dialog.accept(paymentReference));
    await row.getByRole("button", { name: "确认支付", exact: true }).click();
    await expect(row).toContainText("已支付");
    await expect(row.getByRole("button", { name: "拒绝", exact: true })).toHaveCount(0);
  } finally {
    if (original !== null) {
      await ensureAdministrator(page);
      const current = await getCommissionSettings(page);
      const restored = await saveCommissionSettings(page, current, original);
      expect(restored.commission_withdraw_limit).toBe(original.commission_withdraw_limit);
    }
  }
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

test("M1 user deletion preflight disables access and recovery keeps old credentials revoked", async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `m1-deletion-${unique}@example.test`;
  const password = "m1-deletion-password-123";

  await login(page, adminEmail, adminPassword);
  await createAdminUserFixture(page, { email, password });
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await filterUser(page, email);
  let row = page.getByRole("row").filter({ hasText: email }).first();
  await row.getByRole("button", { name: `用户操作：${email}` }).click();
  await page.getByRole("dialog", { name: "用户操作" }).getByRole("button", { name: "申请删除用户", exact: true }).click();
  let lifecycleDialog = page.getByRole("alertdialog", { name: "删除用户影响确认" });
  await expect(lifecycleDialog.getByText(/30 天恢复期/)).toBeVisible();
  const deleteButton = lifecycleDialog.getByRole("button", { name: "确认申请删除", exact: true });
  await expect(deleteButton).toBeEnabled();
  await deleteButton.click();
  await expect(lifecycleDialog).toBeHidden();
  row = page.getByRole("row").filter({ hasText: email }).first();
  await expect(row).toContainText("待删除");

  await logoutAndWait(page);
  await expectLoginFailure(page, email, password);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await filterUser(page, email);
  row = page.getByRole("row").filter({ hasText: email }).first();
  await row.getByRole("button", { name: `用户操作：${email}` }).click();
  await page.getByRole("dialog", { name: "用户操作" }).getByRole("button", { name: "恢复用户", exact: true }).click();
  lifecycleDialog = page.getByRole("alertdialog", { name: "恢复用户" });
  await expect(lifecycleDialog).toContainText("已轮换的密码、会话、访问令牌、UUID 和订阅令牌不会恢复");
  await lifecycleDialog.getByRole("button", { name: "确认恢复", exact: true }).click();
  await expect(lifecycleDialog).toBeHidden();
  row = page.getByRole("row").filter({ hasText: email }).first();
  await expect(row).toContainText("正常");

  await logoutAndWait(page);
  await expectLoginFailure(page, email, password);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  if (await page.getByRole("button", { name: "退出", exact: true }).count() === 0) {
    await page.goto("/");
    await expectLoginPage(page);
  }
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible({ timeout: 60_000 });
}

async function ensureAdministrator(page: Page) {
  if (await page.getByRole("button", { name: "退出", exact: true }).count() > 0) await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
}

async function expectLoginFailure(page: Page, email: string, password: string) {
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toHaveCount(0);
}

async function filterUser(page: Page, email: string) {
  await page.getByRole("searchbox", { name: "邮箱前缀", exact: true }).fill(email);
  await page.getByRole("button", { name: "查询用户", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: email }).first()).toBeVisible();
}

async function getCommissionSettings(page: Page): Promise<CommissionSettings> {
  return decodeData<CommissionSettings>(await adminRequest(page, "/api/v1/admin/commission-settings", "GET"));
}

async function saveCommissionSettings(page: Page, current: CommissionSettings, overrides: Partial<CommissionSettings>): Promise<CommissionSettings> {
  return decodeData<CommissionSettings>(await adminRequest(page, "/api/v1/admin/commission-settings", "PUT", {
    invite_commission: overrides.invite_commission ?? current.invite_commission,
    commission_first_time_enable: overrides.commission_first_time_enable ?? current.commission_first_time_enable,
    commission_auto_check_enable: overrides.commission_auto_check_enable ?? current.commission_auto_check_enable,
    commission_withdraw_limit: overrides.commission_withdraw_limit ?? current.commission_withdraw_limit,
    commission_withdraw_method: overrides.commission_withdraw_method ?? current.commission_withdraw_method,
    withdraw_close_enable: overrides.withdraw_close_enable ?? current.withdraw_close_enable,
    commission_distribution_enable: overrides.commission_distribution_enable ?? current.commission_distribution_enable,
    commission_distribution_l1: overrides.commission_distribution_l1 ?? current.commission_distribution_l1,
    commission_distribution_l2: overrides.commission_distribution_l2 ?? current.commission_distribution_l2,
    commission_distribution_l3: overrides.commission_distribution_l3 ?? current.commission_distribution_l3,
    revision: current.revision
  }));
}

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json",
        "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path, method, body });
}
