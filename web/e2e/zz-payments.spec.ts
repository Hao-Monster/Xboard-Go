import { createHmac } from "node:crypto";
import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

test("payment administration, exact checkout fees, signed callback, and duplicate notification work on every supported viewport", async ({ page }) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  const unique = `${Date.now()}-${test.info().project.name}`;
  const userEmail = `payment-user-${unique}@example.test`;
  const userPassword = "payment-user-password-123";
  const planName = `E2E 支付套餐 ${unique}`;
  const paymentName = `CoinPayments ${unique}`;
  const secret = `payment-secret-${unique}`;

  await login(page, adminEmail, adminPassword);
  await createUser(page, userEmail, userPassword);
  await createPaidPlan(page, planName);
  await page.getByRole("button", { name: "支付配置", exact: true }).click();
  await expect(page.getByRole("heading", { name: "支付配置", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "添加支付方式", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "添加支付方式" });
  await dialog.getByLabel("显示名称", { exact: true }).fill(paymentName);
  await dialog.getByLabel("百分比手续费（%）", { exact: true }).fill("2.5");
  await dialog.getByLabel("固定手续费（分）", { exact: true }).fill("123");
  await dialog.getByLabel("支付接口", { exact: true }).selectOption("CoinPayments");
  await dialog.getByLabel("Merchant ID", { exact: true }).fill("merchant-e2e");
  await dialog.getByLabel("IPN Secret", { exact: true }).fill(secret);
  await dialog.getByLabel("货币代码", { exact: true }).fill("CNY");
  await dialog.getByLabel("保存后立即启用", { exact: true }).check();
  await dialog.getByRole("button", { name: "确认", exact: true }).click();
  const methodRow = paymentRow(page, paymentName);
  await expect(methodRow).toContainText("CoinPayments");
  await expect(methodRow).toContainText("2.5% + 123 分");
  const notifyURL = (await methodRow.locator("code.payment-notify-url").textContent())?.trim() ?? "";
  expect(notifyURL).toMatch(/\/api\/v1\/guest\/payment\/notify\/CoinPayments\/[A-Za-z0-9]+$/);
  const callbackPath = new URL(notifyURL).pathname;

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "订阅套餐", exact: true }).click();
  const card = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await card.getByRole("button", { name: "立即订阅", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "配置订阅" });
  await dialog.getByRole("button", { name: "下单", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "订单详情" });
  const tradeNo = (await dialog.locator("strong.monospace").textContent())?.trim() ?? "";
  expect(tradeNo).toMatch(/^\d{25}$/);
  const selectedMethod = dialog.getByLabel(paymentName, { exact: true }).locator("..");
  await selectedMethod.getByRole("radio", { name: paymentName, exact: true }).check();
  await expect(selectedMethod.getByText("手续费 ¥26.23", { exact: true })).toBeVisible();
  await expect(dialog.getByText("¥1026.23", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "立即支付", exact: true }).click();
  await expect(dialog.getByRole("link", { name: "前往支付", exact: true })).toHaveAttribute("href", /coinpayments\.net\/index\.php/);
  await expect(dialog.getByText("应付 ¥1026.23", { exact: true })).toBeVisible();

  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await orderRow(page, tradeNo).getByRole("button", { name: `查看订单：${tradeNo}`, exact: true }).click();
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await dialog.getByLabel(paymentName, { exact: true }).check();
  await dialog.getByRole("button", { name: "关闭订单", exact: true }).click();
  await expect(dialog.getByRole("alert")).toContainText("支付订单已创建，不能取消");
  await dialog.getByRole("button", { name: "立即支付", exact: true }).click();
  await expect(dialog.getByRole("link", { name: "前往支付", exact: true })).toHaveAttribute("href", /coinpayments\.net\/index\.php/);

  const wrongBody = callbackBody(tradeNo, "1026.24", `wrong-${unique}`);
  const wrong = await page.request.post(callbackPath, { data: wrongBody, headers: callbackHeaders(secret, wrongBody) });
  expect(wrong.status()).toBe(409);
  expect(await wrong.text()).toBe("fail");
  const goodBody = callbackBody(tradeNo, "1026.23", `paid-${unique}`);
  const good = await page.request.post(callbackPath, { data: goodBody, headers: callbackHeaders(secret, goodBody) });
  expect(good.status()).toBe(200);
  expect(await good.text()).toBe("IPN OK");
  const duplicate = await page.request.post(callbackPath, { data: goodBody, headers: callbackHeaders(secret, goodBody) });
  expect(duplicate.status()).toBe(200);
  expect(await duplicate.text()).toBe("IPN OK");

  await expect(dialog.getByText("已完成", { exact: true })).toBeVisible({ timeout: 8_000 });
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await page.getByRole("button", { name: "刷新", exact: true }).click();
  await expect(orderRow(page, tradeNo)).toContainText("已完成");

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "支付配置", exact: true }).click();
  await paymentRow(page, paymentName).getByRole("button", { name: `禁用：${paymentName}`, exact: true }).click();
  await expect(paymentRow(page, paymentName)).toContainText("已禁用");

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

function callbackBody(tradeNo: string, amount: string, transactionID: string): string {
  return `merchant=merchant-e2e&status=100&amount1=${amount}&currency1=CNY&item_number=${tradeNo}&txn_id=${transactionID}`;
}

function callbackHeaders(secret: string, body: string): Record<string, string> {
  return { "Content-Type": "application/x-www-form-urlencoded", HMAC: createHmac("sha512", secret).update(body).digest("hex") };
}

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function createUser(page: Page, email: string, password: string) {
  await createAdminUserFixture(page, { email, password });
}

async function createPaidPlan(page: Page, name: string) {
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称", { exact: true }).fill(name);
  await dialog.getByLabel("流量（GiB）", { exact: true }).fill("100");
  await dialog.getByLabel("月付", { exact: true }).fill("1000.00");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const row = planRow(page, name);
  await row.getByLabel("展示", { exact: true }).check();
  await row.getByLabel("销售", { exact: true }).check();
}

function paymentRow(page: Page, name: string): Locator { return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) }); }
function planRow(page: Page, name: string): Locator { return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) }); }
function orderRow(page: Page, tradeNo: string): Locator { return page.getByRole("row").filter({ has: page.getByText(tradeNo, { exact: true }) }); }
