import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

interface NodeRecord {
  id: number;
  revision: number;
  name: string;
}

test("administrator node management preserves the observed Xboard workflow on every viewport", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await loginAdministrator(page);
  const unique = `${testInfo.project.name}-${Date.now()}`;
  const prefix = `Node parity ${unique}`;
  const firstName = `${prefix} A`;
  const secondName = `${prefix} B`;
  const editedName = `${prefix} A edited`;

  try {
    await createNode(page, { name: firstName, type: "vless", host: "node-a.example.test", port: "443", sort: 10 });
    await createNode(page, { name: secondName, type: "trojan", host: "node-b.example.test", port: "8443", sort: 20 });

    await page.getByRole("button", { name: "节点管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "节点管理" })).toBeVisible();
    await page.getByLabel("搜索节点").fill(prefix);
    await page.getByRole("button", { name: "查询节点" }).click();
    await expect(page.getByText(firstName, { exact: true })).toBeVisible();
    await expect(page.getByText(secondName, { exact: true })).toBeVisible();

    const expectedColumns = ["节点ID", "显隐", "节点", "部署方式", "地址", "在线人数", "倍率", "权限组", "流量使用", "操作"];
    expect(await page.getByRole("table", { name: "节点列表" }).locator("th").allTextContents()).toEqual(expectedColumns);
    await expect(page.getByLabel("协议筛选").locator("option")).toHaveText([
      "全部", "Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"
    ]);
    await expect(page.getByText("第 1 / 1 页 · 共 2 个节点", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `编辑节点：${firstName}` }).click();
    const editDialog = page.getByRole("dialog", { name: "编辑节点" });
    await editDialog.getByLabel("节点名称").fill(editedName);
    await editDialog.getByLabel("节点地址").fill("node-a-updated.example.test");
    await editDialog.getByRole("button", { name: "保存修改" }).click();
    await expect(editDialog).toBeHidden();
    await expect(page.getByText(editedName, { exact: true })).toBeVisible();
    await expect(page.getByText("node-a-updated.example.test:443", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `复制节点：${editedName}` }).click();
    const copiedName = `${editedName} - 副本`;
    await expect(page.getByText(copiedName, { exact: true })).toBeVisible();
    const copyRow = page.locator("tr", { has: page.getByText(copiedName, { exact: true }) });
    await expect(copyRow.getByText("隐藏", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `上移节点：${secondName}` }).click();
    await expect.poll(async () => {
      const names = await nodeNames(page);
      return names.indexOf(secondName) < names.indexOf(copiedName);
    }).toBe(true);
    await page.getByRole("button", { name: `上移节点：${secondName}` }).click();
    await expect.poll(async () => {
      const names = await nodeNames(page);
      return names.indexOf(secondName) < names.indexOf(editedName);
    }).toBe(true);

    await page.getByRole("checkbox", { name: `选择节点：${editedName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量隐藏" }).click();
    const editedRow = page.locator("tr", { has: page.getByText(editedName, { exact: true }) });
    await expect(editedRow.getByText("隐藏", { exact: true })).toBeVisible();

    await page.getByRole("checkbox", { name: `选择节点：${secondName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量重置流量" }).click();
    const resetDialog = page.getByRole("alertdialog", { name: "重置节点流量" });
    await expect(resetDialog).toContainText("当前累计流量归零");
    await resetDialog.getByRole("button", { name: "确认重置" }).click();
    await expect(resetDialog).toBeHidden();

    await page.getByRole("checkbox", { name: `选择节点：${editedName}`, exact: true }).check();
    await page.getByRole("checkbox", { name: `选择节点：${secondName}`, exact: true }).check();
    await page.getByRole("checkbox", { name: `选择节点：${copiedName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量删除" }).click();
    const deleteDialog = page.getByRole("alertdialog", { name: "删除节点" });
    await expect(deleteDialog).toContainText("选中的 3 个节点");
    await deleteDialog.getByRole("button", { name: "确认删除" }).click();
    await expect(deleteDialog).toBeHidden();
    await expect(page.getByText("没有符合条件的节点。", { exact: true })).toBeVisible();

    const viewport = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth
    }));
    expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.viewportWidth);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await deleteFixtureNodes(page, prefix);
  }
});

async function loginAdministrator(page: Page) {
  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function createNode(page: Page, input: { name: string; type: string; host: string; port: string; sort: number }) {
  const result = await adminFetch(page, "/api/v1/admin/nodes", "POST", {
    ...input,
    show: true,
    enabled: true
  });
  expect(result.status, result.body).toBe(201);
}

async function deleteFixtureNodes(page: Page, prefix: string) {
  const listed = await adminFetch(page, `/api/v1/admin/nodes?page=1&page_size=500&q=${encodeURIComponent(prefix)}`, "GET");
  if (listed.status !== 200) return;
  const payload: unknown = JSON.parse(listed.body);
  const data = objectProperty(payload, "data");
  const items = objectProperty(data, "items");
  if (!Array.isArray(items)) return;
  const targets = items.flatMap((candidate): NodeRecord[] => {
    if (typeof candidate !== "object" || candidate === null) return [];
    const id = Reflect.get(candidate, "id");
    const revision = Reflect.get(candidate, "revision");
    const name = Reflect.get(candidate, "name");
    return Number.isSafeInteger(id) && Number.isSafeInteger(revision) && typeof name === "string" && name.startsWith(prefix)
      ? [{ id: id as number, revision: revision as number, name }]
      : [];
  });
  if (targets.length > 0) {
    await adminFetch(page, "/api/v1/admin/nodes/bulk-delete", "POST", {
      targets: targets.map(({ id, revision }) => ({ id, revision }))
    });
  }
}

async function adminFetch(page: Page, path: string, method: "GET" | "POST", body?: unknown) {
  return page.evaluate(async ({ target, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const headers: Record<string, string> = {};
    if (requestBody !== undefined) {
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = decodeURIComponent(encoded);
    }
    const response = await fetch(target, {
      method: requestMethod,
      credentials: "same-origin",
      headers,
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { target: path, requestMethod: method, requestBody: body });
}

function objectProperty(value: unknown, key: string): unknown {
  return typeof value === "object" && value !== null ? Reflect.get(value, key) : undefined;
}

async function nodeNames(page: Page): Promise<string[]> {
  return page.locator("tbody tr td[data-label='节点'] strong").allTextContents();
}
