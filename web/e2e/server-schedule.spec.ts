import { expect, test } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

test("[FE-SCH-001][FE-SCH-002][FE-SCH-003][FE-SCH-004][FE-SCH-005][FE-SCH-006][FE-SCH-007][FE-SCH-008] nested schedule modal is complete", async ({ page }) => {
  const pageErrors: string[] = [];
  const consoleErrors: string[] = [];
  const serverErrors: string[] = [];
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
    const schedulePrefix = adminAPIPath("/api/v1/admin/nodes/");
    if (response.status() === 503 && response.request().method() === "PUT" && path.startsWith(schedulePrefix) && /^\d+\/activation-schedule$/.test(path.slice(schedulePrefix.length))) {
      expectedFailureResponses += 1;
      return;
    }
    serverErrors.push(`${response.status()} ${response.request().method()} ${path}`);
  });

  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const unique = `${Date.now()}`;
  const nodeName = `E2E 节点 ${unique}`;
  const machineName = `E2E 服务器 ${unique}`;
  const createNodeResult = await page.evaluate(async ({ name, path }) => {
    const prefix = "xboard_csrf=";
    const csrf = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length);
    const response = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf ?? "") },
      body: JSON.stringify({ name, type: "vless", host: "sg.example.test", port: "443", show: true, enabled: true, sort: 0 })
    });
    return { status: response.status, body: await response.text() };
  }, { name: nodeName, path: adminAPIPath("/api/v1/admin/nodes") });
  expect(createNodeResult.status, createNodeResult.body).toBe(201);
  const nodeIdentity = createdNodeIdentity(createNodeResult.body);

  await page.getByRole("button", { name: "新增服务器" }).click();
  const createDialog = page.getByRole("dialog", { name: "新增服务器" });
  await createDialog.getByLabel("服务器名称").fill(machineName);
  await createDialog.getByLabel("备注").fill("Playwright regression fixture");
  const machineResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && new URL(response.url()).pathname === adminAPIPath("/api/v1/admin/machines")
  );
  await createDialog.getByRole("button", { name: "创建服务器" }).click();
  const createdMachineResponse = await machineResponse;
  expect(createdMachineResponse.status()).toBe(201);
  const machineID = createdID(await createdMachineResponse.text(), "machine");
  const enrollmentDialog = page.getByRole("dialog", { name: "服务器接入命令" });
  await expect(enrollmentDialog).toBeVisible();
  await enrollmentDialog.getByRole("button", { name: "关闭服务器接入命令" }).click();

  const assignmentPath = adminAPIPath(`/api/v1/admin/machines/${machineID}/nodes/${nodeIdentity.id}`);
  const assignment = await page.evaluate(async ({ revision, path }) => {
    const prefix = "xboard_csrf=";
    const csrf = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length);
    const response = await fetch(path, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf ?? "") },
      body: JSON.stringify({ revision })
    });
    return { status: response.status, body: await response.text() };
  }, { revision: nodeIdentity.revision, path: assignmentPath });
  expect(assignment.status, assignment.body).toBe(204);

  const machineCard = page.locator("article.machine-card", { hasText: machineName });
  await expect(machineCard).toBeVisible();
  await machineCard.getByRole("button", { name: "服务器详情" }).click();
  const drawer = page.getByRole("dialog", { name: "服务器详情" });
  await expect(drawer).toBeVisible();

  const scheduleButton = drawer.getByRole("button", { name: `定时设置：${nodeName}` });
  await expect(scheduleButton).toBeVisible();
  await expect(scheduleButton).toHaveCount(1);

  await drawer.getByRole("button", { name: "关闭服务器详情" }).click();
  await expect(drawer).toBeHidden();
  await machineCard.getByRole("button", { name: "服务器详情" }).click();
  await expect(drawer.getByRole("button", { name: `定时设置：${nodeName}` })).toHaveCount(1);
  await drawer.getByRole("button", { name: `定时设置：${nodeName}` }).click();

  const scheduleDialog = page.getByRole("dialog", { name: "激活计划设置" });
  await expect(scheduleDialog).toBeVisible();
  await expect(scheduleDialog.getByLabel("启用时间")).toBeEnabled();
  const layers = await page.evaluate(() => ({
    drawer: Number.parseInt(getComputedStyle(document.querySelector("[data-testid=\"drawer-layer\"]") as HTMLElement).zIndex, 10),
    modal: Number.parseInt(getComputedStyle(document.querySelector("[data-testid=\"modal-layer\"]") as HTMLElement).zIndex, 10)
  }));
  expect(layers.modal).toBeGreaterThan(layers.drawer);
  expect(await drawer.evaluate((element) => (element as HTMLElement).inert)).toBe(true);
  await expect(drawer).toHaveAttribute("aria-modal", "false");

  const viewport = page.viewportSize();
  const dialogBox = await scheduleDialog.boundingBox();
  expect(viewport).not.toBeNull();
  expect(dialogBox).not.toBeNull();
  if (viewport !== null && dialogBox !== null) {
    expect(dialogBox.x).toBeGreaterThanOrEqual(0);
    expect(dialogBox.x + dialogBox.width).toBeLessThanOrEqual(viewport.width + 1);
  }
  expect(await scheduleDialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);

  const closeSchedule = scheduleDialog.getByRole("button", { name: "关闭激活计划设置" });
  const saveSchedule = scheduleDialog.locator('button[type="submit"]');
  await expect(saveSchedule).toHaveText("保存计划");
  await closeSchedule.focus();
  await page.keyboard.press("Shift+Tab");
  await expect(saveSchedule).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(closeSchedule).toBeFocused();

  await scheduleDialog.getByLabel("启用时间").fill("20:30");
  await scheduleDialog.getByLabel("停用时间").fill("01:00");
  const schedulePath = adminAPIPath(`/api/v1/admin/nodes/${nodeIdentity.id}/activation-schedule`);
  let signalFailureRequest!: () => void;
  let releaseFailureResponse!: () => void;
  const failureRequest = new Promise<void>((resolve) => { signalFailureRequest = resolve; });
  const failureResponseRelease = new Promise<void>((resolve) => { releaseFailureResponse = resolve; });
  let saveRequests = 0;
  await page.route(`**${schedulePath}`, async (route) => {
    if (route.request().method() !== "PUT") {
      await route.continue();
      return;
    }
    saveRequests += 1;
    signalFailureRequest();
    await failureResponseRelease;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ status: "fail", error: { code: "test_failure", message: "模拟计划保存失败" } })
    });
  });
  await saveSchedule.click();
  await failureRequest;
  await expect(saveSchedule).toBeDisabled();
  await saveSchedule.evaluate((button: HTMLButtonElement) => button.click());
  expect(saveRequests).toBe(1);
  releaseFailureResponse();
  await expect(scheduleDialog.getByRole("alert")).toHaveText("模拟计划保存失败");
  await expect(scheduleDialog.getByLabel("启用时间")).toHaveValue("20:30");
  await expect(scheduleDialog.getByLabel("停用时间")).toHaveValue("01:00");
  await expect(scheduleDialog).toBeVisible();
  await page.unroute(`**${schedulePath}`);

  await saveSchedule.click();
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();

  const restoredScheduleButton = drawer.getByRole("button", { name: `定时设置：${nodeName}` });
  await restoredScheduleButton.click();
  await expect(scheduleDialog.getByLabel("启用时间")).toHaveValue("20:30");
  await page.keyboard.press("Escape");
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();
  await expect(restoredScheduleButton).toBeFocused();

  await restoredScheduleButton.click();
  await expect(scheduleDialog.getByRole("button", { name: "删除计划" })).toBeVisible();
  const deleteResponse = page.waitForResponse((response) =>
    response.request().method() === "DELETE" && new URL(response.url()).pathname === schedulePath
  );
  await scheduleDialog.getByRole("button", { name: "删除计划" }).click();
  expect((await deleteResponse).status()).toBe(204);
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();

  await restoredScheduleButton.click();
  await expect(scheduleDialog.getByLabel("启用时间")).toBeEnabled();
  await scheduleDialog.getByRole("button", { name: "取消" }).click();
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();
  await expect(restoredScheduleButton).toBeFocused();

  expect(expectedFailureResponses).toBe(1);
  expect(pageErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

function createdNodeIdentity(body: string): { id: number; revision: number } {
  const id = createdID(body, "node");
  const payload: unknown = JSON.parse(body);
  const data: unknown = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  const revision = typeof data === "object" && data !== null ? Number(Reflect.get(data, "revision")) : Number.NaN;
  if (!Number.isSafeInteger(revision) || revision < 1) {
    throw new Error("created node response is missing a positive revision");
  }
  return { id, revision };
}

function createdID(body: string, resource: string): number {
  const payload: unknown = JSON.parse(body);
  const data: unknown = typeof payload === "object" && payload !== null ? Reflect.get(payload, "data") : null;
  const id = typeof data === "object" && data !== null ? Number(Reflect.get(data, "id")) : Number.NaN;
  if (!Number.isSafeInteger(id) || id < 1) throw new Error(`created ${resource} response is missing a positive id`);
  return id;
}
