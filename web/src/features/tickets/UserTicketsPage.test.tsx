import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Ticket } from "../../lib/api";
import { UserTicketsPage } from "./UserTicketsPage";

const openTicket: Ticket = {
  id: 7, user_id: 12, subject: "Cannot connect", level: 2, status: 0, reply_status: 0,
  created_at: "2026-08-24T10:00:00Z", updated_at: "2026-08-24T10:00:00Z"
};
const detail: Ticket = {
  ...openTicket,
  messages: [{ id: 1, ticket_id: 7, is_me: true, message: "Initial message", created_at: openTicket.created_at, updated_at: openTicket.updated_at }]
};

describe("UserTicketsPage", () => {
  it("creates, views, replies to, and closes a ticket through the legacy user workflow", async () => {
    const replied: Ticket = {
      ...detail, updated_at: "2026-08-24T10:01:00Z",
      messages: [...(detail.messages ?? []), { id: 2, ticket_id: 7, is_me: true, message: "Follow-up", created_at: "2026-08-24T10:01:00Z", updated_at: "2026-08-24T10:01:00Z" }]
    };
    const api = {
      listTickets: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      createTicket: vi.fn().mockResolvedValue(openTicket),
      getTicket: vi.fn().mockResolvedValue(detail),
      replyTicket: vi.fn().mockResolvedValue(replied),
      closeTicket: vi.fn().mockResolvedValue({ ...openTicket, status: 1 })
    };
    const user = userEvent.setup();
    render(<UserTicketsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "我的工单" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "新建工单" }));
    let dialog = screen.getByRole("dialog", { name: "新建工单" });
    await user.type(within(dialog).getByLabelText("主题"), "Cannot connect");
    await user.selectOptions(within(dialog).getByLabelText("工单级别"), "2");
    await user.type(within(dialog).getByLabelText("消息"), "Initial message");
    await user.click(within(dialog).getByRole("button", { name: "创建工单" }));
    await waitFor(() => expect(api.createTicket).toHaveBeenCalledWith({ subject: "Cannot connect", level: 2, message: "Initial message" }));
    expect(await screen.findByText("Cannot connect", { exact: true })).toBeVisible();
    expect(screen.getByText("高")).toBeVisible();
    expect(screen.getByText("待回复")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "查看工单：Cannot connect" }));
    dialog = await screen.findByRole("dialog", { name: "工单详情" });
    expect(within(dialog).getByText("Initial message")).toBeVisible();
    await user.type(within(dialog).getByLabelText("回复内容"), "Follow-up");
    await user.click(within(dialog).getByRole("button", { name: "回复" }));
    await waitFor(() => expect(api.replyTicket).toHaveBeenCalledWith(7, "Follow-up"));
    expect(await within(dialog).findByText("Follow-up")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: /^关闭工单$/ }));
    const confirmation = screen.getByRole("dialog", { name: "关闭工单" });
    await user.click(within(confirmation).getByRole("button", { name: "确认关闭" }));
    await waitFor(() => expect(api.closeTicket).toHaveBeenCalledWith(7));
    expect(await within(dialog).findByText("已关闭")).toBeVisible();
    expect(within(dialog).queryByLabelText("回复内容")).not.toBeInTheDocument();
  });

  it("keeps the create dialog open when the server rejects a second open ticket", async () => {
    const api = {
      listTickets: vi.fn().mockResolvedValue({ items: [openTicket], total: 1, page: 1, page_size: 20 }),
      createTicket: vi.fn().mockRejectedValue(new Error("存在未关闭的工单")),
      getTicket: vi.fn(), replyTicket: vi.fn(), closeTicket: vi.fn()
    };
    const user = userEvent.setup();
    render(<UserTicketsPage api={api} />);
    expect(await screen.findByText("Cannot connect", { exact: true })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "新建工单" }));
    const dialog = screen.getByRole("dialog", { name: "新建工单" });
    await user.type(within(dialog).getByLabelText("主题"), "Second issue");
    await user.type(within(dialog).getByLabelText("消息"), "Second message");
    await user.click(within(dialog).getByRole("button", { name: "创建工单" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("存在未关闭的工单");
    expect(dialog).toBeVisible();
  });
});
