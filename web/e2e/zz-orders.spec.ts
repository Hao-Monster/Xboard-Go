import { expect, test, type Locator, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword, createAdminUserFixture, expectLoginPage, logoutAndWait  } from "./support";

test("free checkout and administrator order lifecycle work on every supported viewport", async ({ page }) => {
  test.setTimeout(90_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  const unique = `${Date.now()}-${test.info().project.name}`;
  const userEmail = `order-user-${unique}@example.test`;
  const userPassword = "order-user-password-123";
  let commissionUserEmail = `commission-user-${unique}@example.test`;
  let expectedInviterEmail = adminEmail;
  const planName = `E2E 免费订单套餐 ${unique}`;

  await login(page, adminEmail, adminPassword);
  await createUser(page, userEmail, userPassword);
  const reusableInvitee = await findReusableInvitedUser(page);
  if (reusableInvitee === null) {
	await createInvitedUser(page, commissionUserEmail, userPassword);
  } else {
	commissionUserEmail = reusableInvitee.email;
	expectedInviterEmail = reusableInvitee.inviterEmail;
  }
  await createFreePlan(page, planName);

  await logoutAndWait(page);
  await login(page, userEmail, userPassword);
  await page.getByRole("button", { name: "订阅套餐", exact: true }).click();
  const planCard = page.getByRole("article").filter({ has: page.getByRole("heading", { name: planName, exact: true }) });
  await expect(planCard).toContainText("月付 ¥0.00");
  await planCard.getByRole("button", { name: "立即订阅", exact: true }).click();

  let dialog = page.getByRole("dialog", { name: "配置订阅" });
  await expect(dialog.getByLabel("付款周期")).toHaveValue("monthly");
  await expect(dialog).toContainText("套餐标价¥0.00");
  await dialog.getByRole("button", { name: "下单", exact: true }).click();

  await expect(page.getByRole("heading", { name: "我的订单", exact: true })).toBeVisible();
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await expect(dialog).toContainText(planName);
  await expect(dialog).toContainText("待支付");
  await expect(dialog).toContainText("该订单无需在线支付，可直接开通。");
  const userTradeNo = (await dialog.locator("strong.monospace").textContent())?.trim() ?? "";
  expect(userTradeNo).toMatch(/^\d{25}$/);
  await dialog.getByRole("button", { name: "立即开通", exact: true }).click();
  await expect(dialog.getByText("已完成", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "立即开通", exact: true })).toBeHidden();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
  await expect(orderRow(page, userTradeNo)).toContainText("已完成");

  await logoutAndWait(page);
  await login(page, adminEmail, adminPassword);
  await page.getByRole("button", { name: "订单管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "订单管理", exact: true })).toBeVisible();
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(userTradeNo);
	await setMultiFilter(page, "订单状态", ["已完成", "已折抵"]);
	await setMultiFilter(page, "订单类型", ["新购", "续费"]);
	await setMultiFilter(page, "佣金状态", ["待确认"]);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  await expect(orderRow(page, userTradeNo)).toContainText(userEmail);
  await expect(orderRow(page, userTradeNo)).toContainText("已完成");
	if ((page.viewportSize()?.width ?? 0) <= 720) await page.getByLabel("订单排序字段", { exact: true }).selectOption("total_amount");
	else await page.getByRole("button", { name: "按订单金额排序", exact: true }).click();
	await expect(orderRow(page, userTradeNo)).toBeVisible();

  const assignedTradeNo = await assignOrder(page, commissionUserEmail, planName, "12.34");
  expect(assignedTradeNo).toMatch(/^[0-9a-f]{32}$/);
  dialog = page.getByRole("dialog", { name: "订单详情" });
	await expect(dialog).toContainText("支付金额¥12.34");
  await dialog.getByRole("button", { name: "标记已支付并开通", exact: true }).click();
  await expect(dialog.getByText("已完成", { exact: true })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "标记已支付并开通", exact: true })).toBeHidden();
	await expect(dialog.getByText(expectedInviterEmail, { exact: true })).toBeVisible();
	await expect(dialog.getByText("manual_operation", { exact: true })).toBeVisible();
	await expect(dialog.getByRole("link", { name: "打开订阅链接", exact: true })).toHaveAttribute("href", /\/s\//);
	await expect(dialog.getByText("待确认", { exact: true })).toBeVisible();
	await dialog.getByRole("button", { name: "设为发放中", exact: true }).click();
	await expect(dialog.getByText("发放中", { exact: true })).toBeVisible();
	await dialog.getByRole("button", { name: "设为无效", exact: true }).click();
	await expect(dialog.getByText("无效", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();

  const cancelledTradeNo = await assignOrder(page, userEmail, planName, "5.00");
  dialog = page.getByRole("dialog", { name: "订单详情" });
  await dialog.getByRole("button", { name: "取消订单", exact: true }).click();
  await expect(dialog.getByText("已取消", { exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "关闭", exact: true }).click();
	await setMultiFilter(page, "订单状态", ["已取消"]);
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(cancelledTradeNo);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  await expect(orderRow(page, cancelledTradeNo)).toContainText("已取消");

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);
  expect(pageErrors).toEqual([]);
  expect(serverErrors).toEqual([]);
});

async function login(page: Page, email: string, password: string) {
  await page.goto(adminEntryPath);
  await expectLoginPage(page);
  await page.getByLabel("邮箱", { exact: true }).fill(email);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

async function createUser(page: Page, email: string, password: string) {
  await createAdminUserFixture(page, { email, password });
}

async function findReusableInvitedUser(page: Page): Promise<{ email: string; inviterEmail: string } | null> {
  const response = await page.evaluate(async (requestPath) => {
    const result = await fetch(requestPath, { credentials: "same-origin" });
    return { status: result.status, body: await result.text() };
  }, adminAPIPath("/api/v1/admin/users?email_prefix=il-&limit=100"));
  expect(response.status, response.body).toBe(200);
  const payload = JSON.parse(response.body) as { data?: { items?: Array<{ email?: string }> } };
  const candidates = (payload.data?.items ?? [])
    .flatMap((item) => item.email?.startsWith("il-") ? [item.email] : [])
    .sort((left, right) => right.localeCompare(left));
  for (const email of candidates) {
    const prior = await page.evaluate(async ({ candidate, requestPrefix }) => {
      const query = new URLSearchParams({ page: "1", page_size: "100", query: candidate });
      for (const status of ["1", "3", "4"]) query.append("status", status);
      const result = await fetch(`${requestPrefix}?${query.toString()}`, { credentials: "same-origin" });
      return { status: result.status, body: await result.text() };
    }, { candidate: email, requestPrefix: adminAPIPath("/api/v1/admin/orders") });
    expect(prior.status, prior.body).toBe(200);
    const orders = JSON.parse(prior.body) as { data?: { items?: Array<{ user_email?: string }> } };
    if (!(orders.data?.items ?? []).some((order) => order.user_email === email)) {
      return { email, inviterEmail: `io-${email.slice(3)}` };
    }
  }
  return null;
}

async function createInvitedUser(page: Page, email: string, password: string) {
	const invitation = await page.evaluate(async () => {
		const csrf = document.cookie
			.split("; ")
			.find((value) => value.startsWith("xboard_csrf="))
			?.slice("xboard_csrf=".length);
		const response = await fetch("/api/v1/invitations", {
			method: "POST",
			headers: { "Content-Type": "application/json", "X-CSRF-Token": decodeURIComponent(csrf ?? "") },
			credentials: "same-origin",
			body: "{}"
		});
		return { status: response.status, body: await response.text() };
	});
	expect(invitation.status, invitation.body).toBe(200);
	const code = (JSON.parse(invitation.body) as { data?: { code?: string } }).data?.code ?? "";
	expect(code).toMatch(/^[A-Za-z0-9]{8}$/);
	const registration = await page.evaluate(async ({ userEmail, userPassword, inviteCode }) => {
		const response = await fetch("/api/v1/auth/register", {
			method: "POST", credentials: "omit", headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ email: userEmail, password: userPassword, password_confirmation: userPassword, invite_code: inviteCode })
		});
		return { status: response.status, body: await response.text() };
	}, { userEmail: email, userPassword: password, inviteCode: code });
	expect(registration.status, registration.body).toBe(200);
}

async function createFreePlan(page: Page, name: string) {
  await page.getByRole("button", { name: "套餐管理", exact: true }).click();
  await page.getByRole("button", { name: "添加套餐", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加套餐" });
  await dialog.getByLabel("套餐名称", { exact: true }).fill(name);
  await dialog.getByLabel("流量（GiB）", { exact: true }).fill("100");
  await dialog.getByLabel("月付", { exact: true }).fill("0.00");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  const row = planRow(page, name);
  await expect(row).toContainText("月付 ¥0.00");
  await row.getByLabel("展示", { exact: true }).check();
  await row.getByLabel("销售", { exact: true }).check();
}

async function assignOrder(page: Page, email: string, planName: string, amount: string): Promise<string> {
  await page.getByRole("button", { name: "添加订单", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "添加订单" });
  await dialog.getByLabel("用户邮箱", { exact: true }).fill(email);
  await dialog.getByRole("combobox", { name: "订阅套餐", exact: true }).selectOption({ label: planName });
  await dialog.getByRole("combobox", { name: "付款周期", exact: true }).selectOption("monthly");
  await dialog.getByLabel("支付金额（CNY）", { exact: true }).fill(amount);
  await dialog.getByRole("button", { name: "创建订单", exact: true }).click();
	await setMultiFilter(page, "订单状态", ["待支付"]);
	await setMultiFilter(page, "订单类型", []);
	await setMultiFilter(page, "佣金状态", []);
  await page.getByRole("searchbox", { name: "搜索订单", exact: true }).fill(email);
  await page.getByRole("button", { name: "查询订单", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: email }).filter({ hasText: amount }).filter({ hasText: "待支付" }).first();
  await expect(row).toBeVisible();
  const tradeNo = (await row.locator("strong.monospace").textContent())?.trim() ?? "";
  await row.getByRole("button", { name: `查看订单：${tradeNo}`, exact: true }).click();
  return tradeNo;
}

function planRow(page: Page, name: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(name, { exact: true }) });
}

function orderRow(page: Page, tradeNo: string): Locator {
  return page.getByRole("row").filter({ has: page.getByText(tradeNo, { exact: true }) });
}

async function setMultiFilter(page: Page, label: string, selected: string[]) {
	const details = page.locator("details.order-multi-filter").filter({ has: page.locator("summary", { hasText: `${label}：` }) });
	if (await details.getAttribute("open") === null) await details.locator("summary").click();
	const group = details.getByRole("group", { name: label, exact: true });
	const clear = group.getByRole("button", { name: "清除", exact: true });
	if (await clear.count() > 0) await clear.click();
	for (const option of selected) await group.getByRole("checkbox", { name: `${label}：${option}`, exact: true }).check();
	await details.locator("summary").click();
}
