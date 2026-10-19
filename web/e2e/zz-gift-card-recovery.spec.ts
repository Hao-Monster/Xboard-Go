import { expect, test, type Page } from "@playwright/test";

import type { GiftCardCode, GiftCardRedeemResult, GiftCardUsagePage } from "../src/lib/api";
import { adminAPIPath, adminEmail, adminEntryPath, adminPassword, expectLoginPage } from "./support";

const historyURL = "**/api/v1/user/gift-card/history?*";
const redeemURL = "**/api/v1/user/gift-card/redeem";
const checkURL = "**/api/v1/user/gift-card/check";
const bytes = 200 * 1024 ** 3;

test.beforeEach(async ({ baseURL, page }, info) => {
  expect(new URL(baseURL ?? "").hostname).toMatch(/^(127\.0\.0\.1|localhost)$/);
  await page.setViewportSize(info.project.name.includes("mobile") ? { width: 390, height: 844 } : { width: 1280, height: 900 });
});

for (const fault of ["before-commit", "after-commit"] as const) {
  test(`PF-10/11 ${fault} failure recovers with exactly one entitlement and order`, async ({ page }, info) => {
    test.setTimeout(120_000);
    const fixture = await setup(page, `${fault}-${info.project.name}`);
    await openGift(page);
    await expect(page.getByText("暂无兑换记录")).toBeVisible();
    await query(page, fixture.code);
    let firstResult: GiftCardRedeemResult | undefined;
    let attempts = 0;
    page.on("request", request => { if (request.url().endsWith("/gift-card/redeem")) attempts++; });
    await page.route(redeemURL, async route => {
      if (fault === "after-commit") {
        const response = await route.fetch();
        expect(response.status()).toBe(200);
        firstResult = (await response.json() as { data: GiftCardRedeemResult }).data;
      }
      await route.abort("failed");
    }, { times: 1 });
    await page.getByRole("button", { name: "确认兑换", exact: true }).click();
    await expect(page.getByRole("alert")).toBeVisible();
    await expect(page.getByText(/兑换成功/)).toHaveCount(0);
    await expect(page.getByRole("textbox", { name: "礼品卡兑换码" })).toBeEnabled();
    await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toBeEnabled();
    await assertLedger(page, fault === "before-commit" ? 0 : 1, fixture.planID);
    const retryResponse = page.waitForResponse(response => response.url().endsWith("/gift-card/redeem"));
    await page.getByRole("button", { name: "确认兑换", exact: true }).click();
    const retried = (await (await retryResponse).json() as { data: GiftCardRedeemResult }).data;
    await expect(page.getByText(/兑换成功/)).toBeVisible();
    await expect(page.getByRole("table", { name: "兑换记录" })).toContainText(retried.usage.order_trade_no!);
    expect(attempts).toBe(2);
    if (fault === "after-commit") {
      expect(firstResult).toBeDefined();
      expect(retried.usage.id).toBe(firstResult!.usage.id);
      expect(retried.usage.order_trade_no).toBe(firstResult!.usage.order_trade_no);
    }
    await assertLedger(page, 1, fixture.planID);
    await expect(page.getByRole("textbox", { name: "礼品卡兑换码" })).toHaveValue("");
    await query(page, fixture.code, false);
    await expect(page.getByRole("alert")).toContainText("已无剩余次数");
    await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toHaveCount(0);
    await page.screenshot({ path: info.outputPath(`${fault}.png`) });
  });
}

test("PF-01/02/03/09/12/13/20 history and query recovery preserve success and keyboard access", async ({ page }, info) => {
  test.setTimeout(120_000);
  const fixture = await setup(page, `history-${info.project.name}`);
  await page.route(historyURL, route => route.abort("failed"), { times: 1 });
  await openGift(page);
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByText("暂无兑换记录")).toHaveCount(0);
  await page.getByRole("textbox", { name: "礼品卡兑换码" }).focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "重试加载记录" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByText("暂无兑换记录")).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  const input = page.getByRole("textbox", { name: "礼品卡兑换码" });
  let checks = 0;
  let redeems = 0;
  page.on("request", request => {
    if (request.url().endsWith("/gift-card/check")) checks++;
    if (request.url().endsWith("/gift-card/redeem")) redeems++;
  });
  await expect(page.getByRole("button", { name: "查询奖励", exact: true })).toBeDisabled();
  await input.pressSequentially("SHORT12");
  await input.press("Enter");
  expect(await input.evaluate(element => (element as HTMLInputElement).validity.tooShort)).toBe(true);
  expect(checks).toBe(0);
  await input.fill("INVALID!CODE");
  await input.press("Enter");
  expect(await input.evaluate(element => (element as HTMLInputElement).validity.patternMismatch)).toBe(true);
  expect(checks).toBe(0);
  await page.route(checkURL, route => route.abort("failed"), { times: 1 });
  await input.fill(fixture.code.toLowerCase());
  await input.press("Enter");
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toHaveCount(0);
  await input.press("Enter");
  await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toBeVisible();
  await expect(input).toHaveValue(fixture.code);
  expect(redeems).toBe(0);
  await assertLedger(page, 0, fixture.planID);
  await page.route(historyURL, route => route.abort("failed"), { times: 1 });
  await input.focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "查询奖励", exact: true })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByText(/兑换成功/)).toBeVisible();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(input).toHaveValue("");
  await expect(input).toBeEnabled();
  expect(redeems).toBe(1);
  await input.focus();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "重试加载记录" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("table", { name: "兑换记录" })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(redeems).toBe(1);
  await assertLedger(page, 1, fixture.planID);
  await page.reload();
  await openGift(page);
  await expect(page.getByRole("table", { name: "兑换记录" }).locator("tbody tr")).toHaveCount(1);
  await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  await page.screenshot({ path: info.outputPath("history-recovered.png") });
});

async function setup(page: Page, label: string) {
  const suffix = `${Date.now()}-${label}`;
  const email = `postfix-${suffix}@example.test`;
  const password = "local-gift-functional-password";
  await login(page, adminEntryPath, adminEmail, adminPassword);
  await post(page, adminAPIPath("/api/v1/admin/users"), { email, password, is_admin: false, is_staff: false, group_id: null, transfer_enable: 0, expired_at: null, speed_limit: 0, device_limit: 0, banned: false }, 201);
  const plan = await post<{ id: number }>(page, adminAPIPath("/api/v1/admin/plans"), { name: `功能测试200GB-${suffix}`, group_id: null, transfer_enable: 200, speed_limit: null, content: "", reset_traffic_method: null, capacity_limit: null, prices: { onetime: 100 }, device_limit: null, tags: [] }, 201);
  const template = await post<{ id: number }>(page, adminAPIPath("/api/v1/admin/gift-card/templates"), { name: `功能恢复-${suffix}`, type: 4, status: true, rewards: { plan_id: plan.id, purchase_period: "onetime" } }, 201);
  const [card] = await post<GiftCardCode[]>(page, adminAPIPath("/api/v1/admin/gift-card/codes/generate"), { template_id: template.id, count: 1, max_usage: 1 }, 201);
  const csrf = (await page.context().cookies()).find(cookie => cookie.name === "xboard_csrf")!.value;
  expect((await page.request.post("/api/v1/auth/logout", { data: {}, headers: { "X-CSRF-Token": decodeURIComponent(csrf) } })).status()).toBe(204);
  await login(page, "/", email, password);
  return { code: card.code, planID: plan.id };
}
async function login(page: Page, path: string, email: string, password: string) {
  await page.goto(path);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  if (path === adminEntryPath) await expect(page.getByRole("navigation", { name: "管理端导航" })).toBeVisible();
  else await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}
async function openGift(page: Page) {
  const navigation = page.getByRole("button", { name: "礼品卡", exact: true });
  if (!await navigation.isVisible()) await page.getByRole("button", { name: "切换导航", exact: true }).click();
  await navigation.click();
  await expect(page.getByRole("heading", { name: "礼品卡兑换", exact: true })).toBeVisible();
}
async function query(page: Page, code: string, canRedeem = true) {
  await page.getByRole("textbox", { name: "礼品卡兑换码" }).fill(code);
  await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  if (canRedeem) await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toBeVisible();
}
async function post<T>(page: Page, path: string, data: unknown, status = 200): Promise<T> {
  const csrf = (await page.context().cookies()).find(cookie => cookie.name === "xboard_csrf")!.value;
  const response = await page.request.post(path, { data, headers: { "X-CSRF-Token": decodeURIComponent(csrf) } });
  expect(response.status()).toBe(status);
  return (await response.json() as { data: T }).data;
}
async function get<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status()).toBe(200);
  return (await response.json() as { data: T }).data;
}
async function assertLedger(page: Page, count: number, planID: number) {
  const history = await get<GiftCardUsagePage>(page, "/api/v1/user/gift-card/history?page=1&page_size=100");
  const orders = await get<{ trade_no: string }[]>(page, "/api/v1/orders?limit=100");
  const subscription = await get<{ transfer_enable: number; plan_id: number | null; expired_at: string | null }>(page, "/api/v1/subscription");
  expect(history.total).toBe(count);
  expect(history.items).toHaveLength(count);
  expect(orders).toHaveLength(count);
  expect(subscription.transfer_enable).toBe(count * bytes);
  if (count > 0) {
    expect(orders[0].trade_no).toBe(history.items[0].order_trade_no);
    expect(subscription.plan_id).toBe(planID);
    expect(subscription.expired_at).toBeNull();
  }
}
