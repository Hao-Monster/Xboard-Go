import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { InvitationPage } from "./InvitationPage";

describe("InvitationPage", () => {
  it("loads all five legacy statistics, commission history, transfers money, and generates a code", async () => {
    const initial = {
      codes: [], invited_count: 2, valid_commission: 12_345, pending_commission: 678, commission_rate: 20,
      commission_distribution_enabled: true, commission_distribution_rates: [10, 6, 4], available_commission: 9_999
    };
    const generated = { code: "Abcd1234", pv: 0, created_at: "2026-08-25T04:00:00Z" };
    const history = { items: [{ id: 1, trade_no: "2026082600000000000000001", order_amount: 50_000, get_amount: 10_000, created_at: "2026-08-26T04:00:00Z" }], total: 1, page: 1, page_size: 50 };
    const api = {
      getInvitations: vi.fn()
        .mockResolvedValueOnce(initial)
        .mockResolvedValueOnce({ ...initial, available_commission: 8_765 })
        .mockResolvedValueOnce({ ...initial, codes: [generated], available_commission: 8_765 }),
      createInvitation: vi.fn().mockResolvedValue(generated),
      listCommissionLogs: vi.fn().mockResolvedValue(history),
      transferCommission: vi.fn().mockResolvedValue({ commission_balance: 8_765, balance: 1_234 }),
      getCommissionWithdrawalPolicy: vi.fn().mockResolvedValue({ currency: "CNY", minimum_amount: 10_000, methods: ["USDT"], available_commission: 9_999, frozen_commission: 0, active: null }),
      listCommissionWithdrawals: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      createCommissionWithdrawal: vi.fn()
    };
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<InvitationPage api={api} />);

    expect(await screen.findByRole("heading", { name: "我的邀请" })).toBeVisible();
    expect(await screen.findByText("已邀请用户", { exact: true })).toBeVisible();
    expect(screen.getByText("¥123.45", { exact: true })).toBeVisible();
    expect(screen.getByText("¥6.78", { exact: true })).toBeVisible();
    expect(screen.getByText("10% / 6% / 4%", { exact: true })).toBeVisible();
    expect(screen.getAllByText("¥99.99", { exact: true })).toHaveLength(2);
    expect(screen.getByText("2026082600000000000000001", { exact: true })).toBeVisible();
    expect(screen.getByRole("heading", { name: "邀请码" })).toBeVisible();
    expect(screen.getByText("暂无可用邀请码", { exact: true })).toBeVisible();

    await user.type(screen.getByLabelText("划转金额（CNY）"), "12.34");
    await user.click(screen.getByRole("button", { name: "佣金划转余额" }));
    await waitFor(() => expect(api.transferCommission).toHaveBeenCalledWith(1_234));
    expect(await screen.findByRole("status")).toHaveTextContent("操作成功");

    await user.click(screen.getByRole("button", { name: "生成邀请码" }));
    await waitFor(() => expect(api.createInvitation).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Abcd1234", { exact: true })).toBeVisible();
    expect(screen.getByText("0", { exact: true })).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("邀请码已生成");
    expect(api.getInvitations).toHaveBeenCalledTimes(3);
    await user.click(screen.getByRole("button", { name: "复制邀请链接" }));
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/#/register?code=Abcd1234`);
    expect(screen.getByRole("status")).toHaveTextContent("邀请链接已复制");
  });

  it("keeps existing data visible when generation reaches the limit", async () => {
    const api = {
      getInvitations: vi.fn().mockResolvedValue({
        codes: [{ code: "Keep1234", pv: 7, created_at: "2026-08-25T04:00:00Z" }], invited_count: 1,
        valid_commission: 0, pending_commission: 0, commission_rate: 10,
        commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0
      }),
      createInvitation: vi.fn().mockRejectedValue(new Error("已达到创建数量上限")),
      listCommissionLogs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }),
      transferCommission: vi.fn(),
      getCommissionWithdrawalPolicy: vi.fn().mockResolvedValue({ currency: "CNY", minimum_amount: 10_000, methods: ["USDT"], available_commission: 0, frozen_commission: 0, active: null }),
      listCommissionWithdrawals: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      createCommissionWithdrawal: vi.fn()
    };
    const user = userEvent.setup();
    render(<InvitationPage api={api} />);

    expect(await screen.findByText("Keep1234", { exact: true })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "生成邀请码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("已达到创建数量上限");
    expect(screen.getByText("Keep1234", { exact: true })).toBeVisible();
    expect(api.getInvitations).toHaveBeenCalledTimes(1);
  });

  it("submits one full-balance withdrawal and never renders the plaintext account", async () => {
    const created = { id: 7, user_id: 2, amount: 15_000, fee_basis_points: 0, fee_amount: 0, net_amount: 15_000, currency: "CNY", method: "USDT", account_masked: "****6789", status: "pending" as const, revision: 1, created_at: "2026-08-26T04:00:00Z", updated_at: "2026-08-26T04:00:00Z", approved_at: null, paid_at: null, rejected_at: null };
    const initialPolicy = { currency: "CNY", minimum_amount: 10_000, methods: ["USDT"], available_commission: 15_000, frozen_commission: 0, active: null };
    const api = {
      getInvitations: vi.fn().mockResolvedValue({ ...emptyInvitationSummary, available_commission: 15_000 }),
      createInvitation: vi.fn(), listCommissionLogs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }), transferCommission: vi.fn(),
      getCommissionWithdrawalPolicy: vi.fn().mockResolvedValueOnce(initialPolicy).mockResolvedValue({ ...initialPolicy, available_commission: 0, frozen_commission: 15_000, active: created }),
      listCommissionWithdrawals: vi.fn().mockResolvedValueOnce({ items: [], total: 0, page: 1, page_size: 20 }).mockResolvedValue({ items: [created], total: 1, page: 1, page_size: 20 }),
      createCommissionWithdrawal: vi.fn().mockResolvedValue(created)
    };
    const user = userEvent.setup();
    render(<InvitationPage api={api} />);
    await screen.findByRole("heading", { name: "佣金提现" });
    const account = "wallet-123456789";
    await user.type(screen.getByLabelText("收款账户"), account);
    await user.click(screen.getByRole("button", { name: "提交提现申请" }));
    await waitFor(() => expect(api.createCommissionWithdrawal).toHaveBeenCalledWith(expect.any(String), "USDT", account));
    expect(await screen.findByText("****6789", { exact: true })).toBeVisible();
    expect(screen.queryByText(account, { exact: true })).not.toBeInTheDocument();
  });
});

const emptyInvitationSummary = {
  codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0, commission_rate: 0,
  commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0
};
