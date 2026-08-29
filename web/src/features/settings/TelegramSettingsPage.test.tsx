import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { TelegramSettings } from "../../lib/api";
import { TelegramSettingsPage } from "./TelegramSettingsPage";

const initial: TelegramSettings = {
  revision: 7,
  telegram_bot_enable: true,
  telegram_bot_token_set: true,
  telegram_webhook_url: "https://panel.example.test",
  telegram_discuss_link: "https://t.me/xboard_group",
  telegram_bot_username: "xboard_test_bot",
  telegram_webhook_configured_at: "2026-08-29T14:00:00Z",
  updated_at: "2026-08-29T14:00:00Z"
};

describe("TelegramSettingsPage", () => {
  it("loads every legacy control on first open without exposing the token", async () => {
    const api = {
      getTelegramSettings: vi.fn().mockResolvedValue(initial),
      updateTelegramSettings: vi.fn(),
      provisionTelegramWebhook: vi.fn()
    };
    render(<TelegramSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "Telegram 设置" })).toBeVisible();
    expect(screen.getByLabelText("机器人令牌")).toHaveAttribute("type", "password");
    expect(screen.getByLabelText("机器人令牌")).toHaveValue("");
    expect(screen.getByText("令牌已安全配置，留空保存将保持不变。")).toBeVisible();
    expect(screen.getByLabelText("Webhook Base URL")).toHaveValue("https://panel.example.test");
    expect(screen.getByRole("checkbox", { name: "启用 Telegram 绑定引导" })).toBeChecked();
    expect(screen.getByLabelText("群组链接")).toHaveValue("https://t.me/xboard_group");
    const provision = screen.getByRole("button", { name: "一键设置 Webhook" });
    expect(provision).toBeEnabled();
    await userEvent.type(screen.getByLabelText("群组链接"), "-unsaved");
    expect(provision).toBeDisabled();
  });

  it("preserves a blank token, supports replacement, provisioning and explicit clearing", async () => {
    const user = userEvent.setup();
    const updated = { ...initial, revision: 8 };
    const api = {
      getTelegramSettings: vi.fn().mockResolvedValue(initial),
      updateTelegramSettings: vi.fn().mockResolvedValue(updated),
      provisionTelegramWebhook: vi.fn().mockResolvedValue({
        webhook_url: "https://panel.example.test/api/v1/guest/telegram/webhook",
        webhook_base_url: "https://panel.example.test",
        bot_username: "xboard_test_bot",
        configured_at: "2026-08-29T14:05:00Z",
        settings: { ...updated, revision: 9 }
      })
    };
    render(<TelegramSettingsPage api={api} />);
    await screen.findByRole("heading", { name: "Telegram 设置" });

    await user.click(screen.getByRole("button", { name: "保存 Telegram 设置" }));
    await waitFor(() => expect(api.updateTelegramSettings).toHaveBeenNthCalledWith(1, {
      revision: 7,
      telegram_bot_enable: true,
      telegram_webhook_url: "https://panel.example.test",
      telegram_discuss_link: "https://t.me/xboard_group"
    }));

    await user.type(screen.getByLabelText("机器人令牌"), "123456789:abcdefghijklmnopqrstuvwxyz_123456");
    await user.click(screen.getByRole("button", { name: "保存 Telegram 设置" }));
    await waitFor(() => expect(api.updateTelegramSettings).toHaveBeenNthCalledWith(2, expect.objectContaining({
      revision: 8,
      telegram_bot_token: "123456789:abcdefghijklmnopqrstuvwxyz_123456"
    })));

    await user.click(screen.getByRole("button", { name: "一键设置 Webhook" }));
    await waitFor(() => expect(api.provisionTelegramWebhook).toHaveBeenCalledWith(8));
    const status = await screen.findByRole("status");
    expect(within(status).getByText(/Webhook 已设置/)).toBeVisible();
    expect(status).not.toHaveTextContent("secret");

    await user.click(screen.getByRole("button", { name: "清除机器人令牌" }));
    await user.click(screen.getByRole("button", { name: "保存 Telegram 设置" }));
    await waitFor(() => expect(api.updateTelegramSettings).toHaveBeenLastCalledWith(expect.objectContaining({
      clear_telegram_bot_token: true,
      telegram_bot_enable: false
    })));
  });

  it("recovers from an initial load failure and requires a saved token before provisioning", async () => {
    const user = userEvent.setup();
    const withoutToken: TelegramSettings = {
      ...initial, telegram_bot_enable: false, telegram_bot_token_set: false,
      telegram_bot_username: "", telegram_webhook_configured_at: null
    };
    const api = {
      getTelegramSettings: vi.fn().mockRejectedValueOnce(new Error("Telegram 设置暂时不可用")).mockResolvedValue(withoutToken),
      updateTelegramSettings: vi.fn().mockResolvedValue({ ...withoutToken, revision: 8, telegram_bot_enable: true, telegram_bot_token_set: true }),
      provisionTelegramWebhook: vi.fn()
    };
    render(<TelegramSettingsPage api={api} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("Telegram 设置暂时不可用");
    await user.click(screen.getByRole("button", { name: "重新加载 Telegram 设置" }));
    expect(await screen.findByRole("heading", { name: "Telegram 设置" })).toBeVisible();
    const provision = screen.getByRole("button", { name: "一键设置 Webhook" });
    expect(provision).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "启用 Telegram 绑定引导" }));
    await user.type(screen.getByLabelText("机器人令牌"), "123456789:abcdefghijklmnopqrstuvwxyz_123456");
    expect(provision).toBeDisabled();
    await user.clear(screen.getByLabelText("Webhook Base URL"));
    await user.type(screen.getByLabelText("Webhook Base URL"), "https://new.example.test");
    await user.clear(screen.getByLabelText("群组链接"));
    await user.type(screen.getByLabelText("群组链接"), "https://t.me/new_group");
    await user.click(screen.getByRole("button", { name: "保存 Telegram 设置" }));
    await waitFor(() => expect(provision).toBeEnabled());
  });

  it("keeps failures visible and reloads the latest revision", async () => {
    const user = userEvent.setup();
    const api = {
      getTelegramSettings: vi.fn().mockResolvedValue(initial),
      updateTelegramSettings: vi.fn().mockRejectedValue(new Error("设置已被其他管理员修改")),
      provisionTelegramWebhook: vi.fn().mockRejectedValue(new Error("Telegram 网络不可用"))
    };
    render(<TelegramSettingsPage api={api} />);
    await screen.findByRole("heading", { name: "Telegram 设置" });

    await user.click(screen.getByRole("button", { name: "保存 Telegram 设置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改");
    await user.click(screen.getByRole("button", { name: "刷新最新设置" }));
    await waitFor(() => expect(api.getTelegramSettings).toHaveBeenCalledTimes(2));
    await user.click(screen.getByRole("button", { name: "一键设置 Webhook" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Telegram 网络不可用");
  });
});
