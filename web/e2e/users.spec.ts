import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword  } from "./support";

test("administrator creates and changes a user's access state", async ({ page, context }) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await page.goto(adminEntryPath);
	await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(page.url() || "http://127.0.0.1:4173").origin });
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
	const unique = Date.now();
	const telegramID = unique;
	const planName = `E2E 用户套餐 ${unique}`;
	await page.getByRole("button", { name: "套餐管理", exact: true }).click();
	await page.getByRole("button", { name: "添加套餐", exact: true }).click();
	let dialog = page.getByRole("dialog", { name: "添加套餐" });
	await dialog.getByLabel("套餐名称").fill(planName);
	await dialog.getByLabel("流量（GiB）").fill("64");
	await dialog.getByLabel("速度限制").fill("80");
	await dialog.getByLabel("设备限制").fill("4");
	await dialog.getByRole("button", { name: "保存", exact: true }).click();
	await expect(page.getByText(planName, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "用户管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  const table = page.getByRole("table", { name: "用户列表" });
  await expect(table).toBeAttached();
  const attachedHeaders = await table.locator("th").allTextContents();
  for (const heading of ["ID", "邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间", "操作"]) {
    const column = table.getByRole("columnheader", { name: heading });
    if ((page.viewportSize()?.width ?? 1280) >= 720) await expect(column).toBeVisible();
    else expect(attachedHeaders.some((value) => value.trim().startsWith(heading)), `mobile header ${heading}`).toBe(true);
  }
  await page.getByRole("searchbox", { name: "邮箱前缀" }).fill(adminEmail);
  await page.getByRole("button", { name: "查询用户" }).click();
  const administratorRow = page.getByRole("row").filter({ hasText: adminEmail });
  await expect(administratorRow.getByText(/最后登录/)).toBeVisible();
  await expect(administratorRow.getByText("最后登录 从未", { exact: true })).toHaveCount(0);

  await page.getByRole("searchbox", { name: "邮箱前缀" }).clear();
  await page.getByRole("button", { name: "查询用户" }).click();

  const email = `e2e-user-${unique}@example.test`;
  await page.getByRole("button", { name: "新增用户" }).click();
  dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("邮箱").fill(email);
  await dialog.getByLabel(/初始密码/).fill("e2e-user-password-123");
  await dialog.getByLabel("订阅计划").selectOption({ label: planName });
  const singleResponsePromise = page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(adminAPIPath("/api/v1/admin/users/generate")));
  await dialog.getByRole("button", { name: "创建" }).click();
  const singleResponse = await singleResponsePromise;
  expect(singleResponse.status(), await singleResponse.text()).toBe(201);
  await expect(dialog.getByRole("status")).toContainText("明文密码只在本窗口保留");
  const credentialTable = dialog.getByRole("table", { name: "一次性账号凭据" });
  await expect(credentialTable).toContainText(email);
  await expect(credentialTable).toContainText("e2e-user-password-123");
  const downloadPromise = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "下载安全 CSV" }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe("users.csv");
  const downloadPath = await download.path();
  expect(downloadPath).not.toBeNull();
  if (downloadPath === null) throw new Error("generated credential download has no local path");
  const csv = await readFile(downloadPath, "utf8");
  expect(csv.startsWith("\uFEFF\"账号\",\"密码\"")).toBe(true);
  expect(csv).toContain(email);
  await dialog.getByRole("button", { name: "完成" }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: email }).getByText("最后登录 从未", { exact: true })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: email })).toContainText("0 / 4");

  const batchPrefix = `e2e-batch-${unique}`;
  await page.getByRole("button", { name: "新增用户" }).click();
  dialog = page.getByRole("dialog", { name: "新增用户" });
  await dialog.getByLabel("生成方式").selectOption("prefixed_batch");
  await expect(dialog.getByLabel(/初始密码/)).toHaveCount(0);
  await dialog.getByLabel("账号前缀").fill(batchPrefix);
  await dialog.getByLabel("邮箱域").fill("example.test");
  await dialog.getByLabel(/生成数量/).fill("2");
  await dialog.getByLabel("订阅计划").selectOption({ label: planName });
  const batchRequestPromise = page.waitForRequest((request) => request.method() === "POST" && request.url().endsWith(adminAPIPath("/api/v1/admin/users/generate")));
  await dialog.getByRole("button", { name: "生成账号" }).click();
  const batchRequest = await batchRequestPromise;
  expect(batchRequest.postDataJSON()).toMatchObject({
    mode: "prefixed_batch", email_prefix: batchPrefix, email_domain: "example.test", count: 2
  });
  const batchCredentialRows = dialog.getByRole("table", { name: "一次性账号凭据" }).locator("tbody tr");
  await expect(batchCredentialRows).toHaveCount(2);
  await expect(dialog).toContainText(`${batchPrefix}_1@example.test`);
  await expect(dialog).toContainText(`${batchPrefix}_2@example.test`);
  const batchPasswords = await batchCredentialRows.locator("td:nth-child(2) code").allTextContents();
  expect(new Set(batchPasswords).size).toBe(2);
  await dialog.getByRole("button", { name: "完成" }).click();

  await page.getByRole("button", { name: `查看详情：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "用户详情" });
  for (const label of ["角色", "套餐", "权限组", "邀请人", "Telegram", "备注", "已用流量", "余额", "佣金", "提醒", "注册 / 更新"]) {
    await expect(dialog.getByText(label, { exact: true })).toBeVisible();
  }
  await expect(dialog.getByText(/Token|UUID|订阅地址/)).toHaveCount(0);
  await dialog.getByRole("button", { name: "关闭" }).last().click();

  await page.getByRole("button", { name: `编辑用户：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "编辑用户" });
	await dialog.getByLabel("套餐").selectOption({ label: planName });
	await expect(dialog.getByLabel("流量额度（GiB）")).toHaveValue("64");
	await expect(dialog.getByLabel("限速（Mbps，0 为不限速）")).toHaveValue("80");
	await expect(dialog.getByLabel("设备数（0 为不限设备）")).toHaveValue("4");
	await dialog.getByLabel("邀请人邮箱（留空表示无）").fill(adminEmail);
	await dialog.getByLabel("新密码（留空不修改）").fill("e2e-profile-password-456");
	await dialog.getByLabel("已用上行流量（GiB）").fill("1.5");
	await dialog.getByLabel("已用下行流量（GiB）").fill("2");
	await dialog.getByLabel("余额（元）", { exact: true }).fill("45.67");
	await dialog.getByLabel("佣金余额（元）", { exact: true }).fill("8.09");
	await dialog.getByLabel("佣金类型").selectOption("1");
	await dialog.getByLabel("专享折扣（留空使用系统默认）").fill("75");
	await dialog.getByLabel("Telegram ID（留空表示未绑定）").fill(String(telegramID));
	await dialog.getByLabel("到期提醒").check();
	await dialog.getByLabel("流量提醒").check();
	await dialog.getByLabel("备注").fill("E2E complete profile");
	await dialog.getByLabel("管理员", { exact: true }).check();
	await dialog.getByLabel("员工", { exact: true }).check();
	await dialog.getByLabel("分销商", { exact: true }).check();
	await dialog.getByLabel("分销商名称").fill("E2E 混合角色");
	const updateResponsePromise = page.waitForResponse((response) => response.request().method() === "PATCH" && response.url().includes(adminAPIPath("/api/v1/admin/users/")));
	await dialog.getByRole("button", { name: "保存" }).click();
	const updateResponse = await updateResponsePromise;
	expect(updateResponse.status(), await updateResponse.text()).toBe(200);
	const updatePayload = updateResponse.request().postDataJSON();
	expect(updatePayload).toMatchObject({
		plan_id: expect.any(Number), transfer_enable: 68_719_476_736, speed_limit: 80, device_limit: 4,
		invite_user_email: adminEmail, traffic_upload: 1_610_612_736, traffic_download: 2_147_483_648,
		balance: 4567, commission_type: 1, commission_balance: 809, discount: 75, telegram_id: telegramID,
		remind_expire: true, remind_traffic: true, remarks: "E2E complete profile",
		is_admin: true, is_staff: true, is_distributor: true, distributor_name: "E2E 混合角色"
	});
	const updatedRow = page.getByRole("row").filter({ hasText: email });
	await expect(updatedRow).toContainText(planName);
	await expect(updatedRow).toContainText("¥45.67");
	await expect(updatedRow).toContainText("管理员 · 员工 · 分销商");
	await expect(updatedRow).toContainText("E2E 混合角色");

	await page.getByRole("button", { name: `查看详情：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户详情" });
	await expect(dialog).toContainText(adminEmail);
	await expect(dialog).toContainText("E2E complete profile");
	await expect(dialog).toContainText(String(telegramID));
	await expect(dialog).toContainText("¥45.67");
	await expect(dialog).toContainText("¥8.09");
	await expect(dialog).toContainText("到期提醒开启 · 流量提醒开启");
	const subscriptionRequestPromise = page.waitForRequest((request) => request.url().includes("/subscription-url"));
	await dialog.getByRole("button", { name: "复制订阅 URL" }).click();
	const subscriptionRequest = await subscriptionRequestPromise;
	expect(subscriptionRequest.method()).toBe("GET");
	await expect(dialog.getByRole("status")).toContainText("订阅地址已复制");
	const copiedSubscriptionURL = await page.evaluate(() => navigator.clipboard.readText());
  expect(copiedSubscriptionURL).toMatch(/\/s\/[0-9a-f]{32}$/);
	const activeSubscriptionBeforeReset = await page.request.get(copiedSubscriptionURL);
	expect(activeSubscriptionBeforeReset.status()).toBe(200);
	await dialog.getByRole("button", { name: "关闭" }).last().click();

	await page.getByRole("button", { name: `用户操作：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户操作" });
	await expect(page.getByRole("dialog")).toHaveCount(1);
	await dialog.getByRole("button", { name: "重置 UUID 与订阅地址" }).click();
	dialog = page.getByRole("dialog", { name: "重置订阅凭据" });
	await expect(page.getByRole("dialog")).toHaveCount(1);
	await expect(dialog).toContainText("旧订阅地址会立即失效");
	const securityResetRequestPromise = page.waitForRequest((request) => request.method() === "POST" && new URL(request.url()).pathname.endsWith("/subscription-security/reset") && new URL(request.url()).pathname.includes(adminAPIPath("/api/v1/admin/users/")));
	await dialog.getByRole("button", { name: "确认重置订阅凭据" }).click();
	const securityResetRequest = await securityResetRequestPromise;
	expect(securityResetRequest.postDataJSON()).toEqual({ revision: expect.any(Number) });
	await expect(dialog.getByRole("status")).toContainText("订阅凭据已重置");
	const expiredSubscription = await page.request.get(copiedSubscriptionURL);
	expect(expiredSubscription.status()).toBe(403);
	await dialog.getByRole("button", { name: "关闭", exact: true }).click();

	await page.getByRole("button", { name: `查看详情：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户详情" });
	await dialog.getByRole("button", { name: "复制订阅 URL" }).click();
	await expect(dialog.getByRole("status")).toContainText("订阅地址已复制");
	const rotatedSubscriptionURL = await page.evaluate(() => navigator.clipboard.readText());
	expect(rotatedSubscriptionURL).toMatch(/\/s\/[0-9a-f]{32}$/);
	expect(rotatedSubscriptionURL).not.toBe(copiedSubscriptionURL);
	const activeSubscription = await page.request.get(rotatedSubscriptionURL);
	expect(activeSubscription.status()).toBe(200);
	await dialog.getByRole("button", { name: "关闭" }).last().click();

	await page.getByRole("button", { name: `用户操作：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户操作" });
	await dialog.getByRole("button", { name: "分配订单" }).click();
	dialog = page.getByRole("dialog", { name: "分配订单" });
	await expect(page.getByRole("dialog")).toHaveCount(1);
	await expect(dialog.getByLabel("用户邮箱")).toHaveValue(email);
	await expect(dialog.getByLabel("用户邮箱")).toHaveAttribute("readonly", "");
	await dialog.getByLabel("订阅套餐").selectOption({ label: planName });
	await dialog.getByLabel("支付金额（CNY）").fill("2.50");
	const assignedOrderResponsePromise = page.waitForResponse((response) => response.request().method() === "POST" && new URL(response.url()).pathname.endsWith("/orders") && new URL(response.url()).pathname.includes(adminAPIPath("/api/v1/admin/users/")));
	await dialog.getByRole("button", { name: "创建订单" }).click();
	const assignedOrderResponse = await assignedOrderResponsePromise;
	expect(assignedOrderResponse.status(), await assignedOrderResponse.text()).toBe(201);
	await expect(dialog).toBeHidden();

	await page.getByRole("button", { name: `用户操作：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户操作" });
	await dialog.getByRole("button", { name: "TA 的订单" }).click();
	dialog = page.getByRole("dialog", { name: "用户关联记录" });
	await expect(page.getByRole("dialog")).toHaveCount(1);
	await expect(dialog).toContainText(planName);
	await dialog.getByRole("button", { name: "关闭关联记录面板" }).click();

	await page.getByRole("button", { name: `用户操作：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "用户操作" });
	await dialog.getByRole("button", { name: "重置流量" }).click();
	dialog = page.getByRole("dialog", { name: "重置流量" });
	await expect(page.getByRole("dialog")).toHaveCount(1);
	await dialog.getByLabel("重置原因（可选）").fill("E2E U4 manual reset");
	const resetRequestPromise = page.waitForRequest((request) => request.method() === "POST" && /\/api\/v1\/admin\/users\/\d+\/traffic-reset$/.test(new URL(request.url()).pathname));
	await dialog.getByRole("button", { name: "确认重置流量" }).click();
	const resetRequest = await resetRequestPromise;
	expect(resetRequest.headers()["idempotency-key"]).toBeTruthy();
	expect(resetRequest.postDataJSON()).toEqual({ reason: "E2E U4 manual reset" });
	await expect(dialog.getByRole("status")).toContainText("流量已重置");
	await dialog.getByRole("tab", { name: "重置历史" }).click();
	await expect(dialog).toContainText("E2E U4 manual reset");
	await expect(dialog).toContainText(adminEmail);
	await dialog.getByRole("button", { name: "关闭" }).last().click();

	await page.getByRole("button", { name: `编辑用户：${email}` }).click();
	dialog = page.getByRole("dialog", { name: "编辑用户" });
  await dialog.getByLabel("封禁用户").check();
  await dialog.getByRole("button", { name: "保存" }).click();
  await expect(page.getByRole("row").filter({ hasText: email }).getByText("已封禁", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: `重置密码：${email}` }).click();
  dialog = page.getByRole("dialog", { name: "重置用户密码" });
  await dialog.getByLabel("新密码").fill("e2e-rotated-password-456");
  await dialog.getByRole("button", { name: "确认重置" }).click();
  await expect(dialog).toBeHidden();

  await page.getByRole("searchbox", { name: "邮箱前缀" }).fill(`e2e-user-${unique}`);
  await page.getByLabel("用户状态").selectOption("banned");
  await page.getByRole("button", { name: "查询用户" }).click();
  await expect(page.getByText(email, { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "高级筛选" }).click();
  await page.getByRole("button", { name: "添加筛选条件" }).click();
  await page.getByLabel("筛选字段 1").selectOption("subscription_token");
  await expect(page.getByLabel("筛选操作符 1").getByRole("option")).toHaveCount(2);
  const secretProbe = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"; // gitleaks:allow -- deterministic transport-safety fixture
  await page.getByLabel("筛选值 1").fill(secretProbe);
	const secretRequestPromise = page.waitForRequest((request) => request.url().endsWith(adminAPIPath("/api/v1/admin/users/query")));
  await page.getByRole("button", { name: "查询用户" }).click();
  const secretRequest = await secretRequestPromise;
  expect(secretRequest.method()).toBe("POST");
  expect(secretRequest.url()).not.toContain(secretProbe);
  expect(secretRequest.postDataJSON()).toMatchObject({
    filters: [{ field: "subscription_token", operator: "eq", value: secretProbe }]
  });
  await expect(page.getByText("没有符合条件的用户。", { exact: true })).toBeVisible();

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);

  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});
