import { randomBytes } from "node:crypto";

import { expect, test, type Page } from "@playwright/test";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test.use({ trace: "off", screenshot: "off", video: "off" });

test("[DIFF-USER-005] legacy and Go user generation is readable from independent admin sessions", async ({ browser }) => {
  test.setTimeout(90_000);
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyReadContext = await browser.newContext({ locale: "zh-CN" });
  const goReadContext = await browser.newContext({ locale: "zh-CN" });
  const legacyUserContext = await browser.newContext({ locale: "zh-CN" });
  const goUserContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyReadPage = await legacyReadContext.newPage();
  const goReadPage = await goReadContext.newPage();
  const legacyUserPage = await legacyUserContext.newPage();
  const goUserPage = await goUserContext.newPage();
  const unique = `${Date.now()}-${randomBytes(4).toString("hex")}`;
  const emailDomain = "diff.local";
  const legacyPrefix = `lu-${unique}`;
  const legacyUserEmail = `${legacyPrefix}@${emailDomain}`;
  const goUserEmail = `gu-${unique}@${emailDomain}`;
  const password = `Persist-${unique}-pw`;
  const expiredAt = Math.floor(Date.now() / 1000) + 31 * 24 * 60 * 60;
  let legacyAuthorization = "";
  let legacyReadAuthorization = "";
  let legacyUserID: number | undefined;
  let goUserID: number | undefined;
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
    await expectResponseStatus(legacyGenerated, 200, "legacy user generation");
    legacyReadAuthorization = await loginLegacyBearer(legacyReadPage);
    const legacyUser = await fetchLegacyUserByEmail(legacyReadPage, legacyReadAuthorization, legacyUserEmail);
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
    expectGoStatus(goGenerated, 201, "Go user generation");
    const generatedItems = readArray(readObject(readJSON(goGenerated.body), "data")["items"]);
    expect(generatedItems).toHaveLength(1);
    const generated = readRecord(generatedItems[0]);
    goUserID = readNumber(generated, "id");
    await loginGoAdministrator(goReadPage);
    goUser = await getGoUser(goReadPage, goUserID);

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

    const legacyLogin = await loginUser(legacyUserPage, legacyURL, legacyUserEmail, password);
    const goLogin = await loginUser(goUserPage, goURL, goUserEmail, password);
    expect(readBoolean(readObject(readJSON(legacyLogin), "data"), "is_admin")).toBe(false);
    expect(readBoolean(readObject(readJSON(goLogin), "data"), "is_admin")).toBe(false);
  } finally {
    const cleanupFailures = await cleanupGeneratedUsers(
      legacyPage,
      legacyAuthorization || legacyReadAuthorization,
      legacyUserID,
      legacyUserEmail,
      goPage,
      goUserID
    );
    const closeFailures = await closeContexts([
      legacyContext,
      goContext,
      legacyReadContext,
      goReadContext,
      legacyUserContext,
      goUserContext
    ]);
    expect([...cleanupFailures, ...closeFailures]).toEqual([]);
  }
});

test("[DIFF-USER-006] legacy and Go banned updates are readable from independent admin sessions", async ({ browser }) => {
  test.setTimeout(90_000);
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyReadContext = await browser.newContext({ locale: "zh-CN" });
  const goReadContext = await browser.newContext({ locale: "zh-CN" });
  const legacyEditReadContext = await browser.newContext({ locale: "zh-CN" });
  const goEditReadContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyReadPage = await legacyReadContext.newPage();
  const goReadPage = await goReadContext.newPage();
  const legacyEditReadPage = await legacyEditReadContext.newPage();
  const goEditReadPage = await goEditReadContext.newPage();
  const unique = `${Date.now()}-${randomBytes(4).toString("hex")}`;
  const emailDomain = "diff.local";
  const legacyPrefix = `le-${unique}`;
  const legacyUserEmail = `${legacyPrefix}@${emailDomain}`;
  const goUserEmail = `ge-${unique}@${emailDomain}`;
  const password = `Edit-${unique}-pw`;
  const expiredAt = Math.floor(Date.now() / 1000) + 31 * 24 * 60 * 60;
  let legacyAuthorization = "";
  let legacyReadAuthorization = "";
  let legacyEditReadAuthorization = "";
  let legacyUserID: number | undefined;
  let goUserID: number | undefined;
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
    await expectResponseStatus(legacyGenerated, 200, "legacy user generation for update");
    legacyReadAuthorization = await loginLegacyBearer(legacyReadPage);
    const legacyUser = await fetchLegacyUserByEmail(legacyReadPage, legacyReadAuthorization, legacyUserEmail);
    legacyUserID = readNumber(legacyUser, "id");
    expect(readBoolean(legacyUser, "banned")).toBe(false);

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
    expectGoStatus(goGenerated, 201, "Go user generation for update");
    const generatedItems = readArray(readObject(readJSON(goGenerated.body), "data")["items"]);
    expect(generatedItems).toHaveLength(1);
    goUserID = readNumber(readRecord(generatedItems[0]), "id");
    await loginGoAdministrator(goReadPage);
    goUser = await getGoUser(goReadPage, goUserID);
    expect(readBoolean(goUser, "banned")).toBe(false);

    const legacyUpdated = await legacyPage.request.post(legacyAdminAPI("/user/update"), {
      headers: { authorization: legacyAuthorization },
      data: { id: legacyUserID, banned: 1 }
    });
    await expectResponseStatus(legacyUpdated, 200, "legacy user banned update");
    legacyEditReadAuthorization = await loginLegacyBearer(legacyEditReadPage);
    const updatedLegacyUser = await fetchLegacyUserByEmail(legacyEditReadPage, legacyEditReadAuthorization, legacyUserEmail);

    const goUpdated = await goAdminRequest(goPage, `/api/v1/admin/users/${goUserID}`, "PATCH", {
      ...goUserUpdatePayload(goUser),
      banned: true
    });
    expectGoStatus(goUpdated, 200, "Go user banned update");
    await loginGoAdministrator(goEditReadPage);
    goUser = await getGoUser(goEditReadPage, goUserID);

    expect(readBoolean(updatedLegacyUser, "banned")).toBe(true);
    expect(readBoolean(goUser, "banned")).toBe(readBoolean(updatedLegacyUser, "banned"));
  } finally {
    const cleanupFailures = await cleanupGeneratedUsers(
      legacyPage,
      legacyAuthorization || legacyReadAuthorization || legacyEditReadAuthorization,
      legacyUserID,
      legacyUserEmail,
      goPage,
      goUserID
    );
    const closeFailures = await closeContexts([
      legacyContext,
      goContext,
      legacyReadContext,
      goReadContext,
      legacyEditReadContext,
      goEditReadContext
    ]);
    expect([...cleanupFailures, ...closeFailures]).toEqual([]);
  }
});

async function loginLegacyBearer(page: Page): Promise<string> {
  const response = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
    data: { email: legacyEmail, password: legacyPassword }
  });
  const body = await expectResponseStatus(response, 200, "legacy administrator login");
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
  const body = await expectResponseStatus(response, 200, "legacy user fetch");
  const items = readArray(readJSON(body)["data"]).map((item) => readRecord(item));
  const matches = items.filter((item) => item.email === email);
  expect(matches, `legacy /user/fetch must return exactly one row for ${email}`).toHaveLength(1);
  return matches[0];
}

async function getGoUser(page: Page, userID: number): Promise<Record<string, unknown>> {
  const response = await goAdminRequest(page, `/api/v1/admin/users/${userID}`, "GET");
  expectGoStatus(response, 200, "Go user readback");
  return readObject(readJSON(response.body), "data");
}

async function cleanupGeneratedUsers(
  legacyPage: Page,
  legacyAuthorization: string,
  legacyUserID: number | undefined,
  legacyUserEmail: string,
  goPage: Page,
  goUserID: number | undefined
): Promise<string[]> {
  const cleanupFailures: string[] = [];
  try {
    if (legacyAuthorization && legacyUserID === undefined) {
      const found = await fetchLegacyUserByEmail(legacyPage, legacyAuthorization, legacyUserEmail).catch((error: Error) => error);
      if (found instanceof Error) {
        cleanupFailures.push(`legacy cleanup lookup failed: ${errorCategory(found)}`);
      } else {
        legacyUserID = readNumber(found, "id");
      }
    }
    if (legacyAuthorization && legacyUserID !== undefined) {
      const destroyed = await legacyPage.request.post(legacyAdminAPI("/user/destroy"), {
        headers: { authorization: legacyAuthorization },
        data: { id: legacyUserID }
      }).catch((error: Error) => error);
      if (destroyed instanceof Error) {
        cleanupFailures.push(`legacy destroy transport failed: ${errorCategory(destroyed)}`);
      } else if (destroyed.status() !== 200) {
        cleanupFailures.push(await responseSummary(destroyed, "legacy destroy"));
      }
    }
  } catch (error) {
    cleanupFailures.push(`legacy cleanup failed: ${errorCategory(error)}`);
  }
  try {
    if (goUserID !== undefined) {
      const latest = await getGoUser(goPage, goUserID).catch((error: Error) => error);
      if (latest instanceof Error) {
        cleanupFailures.push(`Go cleanup read failed: ${errorCategory(latest)}`);
      } else if (readString(latest, "lifecycle_status") === "active") {
        const deactivated = await goAdminRequest(goPage, `/api/v1/admin/users/${goUserID}/deactivate`, "POST", {
          revision: readNumber(latest, "revision")
        }).catch((error: Error) => error);
        if (deactivated instanceof Error) {
          cleanupFailures.push(`Go deactivate transport failed: ${errorCategory(deactivated)}`);
        } else if (deactivated.status !== 200) {
          cleanupFailures.push(goResponseSummary(deactivated, "Go deactivate"));
        }
      }
    }
  } catch (error) {
    cleanupFailures.push(`Go cleanup failed: ${errorCategory(error)}`);
  }
  return cleanupFailures;
}

async function closeContexts(contexts: Array<{ close(): Promise<void> }>): Promise<string[]> {
  const settled = await Promise.allSettled(contexts.map((context) => context.close()));
  return settled.flatMap((result, index) => result.status === "rejected"
    ? [`context ${index} close failed: ${errorCategory(result.reason)}`]
    : []);
}

async function loginUser(page: Page, baseURL: string, email: string, password: string): Promise<string> {
  const response = await page.request.post(new URL("/api/v1/passport/auth/login", baseURL).toString(), {
    data: { email, password }
  });
  const body = await response.text();
  expect(response.status(), safeResponseMessage("user login", response.status(), body)).toBe(200);
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

function goUserUpdatePayload(user: Record<string, unknown>) {
  return {
    revision: readNumber(user, "revision"),
    email: readString(user, "email"),
    group_id: readNullableNumber(user, "group_id"),
    transfer_enable: readNumber(user, "transfer_enable"),
    expired_at: readNullableString(user, "expired_at"),
    speed_limit: readNumber(user, "speed_limit"),
    device_limit: readNumber(user, "device_limit"),
    banned: readBoolean(user, "banned")
  };
}

function normalizeGeneratedUser(user: Record<string, unknown>, source: "legacy" | "go") {
  return {
    banned: readBoolean(user, "banned"),
    isAdmin: readBoolean(user, "is_admin"),
    isStaff: readBoolean(user, "is_staff"),
    isDistributor: readBoolean(user, "is_distributor"),
    distributorName: readNullableString(user, "distributor_name", source === "legacy"),
    groupID: readNullableNumber(user, "group_id"),
    planID: readNullableNumber(user, "plan_id"),
    transferEnable: readNumber(user, "transfer_enable"),
    trafficUpload: readNumber(user, source === "legacy" ? "u" : "traffic_upload"),
    trafficDownload: readNumber(user, source === "legacy" ? "d" : "traffic_download"),
    trafficUsed: readNumber(user, source === "legacy" ? "total_used" : "traffic_used"),
    expiredAt: source === "legacy" ? readNullableNumber(user, "expired_at") : toUnixSeconds(readString(user, "expired_at")),
    speedLimit: readLegacyNullableNumberAsZero(user, "speed_limit", source),
    deviceLimit: readLegacyNullableNumberAsZero(user, "device_limit", source)
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

function readRequired(value: Record<string, unknown>, key: string): unknown {
  if (!Object.hasOwn(value, key)) throw new Error(`response property ${key} is missing`);
  const result = value[key];
  if (result === undefined) throw new Error(`response property ${key} is undefined`);
  return result;
}

function readArray(value: unknown): unknown[] {
  if (!Array.isArray(value)) throw new Error("response property is not an array");
  return value;
}

function readString(value: Record<string, unknown>, key: string): string {
  const result = readRequired(value, key);
  if (typeof result !== "string") throw new Error(`response property ${key} is not a string`);
  return result;
}

function readNullableString(value: Record<string, unknown>, key: string, legacyEmptyStringAsNull = false): string | null {
  const result = readRequired(value, key);
  if (result === null || legacyEmptyStringAsNull && result === "") return null;
  if (typeof result !== "string") throw new Error(`response property ${key} is not a string or null`);
  return result;
}

function readNumber(value: Record<string, unknown>, key: string): number {
  const raw = readRequired(value, key);
  if (raw === null || raw === "") throw new Error(`response property ${key} is null or empty, not an integer`);
  const result = Number(raw);
  if (!Number.isSafeInteger(result)) throw new Error(`response property ${key} is not an integer`);
  return result;
}

function readNullableNumber(value: Record<string, unknown>, key: string): number | null {
  const result = readRequired(value, key);
  if (result === null) return null;
  if (result === "") throw new Error(`response property ${key} is empty, not an integer or null`);
  const number = Number(result);
  if (!Number.isSafeInteger(number)) throw new Error(`response property ${key} is not an integer or null`);
  return number;
}

function readLegacyNullableNumberAsZero(value: Record<string, unknown>, key: string, source: "legacy" | "go"): number {
  if (source === "go") return readNumber(value, key);
  const result = readNullableNumber(value, key);
  return result ?? 0;
}

function readBoolean(value: Record<string, unknown>, key: string): boolean {
  const result = readRequired(value, key);
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

function errorCategory(error: unknown): string {
  if (error instanceof Error) return error.name || "Error";
  return typeof error;
}

async function expectResponseStatus(response: { status(): number; text(): Promise<string> }, expected: number, label: string): Promise<string> {
  const body = await response.text();
  expect(response.status(), safeResponseMessage(label, response.status(), body)).toBe(expected);
  return body;
}

function expectGoStatus(response: { status: number; body: string }, expected: number, label: string): void {
  expect(response.status, safeResponseMessage(label, response.status, response.body)).toBe(expected);
}

async function responseSummary(response: { status(): number; text(): Promise<string> }, label: string): Promise<string> {
  return safeResponseMessage(label, response.status(), await response.text());
}

function goResponseSummary(response: { status: number; body: string }, label: string): string {
  return safeResponseMessage(label, response.status, response.body);
}

function safeResponseMessage(label: string, status: number, body: string): string {
  return `${label} status=${status} ${safeBodySummary(body)}`;
}

function safeBodySummary(body: string): string {
  if (body.trim() === "") return "body=<empty>";
  try {
    return `body=${JSON.stringify(sanitizeJSONSummary(JSON.parse(body) as unknown))}`;
  } catch {
    return `body=<non-json ${Buffer.byteLength(body, "utf8")} bytes>`;
  }
}

function sanitizeJSONSummary(value: unknown): unknown {
  if (Array.isArray(value)) return `<array length=${value.length}>`;
  if (!isRecord(value)) return typeof value;
  const result: Record<string, unknown> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (["auth_data", "password", "token", "uuid", "subscribe_url", "data"].includes(key)) {
      result[key] = "<redacted>";
    } else if (key === "error" && isRecord(entry)) {
      result[key] = sanitizeJSONSummary(entry);
    } else if (["status", "code", "message"].includes(key)) {
      result[key] = typeof entry === "string" || typeof entry === "number" || typeof entry === "boolean" || entry === null ? entry : typeof entry;
    } else {
      result[key] = Array.isArray(entry) ? `<array length=${entry.length}>` : isRecord(entry) ? "<object>" : typeof entry;
    }
  }
  return result;
}
