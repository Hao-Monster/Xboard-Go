import { randomBytes } from "node:crypto";

import { expect, test, type Page } from "@playwright/test";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test.use({ trace: "off", screenshot: "off", video: "off" });

test("[DIFF-USER-005] legacy and Go user generation persists equivalent administrator-readable fields", async ({ browser }) => {
  test.setTimeout(90_000);
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const unique = `${Date.now()}-${randomBytes(4).toString("hex")}`;
  const emailDomain = "diff.local";
  const legacyPrefix = `lu-${unique}`;
  const legacyUserEmail = `${legacyPrefix}@${emailDomain}`;
  const goUserEmail = `gu-${unique}@${emailDomain}`;
  const password = `Persist-${unique}-pw`;
  const expiredAt = Math.floor(Date.now() / 1000) + 31 * 24 * 60 * 60;
  let legacyAuthorization = "";
  let legacyUserID: number | undefined;
  let goUser: Record<string, unknown> | undefined;

  try {
    legacyAuthorization = await loginLegacyBearer(legacyPage);
    const legacyGenerated = await legacyPage.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization: legacyAuthorization },
      data: {
        email_prefix: legacyPrefix,
        email_suffix: emailDomain,
        password,
        expired_at: expiredAt,
        plan_id: null,
        is_distributor: 0,
        distributor_name: ""
      }
    });
    expect(legacyGenerated.status(), await legacyGenerated.text()).toBe(200);
    const legacyUser = await fetchLegacyUserByEmail(legacyPage, legacyAuthorization, legacyUserEmail);
    legacyUserID = readNumber(legacyUser, "id");

    await loginGoAdministrator(goPage);
    const goGenerated = await goAdminRequest(goPage, "/api/v1/admin/users/generate", "POST", {
      mode: "single",
      email: goUserEmail,
      password,
      plan_id: null,
      expired_at: new Date(expiredAt * 1000).toISOString(),
      is_distributor: false,
      distributor_name: ""
    });
    expect(goGenerated.status, goGenerated.body).toBe(201);
    const generatedItems = readArray(readObject(readJSON(goGenerated.body), "data")["items"]);
    expect(generatedItems).toHaveLength(1);
    const generated = readRecord(generatedItems[0]);
    goUser = await getGoUser(goPage, readNumber(generated, "id"));

    expect(readString(legacyUser, "email")).toBe(legacyUserEmail);
    expect(readString(goUser, "email")).toBe(goUserEmail);
    expect(normalizeGeneratedUser(legacyUser, "legacy")).toEqual({
      banned: false,
      isAdmin: false,
      isStaff: false,
      isDistributor: false,
      distributorName: null,
      groupID: null,
      planID: null,
      transferEnable: 0,
      trafficUpload: 0,
      trafficDownload: 0,
      trafficUsed: 0,
      expiredAt,
      speedLimit: 0,
      deviceLimit: 0
    });
    expect(normalizeGeneratedUser(goUser, "go")).toEqual(normalizeGeneratedUser(legacyUser, "legacy"));

    const legacyLogin = await loginUser(legacyPage, legacyURL, legacyUserEmail, password);
    const goLogin = await loginUser(goPage, goURL, goUserEmail, password);
    expect(readBoolean(readObject(readJSON(legacyLogin), "data"), "is_admin")).toBe(false);
    expect(readBoolean(readObject(readJSON(goLogin), "data"), "is_admin")).toBe(false);
  } finally {
    if (legacyAuthorization && legacyUserID !== undefined) {
      await legacyPage.request.post(legacyAdminAPI("/user/destroy"), {
        headers: { authorization: legacyAuthorization },
        data: { id: legacyUserID }
      }).catch(() => undefined);
    }
    if (goUser !== undefined) {
      await bestEffortGoRequest(goPage, `/api/v1/admin/users/${readNumber(goUser, "id")}/deactivate`, "POST", {
        revision: readNumber(goUser, "revision")
      });
    }
    await legacyContext.close();
    await goContext.close();
  }
});

async function loginLegacyBearer(page: Page): Promise<string> {
  const response = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
    data: { email: legacyEmail, password: legacyPassword }
  });
  const body = await response.text();
  expect(response.status(), body).toBe(200);
  const authorization = readString(readObject(readJSON(body), "data"), "auth_data");
  expect(authorization).toMatch(/^Bearer /);
  return authorization;
}

async function loginGoAdministrator(page: Page): Promise<void> {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible({ timeout: 60_000 });
}

async function fetchLegacyUserByEmail(page: Page, authorization: string, email: string): Promise<Record<string, unknown>> {
  const response = await page.request.post(legacyAdminAPI("/user/fetch"), {
    headers: { authorization },
    data: {
      current: 1,
      pageSize: 20,
      filter: [{ id: "email", value: email }],
      sort: [{ id: "id", desc: true }]
    }
  });
  const body = await response.text();
  expect(response.status(), body).toBe(200);
  const items = readArray(readJSON(body)["data"]);
  const user = items.find((item) => readRecord(item).email === email);
  if (user === undefined) throw new Error(`legacy generated user ${email} was not returned by /user/fetch`);
  return readRecord(user);
}

async function getGoUser(page: Page, userID: number): Promise<Record<string, unknown>> {
  const response = await goAdminRequest(page, `/api/v1/admin/users/${userID}`, "GET");
  expect(response.status, response.body).toBe(200);
  return readObject(readJSON(response.body), "data");
}

async function loginUser(page: Page, baseURL: string, email: string, password: string): Promise<string> {
  const response = await page.request.post(new URL("/api/v1/passport/auth/login", baseURL).toString(), {
    data: { email, password }
  });
  const body = await response.text();
  expect(response.status(), body).toBe(200);
  return body;
}

async function goAdminRequest(page: Page, path: string, method: string, body?: unknown): Promise<{ status: number; body: string }> {
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json",
        "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: goAdminURL(path), requestMethod: method, requestBody: body });
}

async function bestEffortGoRequest(page: Page, path: string, method: string, body?: unknown): Promise<void> {
  await goAdminRequest(page, path, method, body).catch(() => undefined);
}

function normalizeGeneratedUser(user: Record<string, unknown>, source: "legacy" | "go") {
  return {
    banned: readBoolean(user, "banned"),
    isAdmin: readBoolean(user, "is_admin"),
    isStaff: readBoolean(user, "is_staff"),
    isDistributor: readBoolean(user, "is_distributor"),
    distributorName: readNullableString(user, "distributor_name"),
    groupID: readNullableNumber(user, "group_id"),
    planID: readNullableNumber(user, "plan_id"),
    transferEnable: readNumber(user, "transfer_enable"),
    trafficUpload: readNumber(user, source === "legacy" ? "u" : "traffic_upload"),
    trafficDownload: readNumber(user, source === "legacy" ? "d" : "traffic_download"),
    trafficUsed: readNumber(user, source === "legacy" ? "total_used" : "traffic_used"),
    expiredAt: source === "legacy" ? readNullableNumber(user, "expired_at") : toUnixSeconds(readString(user, "expired_at")),
    speedLimit: readNumber(user, "speed_limit"),
    deviceLimit: readNumber(user, "device_limit")
  };
}

function goAdminURL(path: string): string {
  const base = new URL(goURL);
  const securePath = base.pathname.replace(/^\/+|\/+$/g, "");
  if (securePath === "" || !/^\/api\/v[12]\/admin\//.test(path)) return new URL(path, goURL).toString();
  if (path.startsWith("/api/v2/admin/")) {
    return new URL(path.replace("/api/v2/admin/", `/api/v2/${securePath}/`), base.origin).toString();
  }
  const [prefix, resource] = path.split(/(?<=^\/api\/v1\/admin)\//, 2);
  return new URL(`${prefix}/${securePath}/${resource ?? ""}`, base.origin).toString();
}

function legacyAdminAPI(path: string): string {
  const securePath = new URL(legacyURL).pathname.replace(/\/$/, "");
  return new URL(`/api/v2${securePath}${path}`, legacyURL).toString();
}

function requiredEnv(name: string): string {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for legacy parity tests`);
  return value;
}

function readJSON(value: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(value);
  if (!isRecord(parsed)) throw new Error("response is not a JSON object");
  return parsed;
}

function readObject(value: Record<string, unknown>, key: string): Record<string, unknown> {
  return readRecord(value[key]);
}

function readRecord(value: unknown): Record<string, unknown> {
  if (!isRecord(value)) throw new Error("response property is not an object");
  return value;
}

function readArray(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new Error("response property is not an array");
  return value;
}

function readString(value: Record<string, unknown>, key: string): string {
  const result = value[key];
  if (typeof result !== "string") throw new Error(`response property ${key} is not a string`);
  return result;
}

function readNullableString(value: Record<string, unknown>, key: string): string | null {
  const result = value[key];
  if (result === null || result === undefined || result === "") return null;
  if (typeof result !== "string") throw new Error(`response property ${key} is not a string or null`);
  return result;
}

function readNumber(value: Record<string, unknown>, key: string): number {
  const result = Number(value[key]);
  if (!Number.isSafeInteger(result)) throw new Error(`response property ${key} is not an integer`);
  return result;
}

function readNullableNumber(value: Record<string, unknown>, key: string): number | null {
  const result = value[key];
  if (result === null || result === undefined || result === "") return null;
  const number = Number(result);
  if (!Number.isSafeInteger(number)) throw new Error(`response property ${key} is not an integer or null`);
  return number;
}

function readBoolean(value: Record<string, unknown>, key: string): boolean {
  const result = value[key];
  if (typeof result === "boolean") return result;
  if (result === 0 || result === 1) return result === 1;
  throw new Error(`response property ${key} is not a boolean`);
}

function toUnixSeconds(value: string): number | null {
  const millis = Date.parse(value);
  if (Number.isNaN(millis)) return null;
  return Math.floor(millis / 1000);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
