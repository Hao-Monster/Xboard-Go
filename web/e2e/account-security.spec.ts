import { expect, test, type Browser, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, expectLoginPage } from "./support";

const originalPassword = adminPassword;
const replacementPassword = "e2e-replacement-password-456";

test("administrator creates, uses, and revokes a long-lived credential without browser persistence", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page, originalPassword);
  await page.getByRole("button", { name: "账号安全", exact: true }).click();
  const credentialSection = page.getByRole("region", { name: "长期访问凭证" });
  while (await credentialSection.getByRole("button", { name: "撤销凭证", exact: true }).count() > 0) {
    await credentialSection.getByRole("button", { name: "撤销凭证", exact: true }).first().click();
  }
  await credentialSection.getByLabel("凭证名称").fill("Playwright isolated client");
  await credentialSection.getByLabel("有效期").selectOption("permanent");
  await credentialSection.getByRole("button", { name: "创建凭证" }).click();
  const oneTime = credentialSection.getByRole("status");
  await expect(oneTime).toContainText("请立即保存");
  const authorization = (await oneTime.locator("code").textContent())?.trim() ?? "";
  expect(authorization).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);
  expect(await page.evaluate((secret) => {
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index);
      if (key !== null && localStorage.getItem(key)?.includes(secret) === true) return false;
    }
    return true;
  }, authorization)).toBe(true);
  expect((await page.request.get("/api/v1/auth/session", { headers: { Authorization: authorization } })).status()).toBe(200);

  await oneTime.getByRole("button", { name: "关闭" }).click();
  await expect(oneTime).toBeHidden();
  const row = credentialSection.locator("[data-testid^='access-token-']", { hasText: "Playwright isolated client" });
  await row.getByRole("button", { name: "撤销凭证" }).click();
  await expect(row).toBeHidden();
  expect((await page.request.get("/api/v1/auth/session", { headers: { Authorization: authorization } })).status()).toBe(401);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

test("administrator revokes other sessions and changes the password without accumulating login failures", async ({ browser }) => {
  const firstContext = await browser.newContext();
  const secondContext = await browser.newContext();
  const first = await firstContext.newPage();
  const second = await secondContext.newPage();
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  let passwordMayNeedRestore = false;

  for (const page of [first, second]) {
    page.on("pageerror", (error) => pageErrors.push(error.message));
    page.on("response", (response) => {
      if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
    });
  }

  try {
    await login(first, originalPassword);
    await login(second, originalPassword);
    await first.getByRole("button", { name: "账号安全", exact: true }).click();
    await expect(first.getByRole("heading", { name: "账号安全" })).toBeVisible();
    await expect(first.getByText("当前会话", { exact: true })).toBeVisible();

    while (await first.getByRole("button", { name: "撤销会话", exact: true }).count() > 0) {
      await first.getByRole("button", { name: "撤销会话", exact: true }).first().click();
    }
    await expect(first.locator("[data-testid^='account-session-']")).toHaveCount(1);
    await second.reload();
    await expectLoginPage(second);

    await first.getByLabel("当前密码").fill(originalPassword);
    await first.getByLabel("新密码", { exact: true }).fill(replacementPassword);
    await first.getByLabel("确认新密码").fill("does-not-match-password");
    await first.getByRole("button", { name: "修改密码" }).click();
    await expect(first.getByRole("alert")).toContainText("两次输入的新密码不一致");

    await first.getByLabel("确认新密码").fill(replacementPassword);
    passwordMayNeedRestore = true;
    await first.getByRole("button", { name: "修改密码" }).click();
    await expectLoginPage(first);
    await first.getByLabel("邮箱").fill(adminEmail);
    await first.getByLabel("密码").fill(replacementPassword);
    await first.getByRole("button", { name: "登录" }).click();
    await expect(first.getByRole("button", { name: "账号安全", exact: true })).toBeVisible();
    await first.getByRole("button", { name: "账号安全", exact: true }).click();
    await first.getByLabel("当前密码").fill(replacementPassword);
    await first.getByLabel("新密码", { exact: true }).fill(originalPassword);
    await first.getByLabel("确认新密码").fill(originalPassword);
    await first.getByRole("button", { name: "修改密码" }).click();
    await expectLoginPage(first);
    passwordMayNeedRestore = false;
    await login(first, originalPassword);

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    if (passwordMayNeedRestore) await restoreOriginalPassword(browser);
    await firstContext.close();
    await secondContext.close();
  }
});

async function login(page: Page, password: string) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("button", { name: "账号安全", exact: true })).toBeVisible();
}

async function restoreOriginalPassword(browser: Browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await page.goto(adminEntryPath);
    await page.getByLabel("邮箱").fill(adminEmail);
    await page.getByLabel("密码").fill(replacementPassword);
    await page.getByRole("button", { name: "登录" }).click();
    const accountButton = page.getByRole("button", { name: "账号安全", exact: true });
    const loggedIn = await accountButton.isVisible({ timeout: 5_000 }).catch(() => false);
    if (!loggedIn) return;
    await accountButton.click();
    await page.getByLabel("当前密码").fill(replacementPassword);
    await page.getByLabel("新密码", { exact: true }).fill(originalPassword);
    await page.getByLabel("确认新密码").fill(originalPassword);
    await page.getByRole("button", { name: "修改密码" }).click();
    await expectLoginPage(page);
  } finally {
    await context.close();
  }
}
