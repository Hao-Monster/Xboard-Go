import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { adminEmail, adminPassword, createAdminUserFixture } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

interface TicketSettings {
  revision: number;
  app_name: string;
  app_url: string;
  ticket_must_wait_reply: boolean;
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_encryption: "starttls" | "tls" | "none";
  smtp_from_address: string;
}

test("administrator bulk mail, filtered CSV, and ban match the legacy user workflow", async ({ page, request }, testInfo) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(120_000);
  const unique = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const selectedEmail = `bulk-selected-${unique}@example.test`;
  const filteredPrefix = `bulk-filtered-${unique}`;
  const filteredEmails = [`${filteredPrefix}-a@example.test`, `${filteredPrefix}-b@example.test`];
  const subject = `批量通知 ${unique}`;
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });
  let originalSettings: TicketSettings | null = null;

  try {
    await loginAdministrator(page);
    originalSettings = await getTicketSettings(page);
    await saveTicketSettings(page, {
      ...originalSettings,
      app_name: "Xboard-Go",
      app_url: new URL(page.url()).origin,
      smtp_enabled: true,
      smtp_host: "mailpit",
      smtp_port: 1025,
      smtp_username: "",
      smtp_encryption: "none",
      smtp_from_address: "support@xboard-go.local"
    });
    await createAdminUserFixture(page, { email: selectedEmail, password: "bulk-selected-password-123", transferEnable: 2_147_483_648 });
    for (const email of filteredEmails) {
      await createAdminUserFixture(page, { email, password: "bulk-filtered-password-123", transferEnable: 4_294_967_296 });
    }

    await page.getByRole("button", { name: "用户管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
    await queryByEmailPrefix(page, selectedEmail);
    await page.getByRole("checkbox", { name: `选择用户：${selectedEmail}` }).check();
    await expect(page.getByText("已选择 1 项，共 1 项", { exact: true })).toBeVisible();

    await openBulkMenu(page);
    await page.getByRole("menuitem", { name: "发送邮件(1)", exact: true }).click();
    let dialog = page.getByRole("dialog", { name: "发送邮件" });
    await expect(dialog).toContainText("向所选或已筛选用户发送邮件");
    await expect(dialog.getByLabel("仅选中（1）")).toBeChecked();
    await expect(dialog.getByLabel("筛选后的用户")).toBeEnabled();
    await expect(dialog.getByLabel("全部用户")).toBeEnabled();
    await dialog.getByLabel("邮件主题").fill(subject);
    await dialog.getByLabel("邮件正文").fill("您好 {{user.email}} / {{app.name}} / {{user.plan_name|无套餐}}");
    await dialog.getByRole("button", { name: "发送", exact: true }).click();

    dialog = page.getByRole("dialog", { name: "批量任务" });
    const mailRow = dialog.getByRole("row").filter({ hasText: "发送邮件" }).first();
    await expect(mailRow).toContainText("仅选中");
    await expect(mailRow).toContainText("已完成", { timeout: 20_000 });
    await expect(mailRow).toContainText("1 / 1");
    const messageID = await waitForCapturedMail(request, selectedEmail, subject);
    const mailDetail = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(messageID)}`);
    expect(mailDetail.status()).toBe(200);
    const captured = JSON.stringify(await mailDetail.json());
    expect(captured).toContain(selectedEmail);
    expect(captured).toContain("Xboard-Go");
    expect(captured).toContain("无套餐");
    await dialog.getByRole("button", { name: "关闭", exact: true }).click();

    await queryByEmailPrefix(page, filteredPrefix);
    await expect(page.getByText("已选择 0 项，共 2 项", { exact: true })).toBeVisible();
    await openBulkMenu(page);
    await page.getByRole("menuitem", { name: "导出 CSV(筛选)", exact: true }).click();
    dialog = page.getByRole("dialog", { name: "批量任务" });
    const csvRow = dialog.getByRole("row").filter({ hasText: "导出 CSV" }).first();
    await expect(csvRow).toContainText("筛选结果");
    await expect(csvRow).toContainText("已完成", { timeout: 20_000 });
    await expect(csvRow).toContainText("2 / 2");
    const downloadEvent = page.waitForEvent("download");
    await csvRow.getByRole("button", { name: "下载", exact: true }).click();
    const download = await downloadEvent;
    expect(download.suggestedFilename()).toMatch(/^users_\d{4}-\d{2}-\d{2}_\d{6}\.csv$/);
    const downloadPath = await download.path();
    expect(downloadPath).not.toBeNull();
    if (downloadPath === null) throw new Error("bulk CSV download has no local path");
    const csvBytes = await readFile(downloadPath);
    expect(Array.from(csvBytes.subarray(0, 3))).toEqual([0xef, 0xbb, 0xbf]);
    const csv = csvBytes.toString("utf8");
    expect(csv.startsWith("\uFEFF邮箱,余额,推广佣金,总流量,剩余流量,套餐到期时间,订阅计划,订阅地址\r\n")).toBe(true);
    for (const email of filteredEmails) expect(csv).toContain(email);
    expect(csv).not.toContain(selectedEmail);
    expect(csv.split("\r\n").filter(Boolean)).toHaveLength(3);
    await dialog.getByRole("button", { name: "关闭", exact: true }).click();

    await queryByEmailPrefix(page, selectedEmail);
    await page.getByRole("checkbox", { name: `选择用户：${selectedEmail}` }).check();
    await openBulkMenu(page);
    await page.getByRole("menuitem", { name: "批量封禁(1)", exact: true }).click();
    const alertDialog = page.getByRole("alertdialog", { name: "确认批量封禁" });
    await expect(alertDialog).toBeVisible();
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await expect(alertDialog).toContainText("此操作将封禁选中的 1 名用户。");
    await expect(alertDialog).toContainText("此操作无法撤销。当前管理员和系统内部账号会被安全跳过。");
    await alertDialog.getByRole("button", { name: "确认封禁", exact: true }).click();
    await expect(page.getByRole("status")).toContainText("批量封禁完成：成功 1 项，跳过 0 项。");
    await expect(page.getByRole("row").filter({ hasText: selectedEmail })).toContainText("已封禁");

    await queryByEmailPrefix(page, "");
    await openBulkMenu(page);
    await page.getByRole("menuitem", { name: "批量封禁(全部)", exact: true }).click();
    const allAlertDialog = page.getByRole("alertdialog", { name: "确认批量封禁" });
    await expect(allAlertDialog).toContainText("此操作将封禁系统中的所有用户。");
    await allAlertDialog.getByRole("button", { name: "取消", exact: true }).click();

    const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(horizontalOverflow).toBeLessThanOrEqual(1);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    if (originalSettings !== null) {
      try {
        await saveTicketSettings(page, originalSettings);
      } catch {
        // Preserve the primary assertion. The packaged test stack is disposable.
      }
    }
  }
});

async function loginAdministrator(page: Page) {
  await page.goto("/");
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

async function queryByEmailPrefix(page: Page, value: string) {
  const search = page.getByRole("searchbox", { name: "邮箱前缀" });
  await search.fill(value);
  const [response] = await Promise.all([
    page.waitForResponse((candidate) => {
      const path = new URL(candidate.url()).pathname;
      const method = candidate.request().method();
      return (path === "/api/v1/admin/users" && method === "GET") ||
        (path === "/api/v1/admin/users/query" && method === "POST");
    }),
    page.getByRole("button", { name: "查询用户", exact: true }).click()
  ]);
  expect(response.status()).toBe(200);
}

async function openBulkMenu(page: Page) {
  await page.getByRole("button", { name: "批量操作", exact: true }).click();
  await expect(page.getByRole("menu", { name: "批量操作菜单" })).toBeVisible();
}

async function getTicketSettings(page: Page): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "GET"));
}

async function saveTicketSettings(page: Page, settings: TicketSettings): Promise<TicketSettings> {
  return decodeData<TicketSettings>(await adminRequest(page, "/api/v1/admin/ticket-settings", "PUT", {
    revision: settings.revision,
    app_name: settings.app_name,
    app_url: settings.app_url,
    ticket_must_wait_reply: settings.ticket_must_wait_reply,
    smtp_enabled: settings.smtp_enabled,
    smtp_host: settings.smtp_host,
    smtp_port: settings.smtp_port,
    smtp_username: settings.smtp_username,
    smtp_encryption: settings.smtp_encryption,
    smtp_from_address: settings.smtp_from_address
  }));
}

function decodeData<T>(response: { status: number; body: string }): T {
  expect(response.status, response.body).toBe(200);
  return (JSON.parse(response.body) as { data: T }).data;
}

async function adminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const response = await fetch(requestPath, {
      method: requestMethod,
      credentials: "same-origin",
      headers: requestBody === undefined ? { "X-CSRF-Token": decodeURIComponent(encoded) } : {
        "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(encoded)
      },
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { path, method, body });
}

async function waitForCapturedMail(request: APIRequestContext, recipient: string, subject: string): Promise<string> {
  let identifier = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages`);
    if (!response.ok()) return 0;
    const payload = await response.json() as { messages?: Array<{ ID?: string; Subject?: string; To?: Array<{ Address?: string }> }> };
    const match = payload.messages?.find((message) => message.Subject === subject && message.To?.some((address) => address.Address === recipient));
    identifier = match?.ID ?? "";
    return identifier === "" ? 0 : 1;
  }, { timeout: 20_000, intervals: [250, 500, 1_000] }).toBe(1);
  return identifier;
}
