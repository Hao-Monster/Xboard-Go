import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage } from "./support";

interface ThemeCatalog {
  active_theme: string;
  revision: number;
  themes: Array<{ name: string; revision: number; is_active: boolean }>;
}

test("administrator safely uploads, previews, configures, activates and deletes a declarative theme", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => { if (response.status() >= 500) errors.push(`${response.status()} ${response.url()}`); });
  const name = `E2ETheme${Date.now()}`;
  await login(page);
  const initial = await getCatalog(page);
  try {
    await page.getByRole("button", { name: "主题配置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "主题配置", exact: true })).toBeVisible();
    await expect(page.getByText(`当前主题：${initial.active_theme}`, { exact: true })).toBeVisible();

    const xboardCard = page.locator(".theme-card").filter({ hasText: "Xboard" });
    const xboardSettingsButton = xboardCard.getByRole("button", { name: "设置 Xboard" });
    await xboardSettingsButton.click();
    const xboardSettings = page.getByRole("dialog", { name: "Xboard 主题设置" });
    await expect(xboardSettings.getByLabel("主题色").locator("option")).toHaveText(["black", "blue", "darkblue", "default"]);
    await page.keyboard.press("Escape");
    await expect(xboardSettings).toHaveCount(0);
    await expect(xboardSettingsButton).toBeFocused();

    const archive = themeArchive(name);
    const uploadResponse = page.waitForResponse((response) => response.url().endsWith("/api/v1/admin/themes") && response.request().method() === "POST");
    await page.getByLabel("上传主题包").setInputFiles({ name: `${name}.zip`, mimeType: "application/zip", buffer: archive });
    expect((await uploadResponse).status()).toBe(201);
    const card = page.locator(".theme-card").filter({ hasText: name });
    await expect(card).toBeVisible();
    await expect(card.getByRole("button", { name: `预览 ${name}` })).toBeVisible();
    await expect(card.getByRole("button", { name: `设置 ${name}` })).toBeVisible();
    await expect(card.getByRole("button", { name: `激活 ${name}` })).toBeVisible();
    await expect(card.getByRole("button", { name: `删除 ${name}` })).toBeVisible();

    const imageResponse = page.waitForResponse((response) => response.url().includes(`/api/v1/theme-assets/${name}/`) && response.status() === 200);
    const previewButton = card.getByRole("button", { name: `预览 ${name}` });
    await previewButton.click();
    const preview = page.getByRole("dialog", { name: `${name} 主题预览` });
    await expect(preview).toBeVisible();
    await expect(preview.getByRole("img", { name: `${name} 预览 1` })).toBeVisible();
    expect((await imageResponse).headers()["cache-control"]).toBe("public, max-age=31536000, immutable");
    const previewClose = preview.getByRole("button", { name: "关闭预览" });
    await expect(previewClose).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(previewClose).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(preview).toHaveCount(0);
    await expect(previewButton).toBeFocused();

    const settingsButton = card.getByRole("button", { name: `设置 ${name}` });
    await settingsButton.click();
    const settings = page.getByRole("dialog", { name: `${name} 主题设置` });
    await expect(settings).toBeVisible();
    await settings.getByLabel("主题色").selectOption("blue");
    await settings.getByLabel("字号").selectOption("large");
    await settings.getByLabel("圆角").selectOption("pill");
    await settings.getByRole("button", { name: "保存主题设置" }).click();
    await expect(page.getByRole("status")).toContainText("主题设置已保存");
    await page.keyboard.press("Escape");
    await expect(settings).toHaveCount(0);
    await expect(settingsButton).toBeFocused();

    await card.getByRole("button", { name: `激活 ${name}` }).click();
    await expect(page.getByRole("status")).toContainText(`${name} 已设为当前主题`);
    await expect(card.getByRole("button", { name: `当前主题 ${name}` })).toBeDisabled();
    await expect(card.getByRole("button", { name: `删除 ${name}` })).toHaveCount(0);
    await expect.poll(() => page.locator("html").getAttribute("data-theme-name")).toBe(name);
    expect(await page.locator("html").evaluate((root) => getComputedStyle(root).getPropertyValue("--theme-primary").trim())).toBe("#93c5fd");

    await page.reload();
    await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();
    await expect.poll(() => page.locator("html").getAttribute("data-theme-name")).toBe(name);
    expect(errors).toEqual([]);
  } finally {
    const current = await getCatalog(page);
    if (current.active_theme === name) {
      const restored = await adminJSON(page, `/api/v1/admin/themes/${encodeURIComponent(initial.active_theme)}/activate`, "POST", { revision: current.revision });
      expect(restored.status, restored.body).toBe(200);
    }
    const remaining = await getCatalog(page);
    if (remaining.themes.some((theme) => theme.name === name)) {
      const deleted = await adminJSON(page, `/api/v1/admin/themes/${encodeURIComponent(name)}`, "DELETE");
      expect(deleted.status, deleted.body).toBe(204);
    }
  }
});

async function login(page: Page) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(adminEmail);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理", exact: true })).toBeVisible();
}

async function getCatalog(page: Page): Promise<ThemeCatalog> {
  const response = await adminJSON(page, "/api/v1/admin/themes", "GET");
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: ThemeCatalog }).data;
}

async function adminJSON(page: Page, path: string, method: "GET" | "POST" | "DELETE", body?: unknown) {
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded) },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestMethod: method, requestBody: body });
}

function themeArchive(name: string): Buffer {
  const preview = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAGElEQVR4nGP4////fwYGBgYmBiYGJgYGBgAABQAB/9Z9JAAAAABJRU5ErkJggg==", "base64");
  const manifest = Buffer.from(JSON.stringify({
    format_version: 1, name, description: "Playwright safe theme", version: "1.0.0",
    images: ["assets/preview.png"], backgrounds: [],
    palettes: {
      default: { background: "#111111", surface: "#18181b", text: "#f4f4f5", muted: "#a1a1aa", primary: "#a5b4fc", primary_text: "#111111", border: "#3f3f46" },
      blue: { background: "#101827", surface: "#172033", text: "#f4f4f5", muted: "#a1a1aa", primary: "#93c5fd", primary_text: "#111111", border: "#334155" }
    },
    default_config: { theme_color: "default", background_url: "", font_scale: "normal", radius: "rounded" }
  }));
  return zipStore([["manifest.json", manifest], ["assets/preview.png", preview]]);
}

function zipStore(entries: Array<[string, Buffer]>): Buffer {
  const localParts: Buffer[] = [];
  const centralParts: Buffer[] = [];
  let offset = 0;
  for (const [name, body] of entries) {
    const filename = Buffer.from(name);
    const checksum = crc32(body);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0); local.writeUInt16LE(20, 4); local.writeUInt16LE(0x0800, 6); local.writeUInt16LE(0, 8);
    local.writeUInt32LE(checksum, 14); local.writeUInt32LE(body.length, 18); local.writeUInt32LE(body.length, 22); local.writeUInt16LE(filename.length, 26);
    localParts.push(local, filename, body);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0); central.writeUInt16LE(20, 4); central.writeUInt16LE(20, 6); central.writeUInt16LE(0x0800, 8);
    central.writeUInt16LE(0, 10); central.writeUInt32LE(checksum, 16); central.writeUInt32LE(body.length, 20); central.writeUInt32LE(body.length, 24);
    central.writeUInt16LE(filename.length, 28); central.writeUInt32LE(offset, 42);
    centralParts.push(central, filename);
    offset += local.length + filename.length + body.length;
  }
  const central = Buffer.concat(centralParts);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0); end.writeUInt16LE(entries.length, 8); end.writeUInt16LE(entries.length, 10);
  end.writeUInt32LE(central.length, 12); end.writeUInt32LE(offset, 16);
  return Buffer.concat([...localParts, central, end]);
}

function crc32(body: Buffer): number {
  let crc = 0xffffffff;
  for (const byte of body) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  return (crc ^ 0xffffffff) >>> 0;
}
