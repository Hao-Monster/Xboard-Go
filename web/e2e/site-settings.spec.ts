import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword  } from "./support";

interface SiteSettings {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  safe_mode_enable: boolean;
  secure_path: string;
  force_https: boolean;
  subscribe_url: string;
  tos_url: string;
  logo: string;
  currency: string;
  currency_symbol: string;
  stop_register: boolean;
  email_verify: boolean;
  email_whitelist_enable: boolean;
  email_whitelist_suffix: string[];
  email_gmail_limit_enable: boolean;
  register_limit_by_ip_enable: boolean;
  register_limit_count: number;
  register_limit_expire: number;
  password_limit_enable: boolean;
  password_limit_count: number;
  password_limit_expire: number;
}

test("administrator site identity persists into the public shell and can be restored", { tag: "@fresh-server" }, async ({ page, request }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  const unique = Date.now();
  const changed = {
    app_name: `Site parity ${unique}`,
    app_description: `Observable identity ${unique}`,
    app_url: `https://site-${unique}.example.test/`,
    force_https: true,
    subscribe_url: `https://subscriptions-${unique}.example.test/root`,
    tos_url: `https://site-${unique}.example.test/terms/`,
    logo: `https://images.example.test/brand-${unique}.svg`,
    currency: "USD",
    currency_symbol: "$",
    stop_register: true,
    email_whitelist_enable: true,
    email_whitelist_suffix: ["example.test", "gmail.com"],
    email_gmail_limit_enable: true,
    register_limit_by_ip_enable: true,
    register_limit_count: 2,
    register_limit_expire: 30
  };
  let original: SiteSettings | null = null;
  let createdKnowledge: { id: number; revision: number } | null = null;

  try {
    await page.goto(adminEntryPath);
    changed.logo = new URL("/xboard-logo.svg", page.url()).toString();
    await page.getByLabel("邮箱").fill(adminEmail);
    await page.getByLabel("密码").fill(adminPassword);
    await page.getByRole("button", { name: "登录" }).click();
    await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

    original = await getAdminSiteSettings(page);
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "系统设置" })).toBeVisible();
    await page.getByLabel("站点名称").fill(changed.app_name);
    await page.getByLabel("站点描述").fill(changed.app_description);
    await page.getByLabel("站点网址", { exact: true }).fill(changed.app_url);
    await page.getByRole("checkbox", { name: "强制使用 HTTPS 生成公开地址" }).check();
    await page.getByLabel("订阅公开地址").fill(`${changed.subscribe_url}/`);
    await page.getByLabel("用户条款(TOS)URL").fill(changed.tos_url);
    await page.getByLabel("LOGO").fill(changed.logo);
    await page.getByLabel("货币代码").fill(changed.currency.toLowerCase());
    await expect(page.getByLabel("货币代码")).toHaveValue(changed.currency);
    await page.getByLabel("货币符号").fill(changed.currency_symbol);
    await page.getByRole("checkbox", { name: "停止新用户注册" }).check();
    await page.getByRole("checkbox", { name: "邮箱后缀白名单" }).check();
    await page.getByRole("textbox", { name: "邮箱后缀", exact: true }).fill(changed.email_whitelist_suffix.join("\n"));
    await page.getByRole("checkbox", { name: "禁止使用Gmail多别名" }).check();
    await page.getByRole("checkbox", { name: "IP注册限制" }).check();
    await page.getByLabel("注册次数").fill(String(changed.register_limit_count));
    await page.getByLabel("限制时长（分钟）").fill(String(changed.register_limit_expire));
    await page.getByRole("button", { name: "保存站点设置" }).click();
    await expect(page.getByRole("status")).toContainText("站点设置已保存");
    await expect(page.locator(".brand").getByText(changed.app_name, { exact: true })).toBeVisible();
    await expect(page.locator(".topbar img.brand-logo")).toHaveCount(0);
    await expect(page).toHaveTitle(`${changed.app_name} 控制面板`);

    await page.reload();
    await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByLabel("站点名称")).toHaveValue(changed.app_name);
    await expect(page.getByLabel("站点网址", { exact: true })).toHaveValue(changed.app_url);
    await expect(page.getByRole("checkbox", { name: "安全模式（仅允许站点网址的域名访问前端）" })).not.toBeChecked();
    await expect(page.getByLabel("管理员安全路径")).toHaveValue(original.secure_path);
    await expect(page.getByRole("checkbox", { name: "强制使用 HTTPS 生成公开地址" })).toBeChecked();
    await expect(page.getByLabel("订阅公开地址")).toHaveValue(changed.subscribe_url);
    await expect(page.getByLabel("LOGO")).toHaveValue(changed.logo);
    await expect(page.getByLabel("货币代码")).toHaveValue(changed.currency);
    await expect(page.getByLabel("货币符号")).toHaveValue(changed.currency_symbol);
    await expect(page.getByRole("checkbox", { name: "停止新用户注册" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "邮箱后缀白名单" })).toBeChecked();
    await expect(page.getByRole("textbox", { name: "邮箱后缀", exact: true })).toHaveValue(changed.email_whitelist_suffix.join("\n"));
    await expect(page.getByRole("checkbox", { name: "禁止使用Gmail多别名" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "IP注册限制" })).toBeChecked();
    await expect(page.getByLabel("注册次数")).toHaveValue(String(changed.register_limit_count));
    await expect(page.getByLabel("限制时长（分钟）")).toHaveValue(String(changed.register_limit_expire));
    const publicResponse = await request.get("/api/v1/guest/comm/config");
    expect(publicResponse.ok()).toBeTruthy();
    const publicPayload = await publicResponse.json() as { data?: Record<string, unknown> };
    expect(publicPayload.data).toMatchObject({
      app_name: changed.app_name, app_description: changed.app_description, app_url: changed.app_url,
      tos_url: changed.tos_url, logo: changed.logo, email_whitelist_suffix: changed.email_whitelist_suffix
    });
    expect(publicPayload.data).not.toHaveProperty("stop_register");
    expect(publicPayload.data).not.toHaveProperty("safe_mode_enable");
    expect(publicPayload.data).not.toHaveProperty("secure_path");
    expect(publicPayload.data).not.toHaveProperty("force_https");
    expect(publicPayload.data).not.toHaveProperty("subscribe_url");
    expect(publicPayload.data).not.toHaveProperty("email_whitelist_enable");
    expect(publicPayload.data).not.toHaveProperty("email_gmail_limit_enable");
    expect(publicPayload.data).not.toHaveProperty("register_limit_by_ip_enable");
    expect(publicPayload.data).not.toHaveProperty("register_limit_count");
    expect(publicPayload.data).not.toHaveProperty("register_limit_expire");
    expect(publicPayload.data).not.toHaveProperty("password_limit_enable");
    expect(publicPayload.data).not.toHaveProperty("password_limit_count");
    expect(publicPayload.data).not.toHaveProperty("password_limit_expire");

    const subscriptionResponse = await page.request.get("/api/v1/subscription");
    expect(subscriptionResponse.status()).toBe(200);
    const subscriptionPayload = await subscriptionResponse.json() as { data?: { token?: string; subscribe_url?: string } };
    expect(subscriptionPayload.data?.token).toMatch(/^[0-9a-f]{32}$/);
    expect(subscriptionPayload.data?.subscribe_url).toBe(`${changed.subscribe_url}/s/${subscriptionPayload.data?.token}`);
    const appConfigResponse = await page.request.get(
      `/api/v2/client/app/getConfig?token=${encodeURIComponent(subscriptionPayload.data?.token ?? "")}`
    );
    expect(appConfigResponse.status()).toBe(200);
    expect(appConfigResponse.headers()["cache-control"]).toBe("no-store, private");
    const appConfigPayload = await appConfigResponse.json() as { data?: Record<string, unknown> };
    expect(Object.keys(appConfigPayload.data ?? {})).toEqual([
      "app_info", "features", "ui_config", "business_rules", "server_config", "security_config",
      "payment_config", "notification_config", "cache_config", "last_updated", "config_hash"
    ]);
    expect(appConfigPayload.data?.payment_config).toMatchObject({
      currency: changed.currency, currency_symbol: changed.currency_symbol
    });
    expect(appConfigPayload.data?.config_hash).toMatch(/^[0-9a-f]{32}$/);

    const knowledgeResponse = await adminRequest(page, "/api/v1/admin/knowledge", "POST", {
      language: "zh-CN", category: "Brand parity", title: `Brand guide ${unique}`,
      body: "# {{siteName}}", show: true
    });
    expect(knowledgeResponse.status, knowledgeResponse.body).toBe(201);
    const knowledgePayload: unknown = JSON.parse(knowledgeResponse.body);
    const knowledgeData: unknown = typeof knowledgePayload === "object" && knowledgePayload !== null
      ? Reflect.get(knowledgePayload, "data") : null;
    if (typeof knowledgeData !== "object" || knowledgeData === null) throw new Error("created knowledge is invalid");
    createdKnowledge = {
      id: Number(Reflect.get(knowledgeData, "id")), revision: Number(Reflect.get(knowledgeData, "revision"))
    };
    const shareURL = String(Reflect.get(knowledgeData, "share_url"));
    const publicKnowledge = await request.get(shareURL);
    expect(publicKnowledge.status()).toBe(200);
    const publicHTML = await publicKnowledge.text();
    expect(publicHTML).toContain(`Brand guide ${unique} - ${changed.app_name}`);
    expect(publicHTML).toContain(`src="${changed.logo}"`);
    expect(publicHTML).toContain(`alt="${changed.app_name} LOGO"`);
    const publicContent = await request.get(`/guide/${createdKnowledge.id}/content`);
    const contentPayload = await publicContent.json() as { data?: { page_title?: string } };
    expect(contentPayload.data?.page_title).toBe(`Brand guide ${unique} - ${changed.app_name}`);

    await page.getByRole("button", { name: "退出" }).click();
    await page.goto("/");
    await expect(page.getByRole("heading", { name: `登录 ${changed.app_name}` })).toBeVisible();
    const logo = page.getByRole("img", { name: `${changed.app_name} LOGO` });
    await expect(logo).toHaveAttribute("src", changed.logo);
    await expect(logo).toHaveAttribute("referrerpolicy", "no-referrer");
    const tos = page.getByRole("link", { name: "用户条款" });
    await expect(tos).toHaveAttribute("href", changed.tos_url);
    await expect(tos).toHaveAttribute("target", "_blank");
    await expect(tos).toHaveAttribute("rel", /noopener/);
    await expect(page.locator('meta[name="description"]')).toHaveAttribute("content", changed.app_description);
    await page.getByRole("button", { name: "注册账号" }).click();
    await page.getByLabel("邮箱").fill(`closed-${unique}@example.test`);
    await page.getByLabel("密码", { exact: true }).fill(`closed-password-${unique}`);
    await page.getByLabel("再次输入密码").fill(`closed-password-${unique}`);
    await page.getByRole("button", { name: "注册", exact: true }).click();
    await expect(page.getByRole("alert")).toHaveText("本站已关闭注册");
  } finally {
    if (original !== null) {
      await ensureAdmin(page);
      if (createdKnowledge !== null) {
        const removed = await adminRequest(page, `/api/v1/admin/knowledge/${createdKnowledge.id}?revision=${createdKnowledge.revision}`, "DELETE");
        expect(removed.status, removed.body).toBe(204);
      }
      const current = await getAdminSiteSettings(page);
      const restored = await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
        revision: current.revision,
        app_name: original.app_name,
        app_description: original.app_description,
        app_url: original.app_url,
        safe_mode_enable: original.safe_mode_enable,
        secure_path: original.secure_path,
        force_https: original.force_https,
        subscribe_url: original.subscribe_url,
        tos_url: original.tos_url,
        logo: original.logo,
        currency: original.currency,
        currency_symbol: original.currency_symbol,
        stop_register: original.stop_register,
        email_verify: original.email_verify,
        email_whitelist_enable: original.email_whitelist_enable,
        email_whitelist_suffix: original.email_whitelist_suffix,
        email_gmail_limit_enable: original.email_gmail_limit_enable,
        register_limit_by_ip_enable: original.register_limit_by_ip_enable,
        register_limit_count: original.register_limit_count,
        register_limit_expire: original.register_limit_expire,
        password_limit_enable: original.password_limit_enable,
        password_limit_count: original.password_limit_count,
        password_limit_expire: original.password_limit_expire
      });
      expect(restored.status, restored.body).toBe(200);
      await page.reload();
      await expect(page.locator(".brand").getByText(original.app_name, { exact: true })).toBeVisible();
    }
  }
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function ensureAdmin(page: Page) {
  await page.goto(adminEntryPath);
  const serverHeading = page.getByRole("heading", { name: "服务器管理" });
  const email = page.getByLabel("邮箱");
  await expect(serverHeading.or(email)).toBeVisible();
  if (await serverHeading.isVisible()) return;
  await email.fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(serverHeading).toBeVisible();
}

async function getAdminSiteSettings(page: Page): Promise<SiteSettings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  if (typeof payload !== "object" || payload === null) throw new Error("site settings envelope is invalid");
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) throw new Error("site settings data is invalid");
  return data as SiteSettings;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  path = adminAPIPath(path);
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
  }, { path, method, body });
}
