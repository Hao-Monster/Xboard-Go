import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { defaultProtocolSettings } from "./NodeDefinitionModal";
import { NodeManagementPage } from "./NodeManagementPage";

const machine = {
  id: 5, name: "edge-sg", notes: "", is_active: true, last_seen_at: null, load_status: null,
  servers_count: 1, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z"
};
const group = { id: 7, name: "Premium", users_count: 1, servers_count: 1, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const route = { id: 9, remarks: "Direct", match: ["domain:example.test"], action: "direct" as const, action_value: "", created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const node = {
  id: 41, revision: 3, name: "SG VLESS", type: "vless", host: "sg.example.test", port: "443",
  show: true, enabled: true, sort: 10, rate: 2.5, traffic_upload: 1024, traffic_download: 2048,
  runtime_configured: true, last_check_at: null, last_push_at: null, machine_id: 5, machine_name: "edge-sg",
  group_ids: [7], online_count: 4, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z"
};
const definition = {
  ...node, external_code: "", parent_id: null, server_port: 443, listen_address: "0.0.0.0", tags: ["edge"],
  protocol_settings: {
    tls: 0, network: "tcp", network_settings: {}, flow: "", encryption: { enabled: false, encryption: "", decryption: "" },
    tls_settings: { server_name: "", allow_insecure: false, ech: { enabled: false, config: "", query_server_name: "", key: "" } },
    reality_settings: { server_name: "", server_port: 443, public_key: "", private_key: "", short_id: "", allow_insecure: false },
    utls: { enabled: false, fingerprint: "chrome" },
    multiplex: { enabled: false, protocol: "smux", max_connections: 4, padding: false, brutal: { enabled: false, up_mbps: 100, down_mbps: 100 } }
  },
  route_ids: [9], rate_time_enabled: false, rate_time_ranges: [], custom_outbounds: [], custom_routes: [],
  certificate_config: { cert_mode: "none" }, transfer_enable: 0
};

describe("NodeManagementPage", () => {
  it("locks the exact legacy defaults for all eleven protocols", () => {
    const tls = { server_name: "", allow_insecure: false, ech: { enabled: false, config: "", query_server_name: "", key: "" } };
    const multiplex = { enabled: false, protocol: "smux", max_connections: 4, padding: false, brutal: { enabled: false, up_mbps: 100, down_mbps: 100 } };
    const reality = { server_name: "", server_port: 443, public_key: "", private_key: "", short_id: "", allow_insecure: false };
    expect(Object.fromEntries([
      "shadowsocks", "vmess", "trojan", "hysteria", "vless", "tuic", "socks", "naive", "http", "mieru", "anytls"
    ].map((protocol) => [protocol, defaultProtocolSettings(protocol)]))).toEqual({
      shadowsocks: { cipher: "aes-128-gcm", plugin: "", plugin_opts: "" },
      vmess: { tls: 0, network: "tcp", network_settings: {}, tls_settings: tls, utls: { enabled: false, fingerprint: "chrome" }, multiplex },
      trojan: { tls: 1, network: "tcp", network_settings: {}, tls_settings: tls, reality_settings: reality, utls: { enabled: false, fingerprint: "chrome" }, multiplex },
      hysteria: { version: 2, alpn: "h2", obfs: { open: false, type: "salamander", password: "" }, tls, bandwidth: { up: 0, down: 0 } },
      vless: { tls: 0, network: "tcp", network_settings: {}, flow: "", encryption: { enabled: false, encryption: "", decryption: "" }, tls_settings: tls, reality_settings: reality, utls: { enabled: false, fingerprint: "chrome" }, multiplex },
      tuic: { version: 5, congestion_control: "bbr", alpn: ["h3"], udp_relay_mode: "native", tls },
      socks: { tls: 0, tls_settings: tls }, naive: { tls: 0, tls_settings: tls }, http: { tls: 0, tls_settings: tls },
      mieru: { transport: "TCP", traffic_pattern: "", multiplex }, anytls: { alpn: "", padding_scheme: [], tls }
    });
  });

  it("keeps the legacy node table headers visible when the directory is empty", async () => {
    const api = nodeAPI([]);
    render(<NodeManagementPage api={api} />);

    const table = await screen.findByRole("table", { name: "节点列表" });
    expect(within(table).getByRole("columnheader", { name: "节点ID" })).toBeVisible();
    expect(within(table).getByRole("columnheader", { name: "操作" })).toBeVisible();
    expect(within(table).getByText("没有符合条件的节点。")).toBeVisible();
  });

  it("matches the legacy directory columns, protocol filters, and bounded server pagination", async () => {
    const api = nodeAPI();
    api.listAdminNodes.mockImplementation((query?: { page?: number }) => Promise.resolve(query?.page === 2
      ? { items: [{ ...node, id: 42, name: "Next node" }], total: 501, page: 2, page_size: 500 }
      : { items: [node], total: 501, page: 1, page_size: 500 }));
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);

    const table = await screen.findByRole("table", { name: "节点列表" });
    for (const heading of ["节点ID", "显隐", "节点", "部署方式", "地址", "在线人数", "倍率", "权限组", "流量使用", "操作"]) {
      expect(within(table).getByRole("columnheader", { name: heading })).toBeVisible();
    }
    for (const protocol of ["Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"]) {
      expect(screen.getByRole("option", { name: protocol })).toBeInTheDocument();
    }
    expect(within(table).getByText("edge-sg")).toBeVisible();
    expect(within(table).getByText("Premium")).toBeVisible();
    expect(within(table).getByText("4")).toBeVisible();
    expect(api.listAdminNodes).toHaveBeenCalledWith({ page: 1, page_size: 500 });

    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByText("Next node")).toBeVisible();
    expect(api.listAdminNodes).toHaveBeenCalledWith({ page: 2, page_size: 500 });
  });

  it("edits common fields, copies complete nodes, and persists explicit order", async () => {
    const second = { ...node, id: 42, revision: 1, name: "US Trojan", type: "trojan", sort: 20 };
    const api = nodeAPI([node, second]);
    api.getAdminNodeDefinition.mockResolvedValue(definition);
    api.replaceAdminNodeDefinition.mockResolvedValue({ ...definition, revision: 4, name: "SG VLESS updated" });
    api.copyAdminNode.mockResolvedValue({ ...node, id: 43, revision: 1, name: "SG VLESS - 副本", show: false });
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    expect(await screen.findByText("US Trojan")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "编辑节点：SG VLESS" }));
    const edit = screen.getByRole("dialog", { name: "编辑节点" });
    await user.clear(within(edit).getByLabelText("节点名称"));
    await user.type(within(edit).getByLabelText("节点名称"), "SG VLESS updated");
    await user.selectOptions(within(edit).getByLabelText("绑定服务器"), "");
    await user.click(within(edit).getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.replaceAdminNodeDefinition).toHaveBeenCalledWith(41, expect.objectContaining({
      revision: 3, type: "vless", name: "SG VLESS updated", host: "sg.example.test", port: "443",
      server_port: 443, listen_address: "0.0.0.0", machine_id: null, group_ids: [7], route_ids: [9]
    })));

    await user.click(screen.getByRole("button", { name: "复制节点：SG VLESS" }));
    await waitFor(() => expect(api.copyAdminNode).toHaveBeenCalledWith(41, 3));
    await user.click(screen.getByRole("button", { name: "上移节点：US Trojan" }));
    await waitFor(() => expect(api.reorderAdminNodes).toHaveBeenCalledWith([{ id: 42, revision: 1 }, { id: 41, revision: 3 }]));
  });

  it("creates nodes with the legacy eleven-protocol order and protocol-specific defaults", async () => {
    const api = nodeAPI([]);
    api.createAdminNodeDefinition.mockResolvedValue(definition);
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    await screen.findByRole("table", { name: "节点列表" });

    await user.click(screen.getByRole("button", { name: "添加节点" }));
    const dialog = screen.getByRole("dialog", { name: "新建节点" });
    const protocol = within(dialog).getByLabelText("协议类型");
    expect(within(protocol).getAllByRole("option").map((option) => option.textContent)).toEqual([
      "Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"
    ]);
    await user.selectOptions(protocol, "tuic");
    expect(within(dialog).getByLabelText("拥塞控制")).toHaveValue("bbr");
    expect(within(dialog).getByLabelText("UDP Relay")).toHaveValue("native");
    await user.selectOptions(protocol, "vmess");
    expect(within(within(dialog).getByLabelText("传输协议")).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual([
      "tcp", "ws", "grpc", "h2", "httpupgrade", "xhttp"
    ]);
    await user.selectOptions(protocol, "vless");
    expect(within(within(dialog).getByLabelText("传输协议")).getAllByRole("option").map((option) => option.getAttribute("value"))).toContain("kcp");
    await user.selectOptions(protocol, "trojan");
    expect(within(within(dialog).getByLabelText("安全性")).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual(["1", "2"]);
    await user.selectOptions(protocol, "hysteria");
    await user.selectOptions(within(dialog).getByLabelText("版本"), "1");
    expect(within(within(dialog).getByLabelText("ALPN")).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual([
      "hysteria", "http/1.1", "h2", "h3"
    ]);
    await user.selectOptions(protocol, "anytls");
    expect(within(dialog).getByLabelText("Padding Scheme")).toHaveValue("");
    await user.click(within(dialog).getByRole("button", { name: "使用默认方案" }));
    expect(within(dialog).getByLabelText("Padding Scheme")).toHaveValue([
      "stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000",
      "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"
    ].join("\n"));
    await user.selectOptions(protocol, "mieru");
    expect(within(dialog).getByLabelText("多路复用")).not.toBeChecked();
    await user.selectOptions(protocol, "shadowsocks");
    expect(within(dialog).getByLabelText("加密算法")).toHaveValue("aes-128-gcm");
    expect(within(within(dialog).getByLabelText("插件")).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual([
      "", "obfs", "v2ray-plugin", "gost-plugin", "shadow-tls", "restls", "kcptun"
    ]);
    await user.click(within(dialog).getByText("高级设置"));
    const certificateMode = within(dialog).getByLabelText("证书模式");
    expect(within(certificateMode).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual([
      "none", "http", "dns", "self", "content"
    ]);
    await user.selectOptions(certificateMode, "self");
    await user.type(within(dialog).getByLabelText("证书域名"), "ss.example.test");
    await user.type(within(dialog).getByLabelText("节点名称"), "New Shadowsocks");
    await user.type(within(dialog).getByLabelText("节点地址"), "ss.example.test");
    await user.click(within(dialog).getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.createAdminNodeDefinition).toHaveBeenCalledWith(expect.objectContaining({
      type: "shadowsocks", name: "New Shadowsocks", host: "ss.example.test", rate: 1,
      port: "443", server_port: 443, listen_address: "0.0.0.0", protocol_settings: {
        cipher: "aes-128-gcm", plugin: "", plugin_opts: ""
      }, certificate_config: { cert_mode: "self", domain: "ss.example.test" }, show: false, enabled: true
    })));
  });

  it("keeps parent choices independent from the active table filter", async () => {
    const api = nodeAPI([node]);
    api.listAdminNodes.mockImplementation((query?: { q?: string }) => Promise.resolve(query?.q === "no-match"
      ? { items: [], total: 0, page: 1, page_size: 500 }
      : { items: [node], total: 1, page: 1, page_size: 500 }));
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    await screen.findByText("SG VLESS", { exact: true });
    await user.type(screen.getByLabelText("搜索节点"), "no-match");
    await user.click(screen.getByRole("button", { name: "查询节点" }));
    await screen.findByText("没有符合条件的节点。", { exact: true });

    await user.click(screen.getByRole("button", { name: "添加节点" }));
    const dialog = screen.getByRole("dialog", { name: "新建节点" });
    await user.selectOptions(within(dialog).getByLabelText("协议类型"), "vless");
    expect(within(within(dialog).getByLabelText("父节点")).getByRole("option", { name: "SG VLESS" })).toHaveValue("41");
  });

  it("exposes the legacy nested transport, uTLS, ECH, Reality, and multiplex controls", async () => {
    const api = nodeAPI([]);
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    await screen.findByRole("table", { name: "节点列表" });
    await user.click(screen.getByRole("button", { name: "添加节点" }));
    const dialog = screen.getByRole("dialog", { name: "新建节点" });
    const protocol = within(dialog).getByLabelText("协议类型");

    await user.selectOptions(protocol, "vmess");
    await user.selectOptions(within(dialog).getByLabelText("安全性"), "1");
    await user.click(within(dialog).getByLabelText("uTLS"));
    expect(within(within(dialog).getByLabelText("uTLS 指纹")).getAllByRole("option").map((option) => option.getAttribute("value"))).toEqual([
      "chrome", "firefox", "safari", "ios", "edge", "random"
    ]);
    await user.click(within(dialog).getByLabelText("ECH"));
    expect(within(dialog).getByLabelText("ECH Config")).toBeVisible();
    await user.click(within(dialog).getByLabelText("多路复用"));
    expect(within(dialog).getByLabelText("Brutal 加速")).toBeVisible();
    await user.click(within(dialog).getByLabelText("Brutal 加速"));
    expect(within(dialog).getByLabelText("Brutal 上行 (Mbps)")).toHaveValue(100);
    await user.selectOptions(within(dialog).getByLabelText("传输协议"), "ws");
    await user.click(within(dialog).getByRole("button", { name: "套用 WebSocket 模板" }));
    expect(within(dialog).getByLabelText("传输协议设置 (JSON)")).toHaveValue(JSON.stringify({ path: "/", headers: { Host: "v2ray.com" } }, null, 2));

    await user.selectOptions(protocol, "trojan");
    await user.selectOptions(within(dialog).getByLabelText("安全性"), "2");
    expect(within(dialog).getByLabelText("Reality 允许不安全连接")).not.toBeChecked();
  });

  it("validates advanced JSON and persists dynamic rate plus DNS certificate fields", async () => {
    const api = nodeAPI([]);
    api.createAdminNodeDefinition.mockResolvedValue(definition);
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    await screen.findByRole("table", { name: "节点列表" });
    await user.click(screen.getByRole("button", { name: "添加节点" }));
    const dialog = screen.getByRole("dialog", { name: "新建节点" });

    await user.type(within(dialog).getByLabelText("节点名称"), "Timed Shadowsocks");
    await user.type(within(dialog).getByLabelText("节点地址"), "timed.example.test");
    await user.click(within(dialog).getByLabelText("启用动态倍率"));
    await user.click(within(dialog).getByRole("button", { name: "添加时间段" }));
    fireEvent.change(within(dialog).getByLabelText("动态倍率 1 开始"), { target: { value: "01:15" } });
    fireEvent.change(within(dialog).getByLabelText("动态倍率 1 结束"), { target: { value: "05:45" } });
    fireEvent.change(within(dialog).getByLabelText("动态倍率 1 倍率"), { target: { value: "0.5" } });

    await user.click(within(dialog).getByText("高级设置"));
    await user.selectOptions(within(dialog).getByLabelText("证书模式"), "dns");
    await user.type(within(dialog).getByLabelText("证书域名"), "timed.example.test");
    await user.type(within(dialog).getByLabelText("DNS Provider"), "cloudflare");
    await user.type(within(dialog).getByLabelText("DNS 环境变量"), "CF_API_TOKEN=local-test-token");
    const outbounds = within(dialog).getByLabelText("自定义出站 (JSON 数组)");
    fireEvent.change(outbounds, { target: { value: "invalid" } });
    await user.click(within(dialog).getByRole("button", { name: "提交" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Unexpected token");
    expect(api.createAdminNodeDefinition).not.toHaveBeenCalled();

    fireEvent.change(outbounds, { target: { value: "[]" } });
    await user.click(within(dialog).getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.createAdminNodeDefinition).toHaveBeenCalledWith(expect.objectContaining({
      rate_time_enabled: true,
      rate_time_ranges: [{ start: "01:15", end: "05:45", rate: 0.5 }],
      certificate_config: {
        cert_mode: "dns", domain: "timed.example.test", dns_provider: "cloudflare",
        dns_env: { CF_API_TOKEN: "local-test-token" }
      }
    })));
  });

  it("requires confirmation for destructive bulk actions and keeps failures visible", async () => {
    const api = nodeAPI();
    api.deleteAdminNodes.mockRejectedValue(new Error("节点仍被子节点引用"));
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    expect(await screen.findByText("SG VLESS")).toBeVisible();

    await user.click(screen.getByRole("checkbox", { name: "选择节点：SG VLESS" }));
    await user.click(screen.getByRole("button", { name: "批量停用" }));
    await waitFor(() => expect(api.updateAdminNodeStates).toHaveBeenCalledWith({ targets: [{ id: 41, revision: 3 }], enabled: false }));
    await user.click(screen.getByRole("checkbox", { name: "选择节点：SG VLESS" }));
    await user.click(screen.getByRole("button", { name: "批量删除" }));
    const dialog = screen.getByRole("alertdialog", { name: "删除节点" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("节点仍被子节点引用");
    expect(api.deleteAdminNodes).toHaveBeenCalledWith([{ id: 41, revision: 3 }]);
  });
});

function nodeAPI(items = [node]) {
  return {
    listAdminNodes: vi.fn().mockResolvedValue({ items, total: items.length, page: 1, page_size: 500 }),
    listMachines: vi.fn().mockResolvedValue([machine]),
    listServerGroups: vi.fn().mockResolvedValue([group]),
    listRoutingRules: vi.fn().mockResolvedValue([route]),
    getAdminNodeDefinition: vi.fn().mockResolvedValue(definition),
    createAdminNodeDefinition: vi.fn(), replaceAdminNodeDefinition: vi.fn(),
    copyAdminNode: vi.fn(), reorderAdminNodes: vi.fn().mockResolvedValue(undefined),
    updateAdminNodeStates: vi.fn().mockResolvedValue(undefined), resetAdminNodeTraffic: vi.fn().mockResolvedValue(undefined),
    deleteAdminNodes: vi.fn()
  };
}
