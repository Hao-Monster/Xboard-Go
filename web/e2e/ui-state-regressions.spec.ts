import { expect, test, type Page, type Route } from "@playwright/test";
import type { AdminOrderDetail, GuestConfig, Plan, Ticket } from "../src/lib/api";
import { deferred } from "../src/test/deferred";
import { adminEntryPath, adminSecurePath } from "./support";

// Real built App and browser interactions, with only the HTTP boundary replaced
// by synthetic fixtures. These checks do not claim backend or deployment coverage.
const initialPlan: Plan = {
  id: 11, group_id: null, transfer_enable: 100, name: "QA Browser Plan", speed_limit: 0, show: true, sort: 0,
  renew: true, content: "", reset_traffic_method: 1, capacity_limit: 0, prices: { monthly: 123 },
  sell: true, device_limit: 3, tags: [], users_count: 0, active_users_count: 0, capacity_users_count: 0, revision: 1,
  created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z"
};
const orderA: AdminOrderDetail = {
  id: 11, user_id: 42, plan_id: 11, payment_id: null, period: "monthly", trade_no: "QA-BROWSER-A",
  original_amount: 123, total_amount: 123, handling_amount: null, balance_amount: 0, surplus_credit: 0, surplus_amount: 0,
  type: 1, status: 0, surplus_order_ids: [], coupon_id: null, commission_status: 0, invite_user_id: null,
  actual_commission_balance: null, commission_rate: null, commission_auto_check: null, commission_balance: 0,
  discount_amount: 0, paid_at: null, callback_no: null, entitlement_expired_at_before: null,
  entitlement_expired_at_after: null, created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z",
  user_email: "qa-a@example.test", plan_name: initialPlan.name, invite_user: null, commission_log: [], subscribe_url: null
};
const orderB: AdminOrderDetail = { ...orderA, id: 12, trade_no: "QA-BROWSER-B", user_email: "qa-b@example.test" };
const ticket: Ticket = {
  id: 7, user_id: 42, subject: "QA Browser Ticket", level: 1, status: 0, reply_status: 0, messages: [],
  created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z"
};
const guest: GuestConfig = {
  app_name: "QA Xboard", app_description: null, app_url: null, tos_url: null, logo: null,
  is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
  captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
  recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
};

async function fixture(page: Page, baseURL: string | undefined, administrator = true) {
  if (!baseURL || !["127.0.0.1", "localhost", "[::1]"].includes(new URL(baseURL).hostname)) {
    throw new Error("UI state regression fixtures require an explicitly local baseURL");
  }
  const origin = new URL(baseURL).origin;
  const state = { plan: { ...initialPlan }, orderA: { ...orderA }, planReads: 0, planWrites: 0, ticketCloses: 0, unexpected: [] as string[], pageErrors: [] as string[] };
  page.on("pageerror", error => state.pageErrors.push(error.message));
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) {
      state.unexpected.push(`external ${url.origin}`);
      await route.abort();
      return;
    }
    const path = url.pathname.replace(`/api/v1/admin/${adminSecurePath}/`, "/api/v1/admin/");
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    if (path === "/api/v1/auth/session") {
      await success(route, { id: 42, email: "qa-browser@example.test", is_admin: administrator });
    } else if (path === "/api/v1/guest/comm/config") {
      await success(route, guest);
    } else if (path === "/api/v1/admin/plans") {
      state.planReads += 1;
      await success(route, [state.plan]);
    } else if (path === "/api/v1/admin/plans/11/state" && route.request().method() === "PATCH") {
      expect(route.request().postDataJSON()).toEqual({ revision: 1, show: true, sell: false, renew: true });
      state.plan = { ...state.plan, sell: false, revision: 2 };
      state.planWrites += 1;
      await success(route, state.plan);
    } else if (["/api/v1/admin/server-groups", "/api/v1/admin/distributors/options"].includes(path)) {
      await success(route, []);
    } else if (path === "/api/v1/admin/distributor-orders") {
      await success(route, { items: [], total: 0, page: 1, page_size: 20 });
    } else if (path === "/api/v1/admin/orders") {
      await success(route, { items: [state.orderA, orderB], total: 2, page: 1, page_size: 20 });
    } else if (path === `/api/v1/admin/orders/${orderA.trade_no}`) {
      await success(route, state.orderA);
    } else if (path === `/api/v1/admin/orders/${orderB.trade_no}`) {
      await success(route, orderB);
    } else if (["/api/v1/admin/tickets", "/api/v1/tickets"].includes(path)) {
      await success(route, { items: [ticket], total: 1, page: 1, page_size: 20 });
    } else if (["/api/v1/admin/tickets/7", "/api/v1/tickets/7"].includes(path)) {
      await success(route, ticket);
    } else if (path.endsWith("/tickets/7/close")) {
      state.ticketCloses += 1;
      await success(route, { ...ticket, status: 1 });
    } else {
      state.unexpected.push(`${route.request().method()} ${path}`);
      await route.fulfill({ status: 404, json: { status: "fail", error: { code: "fixture_unexpected", message: `Unexpected fixture request: ${path}` } } });
    }
  });
  return state;
}

function success(route: Route, data: unknown) {
  return route.fulfill({ status: 200, json: { status: "success", data } });
}

test("BUG-09 keeps persisted plan state when navigation returns within the prefetch TTL", async ({ page, baseURL }) => {
  const state = await fixture(page, baseURL);
  await page.clock.setFixedTime(new Date("2026-10-10T00:00:00Z"));
  await page.goto(`${adminEntryPath}#/user/ticket`);
  await expect(page.getByRole("heading", { name: "工单管理", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "订阅套餐", exact: true })).toBeVisible();
  const sell = page.getByRole("switch", { name: "新购", exact: true });
  await expect(sell).toBeChecked();
  await page.locator("label").filter({ has: sell }).click();
  await expect(sell).not.toBeChecked();
  await expect(sell).toBeEnabled();
  expect(state.planWrites).toBe(1);
  const readsBeforeReturn = state.planReads;
  await page.getByRole("button", { name: "工单管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "工单管理", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await expect(sell).not.toBeChecked();
  expect(state.planReads).toBeGreaterThan(readsBeforeReturn);
  expect(state.unexpected).toEqual([]);
  expect(state.pageErrors).toEqual([]);
});

for (const closeBeforeOldReply of [false, true]) {
  test(`BUG-10 ignores an older order detail response (closed=${closeBeforeOldReply})`, async ({ page, baseURL }) => {
    const state = await fixture(page, baseURL);
    const intercepted = deferred<Route>();
    await page.route(`**/orders/${orderA.trade_no}`, route => intercepted.resolve(route));
    await page.goto(`${adminEntryPath}#/finance/order`);
    await page.getByRole("button", { name: `查看订单：${orderA.trade_no}` }).click();
    const oldRoute = await intercepted.promise;
    await page.getByRole("button", { name: `查看订单：${orderB.trade_no}` }).click();
    const detail = page.getByRole("dialog", { name: "订单详情" });
    await expect(detail.getByText(orderB.trade_no, { exact: true })).toBeVisible();
    if (closeBeforeOldReply) await detail.getByRole("button", { name: "关闭", exact: true }).click();
    const response = page.waitForResponse(item => new URL(item.url()).pathname.endsWith(`/orders/${orderA.trade_no}`));
    await success(oldRoute, orderA);
    await (await response).finished();
    // Let the network completion and the subsequent React paint finish; no
    // timing-based response race is assumed by the fixture itself.
    await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
    if (closeBeforeOldReply) await expect(detail).toHaveCount(0);
    else await expect(detail.getByText(orderB.trade_no, { exact: true })).toBeVisible();
    expect(state.unexpected).toEqual([]);
    expect(state.pageErrors).toEqual([]);
  });
}

test("BUG-10 synchronizes a completed action without reopening its closed detail or stale prefetch", async ({ page, baseURL }) => {
  const state = await fixture(page, baseURL);
  const intercepted = deferred<Route>();
  await page.clock.setFixedTime(new Date("2026-10-10T00:00:00Z"));
  await page.route(`**/orders/${orderA.trade_no}/cancel`, route => intercepted.resolve(route));
  await page.goto(`${adminEntryPath}#/user/ticket`);
  await page.getByRole("button", { name: "订单管理", exact: true }).click();
  await page.getByRole("button", { name: `查看订单：${orderA.trade_no}` }).click();
  const detail = page.getByRole("dialog", { name: "订单详情" });
  await detail.getByRole("button", { name: "取消订单", exact: true }).click();
  const action = await intercepted.promise;
  expect(action.request().method()).toBe("POST");
  await detail.getByRole("button", { name: "关闭", exact: true }).click();
  state.orderA = { ...orderA, status: 2 };
  await success(action, state.orderA);
  await expect(detail).toHaveCount(0);
  const orderRow = page.getByRole("row", { name: new RegExp(orderA.trade_no) });
  await expect(orderRow).toContainText("已取消");
  await page.getByRole("button", { name: "工单管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "工单管理", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "订单管理", exact: true }).click();
  await expect(orderRow).toContainText("已取消");
  expect(state.unexpected).toEqual([]);
  expect(state.pageErrors).toEqual([]);
});

for (const administrator of [false, true]) {
  test(`BUG-11 Escape cancels only ticket confirmation and preserves draft (admin=${administrator})`, async ({ page, baseURL }) => {
    const state = await fixture(page, baseURL, administrator);
    await page.goto(administrator ? `${adminEntryPath}#/user/ticket` : "/#/ticket");
    await page.getByRole("button", { name: `查看工单：${ticket.subject}` }).click();
    const detail = page.getByRole("dialog", { name: "工单详情" });
    await detail.getByLabel("回复内容").fill("QA unsent browser reply");
    const close = detail.getByRole("button", { name: "关闭工单", exact: true });
    await close.click();
    await expect(page.getByRole("dialog", { name: "关闭工单", exact: true })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog", { name: "关闭工单", exact: true })).toHaveCount(0);
    await expect(detail).toBeVisible();
    await expect(detail.getByLabel("回复内容")).toHaveValue("QA unsent browser reply");
    await expect(close).toBeFocused();
    expect(state.ticketCloses).toBe(0);
    expect(state.unexpected).toEqual([]);
    expect(state.pageErrors).toEqual([]);
  });
}
