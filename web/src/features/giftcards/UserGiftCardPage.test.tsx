import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UserGiftCardPage } from "./UserGiftCardPage";

describe("UserGiftCardPage", () => {
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
