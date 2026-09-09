import { randomBytes, randomUUID } from "node:crypto";

import { expect, test, type APIRequestContext, type Browser, type Page } from "@playwright/test";

test.use({ trace: "off", screenshot: "off", video: "off" });

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test("legacy and Go ticket creation persist the same user-visible contract", async ({ browser }) => {
  test.setTimeout(120_000);
  const unique = randomUUID();
  const subject = `Ticket persistence ${unique}`;
  const message = `Initial persisted message ${unique}`;
  const legacyUser = { email: `ticket-persistence-${unique}@legacy.local`, password: randomBytes(24).toString("base64url") };
  const goUser = { email: `ticket-persistence-${unique}@go.local`, password: randomBytes(24).toString("base64url") };
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
    await readJSON(legacyGenerated);
    expect(legacyGenerated.status()).toBe(200);
    const legacyUsers = await legacyAdminPage.request.get(legacyAdminAPI(`/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(legacyUser.email)}`), {
      headers: { authorization: legacyAuthorization }
    });
    const legacyUsersBody = await readJSON(legacyUsers);
    expect(legacyUsers.status()).toBe(200);
    const legacyUserItems = requiredArrayProperty(legacyUsersBody, "data");
    const legacyUserMatches = legacyUserItems.filter((item) => readStringProperty(item, "email") === legacyUser.email);
    expect(legacyUserMatches.length, "legacy generated user exact email match count").toBe(1);
    const legacyUserRecord = legacyUserMatches[0];
    legacyUserID = requiredPositiveNumber(legacyUserRecord, "id");

    const goGenerated = await goAdminRequest(goAdminPage, "/api/v1/admin/users/generate", {
      mode: "single", email: goUser.email, count: 1, password: goUser.password,
      plan_id: null, expired_at: new Date(Date.now() + 30 * 24 * 60 * 60 * 1_000).toISOString(),
      download_csv: false, is_distributor: false
    });
    const goGeneratedBody = parseJSONBody(goGenerated.body);
    expect(goGenerated.status).toBe(201);
    const goUserItems = requiredArrayProperty(readProperty(goGeneratedBody, "data"), "items");
    goUserID = requiredPositiveNumber(goUserItems[0], "id");

    const exerciseResults = await Promise.allSettled([
      exerciseLegacyCreation(browser, legacyUser.email, legacyUser.password, subject, message),
      exerciseGoCreation(browser, goUser.email, goUser.password, subject, message)
    ]);
    expect(exerciseResults.every((result) => result.status === "fulfilled"), "dual-service creation exercise completed").toBe(true);
    if (exerciseResults[0].status !== "fulfilled" || exerciseResults[1].status !== "fulfilled") throw new Error("dual-service creation exercise failed");
    const [legacySnapshot, goSnapshot] = [exerciseResults[0].value, exerciseResults[1].value];
    expect(goSnapshot).toEqual(legacySnapshot);
    expect(goSnapshot).toEqual({
      subject, level: 2, status: 0, reply_status: 0,
      messages: [{ message, is_me: true }]
    });
  } finally {
    const cleanupErrors: string[] = [];
    try {
      if (legacyUserID === null && legacyAuthorization) {
        const fallback = await legacyAdminPage.request.get(legacyAdminAPI(`/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(legacyUser.email)}`), {
          headers: { authorization: legacyAuthorization }
        });
        const fallbackBody = await readJSON(fallback);
        expect(fallback.status()).toBe(200);
        const fallbackItems = requiredArrayProperty(fallbackBody, "data");
        const fallbackMatches = fallbackItems.filter((item) => readStringProperty(item, "email") === legacyUser.email);
        expect(fallbackMatches.length, "legacy cleanup exact email match count").toBe(1);
        legacyUserID = requiredPositiveNumber(fallbackMatches[0], "id");
      }
      if (legacyUserID !== null) {
        const response = await legacyAdminPage.request.post(legacyAdminAPI("/user/destroy"), {
          headers: { authorization: legacyAuthorization }, data: { id: legacyUserID }
        });
        if (response.status() !== 200) cleanupErrors.push(`legacy cleanup status ${response.status()}`);
      }
    } catch {
      cleanupErrors.push("legacy cleanup threw");
    }
    try {
      if (goUserID !== null) {
        const detail = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}`);
        const detailBody = parseJSONBody(detail.body);
        const revision = requiredPositiveNumber(readProperty(detailBody, "data"), "revision");
        const response = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}/deactivate`, { revision });
        if (response.status !== 200) cleanupErrors.push(`Go cleanup status ${response.status}`);
      }
    } catch {
      cleanupErrors.push("Go cleanup threw");
    }
    const contextClosures = await Promise.allSettled([legacyAdminContext.close(), goAdminContext.close()]);
    if (contextClosures.some((result) => result.status === "rejected")) cleanupErrors.push("admin context close failed");
    expect(cleanupErrors, "test fixture cleanup").toEqual([]);
  }
});

test("legacy and Go ticket reply and close persist across re-login without reopening", async ({ browser }) => {
  test.setTimeout(120_000);
  const unique = randomUUID();
  const subject = `Ticket reply close ${unique}`;
  const initialMessage = `Initial reply-close message ${unique}`;
  const userReplyMessage = `User reply ${unique}`;
  const adminReplyMessage = `Administrator reply ${unique}`;
  const legacyUser = { email: `ticket-reply-close-${unique}@legacy.local`, password: randomBytes(24).toString("base64url") };
  const goUser = { email: `ticket-reply-close-${unique}@go.local`, password: randomBytes(24).toString("base64url") };
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
    await readJSON(legacyGenerated);
    expect(legacyGenerated.status()).toBe(200);
    const legacyUsers = await legacyAdminPage.request.get(legacyAdminAPI(`/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(legacyUser.email)}`), {
      headers: { authorization: legacyAuthorization }
    });
    const legacyUserItems = requiredArrayProperty(await readJSON(legacyUsers), "data");
    const legacyUserMatches = legacyUserItems.filter((item) => readStringProperty(item, "email") === legacyUser.email);
    expect(legacyUserMatches.length, "legacy generated user exact email match count").toBe(1);
    legacyUserID = requiredPositiveNumber(legacyUserMatches[0], "id");
    const goGenerated = await goAdminRequest(goAdminPage, "/api/v1/admin/users/generate", {
      mode: "single", email: goUser.email, count: 1, password: goUser.password,
      plan_id: null, expired_at: new Date(Date.now() + 30 * 24 * 60 * 60 * 1_000).toISOString(),
      download_csv: false, is_distributor: false
    });
    expect(goGenerated.status).toBe(201);
    const goUserItems = requiredArrayProperty(readProperty(parseJSONBody(goGenerated.body), "data"), "items");
    goUserID = requiredPositiveNumber(goUserItems[0], "id");
    const exerciseResults = await Promise.allSettled([
      exerciseLegacyReplyClose(browser, legacyUser.email, legacyUser.password, subject, initialMessage, userReplyMessage, adminReplyMessage, legacyAuthorization),
      exerciseGoReplyClose(browser, goUser.email, goUser.password, subject, initialMessage, userReplyMessage, adminReplyMessage, goAdminPage)
    ]);
    expect(exerciseResults.every((result) => result.status === "fulfilled"), "dual-service reply-close exercise completed").toBe(true);
    if (exerciseResults[0].status !== "fulfilled" || exerciseResults[1].status !== "fulfilled") throw new Error("dual-service reply-close exercise failed");
    const [legacySnapshot, goSnapshot] = [exerciseResults[0].value, exerciseResults[1].value];
    expect(goSnapshot).toEqual(legacySnapshot);
    expect(goSnapshot).toEqual({
      subject, level: 2, status: 1, reply_status: 1,
      messages: [
        { message: initialMessage, is_me: true },
        { message: userReplyMessage, is_me: true },
        { message: adminReplyMessage, is_me: false }
      ]
    });
  } finally {
    const cleanupErrors: string[] = [];
    try {
      if (legacyUserID === null && legacyAuthorization) {
        const fallback = await legacyAdminPage.request.get(legacyAdminAPI(`/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(legacyUser.email)}`), {
          headers: { authorization: legacyAuthorization }
        });
        const fallbackItems = requiredArrayProperty(await readJSON(fallback), "data");
        const fallbackMatches = fallbackItems.filter((item) => readStringProperty(item, "email") === legacyUser.email);
        expect(fallbackMatches.length, "legacy cleanup exact email match count").toBe(1);
        legacyUserID = requiredPositiveNumber(fallbackMatches[0], "id");
      }
      if (legacyUserID !== null) {
        const response = await legacyAdminPage.request.post(legacyAdminAPI("/user/destroy"), {
          headers: { authorization: legacyAuthorization }, data: { id: legacyUserID }
        });
        if (response.status() !== 200) cleanupErrors.push(`legacy cleanup status ${response.status()}`);
      }
    } catch {
      cleanupErrors.push("legacy cleanup threw");
    }
    try {
      if (goUserID !== null) {
        const detail = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}`);
        const revision = requiredPositiveNumber(readProperty(parseJSONBody(detail.body), "data"), "revision");
        const response = await goAdminRequest(goAdminPage, `/api/v1/admin/users/${goUserID}/deactivate`, { revision });
        if (response.status !== 200) cleanupErrors.push(`Go cleanup status ${response.status}`);
      }
    } catch {
      cleanupErrors.push("Go cleanup threw");
    }
    const contextClosures = await Promise.allSettled([legacyAdminContext.close(), goAdminContext.close()]);
    if (contextClosures.some((result) => result.status === "rejected")) cleanupErrors.push("admin context close failed");
    expect(cleanupErrors, "test fixture cleanup").toEqual([]);
  }
});

async function loginLegacyUserAPI(request: APIRequestContext, email: string, password: string): Promise<string> {
  const response = await request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), { data: { email, password } });
  expect(response.status()).toBe(200);
  const authorization = readStringProperty(readProperty(await readJSON(response), "data"), "auth_data");
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy user authorization is missing");
  return authorization;
}

async function exerciseLegacyReplyClose(browser: Browser, email: string, password: string, subject: string, initialMessage: string, userReplyMessage: string, adminReplyMessage: string, adminAuthorization: string) {
  const createContext = await browser.newContext({ locale: "zh-CN" });
  let ticketID: number;
  try {
    const authorization = await loginLegacyUserAPI(createContext.request, email, password);
    const created = await createContext.request.post(new URL("/api/v1/user/ticket/save", legacyURL).toString(), {
      headers: { authorization }, data: { subject, level: 2, message: initialMessage }
    });
    expect(created.status()).toBe(200);
    const list = await createContext.request.get(new URL("/api/v1/user/ticket/fetch", legacyURL).toString(), { headers: { authorization } });
    const tickets = requiredArrayProperty(await readJSON(list), "data");
    ticketID = requiredPositiveNumber(tickets.find((item) => readStringProperty(item, "subject") === subject), "id");
  } finally { await createContext.close(); }
  const replyContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginLegacyUserAPI(replyContext.request, email, password);
    const replied = await replyContext.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: { authorization }, data: { id: ticketID, message: userReplyMessage }
    });
    expect(replied.status()).toBe(200);
    const closed = await replyContext.request.post(new URL("/api/v1/user/ticket/close", legacyURL).toString(), {
      headers: { authorization }, data: { id: ticketID }
    });
    expect(closed.status()).toBe(200);
  } finally { await replyContext.close(); }
  const rereadContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginLegacyUserAPI(rereadContext.request, email, password);
    const detail = await rereadContext.request.get(new URL(`/api/v1/user/ticket/fetch?id=${ticketID}`, legacyURL).toString(), { headers: { authorization } });
    expect(detail.status()).toBe(200);
    expect(normalizeTicket(readProperty(await readJSON(detail), "data"), "message")).toEqual({
      subject, level: 2, status: 1, reply_status: 0,
      messages: [{ message: initialMessage, is_me: true }, { message: userReplyMessage, is_me: true }]
    });
    const rejected = await rereadContext.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: { authorization }, data: { id: ticketID, message: "Rejected after close" }
    });
    expect(rejected.status()).toBe(400);
    expect(readStringProperty(await readJSON(rejected), "message")).toBe("工单已关闭，无法回复");
  } finally { await rereadContext.close(); }
  const adminContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const adminReply = await adminContext.request.post(legacyAdminAPI("/ticket/reply"), {
      headers: { authorization: adminAuthorization }, data: { id: ticketID, message: adminReplyMessage }
    });
    expect(adminReply.status()).toBe(200);
  } finally { await adminContext.close(); }
  const finalContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginLegacyUserAPI(finalContext.request, email, password);
    const detail = await finalContext.request.get(new URL(`/api/v1/user/ticket/fetch?id=${ticketID}`, legacyURL).toString(), { headers: { authorization } });
    expect(detail.status()).toBe(200);
    return normalizeTicket(readProperty(await readJSON(detail), "data"), "message");
  } finally { await finalContext.close(); }
}

async function exerciseGoReplyClose(browser: Browser, email: string, password: string, subject: string, initialMessage: string, userReplyMessage: string, adminReplyMessage: string, adminPage: Page) {
  const createContext = await browser.newContext({ locale: "zh-CN" });
  let ticketID: number;
  try {
    const authorization = await loginGoUser(createContext.request, email, password);
    const created = await goUserRequest(createContext.request, "/api/v1/tickets", "POST", { subject, level: 2, message: initialMessage }, authorization);
    expect(created.status).toBe(201);
    const list = await goUserRequest(createContext.request, "/api/v1/tickets?page=1&page_size=20", "GET", undefined, authorization);
    const tickets = requiredArrayProperty(readProperty(parseJSONBody(list.body), "data"), "items");
    ticketID = requiredPositiveNumber(tickets.find((item) => readStringProperty(item, "subject") === subject), "id");
  } finally { await createContext.close(); }
  const replyContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(replyContext.request, email, password);
    const replied = await goUserRequest(replyContext.request, `/api/v1/tickets/${ticketID}/messages`, "POST", { message: userReplyMessage }, authorization);
    expect(replied.status).toBe(200);
    const closed = await goUserRequest(replyContext.request, `/api/v1/tickets/${ticketID}/close`, "POST", {}, authorization);
    expect(closed.status).toBe(200);
  } finally { await replyContext.close(); }
  const rereadContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(rereadContext.request, email, password);
    const detail = await goUserRequest(rereadContext.request, `/api/v1/tickets/${ticketID}`, "GET", undefined, authorization);
    expect(detail.status).toBe(200);
    expect(normalizeTicket(readProperty(parseJSONBody(detail.body), "data"), "messages")).toEqual({
      subject, level: 2, status: 1, reply_status: 0,
      messages: [{ message: initialMessage, is_me: true }, { message: userReplyMessage, is_me: true }]
    });
    const rejected = await goUserRequest(rereadContext.request, `/api/v1/tickets/${ticketID}/messages`, "POST", { message: "Rejected after close" }, authorization);
    expect(rejected.status).toBe(409);
    expect(readStringProperty(readProperty(parseJSONBody(rejected.body), "error"), "code")).toBe("ticket_closed");
  } finally { await rereadContext.close(); }
  const adminReply = await goAdminRequest(adminPage, `/api/v1/admin/tickets/${ticketID}/messages`, { message: adminReplyMessage });
  expect(adminReply.status).toBe(200);
  const finalContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(finalContext.request, email, password);
    const detail = await goUserRequest(finalContext.request, `/api/v1/tickets/${ticketID}`, "GET", undefined, authorization);
    expect(detail.status).toBe(200);
    return normalizeTicket(readProperty(parseJSONBody(detail.body), "data"), "messages");
  } finally { await finalContext.close(); }
}

async function exerciseLegacyCreation(browser: Browser, email: string, password: string, subject: string, message: string) {
  const firstContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const first = await firstContext.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email, password }
    });
    const loginBody = await readJSON(first);
    expect(first.status()).toBe(200);
    const authorization = readStringProperty(readProperty(loginBody, "data"), "auth_data");
    expect(authorization).toBeTruthy();
    if (!authorization) throw new Error("legacy user authorization is missing");
    const created = await firstContext.request.post(new URL("/api/v1/user/ticket/save", legacyURL).toString(), {
      headers: { authorization }, data: { subject, level: 2, message }
    });
    expect(created.status()).toBe(200);
  } finally {
    await firstContext.close();
  }

  const secondContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const login = await secondContext.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email, password }
    });
    const loginBody = await readJSON(login);
    expect(login.status()).toBe(200);
    const authorization = readStringProperty(readProperty(loginBody, "data"), "auth_data");
    if (!authorization) throw new Error("legacy persistence login authorization is missing");
    const headers = { authorization };
    const list = await secondContext.request.get(new URL("/api/v1/user/ticket/fetch", legacyURL).toString(), { headers });
    const listBody = await readJSON(list);
    expect(list.status()).toBe(200);
    const tickets = requiredArrayProperty(listBody, "data");
    const ticket = tickets.find((item) => readStringProperty(item, "subject") === subject);
    const ticketID = requiredPositiveNumber(ticket, "id");
    const detail = await secondContext.request.get(new URL(`/api/v1/user/ticket/fetch?id=${ticketID}`, legacyURL).toString(), { headers });
    const detailBody = await readJSON(detail);
    expect(detail.status()).toBe(200);
    return normalizeTicket(readProperty(detailBody, "data"), "message");
  } finally {
    await secondContext.close();
  }
}

async function exerciseGoCreation(browser: Browser, email: string, password: string, subject: string, message: string) {
  const firstContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(firstContext.request, email, password);
    const created = await goUserRequest(firstContext.request, "/api/v1/tickets", "POST", { subject, level: 2, message }, authorization);
    expect(created.status).toBe(201);
  } finally {
    await firstContext.close();
  }

  const secondContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const authorization = await loginGoUser(secondContext.request, email, password);
    const list = await goUserRequest(secondContext.request, "/api/v1/tickets?page=1&page_size=20", "GET", undefined, authorization);
    expect(list.status).toBe(200);
    const listBody = parseJSONBody(list.body);
    const data = readProperty(listBody, "data");
    const tickets = requiredArrayProperty(data, "items");
    const ticket = tickets.find((item) => readStringProperty(item, "subject") === subject);
    const ticketID = requiredPositiveNumber(ticket, "id");
    const detail = await goUserRequest(secondContext.request, `/api/v1/tickets/${ticketID}`, "GET", undefined, authorization);
    expect(detail.status).toBe(200);
    return normalizeTicket(readProperty(parseJSONBody(detail.body), "data"), "messages");
  } finally {
    await secondContext.close();
  }
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
  const ticketResponse = await response;
  const authorization = ticketResponse.request().headers().authorization;
  if (!authorization) throw new Error("legacy administrator authorization is missing");
  return authorization;
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
  const body = await readJSON(response);
  const authorization = readStringProperty(readProperty(body, "data"), "auth_data");
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("Go user authorization is missing");
  return authorization;
}

async function goAdminRequest(page: Page, path: string, body?: unknown): Promise<{ status: number; body: string }> {
  return page.evaluate(async ({ path: requestPath, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
    const response = await fetch(requestPath, {
      method: requestBody === undefined ? "GET" : "POST", credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      }, body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path: goAdminURL(path), body });
}

async function goUserRequest(request: APIRequestContext, path: string, method = "GET", body?: unknown, authorization = "") {
  const cookies = await request.storageState();
  const csrf = cookies.cookies.find((cookie) => cookie.name === "xboard_csrf")?.value ?? "";
  const response = await request.fetch(new URL(path, goURL).toString(), {
    method, headers: body === undefined ? { Authorization: authorization, "X-CSRF-Token": decodeURIComponent(csrf) } : {
      Authorization: authorization, "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf)
    }, data: body
  });
  return { status: response.status(), body: await response.text() };
}

function normalizeTicket(value: unknown, messagesKey: string) {
  const messages = requiredArrayProperty(value, messagesKey);
  return {
    subject: requiredStringProperty(value, "subject"), level: requiredNumberProperty(value, "level"),
    status: requiredNumberProperty(value, "status"), reply_status: requiredNumberProperty(value, "reply_status"),
    messages: messages.map((item) => ({ message: requiredStringProperty(item, "message"), is_me: requiredBooleanProperty(item, "is_me") }))
  };
}

async function readJSON(response: { text(): Promise<string> }) {
  return JSON.parse(await response.text()) as unknown;
}

function parseJSONBody(body: string): unknown {
  return JSON.parse(body) as unknown;
}

function goAdminURL(path: string) {
  const base = new URL(goURL);
  const securePath = base.pathname.replace(/^\/+|\/+$/g, "");
  if (path.startsWith("/api/v1/admin/")) return new URL(`/api/v1/admin/${securePath}/${path.slice("/api/v1/admin/".length)}`, base.origin).toString();
  return new URL(path, goURL).toString();
}

function legacyAdminAPI(path: string) {
  const securePath = new URL(legacyURL).pathname.replace(/\/$/, "");
  return new URL(`/api/v2${securePath}${path}`, legacyURL).toString();
}

function requiredEnv(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for ticket parity tests`);
  return value;
}

function readProperty(value: unknown, key: string): unknown {
  if (typeof value !== "object" || value === null) return undefined;
  return Reflect.get(value, key);
}

function readStringProperty(value: unknown, key: string): string | null {
  const property = readProperty(value, key);
  return typeof property === "string" ? property : null;
}

function requiredArrayProperty(value: unknown, key: string): unknown[] {
  const property = readProperty(value, key);
  if (!Array.isArray(property)) throw new Error(`missing array property: ${key}`);
  return property;
}

function requiredStringProperty(value: unknown, key: string): string {
  const property = readProperty(value, key);
  if (typeof property !== "string") throw new Error(`missing string property: ${key}`);
  return property;
}

function requiredNumberProperty(value: unknown, key: string): number {
  const property = readProperty(value, key);
  if (typeof property !== "number" || !Number.isFinite(property)) throw new Error(`missing number property: ${key}`);
  return property;
}

function requiredBooleanProperty(value: unknown, key: string): boolean {
  const property = readProperty(value, key);
  if (typeof property !== "boolean") throw new Error(`missing boolean property: ${key}`);
  return property;
}

function requiredPositiveNumber(value: unknown, key: string): number {
  const property = requiredNumberProperty(value, key);
  if (!Number.isSafeInteger(property) || property < 1) throw new Error(`invalid positive number property: ${key}`);
  return property;
}
