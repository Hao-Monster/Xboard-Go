import { expect, test, type Browser, type Page } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

const originalPassword = adminPassword;
const replacementPassword = "e2e-replacement-password-456";

test("administrator revokes other sessions and changes the password", async ({ browser }) => {
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
    await expect(second.getByRole("heading", { name: "管理员登录" })).toBeVisible();

    await first.getByLabel("当前密码").fill(originalPassword);
    await first.getByLabel("新密码", { exact: true }).fill(replacementPassword);
    await first.getByLabel("确认新密码").fill("does-not-match-password");
    await first.getByRole("button", { name: "修改密码" }).click();
    await expect(first.getByRole("alert")).toContainText("两次输入的新密码不一致");

    await first.getByLabel("确认新密码").fill(replacementPassword);
    passwordMayNeedRestore = true;
    await first.getByRole("button", { name: "修改密码" }).click();
    await expect(first.getByRole("heading", { name: "管理员登录" })).toBeVisible();
    await first.getByLabel("邮箱").fill(adminEmail);
    await first.getByLabel("密码").fill(originalPassword);
    await first.getByRole("button", { name: "登录" }).click();
    await expect(first.getByRole("alert")).toContainText("邮箱或密码错误");

    await first.getByLabel("密码").fill(replacementPassword);
    await first.getByRole("button", { name: "登录" }).click();
    await expect(first.getByRole("button", { name: "账号安全", exact: true })).toBeVisible();
    await first.getByRole("button", { name: "账号安全", exact: true }).click();
    await first.getByLabel("当前密码").fill(replacementPassword);
    await first.getByLabel("新密码", { exact: true }).fill(originalPassword);
    await first.getByLabel("确认新密码").fill(originalPassword);
    await first.getByRole("button", { name: "修改密码" }).click();
    await expect(first.getByRole("heading", { name: "管理员登录" })).toBeVisible();
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
  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("button", { name: "账号安全", exact: true })).toBeVisible();
}

async function restoreOriginalPassword(browser: Browser) {
  const context = await browser.newContext();
  const page = await context.newPage();
  try {
    await page.goto("/");
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
    await expect(page.getByRole("heading", { name: "管理员登录" })).toBeVisible();
  } finally {
    await context.close();
  }
}
