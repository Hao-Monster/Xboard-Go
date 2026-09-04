import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, logoutAndWait  } from "./support";

test("all legacy CAPTCHA providers protect registration and admin secrets never reappear", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await installProviderScriptStubs(page);
  await loginAdmin(page);
  const original = await getSettings(page);
  expect(original.captcha_enable).toBe(false);
  expect(original.recaptcha_secret_configured).toBe(false);
  expect(original.recaptcha_v3_secret_configured).toBe(false);
  expect(original.turnstile_secret_configured).toBe(false);

  try {
    const providers = [
      { type: "recaptcha", siteField: "recaptcha_site_key", site: "e2e-v2-site", secretField: "recaptcha_secret", secret: "e2e-v2-secret" },
      { type: "recaptcha-v3", siteField: "recaptcha_v3_site_key", site: "e2e-v3-site", secretField: "recaptcha_v3_secret", secret: "e2e-v3-secret" },
      { type: "turnstile", siteField: "turnstile_site_key", site: "e2e-turnstile-site", secretField: "turnstile_secret", secret: "e2e-turnstile-secret" }
    ] as const;
    for (const [index, provider] of providers.entries()) {
      if (index === 0) {
        await page.getByRole("button", { name: "系统设置", exact: true }).click();
        await page.getByRole("checkbox", { name: "验证码" }).check();
        await page.getByLabel("验证码类型").selectOption(provider.type);
        await page.getByLabel("reCAPTCHA v2 站点密钥").fill(provider.site);
        await page.getByLabel("reCAPTCHA v2 服务端密钥").fill(provider.secret);
        const updateResponse = page.waitForResponse((response) => response.url().endsWith(adminAPIPath("/api/v1/admin/site-settings")) && response.request().method() === "PUT");
        await page.getByRole("button", { name: "保存站点设置" }).click();
        const update = await updateResponse;
        expect(update.status()).toBe(200);
        const updateBody = await update.text();
        expect(updateBody).not.toContain(provider.secret);
        expect(updateBody).not.toContain("_cipher");
        await expect(page.getByRole("status")).toHaveText("站点设置已保存");
        await expect(page.getByRole("textbox", { name: "reCAPTCHA v2 服务端密钥", exact: true })).toHaveAttribute("placeholder", "已配置，留空保持不变");
      } else {
        const current = await getSettings(page);
        const update = await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
          revision: current.revision, app_name: current.app_name, app_description: current.app_description,
          app_url: current.app_url, tos_url: current.tos_url, logo: current.logo,
          captcha_enable: true, captcha_type: provider.type, [provider.siteField]: provider.site,
          [provider.secretField]: provider.secret, recaptcha_v3_score_threshold: 0.7
        });
        expect(update.status, update.body).toBe(200);
        expect(update.body).not.toContain(provider.secret);
        expect(update.body).not.toContain("_cipher");
      }

      await logoutAndWait(page);
      await page.goto("/#/forgetpassword");
      await page.reload();
      await page.getByLabel("邮箱", { exact: true }).fill(`captcha-code-${Date.now()}-${index}@example.test`);
      const codeRequest = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/password-reset/request"));
      await page.getByRole("button", { name: "发送", exact: true }).click();
      if (provider.type !== "recaptcha-v3") {
        await expect(page.getByRole("dialog", { name: "人机验证" })).toBeVisible();
        await completeInteractiveCaptcha(page);
      }
      const codeResponse = await codeRequest;
      expect(codeResponse.status()).toBe(503);
      expect(await codeResponse.json()).toMatchObject({ error: { code: "mail_unavailable" } });
      await page.goto("/#/register");
      await page.reload();
      await expect(page.getByRole("heading", { name: /注册 / })).toBeVisible();
      const unique = `${Date.now()}-${testInfo.project.name}-${index}`.replace(/[^a-zA-Z0-9-]/g, "-");
      const email = `captcha-${unique}@example.test`;
      const password = `captcha-password-${unique}`;
      await page.getByLabel("邮箱").fill(email);
      await page.getByLabel("密码", { exact: true }).fill(password);
      await page.getByLabel("再次输入密码").fill(password);
      const registration = page.waitForResponse((response) => response.url().endsWith("/api/v1/auth/register"));
      await page.getByRole("button", { name: "注册", exact: true }).click();
      if (provider.type !== "recaptcha-v3") {
        await expect(page.getByRole("dialog", { name: "人机验证" })).toBeVisible();
        await completeInteractiveCaptcha(page);
      }
      expect((await registration).status()).toBe(200);
      await expect(page.getByRole("navigation", { name: "用户导航" })).toBeVisible();
      await logoutAndWait(page);
      await loginAdmin(page);

      const persisted = await getSettings(page);
      expect(persisted.captcha_type).toBe(provider.type);
      expect(JSON.stringify(persisted)).not.toContain(provider.secret);
    }

    const missing = await page.request.post("/api/v1/auth/register", {
      headers: { Origin: new URL(page.url()).origin },
      data: { email: `missing-${Date.now()}@example.test`, password: "missing-password-123", password_confirmation: "missing-password-123" }
    });
    expect(missing.status()).toBe(400);
    expect(await missing.json()).toMatchObject({ error: { code: "captcha_invalid", message: "验证码有误" } });
  } finally {
    if (await page.getByRole("button", { name: "退出" }).count() === 0) await loginAdmin(page);
    const current = await getSettings(page);
    const restored = await adminRequest(page, "/api/v1/admin/site-settings", "PUT", {
      revision: current.revision,
      app_name: original.app_name, app_description: original.app_description, app_url: original.app_url,
      tos_url: original.tos_url, logo: original.logo, stop_register: original.stop_register,
      email_verify: original.email_verify, email_whitelist_enable: original.email_whitelist_enable,
      email_whitelist_suffix: original.email_whitelist_suffix, email_gmail_limit_enable: original.email_gmail_limit_enable,
      register_limit_by_ip_enable: original.register_limit_by_ip_enable, register_limit_count: original.register_limit_count,
      register_limit_expire: original.register_limit_expire, password_limit_enable: original.password_limit_enable,
      password_limit_count: original.password_limit_count, password_limit_expire: original.password_limit_expire,
      invite_force: original.invite_force, invite_gen_limit: original.invite_gen_limit,
      invite_never_expire: original.invite_never_expire, login_with_mail_link_enable: original.login_with_mail_link_enable,
      captcha_enable: false, captcha_type: original.captcha_type, recaptcha_site_key: original.recaptcha_site_key,
      recaptcha_v3_site_key: original.recaptcha_v3_site_key, recaptcha_v3_score_threshold: original.recaptcha_v3_score_threshold,
      turnstile_site_key: original.turnstile_site_key, clear_recaptcha_secret: true,
      clear_recaptcha_v3_secret: true, clear_turnstile_secret: true
    });
    expect(restored.status, restored.body).toBe(200);
  }
  expect(pageErrors).toEqual([]);
});

async function installProviderScriptStubs(page: Page) {
  await page.route("https://www.google.com/recaptcha/api.js**", async (route) => route.fulfill({
    contentType: "application/javascript",
    body: `window.grecaptcha={ready:(callback)=>callback(),execute:(_site,options)=>Promise.resolve('e2e-v3:'+options.action),render:(_element,options)=>{window.__e2eCaptchaComplete=()=>options.callback('e2e-v2-token');return 1;},reset:()=>{}};`
  }));
  await page.route("https://challenges.cloudflare.com/turnstile/v0/api.js**", async (route) => route.fulfill({
    contentType: "application/javascript",
    body: `window.turnstile={render:(_element,options)=>{window.__e2eCaptchaComplete=()=>options.callback('e2e-turnstile:'+options.action);return 'widget';},reset:()=>{},remove:()=>{}};`
  }));
}

async function completeInteractiveCaptcha(page: Page) {
  await expect.poll(() => page.evaluate(() => typeof Reflect.get(window, "__e2eCaptchaComplete"))).toBe("function");
  await page.evaluate(() => {
    const complete: unknown = Reflect.get(window, "__e2eCaptchaComplete");
    if (typeof complete !== "function") throw new Error("CAPTCHA completion hook is missing");
    (complete as () => void)();
  });
}

async function loginAdmin(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

type Settings = Record<string, unknown> & {
  revision: number; app_name: string; app_description: string; app_url: string; tos_url: string; logo: string;
  captcha_enable: boolean; captcha_type: string; recaptcha_site_key: string; recaptcha_secret_configured: boolean;
  recaptcha_v3_site_key: string; recaptcha_v3_score_threshold: number; recaptcha_v3_secret_configured: boolean;
  turnstile_site_key: string; turnstile_secret_configured: boolean;
};

async function getSettings(page: Page): Promise<Settings> {
  const response = await adminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload = JSON.parse(response.body) as { data: Settings };
  return payload.data;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  path = adminAPIPath(path);
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestMethod: method, requestBody: body });
}
