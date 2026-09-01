import { randomBytes } from "node:crypto";
import * as net from "node:net";
import * as tls from "node:tls";

import { expect, test, type Page } from "@playwright/test";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");
const legacyWebSocketURL = process.env.LEGACY_NODE_WS_URL?.trim() || webSocketURL(legacyURL);
const goWebSocketURL = process.env.XBOARD_GO_WS_URL?.trim() || webSocketURL(goURL);

test.use({ trace: "off", screenshot: "off", video: "off" });

test("[DIFF-NODE-004] legacy and Go dual credentials enforce the same HTTP and WebSocket ownership boundary", async ({ browser }) => {
  test.setTimeout(120_000);
  const legacyContext = await browser.newContext();
  const goContext = await browser.newContext();
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const unique = `${Date.now()}-${randomBytes(4).toString("hex")}`;
  const legacyToken = `legacy-${unique}-${randomBytes(18).toString("base64url")}`;
  const rotatedLegacyToken = `legacy-rotated-${unique}-${randomBytes(18).toString("base64url")}`;
  const goToken = `go-${unique}-${randomBytes(20).toString("base64url")}`;
  const rotatedGoToken = `go-rotated-${unique}-${randomBytes(20).toString("base64url")}`;
  const legacyNodeIDs: number[] = [];
  const legacyMachineIDs: number[] = [];
  const goNodes: Array<{ id: number; revision: number }> = [];
  const goMachineIDs: number[] = [];
  let legacyAuthorization = "";
  let legacyOriginal: Record<string, unknown> | undefined;
  let goOriginal: Record<string, unknown> | undefined;

  try {
    legacyAuthorization = await loginLegacyAdministrator(legacyPage);
    legacyOriginal = await getLegacyServerSettings(legacyPage, legacyAuthorization);
    await setLegacyServerSettings(legacyPage, legacyAuthorization, {
      server_token: legacyToken,
      server_ws_enable: 1,
      server_ws_url: readString(legacyOriginal, "server_ws_url")
    });

    await loginGoAdministrator(goPage);
    goOriginal = await getGoNodeSettings(goPage);
    expect(readBoolean(goOriginal, "server_token_configured"), "DIFF-NODE-004 requires a disposable Go target with no pre-existing global token").toBe(false);
    await setGoNodeSettings(goPage, goOriginal, goToken);

    const legacyMachineA = await createLegacyMachine(legacyPage, legacyAuthorization, `diff-a-${unique}`);
    const legacyMachineB = await createLegacyMachine(legacyPage, legacyAuthorization, `diff-b-${unique}`);
    legacyMachineIDs.push(legacyMachineA.id, legacyMachineB.id);
    const legacyCredentialA = await exchangeMachineEnrollment(legacyPage, legacyURL, legacyMachineA.id, legacyMachineA.enrollmentCode);
    await exchangeMachineEnrollment(legacyPage, legacyURL, legacyMachineB.id, legacyMachineB.enrollmentCode);

    const goMachineA = await createGoMachine(goPage, `diff-a-${unique}`);
    const goMachineB = await createGoMachine(goPage, `diff-b-${unique}`);
    goMachineIDs.push(goMachineA.id, goMachineB.id);
    const goCredentialA = await exchangeMachineEnrollment(goPage, goURL, goMachineA.id, goMachineA.enrollmentCode);
    await exchangeMachineEnrollment(goPage, goURL, goMachineB.id, goMachineB.enrollmentCode);

    const legacyAssigned = await createLegacyNode(legacyPage, legacyAuthorization, `diff-assigned-${unique}`, legacyMachineA.id, true);
    const legacyDisabled = await createLegacyNode(legacyPage, legacyAuthorization, `diff-disabled-${unique}`, legacyMachineA.id, false);
    const legacyCross = await createLegacyNode(legacyPage, legacyAuthorization, `diff-cross-${unique}`, legacyMachineB.id, true);
    const legacyUnassigned = await createLegacyNode(legacyPage, legacyAuthorization, `diff-unassigned-${unique}`, null, true);
    legacyNodeIDs.push(legacyAssigned, legacyDisabled, legacyCross, legacyUnassigned);

    const goAssigned = await createGoNode(goPage, `diff-assigned-${unique}`, goMachineA.id, true);
    const goDisabled = await createGoNode(goPage, `diff-disabled-${unique}`, goMachineA.id, false);
    const goCross = await createGoNode(goPage, `diff-cross-${unique}`, goMachineB.id, true);
    const goUnassigned = await createGoNode(goPage, `diff-unassigned-${unique}`, null, true);
    goNodes.push(goAssigned, goDisabled, goCross, goUnassigned);

    const httpCases = [
      { name: "global credential accesses enabled assigned node", legacyNode: legacyAssigned, goNode: goAssigned.id, legacyCredential: legacyToken, goCredential: goToken, machine: false, legacyAllowed: true, goAllowed: true },
      { name: "global credential accesses enabled unassigned node", legacyNode: legacyUnassigned, goNode: goUnassigned.id, legacyCredential: legacyToken, goCredential: goToken, machine: false, legacyAllowed: true, goAllowed: true },
      { name: "wrong global credential is rejected", legacyNode: legacyAssigned, goNode: goAssigned.id, legacyCredential: "wrong-global-credential", goCredential: "wrong-global-credential", machine: false, legacyAllowed: false, goAllowed: false },
      // The fixed oracle does not apply its enabled check to global-token HTTP or WS.
      // D-001 deliberately keeps the Go side fail-closed instead of cloning that privilege bug.
      { name: "Go hardens disabled-node access omitted by the legacy global-token path", legacyNode: legacyDisabled, goNode: goDisabled.id, legacyCredential: legacyToken, goCredential: goToken, machine: false, legacyAllowed: true, goAllowed: false },
      { name: "machine credential accesses its enabled node", legacyNode: legacyAssigned, goNode: goAssigned.id, legacyCredential: legacyCredentialA, goCredential: goCredentialA, machine: true, legacyAllowed: true, goAllowed: true },
      { name: "wrong machine credential is rejected", legacyNode: legacyAssigned, goNode: goAssigned.id, legacyCredential: "wrong-machine-credential", goCredential: "wrong-machine-credential", machine: true, legacyAllowed: false, goAllowed: false },
      { name: "machine credential cannot cross machines", legacyNode: legacyCross, goNode: goCross.id, legacyCredential: legacyCredentialA, goCredential: goCredentialA, machine: true, legacyAllowed: false, goAllowed: false },
      { name: "machine credential cannot access disabled node", legacyNode: legacyDisabled, goNode: goDisabled.id, legacyCredential: legacyCredentialA, goCredential: goCredentialA, machine: true, legacyAllowed: false, goAllowed: false },
      { name: "machine credential cannot access unassigned node", legacyNode: legacyUnassigned, goNode: goUnassigned.id, legacyCredential: legacyCredentialA, goCredential: goCredentialA, machine: true, legacyAllowed: false, goAllowed: false }
    ];
    for (const item of httpCases) {
      const legacyAllowed = await nodeConfigAllowed(legacyPage, legacyURL, item.legacyNode, item.legacyCredential, item.machine ? legacyMachineA.id : null);
      const goAllowed = await nodeConfigAllowed(goPage, goURL, item.goNode, item.goCredential, item.machine ? goMachineA.id : null);
      expect(legacyAllowed, `legacy: ${item.name}`).toBe(item.legacyAllowed);
      expect(goAllowed, `Go: ${item.name}`).toBe(item.goAllowed);
    }

    const legacyGlobalWS = await probeWebSocket(legacyWebSocketURL, { node_id: legacyAssigned }, legacyToken);
    const goGlobalWS = await probeWebSocket(goWebSocketURL, { node_id: goAssigned.id }, goToken);
    expect(legacyGlobalWS.accepted, `legacy WebSocket result: ${legacyGlobalWS.payload}`).toBe(true);
    expect(goGlobalWS.accepted).toBe(legacyGlobalWS.accepted);
    expect((await probeWebSocket(legacyWebSocketURL, { node_id: legacyAssigned }, "wrong-global-credential")).accepted).toBe(false);
    expect((await probeWebSocket(goWebSocketURL, { node_id: goAssigned.id }, "wrong-global-credential")).accepted).toBe(false);
    expect((await probeWebSocket(legacyWebSocketURL, { node_id: legacyDisabled }, legacyToken)).accepted).toBe(true);
    expect((await probeWebSocket(goWebSocketURL, { node_id: goDisabled.id }, goToken)).accepted).toBe(false);

    const legacyMachineWS = await probeWebSocket(legacyWebSocketURL, { machine_id: legacyMachineA.id }, legacyCredentialA);
    const goMachineWS = await probeWebSocket(goWebSocketURL, { machine_id: goMachineA.id }, goCredentialA);
    expect(legacyMachineWS.accepted, `legacy machine WebSocket result: ${legacyMachineWS.payload}`).toBe(true);
    expect(goMachineWS.accepted).toBe(legacyMachineWS.accepted);
    expect(readAuthNodeIDs(legacyMachineWS.payload)).toEqual([legacyAssigned]);
    expect(readAuthNodeIDs(goMachineWS.payload)).toEqual([goAssigned.id]);
    expect((await probeWebSocket(legacyWebSocketURL, { machine_id: legacyMachineA.id }, "wrong-machine-credential")).accepted).toBe(false);
    expect((await probeWebSocket(goWebSocketURL, { machine_id: goMachineA.id }, "wrong-machine-credential")).accepted).toBe(false);

    await setLegacyServerSettings(legacyPage, legacyAuthorization, { server_token: rotatedLegacyToken });
    const currentGoSettings = await getGoNodeSettings(goPage);
    await setGoNodeSettings(goPage, currentGoSettings, rotatedGoToken);
    expect(await nodeConfigAllowed(legacyPage, legacyURL, legacyAssigned, legacyToken, null)).toBe(false);
    expect(await nodeConfigAllowed(goPage, goURL, goAssigned.id, goToken, null)).toBe(false);
    expect(await nodeConfigAllowed(legacyPage, legacyURL, legacyAssigned, rotatedLegacyToken, null)).toBe(true);
    expect(await nodeConfigAllowed(goPage, goURL, goAssigned.id, rotatedGoToken, null)).toBe(true);
    expect((await probeWebSocket(legacyWebSocketURL, { node_id: legacyAssigned }, legacyToken)).accepted).toBe(false);
    expect((await probeWebSocket(goWebSocketURL, { node_id: goAssigned.id }, goToken)).accepted).toBe(false);

    const rotatedLegacyEnrollment = await rotateLegacyMachine(legacyPage, legacyAuthorization, legacyMachineA.id);
    const rotatedGoEnrollment = await rotateGoMachine(goPage, goMachineA.id);
    const rotatedLegacyCredential = await exchangeMachineEnrollment(legacyPage, legacyURL, legacyMachineA.id, rotatedLegacyEnrollment);
    const rotatedGoCredential = await exchangeMachineEnrollment(goPage, goURL, goMachineA.id, rotatedGoEnrollment);
    expect(await nodeConfigAllowed(legacyPage, legacyURL, legacyAssigned, legacyCredentialA, legacyMachineA.id)).toBe(false);
    expect(await nodeConfigAllowed(goPage, goURL, goAssigned.id, goCredentialA, goMachineA.id)).toBe(false);
    expect(await nodeConfigAllowed(legacyPage, legacyURL, legacyAssigned, rotatedLegacyCredential, legacyMachineA.id)).toBe(true);
    expect(await nodeConfigAllowed(goPage, goURL, goAssigned.id, rotatedGoCredential, goMachineA.id)).toBe(true);
    expect((await probeWebSocket(legacyWebSocketURL, { machine_id: legacyMachineA.id }, legacyCredentialA)).accepted).toBe(false);
    expect((await probeWebSocket(goWebSocketURL, { machine_id: goMachineA.id }, goCredentialA)).accepted).toBe(false);
  } finally {
    for (const nodeID of legacyNodeIDs) await bestEffortLegacyPost(legacyPage, legacyAuthorization, "/server/manage/drop", { id: nodeID });
    for (const machineID of legacyMachineIDs) await bestEffortLegacyPost(legacyPage, legacyAuthorization, "/server/machine/drop", { id: machineID });
    if (legacyOriginal && legacyAuthorization) await bestEffortLegacyPost(legacyPage, legacyAuthorization, "/config/save", legacyOriginal);
    if (goNodes.length > 0) await bestEffortGoRequest(goPage, "/api/v1/admin/nodes/bulk-delete", "POST", { targets: goNodes });
    for (const machineID of goMachineIDs) await bestEffortGoRequest(goPage, `/api/v1/admin/machines/${machineID}`, "DELETE");
    if (goOriginal) {
      const current = await getGoNodeSettings(goPage).catch(() => undefined);
      if (current) await bestEffortGoRequest(goPage, "/api/v1/admin/node-agent-settings", "PUT", {
        revision: readNumber(current, "revision"), server_token: "",
        server_pull_interval: readNumber(goOriginal, "server_pull_interval"), server_push_interval: readNumber(goOriginal, "server_push_interval"),
        device_limit_mode: readNumber(goOriginal, "device_limit_mode"), server_ws_enable: readBoolean(goOriginal, "server_ws_enable"),
        server_ws_url: readString(goOriginal, "server_ws_url")
      });
    }
    await legacyContext.close();
    await goContext.close();
  }
});

async function loginLegacyAdministrator(page: Page): Promise<string> {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await fields.first().fill(legacyEmail);
  await fields.nth(1).fill(legacyPassword);
  await fields.nth(1).press("Enter");
  await expect(page.locator('a[href="#/config/system"]')).toBeVisible({ timeout: 60_000 });
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization ?? "";
  if (!authorization) throw new Error("legacy administrator authorization is missing");
  return authorization;
}

async function loginGoAdministrator(page: Page): Promise<void> {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible({ timeout: 60_000 });
}

async function getLegacyServerSettings(page: Page, authorization: string): Promise<Record<string, unknown>> {
  const response = await page.request.get(legacyAdminAPI("/config/fetch"), { headers: { authorization } });
  expect(response.status()).toBe(200);
  return readObject(readObject(readJSON(await response.text()), "data"), "server");
}

async function setLegacyServerSettings(page: Page, authorization: string, settings: Record<string, unknown>): Promise<void> {
  const response = await page.request.post(legacyAdminAPI("/config/save"), { headers: { authorization }, data: settings });
  expect(response.status()).toBe(200);
}

async function getGoNodeSettings(page: Page): Promise<Record<string, unknown>> {
  const response = await goAdminRequest(page, "/api/v1/admin/node-agent-settings", "GET");
  expect(response.status).toBe(200);
  return readObject(readJSON(response.body), "data");
}

async function setGoNodeSettings(page: Page, current: Record<string, unknown>, token: string): Promise<void> {
  const response = await goAdminRequest(page, "/api/v1/admin/node-agent-settings", "PUT", {
    revision: readNumber(current, "revision"), server_token: token,
    server_pull_interval: readNumber(current, "server_pull_interval"), server_push_interval: readNumber(current, "server_push_interval"),
    device_limit_mode: readNumber(current, "device_limit_mode"), server_ws_enable: true,
    server_ws_url: readString(current, "server_ws_url")
  });
  expect(response.status).toBe(200);
}

async function createLegacyMachine(page: Page, authorization: string, name: string): Promise<{ id: number; enrollmentCode: string }> {
  const response = await page.request.post(legacyAdminAPI("/server/machine/save"), { headers: { authorization }, data: { name, notes: "DIFF-NODE-004", is_active: 1 } });
  expect(response.status()).toBe(200);
  const data = readObject(readJSON(await response.text()), "data");
  return { id: readNumber(data, "id"), enrollmentCode: readString(data, "token") };
}

async function createGoMachine(page: Page, name: string): Promise<{ id: number; enrollmentCode: string }> {
  const response = await goAdminRequest(page, "/api/v1/admin/machines", "POST", { name, notes: "DIFF-NODE-004", is_active: true });
  expect(response.status).toBe(201);
  const data = readObject(readJSON(response.body), "data");
  return { id: readNumber(data, "id"), enrollmentCode: readString(data, "token") };
}

async function exchangeMachineEnrollment(page: Page, baseURL: string, machineID: number, enrollmentCode: string): Promise<string> {
  const response = await page.request.post(new URL("/api/v2/server/machine/enroll", baseURL).toString(), {
    data: { machine_id: machineID, enrollment_code: enrollmentCode }
  });
  expect(response.status()).toBe(200);
  return readString(readObject(readJSON(await response.text()), "data"), "token");
}

async function rotateLegacyMachine(page: Page, authorization: string, machineID: number): Promise<string> {
  const response = await page.request.post(legacyAdminAPI("/server/machine/resetToken"), { headers: { authorization }, data: { id: machineID } });
  expect(response.status()).toBe(200);
  return readString(readObject(readJSON(await response.text()), "data"), "token");
}

async function rotateGoMachine(page: Page, machineID: number): Promise<string> {
  const response = await goAdminRequest(page, `/api/v1/admin/machines/${machineID}/enrollments`, "POST", { revoke_existing: true });
  expect(response.status).toBe(201);
  return readString(readObject(readJSON(response.body), "data"), "token");
}

async function createLegacyNode(page: Page, authorization: string, name: string, machineID: number | null, enabled: boolean): Promise<number> {
  const response = await page.request.post(legacyAdminAPI("/server/manage/save"), { headers: { authorization }, data: {
    type: "vless", name, host: "diff.example.test", port: "443", server_port: 443, rate: 1,
    show: 1, enabled, machine_id: machineID, group_ids: [], route_ids: [], tags: [],
    protocol_settings: { tls: 0, network: "tcp", network_settings: {} }, rate_time_enable: false,
    rate_time_ranges: [], custom_outbounds: [], custom_routes: [], cert_config: {}
  } });
  expect(response.status()).toBe(200);
  const listed = await page.request.get(legacyAdminAPI("/server/manage/getNodes"), { headers: { authorization } });
  expect(listed.status()).toBe(200);
  const items = readArray(readJSON(await listed.text())["data"]);
  const node = items.find((item) => isRecord(item) && readString(item, "name") === name);
  if (!isRecord(node)) throw new Error("legacy fixture node was not returned by getNodes");
  return readNumber(node, "id");
}

async function createGoNode(page: Page, name: string, machineID: number | null, enabled: boolean): Promise<{ id: number; revision: number }> {
  const created = await goAdminRequest(page, "/api/v1/admin/nodes", "POST", {
    name, type: "vless", host: "diff.example.test", port: "443", show: true, enabled, sort: 0, machine_id: machineID
  });
  expect(created.status).toBe(201);
  const data = readObject(readJSON(created.body), "data");
  const identity = { id: readNumber(data, "id"), revision: readNumber(data, "revision") };
  const runtime = await goAdminRequest(page, `/api/v1/admin/nodes/${identity.id}/runtime`, "PUT", {
    rate: 1, group_ids: [], route_ids: [], config: { protocol: "vless", listen_ip: "0.0.0.0", server_port: 443 }
  });
  expect(runtime.status).toBe(200);
  return identity;
}

async function nodeConfigAllowed(page: Page, baseURL: string, nodeID: number, credential: string, machineID: number | null): Promise<boolean> {
  const parameters = new URLSearchParams({ node_id: String(nodeID) });
  if (machineID !== null) parameters.set("machine_id", String(machineID));
  const response = await page.request.get(new URL(`/api/v2/server/config?${parameters}`, baseURL).toString(), {
    headers: { Authorization: `Bearer ${credential}` }
  });
  return response.status() >= 200 && response.status() < 300;
}

async function goAdminRequest(page: Page, path: string, method: string, body?: unknown): Promise<{ status: number; body: string }> {
  return page.evaluate(async ({ requestPath, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod, credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { requestPath: path, requestMethod: method, requestBody: body });
}

async function bestEffortLegacyPost(page: Page, authorization: string, path: string, data: unknown): Promise<void> {
  if (!authorization) return;
  await page.request.post(legacyAdminAPI(path), { headers: { authorization }, data }).catch(() => undefined);
}

async function bestEffortGoRequest(page: Page, path: string, method: string, body?: unknown): Promise<void> {
  await goAdminRequest(page, path, method, body).catch(() => undefined);
}

function probeWebSocket(baseURL: string, query: Record<string, number>, credential: string): Promise<{ accepted: boolean; payload: string }> {
  return new Promise((resolve, reject) => {
    const target = new URL(baseURL);
    for (const [key, value] of Object.entries(query)) target.searchParams.set(key, String(value));
    const secure = target.protocol === "wss:";
    const port = target.port ? Number(target.port) : secure ? 443 : 80;
    const socket = secure
      ? tls.connect({ host: target.hostname, port, servername: target.hostname })
      : net.connect({ host: target.hostname, port });
    let settled = false;
    let response = Buffer.alloc(0);
    let upgraded = false;
    const finish = (result: { accepted: boolean; payload: string }) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      socket.destroy();
      resolve(result);
    };
    const timer = setTimeout(() => finish({ accepted: false, payload: "timeout" }), 5_000);
    socket.once("error", (error: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(new Error(`WebSocket transport failed: ${error.message}`));
    });
    socket.once(secure ? "secureConnect" : "connect", () => {
      const path = `${target.pathname || "/"}${target.search}`;
      socket.write([
        `GET ${path} HTTP/1.1`, `Host: ${target.host}`, "Upgrade: websocket", "Connection: Upgrade",
        `Sec-WebSocket-Key: ${randomBytes(16).toString("base64")}`, "Sec-WebSocket-Version: 13",
        `Authorization: Bearer ${credential}`, "", ""
      ].join("\r\n"));
    });
    socket.on("data", (chunk) => {
      response = Buffer.concat([response, chunk]);
      if (!upgraded) {
        const headerEnd = response.indexOf("\r\n\r\n");
        if (headerEnd < 0) return;
        const statusLine = response.subarray(0, headerEnd).toString("ascii").split("\r\n", 1)[0];
        if (!/^HTTP\/1\.[01] 101\b/.test(statusLine)) return finish({ accepted: false, payload: statusLine });
        upgraded = true;
        response = response.subarray(headerEnd + 4);
      }
      const payload = firstWebSocketPayload(response);
      if (payload === undefined) return;
      finish({ accepted: payload.includes('"auth.success"'), payload });
    });
  });
}

function firstWebSocketPayload(frame: Buffer): string | undefined {
  if (frame.length < 2) return undefined;
  let length = frame[1] & 0x7f;
  let offset = 2;
  if (length === 126) {
    if (frame.length < 4) return undefined;
    length = frame.readUInt16BE(2); offset = 4;
  } else if (length === 127) {
    if (frame.length < 10) return undefined;
    const large = frame.readBigUInt64BE(2);
    if (large > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("WebSocket frame is too large");
    length = Number(large); offset = 10;
  }
  if (frame.length < offset + length) return undefined;
  return frame.subarray(offset, offset + length).toString("utf8");
}

function readAuthNodeIDs(payload: string): number[] {
  const data = readObject(readJSON(payload), "data");
  return readArray(data["node_ids"]).map((value) => Number(value)).filter((value) => Number.isSafeInteger(value)).sort((a, b) => a - b);
}

function legacyAdminAPI(path: string): string {
  const securePath = new URL(legacyURL).pathname.replace(/\/$/, "");
  return new URL(`/api/v2${securePath}${path}`, legacyURL).toString();
}

function webSocketURL(baseURL: string): string {
  const target = new URL("/ws", baseURL);
  target.protocol = target.protocol === "https:" ? "wss:" : "ws:";
  return target.toString();
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
  const result = value[key];
  if (!isRecord(result)) throw new Error(`response property ${key} is not an object`);
  return result;
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

function readNumber(value: Record<string, unknown>, key: string): number {
  const result = Number(value[key]);
  if (!Number.isSafeInteger(result)) throw new Error(`response property ${key} is not an integer`);
  return result;
}

function readBoolean(value: Record<string, unknown>, key: string): boolean {
  const result = value[key];
  if (typeof result !== "boolean") throw new Error(`response property ${key} is not a boolean`);
  return result;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
