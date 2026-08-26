import { expect, test, type Page } from "@playwright/test";

import { adminEmail, adminPassword, expectLoginPage } from "./support";

test("administrator uploads, publishes, reopens, and removes a multi-chunk knowledge attachment", async ({ page }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  const consoleErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
  page.on("response", (response) => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`); });

  const unique = Date.now();
  const title = `Attachment guide ${unique}`;
  const fileName = `multi-chunk-${unique}.bin`;
  const content = Buffer.alloc(5 * 1024 * 1024 + 37, 0x61);

  await login(page);
  consoleErrors.length = 0;
  await page.getByRole("button", { name: "知识库管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "知识库管理" })).toBeVisible();
  await page.getByRole("button", { name: "添加知识" }).click();
  let dialog = page.getByRole("dialog", { name: "添加知识" });
  await dialog.getByLabel("标题").fill(title);
  await dialog.getByLabel("分类").fill("附件测试");
  await dialog.getByLabel("显示").check();
  await dialog.getByLabel("内容").fill("# Multi-chunk attachment");

  const completedResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && /\/knowledge-attachments\/uploads\/[0-9a-f-]+\/complete$/.test(new URL(response.url()).pathname));
  await dialog.getByLabel("选择知识附件").setInputFiles({ name: fileName, mimeType: "application/octet-stream", buffer: content });
  const completed = await completedResponse;
  expect(completed.status()).toBe(200);
  const completedPayload = await completed.json() as { data: { uuid: string; placeholder: string; url: string } };
  const attachment = completedPayload.data;
  expect(attachment.placeholder).toBe(`knowledge-attachment://${attachment.uuid}`);
  await expect(dialog.getByRole("listitem").filter({ hasText: fileName }).getByText(fileName, { exact: true })).toBeVisible();
  await expect(dialog.getByText(/5\.00 MB · 已就绪/)).toBeVisible();
  await expect(dialog.getByLabel(`${fileName} 上传进度`)).toHaveAttribute("value", "100");
  await expect(dialog.getByLabel("内容")).toHaveValue(new RegExp(escapeRegExp(attachment.placeholder)));

  const qrCompletedResponse = page.waitForResponse((response) =>
    response.request().method() === "POST" && /\/knowledge-attachments\/uploads\/[0-9a-f-]+\/complete$/.test(new URL(response.url()).pathname));
  await dialog.getByRole("button", { name: "插入二维码" }).click();
  await dialog.getByLabel("二维码链接").fill(`https://example.test/guide?id=${unique}`);
  await dialog.getByRole("button", { name: "生成并上传二维码" }).click();
  const qrCompleted = await qrCompletedResponse;
  expect(qrCompleted.status()).toBe(200);
  const qrPayload = await qrCompleted.json() as { data: { uuid: string; original_name: string; placeholder: string } };
  const qrAttachment = qrPayload.data;
  expect(qrAttachment.original_name).toMatch(/^链接二维码-\d{14}\.png$/);
  await expect(dialog.getByRole("listitem").filter({ hasText: qrAttachment.original_name }).getByText(qrAttachment.original_name, { exact: true })).toBeVisible();
  await expect(dialog.getByLabel("内容")).toHaveValue(new RegExp(escapeRegExp(qrAttachment.placeholder)));
  await expect(dialog.getByRole("region", { name: "知识正文预览" }).locator("img")).toBeVisible();
  await dialog.getByRole("button", { name: "提交" }).click();
  await expect(page.getByText(title, { exact: true })).toBeVisible();

  const summaries = await apiData<Array<{ id: number; title: string; share_url: string }>>(page, "/api/v1/admin/knowledge");
  const summary = summaries.find((candidate) => candidate.title === title);
  expect(summary).toBeDefined();
  if (summary === undefined) throw new Error("created knowledge article is missing");
  const detail = await apiData<{ body: string }>(page, `/api/v1/admin/knowledge/${summary.id}`);
  expect(detail.body).toContain(attachment.placeholder);
  expect(detail.body).toContain(qrAttachment.placeholder);
  expect(detail.body).not.toContain(`/knowledge-attachments/${attachment.uuid}`);

  const rangeResponse = await page.request.get(attachment.url, { headers: { Range: "bytes=5242880-5242916" } });
  expect(rangeResponse.status()).toBe(206);
  expect((await rangeResponse.body()).equals(Buffer.alloc(37, 0x61))).toBe(true);
  expect(rangeResponse.headers()["content-range"]).toBe(`bytes 5242880-5242916/${content.length}`);

  const publicPage = await page.request.get(summary.share_url);
  expect(publicPage.status()).toBe(200);
  const publicHTML = await publicPage.text();
  expect(publicHTML).toContain(`/guide-attachments/${attachment.uuid}`);
  expect(publicHTML).toContain(`/guide-attachments/${qrAttachment.uuid}`);
  expect(publicHTML).not.toContain("knowledge-attachment://");
  const publicRange = await page.request.get(`/guide-attachments/${attachment.uuid}`, { headers: { Range: "bytes=0-3" } });
  expect(publicRange.status()).toBe(206);
  expect(await publicRange.text()).toBe("aaaa");

  await page.getByRole("button", { name: `编辑知识：${title}` }).click();
  dialog = page.getByRole("dialog", { name: "编辑知识" });
  await expect(dialog.getByRole("listitem").filter({ hasText: fileName }).getByText(fileName, { exact: true })).toBeVisible();
  await expect(dialog.getByRole("listitem").filter({ hasText: qrAttachment.original_name }).getByText(qrAttachment.original_name, { exact: true })).toBeVisible();
  await expect(dialog.getByText(/5\.00 MB · 已就绪/)).toBeVisible();
  await expect(dialog.getByLabel("内容")).toHaveValue(new RegExp(escapeRegExp(attachment.placeholder)));
  await dialog.getByRole("listitem").filter({ hasText: fileName }).getByRole("button", { name: "移除" }).click();
  await expect(dialog.getByRole("listitem").filter({ hasText: fileName })).toHaveCount(0);
  await expect(dialog.getByLabel("内容")).not.toHaveValue(new RegExp(escapeRegExp(attachment.placeholder)));
  await dialog.getByRole("button", { name: "提交" }).click();
  await expect(page.getByText(title, { exact: true })).toBeVisible();

  const removedDetail = await apiData<{ body: string }>(page, `/api/v1/admin/knowledge/${summary.id}`);
  expect(removedDetail.body).not.toContain(attachment.placeholder);
  expect(removedDetail.body).toContain(qrAttachment.placeholder);
  expect((await page.request.get(`/guide-attachments/${attachment.uuid}`)).status()).toBe(404);
  expect((await page.request.get(`/guide-attachments/${qrAttachment.uuid}`)).status()).toBe(200);

  await page.getByRole("button", { name: `删除知识：${title}` }).click();
  dialog = page.getByRole("dialog", { name: "删除知识" });
  await dialog.getByRole("button", { name: "确认删除" }).click();
  await expect(page.getByText(title, { exact: true })).toHaveCount(0);
  expect((await page.request.get(`/guide-attachments/${qrAttachment.uuid}`)).status()).toBe(404);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
  expect(consoleErrors).toEqual([]);
});

async function login(page: Page) {
  await page.goto("/");
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
}

async function apiData<T>(page: Page, path: string): Promise<T> {
  const response = await page.request.get(path);
  expect(response.status()).toBe(200);
  const payload = await response.json() as { data: T };
  return payload.data;
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
