import { expect, request as playwrightRequest, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";

const legacyURL = requiredEnv("LEGACY_ADMIN_URL");
const legacyEmail = requiredEnv("LEGACY_ADMIN_EMAIL");
const legacyPassword = requiredEnv("LEGACY_ADMIN_PASSWORD");
const goURL = requiredEnv("XBOARD_GO_URL");
const goEmail = requiredEnv("XBOARD_GO_ADMIN_EMAIL");
const goPassword = requiredEnv("XBOARD_GO_ADMIN_PASSWORD");
const legacyDockerContainer = requiredEnv("LEGACY_DOCKER_CONTAINER");

const legacyMenu = [
  ["仪表盘", "#/"],
  ["系统配置", "#/config/system"],
  ["插件管理", "#/config/plugin"],
  ["主题配置", "#/config/theme"],
  ["公告管理", "#/config/notice"],
  ["支付配置", "#/config/payment"],
  ["知识库管理", "#/config/knowledge"],
  ["服务器管理", "#/server/machine"],
  ["节点管理", "#/server/manage"],
  ["权限组管理", "#/server/group"],
  ["路由管理", "#/server/route"],
  ["套餐管理", "#/finance/plan"],
  ["订单管理", "#/finance/order"],
  ["优惠券管理", "#/finance/coupon"],
  ["礼品卡管理", "#/finance/gift-card"],
  ["用户管理", "#/user/manage"],
  ["工单管理", "#/user/ticket"]
] as const;

test("legacy administrator surface remains observable without frontend source", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacy(page);

  for (const [label, href] of legacyMenu) {
    await expect(page.locator(`a[href="${href}"]`), `${label} (${href})`).toBeVisible();
  }

  const machineResponse = page.waitForResponse((response) => response.url().includes("/server/machine/fetch"));
  await page.locator('a[href="#/server/machine"]').click();
  expect((await machineResponse).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "添加服务器" })).toBeVisible();
  for (const column of ["服务器名称", "状态", "负载", "节点数", "最后心跳", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }

  const userResponse = page.waitForResponse((response) => response.url().includes("/user/fetch"));
  await page.locator('a[href="#/user/manage"]').click();
  expect((await userResponse).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "用户管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "创建用户" })).toBeVisible();
  for (const column of ["邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }

  const ticketResponse = page.waitForResponse((response) => response.url().includes("/ticket/fetch"));
  await page.locator('a[href="#/user/ticket"]').click();
  expect((await ticketResponse).status()).toBe(200);
  await expect(page.getByRole("heading", { name: "工单管理" })).toBeVisible();
  await expect(page.getByText("处理中", { exact: true }).first()).toBeVisible();
  await expect(page.getByText("已关闭", { exact: true }).first()).toBeVisible();
  for (const column of ["工单号", "主题", "优先级", "状态", "最后更新", "创建时间", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }

  const noticeResponse = page.waitForResponse((response) => response.url().includes("/notice/fetch"));
  await page.locator('a[href="#/config/notice"]').click();
  const fetchedNotices = await noticeResponse;
  expect(fetchedNotices.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "公告管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: /添加公告/ })).toBeVisible();
  for (const column of ["ID", "显示状态", "标题", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }
  const authorization = fetchedNotices.request().headers().authorization;
  expect(authorization).toBeTruthy();
  const userNotices = await page.request.get(new URL("/api/v1/user/notice/fetch?current=1", legacyURL).toString(), {
    headers: { authorization }
  });
  expect(userNotices.status()).toBe(200);
  const userNoticePayload = await userNotices.json() as { data?: unknown; total?: unknown };
  expect(Array.isArray(userNoticePayload.data)).toBe(true);
  expect(typeof userNoticePayload.total).toBe("number");
  if (!Array.isArray(userNoticePayload.data)) throw new Error("legacy user notice data must be an array");
  expect(userNoticePayload.data.length).toBeLessThanOrEqual(5);
  expect(userNoticePayload.data.every((item) => isVisibleLegacyNotice(item))).toBe(true);

  const knowledgeResponse = page.waitForResponse((response) => response.url().includes("/knowledge/fetch"));
  await page.locator('a[href="#/config/knowledge"]').click();
  const fetchedKnowledge = await knowledgeResponse;
  expect(fetchedKnowledge.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "知识库管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: /添加知识/ })).toBeVisible();
  for (const column of ["ID", "状态", "标题", "分类", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }
  await page.getByRole("button", { name: /添加知识/ }).click();
  const knowledgeDialog = page.getByRole("dialog", { name: "添加知识" });
  await expect(knowledgeDialog.getByLabel("标题", { exact: true })).toBeVisible();
  await expect(knowledgeDialog.getByLabel("分类", { exact: true })).toBeVisible();
  for (const field of ["语言", "显示", "内容"]) await expect(knowledgeDialog.getByText(field, { exact: true }).first()).toBeVisible();
  await expect(knowledgeDialog.getByRole("combobox").first()).toBeVisible();
  await expect(knowledgeDialog.getByRole("switch")).toBeVisible();
  await expect(knowledgeDialog.getByRole("textbox", { name: "知识文章正文" })).toBeVisible();
  await knowledgeDialog.getByRole("button", { name: "取消" }).click();
  const userKnowledge = await page.request.get(new URL("/api/v1/user/knowledge/fetch?language=zh-CN", legacyURL).toString(), {
    headers: { authorization }
  });
  expect(userKnowledge.status()).toBe(200);
  const userKnowledgePayload = await userKnowledge.json() as { data?: unknown };
  expect(typeof userKnowledgePayload.data).toBe("object");

  const clientCatalogResponse = page.waitForResponse((response) => response.url().includes("/client-catalog") && !response.url().includes("/save"));
  await page.locator(".xboard-client-catalog-nav").click();
  const fetchedCatalog = await clientCatalogResponse;
  expect(fetchedCatalog.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "客户端管理" })).toBeVisible();
  await expect(page.getByRole("button", { name: "保存全部配置" })).toBeVisible();
  await expect(page.getByRole("button", { name: /Karing/ })).toBeVisible();
  const userCatalog = await page.request.get(new URL("/api/v1/user/client-catalog", legacyURL).toString(), {
    headers: { authorization }
  });
  expect(userCatalog.status()).toBe(200);
  const clientPayload = await userCatalog.json() as { data?: unknown };
  expectLegacyClientCatalog(clientPayload.data);
  expect(errors).toEqual([]);
});

test("legacy and Go node directories preserve the U1 management surface", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/server/manage/getNodes"));
    await legacyPage.locator('a[href="#/server/manage"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await expect(legacyPage.getByRole("heading", { name: "节点管理" })).toBeVisible();
    await expect(legacyPage.getByRole("button", { name: "添加节点", exact: true })).toBeVisible();

    const goFetch = goPage.waitForResponse((response) => response.url().includes("/api/v1/admin/nodes?"));
    await goPage.getByRole("button", { name: "节点管理", exact: true }).click();
    const goResponse = await goFetch;
    expect(goResponse.status()).toBe(200);
    expect(new URL(goResponse.url()).searchParams.get("page_size")).toBe("500");
    await expect(goPage.getByRole("heading", { name: "节点管理" })).toBeVisible();

    const columns = ["节点ID", "显隐", "节点", "部署方式", "地址", "在线人数", "倍率", "权限组", "流量使用", "操作"];
    for (const column of columns) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `旧 Xboard：${column}`).toBeVisible();
      await expect(goPage.getByRole("table", { name: "节点列表" }).getByRole("columnheader", { name: column, exact: true }), `Go：${column}`).toBeVisible();
    }

    await legacyPage.getByRole("button", { name: "添加节点", exact: true }).click();
    const legacyDialog = legacyPage.getByRole("dialog");
    await expect(legacyDialog.getByRole("heading", { name: "新建节点", exact: true })).toBeVisible();
    const legacyProtocolSelect = legacyDialog.getByRole("combobox").first();
    await legacyProtocolSelect.click();
    const protocols = ["Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"];
    for (const protocol of protocols) {
      await expect(legacyPage.getByText(protocol, { exact: true }).filter({ visible: true }).first(), `旧 Xboard：${protocol}`).toBeVisible();
      await expect(goPage.getByLabel("协议筛选").getByRole("option", { name: protocol, exact: true }), `Go：${protocol}`).toBeAttached();
    }
    for (const action of ["批量显示", "批量隐藏", "批量启用", "批量停用", "批量重置流量", "批量删除"]) {
      await expect(goPage.getByRole("button", { name: action, exact: true })).toBeVisible();
    }
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go user directories expose the same core table and query surface", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/user/fetch"));
    await legacyPage.locator('a[href="#/user/manage"]').click();
    expect((await legacyFetch).status()).toBe(200);
    const goFetch = goPage.waitForResponse((response) => response.url().includes("/api/v1/admin/users?"));
    await goPage.getByRole("button", { name: "用户管理", exact: true }).click();
    expect((await goFetch).status()).toBe(200);

    const columns = ["ID", "邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间", "操作"];
    for (const column of columns) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `旧 Xboard：${column}`).toBeVisible();
      await expect(goPage.getByRole("table", { name: "用户列表" }).getByRole("columnheader", { name: column }), `Go：${column}`).toBeVisible();
    }
    await expect(legacyPage.getByRole("button", { name: "高级筛选", exact: true })).toBeVisible();
    await expect(goPage.getByRole("button", { name: "高级筛选", exact: true })).toBeVisible();
    await expect(legacyPage.getByRole("button", { name: "创建用户", exact: true })).toBeVisible();
    await expect(goPage.getByRole("button", { name: "新增用户", exact: true })).toBeVisible();
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go user operation surfaces preserve related records and clickable traffic reset flows", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    const fixtureSuffix = Date.now();
    const fixtureEmail = `parity-u4-${fixtureSuffix}@example.test`;
    const createdPlanResponse = await goAdminRequest(goPage, "/api/v1/admin/plans", "POST", {
      group_id: null, transfer_enable: 10, name: `Parity U4 ${fixtureSuffix}`, speed_limit: null,
      content: "U4 parity fixture", reset_traffic_method: 1, capacity_limit: null,
      prices: { monthly: 100 }, device_limit: null, tags: ["parity"]
    });
    expect(createdPlanResponse.status, createdPlanResponse.body).toBe(201);
    const createdPlan = readObjectProperty(JSON.parse(createdPlanResponse.body) as unknown, "data");
    const generatedUserResponse = await goAdminRequest(goPage, "/api/v1/admin/users/generate", "POST", {
      mode: "single", email: fixtureEmail, count: 1, password: `parity-u4-password-${fixtureSuffix}`,
      plan_id: Number(readProperty(createdPlan, "id")),
      expired_at: new Date(Date.now() + 30 * 24 * 60 * 60 * 1_000).toISOString(),
      download_csv: false, is_distributor: false
    });
    expect(generatedUserResponse.status, generatedUserResponse.body).toBe(201);

    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/user/fetch"));
    await legacyPage.locator('a[href="#/user/manage"]').click();
    expect((await legacyFetch).status()).toBe(200);
    const legacyOperation = legacyPage.locator('button[aria-label="操作"]').first();
    await legacyOperation.click();
    for (const action of [
      "编辑", "分配订单", "复制订阅URL", "重置UUID及订阅URL",
      "TA的订单", "TA的邀请", "TA的流量记录", "重置流量", "删除"
    ]) {
      await expect(legacyPage.getByRole("menuitem", { name: action, exact: true }), `旧 Xboard：${action}`).toBeVisible();
    }
    await legacyPage.getByRole("menuitem", { name: "重置流量", exact: true }).click();
    const legacyReset = legacyPage.locator('[role="dialog"]:visible').last();
    await expect(legacyReset.getByText("流量重置", { exact: true })).toBeVisible();
    await expect(legacyReset.getByRole("tab", { name: "重置流量", exact: true })).toBeVisible();
    await expect(legacyReset.getByRole("tab", { name: "重置历史", exact: true })).toBeVisible();
    await expect(legacyReset.locator("textarea")).toBeEditable();
    await expect(legacyReset.getByRole("button", { name: "确认重置", exact: true })).toBeEnabled();
    await legacyReset.getByRole("button", { name: "Close", exact: true }).click();

    const goFetch = goPage.waitForResponse((response) => response.url().includes("/api/v1/admin/users?"));
    await goPage.getByRole("button", { name: "用户管理", exact: true }).click();
    expect((await goFetch).status()).toBe(200);
    const goTable = goPage.getByRole("table", { name: "用户列表" });
    const goRow = goTable.getByRole("row").filter({ hasText: fixtureEmail });
    await expect(goRow).toBeVisible();

    await goRow.getByRole("button", { name: `查看详情：${fixtureEmail}`, exact: true }).click();
    const detail = goPage.getByRole("dialog", { name: "用户详情" });
    await expect(detail.getByRole("button", { name: "复制订阅 URL", exact: true })).toBeVisible();
    await expect(detail).not.toContainText("/api/v1/client/subscribe?token=");
    await detail.getByRole("button", { name: "关闭", exact: true }).click();

    const openOperations = async () => {
      await goRow.getByRole("button", { name: `用户操作：${fixtureEmail}`, exact: true }).click();
      const dialog = goPage.getByRole("dialog", { name: "用户操作" });
      await expect(dialog).toBeVisible();
      await expect(goPage.locator('[role="dialog"]:visible')).toHaveCount(1);
      return dialog;
    };
    let operations = await openOperations();
    for (const action of ["分配订单", "TA 的订单", "TA 的邀请", "TA 的流量记录", "重置流量", "重置密码"]) {
      await expect(operations.getByRole("button", { name: action, exact: true }), `Go：${action}`).toBeVisible();
    }

    await operations.getByRole("button", { name: "分配订单", exact: true }).click();
    const assignment = goPage.getByRole("dialog", { name: "分配订单" });
    await expect(goPage.locator('[role="dialog"]:visible')).toHaveCount(1);
    await expect(assignment.getByLabel("用户邮箱")).toHaveAttribute("readonly", "");
    await assignment.getByRole("button", { name: "取消", exact: true }).click();

    operations = await openOperations();
    await operations.getByRole("button", { name: "TA 的订单", exact: true }).click();
    const related = goPage.getByRole("dialog", { name: "用户关联记录" });
    await expect(goPage.locator('[role="dialog"]:visible')).toHaveCount(1);
    for (const tab of ["TA 的订单", "TA 的邀请", "TA 的流量记录"]) {
      const button = related.getByRole("tab", { name: tab, exact: true });
      await button.click();
      await expect(button).toHaveAttribute("aria-selected", "true");
    }
    await related.getByRole("button", { name: "关闭关联记录面板", exact: true }).click();

    operations = await openOperations();
    await operations.getByRole("button", { name: "重置流量", exact: true }).click();
    const goReset = goPage.getByRole("dialog", { name: "重置流量" });
    await expect(goPage.locator('[role="dialog"]:visible')).toHaveCount(1);
    await expect(goReset.getByLabel("重置原因（可选）")).toBeEditable();
    await expect(goReset.getByRole("button", { name: "确认重置流量", exact: true })).toBeEnabled();
    const historyTab = goReset.getByRole("tab", { name: "重置历史", exact: true });
    await historyTab.click();
    await expect(historyTab).toHaveAttribute("aria-selected", "true");
    const resetTab = goReset.getByRole("tab", { name: "重置流量", exact: true });
    await resetTab.click();
    await expect(resetTab).toHaveAttribute("aria-selected", "true");
    await expect(goPage.locator('[role="dialog"]:visible')).toHaveCount(1);
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go user generators preserve single and batch concepts with approved credential hardening", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyRequests: Record<string, unknown>[] = [];
  const goRequests: Record<string, unknown>[] = [];
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await legacyPage.route(/\/api\/v2\/[^/]+\/user\/generate(?:\?.*)?$/, async (route) => {
      legacyRequests.push(route.request().postDataJSON() as Record<string, unknown>);
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data: true }) });
    });
    await goPage.route(/\/api\/v1\/admin\/users\/generate(?:\?.*)?$/, async (route) => {
      const input = route.request().postDataJSON() as Record<string, unknown>;
      goRequests.push(input);
      const mode = readStringProperty(input, "mode");
      const count = mode === "prefixed_batch" ? Number(input.count) : 1;
      const prefix = readStringProperty(input, "email_prefix") ?? "parity-single";
      const domain = readStringProperty(input, "email_domain") ?? "example.test";
      const items = Array.from({ length: count }, (_, index) => ({
        id: 900 + index,
        email: mode === "prefixed_batch" ? `${prefix}_${index + 1}@${domain}` : (readStringProperty(input, "email") ?? ""),
        password: mode === "single" ? (readStringProperty(input, "password") ?? "") : `independent-${index + 1}-credential`,
        expired_at: null,
        uuid: `00000000-0000-4000-8000-${String(index + 1).padStart(12, "0")}`,
        created_at: "2026-08-27T12:00:00Z",
        subscribe_url: `https://panel.example.test/s/parity-${index + 1}`
      }));
      await route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify({ data: { items } }) });
    });

    await loginLegacy(legacyPage);
    await loginGo(goPage);
    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/user/fetch"));
    await legacyPage.locator('a[href="#/user/manage"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await goPage.getByRole("button", { name: "用户管理", exact: true }).click();

    await legacyPage.getByRole("button", { name: "创建用户", exact: true }).click();
    let legacyDialog = legacyPage.locator('[role="dialog"]:visible').last();
    const legacyPrefix = legacyDialog.locator('input[placeholder="帐号(批量生成请留空)"]');
    const legacyDomain = legacyDialog.locator('input[placeholder="域"]');
    const legacyPassword = legacyDialog.locator('input[placeholder="留空则密码与邮件相同"]');
    await expect(legacyPrefix).toBeVisible();
    await expect(legacyDomain).toBeVisible();
    await expect(legacyPassword).toBeVisible();
    await expect(legacyDialog.getByText("订阅计划", { exact: true })).toBeVisible();
    await expect(legacyDialog.getByText("到期时间", { exact: true })).toBeVisible();
    await legacyPrefix.fill("parity-single");
    await legacyDomain.fill("example.test");
    await legacyPassword.fill("Parity-single-password-123");
    await expect(legacyDialog.locator('input[placeholder="如果为批量生产请输入生成数量"]')).toHaveCount(0);
    await legacyDialog.getByRole("button", { name: "确认", exact: true }).click();
    await expect.poll(() => legacyRequests.length).toBe(1);
    expect(legacyRequests[0]).toMatchObject({
      email_prefix: "parity-single", email_suffix: "example.test", password: "Parity-single-password-123",
      expired_at: null, plan_id: null, download_csv: false, is_distributor: 0, distributor_name: ""
    });

    await legacyPage.getByRole("button", { name: "创建用户", exact: true }).click();
    legacyDialog = legacyPage.locator('[role="dialog"]:visible').last();
    await legacyDialog.locator('input[placeholder="域"]').fill("example.test");
    await legacyDialog.locator('input[placeholder="如果为批量生产请输入生成数量"]').fill("2");
    await expect(legacyDialog.locator('input[placeholder="帐号(批量生成请留空)"]')).toHaveCount(0);
    await legacyDialog.getByRole("button", { name: "确认", exact: true }).click();
    await expect.poll(() => legacyRequests.length).toBe(2);
    expect(legacyRequests[1]).toMatchObject({
      email_prefix: "", email_suffix: "example.test", password: "", generate_count: 2,
      expired_at: null, plan_id: null, download_csv: false, is_distributor: 0, distributor_name: ""
    });

    await goPage.getByRole("button", { name: "新增用户", exact: true }).click();
    let goDialog = goPage.getByRole("dialog", { name: "新增用户" });
    await expect(goDialog.getByLabel("生成方式").getByRole("option")).toHaveText([
      "单个用户", "随机账号批量", "固定前缀批量"
    ]);
    await expect(goDialog.getByLabel("订阅计划")).toBeVisible();
    await expect(goDialog.getByLabel("到期时间（留空表示长期有效）")).toBeVisible();
    await goDialog.getByLabel("邮箱").fill("parity-single@example.test");
    await goDialog.getByLabel(/初始密码/).fill("Parity-single-password-123");
    await goDialog.getByRole("button", { name: "创建", exact: true }).click();
    await expect.poll(() => goRequests.length).toBe(1);
    expect(goRequests[0]).toEqual({
      mode: "single", email: "parity-single@example.test", password: "Parity-single-password-123",
      plan_id: null, expired_at: null, is_distributor: false, distributor_name: null
    });
    await expect(goDialog.getByRole("status")).toContainText("明文密码只在本窗口保留");
    await goDialog.getByRole("button", { name: "完成" }).click();

    await goPage.getByRole("button", { name: "新增用户", exact: true }).click();
    goDialog = goPage.getByRole("dialog", { name: "新增用户" });
    await goDialog.getByLabel("生成方式").selectOption("prefixed_batch");
    await expect(goDialog.getByLabel(/初始密码/)).toHaveCount(0);
    await goDialog.getByLabel("账号前缀").fill("parity-team");
    await goDialog.getByLabel("邮箱域").fill("example.test");
    await goDialog.getByLabel(/生成数量/).fill("2");
    await goDialog.getByRole("button", { name: "生成账号" }).click();
    await expect.poll(() => goRequests.length).toBe(2);
    expect(goRequests[1]).toEqual({
      mode: "prefixed_batch", email_prefix: "parity-team", email_domain: "example.test", count: 2,
      plan_id: null, expired_at: null, is_distributor: false, distributor_name: null
    });
    await expect(goDialog.getByRole("table", { name: "一次性账号凭据" }).getByRole("row")).toHaveCount(3);
    await expect(goDialog).toContainText("independent-1-credential");
    await expect(goDialog).toContainText("independent-2-credential");
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go user editors preserve the same profile concepts and explicit unit conversions", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  let legacyUpdate: Record<string, unknown> | undefined;
  let goUpdate: Record<string, unknown> | undefined;
  let goLegacyAuthorization = "";
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await legacyPage.route(/\/api\/v2\/[^/]+\/user\/update(?:\?.*)?$/, async (route) => {
      legacyUpdate = route.request().postDataJSON() as Record<string, unknown>;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data: true }) });
    });
    await goPage.route(/\/api\/v1\/admin\/users\/\d+(?:\?.*)?$/, async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      goUpdate = route.request().postDataJSON() as Record<string, unknown>;
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data: true }) });
    });

    await loginLegacy(legacyPage);
    await loginGo(goPage);
    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/user/fetch"));
    await legacyPage.locator('a[href="#/user/manage"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await goPage.getByRole("button", { name: "用户管理", exact: true }).click();

    await legacyPage.getByRole("button", { name: "操作", exact: true }).last().click();
    await legacyPage.getByRole("menuitem", { name: "编辑", exact: true }).click();
    const legacyDialog = legacyPage.locator('[role="dialog"]:visible').last();
    await expect(legacyDialog).toBeVisible();
    await goPage.locator('button[aria-label^="编辑用户："]').last().click();
    const goDialog = goPage.getByRole("dialog", { name: "编辑用户" });
    await expect(goDialog).toBeVisible();

    const fields = [
      ["邮箱", "邮箱"], ["邀请人邮箱", "邀请人邮箱（留空表示无）"], ["密码", "新密码（留空不修改）"],
      ["余额", "余额（元）"], ["佣金余额", "佣金余额（元）"], ["已用上行", "已用上行流量（GiB）"],
      ["已用下行", "已用下行流量（GiB）"], ["流量", "流量额度（GiB）"], ["到期时间", "到期时间（留空表示不限期）"],
      ["订阅计划", "套餐"], ["佣金类型", "佣金类型"], ["推荐返利比例", "佣金比例（留空使用系统默认）"],
      ["专享折扣比例", "专享折扣（留空使用系统默认）"], ["限速", "限速（Mbps，0 为不限速）"],
      ["设备限制", "设备数（0 为不限设备）"], ["是否管理员", "管理员"], ["是否员工", "员工"], ["备注", "备注"]
    ] as const;
    for (const [legacyLabel, goLabel] of fields) {
      await expect(legacyDialog.getByText(legacyLabel, { exact: true }).first(), `legacy editor field ${legacyLabel}`).toBeVisible();
      const goControl = goLabel === "套餐" || goLabel === "佣金类型"
        ? goDialog.getByRole("combobox", { name: goLabel, exact: true })
        : goDialog.getByLabel(goLabel, { exact: true });
      await expect(goControl, `Go editor field ${goLabel}`).toBeVisible();
    }
    await expect(legacyDialog.getByText("账户状态", { exact: true })).toBeVisible();
    await expect(goDialog.getByLabel("封禁用户", { exact: true })).toBeVisible();
    await expect(legacyDialog.getByText("是否分销商", { exact: true })).toBeVisible();
    await expect(goDialog.getByLabel("分销商", { exact: true })).toBeVisible();
    for (const goOnlyField of ["Telegram ID（留空表示未绑定）", "到期提醒", "流量提醒"]) {
      await expect(goDialog.getByLabel(goOnlyField, { exact: true })).toBeVisible();
    }

    await legacyDialog.locator('input[placeholder="请输入余额"]').fill("12.34");
    await legacyDialog.locator('input[placeholder="请输入佣金余额"]').fill("5.67");
    await legacyDialog.locator('input[placeholder="已用上行"]').fill("1.25");
    await legacyDialog.locator('input[placeholder="已用下行"]').fill("2.5");
    await legacyDialog.locator('input[placeholder="请输入流量"]').fill("10.75");
    await legacyDialog.getByRole("button", { name: "提交", exact: true }).click();

    await goDialog.getByLabel("余额（元）", { exact: true }).fill("12.34");
    await goDialog.getByLabel("佣金余额（元）", { exact: true }).fill("5.67");
    await goDialog.getByLabel("已用上行流量（GiB）", { exact: true }).fill("1.25");
    await goDialog.getByLabel("已用下行流量（GiB）", { exact: true }).fill("2.5");
    await goDialog.getByLabel("流量额度（GiB）", { exact: true }).fill("10.75");
    await goDialog.getByRole("button", { name: "保存", exact: true }).click();

    await expect.poll(() => legacyUpdate).toBeDefined();
    await expect.poll(() => goUpdate).toBeDefined();
    expect(legacyUpdate?.u).toBe(1_342_177_280);
    expect(legacyUpdate?.d).toBe(2_684_354_560);
    expect(goUpdate?.traffic_upload).toBe(legacyUpdate?.u);
    expect(goUpdate?.traffic_download).toBe(legacyUpdate?.d);
    expect(Number(legacyUpdate?.balance) * 100).toBe(goUpdate?.balance);
    expect(Number(legacyUpdate?.commission_balance) * 100).toBe(goUpdate?.commission_balance);
    expect(legacyUpdate?.transfer_enable).toBe(10_737_418_240);
    expect(goUpdate?.transfer_enable).toBe(11_542_724_608);
    expect(legacyUpdate?.is_distributor).toBe(0);

    const goLegacyLogin = await goPage.request.post(new URL("/api/v2/passport/auth/login", goURL).toString(), {
      data: { email: goEmail, password: goPassword }
    });
    expect(goLegacyLogin.status()).toBe(200);
    goLegacyAuthorization = readStringProperty(readProperty(await goLegacyLogin.json() as unknown, "data"), "auth_data") ?? "";
    expect(goLegacyAuthorization).not.toBe("");
    const goLegacyUsers = await goPage.request.post(new URL("/api/v2/admin/user/fetch", goURL).toString(), {
      headers: { authorization: goLegacyAuthorization }, data: { current: 1, pageSize: 200 }
    });
    expect(goLegacyUsers.status()).toBe(200);
    const goLegacyUserItems = readArrayProperty(await goLegacyUsers.json() as unknown, "data");
    expect(goLegacyUserItems).not.toBeNull();
    const compatibleTarget = goLegacyUserItems?.find((item) => readProperty(item, "is_distributor") === false);
    const compatibleTargetID = Number(readProperty(compatibleTarget, "id"));
    expect(Number.isSafeInteger(compatibleTargetID) && compatibleTargetID > 0).toBe(true);
    const observedNoChange = await goPage.request.post(new URL("/api/v2/admin/user/update", goURL).toString(), {
      headers: { authorization: goLegacyAuthorization },
      data: { id: compatibleTargetID, is_distributor: 0, distributor_name: "" }
    });
    expect(observedNoChange.status(), await observedNoChange.text()).toBe(200);
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    if (goLegacyAuthorization !== "") {
      await goPage.request.post(new URL("/api/v1/user/logout", goURL).toString(), {
        headers: { authorization: goLegacyAuthorization }
      }).catch(() => undefined);
    }
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go payment administration expose the same six core gateways and observable business fields", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyErrors = watchErrors(legacyPage);
  const goErrors = watchErrors(goPage);
  try {
    await loginLegacy(legacyPage);
    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/payment/fetch"));
    await legacyPage.locator('a[href="#/config/payment"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await expect(legacyPage.getByRole("heading", { name: "支付配置", exact: true })).toBeVisible();
    await expect(legacyPage.getByRole("button", { name: /添加支付方式/ })).toBeVisible();
    for (const column of ["ID", "启用", "显示名称", "支付接口", "通知地址", "操作"]) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `legacy payment column ${column}`).toBeVisible();
    }
    await legacyPage.getByRole("button", { name: /添加支付方式/ }).click();
    const legacyDialog = legacyPage.getByRole("dialog", { name: "添加支付方式" });
    for (const field of ["显示名称*", "图标URL", "通知域名", "百分比手续费(%)", "固定手续费", "支付接口*"]) {
      await expect(legacyDialog.getByText(field, { exact: true }).first(), `legacy payment field ${field}`).toBeVisible();
    }
    await legacyDialog.getByRole("combobox").click();
    for (const provider of ["AlipayF2F", "BTCPay", "CoinPayments", "Coinbase", "EPay", "MGate"]) {
      await expect(legacyPage.getByRole("option", { name: provider, exact: true })).toBeVisible();
    }
    const legacyForm = legacyPage.waitForResponse((response) => response.url().includes("/payment/getPaymentForm"));
    await legacyPage.getByRole("option", { name: "EPay", exact: true }).click();
    expect((await legacyForm).status()).toBe(200);
    for (const field of ["支付网关地址", "商户ID", "通信密钥", "支付类型"]) {
      await expect(legacyDialog.getByText(field, { exact: true }).first(), `legacy EPay field ${field}`).toBeVisible();
    }
    await legacyDialog.getByRole("button", { name: "取消", exact: true }).click();

    await loginGo(goPage);
    await goPage.getByRole("button", { name: "支付配置", exact: true }).click();
    await expect(goPage.getByRole("heading", { name: "支付配置", exact: true })).toBeVisible();
    for (const column of ["ID", "启用", "显示名称", "支付接口", "手续费", "通知地址", "操作"]) {
      await expect(goPage.getByText(column, { exact: true }).first(), `Go payment column ${column}`).toBeVisible();
    }
    await goPage.getByRole("button", { name: "添加支付方式", exact: true }).click();
    const goDialog = goPage.getByRole("dialog", { name: "添加支付方式" });
    for (const field of ["显示名称", "图标 URL", "通知域名", "百分比手续费（%）", "固定手续费（分）", "支付接口"]) {
      await expect(goDialog.getByLabel(field, { exact: true }), `Go payment field ${field}`).toBeVisible();
    }
    const providerOptions = await goDialog.getByLabel("支付接口", { exact: true }).locator("option").evaluateAll((options) => options.map((option) => option.getAttribute("value")));
    expect(providerOptions).toEqual(["AlipayF2F", "BTCPay", "CoinPayments", "Coinbase", "EPay", "MGate"]);
    await goDialog.getByLabel("支付接口", { exact: true }).selectOption("EPay");
    for (const field of ["支付网关地址", "商户ID", "通信密钥", "支付类型"]) {
      await expect(goDialog.getByLabel(field, { exact: true }), `Go EPay field ${field}`).toBeVisible();
    }
    await goDialog.getByRole("button", { name: "取消", exact: true }).click();
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go coupon administration expose the same observable business fields", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyErrors = watchErrors(legacyPage);
  const goErrors = watchErrors(goPage);
  try {
    await loginLegacy(legacyPage);
    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/coupon/fetch"));
    await legacyPage.locator('a[href="#/finance/coupon"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await expect(legacyPage.getByRole("heading", { name: "优惠券管理" })).toBeVisible();
    await expect(legacyPage.getByRole("button", { name: "添加优惠券" })).toBeVisible();
    for (const column of ["ID", "启用", "卷名称", "类型", "卷码", "剩余次数", "可用次数/用户", "有效期", "操作"]) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `legacy coupon column ${column}`).toBeVisible();
    }
    await legacyPage.getByRole("button", { name: "添加优惠券" }).click();
    const legacyDialog = legacyPage.getByRole("dialog", { name: "添加优惠券" });
    for (const field of ["优惠券名称*", "批量生成数量", "自定义优惠码", "优惠券类型和值", "优惠券有效期", "最大使用次数", "每个用户可使用次数", "指定周期", "指定订阅"]) {
      await expect(legacyDialog.getByText(field, { exact: true }).first(), `legacy coupon field ${field}`).toBeVisible();
    }
    await legacyDialog.getByRole("button", { name: "取消" }).click();

    await loginGo(goPage);
    const now = Math.floor(Date.now() / 1000);
    const created = await goAdminRequest(goPage, "/api/v1/admin/coupons", "POST", {
      code: `PARITY${now}`, name: "coupon parity fixture", type: 1, value: 100, show: true,
      limit_use: null, limit_use_with_user: null, limit_plan_ids: [], limit_period: [],
      started_at: now - 60, ended_at: now + 3600
    });
    expect(created.status, created.body).toBe(201);
    const createdCoupon = readProperty(JSON.parse(created.body) as unknown, "data");
    const createdCouponID = readProperty(createdCoupon, "id");
    expect(typeof createdCouponID).toBe("number");
    await goPage.getByRole("button", { name: "优惠券管理", exact: true }).click();
    await expect(goPage.getByRole("heading", { name: "优惠券管理" })).toBeVisible();
    await expect(goPage.getByRole("button", { name: "新增优惠券" })).toBeVisible();
    for (const column of ["ID / 状态", "卷名称", "类型", "卷码", "剩余次数", "每用户次数", "有效期", "操作"]) {
      await expect(goPage.getByText(column, { exact: true }).first(), `Go coupon column ${column}`).toBeVisible();
    }
    await goPage.getByRole("button", { name: "新增优惠券" }).click();
    const goDialog = goPage.getByRole("dialog", { name: "新增优惠券" });
    for (const field of ["卷名称", "卷码", "批量数量", "优惠金额（元）", "开始时间", "结束时间", "可用总次数", "每用户可用次数"]) {
      await expect(goDialog.getByLabel(field, { exact: true }), `Go coupon field ${field}`).toBeVisible();
    }
    await expect(goDialog.locator("label").filter({ hasText: "优惠类型" }).getByRole("combobox"), "Go coupon field 优惠类型").toBeVisible();
    for (const field of ["可用付款周期（不选表示不限）", "可用订阅套餐（不选表示不限）"]) {
      await expect(goDialog.getByText(field, { exact: true }), `Go coupon field ${field}`).toBeVisible();
    }
    await goDialog.getByRole("button", { name: "取消" }).click();
    const deleted = await goAdminRequest(goPage, `/api/v1/admin/coupons/${String(createdCouponID)}`, "DELETE");
    expect(deleted.status, deleted.body).toBe(204);
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy and Go gift-card administration expose the same four surfaces and business fields", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  const legacyErrors = watchErrors(legacyPage);
  const goErrors = watchErrors(goPage);
  try {
    await loginLegacy(legacyPage);
    const legacyFetch = legacyPage.waitForResponse((response) => response.url().includes("/gift-card/templates"));
    await legacyPage.locator('a[href="#/finance/gift-card"]').click();
    expect((await legacyFetch).status()).toBe(200);
    await expect(legacyPage.getByRole("heading", { name: "礼品卡管理", exact: true })).toBeVisible();
    for (const tab of ["模板管理", "兑换码管理", "使用记录", "统计数据"]) {
      await expect(legacyPage.getByText(tab, { exact: true }).last(), `legacy gift-card tab ${tab}`).toBeVisible();
    }
    for (const column of ["ID", "状态", "名称", "类型", "奖励内容", "排序", "创建时间", "操作"]) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `legacy gift-card template column ${column}`).toBeVisible();
    }
    await legacyPage.getByRole("button", { name: "添加模板", exact: true }).click();
    const legacyTemplateDialog = legacyPage.getByRole("dialog").last();
    for (const field of [
      "模板名称", "类型", "描述", "排序", "状态", "奖励余额 (元)", "奖励流量 (GB)", "延长有效期 (天)", "增加设备数",
      "重置当月流量", "新用户注册天数限制", "仅限新用户", "仅限付费用户", "需要邀请关系", "允许的套餐", "禁止的套餐",
      "单用户最大使用次数", "同类卡冷却时间(小时)", "邀请人奖励比例", "活动开始时间", "活动结束时间", "节日奖励乘数", "图标", "背景图片"
    ]) {
      await expect(legacyTemplateDialog.getByText(field, { exact: true }).first(), `legacy gift-card field ${field}`).toBeVisible();
    }
    expect(await legacyTemplateDialog.locator("select option").allTextContents()).toEqual(["通用礼品卡", "套餐礼品卡", "盲盒礼品卡"]);
    await legacyTemplateDialog.getByRole("button", { name: "取消", exact: true }).click();

    await legacyPage.getByText("兑换码管理", { exact: true }).last().click();
    for (const column of ["ID", "兑换码", "模板名称", "状态", "过期时间", "已用次数", "可用次数", "创建时间"]) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `legacy gift-card code column ${column}`).toBeVisible();
    }
    await legacyPage.getByRole("button", { name: "生成兑换码", exact: true }).click();
    const legacyGenerator = legacyPage.getByRole("dialog").last();
    for (const field of ["选择模板*", "生成数量*", "自定义前缀 (可选)", "有效期 (小时)*", "最大使用次数*", "导出CSV"]) {
      await expect(legacyGenerator.getByText(field, { exact: true }).first(), `legacy code-generator field ${field}`).toBeVisible();
    }
    await legacyGenerator.getByRole("button", { name: "取消", exact: true }).click();
    await legacyPage.getByText("使用记录", { exact: true }).last().click();
    for (const column of ["ID", "兑换码", "用户邮箱", "模板名称", "使用时间"]) {
      await expect(legacyPage.getByText(column, { exact: true }).first(), `legacy gift-card usage column ${column}`).toBeVisible();
    }
    await legacyPage.getByText("统计数据", { exact: true }).last().click();
    for (const metric of ["模板总数", "活跃模板数", "兑换码总数", "已使用兑换码"]) {
      await expect(legacyPage.getByText(metric, { exact: true }).first(), `legacy gift-card metric ${metric}`).toBeVisible();
    }

    await loginGo(goPage);
    await goPage.getByRole("button", { name: "礼品卡管理", exact: true }).click();
    await expect(goPage.getByRole("heading", { name: "礼品卡管理", exact: true })).toBeVisible();
    for (const tab of ["模板管理", "兑换码管理", "使用记录", "统计数据"]) {
      await expect(goPage.getByRole("button", { name: tab, exact: true }), `Go gift-card tab ${tab}`).toBeVisible();
    }
    for (const column of ["ID", "状态", "名称", "类型", "奖励内容", "排序", "创建时间", "操作"]) {
      await expect(goPage.getByText(column, { exact: true }).first(), `Go gift-card template column ${column}`).toBeVisible();
    }
    await goPage.getByRole("button", { name: "添加模板", exact: true }).click();
    const goTemplateDialog = goPage.getByRole("dialog", { name: "添加礼品卡模板" });
    for (const field of [
      "模板名称", "模板描述", "排序", "余额（元）", "流量（GB）", "有效期（天）", "设备数", "新用户最大注册天数",
      "允许套餐 ID", "禁止套餐 ID", "每用户最多使用次数", "冷却时间（小时）", "邀请奖励比例", "活动开始时间", "活动结束时间",
      "节日奖励倍率", "图标", "背景图片", "主题色"
    ]) {
      await expect(goTemplateDialog.getByLabel(field, { exact: true }), `Go gift-card field ${field}`).toBeVisible();
    }
    const goGiftCardType = goTemplateDialog.locator("select").first();
    await expect(goGiftCardType, "Go gift-card field 礼品卡类型").toBeVisible();
    expect(await goGiftCardType.locator("option").allTextContents()).toEqual(["通用礼品卡", "套餐礼品卡", "盲盒礼品卡"]);
    for (const condition of ["启用模板", "重置已用流量", "仅新用户", "仅付费用户", "必须有邀请人"]) {
      await expect(goTemplateDialog.getByText(condition, { exact: true }).first(), `Go gift-card condition ${condition}`).toBeVisible();
    }
    await goTemplateDialog.getByRole("button", { name: "取消", exact: true }).click();

    await goPage.getByRole("button", { name: "兑换码管理", exact: true }).click();
    for (const column of ["ID", "兑换码", "模板名称", "状态", "过期时间", "已用次数", "可用次数", "创建时间", "操作"]) {
      await expect(goPage.getByText(column, { exact: true }).first(), `Go gift-card code column ${column}`).toBeVisible();
    }
    await goPage.getByRole("button", { name: "生成兑换码", exact: true }).click();
    const goGenerator = goPage.getByTestId("modal-layer");
    await expect(goGenerator.locator("select").first(), "Go code-generator field 礼品卡模板").toBeVisible();
    for (const field of ["生成数量", "兑换码前缀", "有效期（小时）", "最大使用次数", "导出CSV"]) {
      await expect(goGenerator.getByLabel(field, { exact: true }), `Go code-generator field ${field}`).toBeVisible();
    }
    await goGenerator.getByRole("button", { name: "取消", exact: true }).click();
    await goPage.getByRole("button", { name: "使用记录", exact: true }).click();
    for (const column of ["ID", "兑换码", "用户邮箱", "模板名称", "使用时间"]) {
      await expect(goPage.getByText(column, { exact: true }).first(), `Go gift-card usage column ${column}`).toBeVisible();
    }
    await goPage.getByRole("button", { name: "统计数据", exact: true }).click();
    for (const metric of ["模板总数", "活跃模板数", "兑换码总数", "已使用兑换码"]) {
      await expect(goPage.getByText(metric, { exact: true }).first(), `Go gift-card metric ${metric}`).toBeVisible();
    }
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy system configuration exposes its observable sections and API groups", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacy(page);

  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const fetchedConfig = await configResponse;
  expect(fetchedConfig.status()).toBe(200);
  await expect(page.getByRole("heading", { name: "系统设置" })).toBeVisible();
  for (const section of ["站点设置", "安全设置", "订阅设置", "邀请&佣金设置", "节点配置", "邮件设置", "Telegram设置", "APP设置", "订阅模板"]) {
    await expect(page.getByRole("link", { name: section, exact: true }).filter({ visible: true }), section).toBeVisible();
  }

  const authorization = fetchedConfig.request().headers().authorization;
  expect(authorization).toBeTruthy();
  const allConfig = await page.request.get(legacyAdminAPI("/config/fetch"), { headers: { authorization } });
  expect(allConfig.status()).toBe(200);
  const payload: unknown = await allConfig.json();
  const data = readProperty(payload, "data");
  for (const group of ["invite", "site", "subscribe", "frontend", "server", "email", "telegram", "app", "safe", "subscribe_template"]) {
    expect(readProperty(data, group), `legacy config group ${group}`).toBeDefined();
  }
  expect(Object.keys(readObjectProperty(data, "site"))).toEqual(expect.arrayContaining([
    "logo", "force_https", "stop_register", "app_name", "app_description", "app_url", "subscribe_url",
    "try_out_plan_id", "try_out_hour", "tos_url", "currency", "currency_symbol", "ticket_must_wait_reply"
  ]));
  expect(Object.keys(readObjectProperty(data, "safe"))).toEqual(expect.arrayContaining([
    "email_verify", "safe_mode_enable", "secure_path", "email_whitelist_enable", "email_whitelist_suffix",
    "captcha_enable", "captcha_type", "register_limit_by_ip_enable", "register_limit_count", "register_limit_expire",
    "password_limit_enable"
  ]));
  expect(Object.keys(readObjectProperty(data, "email"))).toEqual(expect.arrayContaining([
    "email_host", "email_port", "email_username", "email_password", "email_encryption", "email_from_address", "remind_mail_enable"
  ]));
  await page.getByRole("link", { name: "安全设置", exact: true }).filter({ visible: true }).click();
  for (const field of ["邮箱验证", "禁止使用Gmail多别名", "邮箱后缀白名单", "IP注册限制"]) {
    await expect(page.getByText(field, { exact: true }).filter({ visible: true }).first(), field).toBeVisible();
  }
  expect(errors).toEqual([]);
});

test("legacy and Go CAPTCHA policies preserve the three observable provider contracts", async ({ browser }) => {
  const legacyContext = await browser.newContext();
  const goContext = await browser.newContext();
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    await exerciseLegacyCaptchaContract(legacyPage);
    await exerciseGoCaptchaContract(goPage);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy basic registration follows the public form and stop-register policy", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const configuration = readProperty(await fetched.json() as unknown, "data");
  const site = readObjectProperty(configuration, "site");
  const safe = readObjectProperty(configuration, "safe");
  const invite = readObjectProperty(configuration, "invite");
  const original = {
    stop_register: readProperty(site, "stop_register"),
    invite_force: readProperty(invite, "invite_force"),
    email_verify: readProperty(safe, "email_verify"),
    email_whitelist_enable: readProperty(safe, "email_whitelist_enable"),
    email_gmail_limit_enable: readProperty(safe, "email_gmail_limit_enable"),
    captcha_enable: readProperty(safe, "captcha_enable"),
    register_limit_by_ip_enable: readProperty(safe, "register_limit_by_ip_enable")
  };
  const unique = Date.now();
  const email = `REGISTER-PARITY-${unique}@LEGACY.LOCAL`;
  const normalizedEmail = email.toLowerCase();
  const closedEmail = `register-closed-${unique}@legacy.local`;
  const password = `register-parity-password-${unique}`;

  try {
    const opened = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: {
        stop_register: 0,
        invite_force: 0,
        email_verify: 0,
        email_whitelist_enable: 0,
        email_gmail_limit_enable: 0,
        captcha_enable: 0,
        register_limit_by_ip_enable: 0
      }
    });
    expect(opened.status()).toBe(200);

    await page.goto(new URL("/#/register", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await expect(page).toHaveTitle(/注册 \| XBoard/);
    await expect(page.getByPlaceholder("邮箱", { exact: true })).toBeVisible();
    await expect(page.getByPlaceholder("密码", { exact: true })).toBeVisible();
    await expect(page.getByPlaceholder("再次输入密码", { exact: true })).toBeVisible();
    await expect(page.getByPlaceholder("邀请码,（选填）", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "注册", exact: true })).toBeVisible();
    await expect(page.getByText("返回登入", { exact: true })).toBeVisible();

    const registered = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email, password }
    });
    expect(registered.status()).toBe(200);
    const registrationPayload = await registered.json() as unknown;
    expect(readStringProperty(registrationPayload, "status")).toBe("success");
    expect(readStringProperty(readProperty(registrationPayload, "data"), "auth_data")).toBeTruthy();

    const login = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: normalizedEmail, password }
    });
    expect(login.status()).toBe(200);

    const duplicate = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email: normalizedEmail, password }
    });
    expect(duplicate.status()).toBe(400);
    expect(readStringProperty(await duplicate.json() as unknown, "message")).toBe("邮箱已在系统中存在");

    const closed = await page.request.post(legacyAdminAPI("/config/save"), {
      headers, data: { stop_register: 1 }
    });
    expect(closed.status()).toBe(200);
    const blocked = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email: closedEmail, password }
    });
    expect(blocked.status()).toBe(400);
    expect(readStringProperty(await blocked.json() as unknown, "message")).toBe("本站已关闭注册");
    const blockedLogin = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: closedEmail, password }
    });
    expect(blockedLogin.status()).not.toBe(200);
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    const cleanup = `App\\Models\\User::whereIn("email",["${normalizedEmail}","${closedEmail}"])->delete();`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
  }
});

test("legacy registration email verification requires a one-time six-digit code", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const configuration = readProperty(await fetched.json() as unknown, "data");
  const site = readObjectProperty(configuration, "site");
  const safe = readObjectProperty(configuration, "safe");
  const invite = readObjectProperty(configuration, "invite");
  const original = {
    stop_register: readProperty(site, "stop_register"),
    invite_force: readProperty(invite, "invite_force"),
    email_verify: readProperty(safe, "email_verify"),
    email_whitelist_enable: readProperty(safe, "email_whitelist_enable"),
    email_gmail_limit_enable: readProperty(safe, "email_gmail_limit_enable"),
    captcha_enable: readProperty(safe, "captcha_enable"),
    register_limit_by_ip_enable: readProperty(safe, "register_limit_by_ip_enable")
  };
  const unique = Date.now();
  const email = `registration-verify-${unique}@legacy.local`;
  const password = `registration-verify-password-${unique}`;

  try {
    clearLegacyPasswordResetCache(email);
    const enabled = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: {
        stop_register: 0, invite_force: 0, email_verify: 1, email_whitelist_enable: 0,
        email_gmail_limit_enable: 0, captcha_enable: 0, register_limit_by_ip_enable: 0
      }
    });
    expect(enabled.status()).toBe(200);
    const guest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(readProperty(readProperty(await guest.json() as unknown, "data"), "is_email_verify")).toBe(1);

    await page.goto(new URL("/#/register", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await expect(page.getByPlaceholder("邮箱验证码", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "发送", exact: true })).toBeVisible();

    const sent = await page.request.post(new URL("/api/v1/passport/comm/sendEmailVerify", legacyURL).toString(), { data: { email } });
    expect(sent.status()).toBe(200);
    expect(readProperty(await sent.json() as unknown, "data")).toBe(true);
    const code = readLegacyPasswordResetCode(email);
    expect(code).toMatch(/^\d{6}$/);

    const resend = await page.request.post(new URL("/api/v1/passport/comm/sendEmailVerify", legacyURL).toString(), { data: { email } });
    expect(resend.status()).toBe(400);
    expect(readStringProperty(await resend.json() as unknown, "message")).toBe("验证码已发送，请过一会儿再请求");

    const missing = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email, password }
    });
    expect(missing.status()).toBe(422);
    const wrong = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email, password, email_code: code === "000000" ? "999999" : "000000" }
    });
    expect(wrong.status()).toBe(400);
    expect(readStringProperty(await wrong.json() as unknown, "message")).toBe("邮箱验证码有误");

    const registered = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email, password, email_code: code }
    });
    expect(registered.status()).toBe(200);
    expect(readStringProperty(readProperty(await registered.json() as unknown, "data"), "auth_data")).toBeTruthy();
    expect(legacyRedisKeys(`*EMAIL_VERIFY_CODE_${email}`)).toEqual([]);

    const reused = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
      data: { email, password, email_code: code }
    });
    expect(reused.status()).toBe(400);
    expect(readStringProperty(await reused.json() as unknown, "message")).toBe("邮箱验证码有误");
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    const cleanup = `App\\Models\\User::where("email","${email}")->delete();`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
    clearLegacyPasswordResetCache(email);
  }
});

test("legacy registration email policies and successful-IP limit remain observable", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const configuration = readProperty(await fetched.json() as unknown, "data");
  const site = readObjectProperty(configuration, "site");
  const safe = readObjectProperty(configuration, "safe");
  const invite = readObjectProperty(configuration, "invite");
  const original = {
    stop_register: readProperty(site, "stop_register"),
    invite_force: readProperty(invite, "invite_force"),
    email_verify: readProperty(safe, "email_verify"),
    email_whitelist_enable: readProperty(safe, "email_whitelist_enable"),
    email_whitelist_suffix: readProperty(safe, "email_whitelist_suffix"),
    email_gmail_limit_enable: readProperty(safe, "email_gmail_limit_enable"),
    captcha_enable: readProperty(safe, "captcha_enable"),
    register_limit_by_ip_enable: readProperty(safe, "register_limit_by_ip_enable"),
    register_limit_count: readProperty(safe, "register_limit_count"),
    register_limit_expire: readProperty(safe, "register_limit_expire")
  };
  const unique = Date.now();
  const password = `registration-policy-password-${unique}`;
  const testIP = `198.51.100.${unique % 200 + 1}`;
  const emails = {
    allowed: `policy-allowed-${unique}@allowed.test`,
    blocked: `policy-blocked-${unique}@blocked.test`,
    uppercaseDomain: `POLICY-UPPER-${unique}@ALLOWED.TEST`,
    gmailAlias: `policy.alias.${unique}@gmail.com`,
    gmailSimple: `policysimple${unique}@gmail.com`,
    nonGmailDot: `policy.alias.${unique}@example.test`,
    firstIP: `policy-ip1-${unique}@example.test`,
    secondIP: `policy-ip2-${unique}@example.test`,
    thirdIP: `policy-ip3-${unique}@example.test`
  };
  const register = (email: string, extraHeaders?: Record<string, string>) => page.request.post(
    new URL("/api/v1/passport/auth/register", legacyURL).toString(),
    { data: { email, password }, headers: extraHeaders }
  );

  try {
    const whitelist = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: {
        stop_register: 0, invite_force: 0, email_verify: 0, captcha_enable: 0,
        email_whitelist_enable: 1, email_whitelist_suffix: ["allowed.test"],
        email_gmail_limit_enable: 0, register_limit_by_ip_enable: 0
      }
    });
    expect(whitelist.status()).toBe(200);
    const guest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(readProperty(readProperty(await guest.json() as unknown, "data"), "email_whitelist_suffix")).toEqual(["allowed.test"]);

    const blocked = await register(emails.blocked);
    expect(blocked.status()).toBe(400);
    expect(readStringProperty(await blocked.json() as unknown, "message")).toBe("邮箱后缀不处于白名单中");
    const legacyCaseSensitiveRejection = await register(emails.uppercaseDomain);
    expect(legacyCaseSensitiveRejection.status()).toBe(400);
    expect(readStringProperty(await legacyCaseSensitiveRejection.json() as unknown, "message")).toBe("邮箱后缀不处于白名单中");
    expect((await register(emails.allowed)).status()).toBe(200);

    const gmailLimit = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: { email_whitelist_enable: 0, email_gmail_limit_enable: 1 }
    });
    expect(gmailLimit.status()).toBe(200);
    const gmailAlias = await register(emails.gmailAlias);
    expect(gmailAlias.status()).toBe(400);
    expect(readStringProperty(await gmailAlias.json() as unknown, "message")).toBe("不支持 Gmail 别名邮箱");
    expect((await register(emails.gmailSimple)).status()).toBe(200);
    const overbroadLegacyRejection = await register(emails.nonGmailDot);
    expect(overbroadLegacyRejection.status()).toBe(400);
    expect(readStringProperty(await overbroadLegacyRejection.json() as unknown, "message")).toBe("不支持 Gmail 别名邮箱");

    const ipLimit = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: {
        email_gmail_limit_enable: 0, register_limit_by_ip_enable: 1,
        register_limit_count: 2, register_limit_expire: 1
      }
    });
    expect(ipLimit.status()).toBe(200);
    const ipHeaders = { "X-Forwarded-For": testIP };
    const duplicateDoesNotConsumeQuota = await register(emails.allowed, ipHeaders);
    expect(duplicateDoesNotConsumeQuota.status()).toBe(400);
    expect(readStringProperty(await duplicateDoesNotConsumeQuota.json() as unknown, "message")).toBe("邮箱已在系统中存在");
    expect((await register(emails.firstIP, ipHeaders)).status()).toBe(200);
    expect((await register(emails.secondIP, ipHeaders)).status()).toBe(200);
    const rateLimited = await register(emails.thirdIP, ipHeaders);
    expect(rateLimited.status()).toBe(429);
    expect(readStringProperty(await rateLimited.json() as unknown, "message")).toBe("注册频繁，请等待 1 分钟后再次尝试");
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    const allEmails = Object.values(emails).map((email) => `"${email}"`).join(",");
    const cleanup = `App\\Models\\User::whereIn("email",[${allEmails}])->delete(); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("REGISTER_IP_RATE_LIMIT","${testIP}"));`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
  }
});

test("legacy invitation registration preserves forced, single-use, reusable, and referral behavior", async ({ page }) => {
  test.setTimeout(90_000);
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const configuration = readProperty(await fetched.json() as unknown, "data");
  const site = readObjectProperty(configuration, "site");
  const safe = readObjectProperty(configuration, "safe");
  const invite = readObjectProperty(configuration, "invite");
  const original = {
    stop_register: readProperty(site, "stop_register"),
    invite_force: readProperty(invite, "invite_force"),
    invite_gen_limit: readProperty(invite, "invite_gen_limit"),
    invite_never_expire: readProperty(invite, "invite_never_expire"),
    email_verify: readProperty(safe, "email_verify"),
    email_whitelist_enable: readProperty(safe, "email_whitelist_enable"),
    email_gmail_limit_enable: readProperty(safe, "email_gmail_limit_enable"),
    captcha_enable: readProperty(safe, "captcha_enable"),
    register_limit_by_ip_enable: readProperty(safe, "register_limit_by_ip_enable")
  };
  const unique = Date.now();
  const password = `legacy-invite-password-${unique}`;
  // The legacy login form silently truncates email values after 40 characters.
  const emails = {
    inviter: `li-${unique}@test.local`,
    missing: `lim-${unique}@test.local`,
    invalid: `lii-${unique}@test.local`,
    optionalInvalid: `lio-${unique}@test.local`,
    singleUse: `lis-${unique}@test.local`,
    reused: `lir-${unique}@test.local`,
    reusedAgain: `lira-${unique}@test.local`
  };
  const register = (email: string, inviteCode?: string) => page.request.post(
    new URL("/api/v1/passport/auth/register", legacyURL).toString(),
    { data: { email, password, ...(inviteCode === undefined ? {} : { invite_code: inviteCode }) } }
  );
  const fetchInvites = async (userHeaders: Record<string, string>) => {
    const response = await page.request.get(new URL("/api/v1/user/invite/fetch", legacyURL).toString(), { headers: userHeaders });
    expect(response.status()).toBe(200);
    return readProperty(await response.json() as unknown, "data");
  };

  try {
    const opened = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: {
        stop_register: 0, invite_force: 0, invite_gen_limit: 1, invite_never_expire: 0,
        email_verify: 0, email_whitelist_enable: 0, email_gmail_limit_enable: 0,
        captcha_enable: 0, register_limit_by_ip_enable: 0
      }
    });
    expect(opened.status()).toBe(200);
    const optionalGuest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(readProperty(readProperty(await optionalGuest.json() as unknown, "data"), "is_invite_force")).toBe(0);
    await page.goto(new URL("/#/register", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await expect(page.getByPlaceholder("邀请码,（选填）", { exact: true })).toBeVisible();

    const inviterRegistration = await register(emails.inviter);
    expect(inviterRegistration.status()).toBe(200);
    const inviterAuthorization = readStringProperty(readProperty(await inviterRegistration.json() as unknown, "data"), "auth_data");
    expect(inviterAuthorization).toBeTruthy();
    if (!inviterAuthorization) throw new Error("legacy inviter authorization is missing");
    const userHeaders = { authorization: inviterAuthorization };
    expect(readArrayProperty(await fetchInvites(userHeaders), "codes")).toEqual([]);

    const generated = await page.request.get(new URL("/api/v1/user/invite/save", legacyURL).toString(), { headers: userHeaders });
    expect(generated.status()).toBe(200);
    expect(readProperty(await generated.json() as unknown, "data")).toBe(true);
    let invitationData = await fetchInvites(userHeaders);
    let codes = readArrayProperty(invitationData, "codes");
    expect(codes).toHaveLength(1);
    if (codes === null || codes.length !== 1) throw new Error("legacy invitation code was not generated");
    const singleUseCode = readStringProperty(codes[0], "code");
    expect(singleUseCode).toMatch(/^[A-Za-z0-9]{8}$/);
    if (!singleUseCode) throw new Error("legacy invitation code is missing");

    const viewed = await page.request.post(new URL("/api/v1/passport/comm/pv", legacyURL).toString(), { data: { invite_code: singleUseCode } });
    expect(viewed.status()).toBe(200);
    invitationData = await fetchInvites(userHeaders);
    codes = readArrayProperty(invitationData, "codes");
    expect(readProperty(codes?.[0], "pv")).toBe(1);
    await page.goto(new URL(`/#/register?code=${singleUseCode}`, legacyURL).toString(), { waitUntil: "domcontentloaded" });
    const linkedInvitation = page.getByPlaceholder("邀请码,（选填）", { exact: true });
    await expect(linkedInvitation).toHaveValue(singleUseCode);
    await expect(linkedInvitation).toBeDisabled();

    await page.context().clearCookies();
    await page.goto(new URL("/", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await page.evaluate(() => window.localStorage.clear());
    await page.reload({ waitUntil: "domcontentloaded" });
    const loginFields = page.locator("input:visible");
    await loginFields.first().fill(emails.inviter);
    await loginFields.nth(1).fill(password);
    await loginFields.nth(1).press("Enter");
    const invitationNavigation = page.getByText("我的邀请", { exact: true });
    await expect(invitationNavigation).toBeVisible();
    await invitationNavigation.click();
    await expect(page).toHaveURL(/#\/invite$/);
    await expect(page.getByText("邀请码管理", { exact: true })).toBeVisible();
    await expect(page.getByText(singleUseCode, { exact: true })).toBeVisible();
    await expect(page.getByText("生成邀请码", { exact: true })).toBeVisible();

    const forced = await page.request.post(legacyAdminAPI("/config/save"), {
      headers, data: { invite_force: 1, invite_never_expire: 0 }
    });
    expect(forced.status()).toBe(200);
    const forcedGuest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(readProperty(readProperty(await forcedGuest.json() as unknown, "data"), "is_invite_force")).toBe(1);
    await page.goto(new URL("/#/register", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await expect(page.getByPlaceholder("邀请码,（必填）", { exact: true })).toBeVisible();

    const missing = await register(emails.missing);
    expect(missing.status()).toBe(422);
    expect(readStringProperty(await missing.json() as unknown, "message")).toBe("必须使用邀请码才可以注册");
    const invalid = await register(emails.invalid, "NotARealCode");
    expect(invalid.status()).toBe(400);
    expect(readStringProperty(await invalid.json() as unknown, "message")).toBe("邀请码无效");
    expect((await register(emails.singleUse, singleUseCode)).status()).toBe(200);
    const reusedSingleUse = await register(emails.reused, singleUseCode);
    expect(reusedSingleUse.status()).toBe(400);
    expect(readStringProperty(await reusedSingleUse.json() as unknown, "message")).toBe("邀请码无效");
    invitationData = await fetchInvites(userHeaders);
    expect(readArrayProperty(invitationData, "codes")).toEqual([]);
    expect(readArrayProperty(invitationData, "stat")?.[0]).toBe(1);

    const optional = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: { invite_force: 0 } });
    expect(optional.status()).toBe(200);
    expect((await register(emails.optionalInvalid, "NotARealCode")).status()).toBe(200);
    invitationData = await fetchInvites(userHeaders);
    expect(readArrayProperty(invitationData, "stat")?.[0]).toBe(1);

    const generatedReusable = await page.request.get(new URL("/api/v1/user/invite/save", legacyURL).toString(), { headers: userHeaders });
    expect(generatedReusable.status()).toBe(200);
    invitationData = await fetchInvites(userHeaders);
    codes = readArrayProperty(invitationData, "codes");
    expect(codes).toHaveLength(1);
    const reusableCode = readStringProperty(codes?.[0], "code");
    expect(reusableCode).toMatch(/^[A-Za-z0-9]{8}$/);
    if (!reusableCode) throw new Error("legacy reusable invitation code is missing");
    const limitReached = await page.request.get(new URL("/api/v1/user/invite/save", legacyURL).toString(), { headers: userHeaders });
    expect(limitReached.status()).toBe(400);
    expect(readStringProperty(await limitReached.json() as unknown, "message")).toBe("已达到创建数量上限");

    const reusable = await page.request.post(legacyAdminAPI("/config/save"), {
      headers, data: { invite_force: 1, invite_never_expire: 1 }
    });
    expect(reusable.status()).toBe(200);
    expect((await register(emails.reused, reusableCode)).status()).toBe(200);
    expect((await register(emails.reusedAgain, reusableCode)).status()).toBe(200);
    invitationData = await fetchInvites(userHeaders);
    expect(readStringProperty(readArrayProperty(invitationData, "codes")?.[0], "code")).toBe(reusableCode);
    expect(readArrayProperty(invitationData, "stat")?.[0]).toBe(3);
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    const allEmails = Object.values(emails).map((email) => `"${email}"`).join(",");
    const cleanup = `$ids=App\\Models\\User::whereIn("email",[${allEmails}])->pluck("id"); App\\Models\\InviteCode::whereIn("user_id",$ids)->delete(); App\\Models\\User::whereIn("email",[${allEmails}])->delete();`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
  }
});

test("legacy password recovery preserves fields, cooldown, lockout, one-time code, and session revocation", async ({ page }) => {
  test.setTimeout(90_000);
  const newPassword = `legacy-reset-password-${Date.now()}`;
  const encodedEmail = Buffer.from(legacyEmail, "utf8").toString("base64");
  const encodedOriginalPassword = Buffer.from(legacyPassword, "utf8").toString("base64");
  const clearCache = `$email=base64_decode("${encodedEmail}"); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("EMAIL_VERIFY_CODE",$email)); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("LAST_SEND_EMAIL_VERIFY_TIMESTAMP",$email)); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("FORGET_REQUEST_LIMIT",$email)); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("PASSWORD_ERROR_LIMIT",$email));`;
  const tinker = (statement: string) => execFileSync(
    "docker",
    ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${statement}`],
    { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" }
  );

  try {
    tinker(clearCache);
    clearLegacyPasswordResetCache(legacyEmail);
    await page.goto(new URL("/#/forgetpassword", legacyURL).toString(), { waitUntil: "domcontentloaded" });
    await expect(page).toHaveTitle(/重置密码 \| XBoard/);
    for (const placeholder of ["邮箱", "邮箱验证码", "密码", "再次输入密码"]) {
      await expect(page.getByPlaceholder(placeholder, { exact: true })).toBeVisible();
    }
    await expect(page.getByRole("button", { name: "发送", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "重置密码", exact: true })).toBeVisible();
    await expect(page.getByText("返回登入", { exact: true })).toBeVisible();

    const loggedIn = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: legacyEmail, password: legacyPassword }
    });
    expect(loggedIn.status()).toBe(200);
    const authorization = readStringProperty(readProperty(await loggedIn.json() as unknown, "data"), "auth_data");
    expect(authorization).toBeTruthy();
    if (!authorization) throw new Error("legacy password recovery session token is missing");
    const oldSession = await page.request.get(new URL("/api/v1/user/info", legacyURL).toString(), { headers: { authorization } });
    expect(oldSession.status()).toBe(200);

    const sent = await page.request.post(new URL("/api/v1/passport/comm/sendEmailVerify", legacyURL).toString(), {
      data: { email: legacyEmail }
    });
    expect(sent.status()).toBe(200);
    expect(readProperty(await sent.json() as unknown, "data")).toBe(true);
    const repeated = await page.request.post(new URL("/api/v1/passport/comm/sendEmailVerify", legacyURL).toString(), {
      data: { email: legacyEmail }
    });
    expect(repeated.status()).toBe(400);
    expect(readStringProperty(await repeated.json() as unknown, "message")).toBe("验证码已发送，请过一会儿再请求");

    const code = readLegacyPasswordResetCode(legacyEmail);
    expect(code).toMatch(/^\d{6}$/);
    const wrongCodes = ["000000", "000001", "000002"].map((candidate) => candidate === code ? "999999" : candidate);
    for (const wrongCode of wrongCodes) {
      const wrong = await page.request.post(new URL("/api/v1/passport/auth/forget", legacyURL).toString(), {
        data: { email: legacyEmail, password: newPassword, email_code: wrongCode }
      });
      expect(wrong.status()).toBe(400);
      expect(readStringProperty(await wrong.json() as unknown, "message")).toBe("邮箱验证码有误");
    }
    const locked = await page.request.post(new URL("/api/v1/passport/auth/forget", legacyURL).toString(), {
      data: { email: legacyEmail, password: newPassword, email_code: code }
    });
    expect(locked.status()).toBe(429);
    expect(readStringProperty(await locked.json() as unknown, "message")).toBe("重置失败，请稍后再试");

    tinker(`$email=base64_decode("${encodedEmail}"); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("FORGET_REQUEST_LIMIT",$email));`);
    const reset = await page.request.post(new URL("/api/v1/passport/auth/forget", legacyURL).toString(), {
      data: { email: legacyEmail, password: newPassword, email_code: code }
    });
    expect(reset.status()).toBe(200);
    expect(readProperty(await reset.json() as unknown, "data")).toBe(true);
    const revokedSession = await page.request.get(new URL("/api/v1/user/info", legacyURL).toString(), { headers: { authorization } });
    expect(revokedSession.status()).toBe(403);
    const reused = await page.request.post(new URL("/api/v1/passport/auth/forget", legacyURL).toString(), {
      data: { email: legacyEmail, password: `${newPassword}-again`, email_code: code }
    });
    expect(reused.status()).toBe(400);
    expect(readStringProperty(await reused.json() as unknown, "message")).toBe("邮箱验证码有误");
    expect((await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: legacyEmail, password: legacyPassword }
    })).status()).toBe(400);
    expect((await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: legacyEmail, password: newPassword }
    })).status()).toBe(200);
  } finally {
    const restore = `$email=base64_decode("${encodedEmail}"); $user=App\\Models\\User::byEmail($email)->first(); if ($user) { $user->password=password_hash(base64_decode("${encodedOriginalPassword}"), PASSWORD_DEFAULT); $user->password_algo=null; $user->password_salt=null; $user->save(); } ${clearCache}`;
    tinker(restore);
    clearLegacyPasswordResetCache(legacyEmail);
    const restoredLogin = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email: legacyEmail, password: legacyPassword }
    });
    expect(restoredLogin.status()).toBe(200);
  }
});

test("Go Passport email compatibility preserves the legacy v1 and v2 validation contract", async ({ page }) => {
  for (const version of ["v1", "v2"]) {
    for (const [path, data] of [
      ["passport/comm/sendEmailVerify", { email: "not-an-email" }],
      ["passport/auth/forget", { email: "not-an-email", password: "short", email_code: "invalid" }]
    ] as const) {
      const legacyResponse = await page.request.post(new URL(`/api/${version}/${path}`, legacyURL).toString(), { data });
      const goResponse = await page.request.post(new URL(`/api/${version}/${path}`, goURL).toString(), { data });

      expect(legacyResponse.status(), `${version} ${path} legacy status`).toBe(422);
      expect(goResponse.status(), `${version} ${path} Go status`).toBe(legacyResponse.status());
      expect(await goResponse.json(), `${version} ${path} response`).toEqual(await legacyResponse.json());
    }
  }
});

test("Go Passport v1 and v2 preserve the remaining public Xboard contracts", async ({ page }) => {
  for (const version of ["v1", "v2"]) {
    for (const [method, path, data] of [
      ["POST", "passport/auth/login", { email: "invalid", password: "short" }],
      ["POST", "passport/auth/register", { email: "invalid", password: "short" }],
      ["POST", "passport/auth/loginWithMailLink", { email: "invalid" }],
      ["POST", "passport/auth/getQuickLoginUrl", {}],
      ["POST", "passport/auth/getQuickLoginUrl", { auth_data: "Bearer invalid" }],
      ["GET", "passport/auth/token2Login", undefined],
      ["GET", "passport/auth/token2Login?verify=invalid", undefined],
      ["POST", "passport/comm/pv", { invite_code: "Badc1234" }]
    ] as const) {
      const legacyEndpoint = new URL(`/api/${version}/${path}`, legacyURL).toString();
      const goEndpoint = new URL(`/api/${version}/${path}`, goURL).toString();
      const legacyResponse = method === "POST"
        ? await page.request.post(legacyEndpoint, { data })
        : await page.request.get(legacyEndpoint);
      const goResponse = method === "POST"
        ? await page.request.post(goEndpoint, { data })
        : await page.request.get(goEndpoint);

      expect(goResponse.status(), `${version} ${method} ${path} status`).toBe(legacyResponse.status());
      expect(await goResponse.json(), `${version} ${method} ${path} response`).toEqual(await legacyResponse.json());
    }
  }
});

test("legacy password error limit exposes its configurable threshold, expiry, and historical bypasses", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const safe = readObjectProperty(readProperty(await fetched.json() as unknown, "data"), "safe");
  const original = {
    password_limit_enable: readProperty(safe, "password_limit_enable"),
    password_limit_count: readProperty(safe, "password_limit_count"),
    password_limit_expire: readProperty(safe, "password_limit_expire")
  };
  const unique = Date.now();
  const emailPrefix = `login-limit-${unique}`;
  const email = `${emailPrefix}@legacy.local`;
  const uppercaseEmail = email.toUpperCase();
  const unknownEmail = `unknown-${unique}@legacy.local`;
  const password = `login-limit-password-${unique}`;
  const loginURL = new URL("/api/v1/passport/auth/login", legacyURL).toString();
  const login = (candidateEmail: string, candidatePassword: string) => page.request.post(loginURL, {
    data: { email: candidateEmail, password: candidatePassword }
  });

  try {
    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers,
      data: { email_prefix: emailPrefix, email_suffix: "legacy.local", password }
    });
    expect(generated.status()).toBe(200);
    const configured = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: { password_limit_enable: 1, password_limit_count: 2, password_limit_expire: 1 }
    });
    expect(configured.status()).toBe(200);
    clearLegacyPasswordErrorLimit(email);
    clearLegacyPasswordErrorLimit(uppercaseEmail);

    const firstWrong = await login(email, `${password}-wrong-1`);
    expect(firstWrong.status()).toBe(400);
    expect(readStringProperty(await firstWrong.json() as unknown, "message")).toBe("邮箱或密码错误");
    expect((await login(email, password)).status()).toBe(200);
    const secondWrong = await login(email, `${password}-wrong-2`);
    expect(secondWrong.status()).toBe(400);

    const locked = await login(email, password);
    expect(locked.status()).toBe(429);
    expect(readStringProperty(await locked.json() as unknown, "message")).toBe("密码错误次数过多，请 1 分钟后再试");

    const legacyCaseBypass = await login(uppercaseEmail, password);
    expect(legacyCaseBypass.status()).toBe(200);
    for (let attempt = 0; attempt < 3; attempt += 1) {
      const unknown = await login(unknownEmail, `${password}-unknown-${attempt}`);
      expect(unknown.status()).toBe(400);
      expect(readStringProperty(await unknown.json() as unknown, "message")).toBe("邮箱或密码错误");
    }

    const disabled = await page.request.post(legacyAdminAPI("/config/save"), {
      headers,
      data: { password_limit_enable: 0 }
    });
    expect(disabled.status()).toBe(200);
    expect((await login(email, password)).status()).toBe(200);
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    clearLegacyPasswordErrorLimit(email);
    clearLegacyPasswordErrorLimit(uppercaseEmail);
    clearLegacyPasswordErrorLimit(unknownEmail);
    legacyTinker(`App\\Models\\User::where("email","${email}")->delete();`);
  }
});

test("Go password error limit preserves Xboard semantics and closes observed bypasses", async ({ page }) => {
  await loginGo(page);
  const originalResponse = await goAdminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(originalResponse.status, originalResponse.body).toBe(200);
  const original = readObjectProperty(JSON.parse(originalResponse.body) as unknown, "data");
  const unique = Date.now();
  const email = `go-login-limit-${unique}@example.test`;
  const unknownEmail = `go-unknown-login-limit-${unique}@example.test`;
  const password = `go-login-limit-password-${unique}`;
  let created: Record<string, unknown> | null = null;
  const loginContext = await playwrightRequest.newContext({ baseURL: goURL });
  const login = (candidateEmail: string, candidatePassword: string) => loginContext.post("/api/v1/passport/auth/login", {
    data: { email: candidateEmail, password: candidatePassword }
  });

  try {
    const configured = await goAdminRequest(page, "/api/v1/admin/site-settings", "PUT", {
      revision: Number(readProperty(original, "revision")),
      app_name: readStringProperty(original, "app_name"), app_description: readStringProperty(original, "app_description"),
      app_url: readStringProperty(original, "app_url"), tos_url: readStringProperty(original, "tos_url"),
      logo: readStringProperty(original, "logo"), password_limit_enable: true,
      password_limit_count: 2, password_limit_expire: 1
    });
    expect(configured.status, configured.body).toBe(200);
    const generated = await goAdminRequest(page, "/api/v1/admin/users", "POST", {
      email, password, group_id: null, transfer_enable: 1_073_741_824, expired_at: null,
      speed_limit: 0, device_limit: 0, banned: false
    });
    expect(generated.status, generated.body).toBe(201);
    created = readObjectProperty(JSON.parse(generated.body) as unknown, "data");

    const firstWrong = await login(email, `${password}-wrong-1`);
    expect(firstWrong.status()).toBe(401);
    const firstError = readObjectProperty(await firstWrong.json() as unknown, "error");
    expect(readStringProperty(firstError, "code")).toBe("invalid_credentials");
    expect(readStringProperty(firstError, "message")).toBe("邮箱或密码错误");
    expect((await login(email, password)).status()).toBe(200);
    expect((await login(email, `${password}-wrong-2`)).status()).toBe(401);
    const locked = await login(email, password);
    expect(locked.status()).toBe(429);
    const lockedError = readObjectProperty(await locked.json() as unknown, "error");
    expect(readStringProperty(lockedError, "code")).toBe("login_rate_limited");
    expect(readStringProperty(lockedError, "message")).toBe("密码错误次数过多，请 1 分钟后再试");
    expect(locked.headers()["retry-after"]).toMatch(/^(?:[1-9]|[1-5]\d|60)$/);
    expect((await login(`  ${email.toUpperCase()}  `, password)).status()).toBe(429);

    for (let attempt = 0; attempt < 2; attempt += 1) {
      const unknown = await login(unknownEmail, `${password}-unknown-${attempt}`);
      expect(unknown.status()).toBe(401);
      const error = readObjectProperty(await unknown.json() as unknown, "error");
      expect(readStringProperty(error, "code")).toBe("invalid_credentials");
      expect(readStringProperty(error, "message")).toBe("邮箱或密码错误");
    }
    expect((await login(unknownEmail.toUpperCase(), `${password}-unknown-locked`)).status()).toBe(429);

    const current = readObjectProperty(JSON.parse(configured.body) as unknown, "data");
    const disabled = await goAdminRequest(page, "/api/v1/admin/site-settings", "PUT", {
      revision: Number(readProperty(current, "revision")),
      app_name: readStringProperty(current, "app_name"), app_description: readStringProperty(current, "app_description"),
      app_url: readStringProperty(current, "app_url"), tos_url: readStringProperty(current, "tos_url"),
      logo: readStringProperty(current, "logo"), password_limit_enable: false
    });
    expect(disabled.status, disabled.body).toBe(200);
    expect((await login(email, password)).status()).toBe(200);
  } finally {
    if (created !== null) {
      const banned = await goAdminRequest(page, `/api/v1/admin/users/${Number(readProperty(created, "id"))}`, "PATCH", {
        revision: Number(readProperty(created, "revision")), email: readStringProperty(created, "email"),
        group_id: readProperty(created, "group_id"), transfer_enable: Number(readProperty(created, "transfer_enable")),
        expired_at: readProperty(created, "expired_at"), speed_limit: Number(readProperty(created, "speed_limit")),
        device_limit: Number(readProperty(created, "device_limit")), banned: true
      });
      expect(banned.status, banned.body).toBe(200);
    }
    const latestResponse = await goAdminRequest(page, "/api/v1/admin/site-settings", "GET");
    const latest = readObjectProperty(JSON.parse(latestResponse.body) as unknown, "data");
    const restored = await goAdminRequest(page, "/api/v1/admin/site-settings", "PUT", {
      revision: Number(readProperty(latest, "revision")),
      app_name: readStringProperty(original, "app_name"), app_description: readStringProperty(original, "app_description"),
      app_url: readStringProperty(original, "app_url"), tos_url: readStringProperty(original, "tos_url"),
      logo: readStringProperty(original, "logo"), password_limit_enable: readProperty(original, "password_limit_enable"),
      password_limit_count: readProperty(original, "password_limit_count"),
      password_limit_expire: readProperty(original, "password_limit_expire")
    });
    expect(restored.status, restored.body).toBe(200);
    await loginContext.dispose();
  }
});

test("legacy bearer credentials are permanent and support complete session revocation", async ({ page }) => {
  const unique = Date.now();
  const emailPrefix = `auth-session-${unique}`;
  const email = `${emailPrefix}@legacy.local`;
  const originalPassword = `auth-session-password-${unique}`;
  const changedPassword = `${originalPassword}-changed`;
  const loginURL = new URL("/api/v1/passport/auth/login", legacyURL).toString();
  const infoURL = new URL("/api/v1/user/info", legacyURL).toString();
  const activeSessionsURL = new URL("/api/v1/user/getActiveSession", legacyURL).toString();
  const removeSessionURL = new URL("/api/v1/user/removeActiveSession", legacyURL).toString();
  const logoutURL = new URL("/api/v1/user/logout", legacyURL).toString();

  const administratorLogin = await page.request.post(loginURL, {
    data: { email: legacyEmail, password: legacyPassword }
  });
  expect(administratorLogin.status()).toBe(200);
  const administratorAuthorization = readStringProperty(
    readProperty(await administratorLogin.json() as unknown, "data"),
    "auth_data"
  );
  if (!administratorAuthorization) throw new Error("legacy administrator bearer credential is missing");

  try {
    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization: administratorAuthorization },
      data: { email_prefix: emailPrefix, email_suffix: "legacy.local", password: originalPassword }
    });
    expect(generated.status()).toBe(200);

    const firstLogin = await page.request.post(loginURL, { data: { email, password: originalPassword } });
    const secondLogin = await page.request.post(loginURL, { data: { email, password: originalPassword } });
    expect(firstLogin.status()).toBe(200);
    expect(secondLogin.status()).toBe(200);
    const firstData = readObjectProperty(await firstLogin.json() as unknown, "data");
    const secondData = readObjectProperty(await secondLogin.json() as unknown, "data");
    expect(Object.keys(firstData).sort()).toEqual(["auth_data", "is_admin", "is_distributor", "token"]);
    expect(readProperty(firstData, "is_admin")).toBe(false);
    expect(readProperty(firstData, "is_distributor")).toBe(false);
    const firstAuthorization = readStringProperty(firstData, "auth_data");
    const secondAuthorization = readStringProperty(secondData, "auth_data");
    expect(firstAuthorization).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);
    expect(secondAuthorization).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);
    if (!firstAuthorization || !secondAuthorization) throw new Error("legacy bearer credentials are missing");

    const changed = await page.request.post(new URL("/api/v1/user/changePassword", legacyURL).toString(), {
      headers: { authorization: firstAuthorization },
      data: { old_password: originalPassword, new_password: changedPassword }
    });
    expect(changed.status()).toBe(200);
    expect(readProperty(await changed.json() as unknown, "data")).toBe(true);
    expect((await page.request.get(infoURL, { headers: { authorization: firstAuthorization } })).status()).toBe(403);
    expect((await page.request.get(infoURL, { headers: { authorization: secondAuthorization } })).status()).toBe(403);

    const currentLogin = await page.request.post(loginURL, { data: { email, password: changedPassword } });
    expect(currentLogin.status()).toBe(200);
    const currentAuthorization = readStringProperty(readProperty(await currentLogin.json() as unknown, "data"), "auth_data");
    if (!currentAuthorization) throw new Error("legacy current bearer credential is missing");
    const beforeSecond = await page.request.get(activeSessionsURL, { headers: { authorization: currentAuthorization } });
    expect(beforeSecond.status()).toBe(200);
    const beforeSecondSessions = readProperty(await beforeSecond.json() as unknown, "data");
    expect(Array.isArray(beforeSecondSessions)).toBe(true);
    if (!Array.isArray(beforeSecondSessions)) throw new Error("legacy active sessions are not an array");
    const beforeIDs = new Set(beforeSecondSessions.map((session: unknown) => Number(readProperty(session, "id"))));

    const removableLogin = await page.request.post(loginURL, { data: { email, password: changedPassword } });
    expect(removableLogin.status()).toBe(200);
    const removableAuthorization = readStringProperty(readProperty(await removableLogin.json() as unknown, "data"), "auth_data");
    if (!removableAuthorization) throw new Error("legacy removable bearer credential is missing");
    const afterSecond = await page.request.get(activeSessionsURL, { headers: { authorization: currentAuthorization } });
    expect(afterSecond.status()).toBe(200);
    const afterSecondSessions = readProperty(await afterSecond.json() as unknown, "data");
    expect(Array.isArray(afterSecondSessions)).toBe(true);
    if (!Array.isArray(afterSecondSessions)) throw new Error("legacy active sessions are not an array");
    const afterSecondItems: unknown[] = afterSecondSessions;
    const added: unknown[] = afterSecondItems.filter((session: unknown) => !beforeIDs.has(Number(readProperty(session, "id"))));
    expect(added).toHaveLength(1);
    const removableSession: unknown = added[0];
    const removableSessionID = Number(readProperty(removableSession, "id"));
    expect(Number.isSafeInteger(removableSessionID) && removableSessionID > 0).toBe(true);
    expect(Object.keys(readObjectProperty({ session: removableSession }, "session")).sort()).toEqual([
      "abilities", "created_at", "expires_at", "id", "last_used_at", "name", "tokenable_id", "tokenable_type", "updated_at"
    ]);
    expect(readProperty(removableSession, "expires_at")).toBeNull();
    expect(readStringProperty(removableSession, "name")).toMatch(/^[A-Za-z0-9]{20}$/);
    expect(readProperty(removableSession, "abilities")).toEqual(["*"]);

    const removed = await page.request.post(removeSessionURL, {
      headers: { authorization: currentAuthorization }, data: { session_id: removableSessionID }
    });
    expect(removed.status()).toBe(200);
    expect(readProperty(await removed.json() as unknown, "data")).toBe(true);
    expect((await page.request.get(infoURL, { headers: { authorization: removableAuthorization } })).status()).toBe(403);

    const loggedOut = await page.request.post(logoutURL, { headers: { authorization: currentAuthorization } });
    expect(loggedOut.status()).toBe(200);
    expect(readProperty(await loggedOut.json() as unknown, "data")).toBe(true);
    expect((await page.request.get(infoURL, { headers: { authorization: currentAuthorization } })).status()).toBe(403);
    expect((await page.request.post(logoutURL)).status()).toBe(403);
  } finally {
    legacyTinker(`App\\Models\\User::where("email","${email}")->delete();`);
    await page.request.post(logoutURL, { headers: { authorization: administratorAuthorization } });
  }
});

test("Go compatibility bearer lifecycle preserves the observed Xboard contract", async ({ page }) => {
  const loginURL = new URL("/api/v1/passport/auth/login", goURL).toString();
  const activeSessionsURL = new URL("/api/v1/user/getActiveSession", goURL).toString();
  const removeSessionURL = new URL("/api/v1/user/removeActiveSession", goURL).toString();
  const logoutURL = new URL("/api/v1/user/logout", goURL).toString();
  let currentAuthorization = "";
  let removableAuthorization = "";

  try {
    const currentLogin = await page.request.post(loginURL, { data: { email: goEmail, password: goPassword } });
    expect(currentLogin.status()).toBe(200);
    const currentData = readObjectProperty(await currentLogin.json() as unknown, "data");
    expect(Object.keys(currentData).sort()).toEqual(["auth_data", "is_admin", "is_distributor", "token"]);
    currentAuthorization = readStringProperty(currentData, "auth_data") ?? "";
    expect(currentAuthorization).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);
    expect(readProperty(currentData, "is_admin")).toBe(true);
    expect(readProperty(currentData, "is_distributor")).toBe(false);

    const beforeSecond = await page.request.get(activeSessionsURL, { headers: { authorization: currentAuthorization } });
    expect(beforeSecond.status()).toBe(200);
    const beforeSessions = readProperty(await beforeSecond.json() as unknown, "data");
    expect(Array.isArray(beforeSessions)).toBe(true);
    if (!Array.isArray(beforeSessions)) throw new Error("Go compatibility sessions are not an array");
    const beforeIDs = new Set(beforeSessions.map((session: unknown) => Number(readProperty(session, "id"))));

    const removableLogin = await page.request.post(loginURL, { data: { email: goEmail, password: goPassword } });
    expect(removableLogin.status()).toBe(200);
    removableAuthorization = readStringProperty(readProperty(await removableLogin.json() as unknown, "data"), "auth_data") ?? "";
    expect(removableAuthorization).toMatch(/^Bearer [A-Za-z0-9_-]{48}$/);

    const afterSecond = await page.request.get(activeSessionsURL, { headers: { authorization: currentAuthorization } });
    expect(afterSecond.status()).toBe(200);
    const afterSessions = readProperty(await afterSecond.json() as unknown, "data");
    expect(Array.isArray(afterSessions)).toBe(true);
    if (!Array.isArray(afterSessions)) throw new Error("Go compatibility sessions are not an array");
    const afterItems: unknown[] = afterSessions;
    const added: unknown[] = afterItems.filter((session: unknown) => !beforeIDs.has(Number(readProperty(session, "id"))));
    expect(added).toHaveLength(1);
    const removable: unknown = added[0];
    const removableID = Number(readProperty(removable, "id"));
    expect(Object.keys(readObjectProperty({ session: removable }, "session")).sort()).toEqual([
      "abilities", "created_at", "expires_at", "id", "last_used_at", "name", "tokenable_id", "tokenable_type", "updated_at"
    ]);
    expect(readProperty(removable, "expires_at")).toBeNull();
    expect(readStringProperty(removable, "name")).toMatch(/^[a-f0-9]{20}$/);
    expect(readProperty(removable, "abilities")).toEqual(["*"]);

    const removed = await page.request.post(removeSessionURL, {
      headers: { authorization: currentAuthorization }, data: { session_id: removableID }
    });
    expect(removed.status()).toBe(200);
    expect(readProperty(await removed.json() as unknown, "data")).toBe(true);
    expect((await page.request.get(new URL("/api/v1/auth/session", goURL).toString(), { headers: { authorization: removableAuthorization } })).status()).toBe(401);
    removableAuthorization = "";

    const loggedOut = await page.request.post(logoutURL, { headers: { authorization: currentAuthorization } });
    expect(loggedOut.status()).toBe(200);
    expect(readProperty(await loggedOut.json() as unknown, "data")).toBe(true);
    expect((await page.request.get(new URL("/api/v1/auth/session", goURL).toString(), { headers: { authorization: currentAuthorization } })).status()).toBe(401);
    currentAuthorization = "";
  } finally {
    if (removableAuthorization !== "") await page.request.post(logoutURL, { headers: { authorization: removableAuthorization } });
    if (currentAuthorization !== "") await page.request.post(logoutURL, { headers: { authorization: currentAuthorization } });
  }
});

test("legacy quick and mail links issue short-lived one-time login tokens", async ({ page }) => {
  test.setTimeout(90_000);
  const originalSettingOutput = legacyTinker('$row=App\\Models\\Setting::where("name","login_with_mail_link_enable")->first(); dump(base64_encode(json_encode(["exists"=>(bool)$row,"value"=>$row?->value])));');
  const encodedSetting = originalSettingOutput.match(/"([A-Za-z0-9+/=]+)"/)?.[1];
  if (!encodedSetting) throw new Error("legacy mail-link setting snapshot is missing");
  const originalSetting = JSON.parse(Buffer.from(encodedSetting, "base64").toString("utf8")) as { exists: boolean; value: unknown };
  const unknownEmail = `unknown-mail-link-${Date.now()}@legacy.local`;
  const originalTempKeys = new Set(legacyRedisKeys("*TEMP_TOKEN_*"));
  const issuedKeys: string[] = [];

  const login = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
    data: { email: legacyEmail, password: legacyPassword }
  });
  expect(login.status()).toBe(200);
  const authorization = readStringProperty(readProperty(await login.json() as unknown, "data"), "auth_data");
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy passwordless-login source session is missing");

  try {
    const unauthorized = await page.request.post(new URL("/api/v1/passport/auth/getQuickLoginUrl", legacyURL).toString(), {
      data: { redirect: "invite" }
    });
    expect(unauthorized.status()).toBe(401);

    const quick = await page.request.post(new URL("/api/v1/passport/auth/getQuickLoginUrl", legacyURL).toString(), {
      data: { auth_data: authorization, redirect: "invite" }
    });
    expect(quick.status()).toBe(200);
    const quickURL = readStringProperty(await quick.json() as unknown, "data");
    expect(quickURL).toBeTruthy();
    if (!quickURL) throw new Error("legacy quick-login URL is missing");
    const quickParams = hashSearchParams(quickURL);
    const quickToken = quickParams.get("verify");
    expect(quickToken).toMatch(/^[a-f0-9]{32}$/);
    expect(quickParams.get("redirect")).toBe("invite");
    if (!quickToken) throw new Error("legacy quick-login token is missing");
    const quickKey = legacyRedisKeys(`*TEMP_TOKEN_${quickToken}`);
    expect(quickKey).toHaveLength(1);
    issuedKeys.push(...quickKey);
    expect(legacyRedisTTL(quickKey[0])).toBeGreaterThan(0);
    expect(legacyRedisTTL(quickKey[0])).toBeLessThanOrEqual(60);

    const quickLoginResponse = page.waitForResponse((response) => response.url().includes(`/passport/auth/token2Login?verify=${quickToken}`));
    await page.goto(quickURL, { waitUntil: "domcontentloaded" });
    expect((await quickLoginResponse).status()).toBe(200);
    await expect(page).toHaveURL(/#\/invite$/);
    await expect(page.getByText("邀请码管理", { exact: true })).toBeVisible();
    expect(legacyRedisKeys(`*TEMP_TOKEN_${quickToken}`)).toEqual([]);

    const reused = await page.request.get(new URL(`/api/v1/passport/auth/token2Login?verify=${quickToken}&redirect=invite`, legacyURL).toString());
    expect(reused.status()).toBe(400);

    const redirected = await page.request.post(new URL("/api/v1/user/getQuickLoginUrl", legacyURL).toString(), {
      headers: { authorization }, data: { redirect: "knowledge" }
    });
    expect(redirected.status()).toBe(200);
    const redirectURL = readStringProperty(await redirected.json() as unknown, "data");
    if (!redirectURL) throw new Error("legacy authenticated quick-login URL is missing");
    const redirectToken = hashSearchParams(redirectURL).get("verify");
    expect(redirectToken).toMatch(/^[a-f0-9]{32}$/);
    if (!redirectToken) throw new Error("legacy redirect token is missing");
    const redirectKeys = legacyRedisKeys(`*TEMP_TOKEN_${redirectToken}`);
    issuedKeys.push(...redirectKeys);
    const tokenRedirect = await page.request.get(new URL(`/api/v1/passport/auth/token2Login?token=${redirectToken}&redirect=knowledge`, legacyURL).toString(), {
      maxRedirects: 0
    });
    expect(tokenRedirect.status()).toBe(302);
    const location = tokenRedirect.headers().location;
    expect(location).toContain(`/#/login?verify=${redirectToken}&redirect=knowledge`);
    expect(legacyRedisKeys(`*TEMP_TOKEN_${redirectToken}`)).toHaveLength(1);

    legacyTinker('admin_setting(["login_with_mail_link_enable"=>1]);');
    clearLegacyMailLinkCooldown(legacyEmail);
    clearLegacyMailLinkCooldown(unknownEmail);
    const beforeMailKeys = new Set(legacyRedisKeys("*TEMP_TOKEN_*"));
    const mailLink = await page.request.post(new URL("/api/v1/passport/auth/loginWithMailLink", legacyURL).toString(), {
      data: { email: legacyEmail, redirect: "dashboard" }
    });
    expect(mailLink.status()).toBe(200);
    expect(readProperty(await mailLink.json() as unknown, "data")).toBe(true);
    const mailKeys = legacyRedisKeys("*TEMP_TOKEN_*").filter((key) => !beforeMailKeys.has(key));
    expect(mailKeys).toHaveLength(1);
    issuedKeys.push(...mailKeys);
    expect(legacyRedisTTL(mailKeys[0])).toBeGreaterThan(0);
    expect(legacyRedisTTL(mailKeys[0])).toBeLessThanOrEqual(300);

    const repeatedMailLink = await page.request.post(new URL("/api/v1/passport/auth/loginWithMailLink", legacyURL).toString(), {
      data: { email: legacyEmail }
    });
    expect(repeatedMailLink.status()).toBe(429);
    expect(readStringProperty(await repeatedMailLink.json() as unknown, "message")).toBe("发送频繁，请稍后再试");

    const beforeUnknownKeys = legacyRedisKeys("*TEMP_TOKEN_*");
    const unknown = await page.request.post(new URL("/api/v1/passport/auth/loginWithMailLink", legacyURL).toString(), {
      data: { email: unknownEmail }
    });
    expect(unknown.status()).toBe(200);
    expect(readProperty(await unknown.json() as unknown, "data")).toBe(true);
    expect(legacyRedisKeys("*TEMP_TOKEN_*")).toEqual(beforeUnknownKeys);

    const mailToken = mailKeys[0].slice(mailKeys[0].lastIndexOf("TEMP_TOKEN_") + "TEMP_TOKEN_".length);
    expect(mailToken).toMatch(/^[a-f0-9]{32}$/);
    const exchanged = await page.request.get(new URL(`/api/v1/passport/auth/token2Login?verify=${mailToken}&redirect=dashboard`, legacyURL).toString());
    expect(exchanged.status()).toBe(200);
    expect(readStringProperty(readProperty(await exchanged.json() as unknown, "data"), "auth_data")).toBeTruthy();
    expect(legacyRedisKeys(`*TEMP_TOKEN_${mailToken}`)).toEqual([]);

    legacyTinker('admin_setting(["login_with_mail_link_enable"=>0]);');
    const disabled = await page.request.post(new URL("/api/v1/passport/auth/loginWithMailLink", legacyURL).toString(), {
      data: { email: legacyEmail }
    });
    expect(disabled.status()).toBe(404);
  } finally {
    clearLegacyMailLinkCooldown(legacyEmail);
    clearLegacyMailLinkCooldown(unknownEmail);
    for (const key of issuedKeys) legacyRedisDelete(key);
    for (const key of legacyRedisKeys("*TEMP_TOKEN_*")) {
      if (!originalTempKeys.has(key)) legacyRedisDelete(key);
    }
    if (originalSetting.exists) {
      const encodedValue = Buffer.from(JSON.stringify(originalSetting.value), "utf8").toString("base64");
      legacyTinker(`admin_setting(["login_with_mail_link_enable"=>json_decode(base64_decode("${encodedValue}"),true)]);`);
    } else {
      legacyTinker('app(App\\Support\\Setting::class)->remove("login_with_mail_link_enable");');
    }
  }
});

test("legacy site identity settings persist and feed the public guest contract", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const originalResponse = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers });
  expect(originalResponse.status()).toBe(200);
  const originalSite = readObjectProperty(readProperty(await originalResponse.json() as unknown, "data"), "site");
  const original = {
    app_name: readProperty(originalSite, "app_name"),
    app_description: readProperty(originalSite, "app_description"),
    app_url: readProperty(originalSite, "app_url"),
    tos_url: readProperty(originalSite, "tos_url")
  };
  const unique = Date.now();
  const changed = {
    app_name: `Xboard parity ${unique}`,
    app_description: `Observable legacy description ${unique}`,
    app_url: `https://legacy-site-${unique}.example.test/`,
    tos_url: `https://legacy-site-${unique}.example.test/terms/`
  };

  try {
    const saved = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: changed });
    expect(saved.status()).toBe(200);

    const fetched = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers });
    expect(fetched.status()).toBe(200);
    const site = readObjectProperty(readProperty(await fetched.json() as unknown, "data"), "site");
    expect(readProperty(site, "app_name")).toBe(changed.app_name);
    expect(readProperty(site, "app_description")).toBe(changed.app_description);
    expect(readProperty(site, "app_url")).toBe(changed.app_url);
    expect(readProperty(site, "tos_url")).toBe(changed.tos_url);

    const guest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(guest.status()).toBe(200);
    const publicConfig = readProperty(await guest.json() as unknown, "data");
    expect(readProperty(publicConfig, "app_description")).toBe(changed.app_description);
    expect(readProperty(publicConfig, "app_url")).toBe(changed.app_url);
    expect(readProperty(publicConfig, "tos_url")).toBe(changed.tos_url);
  } finally {
    const restored = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    expect(restored.status()).toBe(200);
    const verification = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers });
    expect(verification.status()).toBe(200);
    const restoredSite = readObjectProperty(readProperty(await verification.json() as unknown, "data"), "site");
    for (const [key, value] of Object.entries(original)) expect(readProperty(restoredSite, key)).toBe(value);
  }
});

test("legacy logo and site name propagate to the administrator shell and public knowledge", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const headers = { authorization };
  const originalResponse = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers });
  expect(originalResponse.status()).toBe(200);
  const originalSite = readObjectProperty(readProperty(await originalResponse.json() as unknown, "data"), "site");
  const original = { app_name: readProperty(originalSite, "app_name"), logo: readProperty(originalSite, "logo") };
  const unique = Date.now();
  const changed = { app_name: `Logo parity ${unique}`, logo: new URL("/favicon.ico", legacyURL).toString() };
  const knowledgeTitle = `Logo guide ${unique}`;
  const userEmailPrefix = `brand-parity-${unique}`;
  const userEmail = `${userEmailPrefix}@legacy.local`;
  const userPassword = crypto.randomUUID();
  let knowledgeID: number | null = null;

  try {
    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers,
      data: { email_prefix: userEmailPrefix, email_suffix: "legacy.local", password: userPassword }
    });
    expect(generated.status()).toBe(200);

    const saved = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: changed });
    expect(saved.status()).toBe(200);
    const guest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
    expect(guest.status()).toBe(200);
    expect(readProperty(readProperty(await guest.json() as unknown, "data"), "logo")).toBe(changed.logo);

    await page.goto(legacyURL);
    const runtime: unknown = await page.evaluate(() => (window as typeof window & { settings?: unknown }).settings);
    expect(readStringProperty(runtime, "title")).toBe(changed.app_name);
    expect(readStringProperty(runtime, "logo")).toBe(changed.logo);

    const browser = page.context().browser();
    if (browser === null) throw new Error("legacy browser is unavailable");
    const publicContext = await browser.newContext({ locale: "zh-CN" });
    try {
      const loginPage = await publicContext.newPage();
      await loginPage.goto(new URL("/", legacyURL).toString());
      await expect(loginPage.locator(`img[src="${changed.logo}"]`).first()).toBeVisible();
      await loginLegacyUser(loginPage, userEmail, userPassword);
      await expect(loginPage.locator(`img[src="${changed.logo}"]`).first()).toBeVisible();
    } finally {
      await publicContext.close();
    }

    const knowledgeSaved = await page.request.post(legacyAdminAPI("/knowledge/save"), {
      headers,
      data: { title: knowledgeTitle, category: "Brand parity", language: "zh-CN", show: true, body: "# Public brand" }
    });
    expect(knowledgeSaved.status()).toBe(200);
    const list = await page.request.get(legacyAdminAPI("/knowledge/fetch"), { headers });
    const items = readProperty(await list.json() as unknown, "data");
    expect(Array.isArray(items)).toBe(true);
    if (!Array.isArray(items)) throw new Error("legacy administrator knowledge list is not an array");
    const knowledgeItems = items as unknown[];
    const created = knowledgeItems.find((item: unknown) => readStringProperty(item, "title") === knowledgeTitle);
    const rawID = readProperty(created, "id");
    knowledgeID = typeof rawID === "number" ? rawID : Number(rawID);
    expect(Number.isSafeInteger(knowledgeID) && knowledgeID > 0).toBe(true);

    const detail = await page.request.get(legacyAdminAPI(`/knowledge/fetch?id=${knowledgeID}`), { headers });
    const shareURL = readStringProperty(readProperty(await detail.json() as unknown, "data"), "share_url");
    expect(shareURL).not.toBeNull();
    if (!shareURL) throw new Error("legacy knowledge share URL is missing");
    const publicPage = await page.request.get(new URL(new URL(shareURL).pathname, legacyURL).toString());
    const publicHTML = await publicPage.text();
    expect(publicPage.status()).toBe(200);
    expect(publicHTML).toContain(`<title>${knowledgeTitle} - ${changed.app_name}</title>`);
    expect(publicHTML).toContain(`<img src="${changed.logo}" alt="">`);
    expect(publicHTML).toContain(`aria-label="${changed.app_name}"`);
    const content = await page.request.get(new URL(`/guide/${knowledgeID}/content`, legacyURL).toString());
    expect(content.status()).toBe(200);
    expect(readProperty(await content.json() as unknown, "page_title")).toBe(`${knowledgeTitle} - ${changed.app_name}`);
  } finally {
    try {
      if (knowledgeID !== null && Number.isSafeInteger(knowledgeID)) {
        const removed = await page.request.post(legacyAdminAPI("/knowledge/drop"), { headers, data: { id: knowledgeID } });
        expect(removed.status()).toBe(200);
      }
      const restored = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
      expect(restored.status()).toBe(200);
      const verification = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers });
      const restoredSite = readObjectProperty(readProperty(await verification.json() as unknown, "data"), "site");
      expect(readProperty(restoredSite, "app_name")).toBe(original.app_name);
      expect(readProperty(restoredSite, "logo")).toBe(original.logo);
    } finally {
      removeLegacyUser(userEmail);
    }
  }
});

test("legacy dashboard exposes scheduler, queue, failed-job, and audit contracts", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();

  await page.locator('a[href="#/"]').click();
  await expect(page.getByText("队列状态", { exact: true }).filter({ visible: true })).toBeVisible();
  await expect(page.getByText("运行状态", { exact: true }).filter({ visible: true })).toBeVisible();

  const headers = { authorization };
  const [statusResponse, statsResponse, workloadResponse, failedResponse, auditResponse] = await Promise.all([
    page.request.get(legacyAdminAPI("/system/getSystemStatus"), { headers }),
    page.request.get(legacyAdminAPI("/system/getQueueStats"), { headers }),
    page.request.get(legacyAdminAPI("/system/getQueueWorkload"), { headers }),
    page.request.get(legacyAdminAPI("/system/getHorizonFailedJobs?current=1&page_size=20"), { headers }),
    page.request.get(legacyAdminAPI("/system/getAuditLog?current=1&page_size=10"), { headers })
  ]);
  for (const response of [statusResponse, statsResponse, workloadResponse, failedResponse, auditResponse]) {
    expect(response.status()).toBe(200);
  }
  const status = readObjectProperty(await statusResponse.json() as unknown, "data");
  expect(Object.keys(status)).toEqual(expect.arrayContaining(["schedule", "horizon", "schedule_last_runtime"]));
  const stats = readObjectProperty(await statsResponse.json() as unknown, "data");
  expect(Object.keys(stats)).toEqual(expect.arrayContaining([
    "failedJobs", "jobsPerMinute", "processes", "recentJobs", "status", "wait"
  ]));
  const workload = readProperty(await workloadResponse.json() as unknown, "data");
  expect(Array.isArray(workload)).toBe(true);
  const failed = await failedResponse.json() as unknown;
  expect(Array.isArray(readProperty(failed, "data"))).toBe(true);
  expect(typeof readProperty(failed, "total")).toBe("number");
  const audit = await auditResponse.json() as unknown;
  expect(Array.isArray(readProperty(audit, "data"))).toBe(true);
  expect(typeof readProperty(audit, "total")).toBe("number");
  expect(errors).toEqual([]);
});

test("legacy user ticket surface remains observable without frontend source", async ({ page }) => {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const unique = Date.now();
  const userEmailPrefix = `ticket-ui-${unique}`;
  const userEmail = `${userEmailPrefix}@legacy.local`;
  const userPassword = crypto.randomUUID();
  const browser = page.context().browser();
  if (browser === null) throw new Error("legacy browser is unavailable");
  const userContext = await browser.newContext({ locale: "zh-CN" });
  try {
    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization },
      data: { email_prefix: userEmailPrefix, email_suffix: "legacy.local", password: userPassword }
    });
    expect(generated.status()).toBe(200);

    const userPage = await userContext.newPage();
    const errors = watchErrors(userPage);
    await loginLegacyUser(userPage, userEmail, userPassword);
    const response = userPage.waitForResponse((item) => item.url().includes("/api/v1/user/ticket/fetch"));
    await userPage.getByText("我的工单", { exact: true }).click();
    expect((await response).status()).toBe(200);
    await expect(userPage.getByText("工单历史", { exact: true })).toBeVisible();
    await expect(userPage.getByRole("button", { name: "新的工单" })).toBeVisible();
    for (const column of ["主题", "工单级别", "工单状态", "创建时间", "最后回复时间", "操作"]) {
      await expect(userPage.getByText(column, { exact: true }).first()).toBeVisible();
    }
    await userPage.getByRole("button", { name: "新的工单" }).click();
    await expect(userPage.locator('input[placeholder="请输入工单主题"]:visible')).toBeVisible();
    await expect(userPage.locator('textarea[placeholder="请描述您遇到的问题"]:visible')).toBeVisible();
    await userPage.locator(".n-base-selection:visible").click();
    for (const level of ["低", "中", "高"]) await expect(userPage.getByText(level, { exact: true }).last()).toBeVisible();
    await userPage.getByRole("button", { name: "取消" }).click();
    expect(errors).toEqual([]);
  } finally {
    await userContext.close();
    removeLegacyUser(userEmail);
  }
});

test("legacy ticket API preserves role, ownership, close, and reply state semantics", async ({ page }) => {
  await loginLegacy(page);
  const ticketResponse = page.waitForResponse((response) => response.url().includes("/ticket/fetch"));
  await page.locator('a[href="#/user/ticket"]').click();
  const authorization = (await ticketResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const unique = Date.now();
  const email = `ticket-parity-${unique}@legacy.local`;
  const password = `ticket-parity-password-${unique}`;
  const subject = `Ticket parity ${unique}`;
  try {
    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization },
      data: { email_prefix: `ticket-parity-${unique}`, email_suffix: "legacy.local", password }
    });
    expect(generated.status()).toBe(200);

    const login = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), {
      data: { email, password }
    });
    expect(login.status()).toBe(200);
    const userAuthorization = readStringProperty(readProperty(await login.json() as unknown, "data"), "auth_data");
    expect(userAuthorization).not.toBeNull();
    if (!userAuthorization) throw new Error("legacy user authorization is missing");
    const userHeaders = { authorization: userAuthorization };

    const created = await page.request.post(new URL("/api/v1/user/ticket/save", legacyURL).toString(), {
      headers: userHeaders, data: { subject, level: 2, message: "Initial user message" }
    });
    expect(created.status()).toBe(200);
    const list = await page.request.get(new URL("/api/v1/user/ticket/fetch", legacyURL).toString(), { headers: userHeaders });
    const tickets = readProperty(await list.json() as unknown, "data");
    expect(Array.isArray(tickets)).toBe(true);
    if (!Array.isArray(tickets)) throw new Error("legacy user ticket list is not an array");
    const ticketItems: unknown[] = tickets;
    const item = ticketItems.find((value: unknown) => readStringProperty(value, "subject") === subject);
    const rawID = readProperty(item, "id");
    const ticketID = typeof rawID === "number" ? rawID : Number(rawID);
    expect(Number.isSafeInteger(ticketID) && ticketID > 0).toBe(true);
    expect(readProperty(item, "status")).toBe(0);
    expect(readProperty(item, "reply_status")).toBe(0);

    const userReply = await page.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: userHeaders, data: { id: ticketID, message: "User follow-up" }
    });
    expect(userReply.status()).toBe(200);
    const close = await page.request.post(new URL("/api/v1/user/ticket/close", legacyURL).toString(), {
      headers: userHeaders, data: { id: ticketID }
    });
    expect(close.status()).toBe(200);
    const rejected = await page.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: userHeaders, data: { id: ticketID, message: "Rejected after close" }
    });
    expect(rejected.status()).toBe(400);
    expect(readStringProperty(await rejected.json() as unknown, "message")).toBe("工单已关闭，无法回复");

    const adminReply = await page.request.post(legacyAdminAPI("/ticket/reply"), {
      headers: { authorization }, data: { id: ticketID, message: "Administrator answer after close" }
    });
    expect(adminReply.status()).toBe(200);
    const detail = await page.request.get(new URL(`/api/v1/user/ticket/fetch?id=${ticketID}`, legacyURL).toString(), { headers: userHeaders });
    const ticket = readProperty(await detail.json() as unknown, "data");
    expect(readProperty(ticket, "status")).toBe(1);
    expect(readProperty(ticket, "reply_status")).toBe(1);
    const messages = readProperty(ticket, "message");
    expect(Array.isArray(messages)).toBe(true);
    if (!Array.isArray(messages)) throw new Error("legacy ticket detail messages are not an array");
    const ticketMessages: unknown[] = messages;
    expect(ticketMessages.map((message: unknown) => readProperty(message, "is_me"))).toEqual([true, true, false]);

    const next = await page.request.post(new URL("/api/v1/user/ticket/save", legacyURL).toString(), {
      headers: userHeaders, data: { subject: `${subject} second`, level: 0, message: "Allowed after close" }
    });
    expect(next.status()).toBe(200);
  } finally {
    const cleanup = `$u=App\\Models\\User::where("email","${email}")->first(); if($u){$ids=App\\Models\\Ticket::where("user_id",$u->id)->pluck("id"); App\\Models\\TicketMessage::whereIn("ticket_id",$ids)->delete(); App\\Models\\Ticket::whereIn("id",$ids)->delete(); $u->delete();}`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
  }
});

test("legacy ticket wait setting blocks consecutive user replies until an administrator answers", async ({ page }) => {
  await loginLegacy(page);
  const ticketResponse = page.waitForResponse((response) => response.url().includes("/ticket/fetch"));
  await page.locator('a[href="#/user/ticket"]').click();
  const authorization = (await ticketResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const fetchedSettings = await page.request.get(legacyAdminAPI("/config/fetch?key=site"), { headers: { authorization } });
  expect(fetchedSettings.status()).toBe(200);
  const site = readProperty(readProperty(await fetchedSettings.json() as unknown, "data"), "site");
  const originalWaitSetting = Boolean(readProperty(site, "ticket_must_wait_reply"));
  const unique = Date.now();
  const email = `ticket-wait-${unique}@legacy.local`;
  const password = `ticket-wait-password-${unique}`;
  const subject = `Ticket wait ${unique}`;
  try {
    const enabled = await page.request.post(legacyAdminAPI("/config/save"), {
      headers: { authorization }, data: { ticket_must_wait_reply: 1 }
    });
    expect(enabled.status()).toBe(200);

    const generated = await page.request.post(legacyAdminAPI("/user/generate"), {
      headers: { authorization }, data: { email_prefix: `ticket-wait-${unique}`, email_suffix: "legacy.local", password }
    });
    expect(generated.status()).toBe(200);
    const login = await page.request.post(new URL("/api/v1/passport/auth/login", legacyURL).toString(), { data: { email, password } });
    expect(login.status()).toBe(200);
    const userAuthorization = readStringProperty(readProperty(await login.json() as unknown, "data"), "auth_data");
    if (!userAuthorization) throw new Error("legacy user authorization is missing");
    const userHeaders = { authorization: userAuthorization };

    const created = await page.request.post(new URL("/api/v1/user/ticket/save", legacyURL).toString(), {
      headers: userHeaders, data: { subject, level: 0, message: "Initial wait-policy message" }
    });
    expect(created.status()).toBe(200);
    const list = await page.request.get(new URL("/api/v1/user/ticket/fetch", legacyURL).toString(), { headers: userHeaders });
    const tickets = readProperty(await list.json() as unknown, "data");
    if (!Array.isArray(tickets)) throw new Error("legacy user ticket list is not an array");
    const ticketItems: unknown[] = tickets;
    const item = ticketItems.find((value: unknown) => readStringProperty(value, "subject") === subject);
    const ticketID = Number(readProperty(item, "id"));
    expect(Number.isSafeInteger(ticketID) && ticketID > 0).toBe(true);

    const blocked = await page.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: userHeaders, data: { id: ticketID, message: "Consecutive user reply" }
    });
    expect(blocked.status()).toBe(400);
    expect(readStringProperty(await blocked.json() as unknown, "message")).toBe("请等待技术支持回复");

    const administratorReply = await page.request.post(legacyAdminAPI("/ticket/reply"), {
      headers: { authorization }, data: { id: ticketID, message: "Administrator unlock reply" }
    });
    expect(administratorReply.status()).toBe(200);
    const allowed = await page.request.post(new URL("/api/v1/user/ticket/reply", legacyURL).toString(), {
      headers: userHeaders, data: { id: ticketID, message: "Allowed after administrator reply" }
    });
    expect(allowed.status()).toBe(200);
  } finally {
    await page.request.post(legacyAdminAPI("/config/save"), {
      headers: { authorization }, data: { ticket_must_wait_reply: originalWaitSetting ? 1 : 0 }
    });
    const cleanup = `$u=App\\Models\\User::where("email","${email}")->first(); if($u){$ids=App\\Models\\Ticket::where("user_id",$u->id)->pluck("id"); App\\Models\\TicketMessage::whereIn("ticket_id",$ids)->delete(); App\\Models\\Ticket::whereIn("id",$ids)->delete(); $u->delete();}`;
    execFileSync("docker", ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${cleanup}`], { stdio: "pipe" });
  }
});

test("legacy knowledge runtime preserves visibility, placeholders, access markers, and public sharing", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacy(page);
  const knowledgeResponse = page.waitForResponse((response) => response.url().includes("/knowledge/fetch"));
  await page.locator('a[href="#/config/knowledge"]').click();
  const authorization = (await knowledgeResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");

  const unique = Date.now();
  const title = `Parity guide ${unique}`;
  const privateText = `PARITY-PRIVATE-${unique}`;
  const category = `Parity ${unique}`;
  let knowledgeID: number | null = null;
  try {
    const saved = await page.request.post(legacyAdminAPI("/knowledge/save"), {
      headers: { authorization },
      data: {
        title, category, language: "zh-CN", show: true,
        body: `# {{siteName}}\n\n{{subscribeUrl}}\n\n<!--access start-->${privateText}<!--access end-->`
      }
    });
    expect(saved.status()).toBe(200);

    const adminList = await page.request.get(legacyAdminAPI("/knowledge/fetch"), { headers: { authorization } });
    expect(adminList.status()).toBe(200);
    const adminItems = readProperty(await adminList.json() as unknown, "data");
    expect(Array.isArray(adminItems)).toBe(true);
    if (!Array.isArray(adminItems)) throw new Error("legacy administrator knowledge list is not an array");
    const adminEntries: unknown[] = adminItems;
    const created = adminEntries.find((item: unknown) => readStringProperty(item, "title") === title);
    const rawID = readProperty(created, "id");
    knowledgeID = typeof rawID === "number" ? rawID : Number(rawID);
    expect(Number.isSafeInteger(knowledgeID) && knowledgeID > 0).toBe(true);

    const userList = await page.request.get(new URL(`/api/v1/user/knowledge/fetch?language=zh-CN&keyword=${encodeURIComponent(title)}`, legacyURL).toString(), { headers: { authorization } });
    expect(userList.status()).toBe(200);
    const grouped = readProperty(await userList.json() as unknown, "data");
    const categoryItems = readProperty(grouped, category);
    expect(Array.isArray(categoryItems)).toBe(true);
    if (!Array.isArray(categoryItems)) throw new Error("legacy user knowledge response is not grouped by category");
    const userEntries: unknown[] = categoryItems;
    const userArticle = userEntries.find((item: unknown) => readStringProperty(item, "title") === title);
    const userBody = readStringProperty(userArticle, "body");
    expect(userBody).not.toBeNull();
    expect(userBody).not.toContain("{{siteName}}");
    expect(userBody).not.toContain("{{subscribeUrl}}");
    const hasPrivateContent = userBody?.includes(privateText) === true;
    const hasNoAccessMessage = userBody?.includes('class="v2board-no-access"') === true;
    if (hasPrivateContent === hasNoAccessMessage) {
      const safeBody = (userBody ?? "").replace(/https?:\/\/[^\s<>"']+/gi, "[URL]").replace(/[0-9a-f]{32,}/gi, "[REDACTED]");
      throw new Error(`legacy knowledge access outcome is ambiguous: ${JSON.stringify(safeBody)}`);
    }

    const detail = await page.request.get(legacyAdminAPI(`/knowledge/fetch?id=${knowledgeID}`), { headers: { authorization } });
    const detailData = readProperty(await detail.json() as unknown, "data");
    const shareURL = readStringProperty(detailData, "share_url");
    expect(shareURL).not.toBeNull();
    if (!shareURL) throw new Error("legacy knowledge share URL is missing");
    const localShare = new URL(new URL(shareURL).pathname, legacyURL).toString();
    const publicPage = await page.request.get(localShare);
    expect(publicPage.status()).toBe(200);
    const publicHTML = await publicPage.text();
    expect(publicHTML).toContain(title);
    const publicArticleBody = publicHTML.match(/data-article-body>([\s\S]*?)<\/div>/i)?.[1] ?? "";
    expect(publicArticleBody).toContain("/#/login");
    expect(publicArticleBody).not.toContain("{{subscribeUrl}}");
    expect(publicHTML).not.toContain("/api/v1/client/subscribe?token=");

    const toggled = await page.request.post(legacyAdminAPI("/knowledge/show"), {
      headers: { authorization }, data: { id: knowledgeID }
    });
    expect(toggled.status()).toBe(200);
    const hiddenList = await page.request.get(new URL(`/api/v1/user/knowledge/fetch?language=zh-CN&keyword=${encodeURIComponent(title)}`, legacyURL).toString(), { headers: { authorization } });
    const hiddenGrouped = readProperty(await hiddenList.json() as unknown, "data");
    expect(readProperty(hiddenGrouped, category)).toBeUndefined();
  } finally {
    if (knowledgeID !== null && Number.isSafeInteger(knowledgeID)) {
      const removed = await page.request.post(legacyAdminAPI("/knowledge/drop"), {
        headers: { authorization }, data: { id: knowledgeID }
      });
      expect(removed.status()).toBe(200);
    }
  }
  expect(errors).toEqual([]);
});

test("implemented Go administrator concepts map to the legacy navigation", async ({ browser }) => {
  const legacyContext = await browser.newContext();
  const goContext = await browser.newContext();
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    for (const [legacyLabel, goLabel] of [
      ["服务器管理", "服务器管理"],
      ["节点管理", "节点管理"],
      ["用户管理", "用户管理"],
      ["权限组管理", "权限组"],
      ["路由管理", "路由规则"],
      ["公告管理", "公告管理"],
      ["知识库管理", "知识库管理"],
      ["客户端管理", "客户端管理"],
      ["工单管理", "工单管理"]
    ] as const) {
      const legacyEntry = legacyLabel === "客户端管理"
        ? legacyPage.locator(".xboard-client-catalog-nav")
        : legacyPage.getByRole("link", { name: legacyLabel, exact: true });
      await expect(legacyEntry).toBeVisible();
      await expect(goPage.getByRole("button", { name: goLabel, exact: true })).toBeVisible();
    }

    const legacyCatalogRequest = legacyPage.waitForResponse((response) => response.url().includes("/client-catalog") && !response.url().includes("/save"));
    await legacyPage.locator(".xboard-client-catalog-nav").click();
    const legacyCatalogResponse = await legacyCatalogRequest;
    const authorization = legacyCatalogResponse.request().headers().authorization;
    expect(authorization).toBeTruthy();
    const legacyUserResponse = await legacyPage.request.get(new URL("/api/v1/user/client-catalog", legacyURL).toString(), {
      headers: { authorization }
    });
    const goUserResponse = await goPage.request.get(new URL("/api/v1/client-catalog", goURL).toString());
    expect(legacyUserResponse.status()).toBe(200);
    expect(goUserResponse.status()).toBe(200);
    const legacyPayload: unknown = await legacyUserResponse.json();
    const goPayload: unknown = await goUserResponse.json();
    expect(normalizeClientCatalog(readProperty(legacyPayload, "data"))).toEqual(normalizeClientCatalog(readProperty(goPayload, "data")));
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

test("legacy subscription settings remain observable and map to Go output controls", async ({ browser }) => {
  const legacyContext = await browser.newContext({ locale: "zh-CN" });
  const goContext = await browser.newContext({ locale: "zh-CN" });
  const legacyPage = await legacyContext.newPage();
  const goPage = await goContext.newPage();
  try {
    const legacyErrors = watchErrors(legacyPage);
    const goErrors = watchErrors(goPage);
    await loginLegacy(legacyPage);
    await loginGo(goPage);

    const legacyConfigResponse = legacyPage.waitForResponse((response) => response.url().includes("/config/fetch"));
    await legacyPage.locator('a[href="#/config/system"]').click();
    const fetchedLegacyConfig = await legacyConfigResponse;
    const authorization = fetchedLegacyConfig.request().headers().authorization;
    expect(authorization).toBeTruthy();
    if (!authorization) throw new Error("legacy administrator authorization is missing");

    await legacyPage.getByRole("link", { name: "订阅设置", exact: true }).filter({ visible: true }).click();
    for (const field of [
      "允许用户更改订阅", "月流量重置方式", "开启折抵方案", "订阅路径",
      "在订阅中展示订阅信息", "在订阅中线路名称中显示协议名称"
    ]) {
      await expect(legacyPage.getByText(field, { exact: true }).filter({ visible: true }).first(), field).toBeVisible();
    }
    const legacyPayload = readProperty(await (await legacyPage.request.get(legacyAdminAPI("/config/fetch"), {
      headers: { authorization }
    })).json() as unknown, "data");
    const legacySubscribe = readObjectProperty(legacyPayload, "subscribe");
    expect(Object.keys(legacySubscribe)).toEqual(expect.arrayContaining([
      "plan_change_enable", "reset_traffic_method", "surplus_enable", "new_order_event_id",
      "renew_order_event_id", "change_order_event_id", "show_info_to_server_enable",
      "show_protocol_to_server_enable", "default_remind_expire", "default_remind_traffic", "subscribe_path"
    ]));

    await legacyPage.getByRole("link", { name: "订阅模板", exact: true }).filter({ visible: true }).click();
    for (const template of ["Sing-box", "Clash", "Clash Meta", "Stash", "Surge", "Surfboard"]) {
      await expect(legacyPage.getByRole("tab", { name: template, exact: true }).filter({ visible: true })).toBeVisible();
    }

    await goPage.getByRole("button", { name: "订阅设置", exact: true }).click();
    await expect(goPage.getByRole("heading", { name: "订阅设置" })).toBeVisible();
    await expect(goPage.getByLabel("订阅路径")).toBeVisible();
    await expect(goPage.getByRole("checkbox", { name: "在订阅中展示订阅信息" })).toBeVisible();
    await expect(goPage.getByRole("checkbox", { name: "在线路名称中显示协议名称" })).toBeVisible();
    for (const template of ["Sing-box", "Clash", "Clash Meta", "Stash", "Surge", "Surfboard"]) {
      await expect(goPage.getByRole("button", { name: template, exact: true })).toBeVisible();
    }
    const goResponse = await goAdminRequest(goPage, "/api/v1/admin/subscription-settings", "GET");
    expect(goResponse.status, goResponse.body).toBe(200);
    const goSettings = readObjectProperty(JSON.parse(goResponse.body) as unknown, "data");
    expect(typeof readProperty(goSettings, "path")).toBe("string");
    expect(typeof readProperty(goSettings, "show_info")).toBe("boolean");
    expect(typeof readProperty(goSettings, "show_protocol")).toBe("boolean");
    expect(Object.keys(readObjectProperty(goSettings, "templates")).sort()).toEqual([
      "clash", "clashmeta", "singbox", "stash", "surfboard", "surge"
    ]);
    expect(legacyErrors).toEqual([]);
    expect(goErrors).toEqual([]);
  } finally {
    await legacyContext.close();
    await goContext.close();
  }
});

async function exerciseLegacyCaptchaContract(page: Page) {
  await loginLegacy(page);
  const configResponse = page.waitForResponse((response) => response.url().includes("/config/fetch"));
  await page.locator('a[href="#/config/system"]').click();
  const authorization = (await configResponse).request().headers().authorization;
  expect(authorization).toBeTruthy();
  if (!authorization) throw new Error("legacy administrator authorization is missing");
  const headers = { authorization };
  const fetched = await page.request.get(legacyAdminAPI("/config/fetch"), { headers });
  expect(fetched.status()).toBe(200);
  const safe = readObjectProperty(readProperty(await fetched.json() as unknown, "data"), "safe");
  const original = {
    captcha_enable: readProperty(safe, "captcha_enable"), captcha_type: readProperty(safe, "captcha_type"),
    recaptcha_key: readProperty(safe, "recaptcha_key"), recaptcha_site_key: readProperty(safe, "recaptcha_site_key"),
    recaptcha_v3_secret_key: readProperty(safe, "recaptcha_v3_secret_key"), recaptcha_v3_site_key: readProperty(safe, "recaptcha_v3_site_key"),
    recaptcha_v3_score_threshold: readProperty(safe, "recaptcha_v3_score_threshold"),
    turnstile_secret_key: readProperty(safe, "turnstile_secret_key"), turnstile_site_key: readProperty(safe, "turnstile_site_key")
  };
  const providers = [
    { type: "recaptcha", siteField: "recaptcha_site_key", site: "parity-v2-site", secretField: "recaptcha_key", secret: "parity-v2-secret" },
    { type: "recaptcha-v3", siteField: "recaptcha_v3_site_key", site: "parity-v3-site", secretField: "recaptcha_v3_secret_key", secret: "parity-v3-secret" },
    { type: "turnstile", siteField: "turnstile_site_key", site: "parity-turnstile-site", secretField: "turnstile_secret_key", secret: "parity-turnstile-secret" }
  ] as const;
  try {
    for (const [index, provider] of providers.entries()) {
      const saved = await page.request.post(legacyAdminAPI("/config/save"), {
        headers, data: {
          captcha_enable: 1, captcha_type: provider.type, [provider.siteField]: provider.site,
          [provider.secretField]: provider.secret, recaptcha_v3_score_threshold: 0.7
        }
      });
      expect(saved.status()).toBe(200);
      const guest = await page.request.get(new URL("/api/v1/guest/comm/config", legacyURL).toString());
      expect(guest.status()).toBe(200);
      const config = readProperty(await guest.json() as unknown, "data");
      expect(readProperty(config, "is_captcha")).toBe(1);
      expect(readProperty(config, "is_recaptcha")).toBe(1);
      expect(readProperty(config, "captcha_type")).toBe(provider.type);
      expect(readProperty(config, provider.siteField)).toBe(provider.site);
      expect(JSON.stringify(config)).not.toContain(provider.secret);

      const unique = `${Date.now()}-${index}`;
      const registration = await page.request.post(new URL("/api/v1/passport/auth/register", legacyURL).toString(), {
        data: { email: `captcha-parity-${unique}@legacy.local`, password: `captcha-parity-password-${unique}` }
      });
      expect(registration.status()).toBe(400);
      expect(readStringProperty(await registration.json() as unknown, "message")).toBe("验证码有误");
      const emailCode = await page.request.post(new URL("/api/v1/passport/comm/sendEmailVerify", legacyURL).toString(), {
        data: { email: `captcha-code-${unique}@legacy.local` }
      });
      expect(emailCode.status()).toBe(400);
      expect(readStringProperty(await emailCode.json() as unknown, "message")).toBe("验证码有误");
    }
  } finally {
    const restored = await page.request.post(legacyAdminAPI("/config/save"), { headers, data: original });
    expect(restored.status()).toBe(200);
  }
}

async function exerciseGoCaptchaContract(page: Page) {
  await loginGo(page);
  const fetched = await goAdminRequest(page, "/api/v1/admin/site-settings", "GET");
  expect(fetched.status, fetched.body).toBe(200);
  const original = readObjectProperty(JSON.parse(fetched.body) as unknown, "data");
  expect(readProperty(original, "recaptcha_secret_configured")).toBe(false);
  expect(readProperty(original, "recaptcha_v3_secret_configured")).toBe(false);
  expect(readProperty(original, "turnstile_secret_configured")).toBe(false);
  const providers = [
    { type: "recaptcha", siteField: "recaptcha_site_key", site: "parity-v2-site", secretField: "recaptcha_secret", secret: "parity-v2-secret", configuredField: "recaptcha_secret_configured" },
    { type: "recaptcha-v3", siteField: "recaptcha_v3_site_key", site: "parity-v3-site", secretField: "recaptcha_v3_secret", secret: "parity-v3-secret", configuredField: "recaptcha_v3_secret_configured" },
    { type: "turnstile", siteField: "turnstile_site_key", site: "parity-turnstile-site", secretField: "turnstile_secret", secret: "parity-turnstile-secret", configuredField: "turnstile_secret_configured" }
  ] as const;
  try {
    for (const [index, provider] of providers.entries()) {
      const currentResponse = await goAdminRequest(page, "/api/v1/admin/site-settings", "GET");
      expect(currentResponse.status, currentResponse.body).toBe(200);
      const current = readObjectProperty(JSON.parse(currentResponse.body) as unknown, "data");
      const saved = await goAdminRequest(page, "/api/v1/admin/site-settings", "PUT", {
        revision: readProperty(current, "revision"), ...goSiteIdentityInput(current),
        captcha_enable: true, captcha_type: provider.type,
        [provider.siteField]: provider.site, [provider.secretField]: provider.secret, recaptcha_v3_score_threshold: 0.7
      });
      expect(saved.status, saved.body).toBe(200);
      expect(saved.body).not.toContain(provider.secret);
      expect(saved.body).not.toContain("_cipher");
      expect(readProperty(readObjectProperty(JSON.parse(saved.body) as unknown, "data"), provider.configuredField)).toBe(true);
      const guest = await page.request.get(new URL("/api/v1/guest/comm/config", goURL).toString());
      expect(guest.status()).toBe(200);
      const config = readProperty(await guest.json() as unknown, "data");
      expect(readProperty(config, "is_captcha")).toBe(1);
      expect(readProperty(config, "is_recaptcha")).toBe(1);
      expect(readProperty(config, "captcha_type")).toBe(provider.type);
      expect(readProperty(config, provider.siteField)).toBe(provider.site);
      expect(JSON.stringify(config)).not.toContain(provider.secret);

      const unique = `${Date.now()}-${index}`;
      for (const [path, data] of [
        ["/api/v1/passport/auth/register", { email: `captcha-parity-${unique}@go.local`, password: `captcha-parity-password-${unique}` }],
        ["/api/v1/auth/registration-email/request", { email: `captcha-register-code-${unique}@go.local` }],
        ["/api/v1/auth/password-reset/request", { email: `captcha-reset-code-${unique}@go.local` }]
      ] as const) {
        const response = await page.request.post(new URL(path, goURL).toString(), { data });
        expect(response.status()).toBe(400);
        const error = readProperty(await response.json() as unknown, "error");
        expect(readProperty(error, "code")).toBe("captcha_invalid");
        expect(readProperty(error, "message")).toBe("验证码有误");
      }
    }
  } finally {
    const currentResponse = await goAdminRequest(page, "/api/v1/admin/site-settings", "GET");
    expect(currentResponse.status, currentResponse.body).toBe(200);
    const current = readObjectProperty(JSON.parse(currentResponse.body) as unknown, "data");
    const restored = await goAdminRequest(page, "/api/v1/admin/site-settings", "PUT", {
      revision: readProperty(current, "revision"), ...goSiteIdentityInput(current),
      captcha_enable: readProperty(original, "captcha_enable"),
      captcha_type: readProperty(original, "captcha_type"), recaptcha_site_key: readProperty(original, "recaptcha_site_key"),
      recaptcha_v3_site_key: readProperty(original, "recaptcha_v3_site_key"),
      recaptcha_v3_score_threshold: readProperty(original, "recaptcha_v3_score_threshold"),
      turnstile_site_key: readProperty(original, "turnstile_site_key"), clear_recaptcha_secret: true,
      clear_recaptcha_v3_secret: true, clear_turnstile_secret: true
    });
    expect(restored.status, restored.body).toBe(200);
  }
}

function goSiteIdentityInput(settings: Record<string, unknown>) {
  return {
    app_name: readProperty(settings, "app_name"), app_description: readProperty(settings, "app_description"),
    app_url: readProperty(settings, "app_url"), tos_url: readProperty(settings, "tos_url"), logo: readProperty(settings, "logo")
  };
}

async function loginLegacy(page: Page) {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await expect(fields).toHaveCount(2);
  await fields.first().fill(legacyEmail);
  await fields.nth(1).fill(legacyPassword);
  await fields.nth(1).press("Enter");
  await expect(page.locator('a[href="#/server/machine"]')).toBeVisible();
}

async function loginGo(page: Page) {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function goAdminRequest(page: Page, path: string, method: string, body?: unknown) {
  return page.evaluate(async ({ path: requestPath, method: requestMethod, body: requestBody }) => {
    const encoded = document.cookie.split("; ").find((item) => item.startsWith("xboard_csrf="))?.slice("xboard_csrf=".length) ?? "";
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

async function loginLegacyUser(page: Page, email: string, password: string) {
  expect(email.length, "legacy user login email input limit").toBeLessThanOrEqual(40);
  expect(password.length, "legacy user login password input limit").toBeLessThanOrEqual(41);
  await page.goto(new URL("/", legacyURL).toString(), { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await fields.first().fill(email);
  await fields.nth(1).fill(password);
  const responsePromise = page.waitForResponse((response) => (
    response.url().includes("/api/v1/passport/auth/login") && response.request().method() === "POST"
  ));
  await fields.nth(1).press("Enter");
  const response = await responsePromise;
  expect(response.status(), await response.text()).toBe(200);
  await expect(page.getByText("我的工单", { exact: true })).toBeVisible();
}

function removeLegacyUser(email: string): void {
  const encodedEmail = Buffer.from(email, "utf8").toString("base64");
  legacyTinker(`$email=base64_decode("${encodedEmail}"); App\\Models\\User::where("email",$email)->delete();`);
}

function watchErrors(page: Page) {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) errors.push(`${response.status()} ${response.url()}`);
  });
  return errors;
}

function requiredEnv(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required for legacy parity tests`);
  return value;
}

function legacyAdminAPI(path: string) {
  const securePath = new URL(legacyURL).pathname.replace(/\/$/, "");
  return new URL(`/api/v2${securePath}${path}`, legacyURL).toString();
}

function readLegacyPasswordResetCode(email: string): string {
  const keys = legacyRedisKeys(`*EMAIL_VERIFY_CODE_${email}`);
  expect(keys).toHaveLength(1);
  if (keys.length !== 1) return "";
  return execFileSync("docker", [
    "exec", legacyDockerContainer, "redis-cli", "-s", "/data/redis.sock", "-n", "1", "--raw", "GET", keys[0]
  ], { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" }).trim();
}

function clearLegacyPasswordResetCache(email: string): void {
  for (const marker of ["EMAIL_VERIFY_CODE", "LAST_SEND_EMAIL_VERIFY_TIMESTAMP", "FORGET_REQUEST_LIMIT"]) {
    for (const key of legacyRedisKeys(`*${marker}_${email}`)) {
      execFileSync("docker", [
        "exec", legacyDockerContainer, "redis-cli", "-s", "/data/redis.sock", "-n", "1", "DEL", key
      ], { stdio: "ignore" });
    }
  }
}

function clearLegacyPasswordErrorLimit(email: string): void {
  const encodedEmail = Buffer.from(email, "utf8").toString("base64");
  legacyTinker(`$email=base64_decode("${encodedEmail}"); Illuminate\\Support\\Facades\\Cache::forget(App\\Utils\\CacheKey::get("PASSWORD_ERROR_LIMIT",$email));`);
}

function legacyRedisKeys(pattern: string): string[] {
  const output = execFileSync("docker", [
    "exec", legacyDockerContainer, "redis-cli", "-s", "/data/redis.sock", "-n", "1", "--scan", "--pattern", pattern
  ], { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" });
  return output.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
}

function legacyRedisTTL(key: string): number {
  return Number(execFileSync("docker", [
    "exec", legacyDockerContainer, "redis-cli", "-s", "/data/redis.sock", "-n", "1", "TTL", key
  ], { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" }).trim());
}

function legacyRedisDelete(key: string): void {
  execFileSync("docker", [
    "exec", legacyDockerContainer, "redis-cli", "-s", "/data/redis.sock", "-n", "1", "DEL", key
  ], { stdio: "ignore" });
}

function clearLegacyMailLinkCooldown(email: string): void {
  for (const key of legacyRedisKeys(`*LAST_SEND_LOGIN_WITH_MAIL_LINK_TIMESTAMP_${email}`)) legacyRedisDelete(key);
}

function legacyTinker(statement: string): string {
  return execFileSync(
    "docker",
    ["exec", legacyDockerContainer, "php", "artisan", "tinker", "--quiet", "--no-interaction", `--execute=${statement}`],
    { stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" }
  ).trim();
}

function hashSearchParams(address: string): URLSearchParams {
  const hash = new URL(address).hash;
  const queryIndex = hash.indexOf("?");
  return new URLSearchParams(queryIndex >= 0 ? hash.slice(queryIndex + 1) : "");
}

function isVisibleLegacyNotice(value: unknown): boolean {
  return typeof value === "object" && value !== null && "show" in value && value.show === true;
}

function expectLegacyClientCatalog(value: unknown): void {
  expect(Array.isArray(value)).toBe(true);
  if (!Array.isArray(value)) return;
  expect(value).toHaveLength(15);
  const entries: unknown[] = value;
  const ids = entries.map((item: unknown) => readStringProperty(item, "id"));
  expect(ids.slice(0, 4)).toEqual(["karing", "happ", "clash-mi", "koalaclash"]);
  const platforms = new Set(["android", "ios", "windows", "macos", "linux"]);
  for (const item of entries) {
    const downloads = readArrayProperty(item, "downloads");
    expect(downloads).not.toBeNull();
    if (downloads === null) continue;
    for (const download of downloads) {
      const platform = readStringProperty(download, "platform");
      const downloadURL = readStringProperty(download, "download_url");
      expect(platforms.has(platform ?? "")).toBe(true);
      expect(downloadURL).toContain("/client-download/");
    }
  }
}

function readStringProperty(value: unknown, key: string): string | null {
  if (typeof value !== "object" || value === null) return null;
  const property: unknown = Reflect.get(value, key);
  return typeof property === "string" ? property : null;
}

function readArrayProperty(value: unknown, key: string): unknown[] | null {
  if (typeof value !== "object" || value === null) return null;
  const property: unknown = Reflect.get(value, key);
  return Array.isArray(property) ? property as unknown[] : null;
}

function readProperty(value: unknown, key: string): unknown {
  if (typeof value !== "object" || value === null) return undefined;
  return Reflect.get(value, key) as unknown;
}

function readObjectProperty(value: unknown, key: string): Record<string, unknown> {
  const property = readProperty(value, key);
  expect(typeof property).toBe("object");
  expect(property).not.toBeNull();
  expect(Array.isArray(property)).toBe(false);
  if (typeof property !== "object" || property === null || Array.isArray(property)) return {};
  return property as Record<string, unknown>;
}

function normalizeClientCatalog(value: unknown) {
  expect(Array.isArray(value)).toBe(true);
  if (!Array.isArray(value)) return [];
  return (value as unknown[]).map((client: unknown) => ({
    id: readStringProperty(client, "id"),
    name: readStringProperty(client, "name"),
    core: readStringProperty(client, "core"),
    description: readStringProperty(client, "description"),
    featured: readProperty(client, "featured") === true,
    hwid: readProperty(client, "hwid") === true,
    downloads: (readArrayProperty(client, "downloads") ?? []).map((download: unknown) => ({
      platform: readStringProperty(download, "platform"),
      source: readStringProperty(download, "source"),
      downloadPath: pathOf(readStringProperty(download, "download_url")),
      hasCloud: readStringProperty(download, "cloud_url") !== null,
      hasTutorial: readStringProperty(download, "tutorial_url") !== null
    }))
  }));
}

function pathOf(address: string | null): string | null {
  if (address === null) return null;
  return new URL(address, "https://panel.invalid").pathname;
}
