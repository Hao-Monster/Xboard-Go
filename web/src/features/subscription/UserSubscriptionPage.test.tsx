import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { UserSubscription } from "../../lib/api";
import { UserSubscriptionPage } from "./UserSubscriptionPage";

const current: UserSubscription = {
  plan_id: 7,
  token: "11112222333344445555666677778888",
  expired_at: "2026-09-25T00:00:00Z",
  u: 1 * 2 ** 30,
  d: 2 * 2 ** 30,
  transfer_enable: 10 * 2 ** 30,
  email: "subscriber@example.test",
  uuid: "11111111-2222-4333-8444-555555555555",
  device_limit: 3,
  speed_limit: 100,
  next_reset_at: "2026-09-02T00:00:00Z",
  plan: { name: "Premium", renew: true },
  subscribe_url: "https://panel.example.test/s/11112222333344445555666677778888",
  reset_day: 7,
  subscription_valid: true
};

describe("UserSubscriptionPage", () => {
  it("shows the observable Xboard subscription summary and one-click import actions", async () => {
    const api = {
      getSubscription: vi.fn().mockResolvedValue(current),
      getSubscriptionQR: vi.fn().mockResolvedValue({ subscribe_url: current.subscribe_url, qr_code: "data:image/svg+xml;base64,PHN2Zy8+" }),
      resetSubscriptionSecurity: vi.fn()
    };
    const user = userEvent.setup();
    const writeClipboard = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: writeClipboard } });
    render(<UserSubscriptionPage api={api} />);

    expect(await screen.findByRole("heading", { name: "我的订阅" })).toBeVisible();
    expect(screen.getByText("Premium")).toBeVisible();
    expect(screen.getByText("已用 3.00 GiB / 总计 10.00 GiB")).toBeVisible();
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "30");
    await user.click(screen.getByRole("button", { name: "一键订阅" }));
    const dialog = await screen.findByRole("dialog", { name: "一键订阅" });
    expect(await within(dialog).findByRole("img", { name: "订阅二维码" })).toHaveAttribute("src", "data:image/svg+xml;base64,PHN2Zy8+");
    expect(within(dialog).getByRole("link", { name: "导入到 Clash" })).toHaveAttribute("href", expect.stringContaining("clash://install-config?url="));
    expect(within(dialog).getByRole("link", { name: "导入到 Hiddify" })).toHaveAttribute("href", expect.stringContaining("hiddify://import/"));
    await user.click(within(dialog).getByRole("button", { name: "复制订阅地址" }));
    expect(writeClipboard).toHaveBeenCalledWith(current.subscribe_url);
    expect(await within(dialog).findByRole("status")).toHaveTextContent("订阅地址已复制");
  });

  it("requires an explicit confirmation and replaces both subscription secrets", async () => {
    const rotated: UserSubscription = {
      ...current,
      token: "99990000111122223333444455556666",
      uuid: "99999999-8888-4777-8666-555555555555",
      subscribe_url: "https://panel.example.test/s/99990000111122223333444455556666"
    };
    const api = {
      getSubscription: vi.fn().mockResolvedValue(current),
      getSubscriptionQR: vi.fn(),
      resetSubscriptionSecurity: vi.fn().mockResolvedValue(rotated)
    };
    const user = userEvent.setup();
    render(<UserSubscriptionPage api={api} />);
    await screen.findByRole("heading", { name: "我的订阅" });
    await user.click(screen.getByRole("button", { name: "重置订阅信息" }));
    const dialog = await screen.findByRole("dialog", { name: "重置订阅信息" });
    expect(within(dialog).getByText(/旧订阅地址会立即失效/)).toBeVisible();
    expect(api.resetSubscriptionSecurity).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "确认重置" }));
    await waitFor(() => expect(api.resetSubscriptionSecurity).toHaveBeenCalledTimes(1));
    expect(await screen.findByDisplayValue(rotated.subscribe_url)).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("订阅信息已重置");
  });
});
