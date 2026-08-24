import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Ticket } from "../../lib/api";
import { TicketManagementPage } from "./TicketManagementPage";

const ticket: Ticket = {
  id: 11, user_id: 42, user_email: "user@example.test", subject: "Route outage", level: 1,
  status: 0, reply_status: 0, created_at: "2026-08-24T10:00:00Z", updated_at: "2026-08-24T10:00:00Z"
};
const detail: Ticket = {
  ...ticket,
  messages: [{ id: 1, ticket_id: 11, is_me: true, message: "Please help", created_at: ticket.created_at, updated_at: ticket.updated_at }]
};

describe("TicketManagementPage", () => {
  it("filters, searches, views, replies to, and closes user tickets", async () => {
    const answered: Ticket = {
      ...detail, reply_status: 1,
      messages: [...(detail.messages ?? []), { id: 2, ticket_id: 11, is_me: false, message: "Resolved", created_at: "2026-08-24T10:01:00Z", updated_at: "2026-08-24T10:01:00Z" }]
    };
    const api = {
      listAdminTickets: vi.fn().mockResolvedValue({ items: [ticket], total: 1, page: 1, page_size: 20 }),
      getAdminTicket: vi.fn().mockResolvedValue(detail),
      replyAdminTicket: vi.fn().mockResolvedValue(answered),
      closeAdminTicket: vi.fn().mockResolvedValue({ ...answered, status: 1 })
    };
    const user = userEvent.setup();
    render(<TicketManagementPage api={api} />);

    expect(await screen.findByRole("heading", { name: "工单管理" })).toBeVisible();
    expect(screen.getByText("Route outage", { exact: true })).toBeVisible();
    await user.type(screen.getByRole("searchbox", { name: "搜索工单" }), "user@example.test");
    await user.selectOptions(screen.getByLabelText("工单级别"), "1");
    await user.selectOptions(screen.getByLabelText("回复状态"), "0");
    await user.click(screen.getByRole("button", { name: "查询工单" }));
    await waitFor(() => expect(api.listAdminTickets).toHaveBeenLastCalledWith({
      page: 1, page_size: 20, status: 0, level: 1, reply_status: 0, query: "user@example.test"
    }));

    await user.click(screen.getByRole("button", { name: "查看工单：Route outage" }));
    let dialog = await screen.findByRole("dialog", { name: "工单详情" });
    expect(within(dialog).getByText("Please help")).toBeVisible();
    await user.type(within(dialog).getByLabelText("回复内容"), "Resolved");
    await user.click(within(dialog).getByRole("button", { name: "回复" }));
    await waitFor(() => expect(api.replyAdminTicket).toHaveBeenCalledWith(11, "Resolved"));
    expect(await within(dialog).findByText("Resolved")).toBeVisible();
    expect(await screen.findByText("没有符合条件的工单。")).toBeVisible();

    await user.click(within(dialog).getByRole("button", { name: /^关闭工单$/ }));
    const confirmation = screen.getByRole("dialog", { name: "关闭工单" });
    await user.click(within(confirmation).getByRole("button", { name: "确认关闭" }));
    await waitFor(() => expect(api.closeAdminTicket).toHaveBeenCalledWith(11));
    expect(await within(dialog).findByText("已关闭")).toBeVisible();

    await user.click(within(dialog).getByRole("button", { name: "关闭工单详情" }));
    await user.click(screen.getByRole("button", { name: "已关闭" }));
    await waitFor(() => expect(api.listAdminTickets).toHaveBeenLastCalledWith({ page: 1, page_size: 20, status: 1 }));
    dialog = screen.queryByRole("dialog", { name: "工单详情" }) as HTMLElement;
    expect(dialog).not.toBeInTheDocument();
  });

  it("allows an administrator to answer a closed ticket without reopening it", async () => {
    const closed = { ...detail, status: 1 as const, reply_status: 1 as const };
    const api = {
      listAdminTickets: vi.fn().mockResolvedValue({ items: [closed], total: 1, page: 1, page_size: 20 }),
      getAdminTicket: vi.fn().mockResolvedValue(closed),
      replyAdminTicket: vi.fn().mockResolvedValue({ ...closed, messages: [...(closed.messages ?? []), { id: 3, ticket_id: 11, is_me: false, message: "Additional answer", created_at: ticket.updated_at, updated_at: ticket.updated_at }] }),
      closeAdminTicket: vi.fn()
    };
    const user = userEvent.setup();
    render(<TicketManagementPage api={api} initialStatus={1} />);
    await user.click(await screen.findByRole("button", { name: "查看工单：Route outage" }));
    const dialog = await screen.findByRole("dialog", { name: "工单详情" });
    await user.type(within(dialog).getByLabelText("回复内容"), "Additional answer");
    await user.click(within(dialog).getByRole("button", { name: "回复" }));
    await waitFor(() => expect(api.replyAdminTicket).toHaveBeenCalledWith(11, "Additional answer"));
    expect(within(dialog).getByText("已关闭")).toBeVisible();
  });
});
