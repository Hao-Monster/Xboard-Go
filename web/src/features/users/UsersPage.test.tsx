import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { APIError, type AdminUser, type Plan, type ServerGroup } from "../../lib/api";
import { UsersPage } from "./UsersPage";

const group: ServerGroup = { id: 7, name: "Premium", users_count: 1, server_count: 1, created_at: "2026-08-24T12:00:00Z", updated_at: "2026-08-24T12:00:00Z" };
const groupTwo: ServerGroup = { ...group, id: 8, name: "Enterprise", users_count: 0 };
const plan: Plan = { id: 3, group_id: 7, transfer_enable: 100, name: "旗舰套餐", speed_limit: 50, show: true, sort: 1, renew: true,
  content: "", reset_traffic_method: 1, capacity_limit: null, prices: { monthly: 2500 }, sell: true, device_limit: 3, tags: [], revision: 1,
  users_count: 1, active_users_count: 1, capacity_users_count: 1, created_at: "2026-08-24T12:00:00Z", updated_at: "2026-08-24T12:00:00Z" };
const planTwo: Plan = { ...plan, id: 4, group_id: 8, transfer_enable: 64, name: "企业套餐", speed_limit: null, device_limit: null,
  users_count: 0, active_users_count: 0, capacity_users_count: 0 };
const account: AdminUser = {
  id: 41, email: "alpha@example.test", is_admin: false, banned: false, group_id: 7,
  group_name: "Premium", plan_id: 3, plan_name: "旗舰套餐", invite_user_id: 2, invite_user_email: "inviter@example.test",
  transfer_enable: 1_073_741_824, traffic_upload: 100, traffic_download: 200, traffic_used: 300,
  expired_at: null, speed_limit: 50, device_limit: 3, online_count: 1, last_online_at: null, last_login_at: "2026-08-24T11:30:00Z",
  balance: 2500, commission_type: 2, commission_rate: 15, commission_balance: 900, discount: 80,
  next_reset_at: "2026-09-01T00:00:00Z", last_reset_at: "2026-08-01T00:00:00Z", reset_count: 4,
  telegram_id: 778899, remind_expire: false, remind_traffic: true, remarks: "重点客户",
  revision: 1, created_at: "2026-08-24T12:00:00Z", updated_at: "2026-08-24T12:00:00Z"
};

describe("UsersPage", () => {
  it("uses stable server pages and keeps quick filters on page navigation", async () => {
    const beta = { ...account, id: 40, email: "beta@example.test" };
    const api = baseAPI();
    api.listAdminUsers
      .mockResolvedValueOnce({ items: [account], total: 21, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ items: [beta], total: 21, page: 2, page_size: 20 })
      .mockResolvedValueOnce({ items: [beta], total: 1, page: 1, page_size: 20 });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);

    expect(await screen.findByText("alpha@example.test")).toBeVisible();
    expect(screen.getByText(/最后登录/)).toBeVisible();
    expect(api.listAdminUsers).toHaveBeenNthCalledWith(1, { page: 1, page_size: 20, sort_by: "id", sort_desc: true });
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByText("beta@example.test")).toBeVisible();
    expect(screen.queryByText("alpha@example.test")).not.toBeInTheDocument();
    expect(api.listAdminUsers).toHaveBeenNthCalledWith(2, { page: 2, page_size: 20, sort_by: "id", sort_desc: true });

    await user.type(screen.getByRole("searchbox", { name: "邮箱前缀" }), "beta");
    await user.selectOptions(screen.getByLabelText("用户状态"), "banned");
    await user.selectOptions(screen.getByLabelText("权限组筛选"), "7");
    await user.click(screen.getByRole("button", { name: "查询用户" }));
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenLastCalledWith({
      page: 1, page_size: 20, sort_by: "id", sort_desc: true, email_prefix: "beta", banned: true, group_id: 7
    }));
  });

  it("matches the legacy columns, submits allowlisted advanced filters, sorts, and exposes a complete secret-free detail", async () => {
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
    api.updateAdminUser.mockResolvedValue({ ...account, revision: 2 });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    const table = await screen.findByRole("table", { name: "用户列表" });
    for (const heading of ["ID", "邮箱", "在线设备", "状态", "订阅", "权限组", "已用流量", "总流量", "到期时间", "余额", "佣金", "注册时间", "操作"]) {
      expect(within(table).getByRole("columnheader", { name: heading })).toBeVisible();
    }
    expect(within(table).getByText("1 / 3", { exact: false })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "高级筛选" }));
    await user.click(screen.getByRole("button", { name: "添加筛选条件" }));
    await user.selectOptions(screen.getByLabelText("筛选字段 1"), "plan_id");
    expect(screen.queryByRole("option", { name: "包含" })).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("筛选值 1"), "3");
    await user.click(screen.getByRole("button", { name: "添加筛选条件" }));
    await user.selectOptions(screen.getByLabelText("筛选字段 2"), "remarks");
    await user.type(screen.getByLabelText("筛选值 2"), "重点");
    await user.click(screen.getByRole("button", { name: "查询用户" }));
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenLastCalledWith(expect.objectContaining({
      filters: [{ field: "plan_id", operator: "eq", value: "3" }, { field: "remarks", operator: "contains", value: "重点" }], page: 1
    })));

    await user.click(within(table).getByRole("button", { name: "按余额排序" }));
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenLastCalledWith(expect.objectContaining({ sort_by: "balance", sort_desc: false })));

    await user.click(screen.getByRole("button", { name: "查看详情：alpha@example.test" }));
    const detail = screen.getByRole("dialog", { name: "用户详情" });
    expect(within(detail).getByText("旗舰套餐")).toBeVisible();
    expect(within(detail).getByText("inviter@example.test")).toBeVisible();
    expect(within(detail).getByText("重点客户")).toBeVisible();
    expect(within(detail).getByText("778899")).toBeVisible();
    expect(within(detail).getByText("¥25.00")).toBeVisible();
    expect(within(detail).queryByText(/订阅地址|Token|UUID/)).not.toBeInTheDocument();

    await user.click(within(detail).getByRole("button", { name: /^关闭$/ }));
    await user.click(screen.getByRole("button", { name: "编辑用户：alpha@example.test" }));
    await user.click(within(screen.getByRole("dialog", { name: "编辑用户" })).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenLastCalledWith(expect.objectContaining({
      filters: [{ field: "plan_id", operator: "eq", value: "3" }, { field: "remarks", operator: "contains", value: "重点" }],
      sort_by: "balance", sort_desc: false, page: 1
    })));
  }, 10_000);

  it("creates and edits only access-state fields, then resets the password", async () => {
    const created = { ...account, id: 42, email: "new@example.test" };
    const updated = { ...created, revision: 2, banned: true, speed_limit: 80 };
    const passwordUpdated = { ...updated, revision: 3 };
    const api = baseAPI();
    let directory = [account];
    api.listAdminUsers.mockImplementation(() => Promise.resolve({ items: directory, total: directory.length, page: 1, page_size: 20 }));
    api.createAdminUser.mockImplementation(() => { directory = [created, ...directory]; return Promise.resolve(created); });
    api.updateAdminUser.mockImplementation(() => { directory = directory.map((item) => item.id === updated.id ? updated : item); return Promise.resolve(updated); });
    api.resetAdminUserPassword.mockImplementation(() => { directory = directory.map((item) => item.id === passwordUpdated.id ? passwordUpdated : item); return Promise.resolve(passwordUpdated); });
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
    await waitFor(() => expect(api.listAdminUsers).toHaveBeenCalledTimes(4));
  });

  it("edits the complete legacy profile and applies plan entitlements with exact units", async () => {
    const updated = { ...account, revision: 2, plan_id: planTwo.id, plan_name: planTwo.name, group_id: groupTwo.id, group_name: groupTwo.name };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
    api.updateAdminUser.mockResolvedValue(updated);
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    await user.click(await screen.findByRole("button", { name: "编辑用户：alpha@example.test" }));
    const dialog = screen.getByRole("dialog", { name: "编辑用户" });

    await user.selectOptions(within(dialog).getByLabelText("套餐"), String(planTwo.id));
    expect(within(dialog).getByLabelText("权限组")).toHaveValue(String(groupTwo.id));
    expect(within(dialog).getByLabelText("流量额度（GiB）")).toHaveValue(64);
    expect(within(dialog).getByLabelText("限速（Mbps，0 为不限速）")).toHaveValue(0);
    expect(within(dialog).getByLabelText("设备数（0 为不限设备）")).toHaveValue(0);

    await user.clear(within(dialog).getByLabelText("邀请人邮箱（留空表示无）"));
    await user.type(within(dialog).getByLabelText("邀请人邮箱（留空表示无）"), "new-inviter@example.test");
    await user.type(within(dialog).getByLabelText("新密码（留空不修改）"), "rotated-profile-password-123");
    await user.clear(within(dialog).getByLabelText("已用上行流量（GiB）"));
    await user.type(within(dialog).getByLabelText("已用上行流量（GiB）"), "1.5");
    await user.clear(within(dialog).getByLabelText("已用下行流量（GiB）"));
    await user.type(within(dialog).getByLabelText("已用下行流量（GiB）"), "2");
    await user.clear(within(dialog).getByLabelText("余额（元）"));
    await user.type(within(dialog).getByLabelText("余额（元）"), "45.67");
    await user.clear(within(dialog).getByLabelText("佣金余额（元）"));
    await user.type(within(dialog).getByLabelText("佣金余额（元）"), "8.09");
    await user.selectOptions(within(dialog).getByLabelText("佣金类型"), "1");
    await user.clear(within(dialog).getByLabelText("佣金比例（留空使用系统默认）"));
    await user.clear(within(dialog).getByLabelText("专享折扣（留空使用系统默认）"));
    await user.type(within(dialog).getByLabelText("专享折扣（留空使用系统默认）"), "75");
    await user.clear(within(dialog).getByLabelText("Telegram ID（留空表示未绑定）"));
    await user.click(within(dialog).getByLabelText("到期提醒"));
    await user.click(within(dialog).getByLabelText("流量提醒"));
    await user.clear(within(dialog).getByLabelText("备注"));
    await user.type(within(dialog).getByLabelText("备注"), "updated complete profile");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));

    await waitFor(() => expect(api.updateAdminUser).toHaveBeenCalledWith(account.id, expect.objectContaining({
      revision: account.revision, password: "rotated-profile-password-123", plan_id: planTwo.id,
      group_id: groupTwo.id, transfer_enable: 64 * 1024 * 1024 * 1024, speed_limit: 0, device_limit: 0,
      invite_user_email: "new-inviter@example.test", traffic_upload: 1_610_612_736, traffic_download: 2_147_483_648,
      balance: 4567, commission_type: 1, commission_rate: null, commission_balance: 809, discount: 75,
      telegram_id: null, remind_expire: true, remind_traffic: false, remarks: "updated complete profile"
    })));
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

  it("requires a distributor name and persists coexisting roles", async () => {
    const distributor = { ...account, revision: 2, is_admin: true, is_staff: true, is_distributor: true, distributor_name: "星河分销" };
    const api = baseAPI();
    let directory = [account];
    api.listAdminUsers.mockImplementation(() => Promise.resolve({ items: directory, total: directory.length, page: 1, page_size: 20 }));
    api.updateAdminUser.mockImplementation(() => { directory = [distributor]; return Promise.resolve(distributor); });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    await user.click(await screen.findByRole("button", { name: "编辑用户：alpha@example.test" }));
    const dialog = screen.getByRole("dialog", { name: "编辑用户" });

    await user.click(within(dialog).getByLabelText("管理员"));
    await user.click(within(dialog).getByLabelText("员工"));
    await user.click(within(dialog).getByLabelText("分销商"));
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("必须填写分销商名称");
    expect(api.updateAdminUser).not.toHaveBeenCalled();

    await user.type(within(dialog).getByLabelText("分销商名称"), "星河分销");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateAdminUser).toHaveBeenCalledWith(account.id, expect.objectContaining({
      is_admin: true, is_staff: true, is_distributor: true, distributor_name: "星河分销"
    })));
    expect(await screen.findByText("星河分销")).toBeVisible();
    expect(screen.getByText(/管理员 · 员工 · 分销商/)).toBeVisible();
  });
});

function baseAPI() {
  return {
    listAdminUsers: vi.fn(), getAdminUser: vi.fn(), createAdminUser: vi.fn(), updateAdminUser: vi.fn(), resetAdminUserPassword: vi.fn(),
    listServerGroups: vi.fn().mockResolvedValue([group, groupTwo]), listPlans: vi.fn().mockResolvedValue([plan, planTwo])
  };
}
