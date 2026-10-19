import { expect, test, type Page } from "@playwright/test";

import type { GiftCardCode, GiftCardTemplate, GiftCardUsage } from "../src/lib/api";
import { adminAPIPath, adminEmail, adminEntryPath, adminPassword, expectLoginPage } from "./support";

test("purchase codes keep confirmation identity, paginate history and fit narrow screens", async ({ page, baseURL }, testInfo) => {
  test.setTimeout(120_000);
  // These fixtures must never run against a shared panel.
  expect(new URL(baseURL ?? "").hostname).toMatch(/^(127\.0\.0\.1|localhost)$/);
  await page.setViewportSize(testInfo.project.name.includes("mobile") ? { width: 390, height: 844 } : { width: 1280, height: 900 });
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  const suffix = `${Date.now()}-${testInfo.project.name}`;
  const email = `gift-regression-${suffix}@example.test`;
  const password = "local-gift-regression-password";
  await login(page, adminEntryPath, adminEmail, adminPassword);
  await post(page, adminAPIPath("/api/v1/admin/users"), {
    email, password, is_admin: false, is_staff: false, group_id: null, transfer_enable: 0,
    expired_at: null, speed_limit: 0, device_limit: 0, banned: false
  }, 201);
  const plan = await post<{ id: number }>(page, adminAPIPath("/api/v1/admin/plans"), {
    name: `QA-GIFT-LONG-PLAN-${suffix}`, group_id: null, transfer_enable: 200, speed_limit: null,
    content: "", reset_traffic_method: null, capacity_limit: null, prices: { onetime: 100 }, device_limit: null, tags: []
  }, 201);
  const createTemplate = (name: string) => post<GiftCardTemplate>(page, adminAPIPath("/api/v1/admin/gift-card/templates"), {
    name, type: 4, status: true, rewards: { plan_id: plan.id, purchase_period: "onetime" }
  }, 201);
  const templateA = await createTemplate(`STALE-A-${suffix}`);
  const templateB = await createTemplate(`QA-GIFT-LONG-HISTORY-TEMPLATE-${suffix}`);
  const generate = (templateID: number, count: number) => post<GiftCardCode[]>(page, adminAPIPath("/api/v1/admin/gift-card/codes/generate"), { template_id: templateID, count, max_usage: 1 }, 201);
  const [codeA] = await generate(templateA.id, 1);
  const codes = await generate(templateB.id, 18);
  const adminCSRF = (await page.context().cookies()).find(cookie => cookie.name === "xboard_csrf")!.value;
  const logout = await page.request.post("/api/v1/auth/logout", { data: {}, headers: { "X-CSRF-Token": decodeURIComponent(adminCSRF) } });
  expect(logout.status()).toBe(204);
  await login(page, "/", email, password);

  const usages: GiftCardUsage[] = [];
  for (const card of codes.slice(0, 16)) {
    const result = await post<{ usage: GiftCardUsage }>(page, "/api/v1/user/gift-card/redeem", { code: card.code });
    usages.push(result.usage);
  }
  if (testInfo.project.name.includes("mobile")) await page.getByRole("button", { name: "切换导航", exact: true }).click();
  await page.getByRole("button", { name: "礼品卡", exact: true }).click();
  const table = page.getByRole("table", { name: "兑换记录", exact: true });
  const rows = table.locator("tbody tr");
  await expect(rows).toHaveCount(15);
  await expect(page.getByRole("button", { name: "上一页", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(rows).toHaveCount(1);
  await expect(table).toContainText(usages[0].order_trade_no!);
  await expect(page.getByRole("button", { name: "下一页", exact: true })).toBeDisabled();
  await expect(page.getByText("第 2 / 2 页 · 共 16 条")).toBeVisible();

  let releaseA!: () => void;
  let receivedA!: () => void;
  const held = new Promise<void>(resolve => { releaseA = resolve; });
  const received = new Promise<void>(resolve => { receivedA = resolve; });
  await page.route("**/api/v1/user/gift-card/check", async route => {
    if ((route.request().postDataJSON() as { code: string }).code !== codeA.code) return route.continue();
    const response = await route.fetch();
    receivedA();
    await held;
    await route.fulfill({ response });
  });
  const input = page.getByRole("textbox", { name: "礼品卡兑换码" });
  await input.fill(codeA.code);
  await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  await received;
  await input.fill(codes[16].code);
  const responseA = page.waitForResponse(response => response.url().endsWith("/gift-card/check") && (response.request().postDataJSON() as { code: string }).code === codeA.code);
  releaseA();
  await (await responseA).finished();
  // The controlled Promise/act component tests prove stale-response ordering;
  // this browser scenario checks the real request and redemption chain.
  await expect(page.getByRole("heading", { name: templateA.name, exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "确认兑换", exact: true })).toHaveCount(0);
  const submitted: string[] = [];
  page.on("request", request => {
    if (request.url().endsWith("/gift-card/redeem")) submitted.push((request.postDataJSON() as { code: string }).code);
  });
  await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  await expect(page.getByRole("heading", { name: templateB.name, exact: true })).toBeVisible();
  await page.getByRole("button", { name: "确认兑换", exact: true }).click();
  await expect(page.getByText("第 1 / 2 页 · 共 17 条")).toBeVisible();
  await expect(rows).toHaveCount(15);
  expect(submitted).toEqual([codes[16].code]);
  await expect(table).toContainText(`${codes[16].code.slice(0, 8)}****`);

  await page.getByRole("button", { name: "下一页", exact: true }).click();
  await expect(rows).toHaveCount(2);
  await input.fill(codes[17].code);
  await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  await page.getByRole("button", { name: "确认兑换", exact: true }).click();
  await expect(page.getByText("第 1 / 2 页 · 共 18 条")).toBeVisible();
  expect(submitted).toEqual([codes[16].code, codes[17].code]);
  const subscription = await page.request.get("/api/v1/subscription");
  expect(subscription.status()).toBe(200);
  expect((await subscription.json()).data).toMatchObject({ plan_id: plan.id, transfer_enable: 18 * 200 * 1024 ** 3, expired_at: null });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  await expect(table.locator('td[data-label="关联订单"]').first()).toBeVisible();
  if (testInfo.project.name.includes("mobile")) {
    expect(await table.locator('td[data-label="关联订单"]').first().evaluate(el => getComputedStyle(el, "::before").content)).toBe('"关联订单"');
  }
  await page.screenshot({ path: testInfo.outputPath("gift-card-fixed.png"), fullPage: true });
  await page.screenshot({ path: testInfo.outputPath("gift-card-viewport.png") });
  expect(errors).toEqual([]);
});

async function login(page: Page, path: string, email: string, password: string) {
  await page.goto(path);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  if (path === adminEntryPath) await expect(page.getByRole("navigation", { name: "管理端导航" })).toBeVisible();
  else await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

async function post<T>(page: Page, path: string, data: unknown, status = 200): Promise<T> {
  const csrf = (await page.context().cookies()).find(cookie => cookie.name === "xboard_csrf")?.value;
  expect(csrf).toBeTruthy();
  const response = await page.request.post(path, { data, headers: { "X-CSRF-Token": decodeURIComponent(csrf!) } });
  expect(response.status(), await response.text()).toBe(status);
  return (await response.json() as { data: T }).data;
}
