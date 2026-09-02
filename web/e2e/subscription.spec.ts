import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, expectLoginPage, logoutAndWait } from "./support";

interface SubscriptionSettings {
  revision: number;
  path: string;
  show_info: boolean;
  show_protocol: boolean;
  templates: Record<string, string>;
}

interface SubscriptionPolicySettings {
  revision: number;
  plan_change_enable: boolean;
  reset_traffic_method: number;
  surplus_enable: boolean;
  new_order_event_id: number;
  renew_order_event_id: number;
  change_order_event_id: number;
  default_remind_expire: boolean;
  default_remind_traffic: boolean;
}

test("administrator subscription settings drive the user dashboard, QR, output path, and secure reset", async ({ page, request }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });
  const unique = Date.now();
  const email = `subscription-e2e-${unique}@example.test`;
  const password = `subscription-e2e-password-${unique}`;
  const path = `feeds_${unique}`;
  let original: SubscriptionSettings | null = null;
  let originalPolicy: SubscriptionPolicySettings | null = null;

  try {
    await login(page, adminEmail, adminPassword);
    original = await getSubscriptionSettings(page);
    originalPolicy = await getSubscriptionPolicySettings(page);
    await page.getByRole("button", { name: "订阅设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "订阅设置" })).toBeVisible();
    const planChange = page.getByRole("checkbox", { name: "允许用户更改订阅" });
    await expect(planChange).toBeVisible();
    await expect(page.getByRole("combobox", { name: "月流量重置方式" })).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "开启折抵方案" })).toBeVisible();
    await expect(page.getByRole("combobox", { name: "当订阅新购时触发事件" })).toBeVisible();
    await expect(page.getByRole("combobox", { name: "当订阅续费时触发事件" })).toBeVisible();
    await expect(page.getByRole("combobox", { name: "当订阅变更时触发事件" })).toBeVisible();
    await planChange.setChecked(!originalPolicy.plan_change_enable);
    await page.getByRole("button", { name: "保存订阅策略" }).click();
    await expect(page.getByText("订阅策略已保存", { exact: true })).toBeVisible();
    expect((await getSubscriptionPolicySettings(page)).plan_change_enable).toBe(!originalPolicy.plan_change_enable);

    await expect(page.getByLabel("订阅路径")).toHaveValue(original.path);
    await page.getByLabel("订阅路径").fill(path);
    await page.getByRole("checkbox", { name: "在订阅中展示订阅信息" }).check();
    await page.getByRole("checkbox", { name: "在线路名称中显示协议名称" }).check();
    await page.getByRole("button", { name: "Clash", exact: true }).click();
    await expect(page.getByLabel("Clash 订阅模板")).toHaveValue(original.templates.clash);
    await page.getByRole("button", { name: "保存订阅设置" }).click();
    await expect(page.getByText("订阅设置已保存", { exact: true })).toBeVisible();

    const created = await adminRequest(page, "/api/v1/admin/users", "POST", {
      email, password, group_id: null, transfer_enable: 10 * 1024 * 1024 * 1024,
      expired_at: null, speed_limit: 100, device_limit: 3, banned: false
    });
    expect(created.status, created.body).toBe(201);

    await logoutAndWait(page);
    await login(page, email, password);
    await expect(page.getByRole("heading", { name: "我的订阅" })).toBeVisible();
    await expect(page.getByText("暂无订阅套餐")).toBeVisible();
    await expect(page.getByText("已用 0 B / 总计 10.00 GiB")).toBeVisible();
    const address = page.getByRole("textbox", { name: "订阅地址" });
    const oldURL = await address.inputValue();
    expect(new URL(oldURL).pathname).toMatch(new RegExp(`^/${path}/[0-9a-f]{32}$`));

    await page.getByRole("button", { name: "一键订阅" }).click();
    const importDialog = page.getByRole("dialog", { name: "一键订阅" });
    await expect(importDialog.getByRole("img", { name: "订阅二维码" })).toHaveAttribute("src", /^data:image\/svg\+xml;base64,/);
    await expect(importDialog.getByRole("link", { name: "导入到 Clash" })).toHaveAttribute("href", /^clash:\/\/install-config\?url=/);
    await expect(importDialog.getByRole("link", { name: "导入到 Hiddify" })).toHaveAttribute("href", /^hiddify:\/\/import\//);
    await importDialog.getByRole("button", { name: "关闭一键订阅" }).click();

    await page.getByRole("button", { name: "重置订阅信息" }).click();
    const resetDialog = page.getByRole("dialog", { name: "重置订阅信息" });
    await expect(resetDialog.getByText(/旧订阅地址会立即失效/)).toBeVisible();
    await resetDialog.getByRole("button", { name: "确认重置" }).click();
    await expect(page.getByRole("status")).toHaveText("订阅信息已重置");
    const newURL = await address.inputValue();
    expect(newURL).not.toBe(oldURL);
    expect((await request.get(subscriptionAPIURL(oldURL))).status()).toBe(403);
    expect((await request.get(subscriptionAPIURL(newURL))).status()).toBe(200);
  } finally {
    if (original !== null) {
      await page.goto("/");
      const logout = page.getByRole("button", { name: "退出" });
      const loginEmail = page.getByLabel("邮箱", { exact: true });
      await expect(logout.or(loginEmail)).toBeVisible();
      if (await logout.isVisible()) await logoutAndWait(page);
      await login(page, adminEmail, adminPassword);
      const current = await getSubscriptionSettings(page);
      const restored = await adminRequest(page, "/api/v1/admin/subscription-settings", "PUT", {
        revision: current.revision,
        path: original.path,
        show_info: original.show_info,
        show_protocol: original.show_protocol,
        templates: original.templates
      });
      expect(restored.status, restored.body).toBe(200);
    }
    if (originalPolicy !== null) {
      const current = await getSubscriptionPolicySettings(page);
      const restored = await adminRequest(page, "/api/v1/admin/subscription-policy-settings", "PUT", {
        revision: current.revision,
        plan_change_enable: originalPolicy.plan_change_enable,
        reset_traffic_method: originalPolicy.reset_traffic_method,
        surplus_enable: originalPolicy.surplus_enable,
        new_order_event_id: originalPolicy.new_order_event_id,
        renew_order_event_id: originalPolicy.renew_order_event_id,
        change_order_event_id: originalPolicy.change_order_event_id,
        default_remind_expire: originalPolicy.default_remind_expire,
        default_remind_traffic: originalPolicy.default_remind_traffic
      });
      expect(restored.status, restored.body).toBe(200);
    }
  }
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(email === adminEmail ? adminEntryPath : "/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出" })).toBeVisible();
}

async function getSubscriptionSettings(page: Page): Promise<SubscriptionSettings> {
  const response = await adminRequest(page, "/api/v1/admin/subscription-settings", "GET");
  expect(response.status, response.body).toBe(200);
  return decodeData<SubscriptionSettings>(response.body);
}

async function getSubscriptionPolicySettings(page: Page): Promise<SubscriptionPolicySettings> {
  const response = await adminRequest(page, "/api/v1/admin/subscription-policy-settings", "GET");
  expect(response.status, response.body).toBe(200);
  return decodeData<SubscriptionPolicySettings>(response.body);
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: adminAPIPath(path), method, body });
}

function decodeData<T>(body: string): T {
  const payload: unknown = JSON.parse(body);
  if (typeof payload !== "object" || payload === null) throw new Error("response envelope is invalid");
  return Reflect.get(payload, "data") as T;
}

function subscriptionAPIURL(value: string): string {
  const token = new URL(value).pathname.split("/").filter(Boolean).at(-1);
  if (token === undefined || !/^[0-9a-f]{32}$/.test(token)) throw new Error("subscription URL token is invalid");
  return `/api/v1/client/subscribe?token=${token}`;
}
