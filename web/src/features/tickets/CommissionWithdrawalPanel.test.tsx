import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Ticket } from "../../lib/api";
import { TicketDetailDialog } from "./TicketDetailDialog";

const withdrawal = {
  id: 7, user_id: 42, ticket_id: 11, amount: 25050, status: "pending" as const,
  method: "USDT", account: "wallet-42", payment_reference: "",
  created_at: "2026-09-08T10:00:00Z", updated_at: "2026-09-08T10:00:00Z"
};
const ticket: Ticket = {
  id: 11, user_id: 42, subject: "[提现申请] 本工单由系统发出", level: 2, status: 1, reply_status: 0,
  created_at: withdrawal.created_at, updated_at: withdrawal.updated_at, withdrawal
};

describe("Commission withdrawal in ticket details", () => {
  it("approves a closed ticket's withdrawal and confirms payment only with a receipt in an interactive nested dialog", async () => {
    const user = userEvent.setup();
    const transition = vi.fn()
      .mockResolvedValueOnce({ ...ticket, withdrawal: { ...withdrawal, status: "approved" } })
      .mockRejectedValueOnce(new Error("暂时无法保存，请重试"))
      .mockResolvedValueOnce({ ...ticket, withdrawal: { ...withdrawal, status: "paid", payment_reference: "receipt-42" } });
    render(<TicketDetailDialog ticketID={11} administrator load={vi.fn().mockResolvedValue(ticket)} reply={vi.fn()} close={vi.fn()} transitionWithdrawal={transition} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "批准提现" }));
    let confirmation = screen.getByRole("dialog", { name: "批准提现" });
    await user.click(within(confirmation).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(transition).toHaveBeenCalledWith(11, "approved", ""));
    await user.click(await screen.findByRole("button", { name: "确认已付款" }));
    confirmation = screen.getByRole("dialog", { name: "确认已付款" });
    expect(within(confirmation).getByRole("button", { name: "确认" })).toBeDisabled();
    await user.type(within(confirmation).getByLabelText("付款凭据"), "receipt-42");
    await user.click(within(confirmation).getByRole("button", { name: "确认" }));
    expect(await within(confirmation).findByRole("alert")).toHaveTextContent("暂时无法保存");
    expect(within(confirmation).getByLabelText("付款凭据")).toHaveValue("receipt-42");
    await user.click(within(confirmation).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "确认已付款" })).not.toBeInTheDocument());
    expect(transition).toHaveBeenNthCalledWith(2, 11, "paid", "receipt-42");
    expect(transition).toHaveBeenNthCalledWith(3, 11, "paid", "receipt-42");
    expect(screen.getByText("佣金提现 · 已付款")).toBeVisible();
    expect(screen.queryByRole("button", { name: "拒绝并退回佣金" })).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "工单详情" })).toBeVisible();
  });

  it("rejects with explicit confirmation and shows released funds", async () => {
    const user = userEvent.setup();
    const transition = vi.fn().mockResolvedValue({ ...ticket, withdrawal: { ...withdrawal, status: "rejected" } });
    render(<TicketDetailDialog ticketID={11} administrator load={vi.fn().mockResolvedValue(ticket)} reply={vi.fn()} close={vi.fn()} transitionWithdrawal={transition} onClose={vi.fn()} onUpdated={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "拒绝并退回佣金" }));
    expect(transition).not.toHaveBeenCalled();
    await user.click(within(screen.getByRole("dialog", { name: "拒绝并退回佣金" })).getByRole("button", { name: "确认" }));
    expect(await screen.findByText("佣金提现 · 已拒绝（金额已退回）")).toBeVisible();
    expect(transition).toHaveBeenCalledWith(11, "rejected", "");
  });

  it("lets the owner inspect the ledger without offering administrator controls", async () => {
    render(<TicketDetailDialog ticketID={11} load={vi.fn().mockResolvedValue(ticket)} reply={vi.fn()} close={vi.fn()} transitionWithdrawal={vi.fn()} onClose={vi.fn()} onUpdated={vi.fn()} />);
    expect(await screen.findByText("佣金提现 · 待审批")).toBeVisible();
    expect(screen.getByText("提现账号：wallet-42")).toBeVisible();
    expect(screen.queryByRole("button", { name: "批准提现" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "拒绝并退回佣金" })).not.toBeInTheDocument();
  });
});
