import { expect, test, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait  } from "./support";

test("administrator manages ordered notices and a user reads only visible safe markdown", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  const unique = Date.now();
  const userEmail = `notice-user-${unique}@example.test`;
  const userPassword = "notice-user-password-123";
  const visibleTitle = `Visible notice ${unique}`;
  const revisedTitle = `${visibleTitle} revised`;
  const hiddenTitle = `Hidden notice ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createAdminUserFixture(page, { email: userEmail, password: userPassword });

  await page.getByRole("button", { name: "公告管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "公告管理" })).toBeVisible();
  await createNotice(page, visibleTitle, "**Available now** <img src=x onerror=alert(1)> [unsafe](javascript:alert(2))", true);
  await createNotice(page, hiddenTitle, "hidden body", false);

  await page.getByRole("button", { name: `显示公告：${hiddenTitle}` }).click();
  await expect(page.getByRole("button", { name: `隐藏公告：${hiddenTitle}` })).toBeVisible();
  await page.getByRole("button", { name: `隐藏公告：${hiddenTitle}` }).click();
  await expect(page.getByRole("button", { name: `显示公告：${hiddenTitle}` })).toBeVisible();

  await page.getByRole("button", { name: `编辑公告：${visibleTitle}` }).click();
  let dialog = page.getByRole("dialog", { name: "编辑公告" });
  await dialog.getByLabel("标题").fill(revisedTitle);
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText(revisedTitle, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "编辑排序" }).click();
  dialog = page.getByRole("dialog", { name: "编辑公告排序" });
  await dialog.getByRole("button", { name: `下移：${hiddenTitle}` }).click();
  await dialog.getByRole("button", { name: "保存排序" }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator("tbody tr").first()).toContainText(revisedTitle);

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "公告", exact: true }).click();
  await expect(page.getByRole("heading", { name: "公告", exact: true })).toBeVisible();
  const visibleNotice = page.getByRole("article").filter({ has: page.getByRole("heading", { name: revisedTitle }) });
  await expect(visibleNotice).toBeVisible();
  await expect(visibleNotice.getByText("Available now", { exact: true })).toBeVisible();
  await expect(page.getByText(hiddenTitle, { exact: true })).toHaveCount(0);
  await expect(visibleNotice.locator(".markdown-body img")).toHaveCount(0);
  await expect(visibleNotice.getByRole("link", { name: "unsafe" })).not.toHaveAttribute("href", /^javascript:/i);
  await expectLayoutWithinViewport(page);

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "公告管理", exact: true }).click();
  for (const title of [hiddenTitle, revisedTitle]) {
    await page.getByRole("button", { name: `删除公告：${title}` }).click();
    dialog = page.getByRole("dialog", { name: "删除公告" });
    await dialog.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByText(title, { exact: true })).toHaveCount(0);
  }

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}

async function createNotice(page: Page, title: string, content: string, show: boolean) {
  await page.getByRole("button", { name: "添加公告" }).click();
  const dialog = page.getByRole("dialog", { name: "添加公告" });
  await dialog.getByLabel("标题").fill(title);
  await dialog.getByLabel("公告内容").fill(content);
  await dialog.getByLabel("节点标签").fill("news, service");
  if (show) await dialog.getByRole("checkbox", { name: "显示给用户" }).check();
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText(title, { exact: true })).toBeVisible();
}

async function expectLayoutWithinViewport(page: Page) {
  const layout = await page.evaluate(() => {
    const selectors = [".brand", ".topbar", ".notice-feed-page", ".notice-card"];
    return {
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth,
      boxes: selectors.map((selector) => {
        const element = document.querySelector(selector);
        const box = element?.getBoundingClientRect();
        return { selector, left: box?.left ?? -1, right: box?.right ?? -1 };
      })
    };
  });
  expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth);
  for (const box of layout.boxes) {
    expect(box.left, `${box.selector} starts outside the viewport`).toBeGreaterThanOrEqual(0);
    expect(box.right, `${box.selector} ends outside the viewport`).toBeLessThanOrEqual(layout.viewportWidth);
  }
}
