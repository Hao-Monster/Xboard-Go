import { expect, test } from "@playwright/test";

test("administrator manages permission groups and routing rules", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await page.goto("/");
  await page.getByLabel("邮箱").fill("admin@e2e.test");
  await page.getByLabel("密码").fill("e2e-admin-password-123");
  await page.getByRole("button", { name: "登录" }).click();

  const unique = `${Date.now()}`;
  const groupName = `E2E 权限组 ${unique}`;
  const renamedGroup = `${groupName} 已编辑`;
  await page.getByRole("button", { name: "权限组", exact: true }).click();
  await expect(page.getByRole("heading", { name: "权限组" })).toBeVisible();
  await page.getByRole("button", { name: "新增权限组" }).click();
  let dialog = page.getByRole("dialog", { name: "新增权限组" });
  await dialog.getByLabel("权限组名称").fill(groupName);
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText(groupName, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `编辑权限组：${groupName}` }).click();
  dialog = page.getByRole("dialog", { name: "编辑权限组" });
  await dialog.getByLabel("权限组名称").fill(renamedGroup);
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText(renamedGroup, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `删除权限组：${renamedGroup}` }).click();
  dialog = page.getByRole("dialog", { name: "删除权限组" });
  await dialog.getByRole("button", { name: "确认删除" }).click();
  await expect(page.getByText(renamedGroup, { exact: true })).toBeHidden();

  const routeName = `E2E route ${unique}`;
  await page.getByRole("button", { name: "路由规则", exact: true }).click();
  await expect(page.getByRole("heading", { name: "路由规则" })).toBeVisible();
  await page.getByRole("button", { name: "新增路由规则" }).click();
  dialog = page.getByRole("dialog", { name: "新增路由规则" });
  await dialog.getByLabel("备注").fill(routeName);
  await dialog.getByLabel("匹配规则").fill("*.example.com\n10.0.0.0/8");
  await dialog.getByLabel("动作").selectOption("proxy");
  await dialog.getByLabel("代理出站标记").fill("warp-out");
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText(routeName, { exact: true })).toBeVisible();
  await page.getByRole("searchbox", { name: "搜索规则" }).fill("10.0.0.0/8");
  await expect(page.getByText(routeName, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `编辑路由规则：${routeName}` }).click();
  dialog = page.getByRole("dialog", { name: "编辑路由规则" });
  await dialog.getByLabel("动作").selectOption("direct");
  await expect(dialog.getByLabel("代理出站标记")).toBeHidden();
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByText("直连", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `删除路由规则：${routeName}` }).click();
  dialog = page.getByRole("dialog", { name: "删除路由规则" });
  await dialog.getByRole("button", { name: "确认删除" }).click();
  await expect(page.getByText(routeName, { exact: true })).toBeHidden();

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});
