import { expect, test, type Locator, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { adminEmail, adminPassword, expectLoginPage, logoutAndWait } from "./support";

test("coupon administration and discounted purchase work on every supported viewport", async ({ page }) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  const unique = `${Date.now()}-${test.info().project.name}`;
  const userEmail = `coupon-user-${unique}@example.test`;
  const userPassword = "coupon-user-password-123";
  const planName = `E2E 优惠券套餐 ${unique}`;
  const couponCode = `C${Date.now().toString(36).toUpperCase()}`;

  await login(page, adminEmail, adminPassword);
  await createUser(page, userEmail, userPassword);
  await createPlan(page, planName);
  await page.getByRole("button", { name: "优惠券管理", exact: true }).click();
  await page.getByRole("button", { name: "新增优惠券", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "新增优惠券" });
  await dialog.getByLabel("卷名称", { exact: true }).fill(`固定 12.34 ${unique}`);
  await dialog.getByLabel("卷码", { exact: true }).fill(couponCode);
  await dialog.getByLabel("优惠金额（元）", { exact: true }).fill("12.34");
  await dialog.getByLabel("可用总次数", { exact: true }).fill("2");
  await dialog.getByLabel("每用户可用次数", { exact: true }).fill("1");
  await dialog.getByLabel(planName, { exact: true }).check();
  await dialog.getByLabel("月付", { exact: true }).check();
  await dialog.getByRole("button", { name: "保存优惠券", exact: true }).click();
  const row = couponRow(page, couponCode);
  await expect(row).toContainText("¥12.34");
  await expect(row).toContainText("已启用");
  await row.getByRole("button", { name: `禁用 ${couponCode}`, exact: true }).click();
  await expect(row).toContainText("已禁用");
  await row.getByRole("button", { name: `启用 ${couponCode}`, exact: true }).click();
  await expect(row).toContainText("已启用");

  await page.getByPlaceholder("搜索名称或券码").fill(couponCode);
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "编辑优惠券" });
  await dialog.getByLabel("卷名称", { exact: true }).fill(`固定券已编辑 ${unique}`);
  await dialog.getByRole("button", { name: "保存优惠券", exact: true }).click();
  await expect(row).toContainText(`固定券已编辑 ${unique}`);

  await page.getByRole("button", { name: "新增优惠券", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "新增优惠券" });
  await dialog.getByLabel("卷名称", { exact: true }).fill("=CSV-SAFETY");
  await dialog.getByLabel("批量数量", { exact: true }).fill("2");
  await dialog.getByLabel("优惠金额（元）", { exact: true }).fill("1.00");
  const downloadPromise = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "生成并下载", exact: true }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("coupons.csv");
  const downloadPath = await download.path();
  if (downloadPath === null) throw new Error("coupon CSV download path is unavailable");
  const csv = await readFile(downloadPath, "utf8");
  expect(csv).toContain("'=CSV-SAFETY");

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "订阅套餐", exact: true }).click();
  const card = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await card.getByRole("button", { name: "立即订阅", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "配置订阅" });
  await dialog.getByPlaceholder("有优惠券？").fill(couponCode);
  await dialog.getByRole("button", { name: "验证", exact: true }).click();
  await expect(dialog.getByText("-¥12.34", { exact: true })).toBeVisible();
  await expect(dialog.getByText("¥987.66", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "下单", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await expect(dialog).toContainText("套餐标价¥1000.00");
  await expect(dialog).toContainText("优惠-¥12.34");
  await expect(dialog).toContainText("待支付¥987.66");

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

async function createPlan(page: Page, name: string) {
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

function planRow(page: Page, name: string): Locator { return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) }); }
function couponRow(page: Page, code: string): Locator { return page.getByRole("row").filter({ has: page.getByText(code, { exact: true }) }); }
