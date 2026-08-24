import { expect, test, type Page } from "@playwright/test";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

const legacyMenu = [
  ["仪表盘", "#/"],
  ["系统配置", "#/config/system"],
  ["插件管理", "#/config/plugin"],
  ["主题配置", "#/config/theme"],
  ["公告管理", "#/config/notice"],
  ["支付配置", "#/config/payment"],
  ["知识库管理", "#/config/knowledge"],
  ["服务器管理", "#/server/machine"],
  ["节点管理", "#/server/manage"],
  ["权限组管理", "#/server/group"],
  ["路由管理", "#/server/route"],
  ["套餐管理", "#/finance/plan"],
  ["订单管理", "#/finance/order"],
  ["优惠券管理", "#/finance/coupon"],
  ["礼品卡管理", "#/finance/gift-card"],
  ["用户管理", "#/user/manage"],
  ["工单管理", "#/user/ticket"]
] as const;

test("legacy administrator surface remains observable without frontend source", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacy(page);

  for (const [label, href] of legacyMenu) {
    await expect(page.locator(`a[href="${href}"]`), `${label} (${href})`).toBeVisible();
  }

  const machineResponse = page.waitForResponse((response) => response.url().includes("/server/machine/fetch"));
  await page.locator('a[href="#/server/machine"]').click();
  expect((await machineResponse).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "添加服务器" })).toBeVisible();
  for (const column of ["服务器名称", "状态", "负载", "节点数", "最后心跳", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }

  const userResponse = page.waitForResponse((response) => response.url().includes("/user/fetch"));
  await page.locator('a[href="#/user/manage"]').click();
  expect((await userResponse).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "创建用户" })).toBeVisible();
  for (const column of ["邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }

  const noticeResponse = page.waitForResponse((response) => response.url().includes("/notice/fetch"));
  await page.locator('a[href="#/config/notice"]').click();
  const fetchedNotices = await noticeResponse;
  expect(fetchedNotices.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "公告管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: /添加公告/ })).toBeVisible();
  for (const column of ["ID", "显示状态", "标题", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }
  const authorization = fetchedNotices.request().headers().authorization;
  expect(authorization).toBeTruthy();
  const userNotices = await page.request.get(new URL("/api/v1/user/notice/fetch?current=1", legacyURL).toString(), {
    headers: { authorization }
  });
  expect(userNotices.status()).toBe(200);
  const userNoticePayload = await userNotices.json() as { data?: unknown; total?: unknown };
  expect(Array.isArray(userNoticePayload.data)).toBe(true);
  expect(typeof userNoticePayload.total).toBe("number");
  if (!Array.isArray(userNoticePayload.data)) throw new Error("legacy user notice data must be an array");
  expect(userNoticePayload.data.length).toBeLessThanOrEqual(5);
  expect(userNoticePayload.data.every((item) => isVisibleLegacyNotice(item))).toBe(true);

  const clientCatalogResponse = page.waitForResponse((response) => response.url().includes("/client-catalog") && !response.url().includes("/save"));
  await page.locator(".xboard-client-catalog-nav").click();
  const fetchedCatalog = await clientCatalogResponse;
  expect(fetchedCatalog.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "客户端管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "保存全部配置" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Karing/ })).toBeVisible();
  const userCatalog = await page.request.get(new URL("/api/v1/user/client-catalog", legacyURL).toString(), {
    headers: { authorization }
  });
  expect(userCatalog.status()).toBe(200);
  const clientPayload = await userCatalog.json() as { data?: unknown };
  expectLegacyClientCatalog(clientPayload.data);
  expect(errors).toEqual([]);
});

test("implemented Go administrator concepts map to the legacy navigation", async ({ browser }) => {
  const legacyContext = await browser.newContext();
  const goContext = await browser.newContext();
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    for (const [legacyLabel, goLabel] of [
      ["服务器管理", "服务器管理"],
      ["用户管理", "用户管理"],
      ["权限组管理", "权限组"],
      ["路由管理", "路由规则"],
      ["公告管理", "公告管理"],
      ["客户端管理", "客户端管理"]
    ] as const) {
      const legacyEntry = legacyLabel === "客户端管理"
        ? legacyPage.locator(".xboard-client-catalog-nav")
        : legacyPage.getByRole("link", { name: legacyLabel, exact: true });
      await expect(legacyEntry).toBeVisible();
      await expect(goPage.getByRole("button", { name: goLabel, exact: true })).toBeVisible();
    }

    const legacyCatalogRequest = legacyPage.waitForResponse((response) => response.url().includes("/client-catalog") && !response.url().includes("/save"));
    await legacyPage.locator(".xboard-client-catalog-nav").click();
    const legacyCatalogResponse = await legacyCatalogRequest;
    const authorization = legacyCatalogResponse.request().headers().authorization;
    expect(authorization).toBeTruthy();
    const legacyUserResponse = await legacyPage.request.get(new URL("/api/v1/user/client-catalog", legacyURL).toString(), {
      headers: { authorization }
    });
    const goUserResponse = await goPage.request.get(new URL("/api/v1/client-catalog", goURL).toString());
    expect(legacyUserResponse.status()).toBe(200);
    expect(goUserResponse.status()).toBe(200);
    const legacyPayload: unknown = await legacyUserResponse.json();
    const goPayload: unknown = await goUserResponse.json();
    expect(normalizeClientCatalog(readProperty(legacyPayload, "data"))).toEqual(normalizeClientCatalog(readProperty(goPayload, "data")));
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

async function loginLegacy(page: Page) {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  await page.locator('input[name="email"]').fill(legacyEmail);
  await page.locator('input[name="password"]').fill(legacyPassword);
  await page.locator('input[name="password"]').press("Enter");
  await expect(page.locator('a[href="#/server/machine"]')).toBeVisible();
}

async function loginGo(page: Page) {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

function watchErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) errors.push(`${response.status()} ${response.url()}`);
  });
  return errors;
}

function requiredEnv(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for legacy parity tests`);
  return value;
}

function isVisibleLegacyNotice(value: unknown): boolean {
  return typeof value === "object" && value !== null && "show" in value && value.show === true;
}

function expectLegacyClientCatalog(value: unknown): void {
  expect(Array.isArray(value)).toBe(true);
  if (!Array.isArray(value)) return;
  expect(value).toHaveLength(15);
  const entries: unknown[] = value;
  const ids = entries.map((item: unknown) => readStringProperty(item, "id"));
  expect(ids.slice(0, 4)).toEqual(["karing", "happ", "clash-mi", "koalaclash"]);
  const platforms = new Set(["android", "ios", "windows", "macos", "linux"]);
  for (const item of entries) {
    const downloads = readArrayProperty(item, "downloads");
    expect(downloads).not.toBeNull();
    if (downloads === null) continue;
    for (const download of downloads) {
      const platform = readStringProperty(download, "platform");
      const downloadURL = readStringProperty(download, "download_url");
      expect(platforms.has(platform ?? "")).toBe(true);
      expect(downloadURL).toContain("/client-download/");
    }
  }
}

function readStringProperty(value: unknown, key: string): string | null {
  if (typeof value !== "object" || value === null) return null;
  const property: unknown = Reflect.get(value, key);
  return typeof property === "string" ? property : null;
}

function readArrayProperty(value: unknown, key: string): unknown[] | null {
  if (typeof value !== "object" || value === null) return null;
  const property: unknown = Reflect.get(value, key);
  return Array.isArray(property) ? property as unknown[] : null;
}

function readProperty(value: unknown, key: string): unknown {
  if (typeof value !== "object" || value === null) return undefined;
  return Reflect.get(value, key) as unknown;
}

function normalizeClientCatalog(value: unknown) {
  expect(Array.isArray(value)).toBe(true);
  if (!Array.isArray(value)) return [];
  return (value as unknown[]).map((client: unknown) => ({
    id: readStringProperty(client, "id"),
    name: readStringProperty(client, "name"),
    core: readStringProperty(client, "core"),
    description: readStringProperty(client, "description"),
    featured: readProperty(client, "featured") === true,
    hwid: readProperty(client, "hwid") === true,
    downloads: (readArrayProperty(client, "downloads") ?? []).map((download: unknown) => ({
      platform: readStringProperty(download, "platform"),
      source: readStringProperty(download, "source"),
      downloadPath: pathOf(readStringProperty(download, "download_url")),
      hasCloud: readStringProperty(download, "cloud_url") !== null,
      hasTutorial: readStringProperty(download, "tutorial_url") !== null
    }))
  }));
}

function pathOf(address: string | null): string | null {
  if (address === null) return null;
  return new URL(address, "https://panel.invalid").pathname;
}
