import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage } from "./support";

const testBotToken = "123456789:abcdefghijklmnopqrstuvwxyzABCDE";

interface TelegramSettings {
  revision: number;
  telegram_bot_enable: boolean;
  telegram_bot_token_set: boolean;
  telegram_webhook_url: string;
  telegram_discuss_link: string;
}

test("Telegram settings are complete, write-only, persistent, and safe before provisioning", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page);
  const original = await getTelegramSettings(page);
  test.skip(original.telegram_bot_token_set, "requires an isolated test database without an unrecoverable existing Telegram token");
  try {
    await openTelegramSettings(page);
    for (const label of ["启用 Telegram 绑定引导", "机器人令牌", "Webhook Base URL", "群组链接"]) {
      await expect(page.getByLabel(label, { exact: true }), label).toBeVisible();
    }
    const tokenInput = page.getByLabel("机器人令牌", { exact: true });
    const provisionButton = page.getByRole("button", { name: "一键设置 Webhook", exact: true });
    await expect(tokenInput).toHaveAttribute("type", "password");
    await expect(tokenInput).toHaveValue("");
    await expect(provisionButton).toBeDisabled();

    await page.getByRole("checkbox", { name: "启用 Telegram 绑定引导" }).check();
    await tokenInput.fill(testBotToken);
    await page.getByLabel("Webhook Base URL", { exact: true }).fill("https://panel.example.test");
    await page.getByLabel("群组链接", { exact: true }).fill("https://t.me/xboard_group");
    await expect(provisionButton, "an unsaved token must not enable provisioning").toBeDisabled();

    const saveResponsePromise = page.waitForResponse((response) => response.url().includes("/api/v1/admin/telegram-settings") && response.request().method() === "PUT");
    await page.getByRole("button", { name: "保存 Telegram 设置", exact: true }).click();
    const saveResponse = await saveResponsePromise;
    expect(saveResponse.status()).toBe(200);
    const saveBody = await saveResponse.text();
    expect(saveBody).not.toContain(testBotToken);
    expect(saveBody).not.toContain("cipher");
    await expect(page.getByRole("status")).toContainText("Telegram 设置已保存");
    await expect(tokenInput).toHaveValue("");
    await expect(provisionButton).toBeEnabled();

    await page.reload();
    await openTelegramSettings(page);
    await expect(page.getByLabel("机器人令牌", { exact: true })).toHaveValue("");
    await expect(page.getByLabel("机器人令牌", { exact: true })).toHaveAttribute("placeholder", "已安全保存");
    await expect(page.getByLabel("Webhook Base URL", { exact: true })).toHaveValue("https://panel.example.test");
    await expect(page.getByLabel("群组链接", { exact: true })).toHaveValue("https://t.me/xboard_group");
    await expect(page.getByRole("button", { name: "一键设置 Webhook", exact: true })).toBeEnabled();

    await page.getByRole("button", { name: "清除机器人令牌", exact: true }).click();
    await expect(page.getByRole("button", { name: "一键设置 Webhook", exact: true })).toBeDisabled();
    await page.getByRole("button", { name: "保存 Telegram 设置", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("Telegram 设置已保存");
    await expect(page.getByText("尚未配置机器人令牌。", { exact: true })).toBeVisible();

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    const current = await getTelegramSettings(page);
    const restored = await adminRequest(page, "/api/v1/admin/telegram-settings", "PUT", {
      revision: current.revision,
      telegram_bot_enable: original.telegram_bot_enable,
      clear_telegram_bot_token: true,
      telegram_webhook_url: original.telegram_webhook_url,
      telegram_discuss_link: original.telegram_discuss_link
    });
    expect(restored.status, restored.body).toBe(200);
  }
});

async function login(page: Page) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(adminEmail);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function openTelegramSettings(page: Page) {
  await page.getByRole("button", { name: "Telegram 设置", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Telegram 设置", exact: true })).toBeVisible();
}

async function getTelegramSettings(page: Page): Promise<TelegramSettings> {
  const response = await adminRequest(page, "/api/v1/admin/telegram-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("Telegram settings response is invalid");
  return data as TelegramSettings;
}

async function adminRequest(page: Page, path: string, method: "GET" | "PUT", body?: unknown) {
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? undefined : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestMethod: method, requestBody: body });
}
