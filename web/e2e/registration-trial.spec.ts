import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEmail, adminEntryPath, adminPassword, expectLoginPage, logoutAndWait } from "./support";

test("administrator configures a registration trial and a public registration receives its full entitlement", async ({ page }, testInfo) => {
  const failures: string[] = [];
  page.on("pageerror", (error) => failures.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) failures.push(`${response.status()} ${response.url()}`);
  });
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const planName = `E2E 注册试用 ${unique}`;
  const email = `trial-${unique}@example.test`;
  const password = `trial-password-${unique}`;
  let original: SiteSettings | null = null;

  try {
    await login(page, adminEmail, adminPassword);
    original = await getSiteSettings(page);
    const created = await adminRequest(page, "/api/v1/admin/plans", "POST", {
      group_id: null, transfer_enable: 13, name: planName, speed_limit: 77, content: "",
      reset_traffic_method: 1, capacity_limit: 1, prices: {}, device_limit: 5, tags: []
    });
    expect(created.status, created.body).toBe(201);
    const planID = Number(decodeData(created.body).id);

    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "系统设置" })).toBeVisible();
    await page.getByLabel("注册试用").selectOption(String(planID));
    await expect(page.getByLabel("注册试用时长")).toBeVisible();
    await page.getByLabel("注册试用时长").fill("2");
    await page.getByRole("button", { name: "保存站点设置" }).click();
    await expect(page.getByRole("status")).toHaveText("站点设置已保存");

    await logoutAndWait(page);
    await page.getByRole("button", { name: "注册账号" }).click();
    await page.getByLabel("邮箱").fill(email);
    await page.getByLabel("密码", { exact: true }).fill(password);
    await page.getByLabel("再次输入密码").fill(password);
    const startedAt = Date.now();
    await page.getByRole("button", { name: "注册", exact: true }).click();
    await expect(page.getByRole("heading", { name: "我的订阅", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: planName })).toBeVisible();
    await expect(page.getByText("设备限制").locator("..")).toContainText("5 台");
    await expect(page.getByText("速度限制").locator("..")).toContainText("77 Mbps");
    await expect(page.getByText("已用 0 B / 总计 13.00 GiB", { exact: true })).toBeVisible();

    const subscriptionResponse = await page.request.get("/api/v1/subscription");
    expect(subscriptionResponse.status()).toBe(200);
    const subscription = decodeData(await subscriptionResponse.text());
    expect(subscription).toMatchObject({ plan_id: planID, transfer_enable: 13 * 1024 ** 3, speed_limit: 77, device_limit: 5 });
    const expiresAt = Date.parse(String(subscription.expired_at));
    const nextResetAt = Date.parse(String(subscription.next_reset_at));
    expect(expiresAt).toBeGreaterThanOrEqual(startedAt + 2 * 60 * 60 * 1000 - 5_000);
    expect(expiresAt).toBeLessThanOrEqual(Date.now() + 2 * 60 * 60 * 1000 + 5_000);
    expect(nextResetAt).toBe(expiresAt);
  } finally {
    if (original !== null) {
      await login(page, adminEmail, adminPassword);
      const current = await getSiteSettings(page);
      const restored = await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
        revision: current.revision, app_name: current.app_name, app_description: current.app_description,
        app_url: current.app_url, tos_url: current.tos_url, logo: current.logo,
        try_out_plan_id: original.try_out_plan_id, try_out_hour: original.try_out_hour
      });
      expect(restored.status, restored.body).toBe(200);
    }
  }
  expect(failures).toEqual([]);
});

interface SiteSettings {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  tos_url: string;
  logo: string;
  try_out_plan_id: number;
  try_out_hour: number;
}

async function login(page: Page, email: string, password: string) {
  if (await page.getByRole("button", { name: "退出" }).count() > 0) {
    await logoutAndWait(page);
  }
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function getSiteSettings(page: Page): Promise<SiteSettings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(response.status, response.body).toBe(200);
  return decodeData(response.body) as unknown as SiteSettings;
}

function decodeData(body: string): Record<string, unknown> {
  const payload: unknown = JSON.parse(body);
  if (typeof payload !== "object" || payload === null) throw new Error("API envelope is invalid");
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) throw new Error("API data is invalid");
  return data as Record<string, unknown>;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  const requestPath = adminAPIPath(path);
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: requestPath, method, body });
}
