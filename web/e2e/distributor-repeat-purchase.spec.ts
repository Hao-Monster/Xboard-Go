import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

interface DistributorOrderSnapshot {
  orderID: number;
  tradeNo: string;
  type: number;
  planID: number;
  period: string;
  subscriptionID: number;
  originalOrderID: number;
  subscriptionTradeNo: string;
  entitlement: {
    planID: number;
    transferEnable: number;
    usedTraffic: number;
    remainingTraffic: number;
    expiredAt: string | null;
    speedLimit: number;
    deviceLimit: number;
  };
}

test.use({ trace: "off", screenshot: "off", video: "off" });

test("repeat purchase creates a second independent distributor subscription without overwriting the first", async ({ page }) => {
  test.setTimeout(90_000);
  const expectedTransferEnable = 100 * 1024 ** 3;
  const unique = `${Date.now()}-${test.info().project.name}`;
  const distributorEmail = `repeat-distributor-${unique}@example.test`;
  const distributorPassword = "repeat-distributor-password-123";
  const planName = `E2E 再次购买套餐 ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createAdminUserFixture(page, {
    email: distributorEmail,
    password: distributorPassword,
    isDistributor: true,
    distributorName: `E2E 再次购买分销商 ${unique}`
  });
  await createPlan(page, planName);

  await logoutAndWait(page);
  await login(page, distributorEmail, distributorPassword);

  const card = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await card.getByRole("button", { name: "已确认，直接下单", exact: true }).click();

  const dialog = page.getByRole("dialog", { name: "订阅交付" });
  await expect(dialog.getByRole("img", { name: "客户订阅二维码" })).toBeVisible();
  const firstTradeNo = await deliveryTradeNo(dialog);
  const firstBeforeRepeat = orderByTradeNo(await listSafeOrderSnapshots(page), firstTradeNo);

  await dialog.getByRole("button", { name: "再次购买该套餐", exact: true }).click();
  await expect(dialog.locator("strong.monospace")).not.toHaveText(firstTradeNo);
  const secondTradeNo = await deliveryTradeNo(dialog);
  expect(secondTradeNo).not.toBe(firstTradeNo);

  const afterRepeat = await listSafeOrderSnapshots(page);
  const firstAfterRepeat = orderByTradeNo(afterRepeat, firstTradeNo);
  const second = orderByTradeNo(afterRepeat, secondTradeNo);

  expect(firstAfterRepeat).toEqual(firstBeforeRepeat);
  expect(afterRepeat.filter((item) => item.tradeNo === firstTradeNo || item.tradeNo === secondTradeNo)).toHaveLength(2);
  expect(firstAfterRepeat.type).toBe(1);
  expect(second.type).toBe(1);
  expect(second.orderID).not.toBe(firstAfterRepeat.orderID);
  expect(second.subscriptionID).not.toBe(firstAfterRepeat.subscriptionID);
  expect(firstAfterRepeat.originalOrderID).toBe(firstAfterRepeat.orderID);
  expect(second.originalOrderID).toBe(second.orderID);
  expect(firstAfterRepeat.subscriptionTradeNo).toBe(firstTradeNo);
  expect(second.subscriptionTradeNo).toBe(secondTradeNo);
  expect(second.planID).toBe(firstAfterRepeat.planID);
  expect(second.period).toBe(firstAfterRepeat.period);
  expect(second.entitlement.planID).toBe(firstAfterRepeat.entitlement.planID);
  expect(firstAfterRepeat.entitlement.transferEnable).toBe(expectedTransferEnable);
  expect(firstAfterRepeat.entitlement.usedTraffic).toBe(0);
  expect(firstAfterRepeat.entitlement.remainingTraffic).toBe(expectedTransferEnable);
  expect(second.entitlement.transferEnable).toBe(expectedTransferEnable);
  expect(second.entitlement.usedTraffic).toBe(0);
  expect(second.entitlement.remainingTraffic).toBe(expectedTransferEnable);
  expect(second.entitlement.expiredAt).not.toBeNull();
  expect(second.entitlement.speedLimit).toBe(firstAfterRepeat.entitlement.speedLimit);
  expect(second.entitlement.deviceLimit).toBe(firstAfterRepeat.entitlement.deviceLimit);

  await dialog.getByRole("button", { name: "关闭订阅交付", exact: true }).click();
  await page.getByRole("button", { name: "我的订单", exact: true }).click();
  await expect(newOrderRow(page, firstTradeNo)).toBeVisible();
  await expect(newOrderRow(page, secondTradeNo)).toBeVisible();
});

async function deliveryTradeNo(dialog: Locator): Promise<string> {
  const tradeNo = (await dialog.locator("strong.monospace").textContent())?.trim() ?? "";
  expect(tradeNo).toMatch(/^\d{25}$/);
  return tradeNo;
}

async function listSafeOrderSnapshots(page: Page): Promise<DistributorOrderSnapshot[]> {
  return page.evaluate(async () => {
    const response = await fetch("/api/v1/distributor/orders?page=1&page_size=10", { credentials: "same-origin" });
    if (!response.ok) throw new Error(`distributor order list failed with status ${response.status}`);
    const payload = await response.json() as {
      data?: {
        items?: Array<{
          order: { id: number; trade_no: string; type: number; plan_id: number; period: string };
          subscription: { id: number; original_order_id: number; trade_no: string };
          subscription_entitlement: {
            plan_id: number;
            transfer_enable: number;
            used_traffic: number;
            remaining_traffic: number;
            expired_at: string | null;
            speed_limit: number;
            device_limit: number;
          };
        }>;
      };
    };
    return (payload.data?.items ?? []).map((item) => ({
      orderID: item.order.id,
      tradeNo: item.order.trade_no,
      type: item.order.type,
      planID: item.order.plan_id,
      period: item.order.period,
      subscriptionID: item.subscription.id,
      originalOrderID: item.subscription.original_order_id,
      subscriptionTradeNo: item.subscription.trade_no,
      entitlement: {
        planID: item.subscription_entitlement.plan_id,
        transferEnable: item.subscription_entitlement.transfer_enable,
        usedTraffic: item.subscription_entitlement.used_traffic,
        remainingTraffic: item.subscription_entitlement.remaining_traffic,
        expiredAt: item.subscription_entitlement.expired_at,
        speedLimit: item.subscription_entitlement.speed_limit,
        deviceLimit: item.subscription_entitlement.device_limit
      }
    }));
  });
}

function orderByTradeNo(items: DistributorOrderSnapshot[], tradeNo: string): DistributorOrderSnapshot {
  const item = items.find((candidate) => candidate.tradeNo === tradeNo);
  if (item === undefined) throw new Error("expected distributor order is missing");
  return item;
}

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function createPlan(page: Page, name: string) {
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称", { exact: true }).fill(name);
  await dialog.getByLabel("流量（GiB）", { exact: true }).fill("100");
  await dialog.getByLabel("月付", { exact: true }).fill("100.00");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const row = page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
  await row.getByLabel("展示", { exact: true }).check();
  await row.getByLabel("销售", { exact: true }).check();
}

function newOrderRow(page: Page, tradeNo: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(tradeNo, { exact: true }) }).filter({ hasText: "新购" });
}
