import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { APIError, type AdminUser, type ServerGroup } from "../../lib/api";
import { UsersPage } from "./UsersPage";

const group: ServerGroup = { id: 7, name: "Premium", users_count: 1, server_count: 1, created_at: "2026-08-24T12:00:00Z", updated_at: "2026-08-24T12:00:00Z" };
const account: AdminUser = {
  id: 41, email: "alpha@example.test", is_admin: false, banned: false, group_id: 7,
  transfer_enable: 1_073_741_824, traffic_upload: 100, traffic_download: 200,
  expired_at: null, speed_limit: 50, device_limit: 3, online_count: 1, last_online_at: null,
  revision: 1, created_at: "2026-08-24T12:00:00Z", updated_at: "2026-08-24T12:00:00Z"
};

describe("UsersPage", () => {
  it("filters on the server and appends cursor pages without replacing existing rows", async () => {
    const beta = { ...account, id: 40, email: "beta@example.test" };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValueOnce({ items: [account], next_cursor: "next-page" }).mockResolvedValueOnce({ items: [beta] });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);

    expect(await screen.findByText("alpha@example.test")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "加载更多用户" }));
    expect(await screen.findByText("beta@example.test")).toBeVisible();
    expect(screen.getByText("alpha@example.test")).toBeVisible();
    expect(api.listAdminUsers).toHaveBeenNthCalledWith(2, { limit: 50, cursor: "next-page" });

    api.listAdminUsers.mockResolvedValueOnce({ items: [beta] });
    await user.type(screen.getByRole("searchbox", { name: "邮箱前缀" }), "beta");
    await user.selectOptions(screen.getByLabelText("用户状态"), "banned");
    await user.selectOptions(screen.getByLabelText("权限组筛选"), "7");
    await user.click(screen.getByRole("button", { name: "查询用户" }));
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenLastCalledWith({ limit: 50, email_prefix: "beta", banned: true, group_id: 7 }));
    expect(screen.queryByText("alpha@example.test")).not.toBeInTheDocument();
  });

  it("creates and edits only access-state fields, then resets the password", async () => {
    const created = { ...account, id: 42, email: "new@example.test" };
    const updated = { ...created, revision: 2, banned: true, speed_limit: 80 };
    const passwordUpdated = { ...updated, revision: 3 };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account] });
    api.createAdminUser.mockResolvedValue(created);
    api.updateAdminUser.mockResolvedValue(updated);
    api.resetAdminUserPassword.mockResolvedValue(passwordUpdated);
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText("alpha@example.test")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "新增用户" }));
    let dialog = screen.getByRole("dialog", { name: "新增用户" });
    await user.type(within(dialog).getByLabelText("邮箱"), "new@example.test");
    await user.type(within(dialog).getByLabelText("初始密码"), "secure-password-123");
    await user.selectOptions(within(dialog).getByLabelText("权限组"), "7");
    await user.clear(within(dialog).getByLabelText("流量额度（字节）"));
    await user.type(within(dialog).getByLabelText("流量额度（字节）"), "1073741824");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));
    await waitFor(() => expect(api.createAdminUser).toHaveBeenCalledWith(expect.objectContaining({
      email: "new@example.test", password: "secure-password-123", group_id: 7, transfer_enable: 1_073_741_824
    })));

    await user.click(screen.getByRole("button", { name: "编辑用户：new@example.test" }));
    dialog = screen.getByRole("dialog", { name: "编辑用户" });
    await user.clear(within(dialog).getByLabelText("限速（Mbps，0 为不限速）"));
    await user.type(within(dialog).getByLabelText("限速（Mbps，0 为不限速）"), "80");
    await user.click(within(dialog).getByLabelText("封禁用户"));
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateAdminUser).toHaveBeenCalledWith(42, expect.objectContaining({ revision: 1, speed_limit: 80, banned: true })));

    await user.click(screen.getByRole("button", { name: "重置密码：new@example.test" }));
    dialog = screen.getByRole("dialog", { name: "重置用户密码" });
    await user.type(within(dialog).getByLabelText("新密码"), "rotated-password-123");
    await user.click(within(dialog).getByRole("button", { name: "确认重置" }));
    await waitFor(() => expect(api.resetAdminUserPassword).toHaveBeenCalledWith(42, 2, "rotated-password-123"));
  });

  it("keeps the editor open on an optimistic conflict and offers a fresh reload", async () => {
    const fresh = { ...account, revision: 2, speed_limit: 90 };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account] });
    api.updateAdminUser.mockRejectedValue(new APIError(409, "user_revision_conflict", "用户状态已变化"));
    api.getAdminUser.mockResolvedValue(fresh);
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText("alpha@example.test")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "编辑用户：alpha@example.test" }));
    const dialog = screen.getByRole("dialog", { name: "编辑用户" });
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("用户状态已变化");
    await user.click(within(dialog).getByRole("button", { name: "加载最新状态" }));
    await waitFor(() => expect(api.getAdminUser).toHaveBeenCalledWith(41));
    expect(within(dialog).getByLabelText("限速（Mbps，0 为不限速）")).toHaveValue(90);
  });
});

function baseAPI() {
  return {
    listAdminUsers: vi.fn(), getAdminUser: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn(), resetAdminUserPassword: vi.fn(),
    listServerGroups: vi.fn().mockResolvedValue([group])
  };
}
