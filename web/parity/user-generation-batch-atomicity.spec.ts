import { randomBytes } from "node:crypto";

import { expect, test, type APIRequestContext } from "@playwright/test";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");

test.use({ trace: "off", screenshot: "off", video: "off" });

test("[DIFF-USER-008] legacy and Go prefixed batch generation reject duplicate emails without partial inserts", async ({ request }) => {
  test.setTimeout(60_000);
  const unique = randomBytes(6).toString("hex");
  const prefix = `atomic-${unique}`;
  const domain = "diff.local";
  const firstCandidateEmail = `${prefix}_1@${domain}`;
  const duplicateEmail = `${prefix}_2@${domain}`;
  const password = `Atomic-${unique}-password`;

  const legacyAuthorization = await loginBearer(
    request,
    new URL("/api/v1/passport/auth/login", legacyURL).toString(),
    legacyEmail,
    legacyPassword,
    "legacy administrator login"
  );
  const goAuthorization = await loginBearer(
    request,
    new URL("/api/v2/passport/auth/login", goURL).toString(),
    goEmail,
    goPassword,
    "Go legacy-compatible administrator login"
  );

  expectStatus(await generateSingle(request, legacyURL, legacyAuthorization, `${prefix}_2`, domain, password), 200, "legacy duplicate fixture");
  expectStatus(await generateSingle(request, goURL, goAuthorization, `${prefix}_2`, domain, password), 200, "Go duplicate fixture");

  const legacyConflict = await generatePrefixedBatch(request, legacyURL, legacyAuthorization, prefix, domain, 2);
  expect(legacyConflict.status, responseSummary("legacy duplicate batch", legacyConflict)).toBe(400);
  expectLegacyDuplicateFailure(legacyConflict.body);

  const goConflict = await generatePrefixedBatch(request, goURL, goAuthorization, prefix, domain, 2);
  expect(goConflict.status, responseSummary("Go duplicate batch", goConflict)).toBe(409);
  expectGoLegacyDuplicateFailure(goConflict.body);

  await expectExactUserCount(request, legacyURL, legacyAuthorization, firstCandidateEmail, 0, "legacy first candidate");
  await expectExactUserCount(request, legacyURL, legacyAuthorization, duplicateEmail, 1, "legacy duplicate fixture");
  await expectExactUserCount(request, goURL, goAuthorization, firstCandidateEmail, 0, "Go first candidate");
  await expectExactUserCount(request, goURL, goAuthorization, duplicateEmail, 1, "Go duplicate fixture");
});

async function loginBearer(request: APIRequestContext, url: string, email: string, password: string, label: string): Promise<string> {
  const response = await request.post(url, { data: { email, password } });
  const result = await responseResult(response);
  expect(result.status, responseSummary(label, result)).toBe(200);
  const authorization = readString(readObject(readJSON(result.body), "data"), "auth_data");
  expect(/^Bearer /.test(authorization), `${label} returned bearer authorization`).toBe(true);
  return authorization;
}

function generateSingle(
  request: APIRequestContext,
  baseURL: string,
  authorization: string,
  emailPrefix: string,
  emailSuffix: string,
  password: string
): Promise<ResponseResult> {
  return postJSON(request, adminAPI(baseURL, "/user/generate"), authorization, {
    email_prefix: emailPrefix,
    email_suffix: emailSuffix,
    password,
    expired_at: null,
    plan_id: null,
    is_distributor: 0,
    distributor_name: ""
  });
}

function generatePrefixedBatch(
  request: APIRequestContext,
  baseURL: string,
  authorization: string,
  emailPrefix: string,
  emailSuffix: string,
  generateCount: number
): Promise<ResponseResult> {
  return postJSON(request, adminAPI(baseURL, "/user/generate"), authorization, {
    email_prefix: emailPrefix,
    email_suffix: emailSuffix,
    password: "",
    generate_count: generateCount,
    expired_at: null,
    plan_id: null,
    download_csv: false,
    is_distributor: 0,
    distributor_name: ""
  });
}

async function expectExactUserCount(
  request: APIRequestContext,
  baseURL: string,
  authorization: string,
  email: string,
  expected: number,
  label: string
): Promise<void> {
  const result = await postJSON(request, adminAPI(baseURL, "/user/fetch"), authorization, {
    current: 1,
    pageSize: 20,
    filter: [{ id: "email", value: email }],
    sort: [{ id: "id", desc: true }]
  });
  expect(result.status, responseSummary(`${label} fetch`, result)).toBe(200);
  const items = readArray(readJSON(result.body)["data"]).map((item) => readRecord(item));
  const matches = items.filter((item) => item.email === email);
  expect(matches.length, `${label} exact match count`).toBe(expected);
}

function expectStatus(result: ResponseResult, expected: number, label: string): void {
  expect(result.status, responseSummary(label, result)).toBe(expected);
}

function expectLegacyDuplicateFailure(bodyText: string): void {
  const body = readJSON(bodyText);
  expect(readString(body, "status")).toBe("fail");
  expect(readString(body, "message").includes("已存在于系统中"), "legacy duplicate failure message marker").toBe(true);
  expect(readRequired(body, "data")).toBeNull();
  expect(readRequired(body, "error")).toBeNull();
}

function expectGoLegacyDuplicateFailure(bodyText: string): void {
  const body = readJSON(bodyText);
  expect(readString(body, "status")).toBe("fail");
  expect(readString(body, "message") === "邮箱已存在于系统中", "Go duplicate failure message marker").toBe(true);
  expect(readRequired(body, "data")).toBeNull();
  expect(readRequired(body, "error")).toBeNull();
}

async function postJSON(request: APIRequestContext, url: string, authorization: string, data: unknown): Promise<ResponseResult> {
  const response = await request.post(url, { headers: { authorization }, data });
  return responseResult(response);
}

async function responseResult(response: { status(): number; text(): Promise<string> }): Promise<ResponseResult> {
  return { status: response.status(), body: await response.text() };
}

function adminAPI(baseURL: string, path: string): string {
  const base = new URL(baseURL);
  const securePath = base.pathname.replace(/^\/+|\/+$/g, "");
  return new URL(`/api/v2/${securePath}${path}`, base.origin).toString();
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
  return readRecord(readRequired(value, key));
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function responseSummary(label: string, result: ResponseResult): string {
  return `${label} status=${result.status} ${safeBodySummary(result.body)}`;
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
    } else if (key === "message" && typeof entry === "string") {
      result[key] = `<string length=${entry.length}>`;
    } else if (["status", "code"].includes(key)) {
      result[key] = typeof entry === "string" || typeof entry === "number" || typeof entry === "boolean" || entry === null ? entry : typeof entry;
    } else {
      result[key] = Array.isArray(entry) ? `<array length=${entry.length}>` : isRecord(entry) ? "<object>" : typeof entry;
    }
  }
  return result;
}

type ResponseResult = {
  status: number;
  body: string;
};
