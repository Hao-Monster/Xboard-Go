import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword } from "./support";

test("administrator inspects worker health, failed mail, and body-free mutation audit", async ({ page }) => {
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

  const uniqueTitle = `Private audit body ${Date.now()}`;
  const created = await adminRequest(page, "/api/v1/admin/notices", "POST", {
    title: uniqueTitle, content: "must never enter the audit log", image_url: "", tags: [], show: false
  });
  expect(created.status, created.body).toBe(201);
  const noticeID = readNoticeIdentity(created.body);

  try {
    await page.getByRole("button", { name: "系统状态", exact: true }).click();
    await expect(page.getByRole("heading", { name: "系统状态" })).toBeVisible();
    await expect(page.getByText("Schema v19", { exact: true })).toBeVisible();
    await expect(page.getByText("正常", { exact: true }).first()).toBeVisible();
    await expect(page.getByRole("heading", { name: "失败邮件任务" })).toBeVisible();

    await page.getByLabel("审计操作").selectOption("POST");
    await page.getByRole("searchbox", { name: "搜索审计日志" }).fill("notices");
    await page.getByRole("button", { name: "查询审计日志" }).click();
    await expect(page.getByText("/api/v1/admin/notices", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("201", { exact: true }).first()).toBeVisible();
    await expect(page.getByText(uniqueTitle, { exact: true })).toHaveCount(0);
    await expect(page.getByText("must never enter the audit log", { exact: true })).toHaveCount(0);
  } finally {
    if (noticeID !== null) {
      const removed = await adminRequest(page, `/api/v1/admin/notices/${noticeID}?revision=1`, "DELETE");
      expect(removed.status, removed.body).toBe(204);
    }
  }
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
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
  }, { path, method, body });
}

function readNoticeIdentity(body: string): number | null {
  const payload: unknown = JSON.parse(body);
  if (typeof payload !== "object" || payload === null) return null;
  const data: unknown = Reflect.get(payload, "data");
  if (typeof data !== "object" || data === null) return null;
  const id: unknown = Reflect.get(data, "id");
  return typeof id === "number" && Number.isSafeInteger(id) ? id : null;
}
