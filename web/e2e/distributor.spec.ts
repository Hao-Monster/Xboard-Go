import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage, logoutAndWait } from "./support";

test("distributor purchase, delivery, renewal, administration, and settlement work on every supported viewport", async ({ page }) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  const unique = `${Date.now()}-${test.info().project.name}`;
  const distributorEmail = `distributor-${unique}@example.test`;
  const distributorPassword = "distributor-password-123";
  const distributorName = `E2E 分销商 ${unique}`;
  const planName = `E2E 分销套餐 ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createDistributor(page, distributorEmail, distributorPassword, distributorName);
  await createPlan(page, planName);

  await logoutAndWait(page);
  await login(page, distributorEmail, distributorPassword);
  await expect(page.getByRole("heading", { name: "购买订阅", exact: true })).toBeVisible();
  for (const item of ["购买订阅", "我的订单", "我的邀请", "使用文档", "客户端下载"]) await expect(page.getByRole("button", { name: item, exact: true })).toBeVisible();
  for (const item of ["我的订阅", "我的工单", "礼品卡"]) await expect(page.getByRole("button", { name: item, exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: "我的邀请", exact: true }).click();
  for (const label of ["已邀请用户", "有效佣金", "确认中佣金", "佣金比例", "可用佣金"]) await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "佣金记录", exact: true })).toBeVisible();
  await expect(page.getByText("暂无数据", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "生成邀请码", exact: true }).click();
  await expect(page.getByRole("status")).toHaveText("邀请码已生成");
  await expect(page.locator("code.monospace")).toHaveText(/^[A-Za-z0-9]{8}$/);
  await page.getByLabel("划转金额（CNY）", { exact: true }).fill("0.01");
  await page.getByRole("button", { name: "佣金划转余额", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("佣金余额不足");
  await page.getByRole("button", { name: "切换语言", exact: true }).click();
  await expect(page.getByRole("heading", { name: "My Invitations", exact: true })).toBeVisible();
  await expect(page.getByText("Valid commission", { exact: true }).first()).toBeVisible();
  await page.getByRole("button", { name: "切换浅色主题", exact: true }).click();
  await expect.poll(() => page.locator("html").getAttribute("data-distributor-theme")).toBe("light");
  await page.getByRole("button", { name: "切换语言", exact: true }).click();
  await page.getByRole("button", { name: "购买订阅", exact: true }).click();

  const card = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await expect(card).toContainText("¥100.00");
  await card.getByRole("button", { name: "已确认，直接下单", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "订阅交付" });
  await expect(dialog.getByRole("img", { name: "客户订阅二维码" })).toBeVisible();
  const tradeNo = (await dialog.locator("strong.monospace").textContent())?.trim() ?? "";
  expect(tradeNo).toMatch(/^\d{25}$/);
  await expect(dialog).toContainText("尚未绑定设备");
  await dialog.getByRole("button", { name: "关闭订阅交付" }).click();

  await page.getByRole("button", { name: "我的订单", exact: true }).click();
  let row = distributorOrderRow(page, tradeNo);
  await expect(row).toContainText("新购");
  await expect(row).toContainText("未结算");
  await row.getByRole("button", { name: "查看权益", exact: true }).click();
  await expect(page.getByText("当前订阅权益", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: "订阅二维码", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "订阅二维码" });
  await expect(dialog.getByRole("img", { name: "订阅二维码" })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭订阅二维码" }).click();

  await row.getByRole("button", { name: "续费", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "续费现有订阅" });
  await dialog.getByLabel("续费周期").selectOption("quarterly");
  await dialog.getByRole("button", { name: "确认续费", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "续费现有订阅" })).toBeHidden();
  await expect(page.getByRole("row").filter({ hasText: `原始订单：${tradeNo}` })).toContainText("¥270.00");

  await page.getByPlaceholder("订单号或客户名称").fill(tradeNo);
  await page.getByRole("button", { name: "搜索", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: tradeNo })).toHaveCount(2);
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出 Excel", exact: true }).click();
  expect((await download).suggestedFilename()).toMatch(/\.xlsx$/);

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "分销管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "分销管理", exact: true })).toBeVisible();
  await page.getByLabel("分销商", { exact: true }).selectOption({ label: `${distributorName} · ${distributorEmail}` });
  row = page.getByRole("row").filter({ hasText: tradeNo }).filter({ hasText: "新购" });
  await expect(row).toContainText(distributorName);
  await row.getByRole("button", { name: `分销订单详情：${tradeNo}`, exact: true }).click();
  dialog = page.getByRole("dialog", { name: "分销订单详情" });
  await expect(dialog.getByText("订阅地址", { exact: true }).locator("..").locator("strong")).toContainText("/s/");
  await dialog.getByLabel("内部备注").fill("E2E 已核对");
  await dialog.getByRole("button", { name: "保存备注", exact: true }).click();
  await expect(dialog.getByRole("status")).toHaveText("设置已保存");
  await dialog.getByLabel("总流量（字节）").fill("214748364800");
  await dialog.getByLabel("限速（Mbps）").fill("300");
  await dialog.getByLabel("设备限制").fill("5");
  await dialog.getByRole("button", { name: "保存权益", exact: true }).click();
  await expect(dialog.getByRole("status")).toHaveText("设置已保存");
  await dialog.getByLabel("HWID 上限").fill("2");
  await dialog.getByRole("button", { name: "保存 HWID 设置", exact: true }).click();
  await expect(dialog.getByRole("status")).toHaveText("设置已保存");
  await dialog.getByRole("button", { name: "关闭分销订单详情" }).click();

  await page.getByRole("button", { name: "结算所选分销商", exact: true }).click();
  dialog = page.getByRole("dialog", { name: "分销订单结算" });
  await expect(dialog).toContainText("订单数2");
  await expect(dialog).toContainText("¥370.00");
  await dialog.getByRole("button", { name: "确认结算", exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.getByLabel("结算状态", { exact: true }).selectOption("1");
  await expect(page.getByRole("row").filter({ hasText: tradeNo }).filter({ hasText: "新购" })).toContainText("已结算");

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

async function createDistributor(page: Page, email: string, password: string, name: string) {
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("邮箱", { exact: true }).fill(email);
  await dialog.getByLabel("初始密码", { exact: true }).fill(password);
  await dialog.getByLabel("流量额度（字节）", { exact: true }).fill("0");
  await dialog.getByLabel("分销商", { exact: true }).check();
  await dialog.getByLabel("分销商名称", { exact: true }).fill(name);
  await dialog.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();
  await expect(page.getByText(name, { exact: true })).toBeVisible();
}

async function createPlan(page: Page, name: string) {
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称", { exact: true }).fill(name);
  await dialog.getByLabel("流量（GiB）", { exact: true }).fill("100");
  await dialog.getByLabel("月付", { exact: true }).fill("100.00");
  await dialog.getByLabel("季付", { exact: true }).fill("270.00");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const row = planRow(page, name);
  await row.getByLabel("展示", { exact: true }).check();
  await row.getByLabel("销售", { exact: true }).check();
  await row.getByLabel("续费", { exact: true }).check();
}

function planRow(page: Page, name: string): Locator { return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) }); }
function distributorOrderRow(page: Page, tradeNo: string): Locator { return page.getByRole("row").filter({ has: page.getByText(tradeNo, { exact: true }) }).filter({ hasText: "新购" }); }
