import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("administrator manages permission groups and routing rules", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();

  const unique = `${Date.now()}`;
  const groupName = `E2E 权限组 ${unique}`;
  const renamedGroup = `${groupName} 已编辑`;
  await page.getByRole("button", { name: "权限组", exact: true }).click();
  await expect(page.getByRole("heading", { name: "权限组" })).toBeVisible();
  const originalGroups = await readAdminResources(page, "/api/v1/admin/server-groups");
  for (const group of originalGroups) await expect(page.getByText(group.name, { exact: true })).toBeVisible();
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
  expect(await readAdminResources(page, "/api/v1/admin/server-groups")).toEqual(originalGroups);

  const routeName = `E2E route ${unique}`;
  await page.getByRole("button", { name: "路由规则", exact: true }).click();
  await expect(page.getByRole("heading", { name: "路由规则" })).toBeVisible();
  const originalRoutes = await readAdminResources(page, "/api/v1/admin/routing-rules");
  for (const route of originalRoutes) await expect(page.getByText(route.name, { exact: true })).toBeVisible();
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
  const updatedRoute = page.getByRole("row").filter({ has: page.getByText(routeName, { exact: true }) });
  await expect(updatedRoute.getByText("直连", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: `删除路由规则：${routeName}` }).click();
  dialog = page.getByRole("dialog", { name: "删除路由规则" });
  await dialog.getByRole("button", { name: "确认删除" }).click();
  await expect(page.getByText(routeName, { exact: true })).toBeHidden();
  expect(await readAdminResources(page, "/api/v1/admin/routing-rules")).toEqual(originalRoutes);

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function readAdminResources(page: Page, path: string): Promise<Array<{ id: number; name: string }>> {
  return page.evaluate(async (requestPath) => {
    const response = await fetch(requestPath, { credentials: "same-origin" });
    if (!response.ok) throw new Error(`resource snapshot failed with ${response.status}`);
    const payload = await response.json() as { data?: Array<{ id?: unknown; name?: unknown; remarks?: unknown }> };
    if (!Array.isArray(payload.data)) throw new Error("resource snapshot data must be an array");
    return payload.data.map((item) => {
      if (!Number.isSafeInteger(item.id)) throw new Error("resource snapshot id must be an integer");
      const name = typeof item.name === "string" ? item.name : item.remarks;
      if (typeof name !== "string" || name === "") throw new Error("resource snapshot name must be non-empty");
      return { id: item.id as number, name };
    });
  }, path);
}
