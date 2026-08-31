import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { TrustedPlugin } from "../../lib/api";
import { PluginManagementPage } from "./PluginManagementPage";

const telegramConfig = {
  enable_ticket_notify: true,
  enable_payment_notify: true,
  start_welcome_title: "欢迎",
  start_bot_description: "机器人说明",
  start_bind_guide: "绑定说明",
  start_unbind_guide: "解绑说明",
  start_bind_commands: "绑定命令",
  start_footer: "页脚",
  help_text: "帮助"
};

const plugins: TrustedPlugin[] = [
  { code: "telegram", name: "Telegram Bot", type: "feature", version: "1.0.1", enabled: true, config: telegramConfig, revision: 1, updated_at: "1970-01-01T00:00:00Z" },
  { code: "epay", name: "EPay", type: "payment", version: "1.0.0", enabled: true, config: {}, revision: 1, updated_at: "1970-01-01T00:00:00Z" }
];

describe("PluginManagementPage", () => {
  it("lists only trusted built-ins, confirms disable, and routes payment configuration", async () => {
    const updateTrustedPlugin = vi.fn().mockImplementation((code: string, input: { enabled: boolean }) => Promise.resolve({
      ...plugins.find((plugin) => plugin.code === code)!, enabled: input.enabled, revision: 2
    }));
    const api = {
      listTrustedPlugins: vi.fn().mockResolvedValue(plugins),
      updateTrustedPlugin
    };
    const navigate = vi.fn();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    render(<PluginManagementPage api={api} onNavigate={navigate} />);

    expect(await screen.findByRole("heading", { name: "插件管理" })).toBeVisible();
    expect(screen.getByText("Telegram Bot")).toBeVisible();
    expect(screen.getByText("EPay")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "禁用：EPay" }));
    await waitFor(() => expect(updateTrustedPlugin).toHaveBeenCalledWith("epay", {
      revision: 1, enabled: false, config: {}
    }));
    expect(confirm).toHaveBeenCalledWith("确认禁用插件“EPay”？相关新业务入口会立即停止，但历史数据仍会保留。");
    expect(await screen.findByRole("button", { name: "启用：EPay" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "支付配置：EPay" }));
    expect(navigate).toHaveBeenCalledWith("payments");
  });

  it("updates the versioned Telegram plugin configuration without dropping fields", async () => {
    const updated = { ...plugins[0]!, config: { ...telegramConfig, enable_ticket_notify: false, help_text: "新帮助" }, revision: 2 };
    const updateTrustedPlugin = vi.fn().mockResolvedValue(updated);
    const api = {
      listTrustedPlugins: vi.fn().mockResolvedValue([plugins[0]]),
      updateTrustedPlugin
    };
    const user = userEvent.setup();
    render(<PluginManagementPage api={api} onNavigate={vi.fn()} />);

    await user.click(await screen.findByRole("button", { name: "插件配置：Telegram Bot" }));
    const dialog = screen.getByRole("dialog", { name: "Telegram 插件配置" });
    await user.click(within(dialog).getByLabelText("工单通知"));
    await user.clear(within(dialog).getByLabelText("帮助文案"));
    await user.type(within(dialog).getByLabelText("帮助文案"), "新帮助");
    await user.click(within(dialog).getByRole("button", { name: "保存插件配置" }));

    await waitFor(() => expect(updateTrustedPlugin).toHaveBeenCalledWith("telegram", {
      revision: 1,
      enabled: true,
      config: { ...telegramConfig, enable_ticket_notify: false, help_text: "新帮助" }
    }));
    expect(screen.queryByRole("dialog", { name: "Telegram 插件配置" })).not.toBeInTheDocument();
  });
});
