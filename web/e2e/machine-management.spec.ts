import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

// This flow handles one-time enrollment and machine credentials. Failure artifacts
// must not persist request bodies, DOM snapshots, screenshots, or video containing them.
test.use({ trace: "off", screenshot: "off", video: "off" });

test("[FE-MACH-001][API-MACH-002][FE-MACH-003][SYS-MACH-004] machine lifecycle stays atomic and recoverable", async ({ page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const unexpectedServerErrors: string[] = [];
  let expectedFailureResponses = 0;
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error" && !message.text().startsWith("Failed to load resource: the server responded with a status of")) {
      consoleErrors.push(message.text());
    }
  });
  page.on("response", (response) => {
    if (response.status() < 500) return;
    const path = new URL(response.url()).pathname;
    const machinePrefix = adminAPIPath("/api/v1/admin/machines/");
    if (response.status() === 503 && path.startsWith(machinePrefix) && /^\d+$/.test(path.slice(machinePrefix.length))) {
      expectedFailureResponses += 1;
      return;
    }
    unexpectedServerErrors.push(`${response.status()} ${response.request().method()} ${path}`);
  });

  await login(page);
  const unique = `${Date.now()}`;
  const nodeName = `MACH 节点 ${unique}`;
  const machineName = `MACH 服务器 ${unique}`;
  const renamedMachine = `${machineName} 已停用`;
  const node = await createNode(page, nodeName);

  await page.getByRole("button", { name: "新增服务器" }).click();
  const createDialog = page.getByRole("dialog", { name: "新增服务器" });
  await createDialog.getByLabel("服务器名称").fill(machineName);
  await createDialog.getByLabel("备注").fill("机器管理浏览器验收夹具");
  const createResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && new URL(response.url()).pathname === adminAPIPath("/api/v1/admin/machines")
  );
  await createDialog.getByRole("button", { name: "创建服务器" }).click();
  const created = await createResponse;
  const createdBody = await created.text();
  expect(created.status()).toBe(201);
  const machineID = createdID(createdBody, "machine");
  const enrollmentCode = stringProperty(readData(createdBody), "token");
  const enrollmentDialog = page.getByRole("dialog", { name: "服务器接入命令" });
  await expect(enrollmentDialog).toContainText("此接入码只展示一次");
  await expect(enrollmentDialog.locator("code")).toContainText("--enrollment-code");
  await enrollmentDialog.getByRole("button", { name: "关闭服务器接入命令" }).click();

  const enrollmentResponse = await page.request.post("/api/v2/server/machine/enroll", {
    data: { machine_id: machineID, enrollment_code: enrollmentCode }
  });
  expect(enrollmentResponse.status()).toBe(200);
  const machineCredential = stringProperty(readData(await enrollmentResponse.text()), "token");
  const statusResponse = await page.request.post("/api/v2/server/machine/status", {
    headers: { Authorization: `Bearer ${machineCredential}` },
    data: {
      machine_id: machineID,
      node_id: 0,
      cpu: 85,
      mem: { total: 1000, used: 920 },
      swap: { total: 100, used: 10 },
      disk: { total: 2000, used: 500 },
      net: { in_speed: 2048, out_speed: 4096 }
    }
  });
  expect(statusResponse.status()).toBe(200);
  const secondStatusResponse = await page.request.post("/api/v2/server/machine/status", {
    headers: { Authorization: `Bearer ${machineCredential}` },
    data: {
      machine_id: machineID,
      node_id: 0,
      cpu: 86,
      mem: { total: 1000, used: 930 },
      swap: { total: 100, used: 10 },
      disk: { total: 2000, used: 600 },
      net: { in_speed: 3072, out_speed: 5120 }
    }
  });
  expect(secondStatusResponse.status()).toBe(200);
  await page.reload();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const search = page.getByRole("searchbox", { name: "搜索" });
  await search.fill(machineName);
  let machineCard = page.locator("article.machine-card", { hasText: machineName });
  await expect(machineCard).toBeVisible();
  await page.getByLabel("状态").selectOption("online");
  await expect(machineCard).toBeVisible();
  await page.getByRole("region", { name: "服务器筛选" }).locator('select:has(option[value="high"])').selectOption("high");
  await expect(machineCard).toBeVisible();
  await page.getByRole("button", { name: "重置" }).click();
  await search.fill(`missing-${unique}`);
  await expect(page.getByText("没有符合当前筛选条件的服务器。")).toBeVisible();
  await page.getByRole("button", { name: "重置" }).click();
  await expect(machineCard).toBeVisible();

  await machineCard.getByRole("button", { name: "服务器详情" }).click();
  const drawer = page.getByRole("dialog", { name: "服务器详情" });
  await expect(drawer.getByRole("heading", { name: "负载与网络" })).toBeVisible();
  await expect(drawer.getByText("86.0%", { exact: true })).toBeVisible();
  await expect(drawer.getByText("93.0%", { exact: true })).toBeVisible();
  await expect(drawer.getByText("3.0 KiB/s / 5.0 KiB/s", { exact: true })).toBeVisible();
  await expect(drawer.getByRole("img", { name: "CPU（蓝）和内存（绿）趋势" })).toBeVisible();
  await expect(drawer.getByRole("img", { name: "网络入站（蓝）和出站（绿）趋势" })).toBeVisible();
  await expect(drawer.getByText("暂无关联节点。")).toBeVisible();

  const rotateButton = drawer.getByRole("button", { name: "生成新的接入命令" });
  const rotationResponsePromise = page.waitForResponse((response) =>
    response.request().method() === "POST" &&
    new URL(response.url()).pathname === adminAPIPath(`/api/v1/admin/machines/${machineID}/enrollments`)
  );
  await rotateButton.click();
  const rotationResponse = await rotationResponsePromise;
  const rotationBody = await rotationResponse.text();
  expect(rotationResponse.status()).toBe(201);
  expect(rotationResponse.headers()["cache-control"]).toBe("no-store");
  const rotationEnrollment = stringProperty(readData(rotationBody), "token");
  await expect(enrollmentDialog).toBeVisible();
  await expect(enrollmentDialog).toContainText("此接入码只展示一次");
  expect(await drawer.evaluate((element) => (element as HTMLElement).inert)).toBe(true);
  await expect(drawer).toHaveAttribute("aria-modal", "false");

  const oldCredentialBeforeExchange = await postMachineStatus(page, machineID, machineCredential, 87);
  expect(oldCredentialBeforeExchange.status()).toBe(200);
  const rotatedEnrollmentResponse = await page.request.post("/api/v2/server/machine/enroll", {
    data: { machine_id: machineID, enrollment_code: rotationEnrollment }
  });
  expect(rotatedEnrollmentResponse.status()).toBe(200);
  expect(rotatedEnrollmentResponse.headers()["cache-control"]).toBe("no-store");
  const newMachineCredential = stringProperty(readData(await rotatedEnrollmentResponse.text()), "token");
  expect((await postMachineStatus(page, machineID, machineCredential, 88)).status()).toBe(401);
  expect((await postMachineStatus(page, machineID, newMachineCredential, 89)).status()).toBe(200);
  const replayedEnrollment = await page.request.post("/api/v2/server/machine/enroll", {
    data: { machine_id: machineID, enrollment_code: rotationEnrollment }
  });
  expect(replayedEnrollment.status()).toBe(401);
  expect(replayedEnrollment.headers()["cache-control"]).toBe("no-store");
  await enrollmentDialog.getByRole("button", { name: "关闭服务器接入命令" }).click();
  await expect(enrollmentDialog).toBeHidden();
  expect(await drawer.evaluate((element) => (element as HTMLElement).inert)).toBe(false);
  await expect(rotateButton).toBeFocused();

  await drawer.getByLabel("待关联节点").selectOption(String(node.id));
  const assignResponse = page.waitForResponse((response) =>
    response.request().method() === "PUT" &&
    new URL(response.url()).pathname === adminAPIPath(`/api/v1/admin/machines/${machineID}/nodes/${node.id}`)
  );
  await drawer.getByRole("button", { name: "关联", exact: true }).click();
  expect((await assignResponse).status()).toBe(204);
  await expect(drawer.getByRole("button", { name: `定时设置：${nodeName}` })).toBeVisible();

  await drawer.getByRole("button", { name: "编辑信息" }).click();
  const editDialog = page.getByRole("dialog", { name: "编辑服务器" });
  await editDialog.getByLabel("服务器名称").fill(renamedMachine);
  await editDialog.getByLabel("备注").fill("失败后保留，再成功提交");
  await editDialog.getByLabel("允许机器接入").uncheck();
  let rejectUpdate = true;
  const machinePath = adminAPIPath(`/api/v1/admin/machines/${machineID}`);
  await page.route(`**${machinePath}`, async (route) => {
    if (route.request().method() === "PATCH" && rejectUpdate) {
      rejectUpdate = false;
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ status: "fail", error: { code: "test_failure", message: "模拟服务器保存失败" } })
      });
      return;
    }
    await route.continue();
  });
  await editDialog.getByRole("button", { name: "保存修改" }).click();
  await expect(editDialog.getByRole("alert")).toHaveText("模拟服务器保存失败");
  await expect(editDialog.getByLabel("服务器名称")).toHaveValue(renamedMachine);
  await expect(editDialog.getByLabel("备注")).toHaveValue("失败后保留，再成功提交");
  await expect(editDialog).toBeVisible();
  await page.unroute(`**${machinePath}`);

  const updateResponse = page.waitForResponse((response) =>
    response.request().method() === "PATCH" && new URL(response.url()).pathname === machinePath
  );
  await editDialog.getByRole("button", { name: "保存修改" }).click();
  expect((await updateResponse).status()).toBe(200);
  await expect(editDialog).toBeHidden();
  await expect(drawer.getByRole("heading", { name: renamedMachine })).toBeVisible();
  await expect(drawer.getByText("已停用", { exact: true })).toBeVisible();
  await drawer.getByRole("button", { name: "关闭服务器详情" }).click();

  await page.getByLabel("状态").selectOption("inactive");
  machineCard = page.locator("article.machine-card", { hasText: renamedMachine });
  await expect(machineCard).toBeVisible();
  await page.getByLabel("承载节点").selectOption("yes");
  await expect(machineCard).toBeVisible();
  await page.getByRole("button", { name: "重置" }).click();

  await machineCard.getByRole("button", { name: "服务器详情" }).click();
  await expect(drawer.getByRole("button", { name: `定时设置：${nodeName}` })).toBeVisible();
  const unassignResponse = page.waitForResponse((response) =>
    response.request().method() === "DELETE" &&
    new URL(response.url()).pathname === adminAPIPath(`/api/v1/admin/machines/${machineID}/nodes/${node.id}`)
  );
  await drawer.getByRole("button", { name: "解除关联" }).click();
  expect((await unassignResponse).status()).toBe(204);
  await expect(drawer.getByText("暂无关联节点。")).toBeVisible();
  const nodeAfterUnassign = await adminRequest(page, `/api/v1/admin/nodes/${node.id}`, "GET");
  expect(nodeAfterUnassign.status, nodeAfterUnassign.body).toBe(200);
  expect(readData(nodeAfterUnassign.body)).toMatchObject({ id: node.id, machine_id: null });

  await drawer.getByLabel("待关联节点").selectOption(String(node.id));
  const reassignResponse = page.waitForResponse((response) =>
    response.request().method() === "PUT" &&
    new URL(response.url()).pathname === adminAPIPath(`/api/v1/admin/machines/${machineID}/nodes/${node.id}`)
  );
  await drawer.getByRole("button", { name: "关联", exact: true }).click();
  expect((await reassignResponse).status()).toBe(204);
  await expect(drawer.getByRole("button", { name: `定时设置：${nodeName}` })).toBeVisible();

  await drawer.getByRole("button", { name: "删除服务器" }).click();
  const deleteDialog = page.getByRole("dialog", { name: "删除服务器" });
  let rejectDelete = true;
  await page.route(`**${machinePath}`, async (route) => {
    if (route.request().method() === "DELETE" && rejectDelete) {
      rejectDelete = false;
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ status: "fail", error: { code: "test_failure", message: "模拟服务器删除失败" } })
      });
      return;
    }
    await route.continue();
  });
  await deleteDialog.getByRole("button", { name: "确认删除" }).click();
  await expect(deleteDialog.getByRole("alert")).toHaveText("模拟服务器删除失败");
  await expect(deleteDialog).toBeVisible();
  await page.unroute(`**${machinePath}`);

  const deleteResponse = page.waitForResponse((response) =>
    response.request().method() === "DELETE" && new URL(response.url()).pathname === machinePath
  );
  await deleteDialog.getByRole("button", { name: "确认删除" }).click();
  expect((await deleteResponse).status()).toBe(204);
  await expect(deleteDialog).toBeHidden();
  await expect(drawer).toBeHidden();
  await expect(page.locator("article.machine-card", { hasText: renamedMachine })).toHaveCount(0);

  const survivingNode = await adminRequest(page, `/api/v1/admin/nodes/${node.id}`, "GET");
  expect(survivingNode.status, survivingNode.body).toBe(200);
  expect(readData(survivingNode.body)).toMatchObject({ id: node.id, machine_id: null });
  const cleanup = await adminRequest(page, "/api/v1/admin/nodes/bulk-delete", "POST", {
    targets: [{ id: node.id, revision: node.revision + 3 }]
  });
  expect(cleanup.status, cleanup.body).toBe(204);

  expect(expectedFailureResponses).toBe(2);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
  expect(unexpectedServerErrors).toEqual([]);
});

async function login(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function postMachineStatus(page: Page, machineID: number, credential: string, cpu: number) {
  return page.request.post("/api/v2/server/machine/status", {
    headers: { Authorization: `Bearer ${credential}` },
    data: {
      machine_id: machineID,
      node_id: 0,
      cpu,
      mem: { total: 1000, used: 900 },
      swap: { total: 100, used: 10 },
      disk: { total: 2000, used: 500 },
      net: { in_speed: 1024, out_speed: 2048 }
    }
  });
}

async function createNode(page: Page, name: string): Promise<{ id: number; revision: number }> {
  const result = await adminRequest(page, "/api/v1/admin/nodes", "POST", {
    name,
    type: "vless",
    host: "machine-e2e.example.test",
    port: "443",
    show: true,
    enabled: true,
    sort: 0
  });
  expect(result.status, result.body).toBe(201);
  const data = readData(result.body);
  const id = Number(Reflect.get(data, "id"));
  const revision = Number(Reflect.get(data, "revision"));
  if (!Number.isSafeInteger(id) || id < 1 || !Number.isSafeInteger(revision) || revision < 1) {
    throw new Error("created node response is missing a positive identity");
  }
  return { id, revision };
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
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
  }, { requestPath: adminAPIPath(path), requestMethod: method, requestBody: body });
}

function readData(body: string): object {
  const payload: unknown = JSON.parse(body);
  const data: unknown = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  if (typeof data !== "object" || data === null) throw new Error("response data is not an object");
  return data;
}

function stringProperty(value: object, name: string): string {
  const property = Reflect.get(value, name);
  if (typeof property !== "string" || property === "") throw new Error(`response data is missing ${name}`);
  return property;
}

function createdID(body: string, resource: string): number {
  const id = Number(Reflect.get(readData(body), "id"));
  if (!Number.isSafeInteger(id) || id < 1) throw new Error(`created ${resource} response is missing a positive id`);
  return id;
}
