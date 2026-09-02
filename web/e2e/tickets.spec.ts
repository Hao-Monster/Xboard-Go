import { expect, test, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait } from "./support";

test("user and administrator complete the legacy ticket lifecycle", async ({ page }) => {
  test.setTimeout(60_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  const unique = Date.now();
  const userEmail = `ticket-user-${unique}@example.test`;
  const userPassword = "ticket-user-password-123";
  const subject = `Route outage ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createAdminUserFixture(page, { email: userEmail, password: userPassword });

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "我的工单", exact: true }).click();
  await expect(page.getByRole("heading", { name: "我的工单" })).toBeVisible();
  await page.getByRole("button", { name: "新建工单" }).click();
  let dialog = page.getByRole("dialog", { name: "新建工单" });
  await dialog.getByLabel("主题").fill(subject);
  await dialog.getByLabel("工单级别").selectOption("2");
  await dialog.getByLabel("消息").fill("Initial user message");
  await dialog.getByRole("button", { name: "创建工单" }).click();
  await expect(page.getByText(subject, { exact: true })).toBeVisible();
  await expect(page.getByText("高", { exact: true })).toBeVisible();
  await expect(page.getByText("待回复", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "新建工单" }).click();
  dialog = page.getByRole("dialog", { name: "新建工单" });
  await dialog.getByLabel("主题").fill(`${subject} duplicate`);
  await dialog.getByLabel("消息").fill("Duplicate open ticket");
  await dialog.getByRole("button", { name: "创建工单" }).click();
  await expect(dialog.getByRole("alert")).toContainText("存在未关闭的工单");
  await dialog.getByRole("button", { name: "取消" }).click();

  await page.getByRole("button", { name: `查看工单：${subject}` }).click();
  dialog = page.getByRole("dialog", { name: "工单详情" });
  await expect(dialog.getByText("Initial user message")).toBeVisible();
  await dialog.getByLabel("回复内容").fill("User follow-up");
  await dialog.getByRole("button", { name: "回复" }).click();
  await expect(dialog.getByText("User follow-up")).toBeVisible();
  await dialog.getByRole("button", { name: "关闭工单", exact: true }).click();
  const closeDialog = page.getByRole("dialog", { name: "关闭工单" });
  await closeDialog.getByRole("button", { name: "确认关闭" }).click();
  await expect(dialog.getByText("已关闭", { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("回复内容")).toHaveCount(0);
  await dialog.getByRole("button", { name: "关闭工单详情" }).click();
  await expectLayoutWithinViewport(page);

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "工单管理", exact: true }).click();
  await page.getByRole("button", { name: "已关闭", exact: true }).click();
  await page.getByRole("searchbox", { name: "搜索工单" }).fill(userEmail);
  await page.getByRole("button", { name: "查询工单" }).click();
  await expect(page.getByText(subject, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `查看工单：${subject}` }).click();
  dialog = page.getByRole("dialog", { name: "工单详情" });
  await expect(dialog).toContainText(userEmail);
  await dialog.getByLabel("回复内容").fill("Administrator answer after close");
  await dialog.getByRole("button", { name: "回复" }).click();
  await expect(dialog.getByText("Administrator answer after close")).toBeVisible();
  await expect(dialog.getByText("已关闭", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭工单详情" }).click();

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "我的工单", exact: true }).click();
  await page.getByRole("button", { name: `查看工单：${subject}` }).click();
  dialog = page.getByRole("dialog", { name: "工单详情" });
  await expect(dialog.getByText("Administrator answer after close")).toBeVisible();
  await expect(dialog.getByLabel("回复内容")).toHaveCount(0);
  await dialog.getByRole("button", { name: "关闭工单详情" }).click();

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

async function expectLayoutWithinViewport(page: Page) {
  const layout = await page.evaluate(() => ({
    viewportWidth: window.innerWidth,
    documentWidth: document.documentElement.scrollWidth,
    boxes: [".brand", ".topbar", ".ticket-page"].map((selector) => {
      const box = document.querySelector(selector)?.getBoundingClientRect();
      return { selector, left: box?.left ?? -1, right: box?.right ?? -1 };
    })
  }));
  expect(layout.documentWidth).toBeLessThanOrEqual(layout.viewportWidth);
  for (const box of layout.boxes) {
    expect(box.left, `${box.selector} starts outside the viewport`).toBeGreaterThanOrEqual(0);
    expect(box.right, `${box.selector} ends outside the viewport`).toBeLessThanOrEqual(layout.viewportWidth);
  }
}
