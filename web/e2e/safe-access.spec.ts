import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

interface SiteAccessSettings {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  safe_mode_enable: boolean;
  secure_path: string;
  tos_url: string;
  logo: string;
}

test("packaged frontend safe mode protects valid entries, rejects unknown routes, and leaves API and assets reachable", { tag: "@fresh-server" }, async ({ page, request, baseURL }) => {
  test.skip(process.env.XBOARD_E2E_EXTERNAL_SERVER !== "true", "safe mode is enforced by the packaged Go frontend server");
  if (baseURL === undefined) throw new Error("packaged base URL is required");

  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const original = await getSiteAccessSettings(page);
  try {
    const enabled = await adminRequest(page, {
      revision: original.revision,
      app_name: original.app_name,
      app_description: original.app_description,
      app_url: baseURL,
      safe_mode_enable: true,
      secure_path: original.secure_path,
      tos_url: original.tos_url,
      logo: original.logo
    });
    expect(enabled.status, enabled.body).toBe(200);

    const matchingRoot = await request.get("/");
    expect(matchingRoot.status()).toBe(200);
    const deniedRoot = await request.get("/", { headers: { Host: "attacker.example.test" } });
    expect(deniedRoot.status()).toBe(403);
    const deniedRoute = await request.get("/account/security", { headers: { Host: "attacker.example.test" } });
    expect(deniedRoute.status()).toBe(404);
    const publicAsset = await request.get("/xboard-logo.svg", { headers: { Host: "attacker.example.test" } });
    expect(publicAsset.status()).toBe(200);
    const publicAPI = await request.get("/api/v1/guest/comm/config", { headers: { Host: "attacker.example.test" } });
    expect(publicAPI.status()).toBe(200);

    await page.reload();
    await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByRole("checkbox", { name: "安全模式（仅允许站点网址的域名访问前端）" })).toBeChecked();
    await expect(page.getByLabel("管理员安全路径")).toHaveValue(original.secure_path);
  } finally {
    const current = await getSiteAccessSettings(page);
    const restored = await adminRequest(page, {
      revision: current.revision,
      app_name: original.app_name,
      app_description: original.app_description,
      app_url: original.app_url,
      safe_mode_enable: original.safe_mode_enable,
      secure_path: original.secure_path,
      tos_url: original.tos_url,
      logo: original.logo
    });
    expect(restored.status, restored.body).toBe(200);
  }
  expect(pageErrors).toEqual([]);
});

async function getSiteAccessSettings(page: Page): Promise<SiteAccessSettings> {
  const response = await page.evaluate(async (path) => {
    const result = await fetch(path, { credentials: "same-origin" });
    return { status: result.status, body: await result.text() };
  }, adminAPIPath("/api/v1/admin/site-settings"));
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data: unknown = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("site settings data is invalid");
  return data as SiteAccessSettings;
}

async function adminRequest(page: Page, body: SiteAccessSettings) {
  return page.evaluate(async ({ requestBody, path }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(path, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestBody: body, path: adminAPIPath("/api/v1/admin/site-settings") });
}
