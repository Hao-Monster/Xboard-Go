import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type {
  AdminDiagnosticsSettingsResponse,
  DiagnosticsAdminAPI,
  DiagnosticsConnectionResponse,
  DiagnosticsOrderLookupResponse,
  DiagnosticsQueryResponse,
  DiagnosticsStatusResponse,
} from "../../lib/api";
import { CustomerDiagnosticsPage } from "./CustomerDiagnosticsPage";

const mockSettings: AdminDiagnosticsSettingsResponse = {
  initialized: true,
  panel_scope: "panel-test-scope",
  query_endpoint: "http://127.0.0.1:8123",
  query_username: "default",
  has_query_password: false,
  has_cursor_signing_secret: false,
  storage_generation: 1,
};

const mockOrderLookup: DiagnosticsOrderLookupResponse = {
  order_id: 1001,
  trade_no: "202610250001",
  order_type: "新购",
  order_status: 3,
  customer_display_name: "test***@example.com",
  subscription_name: "旗舰高速套餐",
  diagnostic_id: "848d531a78dc1fcbb6ac065b",
};

const mockInactiveStatus: DiagnosticsStatusResponse = {
  order_id: 1001,
  trade_no: "202610250001",
  diagnostic_id: "848d531a78dc1fcbb6ac065b",
  has_session: false,
  coverage: {
    state: "full",
    reasons: [],
    nodes: [],
  },
};

const mockActiveStatus: DiagnosticsStatusResponse = {
  order_id: 1001,
  trade_no: "202610250001",
  diagnostic_id: "848d531a78dc1fcbb6ac065b",
  has_session: true,
  coverage: {
    state: "full",
    reasons: [],
    nodes: [],
  },
  session: {
    session_id: "sess-001",
    diagnostic_id: "848d531a78dc1fcbb6ac065b",
    operator_id: 1,
    state: "active",
    issued_at: "2026-10-25T03:00:00Z",
    expires_at: new Date(Date.now() + 14 * 60 * 1000).toISOString(),
    revision: 2,
    enabled: true,
    target_node_ids: [2],
    node_acks: {
      "2": {
        node_id: 2,
        effective: true,
        effective_revision: 2,
        effective_deadline: "2026-10-25T03:15:00Z",
        ack_received_at: "2026-10-25T03:00:01Z",
      },
    },
  },
};

const mockQueryResult: DiagnosticsQueryResponse = {
  diagnostic_id: "848d531a78dc1fcbb6ac065b",
  snapshot_id: "snap-001",
  as_of: "2026-10-25T03:05:00Z",
  items: [
    {
      node_id: 2,
      runtime_id: "rt-node-2",
      connection_id: "conn-100",
      target_id: 1,
      diagnostic_id: "848d531a78dc1fcbb6ac065b",
      observed_protocol: "tcp",
      observed_domain: "api.example.com",
      domain_source: "tls_sni",
      selected_target_address: "1.1.1.1",
      selected_target_port: 443,
      socket_peer: "192.168.1.100:54321",
      transport_state: "connected",
      has_error: true,
      last_error_stage: "proxy_handshake",
      last_reason_code: "upstream_reset",
      error_stages: ["proxy_handshake"],
      reason_codes: ["upstream_reset"],
      span_status: "error",
      start_time: "2026-10-25T03:04:00Z",
      last_event_time: "2026-10-25T03:04:10Z",
      event_count: 5,
      bytes_in: 1024,
      bytes_out: 2048,
      has_stack: true,
      stack_capture_kind: "proxy_error",
      payload_truncated: false,
    },
  ],
  next_cursor: null,
  truncated: false,
  scan_truncated: false,
  coverage: {
    state: "full",
    reasons: [],
    nodes: [
      {
        node_id: 2,
        capability: "supported",
        collector: "healthy",
        known_dropped_events: 0,
      },
    ],
  },
};

const mockConnectionDetail: DiagnosticsConnectionResponse = {
  node_id: 2,
  runtime_id: "rt-node-2",
  connection_id: "conn-100",
  diagnostic_id: "848d531a78dc1fcbb6ac065b",
  events: [
    {
      schema_version: 1,
      event_time: "2026-10-25T03:04:00Z",
      event_id: "evt-1",
      event_family: "connection",
      event: "connection_opened",
      connection_id: "conn-100",
      node_id: 2,
      span_name: "tcp_open",
      span_status: "ok",
      observed_domain: "api.example.com",
    },
    {
      schema_version: 1,
      event_time: "2026-10-25T03:04:10Z",
      event_id: "evt-3",
      event_family: "connection",
      event: "proxy_handshake_failed",
      connection_id: "conn-100",
      node_id: 2,
      span_name: "handshake",
      span_status: "error",
      stage: "proxy_handshake",
      last_reason_code: "upstream_reset",
      error_message: "remote peer closed connection during tls handshake",
      exception_stack: ["goroutine 42 [running]:", "main.handleTCP(0x123)", "\t/app/proxy.go:120"],
    },
  ],
  dns_exchange: [
    {
      schema_version: 1,
      event_time: "2026-10-25T03:04:02Z",
      event_id: "evt-2",
      event_family: "resolver_operation",
      event: "dns_resolved",
      connection_id: "conn-100",
      node_id: 2,
      resolver_operation_id: "res-001",
      span_name: "resolve_host",
      span_status: "ok",
      dns_answers: ["1.1.1.1, 1.0.0.1"],
      dns_server: "8.8.8.8",
      dns_transport: "UDP",
      dns_rcode: "NOERROR",
    },
  ],
};

function createMockAPI(): DiagnosticsAdminAPI {
  return {
    getDiagnosticsSettings: vi.fn().mockResolvedValue(mockSettings),
    updateDiagnosticsSettings: vi.fn().mockResolvedValue({
      ...mockSettings,
      query_endpoint: "http://10.0.0.1:8123",
      storage_generation: 2,
    }),
    initDiagnosticsScope: vi.fn().mockResolvedValue({
      panel_scope: "panel-test-scope",
      created: true,
    }),
    lookupDiagnosticsOrder: vi.fn().mockResolvedValue(mockOrderLookup),
    getDiagnosticsSessionStatus: vi.fn().mockResolvedValue(mockInactiveStatus),
    startDiagnosticsSession: vi.fn().mockResolvedValue({
      session_id: "sess-001",
      diagnostic_id: "848d531a78dc1fcbb6ac065b",
      order_id: 1001,
      state: "active",
      issued_at: "2026-10-25T03:00:00Z",
      expires_at: new Date(Date.now() + 14 * 60 * 1000).toISOString(),
      revision: 2,
      target_node_ids: [2],
    }),
    stopDiagnosticsSession: vi.fn().mockResolvedValue({
      session_id: "sess-001",
      diagnostic_id: "848d531a78dc1fcbb6ac065b",
      order_id: 1001,
      state: "stopping",
      issued_at: "2026-10-25T03:00:00Z",
      expires_at: new Date(Date.now() + 14 * 60 * 1000).toISOString(),
      revision: 3,
      target_node_ids: [2],
    }),
    queryDiagnosticsOrder: vi.fn().mockResolvedValue(mockQueryResult),
    connectionDiagnosticsOrder: vi.fn().mockResolvedValue(mockConnectionDetail),
  };
}

describe("CustomerDiagnosticsPage", () => {
  it("renders page header and scope configuration", async () => {
    const api = createMockAPI();
    render(<CustomerDiagnosticsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "客户连接诊断与日志中心" })).toBeVisible();
    expect(await screen.findByText(/Scope 已就绪/)).toBeVisible();
  });

  it("performs order lookup and renders sanitized identity card without secrets or tokens", async () => {
    const api = createMockAPI();
    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} />);

    const input = await screen.findByPlaceholderText("例如: 2026102512345678 或 1024");
    await user.type(input, "202610250001");
    await user.click(screen.getByRole("button", { name: "定位客户" }));

    await waitFor(() => {
      expect(api.lookupDiagnosticsOrder).toHaveBeenCalledWith({ trade_no: "202610250001" });
    });

    // Identity card rendered
    expect(await screen.findByLabelText("客户诊断身份卡")).toBeVisible();
    expect(screen.getByText("202610250001")).toBeVisible();
    expect(screen.getByText("#1001")).toBeVisible();
    expect(screen.getByText("test***@example.com")).toBeVisible();
    expect(screen.getByText("848d531a78dc1fcbb6ac065b")).toBeVisible();

    // Sensitive keys/tokens MUST NOT be present
    expect(screen.queryByText(/token/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/uuid/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/secret/i)).not.toBeInTheDocument();
  });

  it("handles 15-minute detailed diagnostic lease start and stop with node acks", async () => {
    const api = createMockAPI();
    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} initialTradeNo="202610250001" />);

    // Wait for initial order lookup
    expect(await screen.findByLabelText("客户诊断身份卡")).toBeVisible();
    expect(screen.getByText("未开启 (Baseline 模式)")).toBeVisible();

    const startBtn = screen.getByRole("button", { name: "开启详细诊断 (15分钟)" });
    expect(startBtn).toBeEnabled();

    // Mock API returning active status on start
    vi.mocked(api.getDiagnosticsSessionStatus).mockResolvedValueOnce(mockActiveStatus);
    await user.click(startBtn);

    await waitFor(() => {
      expect(api.startDiagnosticsSession).toHaveBeenCalledWith(
        expect.objectContaining({
          order_id: 1001,
          trade_no: "202610250001",
        })
      );
    });

    // Verify Active status and Node ACK breakdown
    expect(await screen.findByText(/已生效 \(Active\)/)).toBeVisible();
    expect(screen.getByLabelText("节点确认列表")).toBeVisible();
    expect(screen.getByText(/节点 #2: 已确认/)).toBeVisible();

    // Now stop the session
    const stopBtn = screen.getByRole("button", { name: "停止详细诊断" });
    vi.mocked(api.getDiagnosticsSessionStatus).mockResolvedValueOnce(mockInactiveStatus);
    await user.click(stopBtn);

    await waitFor(() => {
      expect(api.stopDiagnosticsSession).toHaveBeenCalledWith(
        expect.objectContaining({
          order_id: 1001,
          trade_no: "202610250001",
          session_id: "sess-001",
        })
      );
    });
  });

  it("switches to log ID read-only mode and isolates lease controls", async () => {
    const api = createMockAPI();
    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} />);

    const idModeBtn = await screen.findByRole("tab", { name: "客户日志 ID 模式 (只读)" });
    await user.click(idModeBtn);

    // Order input and lease controls should disappear
    expect(screen.queryByPlaceholderText("例如: 2026102512345678 或 1024")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "开启详细诊断 (15分钟)" })).not.toBeInTheDocument();

    // Diagnostic ID input should appear
    const idInput = screen.getByPlaceholderText("例如: 848d531a78dc1fcbb6ac065b");
    expect(idInput).toBeVisible();

    // Button disabled until 24 hex characters
    const queryBtn = screen.getByRole("button", { name: "只读查询" });
    expect(queryBtn).toBeDisabled();

    await user.type(idInput, "848d531a78dc1fcbb6ac065b");
    expect(queryBtn).toBeEnabled();

    await user.click(queryBtn);
    await waitFor(() => {
      expect(api.queryDiagnosticsOrder).toHaveBeenCalled();
    });
  });

  it("queries connections, renders table, and opens drawer with timeline and stack trace", async () => {
    const api = createMockAPI();
    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} initialTradeNo="202610250001" />);

    expect(await screen.findByLabelText("客户诊断身份卡")).toBeVisible();

    // Click query button
    const executeBtn = screen.getByRole("button", { name: "执行查询" });
    await user.click(executeBtn);

    await waitFor(() => {
      expect(api.queryDiagnosticsOrder).toHaveBeenCalled();
    });

    // Verify table items
    expect(await screen.findByText("api.example.com")).toBeVisible();
    expect(screen.getByText("upstream_reset")).toBeVisible();
    expect(screen.getByText("proxy_handshake")).toBeVisible();

    // Click drawer button
    const detailBtn = screen.getByRole("button", { name: "详情" });
    await user.click(detailBtn);

    await waitFor(() => {
      expect(api.connectionDiagnosticsOrder).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: "conn-100",
          node_id: 2,
        })
      );
    });

    // Drawer opens
    expect(await screen.findByLabelText("连接详情与事件流")).toBeVisible();
    expect(screen.getAllByText("upstream_reset").length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText("remote peer closed connection during tls handshake")).toBeVisible();

    // Switch drawer tabs: DNS & Stack trace
    const dnsTab = screen.getByRole("button", { name: /DNS 解析/ });
    await user.click(dnsTab);
    expect(await screen.findByText("1.1.1.1, 1.0.0.1")).toBeVisible();

    const stackTab = screen.getByRole("button", { name: /异常调用栈/ });
    await user.click(stackTab);
    expect(await screen.findByText(/main\.handleTCP/)).toBeVisible();

    // Close drawer
    const closeBtn = screen.getByRole("button", { name: "关闭详情抽屉" });
    await user.click(closeBtn);
    expect(screen.queryByLabelText("连接详情与事件流")).not.toBeInTheDocument();
  });

  it("supports initializing scope when not ready", async () => {
    const api = createMockAPI();
    vi.mocked(api.getDiagnosticsSettings).mockResolvedValueOnce({
      ...mockSettings,
      initialized: false,
    });

    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} />);

    const initBtn = await screen.findByRole("button", { name: "初始化诊断 Scope" });
    expect(initBtn).toBeVisible();

    await user.click(initBtn);
    await waitFor(() => {
      expect(api.initDiagnosticsScope).toHaveBeenCalled();
    });
  });

  it("opens settings modal and updates storage settings", async () => {
    const api = createMockAPI();
    const user = userEvent.setup();
    render(<CustomerDiagnosticsPage api={api} />);

    const configBtn = await screen.findByRole("button", { name: "存储配置" });
    await user.click(configBtn);

    expect(await screen.findByRole("heading", { name: "诊断存储与端点配置" })).toBeVisible();

    const endpointInput = screen.getByLabelText("VictoriaLogs 查询端点 (Query Endpoint)");
    await user.clear(endpointInput);
    await user.type(endpointInput, "http://10.0.0.1:8123");

    const saveBtn = screen.getByRole("button", { name: "保存配置" });
    await user.click(saveBtn);

    await waitFor(() => {
      expect(api.updateDiagnosticsSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          query_endpoint: "http://10.0.0.1:8123",
        })
      );
    });
  });
});
