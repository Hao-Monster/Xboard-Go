import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait  } from "./support";

test("administrator manages plans and a user sees the same purchasable catalog", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page, adminEmail, adminPassword);
  const unique = Date.now();
  const userEmail = `plan-user-${unique}@example.test`;
  const userPassword = "plan-user-password-123";
  await createAdminUserFixture(page, { email: userEmail, password: userPassword });

  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "套餐管理" })).toBeVisible();
  const premiumName = `E2E 高级套餐 ${unique}`;
  const basicName = `E2E 基础套餐 ${unique}`;
  await createPlan(page, {
    name: premiumName, transfer: "1024", speed: "500", devices: "5", capacity: "0",
    reset: "4", monthly: "9.99", quarterly: "27.50", tags: "推荐, 稳定", content: "## E2E 套餐说明"
  });
  await createPlan(page, {
    name: basicName, transfer: "100", speed: "", devices: "", capacity: "25",
    reset: "", monthly: "2.50", quarterly: "", tags: "入门", content: ""
  });

  let premiumRow = planRow(page, premiumName);
  await expect(premiumRow.getByText("月付 ¥9.99", { exact: true })).toBeVisible();
  await expect(premiumRow.getByText("不限量", { exact: true })).toBeVisible();
  await premiumRow.getByLabel("展示", { exact: true }).check();
  await premiumRow.getByLabel("销售", { exact: true }).check();
  const basicRow = planRow(page, basicName);
  await basicRow.getByLabel("展示", { exact: true }).check();
  await basicRow.getByLabel("销售", { exact: true }).check();

  const publicPlans = await page.request.get("/api/v1/guest/plans");
  expect(publicPlans.status()).toBe(200);
  const publicBody = await publicPlans.text();
  expect(publicBody).toContain(`"name":"${premiumName}"`);
  expect(publicBody).toContain('"capacity_remaining":null');
  expect(publicBody).toContain('"monthly":999');

  await page.getByRole("button", { name: `编辑套餐：${premiumName}` }).click();
  const dialog = page.getByRole("dialog", { name: "编辑套餐" });
  await expect(dialog.getByLabel("流量（GiB）")).toHaveValue("1024");
  await expect(dialog.getByLabel("月付", { exact: true })).toHaveValue("9.99");
  await dialog.getByLabel("月付", { exact: true }).fill("10.01");
  await dialog.getByLabel("强制同步套餐权益到现有用户").check();
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  premiumRow = planRow(page, premiumName);
  await expect(premiumRow.getByText("月付 ¥10.01", { exact: true })).toBeVisible();

  await expect(premiumRow).toContainText("总 0");
  await expect(premiumRow).toContainText("有效 0 · 活跃率 0%");

  await page.getByRole("button", { name: "编辑排序" }).click();
  await page.getByRole("button", { name: `下移套餐：${premiumName}` }).click();
  await page.getByRole("button", { name: "保存排序" }).click();
  const rows = page.locator('section[aria-label="套餐列表"] tbody tr');
  await expect.poll(async () => {
    const rowTexts = await rows.allTextContents();
    const basicIndex = rowTexts.findIndex((text) => text.includes(basicName));
    const premiumIndex = rowTexts.findIndex((text) => text.includes(premiumName));
    return basicIndex >= 0 && premiumIndex > basicIndex;
  }).toBe(true);

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "订阅套餐", exact: true }).click();
  await expect(page.getByRole("heading", { name: "订阅套餐" })).toBeVisible();
  const premiumCard = page.getByRole("article").filter({ has: page.getByRole("heading", { name: premiumName }) });
  await expect(premiumCard).toContainText("1024 GiB · 速度 500 · 设备 5");
  await expect(premiumCard).toContainText("不限量");
  await expect(premiumCard).toContainText("月付 ¥10.01");
  await expect(premiumCard).toContainText("可购买");

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await deletePlan(page, premiumName);
  await deletePlan(page, basicName);
  await expect(planRow(page, premiumName)).toBeHidden();
  await expect(planRow(page, basicName)).toBeHidden();

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function createPlan(page: Page, input: {
  name: string; transfer: string; speed: string; devices: string; capacity: string;
  reset: string; monthly: string; quarterly: string; tags: string; content: string;
}) {
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称").fill(input.name);
  await dialog.getByLabel("标签").fill(input.tags);
  await dialog.getByLabel("流量（GiB）").fill(input.transfer);
  await dialog.getByLabel("速度限制").fill(input.speed);
  await dialog.getByLabel("设备限制").fill(input.devices);
  await dialog.getByLabel("容量限制").fill(input.capacity);
  await dialog.getByLabel("流量重置方式").selectOption(input.reset);
  await dialog.getByLabel("月付", { exact: true }).fill(input.monthly);
  await dialog.getByLabel("季付", { exact: true }).fill(input.quarterly);
  await dialog.getByLabel("套餐描述").fill(input.content);
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await expect(planRow(page, input.name)).toBeVisible();
}

async function deletePlan(page: Page, name: string) {
  await page.getByRole("button", { name: `删除套餐：${name}` }).click();
  const dialog = page.getByRole("dialog", { name: "删除套餐" });
  await dialog.getByRole("button", { name: "确认删除" }).click();
  await expect(planRow(page, name)).toBeHidden();
}

function planRow(page: Page, name: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
}
