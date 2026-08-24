import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, logoutAndWait } from "./support";

test("administrator manages knowledge while active, inactive, and public readers receive the correct content", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  const unique = Date.now();
  const activeEmail = `knowledge-active-${unique}@example.test`;
  const inactiveEmail = `knowledge-inactive-${unique}@example.test`;
  const password = "knowledge-user-password-123";
  const visibleTitle = `Visible guide ${unique}`;
  const editedTitle = `${visibleTitle} revised`;
  const hiddenTitle = `Hidden guide ${unique}`;
  const privateText = `SUBSCRIPTION-ONLY-${unique}`;

  await login(page, adminEmail, adminPassword);
  await createUser(page, activeEmail, password, "1048576");
  await createUser(page, inactiveEmail, password, "0");

  await page.getByRole("button", { name: "知识库管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "知识库管理" })).toBeVisible();
  await createKnowledge(page, visibleTitle, `# Setup\n\n{{siteName}}\n\n[Subscription]({{subscribeUrl}})\n\n<!--access start-->${privateText}<!--access end-->\n\n<script>alert(1)</script>`, true);
  await createKnowledge(page, hiddenTitle, "hidden content", false);

  await page.getByRole("button", { name: `编辑知识：${visibleTitle}` }).click();
  let dialog = page.getByRole("dialog", { name: "编辑知识" });
  await dialog.getByLabel("标题").fill(editedTitle);
  await dialog.getByRole("button", { name: "提交" }).click();
  await expect(page.getByText(editedTitle, { exact: true })).toBeVisible();

  const share = page.locator('a[href*="/guide/"]', { hasText: "分享" }).first();
  const shareURL = await share.getAttribute("href");
  expect(shareURL).toBeTruthy();
  if (shareURL === null) throw new Error("knowledge share URL is missing");
  const publicResponse = await page.request.get(shareURL);
  expect(publicResponse.status()).toBe(200);
  const publicHTML = await publicResponse.text();
  expect(publicHTML).toContain(editedTitle);
  expect(publicHTML).toContain("Xboard-Go");
  expect(publicHTML).toContain("/#/login");
  expect(publicHTML).not.toContain("{{subscribeUrl}}");
  expect(publicHTML).not.toContain("/api/v1/client/subscribe?token=");
  expect(publicHTML).not.toMatch(/<script|onerror=/i);

  await logoutAndWait(page);
  await login(page, inactiveEmail, password);
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await expect(page.getByText(editedTitle, { exact: true })).toBeVisible();
  await expect(page.getByText(hiddenTitle, { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: `阅读：${editedTitle}` }).click();
  dialog = page.getByRole("dialog", { name: editedTitle });
  await expect(dialog.getByText("您必须拥有有效的订阅才可以查看该区域的内容")).toBeVisible();
  await expect(dialog.getByText(privateText, { exact: true })).toHaveCount(0);
  await dialog.getByRole("button", { name: `关闭${editedTitle}` }).click();

  await logoutAndWait(page);
  await login(page, activeEmail, password);
  await page.getByRole("button", { name: "知识库", exact: true }).click();
  await page.getByRole("button", { name: `阅读：${editedTitle}` }).click();
  dialog = page.getByRole("dialog", { name: editedTitle });
  await expect(dialog.getByText(privateText, { exact: true })).toBeVisible();
  await expect(dialog.getByRole("link", { name: "Subscription" })).toHaveAttribute("href", /\/api\/v1\/client\/subscribe\?token=[0-9a-f]{32}$/);
  await expect(dialog.locator("script")).toHaveCount(0);
  await dialog.getByRole("button", { name: `关闭${editedTitle}` }).click();

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "知识库管理", exact: true }).click();
  for (const title of [hiddenTitle, editedTitle]) {
    await page.getByRole("button", { name: `删除知识：${title}` }).click();
    dialog = page.getByRole("dialog", { name: "删除知识" });
    await dialog.getByRole("button", { name: "确认删除" }).click();
    await expect(page.getByText(title, { exact: true })).toHaveCount(0);
  }

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "登录 Xboard-Go" })).toBeVisible();
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}

async function createUser(page: Page, email: string, password: string, transfer: string) {
  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await page.getByRole("button", { name: "新增用户" }).click();
  const dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("邮箱").fill(email);
  await dialog.getByLabel("初始密码").fill(password);
  await dialog.getByLabel("流量额度（字节）").fill(transfer);
  await dialog.getByRole("button", { name: "创建" }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();
}

async function createKnowledge(page: Page, title: string, body: string, show: boolean) {
  await page.getByRole("button", { name: "添加知识" }).click();
  const dialog = page.getByRole("dialog", { name: "添加知识" });
  await dialog.getByLabel("标题").fill(title);
  await dialog.getByLabel("分类").fill("使用指南");
  await dialog.getByLabel("语言").selectOption("zh-CN");
  await dialog.getByLabel("内容").fill(body);
  if (show) await dialog.getByLabel("显示").check();
  await dialog.getByRole("button", { name: "提交" }).click();
  await expect(page.getByText(title, { exact: true })).toBeVisible();
}
