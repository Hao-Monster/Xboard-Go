import { expect, test } from "@playwright/test";

import { adminEmail, adminPassword, adminEntryPath } from "./support";

test("administrator navigation stays in a vertical left sidebar on desktop", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop shell layout regression");

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  await expect(page.getByRole("main")).toHaveCount(1);

  const sidebar = page.getByRole("navigation", { name: "管理端导航" });
  const content = page.locator(".admin-content");
  const navigation = sidebar.locator(".admin-nav");
  await expect(sidebar).toBeVisible();
  await expect(navigation).toHaveCSS("flex-direction", "column");
  await expect(navigation).toHaveCSS("overflow-x", "visible");
  await expect(page.locator(".topbar .admin-nav")).toHaveCount(0);

  await page.evaluate(() => {
    const root = document.documentElement;
    root.dataset.themeSidebarStyle = "light";
    root.style.setProperty("--theme-background", "#10141c");
    root.style.setProperty("--theme-surface", "#1b2230");
    root.style.setProperty("--theme-muted", "#12ab34");
  });
  const themedBackground = await sidebar.evaluate((element) => getComputedStyle(element).backgroundColor);
  await page.evaluate(() => document.documentElement.style.setProperty("--theme-surface", "#263044"));
  await expect.poll(() => sidebar.evaluate((element) => getComputedStyle(element).backgroundColor)).not.toBe(themedBackground);
  await expect(navigation.getByRole("button", { name: "仪表盘" })).toHaveCSS("color", "rgb(18, 171, 52)");

  const sidebarBox = await sidebar.boundingBox();
  const contentBox = await content.boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(sidebarBox!.x).toBeLessThan(contentBox!.x);
  expect(sidebarBox!.width).toBeLessThan(contentBox!.width);

  const firstButtonBox = await navigation.getByRole("button", { name: "仪表盘" }).boundingBox();
  const secondButtonBox = await navigation.locator(".nav-group-header").first().boundingBox();
  expect(firstButtonBox).not.toBeNull();
  expect(secondButtonBox).not.toBeNull();
  expect(firstButtonBox!.x).toBe(secondButtonBox!.x);
  expect(firstButtonBox!.y).toBeLessThan(secondButtonBox!.y);
});

test("administrator navigation stays in a vertical left sidebar on mobile", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile-chromium", "Mobile shell layout regression");

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const sidebar = page.getByRole("navigation", { name: "管理端导航" });
  const content = page.locator(".admin-content");
  const navigation = sidebar.locator(".admin-nav");
  await expect(sidebar).toBeVisible();
  await expect(navigation).toHaveCSS("flex-direction", "column");
  await expect(navigation).toHaveCSS("overflow-x", "visible");

  const sidebarBox = await sidebar.boundingBox();
  const contentBox = await content.boundingBox();
  const firstButtonBox = await navigation.getByRole("button", { name: "仪表盘" }).boundingBox();
  const secondButtonBox = await navigation.getByRole("button", { name: "系统配置" }).boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(firstButtonBox).not.toBeNull();
  expect(secondButtonBox).not.toBeNull();
  expect(sidebarBox!.x).toBeLessThan(contentBox!.x);
  expect(sidebarBox!.width).toBeLessThanOrEqual(160);
  expect(secondButtonBox!.x).toBeGreaterThan(firstButtonBox!.x);
  expect(firstButtonBox!.y).toBeLessThan(secondButtonBox!.y);

  const accountMenu = page.locator(".admin-account-menu");
  await accountMenu.locator("summary").click();
  await expect(accountMenu.getByRole("button", { name: "账号安全" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test("administrator sidebar leaves the management surface usable at tablet width", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Tablet shell layout regression");
  await page.setViewportSize({ width: 900, height: 800 });

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const sidebar = page.getByRole("navigation", { name: "管理端导航" });
  const content = page.locator(".admin-content");
  await expect(sidebar.locator(".admin-nav")).toHaveCSS("flex-direction", "column");
  const sidebarBox = await sidebar.boundingBox();
  const contentBox = await content.boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(sidebarBox!.x).toBeLessThan(contentBox!.x);
  expect(sidebarBox!.width).toBeLessThan(contentBox!.width);

  const users = sidebar.locator("#admin-group-users").getByRole("button", { name: "用户管理" });
  await users.scrollIntoViewIfNeeded();
  await users.click();
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test("admin navigation survives reload and browser history", async ({ page }) => {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", {name:"登录",exact:true}).click();
  const nav = page.getByRole("navigation", {name:"管理端导航"});
  await expect(nav).toBeVisible();
  await nav.getByRole("button", {name:"节点管理",exact:true}).click();
  await expect(page).toHaveURL(/#\/server\/manage$/);
  await page.reload();
  await expect(page.getByRole("heading", {name:"节点管理",exact:true})).toBeVisible();
  await nav.getByRole("button", {name:"系统配置",exact:true}).click();
  await expect(page).toHaveURL(/#\/config\/system$/);
  await page.reload();
  await expect(nav.getByRole("button", {name:"系统配置",exact:true})).toHaveAttribute("aria-current","page");
  await expect(page.getByRole("heading", {name:"服务器管理",exact:true})).toHaveCount(0);
  await page.goBack();
  await expect(page.getByRole("heading", {name:"节点管理",exact:true})).toBeVisible();
  await page.goForward();
  await expect(nav.getByRole("button", {name:"系统配置",exact:true})).toHaveAttribute("aria-current","page");
  await page.goto(adminEntryPath.split("#")[0] + "#/server/manage");
  await expect(page.getByRole("heading", {name:"节点管理",exact:true})).toBeVisible();
});

test("administrator navigation keeps the current page visible while an uncached page chunk loads", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop navigation loading regression");

  let releaseGiftCardChunk!: () => void;
  const giftCardChunkBlocked = new Promise<void>((resolve) => { releaseGiftCardChunk = resolve; });
  let giftCardChunkRequested = false;
  await page.route("**/assets/GiftCardManagementPage-*.js", async (route) => {
    giftCardChunkRequested = true;
    await giftCardChunkBlocked;
    await route.continue();
  });

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  const serverHeading = page.getByRole("heading", { name: "服务器管理", exact: true });
  await expect(serverHeading).toBeVisible();
  await expect.poll(() => giftCardChunkRequested).toBe(true);

  await page.getByRole("navigation", { name: "管理端导航" }).getByRole("button", { name: "礼品卡管理", exact: true }).click();
  await expect(page).toHaveURL(/#\/server\/machine$/);
  await expect(page.getByText("正在加载管理页面…", { exact: true })).toHaveCount(0);
  await expect(serverHeading).toBeVisible();

  releaseGiftCardChunk();
  await expect(page).toHaveURL(/#\/finance\/gift-card$/);
  await expect(page.getByRole("heading", { name: "礼品卡管理", exact: true })).toBeVisible();
});

test("browser history cancels a stale navigation waiting on a page chunk", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop navigation history race regression");

  let releaseGiftCardChunk!: () => void;
  const giftCardChunkBlocked = new Promise<void>((resolve) => { releaseGiftCardChunk = resolve; });
  let giftCardChunkRequested = false;
  await page.route("**/assets/GiftCardManagementPage-*.js", async (route) => {
    giftCardChunkRequested = true;
    await giftCardChunkBlocked;
    await route.continue();
  });

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  const navigation = page.getByRole("navigation", { name: "管理端导航" });
  await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();
  await expect.poll(() => giftCardChunkRequested).toBe(true);

  await navigation.getByRole("button", { name: "节点管理", exact: true }).click();
  await expect(page).toHaveURL(/#\/server\/manage$/);
  await expect(page.getByRole("heading", { name: "节点管理", exact: true })).toBeVisible();
  await navigation.getByRole("button", { name: "礼品卡管理", exact: true }).click();
  await expect(page).toHaveURL(/#\/server\/manage$/);
  await page.goBack();
  await expect(page).toHaveURL(/#\/server\/machine$/);
  await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();

  releaseGiftCardChunk();
  await expect(page).toHaveURL(/#\/server\/machine$/);
  await expect(page.getByRole("heading", { name: "礼品卡管理", exact: true })).toHaveCount(0);
});

test("canceling a newer leave confirmation also cancels an older pending navigation", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop unsaved-change navigation race regression");

  let releaseGiftCardChunk!: () => void;
  const giftCardChunkBlocked = new Promise<void>((resolve) => { releaseGiftCardChunk = resolve; });
  let giftCardChunkRequested = false;
  await page.route("**/assets/GiftCardManagementPage-*.js", async (route) => {
    giftCardChunkRequested = true;
    await giftCardChunkBlocked;
    await route.continue();
  });

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  const navigation = page.getByRole("navigation", { name: "管理端导航" });
  await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();
  await expect.poll(() => giftCardChunkRequested).toBe(true);

  await navigation.getByRole("button", { name: "系统配置", exact: true }).click();
  await page.getByRole("navigation", { name: "系统配置子导航" }).getByRole("button", { name: "客户端版本", exact: true }).click();
  const windowsVersion = page.getByLabel("Windows 版本");
  await windowsVersion.fill("unsaved-performance-test");
  await expect(windowsVersion).toHaveValue("unsaved-performance-test");

  let confirmationCount = 0;
  page.on("dialog", async (dialog) => {
    confirmationCount += 1;
    if (confirmationCount === 1) await dialog.accept();
    else await dialog.dismiss();
  });
  await navigation.getByRole("button", { name: "礼品卡管理", exact: true }).click();
  await navigation.getByRole("button", { name: "插件管理", exact: true }).click();
  await expect.poll(() => confirmationCount).toBe(2);

  const giftCardChunkLoaded = page.waitForResponse((response) => response.url().includes("/assets/GiftCardManagementPage-") && response.ok());
  releaseGiftCardChunk();
  await giftCardChunkLoaded;
  await expect(page).toHaveURL(/#\/config\/system\/client-app$/);
  await expect(page.getByRole("heading", { name: "客户端版本", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "礼品卡管理", exact: true })).toHaveCount(0);
});
