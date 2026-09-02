import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

test("node compatibility settings are complete, one-time, conflict-safe, and secret-free", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  await login(page);
  await page.getByRole("button", { name: "节点配置", exact: true }).click();
  await expect(page.getByRole("heading", { name: "节点配置" })).toBeVisible();

  for (const label of ["通讯密钥操作", "拉取间隔（秒）", "推送间隔（秒）", "WebSocket 地址"]) {
    await expect(page.getByLabel(label, { exact: true }), label).toBeVisible();
  }
  await expect(page.getByLabel("设备限制模式", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "启用节点 WebSocket" })).toBeVisible();
  await expect(page.getByText("尚未配置。密钥仅在替换或生成成功后显示一次。")).toBeVisible();

  await page.getByLabel("通讯密钥操作").selectOption("generate");
  await page.getByLabel("拉取间隔（秒）").fill("31");
  await page.getByLabel("推送间隔（秒）").fill("29");
  await page.getByLabel("WebSocket 地址").fill("wss://panel.example.test/ws");
  const generatedResponsePromise = page.waitForResponse((response) => response.url().includes(adminAPIPath("/api/v1/admin/node-agent-settings")) && response.request().method() === "PUT");
  await page.getByRole("button", { name: "保存节点配置" }).click();
  const generatedResponse = await generatedResponsePromise;
  expect(generatedResponse.status()).toBe(200);
  const generatedBody = await generatedResponse.text();
  expect(generatedBody).not.toContain("server_token_hash");
  const status = page.getByRole("status");
  await expect(status).toContainText("请立即保存通讯密钥");
  const token = (await status.locator("code").textContent()) ?? "";
  expect(token).toMatch(/^[A-Za-z0-9_-]{48}$/);
  await status.getByRole("button", { name: "我已保存" }).click();
  await expect(page.getByText(token, { exact: true })).toHaveCount(0);

  await page.reload();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  await page.getByRole("button", { name: "节点配置", exact: true }).click();
  await expect(page.getByText(/已配置（前缀 .+…）/)).toBeVisible();
  expect(await page.locator("body").textContent()).not.toContain(token);
  const readBack = await adminRequest(page, "GET");
  expect(readBack.status).toBe(200);
  expect(readBack.body).not.toContain(token);
  expect(readBack.body).not.toContain("server_token_hash");

  const current = readData(readBack.body);
  const concurrent = await adminRequest(page, "PUT", {
    revision: current.revision,
    server_pull_interval: 32,
    server_push_interval: 30,
    device_limit_mode: 0,
    server_ws_enable: false,
    server_ws_url: ""
  });
  expect(concurrent.status, concurrent.body).toBe(200);
  await page.getByRole("button", { name: "保存节点配置" }).click();
  await expect(page.getByRole("alert")).toContainText("设置已被其他管理员修改");
  await page.getByRole("button", { name: "刷新最新设置" }).click();
  await expect(page.getByLabel("拉取间隔（秒）")).toHaveValue("32");

  await page.getByLabel("通讯密钥操作").selectOption("clear");
  await page.getByLabel("拉取间隔（秒）").fill("60");
  await page.getByLabel("推送间隔（秒）").fill("60");
  await page.getByLabel("WebSocket 地址").fill("");
  await page.getByRole("button", { name: "保存节点配置" }).click();
  await expect(page.getByRole("status")).toContainText("节点配置已保存");
  await expect(page.getByText("尚未配置。密钥仅在替换或生成成功后显示一次。")).toBeVisible();

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function adminRequest(page: Page, method: "GET" | "PUT", body?: Record<string, unknown>) {
  return page.evaluate(async ({ requestMethod, requestBody, path }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(path, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestMethod === "PUT" ? { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) } : undefined,
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestMethod: method, requestBody: body, path: adminAPIPath("/api/v1/admin/node-agent-settings") });
}

function readData(body: string): { revision: number } {
  const payload: unknown = JSON.parse(body);
  if (typeof payload !== "object" || payload === null) throw new Error("node settings payload is invalid");
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) throw new Error("node settings data is invalid");
  return { revision: Number(Reflect.get(data, "revision")) };
}
