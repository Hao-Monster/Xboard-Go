import { randomBytes, randomUUID } from "node:crypto";

import {
  expect,
  test,
  type APIRequestContext,
  type Browser,
  type BrowserContext,
  type Page,
} from "@playwright/test";

test.use({ trace: "off", screenshot: "off", video: "off" });

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test("legacy and Go preserve an owner ticket against another user", async ({
  browser,
}) => {
  test.setTimeout(120_000);
  const unique = randomUUID();
  const subject = `Ownership gap ${unique}`;
  const message = `Owner message ${unique}`;
  const legacyOwner = {
    email: `ticket-owner-${unique}@legacy.local`,
    password: randomBytes(24).toString("base64url"),
  };
  const legacyOther = {
    email: `ticket-other-${unique}@legacy.local`,
    password: randomBytes(24).toString("base64url"),
  };
  const goOwner = {
    email: `ticket-owner-${unique}@go.local`,
    password: randomBytes(24).toString("base64url"),
  };
  const goOther = {
    email: `ticket-other-${unique}@go.local`,
    password: randomBytes(24).toString("base64url"),
  };
  let legacyOwnerID: number | null = null;
  let legacyOtherID: number | null = null;
  let goOwnerID: number | null = null;
  let goOtherID: number | null = null;
  let legacyAuthorization = "";
  let legacyAdminContext: BrowserContext | null = null;
  let goAdminContext: BrowserContext | null = null;
  let legacyAdminPage: Page | null = null;
  let goAdminPage: Page | null = null;

  try {
    legacyAdminContext = await browser.newContext({ locale: "zh-CN" });
    goAdminContext = await browser.newContext({ locale: "zh-CN" });
    legacyAdminPage = await legacyAdminContext.newPage();
    goAdminPage = await goAdminContext.newPage();
    if (legacyAdminPage === null || goAdminPage === null) {
      throw new Error("admin pages were not created");
    }
    legacyAuthorization = await loginLegacyAdmin(legacyAdminPage!);
    await loginGoAdmin(goAdminPage!);
    legacyOwnerID = await generateLegacyUser(
      legacyAdminPage!.request,
      legacyAuthorization,
      legacyOwner,
    );
    legacyOtherID = await generateLegacyUser(
      legacyAdminPage!.request,
      legacyAuthorization,
      legacyOther,
    );
    goOwnerID = await generateGoUser(goAdminPage!, goOwner);
    goOtherID = await generateGoUser(goAdminPage!, goOther);
    const results = await Promise.allSettled([
      exerciseLegacy(browser, legacyOwner, legacyOther, subject, message),
      exerciseGo(browser, goOwner, goOther, subject, message),
    ]);
    const failed = results.find(
      (result): result is PromiseRejectedResult => result.status === "rejected",
    );
    if (failed !== undefined)
      throw new Error(
        `ownership parity failed: ${failed.reason instanceof Error ? failed.reason.message : "unknown error"}`,
      );
  } finally {
    const cleanupErrors: string[] = [];
    for (const id of [legacyOwnerID, legacyOtherID]) {
      if (id === null) continue;
      try {
        const response = await legacyAdminPage!.request.post(
          legacyAdminAPI("/user/destroy"),
          { headers: { authorization: legacyAuthorization }, data: { id } },
        );
        if (response.status() !== 200)
          cleanupErrors.push(`legacy cleanup status ${response.status()}`);
      } catch {
        cleanupErrors.push("legacy cleanup threw");
      }
    }
    for (const id of [goOwnerID, goOtherID]) {
      if (id === null) continue;
      try {
        const detail = await goAdminRequest(
          goAdminPage!,
          `/api/v1/admin/users/${id}`,
        );
        const revision = requiredPositiveNumber(
          readProperty(parseJSONBody(detail.body), "data"),
          "revision",
        );
        const response = await goAdminRequest(
          goAdminPage!,
          `/api/v1/admin/users/${id}/deactivate`,
          { revision },
        );
        if (response.status !== 200)
          cleanupErrors.push(`Go cleanup status ${response.status}`);
      } catch {
        cleanupErrors.push("Go cleanup threw");
      }
    }
    const closures = await Promise.allSettled(
      [legacyAdminContext, goAdminContext]
        .filter((context): context is BrowserContext => context !== null)
        .map((context) => context.close()),
    );
    if (closures.some((result) => result.status === "rejected"))
      cleanupErrors.push("admin context close failed");
    expect(cleanupErrors, "test fixture cleanup").toEqual([]);
  }
});

async function exerciseLegacy(
  browser: Browser,
  owner: User,
  other: User,
  subject: string,
  message: string,
) {
  let ownerContext: BrowserContext | null = null;
  let otherContext: BrowserContext | null = null;
  try {
    ownerContext = await browser.newContext({ locale: "zh-CN" });
    otherContext = await browser.newContext({ locale: "zh-CN" });
    if (ownerContext === null || otherContext === null) {
      throw new Error("legacy user contexts were not created");
    }
    const ownerAuth = await loginLegacyUser(ownerContext!.request, owner);
    const otherAuth = await loginLegacyUser(otherContext!.request, other);
    const ownerHeaders = { authorization: ownerAuth };
    const otherHeaders = { authorization: otherAuth };
    const created = await ownerContext!.request.post(
      legacyUserAPI("/ticket/save"),
      { headers: ownerHeaders, data: { subject, level: 2, message } },
    );
    expect(created.status()).toBe(200);
    const ticketID = await legacyTicketID(
      ownerContext!.request,
      ownerHeaders,
      subject,
    );
    const before = await legacySnapshot(
      ownerContext!.request,
      ownerHeaders,
      ticketID,
    );
    for (const { request, status, message: expectedMessage } of [
      {
        request: () =>
          otherContext!.request.get(
            legacyUserAPI(`/ticket/fetch?id=${ticketID}`),
            { headers: otherHeaders },
          ),
        status: 500,
        message: null,
      },
      {
        request: () =>
          otherContext!.request.post(legacyUserAPI("/ticket/reply"), {
            headers: otherHeaders,
            data: { id: ticketID, message: "Unauthorized" },
          }),
        status: 400,
        message: "工单不存在",
      },
      {
        request: () =>
          otherContext!.request.post(legacyUserAPI("/ticket/close"), {
            headers: otherHeaders,
            data: { id: ticketID },
          }),
        status: 400,
        message: "工单不存在",
      },
    ]) {
      const response = await request();
      const responseBody = await response.text();
      expect(response.status()).toBe(status);
      expect(responseBody.includes(subject)).toBe(false);
      expect(responseBody.includes(message)).toBe(false);
      expect(responseBody.includes(owner.email)).toBe(false);
      if (expectedMessage !== null)
        expect(
          readStringProperty(JSON.parse(responseBody) as unknown, "message"),
        ).toBe(expectedMessage);
      expect(
        await legacySnapshot(ownerContext!.request, ownerHeaders, ticketID),
      ).toEqual(before);
    }
  } finally {
    const closures = await Promise.allSettled(
      [ownerContext, otherContext]
        .filter((context): context is BrowserContext => context !== null)
        .map((context) => context.close()),
    );
    const failed = closures.find(
      (result): result is PromiseRejectedResult => result.status === "rejected",
    );
    if (failed !== undefined) throw failed.reason;
  }
}

async function exerciseGo(
  browser: Browser,
  owner: User,
  other: User,
  subject: string,
  message: string,
) {
  let ownerContext: BrowserContext | null = null;
  let otherContext: BrowserContext | null = null;
  try {
    ownerContext = await browser.newContext({ locale: "zh-CN" });
    otherContext = await browser.newContext({ locale: "zh-CN" });
    if (ownerContext === null || otherContext === null) {
      throw new Error("Go user contexts were not created");
    }
    const ownerAuth = await loginGoUser(ownerContext!.request, owner);
    const otherAuth = await loginGoUser(otherContext!.request, other);
    const created = await goUserRequest(
      ownerContext!.request,
      "/api/v1/tickets",
      "POST",
      { subject, level: 2, message },
      ownerAuth,
    );
    expect(created.status).toBe(201);
    const ticketID = requiredPositiveNumber(
      readProperty(parseJSONBody(created.body), "data"),
      "id",
    );
    const before = await goSnapshot(ownerContext!.request, ownerAuth, ticketID);
    for (const request of [
      () =>
        goUserRequest(
          otherContext!.request,
          `/api/v1/tickets/${ticketID}`,
          "GET",
          undefined,
          otherAuth,
        ),
      () =>
        goUserRequest(
          otherContext!.request,
          `/api/v1/tickets/${ticketID}/messages`,
          "POST",
          { message: "Unauthorized" },
          otherAuth,
        ),
      () =>
        goUserRequest(
          otherContext!.request,
          `/api/v1/tickets/${ticketID}/close`,
          "POST",
          {},
          otherAuth,
        ),
    ]) {
      const response = await request();
      expect(response.body.includes(subject)).toBe(false);
      expect(response.body.includes(message)).toBe(false);
      expect(response.body.includes(owner.email)).toBe(false);
      expect(response.status).toBe(404);
      expect(
        readStringProperty(
          readProperty(parseJSONBody(response.body), "error"),
          "code",
        ),
      ).toBe("not_found");
      expect(
        await goSnapshot(ownerContext!.request, ownerAuth, ticketID),
      ).toEqual(before);
    }
  } finally {
    const closures = await Promise.allSettled(
      [ownerContext, otherContext]
        .filter((context): context is BrowserContext => context !== null)
        .map((context) => context.close()),
    );
    const failed = closures.find(
      (result): result is PromiseRejectedResult => result.status === "rejected",
    );
    if (failed !== undefined) throw failed.reason;
  }
}

type User = { email: string; password: string };
async function generateLegacyUser(
  request: APIRequestContext,
  authorization: string,
  user: User,
): Promise<number> {
  const generated = await request.post(legacyAdminAPI("/user/generate"), {
    headers: { authorization },
    data: {
      email_prefix: user.email.split("@", 1)[0],
      email_suffix: "legacy.local",
      password: user.password,
    },
  });
  expect(generated.status()).toBe(200);
  const usersResponse = await request.get(
    legacyAdminAPI(
      `/user/fetch?current=1&pageSize=20&search=${encodeURIComponent(user.email)}`,
    ),
    { headers: { authorization } },
  );
  const users = requiredArrayProperty(await readJSON(usersResponse), "data");
  return requiredPositiveNumber(
    users.find((item) => readStringProperty(item, "email") === user.email),
    "id",
  );
}

async function generateGoUser(page: Page, user: User): Promise<number> {
  const response = await goAdminRequest(page, "/api/v1/admin/users/generate", {
    mode: "single",
    email: user.email,
    count: 1,
    password: user.password,
    plan_id: null,
    expired_at: new Date(Date.now() + 30 * 24 * 60 * 60 * 1_000).toISOString(),
    download_csv: false,
    is_distributor: false,
  });
  expect(response.status).toBe(201);
  return requiredPositiveNumber(
    requiredArrayProperty(
      readProperty(parseJSONBody(response.body), "data"),
      "items",
    )[0],
    "id",
  );
}

async function loginLegacyAdmin(page: Page): Promise<string> {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await expect(fields).toHaveCount(2);
  await fields.first().fill(legacyEmail);
  await fields.nth(1).fill(legacyPassword);
  await fields.nth(1).press("Enter");
  await expect(page.locator('a[href="#/server/machine"]')).toBeVisible({
    timeout: 60_000,
  });
  const response = page.waitForResponse((item) =>
    item.url().includes("/ticket/fetch"),
  );
  await page.locator('a[href="#/user/ticket"]').click();
  return (await response).request().headers().authorization ?? "";
}
async function loginLegacyUser(
  request: APIRequestContext,
  user: User,
): Promise<string> {
  const response = await request.post(
    new URL("/api/v1/passport/auth/login", legacyURL).toString(),
    { data: user },
  );
  expect(response.status()).toBe(200);
  return (
    readStringProperty(
      readProperty(await readJSON(response), "data"),
      "auth_data",
    ) ?? ""
  );
}
async function loginGoAdmin(page: Page) {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible({
    timeout: 60_000,
  });
}
async function loginGoUser(
  request: APIRequestContext,
  user: User,
): Promise<string> {
  const response = await request.post(
    new URL("/api/v1/passport/auth/login", goURL).toString(),
    { data: user },
  );
  expect(response.status()).toBe(200);
  return (
    readStringProperty(
      readProperty(await readJSON(response), "data"),
      "auth_data",
    ) ?? ""
  );
}
async function legacyTicketID(
  request: APIRequestContext,
  headers: { authorization: string },
  subject: string,
) {
  const response = await request.get(legacyUserAPI("/ticket/fetch"), {
    headers,
  });
  const list = requiredArrayProperty(await readJSON(response), "data");
  expect(list).toHaveLength(1);
  return requiredPositiveNumber(
    list.find((item) => readStringProperty(item, "subject") === subject),
    "id",
  );
}
async function legacySnapshot(
  request: APIRequestContext,
  headers: { authorization: string },
  id: number,
) {
  const listResponse = await request.get(legacyUserAPI("/ticket/fetch"), {
    headers,
  });
  const list = requiredArrayProperty(await readJSON(listResponse), "data");
  expect(list).toHaveLength(1);
  const detail = await request.get(legacyUserAPI(`/ticket/fetch?id=${id}`), {
    headers,
  });
  expect(detail.status()).toBe(200);
  const ticket = readProperty(await readJSON(detail), "data");
  const messages = requiredArrayProperty(ticket, "message");
  expect(messages).toHaveLength(1);
  return {
    subject: readStringProperty(ticket, "subject"),
    status: readProperty(ticket, "status"),
    reply_status: readProperty(ticket, "reply_status"),
    message: readStringProperty(messages[0], "message"),
  };
}
async function goSnapshot(
  request: APIRequestContext,
  authorization: string,
  id: number,
) {
  const list = requiredArrayProperty(
    readProperty(
      parseJSONBody(
        (
          await goUserRequest(
            request,
            "/api/v1/tickets?page=1&page_size=20",
            "GET",
            undefined,
            authorization,
          )
        ).body,
      ),
      "data",
    ),
    "items",
  );
  expect(list).toHaveLength(1);
  const detail = await goUserRequest(
    request,
    `/api/v1/tickets/${id}`,
    "GET",
    undefined,
    authorization,
  );
  expect(detail.status).toBe(200);
  const ticket = readProperty(parseJSONBody(detail.body), "data");
  const messages = requiredArrayProperty(ticket, "messages");
  expect(messages).toHaveLength(1);
  return {
    subject: readStringProperty(ticket, "subject"),
    status: readProperty(ticket, "status"),
    reply_status: readProperty(ticket, "reply_status"),
    message: readStringProperty(messages[0], "message"),
  };
}
async function goAdminRequest(page: Page, path: string, body?: unknown) {
  return page.evaluate(
    async ({ path: requestPath, body: requestBody }) => {
      const csrf =
        document.cookie
          .split("; ")
          .find((item) => item.startsWith("xboard_csrf="))
          ?.slice("xboard_csrf=".length) ?? "";
      const response = await fetch(requestPath, {
        method: requestBody === undefined ? "GET" : "POST",
        credentials: "same-origin",
        headers: {
          "Content-Type": "application/json",
          "X-CSRF-Token": decodeURIComponent(csrf),
        },
        body:
          requestBody === undefined ? undefined : JSON.stringify(requestBody),
      });
      return { status: response.status, body: await response.text() };
    },
    { path: goAdminURL(path), body },
  );
}
async function goUserRequest(
  request: APIRequestContext,
  path: string,
  method = "GET",
  body?: unknown,
  authorization = "",
) {
  const cookies = await request.storageState();
  const csrf =
    cookies.cookies.find((cookie) => cookie.name === "xboard_csrf")?.value ??
    "";
  const response = await request.fetch(new URL(path, goURL).toString(), {
    method,
    headers: {
      Authorization: authorization,
      "Content-Type": "application/json",
      "X-CSRF-Token": decodeURIComponent(csrf),
    },
    data: body,
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
    ? new URL(
        `/api/v1/admin/${securePath}/${path.slice("/api/v1/admin/".length)}`,
        base.origin,
      ).toString()
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
  return typeof value === "object" && value !== null
    ? Reflect.get(value, key)
    : undefined;
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
  if (
    typeof property !== "number" ||
    !Number.isSafeInteger(property) ||
    property < 1
  )
    throw new Error(`invalid positive number: ${key}`);
  return property;
}
