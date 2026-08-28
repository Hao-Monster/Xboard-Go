import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NodeAgentSettings } from "../../lib/api";
import { NodeAgentSettingsPage } from "./NodeAgentSettingsPage";

const initial: NodeAgentSettings = {
  revision: 7,
  server_token_configured: true,
  server_token_prefix: "legacy-a",
  server_pull_interval: 60,
  server_push_interval: 60,
  device_limit_mode: 0,
  server_ws_enable: true,
  server_ws_url: "",
  websocket_available: true,
  legacy_http_auth_success_count: 12,
  legacy_websocket_auth_success_count: 3,
  legacy_last_used_at: "2026-08-28T12:00:00Z",
  updated_at: "2026-08-28T11:00:00Z"
};

describe("NodeAgentSettingsPage", () => {
  it("shows the five observable legacy controls without exposing the token or the API-only device mode", async () => {
    const api = { getNodeAgentSettings: vi.fn().mockResolvedValue(initial), updateNodeAgentSettings: vi.fn() };
    render(<NodeAgentSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "节点通讯设置" })).toBeVisible();
    expect(screen.getByText(/已配置（前缀 legacy-a…）/)).toBeVisible();
    expect(screen.getByLabelText("拉取间隔（秒）")).toHaveValue(60);
    expect(screen.getByLabelText("推送间隔（秒）")).toHaveValue(60);
    expect(screen.queryByLabelText("设备限制模式")).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "启用节点 WebSocket" })).toBeChecked();
    expect(screen.getByLabelText("WebSocket 地址")).toHaveValue("");
    expect(screen.queryByDisplayValue(/legacy-agent-token/i)).not.toBeInTheDocument();
    const telemetry = screen.getByRole("region", { name: "旧单节点使用情况" });
    expect(within(telemetry).getByText("12")).toBeVisible();
    expect(within(telemetry).getByText("3")).toBeVisible();
  });

  it("replaces and generates one-time tokens with optimistic revisions", async () => {
    const manual = "manual-node-agent-token-1234567890";
    const generated = "generated-node-agent-token-abcdefghijklmnopqrstuvwxyz";
    const api = {
      getNodeAgentSettings: vi.fn().mockResolvedValue(initial),
      updateNodeAgentSettings: vi.fn()
        .mockResolvedValueOnce({ ...initial, revision: 8, server_token_prefix: "manual-n", server_pull_interval: 31, server_push_interval: 29, server_ws_url: "wss://panel.example.test/ws", issued_token: manual })
        .mockResolvedValueOnce({ ...initial, revision: 9, server_token_prefix: "generate", issued_token: generated })
    };
    const user = userEvent.setup();
    render(<NodeAgentSettingsPage api={api} />);
    await screen.findByRole("heading", { name: "节点通讯设置" });

    await user.selectOptions(screen.getByLabelText("通讯密钥操作"), "replace");
    await user.type(screen.getByLabelText("新通讯密钥"), manual);
    await user.clear(screen.getByLabelText("拉取间隔（秒）"));
    await user.type(screen.getByLabelText("拉取间隔（秒）"), "31");
    await user.clear(screen.getByLabelText("推送间隔（秒）"));
    await user.type(screen.getByLabelText("推送间隔（秒）"), "29");
    await user.type(screen.getByLabelText("WebSocket 地址"), "wss://panel.example.test/ws");
    await user.click(screen.getByRole("button", { name: "保存节点配置" }));
    await waitFor(() => expect(api.updateNodeAgentSettings).toHaveBeenNthCalledWith(1, {
      revision: 7, server_token: manual, server_pull_interval: 31, server_push_interval: 29,
      device_limit_mode: 0, server_ws_enable: true, server_ws_url: "wss://panel.example.test/ws"
    }));
    expect(screen.getByRole("status")).toHaveTextContent(manual);
    await user.click(screen.getByRole("button", { name: "我已保存" }));
    expect(screen.queryByText(manual)).not.toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("通讯密钥操作"), "generate");
    await user.click(screen.getByRole("button", { name: "保存节点配置" }));
    await waitFor(() => expect(api.updateNodeAgentSettings).toHaveBeenNthCalledWith(2, expect.objectContaining({ revision: 8, generate_server_token: true })));
    expect(screen.getByRole("status")).toHaveTextContent(generated);
  });

  it("prevents enabling unavailable WebSocket capability and supports conflict recovery", async () => {
    const unavailable = { ...initial, server_ws_enable: false, websocket_available: false };
    const api = {
      getNodeAgentSettings: vi.fn().mockResolvedValue(unavailable),
      updateNodeAgentSettings: vi.fn().mockRejectedValue(new Error("设置已被其他管理员修改，请刷新后重试"))
    };
    const user = userEvent.setup();
    render(<NodeAgentSettingsPage api={api} />);
    const checkbox = await screen.findByRole("checkbox", { name: "启用节点 WebSocket" });
    expect(checkbox).toBeDisabled();
    expect(screen.getByText("当前部署没有启用 WebSocket 服务能力，管理设置不能绕过部署约束。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "保存节点配置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改");
    await user.click(screen.getByRole("button", { name: "刷新最新设置" }));
    await waitFor(() => expect(api.getNodeAgentSettings).toHaveBeenCalledTimes(2));
  });
});
