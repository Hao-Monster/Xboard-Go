import { expect, test, type APIRequestContext, type Page } from "@playwright/test";

import { adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait  } from "./support";

const mailpitURL = process.env.XBOARD_E2E_MAILPIT_URL;

test("ticket wait policy and administrator reply email work through the local Docker stack", async ({ page, request }) => {
  test.skip(mailpitURL === undefined, "requires the local Docker Mailpit service");
  test.setTimeout(90_000);
  const unique = Date.now();
  const userEmail = `ticket-mail-${unique}@example.test`;
  const userPassword = "ticket-mail-user-password-123";
  const subject = `邮件通知 ${unique}`;
  const administratorReply = `管理员回复 ${unique}`;
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  try {
    await login(page, adminEmail, adminPassword);
    const panelOrigin = new URL(page.url()).origin;
    await configureTicketSettings(page, true, true);

    await createAdminUserFixture(page, { email: userEmail, password: userPassword });

    await logoutAndWait(page);
    await login(page, userEmail, userPassword);
    await page.getByRole("button", { name: "我的工单", exact: true }).click();
    await page.getByRole("button", { name: "新建工单" }).click();
    let dialog = page.getByRole("dialog", { name: "新建工单" });
    await dialog.getByLabel("主题").fill(subject);
    await dialog.getByLabel("消息").fill("用户初始问题");
    await dialog.getByRole("button", { name: "创建工单" }).click();
    await page.getByRole("button", { name: `查看工单：${subject}` }).click();
    dialog = page.getByRole("dialog", { name: "工单详情" });
    await dialog.getByLabel("回复内容").fill("不应被接受的连续回复");
    await dialog.getByRole("button", { name: "回复" }).click();
    await expect(dialog.getByRole("alert")).toContainText("请等待技术支持回复");
    await dialog.getByRole("button", { name: "关闭工单详情" }).click();

    await logoutAndWait(page);
    await login(page, adminEmail, adminPassword);
    await page.getByRole("button", { name: "工单管理", exact: true }).click();
    await page.getByRole("searchbox", { name: "搜索工单" }).fill(userEmail);
    await page.getByRole("button", { name: "查询工单" }).click();
    await page.getByRole("button", { name: `查看工单：${subject}` }).click();
    dialog = page.getByRole("dialog", { name: "工单详情" });
    await dialog.getByLabel("回复内容").fill(administratorReply);
    await dialog.getByRole("button", { name: "回复" }).click();
    await expect(dialog.getByText(administratorReply, { exact: true })).toBeVisible();

    const messageID = await waitForCapturedMail(request, userEmail, `您在Xboard-Go的工单得到了回复`);
    const detailResponse = await request.get(`${mailpitURL}/api/v1/message/${encodeURIComponent(messageID)}`);
    expect(detailResponse.ok()).toBeTruthy();
    const captured = JSON.stringify(await detailResponse.json());
    expect(captured).toContain(userEmail);
    expect(captured).toContain(subject);
    expect(captured).toContain(administratorReply);
    expect(captured).toContain(panelOrigin);

    await dialog.getByRole("button", { name: "关闭工单详情" }).click();
    await logoutAndWait(page);
    await login(page, userEmail, userPassword);
    await page.getByRole("button", { name: "我的工单", exact: true }).click();
    await page.getByRole("button", { name: `查看工单：${subject}` }).click();
    dialog = page.getByRole("dialog", { name: "工单详情" });
    await dialog.getByLabel("回复内容").fill("管理员回复后允许继续发送");
    await dialog.getByRole("button", { name: "回复" }).click();
    await expect(dialog.getByText("管理员回复后允许继续发送", { exact: true })).toBeVisible();

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await restoreTicketSettings(page);
  }
});

async function configureTicketSettings(page: Page, waitForAdministrator: boolean, emailEnabled: boolean) {
  await page.getByRole("button", { name: "工单管理", exact: true }).click();
  await page.getByRole("button", { name: "工单设置" }).click();
  const dialog = page.getByRole("dialog", { name: "工单设置" });
  await expect(dialog.getByLabel("用户必须等待管理员回复")).toBeVisible();
  const waitSetting = dialog.getByLabel("用户必须等待管理员回复");
  if (await waitSetting.isChecked() !== waitForAdministrator) await waitSetting.click();
  const emailSetting = dialog.getByLabel("启用工单回复邮件");
  if (await emailSetting.isChecked() !== emailEnabled) await emailSetting.click();
  await dialog.getByLabel("站点名称").fill("Xboard-Go");
  await dialog.getByLabel("站点地址").fill(new URL(page.url()).origin);
  await dialog.getByLabel("SMTP 主机").fill("mailpit");
  await dialog.getByLabel("SMTP 端口").fill("1025");
  await dialog.getByLabel("传输加密").selectOption("none");
  await dialog.getByLabel("SMTP 用户名").fill("");
  await dialog.getByLabel("发件地址").fill("support@xboard-go.local");
  await dialog.getByRole("button", { name: "保存设置" }).click();
  await expect(dialog.getByRole("status")).toContainText("工单设置已保存");
  await dialog.getByRole("button", { name: "关闭工单设置" }).click();
}

async function restoreTicketSettings(page: Page) {
  try {
    const closeDetail = page.getByRole("button", { name: "关闭工单详情", exact: true });
    const closeSettings = page.getByRole("button", { name: "关闭工单设置", exact: true });
    if (await closeDetail.count() > 0) await closeDetail.click();
    else if (await closeSettings.count() > 0) await closeSettings.click();
    const logoutButton = page.getByRole("button", { name: "退出" });
    if (await logoutButton.count() > 0) await logoutAndWait(page);
    await login(page, adminEmail, adminPassword);
    await configureTicketSettings(page, false, false);
  } catch {
    // The primary assertion retains the original failure. Persistent Docker
    // settings are also reset before each run by configureTicketSettings.
  }
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
  }, { timeout: 15_000, intervals: [250, 500, 1_000] }).toBe(1);
  return identifier;
}

async function login(page: Page, email: string, password: string) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱").fill(email);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
}
