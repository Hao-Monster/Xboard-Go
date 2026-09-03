import { expect, test, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait  } from "./support";

test("gift-card administrator and user lifecycle works on every supported viewport", async ({ page }) => {
  test.setTimeout(120_000);
  const pageErrors: string[] = []; const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });
  const unique = `${Date.now()}-${test.info().project.name}`;
  const userEmail = `gift-user-${unique}@example.test`; const userPassword = "gift-user-password-123"; const templateName = `E2E 礼品卡 ${unique}`;

  await login(page, adminEmail, adminPassword); await createUser(page, userEmail, userPassword);
  await page.getByRole("button", { name: "礼品卡管理", exact: true }).click();
  for (const tab of ["模板管理", "兑换码管理", "使用记录", "统计数据"]) await expect(page.getByRole("button", { name: tab, exact: true })).toBeVisible();
  await page.getByRole("button", { name: "添加模板", exact: true }).click();
  let dialog = page.getByRole("dialog", { name: "添加礼品卡模板" });
  await dialog.getByLabel("模板名称", { exact: true }).fill(templateName);
  await dialog.getByLabel("模板描述", { exact: true }).fill("端到端原子兑换验证");
  await dialog.getByLabel("余额（元）", { exact: true }).fill("5.00");
  await dialog.getByLabel("每用户最多使用次数", { exact: true }).fill("1");
  await dialog.getByRole("button", { name: "保存模板", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: templateName })).toContainText("余额 ¥5.00");

  await page.getByRole("button", { name: "兑换码管理", exact: true }).click();
  await page.getByRole("button", { name: "生成兑换码", exact: true }).click();
  const generator = page.getByTestId("modal-layer");
  await expect(generator.getByRole("dialog", { name: "生成兑换码" })).toBeVisible();
  const templateSelect = generator.locator("select").first();
  await expect(templateSelect).toHaveValue(/^\d+$/);
  await generator.getByLabel("生成数量", { exact: true }).fill("1");
  await generator.getByLabel("兑换码前缀", { exact: true }).fill("E2E");
  await generator.getByRole("button", { name: "生成兑换码", exact: true }).click();
  const codeRow = page.getByRole("row").filter({ hasText: templateName });
  await expect(codeRow).toBeVisible();
  const code = (await codeRow.locator("code").textContent())?.trim() ?? "";
  expect(code).toMatch(/^E2E[A-Z0-9]{20}$/);
  const batch = (await codeRow.getByRole("button", { name: "导出批次", exact: true }).count()) === 1;
  expect(batch).toBe(true);
  const downloadPromise = page.waitForEvent("download"); await codeRow.getByRole("button", { name: "导出批次", exact: true }).click();
  const download = await downloadPromise; const path = await download.path(); if (path === null) throw new Error("gift-card CSV download path is unavailable");
  expect(await readFile(path, "utf8")).toContain(code);
  await codeRow.getByRole("button", { name: "编辑", exact: true }).click(); dialog = page.getByRole("dialog", { name: "编辑兑换码" });
  await expect(dialog.getByLabel("过期时间", { exact: true })).toHaveValue(""); await dialog.getByRole("button", { name: "保存兑换码", exact: true }).click();
  await codeRow.getByRole("button", { name: "禁用", exact: true }).click(); await expect(codeRow).toContainText("已禁用");
  await codeRow.getByRole("button", { name: "启用", exact: true }).click(); await expect(codeRow).toContainText("可用");

  await logoutAndWait(page); await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "礼品卡", exact: true }).click();
  await page.getByLabel("礼品卡兑换码", { exact: true }).fill(code); await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: templateName })).toContainText("余额 ¥5.00");
  await page.getByRole("button", { name: "确认兑换", exact: true }).click(); await expect(page.getByText(/兑换成功！.*余额 ¥5.00/)).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: `${code.slice(0, 8)}****` })).toContainText(templateName);
  await page.getByLabel("礼品卡兑换码", { exact: true }).fill(code); await page.getByRole("button", { name: "查询奖励", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("已无剩余次数");

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1); expect(pageErrors).toEqual([]); expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/"); await expectLoginPage(page); await page.getByLabel("邮箱", { exact: true }).fill(email); await page.getByLabel("密码", { exact: true }).fill(password); await page.getByRole("button", { name: "登录", exact: true }).click();
}

async function createUser(page: Page, email: string, password: string) {
  await createAdminUserFixture(page, { email, password });
}
