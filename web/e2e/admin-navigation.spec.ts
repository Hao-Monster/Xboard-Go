import { expect, test } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("administrator navigation stays in a vertical left sidebar on desktop", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop shell layout regression");

  await page.goto("/");
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

  const sidebarBox = await sidebar.boundingBox();
  const contentBox = await content.boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(sidebarBox!.x).toBeLessThan(contentBox!.x);
  expect(sidebarBox!.width).toBeLessThan(contentBox!.width);

  const firstButtonBox = await navigation.getByRole("button", { name: "系统状态" }).boundingBox();
  const secondButtonBox = await navigation.getByRole("button", { name: "系统设置" }).boundingBox();
  expect(firstButtonBox).not.toBeNull();
  expect(secondButtonBox).not.toBeNull();
  expect(firstButtonBox!.x).toBe(secondButtonBox!.x);
  expect(firstButtonBox!.y).toBeLessThan(secondButtonBox!.y);
});

test("administrator navigation remains horizontally scrollable on mobile", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile-chromium", "Mobile shell layout regression");

  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const sidebar = page.getByRole("navigation", { name: "管理端导航" });
  const navigation = sidebar.locator(".admin-nav");
  await expect(sidebar).toBeVisible();
  await expect(navigation).toHaveCSS("flex-direction", "row");
  await expect(navigation).toHaveCSS("overflow-x", "auto");

  const firstButtonBox = await navigation.getByRole("button", { name: "系统状态" }).boundingBox();
  const secondButtonBox = await navigation.getByRole("button", { name: "系统设置" }).boundingBox();
  expect(firstButtonBox).not.toBeNull();
  expect(secondButtonBox).not.toBeNull();
  expect(firstButtonBox!.x).toBeLessThan(secondButtonBox!.x);
  expect(firstButtonBox!.y).toBe(secondButtonBox!.y);

  const accountSecurity = navigation.getByRole("button", { name: "账号安全" });
  await accountSecurity.scrollIntoViewIfNeeded();
  await expect(accountSecurity).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test("administrator sidebar leaves the management surface usable at tablet width", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Tablet shell layout regression");
  await page.setViewportSize({ width: 900, height: 800 });

  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const sidebar = page.getByRole("navigation", { name: "管理端导航" });
  const content = page.locator(".admin-content");
  await expect(sidebar.locator(".admin-nav")).toHaveCSS("flex-direction", "row");
  const sidebarBox = await sidebar.boundingBox();
  const contentBox = await content.boundingBox();
  expect(sidebarBox).not.toBeNull();
  expect(contentBox).not.toBeNull();
  expect(sidebarBox!.y).toBeLessThan(contentBox!.y);

  const users = sidebar.getByRole("button", { name: "用户管理" });
  await users.scrollIntoViewIfNeeded();
  await users.click();
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});
