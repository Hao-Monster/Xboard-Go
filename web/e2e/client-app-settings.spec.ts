import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, expectLoginPage  } from "./support";

interface ClientAppSettings {
  revision: number;
  windows_version: string;
  windows_download_url: string;
  macos_version: string;
  macos_download_url: string;
  android_version: string;
  android_download_url: string;
}

test("client app settings show all legacy fields, guard edits, validate and persist", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await login(page);
  const original = await getClientAppSettings(page);
  try {
    await openClientAppSettings(page);
    for (const label of [
      "Windows 版本", "Windows 下载地址", "macOS 版本", "macOS 下载地址", "Android 版本", "Android 下载地址"
    ]) {
      await expect(page.getByLabel(label, { exact: true }), label).toBeVisible();
    }

    const marker = `${Date.now()}`;
    const windowsVersion = `5.0.${marker.slice(-4)}`;
    await page.getByLabel("Windows 版本", { exact: true }).fill(windowsVersion);
    await page.getByLabel("Windows 下载地址", { exact: true }).fill("http://download.example.test/unsafe.exe");
    expect(await page.getByLabel("Windows 下载地址", { exact: true }).evaluate((input: HTMLInputElement) => input.checkValidity())).toBe(false);
    await page.getByLabel("Windows 下载地址", { exact: true }).fill(`https://download.example.test/windows-${marker}.exe`);

    page.once("dialog", async (dialog) => {
      expect(dialog.message()).toContain("未保存的修改");
      await dialog.dismiss();
    });
    await page.getByRole("button", { name: "系统设置", exact: true }).click();
    await expect(page.getByRole("heading", { name: "客户端版本", exact: true })).toBeVisible();

    const saveResponsePromise = page.waitForResponse((response) => response.url().includes(adminAPIPath("/api/v1/admin/client-app-settings")) && response.request().method() === "PUT");
    await page.getByRole("button", { name: "保存客户端版本", exact: true }).click();
    expect((await saveResponsePromise).status()).toBe(200);
    await expect(page.getByRole("status")).toContainText("客户端版本设置已保存");

    await page.reload();
    await openClientAppSettings(page);
    await expect(page.getByLabel("Windows 版本", { exact: true })).toHaveValue(windowsVersion);
    await expect(page.getByLabel("Windows 下载地址", { exact: true })).toHaveValue(`https://download.example.test/windows-${marker}.exe`);
    await expect(page.getByLabel("macOS 版本", { exact: true })).toHaveValue(original.macos_version);
    await expect(page.getByLabel("Android 版本", { exact: true })).toHaveValue(original.android_version);

    const beforeConflict = await getClientAppSettings(page);
    const staleWindowsVersion = `5.1.${marker.slice(-4)}`;
    const concurrentAndroidVersion = `5.2.${marker.slice(-4)}`;
    await page.getByLabel("Windows 版本", { exact: true }).fill(staleWindowsVersion);
    const concurrent = await adminRequest(page, "/api/v1/admin/client-app-settings", "PUT", {
      revision: beforeConflict.revision,
      windows_version: beforeConflict.windows_version, windows_download_url: beforeConflict.windows_download_url,
      macos_version: beforeConflict.macos_version, macos_download_url: beforeConflict.macos_download_url,
      android_version: concurrentAndroidVersion, android_download_url: beforeConflict.android_download_url
    });
    expect(concurrent.status, concurrent.body).toBe(200);
    const conflictResponsePromise = page.waitForResponse((response) => response.url().includes(adminAPIPath("/api/v1/admin/client-app-settings")) && response.request().method() === "PUT");
    await page.getByRole("button", { name: "保存客户端版本", exact: true }).click();
    expect((await conflictResponsePromise).status()).toBe(409);
    await expect(page.getByRole("alert")).toContainText("设置已被其他管理员修改");

    page.once("dialog", async (dialog) => {
      expect(dialog.message()).toContain("重新加载并放弃");
      await dialog.accept();
    });
    await page.getByRole("button", { name: "重新加载最新设置", exact: true }).click();
    await expect(page.getByLabel("Windows 版本", { exact: true })).toHaveValue(beforeConflict.windows_version);
    await expect(page.getByLabel("Android 版本", { exact: true })).toHaveValue(concurrentAndroidVersion);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    const current = await getClientAppSettings(page);
    const restored = await adminRequest(page, "/api/v1/admin/client-app-settings", "PUT", {
      revision: current.revision,
      windows_version: original.windows_version, windows_download_url: original.windows_download_url,
      macos_version: original.macos_version, macos_download_url: original.macos_download_url,
      android_version: original.android_version, android_download_url: original.android_download_url
    });
    expect(restored.status, restored.body).toBe(200);
  }
});

async function login(page: Page) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(adminEmail);
  await page.getByLabel("密码", { exact: true }).fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function openClientAppSettings(page: Page) {
  await page.getByRole("button", { name: "客户端版本", exact: true }).click();
  await expect(page.getByRole("heading", { name: "客户端版本", exact: true })).toBeVisible();
}

async function getClientAppSettings(page: Page): Promise<ClientAppSettings> {
  const response = await adminRequest(page, "/api/v1/admin/client-app-settings", "GET");
  expect(response.status, response.body).toBe(200);
  const payload: unknown = JSON.parse(response.body);
  const data = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("client app settings response is invalid");
  return data as ClientAppSettings;
}

async function adminRequest(page: Page, path: string, method: "GET" | "PUT", body?: unknown) {
  path = adminAPIPath(path);
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
