import { randomBytes, randomUUID } from "node:crypto";

import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";

test.use({ trace: "off", screenshot: "off", video: "off" });

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test("legacy and Go enforce one open ordinary ticket per user", async ({ browser }) => {
  test.setTimeout(120_000);
  const unique = randomUUID();
  const subject = `Single open ${unique}`;
  const duplicateSubject = `Single open duplicate ${unique}`;
  const thirdSubject = `Single open after close ${unique}`;
  const firstMessage = `First ${unique}`;
  const duplicateMessage = `Duplicate ${unique}`;
  const thirdMessage = `Third ${unique}`;
  const legacyUser = { email: `ticket-single-open-${unique}@legacy.local`, password: randomBytes(24).toString("base64url") };
  const goUser = { email: `ticket-single-open-${unique}@go.local`, password: randomBytes(24).toString("base64url") };
  let legacyUserID: number | null = null;
  let goUserID: number | null = null;
  let legacyAuthorization = "";
  const legacyAdminContext = await browser.newContext({ locale: "zh-CN" });
  const goAdminContext = await browser.newContext({ locale: "zh-CN" });
  const legacyAdminPage = await legacyAdminContext.newPage();
  const goAdminPage = await goAdminContext.newPage();

  try {
    legacyAuthorization = await loginLegacyAdmin(legacyAdminPage);
    await loginGoAdmin(goAdminPage);
    const legacyGenerated = await legacyAdminPage.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization: legacyAuthorization },
      data: { email_prefix: legacyUser.email.split("@", 1)[0], email_suffix: "legacy.local", password: legacyUser.password }
    });
    expect(legacyGenerated.status()).toBe(200);
    const legacyUsersResponse = await legacyAdminPage.request.get(legacyAdminAPI(`/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(legacyUser.email)}`), { headers: { authorization: legacyAuthorization } });
    const legacyUsers = await readJSON(legacyUsersResponse);
    legacyUserID = requiredPositiveNumber(requiredArrayProperty(legacyUsers, "data").find((item) => readStringProperty(item, "email") === legacyUser.email), "id");
    const goGenerated = await goAdminRequest(goAdminPage, "/api/v1/admin/users/generate", {
      mode: "single", email: goUser.email, count: 1, password: goUser.password,
      plan_id: null, expired_at: new Date(Date.now() + 30 * 24 * 60 * 60 * 1_000).toISOString(), download_csv: false, is_distributor: false
    });
    expect(goGenerated.status).toBe(201);
    goUserID = requiredPositiveNumber(requiredArrayProperty(readProperty(parseJSONBody(goGenerated.body), "data"), "items")[0], "id");

    const exerciseResults = await Promise.allSettled([
      exerciseLegacy(browser, legacyUser.email, legacyUser.password, subject, duplicateSubject, thirdSubject, firstMessage, duplicateMessage, thirdMessage),
      exerciseGo(browser, goUser.email, goUser.password, subject, duplicateSubject, thirdSubject, firstMessage, duplicateMessage, thirdMessage)
    ]);
    const failedExercise = exerciseResults.find((result): result is PromiseRejectedResult => result.status === "rejected");
    if (failedExercise !== undefined) {
      throw new Error(`ticket parity exercise failed: ${failedExercise.reason instanceof Error ? failedExercise.reason.message : "unknown error"}`);
    }
  } finally {
    const cleanupErrors: string[] = [];
    try {
      if (legacyUserID !== null) {
        const response = await legacyAdminPage.request.post(legacyAdminAPI("/user/destroy"), { headers: { authorization: legacyAuthorization }, data: { id: legacyUserID } });
        if (response.status() !== 200) cleanupErrors.push(`legacy cleanup status ${response.status()}`);
      }
    } catch { cleanupErrors.push("legacy cleanup threw"); }
    try {
      if (goUserID !== null) {
        const detail = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}`);
        const revision = requiredPositiveNumber(readProperty(parseJSONBody(detail.body), "data"), "revision");
        const response = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}/deactivate`, { revision });
        if (response.status !== 200) cleanupErrors.push(`Go cleanup status ${response.status}`);
      }
    } catch { cleanupErrors.push("Go cleanup threw"); }
    const contextClosures = await Promise.allSettled([legacyAdminContext.close(), goAdminContext.close()]);
    if (contextClosures.some((result) => result.status === "rejected")) cleanupErrors.push("admin context close failed");
    expect(cleanupErrors, "test fixture cleanup").toEqual([]);
  }
});

async function exerciseLegacy(browser: Browser, email: string, password: string, subject: string, duplicateSubject: string, thirdSubject: string, firstMessage: string, duplicateMessage: string, thirdMessage: string) {
  const context = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginLegacyUserAPI(context.request, email, password);
    const headers = { authorization };
    const first = await context.request.post(legacyUserAPI("/ticket/save"), { headers, data: { subject, level: 2, message: firstMessage } });
    expect(first.status()).toBe(200);
    const firstID = await legacyTicketID(context.request, headers, subject);
    const duplicate = await context.request.post(legacyUserAPI("/ticket/save"), { headers, data: { subject: duplicateSubject, level: 0, message: duplicateMessage } });
    expect(duplicate.status()).toBe(400);
    expect(readStringProperty(await readJSON(duplicate), "message")).toBe("存在未关闭的工单");
    await expectLegacySingleTicket(context.request, headers, firstID, subject, firstMessage, 0);
    const close = await context.request.post(legacyUserAPI("/ticket/close"), { headers, data: { id: firstID } });
    expect(close.status()).toBe(200);
    const third = await context.request.post(legacyUserAPI("/ticket/save"), { headers, data: { subject: thirdSubject, level: 1, message: thirdMessage } });
    expect(third.status()).toBe(200);
    const listResponse = await context.request.get(legacyUserAPI("/ticket/fetch"), { headers });
    const list = requiredArrayProperty(await readJSON(listResponse), "data");
    expect(list).toHaveLength(2);
    const old = list.find((item) => requiredPositiveNumber(item, "id") === firstID);
    const next = list.find((item) => readStringProperty(item, "subject") === thirdSubject);
    expect(readProperty(old, "status")).toBe(1);
    expect(requiredPositiveNumber(next, "id")).not.toBe(firstID);
    expect(readProperty(next, "status")).toBe(0);
  } finally { await context.close(); }
}

async function exerciseGo(browser: Browser, email: string, password: string, subject: string, duplicateSubject: string, thirdSubject: string, firstMessage: string, duplicateMessage: string, thirdMessage: string) {
  const context = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(context.request, email, password);
    const first = await goUserRequest(context.request, "/api/v1/tickets", "POST", { subject, level: 2, message: firstMessage }, authorization);
    expect(first.status).toBe(201);
    const firstData = readProperty(parseJSONBody(first.body), "data");
    const firstID = requiredPositiveNumber(firstData, "id");
    const duplicate = await goUserRequest(context.request, "/api/v1/tickets", "POST", { subject: duplicateSubject, level: 0, message: duplicateMessage }, authorization);
    expect(duplicate.status).toBe(409);
    expect(readStringProperty(readProperty(parseJSONBody(duplicate.body), "error"), "code")).toBe("open_ticket_exists");
    await expectGoSingleTicket(context.request, authorization, firstID, subject, firstMessage, 0);
    const close = await goUserRequest(context.request, `/api/v1/tickets/${firstID}/close`, "POST", {}, authorization);
    expect(close.status).toBe(200);
    const third = await goUserRequest(context.request, "/api/v1/tickets", "POST", { subject: thirdSubject, level: 1, message: thirdMessage }, authorization);
    expect(third.status).toBe(201);
    const thirdID = requiredPositiveNumber(readProperty(parseJSONBody(third.body), "data"), "id");
    expect(thirdID).not.toBe(firstID);
    const list = requiredArrayProperty(readProperty(parseJSONBody((await goUserRequest(context.request, "/api/v1/tickets?page=1&page_size=20", "GET", undefined, authorization)).body), "data"), "items");
    expect(list).toHaveLength(2);
    expect(readProperty(list.find((item) => requiredPositiveNumber(item, "id") === firstID), "status")).toBe(1);
    expect(readProperty(list.find((item) => requiredPositiveNumber(item, "id") === thirdID), "status")).toBe(0);
  } finally { await context.close(); }
}

async function expectLegacySingleTicket(request: APIRequestContext, headers: { authorization: string }, id: number, subject: string, message: string, status: number) {
  const listResponse = await request.get(legacyUserAPI("/ticket/fetch"), { headers });
  const list = requiredArrayProperty(await readJSON(listResponse), "data");
  expect(list).toHaveLength(1);
  const detail = await request.get(legacyUserAPI(`/ticket/fetch?id=${id}`), { headers });
  expect(detail.status()).toBe(200);
  const ticket = readProperty(await readJSON(detail), "data");
  const messages = requiredArrayProperty(ticket, "message");
  expect(messages).toHaveLength(1);
  expect({ subject: readStringProperty(ticket, "subject"), status: readProperty(ticket, "status"), message: readStringProperty(messages[0], "message") }).toEqual({ subject, status, message });
}

async function expectGoSingleTicket(request: APIRequestContext, authorization: string, id: number, subject: string, message: string, status: number) {
  const list = requiredArrayProperty(readProperty(parseJSONBody((await goUserRequest(request, "/api/v1/tickets?page=1&page_size=20", "GET", undefined, authorization)).body), "data"), "items");
  expect(list).toHaveLength(1);
  const detail = await goUserRequest(request, `/api/v1/tickets/${id}`, "GET", undefined, authorization);
  expect(detail.status).toBe(200);
  const ticket = readProperty(parseJSONBody(detail.body), "data");
  const messages = requiredArrayProperty(ticket, "messages");
  expect(messages).toHaveLength(1);
  expect({ subject: readStringProperty(ticket, "subject"), status: readProperty(ticket, "status"), message: readStringProperty(messages[0], "message") }).toEqual({ subject, status, message });
}

async function legacyTicketID(request: APIRequestContext, headers: { authorization: string }, subject: string) {
  const listResponse = await request.get(legacyUserAPI("/ticket/fetch"), { headers });
  const list = requiredArrayProperty(await readJSON(listResponse), "data");
  expect(list).toHaveLength(1);
  return requiredPositiveNumber(list.find((item) => readStringProperty(item, "subject") === subject), "id");
}

async function loginLegacyAdmin(page: Page): Promise<string> {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await expect(fields).toHaveCount(2);
  await fields.first().fill(legacyEmail);
  await fields.nth(1).fill(legacyPassword);
  await fields.nth(1).press("Enter");
  await expect(page.locator('a[href="#/server/machine"]')).toBeVisible({ timeout: 60_000 });
  const response = page.waitForResponse((item) => item.url().includes("/ticket/fetch"));
  await page.locator('a[href="#/user/ticket"]').click();
  return (await response).request().headers().authorization ?? "";
}

async function loginLegacyUserAPI(request: APIRequestContext, email: string, password: string): Promise<string> {
  const response = await request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), { data: { email, password } });
  expect(response.status()).toBe(200);
  return readStringProperty(readProperty(await readJSON(response), "data"), "auth_data") ?? "";
}

async function loginGoAdmin(page: Page) {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible({ timeout: 60_000 });
}

async function loginGoUser(request: APIRequestContext, email: string, password: string): Promise<string> {
  const response = await request.post(new URL("/api/v1/passport/auth/login", goURL).toString(), { data: { email, password } });
  expect(response.status()).toBe(200);
  return readStringProperty(readProperty(await readJSON(response), "data"), "auth_data") ?? "";
}

async function goAdminRequest(page: Page, path: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, body: requestBody }) => {
    const csrf = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestBody === undefined ? "GET" : "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf) },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: goAdminURL(path), body });
}

async function goUserRequest(request: APIRequestContext, path: string, method = "GET", body?: unknown, authorization = "") {
  const cookies = await request.storageState();
  const csrf = cookies.cookies.find((cookie) => cookie.name === "xboard_csrf")?.value ?? "";
  const response = await request.fetch(new URL(path, goURL).toString(), {
    method,
    headers: { Authorization: authorization, "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf) },
    data: body
  });
  return { status: response.status(), body: await response.text() };
}

function legacyUserAPI(path: string) {
  return new URL(`/api/v1/user${path}`, new URL(legacyURL).origin).toString();
}

function legacyAdminAPI(path: string) {
  const securePath = new URL(legacyURL).pathname.replace(/\/$/, "");
  return new URL(`/api/v2${securePath}${path}`, legacyURL).toString();
}

function goAdminURL(path: string) {
  const base = new URL(goURL);
  const securePath = base.pathname.replace(/^\/+|\/+$/g, "");
  return path.startsWith("/api/v1/admin/")
    ? new URL(`/api/v1/admin/${securePath}/${path.slice("/api/v1/admin/".length)}`, base.origin).toString()
    : new URL(path, goURL).toString();
}

async function readJSON(response: { text(): Promise<string> }) {
  return JSON.parse(await response.text()) as unknown;
}

function parseJSONBody(body: string) {
  return JSON.parse(body) as unknown;
}

function requiredEnv(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function readProperty(value: unknown, key: string): unknown {
  return typeof value === "object" && value !== null ? Reflect.get(value, key) : undefined;
}

function readStringProperty(value: unknown, key: string): string | null {
  const property = readProperty(value, key);
  return typeof property === "string" ? property : null;
}

function requiredArrayProperty(value: unknown, key: string): unknown[] {
  const property = readProperty(value, key);
  if (!Array.isArray(property)) throw new Error(`missing array: ${key}`);
  return property;
}

function requiredPositiveNumber(value: unknown, key: string): number {
  const property = readProperty(value, key);
  if (typeof property !== "number" || !Number.isSafeInteger(property) || property < 1) throw new Error(`invalid positive number: ${key}`);
  return property;
}
