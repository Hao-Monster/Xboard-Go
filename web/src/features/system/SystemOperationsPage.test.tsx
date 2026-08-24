import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdminAuditPage, SystemStatus, TicketMailFailurePage } from "../../lib/api";
import { SystemOperationsPage } from "./SystemOperationsPage";

const status: SystemStatus = {
  started_at: "2026-08-24T10:00:00Z", uptime_seconds: 3661, schema_version: 17,
  scheduler: { healthy: true, last_run_at: "2026-08-24T11:01:00Z" },
  mail_worker: { healthy: true, last_run_at: "2026-08-24T11:01:00Z" },
  mail_queue: { pending: 2, claimed: 1, sent: 12, failed: 1, oldest_pending_at: "2026-08-24T11:00:00Z" }
};

const audit: AdminAuditPage = {
  items: [{
    id: 9, administrator_id: 1, administrator_email: "admin@example.test", method: "PUT",
    route: "/api/v1/admin/ticket-settings", status_code: 200, created_at: "2026-08-24T11:00:00Z"
  }],
  total: 1, page: 1, page_size: 20
};

const failures: TicketMailFailurePage = {
  items: [{
    id: 4, kind: "ticket", recipient: "user@example.test", ticket_subject: "Unable to connect", attempt_count: 3,
    last_error: "connection refused", created_at: "2026-08-24T10:00:00Z", failed_at: "2026-08-24T10:07:00Z"
  }],
  total: 21, page: 1, page_size: 20
};

describe("SystemOperationsPage", () => {
  it("shows runtime and queue health, filters audit entries, and exposes failed mail without message bodies", async () => {
    const api = {
      getSystemStatus: vi.fn().mockResolvedValue(status),
      listAdminAudit: vi.fn().mockResolvedValue(audit),
      listTicketMailFailures: vi.fn().mockResolvedValue(failures)
    };
    const user = userEvent.setup();
    render(<SystemOperationsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "系统状态" })).toBeVisible();
    expect(await screen.findByText("Schema v17", { exact: true })).toBeVisible();
    expect(screen.getByText("待处理 2", { exact: true })).toBeVisible();
    expect(screen.getByText("Unable to connect", { exact: true })).toBeVisible();
    expect(screen.queryByText("private reply body", { exact: true })).not.toBeInTheDocument();
    expect(screen.getByText("/api/v1/admin/ticket-settings", { exact: true })).toBeVisible();

    await user.selectOptions(screen.getByLabelText("审计操作"), "PUT");
    await user.type(screen.getByRole("searchbox", { name: "搜索审计日志" }), "ticket-settings");
    await user.click(screen.getByRole("button", { name: "查询审计日志" }));
    await waitFor(() => expect(api.listAdminAudit).toHaveBeenLastCalledWith(1, 20, "PUT", "ticket-settings"));

    await user.click(screen.getByRole("button", { name: "刷新系统状态" }));
    await waitFor(() => expect(api.getSystemStatus).toHaveBeenCalledTimes(2));
    expect(api.listTicketMailFailures).toHaveBeenCalledTimes(2);

    await user.click(screen.getByRole("button", { name: "失败任务下一页" }));
    await waitFor(() => expect(api.listTicketMailFailures).toHaveBeenLastCalledWith(2, 20));
  });
});
