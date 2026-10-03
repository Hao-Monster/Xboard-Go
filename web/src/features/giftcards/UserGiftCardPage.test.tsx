import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UserGiftCardPage } from "./UserGiftCardPage";

describe("UserGiftCardPage", () => {
  it("previews accumulated traffic without clearing usage and exposes the purchase order", async () => {
    const rewards = { purchase_snapshot: { plan_id: 9, plan_name: "固定 200GB", period: "onetime", transfer_enable: 200 * 1073741824 } };
    const api = { checkGiftCard: vi.fn().mockResolvedValue({ template: { type: 4, name: "购买码" }, reward_preview: rewards, can_redeem: true, purchase_preview: { transfer_before: 200 * 1073741824, transfer_after: 400 * 1073741824, used_traffic: 150 * 1073741824, expires_before: null, expires_after: null, renewal: true } }), redeemGiftCard: vi.fn().mockResolvedValue({ message: "兑换成功", rewards }), listMyGiftCardUsages: vi.fn().mockResolvedValue({ items: [{ id: 1, template_name: "购买码", rewards, used_at: "2026-10-02T00:00:00Z", order_trade_no: "PURCHASE001" }], total: 1, page: 1, page_size: 15 }) };
    const user = userEvent.setup(); render(<UserGiftCardPage api={api} />);
    await user.type(screen.getByLabelText("礼品卡兑换码"), "GCABCDEFGH"); await user.click(screen.getByRole("button", { name: "查询奖励" }));
    expect(await screen.findByText("200 GB → 400 GB")).toBeVisible();
    expect(screen.getByText("150 GB")).toBeVisible(); expect(screen.getByText("250 GB")).toBeVisible();
    expect(screen.getByText(/请先取消待支付订单/)).toBeVisible();
    expect(screen.getByText("PURCHASE001")).toBeVisible();
    expect(api.redeemGiftCard).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "确认兑换" }));
    await waitFor(() => expect(api.redeemGiftCard).toHaveBeenCalledWith("GCABCDEFGH"));
  });

  it("previews before redeeming and refreshes masked history", async () => {
    const api = {
      checkGiftCard: vi.fn().mockResolvedValue({ template: { name: "欢迎卡" }, code_info: { code: "GCABCDEFGH1234" }, reward_preview: { balance: 500 }, can_redeem: true, reason: "" }),
      redeemGiftCard: vi.fn().mockResolvedValue({ message: "兑换成功！", rewards: { balance: 500 }, invite_rewards: {}, template_name: "欢迎卡", usage: {} }),
      listMyGiftCardUsages: vi.fn().mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 15 }).mockResolvedValue({ items: [{ id: 1, code: "GCABCDEFGH1234", template_name: "欢迎卡", rewards: { balance: 500 }, used_at: "2026-08-26T00:00:00Z" }], total: 1, page: 1, page_size: 15 })
    };
    const user = userEvent.setup(); render(<UserGiftCardPage api={api} />);
    await user.type(screen.getByLabelText("礼品卡兑换码"), "gcabcdefgh1234"); await user.click(screen.getByRole("button", { name: "查询奖励" }));
    expect(await screen.findByText("欢迎卡")).toBeVisible(); expect(screen.getByText("余额 ¥5.00")).toBeVisible();
    expect(api.redeemGiftCard).not.toHaveBeenCalled(); await user.click(screen.getByRole("button", { name: "确认兑换" }));
    await waitFor(() => expect(api.redeemGiftCard).toHaveBeenCalledWith("GCABCDEFGH1234"));
    expect(await screen.findByText(/兑换成功/)).toBeVisible(); expect(await screen.findByText("GCABCDEF****")).toBeVisible();
  });

  it("previews cycle expiry extension while preserving used traffic", async () => {
    const api = { checkGiftCard: vi.fn().mockResolvedValue({ template: { type: 4, name: "周期购买码" }, reward_preview: { purchase_snapshot: { plan_id: 9, plan_name: "月度套餐", period: "monthly", transfer_enable: 200 * 1073741824 } }, can_redeem: true, purchase_preview: { transfer_before: 200 * 1073741824, transfer_after: 200 * 1073741824, used_traffic: 150 * 1073741824, expires_before: "2026-11-01T00:00:00Z", expires_after: "2026-12-01T00:00:00Z", renewal: true } }), redeemGiftCard: vi.fn(), listMyGiftCardUsages: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 15 }) };
    const user = userEvent.setup(); render(<UserGiftCardPage api={api} />);
    await user.type(screen.getByLabelText("礼品卡兑换码"), "GCABCDEFGH"); await user.click(screen.getByRole("button", { name: "查询奖励" }));
    expect(await screen.findByText("200 GB → 200 GB")).toBeVisible();
    expect(screen.getByText("150 GB")).toBeVisible();
    const date = (value: string) => new Date(value).toLocaleString("zh-CN", { hour12: false });
    expect(screen.getByText(`${date("2026-11-01T00:00:00Z")} → ${date("2026-12-01T00:00:00Z")}`)).toBeVisible();
  });

  it("shows an eligibility reason and never offers redemption", async () => {
    const api = { checkGiftCard: vi.fn().mockResolvedValue({ template: { name: "套餐卡" }, code_info: {}, reward_preview: { plan_id: 9 }, can_redeem: false, reason: "已有有效套餐" }), redeemGiftCard: vi.fn(), listMyGiftCardUsages: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 15 }) };
    const user = userEvent.setup(); render(<UserGiftCardPage api={api} />); await user.type(screen.getByLabelText("礼品卡兑换码"), "GCABCDEFGH"); await user.click(screen.getByRole("button", { name: "查询奖励" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("已有有效套餐"); expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
  });

  it("reports a history failure instead of presenting it as an empty history", async () => {
	const api = { checkGiftCard: vi.fn(), redeemGiftCard: vi.fn(), listMyGiftCardUsages: vi.fn().mockRejectedValue(new Error("记录服务暂不可用")) };
	render(<UserGiftCardPage api={api} />);
	expect(await screen.findByRole("alert")).toHaveTextContent("记录服务暂不可用");
	expect(screen.queryByText("暂无兑换记录")).not.toBeInTheDocument();
  });
});
