import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Ticket } from "../../lib/api";
import { TicketDetailDialog } from "./TicketDetailDialog";

const ticket: Ticket = {
  id: 7, user_id: 12, subject: "QA Ticket", level: 1, status: 0, reply_status: 0,
  created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z", messages: []
};

describe("ticket confirmation overlay", () => {
  it.each([false, true])("preserves the reply draft and focus when Escape cancels confirmation (admin=%s)", async administrator => {
    const load = vi.fn().mockResolvedValue(ticket);
    const reply = vi.fn().mockResolvedValue(ticket);
    const close = vi.fn();
    const onClose = vi.fn();
    const onUpdated = vi.fn();
    function Example() {
      const [open, setOpen] = useState(true);
      return open && <TicketDetailDialog ticketID={ticket.id} administrator={administrator} load={load} reply={reply} close={close}
        onClose={() => { setOpen(false); onClose(); }} onUpdated={onUpdated} />;
    }
    const user = userEvent.setup();
    render(<Example />);
    const draft = await screen.findByLabelText("回复内容");
    await user.type(draft, "QA unsent reply");
    const closeButton = screen.getByRole("button", { name: "关闭工单" });
    await user.click(closeButton);
    expect(screen.getByRole("dialog", { name: "关闭工单" })).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "关闭工单" })).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "工单详情" })).toBeVisible();
    expect(draft).toHaveValue("QA unsent reply");
    expect(onClose).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    await waitFor(() => expect(closeButton).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "回复" }));
    await waitFor(() => expect(reply).toHaveBeenCalledExactlyOnceWith(ticket.id, "QA unsent reply"));
    expect(onUpdated).toHaveBeenCalledWith(ticket);
  });

  it("cancels without a write and still confirms closure normally", async () => {
    const closed: Ticket = { ...ticket, status: 1 };
    const close = vi.fn().mockResolvedValue(closed);
    const onUpdated = vi.fn();
    const user = userEvent.setup();
    render(<TicketDetailDialog ticketID={ticket.id} load={vi.fn().mockResolvedValue(ticket)} reply={vi.fn()} close={close}
      onClose={vi.fn()} onUpdated={onUpdated} />);
    await user.click(await screen.findByRole("button", { name: "关闭工单" }));
    await user.click(within(screen.getByRole("dialog", { name: "关闭工单" })).getByRole("button", { name: "取消" }));
    expect(close).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "关闭工单" }));
    await user.click(within(screen.getByRole("dialog", { name: "关闭工单" })).getByRole("button", { name: "确认关闭" }));
    await waitFor(() => expect(close).toHaveBeenCalledExactlyOnceWith(ticket.id));
    expect(onUpdated).toHaveBeenCalledWith(closed);
    expect(within(screen.getByRole("dialog", { name: "工单详情" })).getByText("已关闭")).toBeVisible();
    expect(screen.queryByLabelText("回复内容")).not.toBeInTheDocument();
  });
});
