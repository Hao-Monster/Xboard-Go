import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, expectLoginPage } from "./support";

test("administrator controls the fixed trusted plugin inventory and disabled payment providers disappear", async ({ page }) => {
  test.setTimeout(90_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  await login(page);
  try {
    await page.getByRole("button", { name: "插件管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "插件管理", exact: true })).toBeVisible();
    const rows = page.getByRole("region", { name: "可信插件列表" }).locator("tbody tr");
    await expect(rows).toHaveCount(7);
    await expect(page.getByText("不执行上传的 PHP、ZIP 或任意第三方代码。", { exact: false })).toBeVisible();

    await page.getByRole("button", { name: "插件配置：Telegram Bot", exact: true }).click();
    const telegram = page.getByRole("dialog", { name: "Telegram 插件配置" });
    await expect(telegram.getByLabel("工单通知", { exact: true })).toBeChecked();
    await expect(telegram.getByLabel("帮助文案", { exact: true })).not.toHaveValue("");
    await telegram.getByRole("button", { name: "取消", exact: true }).click();

    page.once("dialog", (dialog) => void dialog.accept());
    await page.getByRole("button", { name: "禁用：EPay", exact: true }).click();
    await expect(page.getByRole("button", { name: "启用：EPay", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "支付配置：EPay", exact: true }).click();
    await page.getByRole("button", { name: "添加支付方式", exact: true }).click();
    const payment = page.getByRole("dialog", { name: "添加支付方式" });
    await expect(payment.getByLabel("支付接口", { exact: true }).locator('option[value="EPay"]')).toHaveCount(0);
    await payment.getByRole("button", { name: "取消", exact: true }).click();

    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(1);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await restoreEPay(page);
  }
});

async function login(page: Page) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(adminEmail);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();
}

async function restoreEPay(page: Page) {
  const result = await page.evaluate(async ({ listPath, updatePath }) => {
    const listed = await fetch(listPath, { credentials: "same-origin" });
    if (!listed.ok) return listed.status;
    const payload: unknown = await listed.json();
    const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
    if (!Array.isArray(data)) return 500;
    const plugin: unknown = data.find((item: unknown) => typeof item === "object" && item !== null && Reflect.get(item, "code") === "epay");
    if (typeof plugin !== "object" || plugin === null || Reflect.get(plugin, "enabled") === true) return 200;
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const restored = await fetch(updatePath, {
      method: "PATCH",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: JSON.stringify({ revision: Reflect.get(plugin, "revision"), enabled: true, config: {} })
    });
    return restored.status;
  }, {
    listPath: adminAPIPath("/api/v1/admin/plugins"),
    updatePath: adminAPIPath("/api/v1/admin/plugins/epay")
  });
  expect(result).toBe(200);
}
