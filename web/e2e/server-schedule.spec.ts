import { expect, test } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("first-open schedule trigger and nested modal remain fully interactive", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();

  const unique = `${Date.now()}`;
  const nodeName = `E2E 节点 ${unique}`;
  const machineName = `E2E 服务器 ${unique}`;
  const createNodeResult = await page.evaluate(async ({ name }) => {
    const prefix = "xboard_csrf=";
    const csrf = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length);
    const response = await fetch("/api/v1/admin/nodes", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf ?? "") },
      body: JSON.stringify({ name, type: "vless", host: "sg.example.test", port: "443", show: true, enabled: true, sort: 0 })
    });
    return { status: response.status, body: await response.text() };
  }, { name: nodeName });
  expect(createNodeResult.status, createNodeResult.body).toBe(201);
  const nodeIdentity = createdNodeIdentity(createNodeResult.body);

  await page.getByRole("button", { name: "新增服务器" }).click();
  const createDialog = page.getByRole("dialog", { name: "新增服务器" });
  await createDialog.getByLabel("服务器名称").fill(machineName);
  await createDialog.getByLabel("备注").fill("Playwright regression fixture");
  const machineResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && new URL(response.url()).pathname === "/api/v1/admin/machines"
  );
  await createDialog.getByRole("button", { name: "创建服务器" }).click();
  const createdMachineResponse = await machineResponse;
  expect(createdMachineResponse.status()).toBe(201);
  const machineID = createdID(await createdMachineResponse.text(), "machine");
  const enrollmentDialog = page.getByRole("dialog", { name: "服务器接入命令" });
  await expect(enrollmentDialog).toBeVisible();
  await enrollmentDialog.getByRole("button", { name: "关闭服务器接入命令" }).click();

  const assignment = await page.evaluate(async ({ machineID, nodeID, revision }) => {
    const prefix = "xboard_csrf=";
    const csrf = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length);
    const response = await fetch(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}`, {
      method: "PUT",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf ?? "") },
      body: JSON.stringify({ revision })
    });
    return { status: response.status, body: await response.text() };
  }, { machineID, nodeID: nodeIdentity.id, revision: nodeIdentity.revision });
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
  const layers = await page.evaluate(() => ({
    drawer: Number.parseInt(getComputedStyle(document.querySelector("[data-testid=\"drawer-layer\"]") as HTMLElement).zIndex, 10),
    modal: Number.parseInt(getComputedStyle(document.querySelector("[data-testid=\"modal-layer\"]") as HTMLElement).zIndex, 10)
  }));
  expect(layers.modal).toBeGreaterThan(layers.drawer);

  await scheduleDialog.getByLabel("启用时间").fill("20:30");
  await scheduleDialog.getByLabel("停用时间").fill("01:00");
  await scheduleDialog.getByRole("button", { name: "保存计划" }).click();
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();

  const restoredScheduleButton = drawer.getByRole("button", { name: `定时设置：${nodeName}` });
  await restoredScheduleButton.click();
  await expect(scheduleDialog.getByLabel("启用时间")).toHaveValue("20:30");
  await page.keyboard.press("Escape");
  await expect(scheduleDialog).toBeHidden();
  await expect(drawer).toBeVisible();
  await expect(restoredScheduleButton).toBeFocused();

  expect(pageErrors).toEqual([]);
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
