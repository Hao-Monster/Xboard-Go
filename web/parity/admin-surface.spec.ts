import { expect, test, type Page } from "@playwright/test";
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

test("legacy user ticket surface remains observable without frontend source", async ({ page }) => {
  const errors = watchErrors(page);
  await loginLegacyUser(page);
  const response = page.waitForResponse((item) => item.url().includes("/api/v1/user/ticket/fetch"));
  await page.getByText("我的工单", { exact: true }).click();
  expect((await response).status()).toBe(200);
  await expect(page.getByText("工单历史", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "新的工单" })).toBeVisible();
  for (const column of ["主题", "工单级别", "工单状态", "创建时间", "最后回复时间", "操作"]) {
    await expect(page.getByText(column, { exact: true }).first()).toBeVisible();
  }
  await page.getByRole("button", { name: "新的工单" }).click();
  await expect(page.locator('input[placeholder="请输入工单主题"]:visible')).toBeVisible();
  await expect(page.locator('textarea[placeholder="请描述您遇到的问题"]:visible')).toBeVisible();
  await page.locator(".n-base-selection:visible").click();
  for (const level of ["低", "中", "高"]) await expect(page.getByText(level, { exact: true }).last()).toBeVisible();
  await page.getByRole("button", { name: "取消" }).click();
  expect(errors).toEqual([]);
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

async function loginLegacy(page: Page) {
  await page.goto(legacyURL, { waitUntil: "domcontentloaded" });
  await page.locator('input[name="email"]').fill(legacyEmail);
  await page.locator('input[name="password"]').fill(legacyPassword);
  await page.locator('input[name="password"]').press("Enter");
  await expect(page.locator('a[href="#/server/machine"]')).toBeVisible();
}

async function loginGo(page: Page) {
  await page.goto(goURL, { waitUntil: "domcontentloaded" });
  await page.getByLabel("邮箱").fill(goEmail);
  await page.getByLabel("密码").fill(goPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function loginLegacyUser(page: Page) {
  await page.goto(new URL("/", legacyURL).toString(), { waitUntil: "domcontentloaded" });
  const fields = page.locator("input:visible");
  await fields.first().fill(legacyEmail);
  await fields.nth(1).fill(legacyPassword);
  await fields.nth(1).press("Enter");
  await expect(page.getByText("我的工单", { exact: true })).toBeVisible();
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
