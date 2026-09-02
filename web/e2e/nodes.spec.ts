import { expect, test, type Page } from "@playwright/test";

import { adminAPIPath, adminEntryPath, adminEmail, adminPassword } from "./support";

interface NodeRecord {
  id: number;
  revision: number;
  name: string;
}

interface ProtocolScenario {
  type: string;
  label: string;
  configure: (dialog: ReturnType<Page["getByRole"]>) => Promise<void>;
  assertPersisted: (settings: Record<string, unknown>) => void;
  assertReopened: (dialog: ReturnType<Page["getByRole"]>) => Promise<void>;
}

const protocolScenarios: ProtocolScenario[] = [
  {
    type: "shadowsocks",
    label: "Shadowsocks",
    configure: async (dialog) => {
      await dialog.getByLabel("加密算法").fill("aes-256-gcm");
      await dialog.getByRole("combobox", { name: "插件", exact: true }).selectOption("obfs");
      await dialog.getByLabel("插件参数").fill("obfs=http;obfs-host=ss.example.test");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({ cipher: "aes-256-gcm", plugin: "obfs", plugin_opts: "obfs=http;obfs-host=ss.example.test" }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("加密算法")).toHaveValue("aes-256-gcm");
      await expect(dialog.getByRole("combobox", { name: "插件", exact: true })).toHaveValue("obfs");
    }
  },
  {
    type: "vmess",
    label: "VMess",
    configure: async (dialog) => {
      await dialog.getByLabel("安全性").selectOption("1");
      await dialog.getByRole("combobox", { name: "传输协议", exact: true }).selectOption("ws");
      await dialog.getByRole("button", { name: "套用 WebSocket 模板" }).click();
      await dialog.getByLabel("SNI").fill("vmess.example.test");
      await dialog.getByRole("checkbox", { name: "uTLS", exact: true }).check();
      await dialog.getByLabel("uTLS 指纹").selectOption("firefox");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({
      tls: 1, network: "ws", network_settings: { path: "/", headers: { Host: "v2ray.com" } },
      tls_settings: { server_name: "vmess.example.test" }, utls: { enabled: true, fingerprint: "firefox" }
    }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("安全性")).toHaveValue("1");
      await expect(dialog.getByRole("combobox", { name: "传输协议", exact: true })).toHaveValue("ws");
      await expect(dialog.getByLabel("uTLS 指纹")).toHaveValue("firefox");
    }
  },
  {
    type: "trojan",
    label: "Trojan",
    configure: async (dialog) => {
      await dialog.getByRole("combobox", { name: "传输协议", exact: true }).selectOption("grpc");
      await dialog.getByRole("button", { name: "套用 gRPC 模板" }).click();
      await dialog.getByLabel("SNI").fill("trojan.example.test");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({
      tls: 1, network: "grpc", network_settings: { serviceName: "GunService" }, tls_settings: { server_name: "trojan.example.test" }
    }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("安全性")).toHaveValue("1");
      await expect(dialog.getByRole("combobox", { name: "传输协议", exact: true })).toHaveValue("grpc");
      await expect(dialog.getByLabel("SNI")).toHaveValue("trojan.example.test");
    }
  },
  {
    type: "hysteria",
    label: "Hysteria",
    configure: async (dialog) => {
      await dialog.getByLabel("上行带宽 (Mbps)").fill("120");
      await dialog.getByLabel("下行带宽 (Mbps)").fill("240");
      await dialog.getByLabel("端口跳跃间隔 (秒)").fill("30");
      await dialog.getByLabel("SNI").fill("hysteria.example.test");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({
      version: 2, hop_interval: 30, bandwidth: { up: 120, down: 240 }, tls: { server_name: "hysteria.example.test" }
    }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("上行带宽 (Mbps)")).toHaveValue("120");
      await expect(dialog.getByLabel("端口跳跃间隔 (秒)")).toHaveValue("30");
    }
  },
  {
    type: "vless",
    label: "VLess",
    configure: async (dialog) => {
      await dialog.getByRole("combobox", { name: "传输协议", exact: true }).selectOption("kcp");
      await dialog.getByLabel("Flow").selectOption("xtls-rprx-direct");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({ tls: 0, network: "kcp", flow: "xtls-rprx-direct", network_settings: {} }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByRole("combobox", { name: "传输协议", exact: true })).toHaveValue("kcp");
      await expect(dialog.getByLabel("Flow")).toHaveValue("xtls-rprx-direct");
    }
  },
  {
    type: "tuic",
    label: "TUIC",
    configure: async (dialog) => {
      await dialog.getByLabel("拥塞控制").selectOption("cubic");
      await dialog.getByLabel("ALPN").selectOption(["h3", "h2"]);
      await dialog.getByLabel("UDP Relay").selectOption("quic");
      await dialog.getByLabel("SNI").fill("tuic.example.test");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({
      version: 5, congestion_control: "cubic", alpn: ["h3", "h2"], udp_relay_mode: "quic", tls: { server_name: "tuic.example.test" }
    }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("拥塞控制")).toHaveValue("cubic");
      await expect(dialog.getByLabel("ALPN")).toHaveValues(["h3", "h2"]);
    }
  },
  ...["socks", "naive", "http"].map((type): ProtocolScenario => ({
    type,
    label: type === "socks" ? "SOCKS" : type === "naive" ? "Naive" : "HTTP",
    configure: async (dialog) => {
      await dialog.getByRole("combobox", { name: "TLS", exact: true }).selectOption("1");
      await dialog.getByLabel("SNI").fill(`${type}.example.test`);
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({ tls: 1, tls_settings: { server_name: `${type}.example.test` } }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByRole("combobox", { name: "TLS", exact: true })).toHaveValue("1");
      await expect(dialog.getByLabel("SNI")).toHaveValue(`${type}.example.test`);
    }
  })),
  {
    type: "mieru",
    label: "Mieru",
    configure: async (dialog) => {
      await dialog.getByRole("combobox", { name: "传输协议", exact: true }).selectOption("UDP");
      await dialog.getByLabel("Traffic Pattern").fill("default");
      await dialog.getByRole("checkbox", { name: "多路复用", exact: true }).check();
      await dialog.getByLabel("复用协议").selectOption("yamux");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({ transport: "UDP", traffic_pattern: "default", multiplex: { enabled: true, protocol: "yamux" } }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByRole("combobox", { name: "传输协议", exact: true })).toHaveValue("UDP");
      await expect(dialog.getByLabel("Traffic Pattern")).toHaveValue("default");
      await expect(dialog.getByLabel("复用协议")).toHaveValue("yamux");
    }
  },
  {
    type: "anytls",
    label: "AnyTLS",
    configure: async (dialog) => {
      await dialog.getByLabel("ALPN").fill("h2");
      await dialog.getByRole("button", { name: "使用默认方案" }).click();
      await dialog.getByLabel("SNI").fill("anytls.example.test");
    },
    assertPersisted: (settings) => expect(settings).toMatchObject({
      alpn: "h2",
      padding_scheme: [
        "stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000",
        "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"
      ],
      tls: { server_name: "anytls.example.test" }
    }),
    assertReopened: async (dialog) => {
      await expect(dialog.getByLabel("ALPN")).toHaveValue("h2");
      await expect(dialog.getByLabel("Padding Scheme")).toContainText("stop=8");
    }
  }
];

test("administrator node management preserves the observed Xboard workflow on every viewport", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await loginAdministrator(page);
  const fixtureRoot = `Node parity ${testInfo.project.name}-`;
  await deleteFixtureNodes(page, fixtureRoot);
  const unique = `${testInfo.project.name}-${Date.now()}`;
  const prefix = `${fixtureRoot}${unique}`;
  const firstName = `${prefix} A`;
  const secondName = `${prefix} B`;
  const editedName = `${prefix} A edited`;

  try {
    await createNode(page, { name: firstName, type: "vless", host: "node-a.example.test", port: "443", sort: 10 });
    await createNode(page, { name: secondName, type: "trojan", host: "node-b.example.test", port: "8443", sort: 20 });

    await page.getByRole("button", { name: "节点管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "节点管理" })).toBeVisible();
    await page.getByLabel("搜索节点").fill(prefix);
    await page.getByRole("button", { name: "查询节点" }).click();
    await expect(page.getByText(firstName, { exact: true })).toBeVisible();
    await expect(page.getByText(secondName, { exact: true })).toBeVisible();

    const expectedColumns = ["节点ID", "显隐", "节点", "部署方式", "地址", "在线人数", "倍率", "权限组", "流量使用", "操作"];
    expect(await page.getByRole("table", { name: "节点列表" }).locator("th").allTextContents()).toEqual(expectedColumns);
    await expect(page.getByLabel("协议筛选").locator("option")).toHaveText([
      "全部", "Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"
    ]);
    await expect(page.getByText("第 1 / 1 页 · 共 2 个节点", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `编辑节点：${firstName}` }).click();
    const editDialog = page.getByRole("dialog", { name: "编辑节点" });
    await editDialog.getByLabel("节点名称").fill(editedName);
    await editDialog.getByLabel("节点地址").fill("node-a-updated.example.test");
    await editDialog.getByRole("button", { name: "提交" }).click();
    await expect(editDialog).toBeHidden();
    await expect(page.getByText(editedName, { exact: true })).toBeVisible();
    await expect(page.getByText("node-a-updated.example.test:443", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `复制节点：${editedName}` }).click();
    const copiedName = `${editedName} - 副本`;
    await expect(page.getByText(copiedName, { exact: true })).toBeVisible();
    const copyRow = page.locator("tr", { has: page.getByText(copiedName, { exact: true }) });
    await expect(copyRow.getByText("隐藏", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: `上移节点：${secondName}` }).click();
    await expect.poll(async () => {
      const names = await nodeNames(page);
      return names.indexOf(secondName) < names.indexOf(copiedName);
    }).toBe(true);
    await page.getByRole("button", { name: `上移节点：${secondName}` }).click();
    await expect.poll(async () => {
      const names = await nodeNames(page);
      return names.indexOf(secondName) < names.indexOf(editedName);
    }).toBe(true);

    await page.getByRole("checkbox", { name: `选择节点：${editedName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量隐藏" }).click();
    const editedRow = page.locator("tr", { has: page.getByText(editedName, { exact: true }) });
    await expect(editedRow.getByText("隐藏", { exact: true })).toBeVisible();

    await page.getByRole("checkbox", { name: `选择节点：${secondName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量重置流量" }).click();
    const resetDialog = page.getByRole("alertdialog", { name: "重置节点流量" });
    await expect(resetDialog).toContainText("当前累计流量归零");
    await resetDialog.getByRole("button", { name: "确认重置" }).click();
    await expect(resetDialog).toBeHidden();

    await page.getByRole("checkbox", { name: `选择节点：${editedName}`, exact: true }).check();
    await page.getByRole("checkbox", { name: `选择节点：${secondName}`, exact: true }).check();
    await page.getByRole("checkbox", { name: `选择节点：${copiedName}`, exact: true }).check();
    await page.getByRole("button", { name: "批量删除" }).click();
    const deleteDialog = page.getByRole("alertdialog", { name: "删除节点" });
    await expect(deleteDialog).toContainText("选中的 3 个节点");
    await deleteDialog.getByRole("button", { name: "确认删除" }).click();
    await expect(deleteDialog).toBeHidden();
    await expect(page.getByText("没有符合条件的节点。", { exact: true })).toBeVisible();

    const viewport = await page.evaluate(() => ({
      viewportWidth: window.innerWidth,
      documentWidth: document.documentElement.scrollWidth
    }));
    expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.viewportWidth);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await deleteFixtureNodes(page, prefix);
  }
});

test("administrator can search, select, and reopen a remote parent node", async ({ page }, testInfo) => {
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await loginAdministrator(page);
  await page.getByRole("button", { name: "节点管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "节点管理" })).toBeVisible();
  const prefix = `Parent search ${testInfo.project.name} ${Date.now()}`;
  const parentName = `${prefix} remote parent`;
  const childName = `${prefix} child`;
  await deleteFixtureNodes(page, prefix);

  try {
    await createNode(page, { name: parentName, type: "vless", host: "parent.example.test", port: "443", sort: 10 });
    const parentDetail = await loadNodeDefinition(page, parentName);
    const parentID = parentDetail.id;
    expect(Number.isSafeInteger(parentID)).toBe(true);

    await page.getByRole("button", { name: "添加节点" }).click();
    const createDialog = page.getByRole("dialog", { name: "新建节点" });
    await createDialog.getByLabel("协议类型").selectOption("vless");
    const parentSearch = createDialog.getByLabel("搜索父节点");
    const parentSelect = createDialog.getByLabel("父节点", { exact: true });
    await parentSearch.fill(parentName);
    await expect(parentSelect.getByRole("option", { name: `${parentName} (#${String(parentID)})` })).toHaveCount(1);
    await parentSearch.press("Tab");
    await expect(parentSelect).toBeFocused();
    await parentSelect.selectOption(String(parentID));
    await createDialog.getByLabel("节点名称").fill(childName);
    await createDialog.getByLabel("节点地址").fill("child.example.test");
    await createDialog.getByRole("button", { name: "提交" }).click();
    await expect(createDialog).toBeHidden();

    const childDetail = await loadNodeDefinition(page, childName);
    expect(childDetail.parent_id).toBe(parentID);
    await page.getByRole("button", { name: `编辑节点：${childName}` }).click();
    const editDialog = page.getByRole("dialog", { name: "编辑节点" });
    await expect(editDialog.getByLabel("父节点", { exact: true })).toHaveValue(String(parentID));
    await expect(editDialog.getByLabel("父节点", { exact: true }).getByRole("option", { name: `${parentName} (#${String(parentID)})` })).toHaveCount(1);
    await editDialog.getByRole("button", { name: "取消" }).click();

    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await deleteFixtureNodes(page, prefix);
  }
});

test("all Xboard node protocols can be created, persisted, and reopened through the administrator UI", async ({ page }, testInfo) => {
  test.setTimeout(300_000);
  page.setDefaultTimeout(10_000);
  const pageErrors: string[] = [];
  const serverErrors: string[] = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`);
  });

  await loginAdministrator(page);
  await page.getByRole("button", { name: "节点管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "节点管理" })).toBeVisible();
  const fixtureRoot = `Protocol parity ${testInfo.project.name} `;
  await deleteFixtureNodes(page, fixtureRoot);
  const prefix = `${fixtureRoot}${Date.now()}`;
  await page.getByLabel("搜索节点").fill(prefix);
  await page.getByRole("button", { name: "查询节点" }).click();

  try {
    for (const [index, scenario] of protocolScenarios.entries()) {
      const name = `${prefix} ${scenario.label}`;
      const editedName = `${name} edited`;
      const host = `${scenario.type}.node.example.test`;
      const editedHost = `${scenario.type}.updated.example.test`;

      await page.getByRole("button", { name: "添加节点" }).click();
      const createDialog = page.getByRole("dialog", { name: "新建节点" });
      await expect(createDialog).toBeVisible();
      await createDialog.getByLabel("协议类型").selectOption(scenario.type);
      await createDialog.getByLabel("节点名称").fill(name);
      await createDialog.getByLabel("节点地址").fill(host);
      await createDialog.getByLabel("连接端口").fill(String(20_000 + index));
      await createDialog.getByLabel("服务端口").fill(String(30_000 + index));
      await createDialog.getByLabel("监听地址").fill("::");
      await createDialog.getByLabel("标签").fill(`parity, ${scenario.type}`);
      await scenario.configure(createDialog);
      await assertDialogFitsViewport(createDialog);
      await createDialog.getByRole("button", { name: "提交" }).click();
      await expect(createDialog).toBeHidden();
      await expect(page.getByText(name, { exact: true })).toBeVisible();

      const detail = await loadNodeDefinition(page, name);
      expect(detail.type).toBe(scenario.type);
      expect(detail.listen_address).toBe("::");
      expect(detail.tags).toEqual(["parity", scenario.type]);
      scenario.assertPersisted(asRecord(detail.protocol_settings));

      await page.getByRole("button", { name: `编辑节点：${name}` }).click();
      const editDialog = page.getByRole("dialog", { name: "编辑节点" });
      await expect(editDialog.getByLabel("节点名称")).toHaveValue(name);
      await expect(editDialog.getByLabel("协议类型")).toBeDisabled();
      await expect(editDialog.getByLabel("协议类型")).toHaveValue(scenario.type);
      await expect(editDialog.getByLabel("监听地址")).toHaveValue("::");
      await scenario.assertReopened(editDialog);
      await editDialog.getByLabel("节点名称").fill(editedName);
      await editDialog.getByLabel("节点地址").fill(editedHost);
      await assertDialogFitsViewport(editDialog);
      await editDialog.getByRole("button", { name: "提交" }).click();
      await expect(editDialog).toBeHidden();
      await expect(page.getByText(editedName, { exact: true })).toBeVisible();

      const editedDetail = await loadNodeDefinition(page, editedName);
      expect(editedDetail.host).toBe(editedHost);
      expect(editedDetail.type).toBe(scenario.type);
      scenario.assertPersisted(asRecord(editedDetail.protocol_settings));
    }

    await page.reload();
    await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
    await page.getByRole("button", { name: "节点管理", exact: true }).click();
    await expect(page.getByRole("heading", { name: "节点管理" })).toBeVisible();
    await page.getByLabel("搜索节点").fill(prefix);
    await page.getByRole("button", { name: "查询节点" }).click();
    await expect(page.getByText(`第 1 / 1 页 · 共 ${protocolScenarios.length} 个节点`, { exact: true })).toBeVisible();
    expect(await nodeNames(page)).toHaveLength(protocolScenarios.length);
    expect(pageErrors).toEqual([]);
    expect(serverErrors).toEqual([]);
  } finally {
    await deleteFixtureNodes(page, prefix);
  }
});

async function loginAdministrator(page: Page) {
  await page.goto(adminEntryPath);
  await page.getByLabel("邮箱").fill(adminEmail);
  await page.getByLabel("密码").fill(adminPassword);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("heading", { name: "服务器管理" })).toBeVisible();
}

async function createNode(page: Page, input: { name: string; type: string; host: string; port: string; sort: number }) {
  const result = await adminFetch(page, "/api/v1/admin/nodes", "POST", {
    ...input,
    show: true,
    enabled: true
  });
  expect(result.status, result.body).toBe(201);
}

async function deleteFixtureNodes(page: Page, prefix: string) {
  const listed = await adminFetch(page, `/api/v1/admin/nodes?page=1&page_size=500&q=${encodeURIComponent(prefix)}`, "GET");
  if (listed.status !== 200) return;
  const payload: unknown = JSON.parse(listed.body);
  const data = objectProperty(payload, "data");
  const items = objectProperty(data, "items");
  if (!Array.isArray(items)) return;
  const targets = items.flatMap((candidate): NodeRecord[] => {
    if (typeof candidate !== "object" || candidate === null) return [];
    const id = Reflect.get(candidate, "id");
    const revision = Reflect.get(candidate, "revision");
    const name = Reflect.get(candidate, "name");
    return Number.isSafeInteger(id) && Number.isSafeInteger(revision) && typeof name === "string" && name.startsWith(prefix)
      ? [{ id: id as number, revision: revision as number, name }]
      : [];
  });
  if (targets.length > 0) {
    await adminFetch(page, "/api/v1/admin/nodes/bulk-delete", "POST", {
      targets: targets.map(({ id, revision }) => ({ id, revision }))
    });
  }
}

async function adminFetch(page: Page, path: string, method: "GET" | "POST", body?: unknown) {
  return page.evaluate(async ({ target, requestMethod, requestBody }) => {
    const prefix = "xboard_csrf=";
    const encoded = document.cookie.split("; ").find((item) => item.startsWith(prefix))?.slice(prefix.length) ?? "";
    const headers: Record<string, string> = {};
    if (requestBody !== undefined) {
      headers["Content-Type"] = "application/json";
      headers["X-CSRF-Token"] = decodeURIComponent(encoded);
    }
    const response = await fetch(target, {
      method: requestMethod,
      credentials: "same-origin",
      headers,
      body: requestBody === undefined ? undefined : JSON.stringify(requestBody)
    });
    return { status: response.status, body: await response.text() };
  }, { target: adminAPIPath(path), requestMethod: method, requestBody: body });
}

async function loadNodeDefinition(page: Page, name: string): Promise<Record<string, unknown>> {
  const listed = await adminFetch(page, `/api/v1/admin/nodes?page=1&page_size=20&q=${encodeURIComponent(name)}`, "GET");
  expect(listed.status, listed.body).toBe(200);
  const listPayload: unknown = JSON.parse(listed.body);
  const items = objectProperty(objectProperty(listPayload, "data"), "items");
  expect(Array.isArray(items)).toBe(true);
  const target = (items as unknown[]).find((candidate) => objectProperty(candidate, "name") === name);
  const id = objectProperty(target, "id");
  expect(Number.isSafeInteger(id)).toBe(true);

  const loaded = await adminFetch(page, `/api/v1/admin/nodes/${String(id)}`, "GET");
  expect(loaded.status, loaded.body).toBe(200);
  const payload: unknown = JSON.parse(loaded.body);
  const data = objectProperty(payload, "data");
  expect(typeof data === "object" && data !== null).toBe(true);
  return data as Record<string, unknown>;
}

async function assertDialogFitsViewport(dialog: ReturnType<Page["getByRole"]>) {
  const dimensions = await dialog.evaluate((element) => ({
    left: element.getBoundingClientRect().left,
    right: element.getBoundingClientRect().right,
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
    viewportWidth: window.innerWidth
  }));
  expect(dimensions.left).toBeGreaterThanOrEqual(0);
  expect(dimensions.right).toBeLessThanOrEqual(dimensions.viewportWidth);
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth);
}

function objectProperty(value: unknown, key: string): unknown {
  return typeof value === "object" && value !== null ? Reflect.get(value, key) : undefined;
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

async function nodeNames(page: Page): Promise<string[]> {
  return page.locator("tbody tr td[data-label='节点'] strong").allTextContents();
}
