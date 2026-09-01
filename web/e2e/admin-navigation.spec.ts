import { expect, test } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("administrator navigation stays in a vertical left sidebar on desktop", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "Desktop shell layout regression");

  await page.goto("/");
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
