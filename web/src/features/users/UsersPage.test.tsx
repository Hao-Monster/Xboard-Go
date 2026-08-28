import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { APIError, type AdminUser, type AdminUserBulkJob, type AdminUserGeneratedCredential, type Plan, type ServerGroup } from "../../lib/api";
import { generatedUsersCSV, UsersPage } from "./UsersPage";

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
const bulkJob: AdminUserBulkJob = {
  id: "00000000-0000-4000-8000-000000000041", kind: "mail", scope: "selected", administrator_id: 1,
  administrator_email: "admin@example.test", status: "queued", subject: "系统通知", total_count: 1, processed_count: 0,
  success_count: 0, failure_count: 0, skipped_count: 0, cancelled_count: 0,
  created_at: "2026-08-28T12:00:00Z", updated_at: "2026-08-28T12:00:00Z"
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

  it("generates a one-time credential, then edits access state and resets the password", async () => {
    const created = { ...account, id: 42, email: "new@example.test" };
    const updated = { ...created, revision: 2, banned: true, speed_limit: 80 };
    const passwordUpdated = { ...updated, revision: 3 };
    const api = baseAPI();
    let directory = [account];
    api.listAdminUsers.mockImplementation(() => Promise.resolve({ items: directory, total: directory.length, page: 1, page_size: 20 }));
    api.generateAdminUsers.mockImplementation(() => {
      directory = [created, ...directory];
      return Promise.resolve({ items: [{
        id: created.id, email: created.email, password: "secure-password-123", expired_at: null,
        uuid: "17f14aa9-5f00-4f1e-bbee-ff5be3d3f977", created_at: "2026-08-27T12:00:00Z",
        subscribe_url: "https://panel.example.test/s/one-time-token"
      }] });
    });
    api.updateAdminUser.mockImplementation(() => { directory = directory.map((item) => item.id === updated.id ? updated : item); return Promise.resolve(updated); });
    api.resetAdminUserPassword.mockImplementation(() => { directory = directory.map((item) => item.id === passwordUpdated.id ? passwordUpdated : item); return Promise.resolve(passwordUpdated); });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText("alpha@example.test")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "新增用户" }));
    let dialog = screen.getByRole("dialog", { name: "新增用户" });
    await user.type(within(dialog).getByLabelText("邮箱"), "new@example.test");
    await user.type(within(dialog).getByLabelText(/初始密码/), "secure-password-123");
    await user.selectOptions(within(dialog).getByLabelText("订阅计划"), "3");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));
    await waitFor(() => expect(api.generateAdminUsers).toHaveBeenCalledWith({
      mode: "single", email: "new@example.test", password: "secure-password-123", plan_id: 3,
      expired_at: null, is_distributor: false, distributor_name: null
    }));
    expect(within(dialog).getByRole("status")).toHaveTextContent("明文密码只在本窗口保留");
    expect(within(dialog).getByRole("table", { name: "一次性账号凭据" })).toHaveTextContent("secure-password-123");
    expect(api.createAdminUser).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: "完成" }));
    expect(await screen.findByText("new@example.test")).toBeVisible();

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

	it("copies the explicit subscription URL and exposes server-scoped legacy user operations without stacked overlays", async () => {
		const api = baseAPI();
		api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
		api.getAdminUserSubscriptionURL.mockResolvedValue({ subscribe_url: "https://panel.example.test/api/v1/client/subscribe?token=explicit-secret" });
		api.listAdminUserOrders.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
		api.listAdminUserInvitations.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
		api.listAdminUserTraffic.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
		api.listAdminUserTrafficResets.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
		api.assignAdminUserOrder.mockResolvedValue({});
		api.resetAdminUserTraffic.mockResolvedValue({
			user_id: account.id, email: account.email, upload_before: 100, download_before: 200,
			upload_after: 0, download_after: 0, reset_count: 5, reset_at: "2026-08-28T04:00:00Z",
			next_reset_at: "2026-09-01T00:00:00Z", reason: "客服确认", idempotent: false
		});
		const user = userEvent.setup();
		const writeText = vi.spyOn(navigator.clipboard, "writeText");
		render(<UsersPage api={api} currentUserID={1} />);
		expect(await screen.findByText(account.email)).toBeVisible();

		await user.click(screen.getByRole("button", { name: `查看详情：${account.email}` }));
		const detail = screen.getByRole("dialog", { name: "用户详情" });
		await user.click(within(detail).getByRole("button", { name: "复制订阅 URL" }));
		await waitFor(() => expect(writeText).toHaveBeenCalledWith("https://panel.example.test/api/v1/client/subscribe?token=explicit-secret"));
		expect(within(detail).getByRole("status")).toHaveTextContent("订阅地址已复制");
		expect(within(detail).queryByText("explicit-secret")).not.toBeInTheDocument();
		await user.click(within(detail).getByRole("button", { name: /^关闭$/ }));

		await user.click(screen.getByRole("button", { name: `用户操作：${account.email}` }));
		let operations = screen.getByRole("dialog", { name: "用户操作" });
		for (const action of ["分配订单", "TA 的订单", "TA 的邀请", "TA 的流量记录", "重置流量"]) {
			expect(within(operations).getByRole("button", { name: action })).toBeVisible();
		}
		await user.click(within(operations).getByRole("button", { name: "TA 的订单" }));
		const related = await screen.findByRole("dialog", { name: "用户关联记录" });
		expect(screen.getAllByRole("dialog")).toHaveLength(1);
		await waitFor(() => expect(api.listAdminUserOrders).toHaveBeenCalledWith(account.id, 1, 20));
		await user.click(within(related).getByRole("button", { name: "关闭关联记录面板" }));

		await user.click(screen.getByRole("button", { name: `用户操作：${account.email}` }));
		operations = screen.getByRole("dialog", { name: "用户操作" });
		await user.click(within(operations).getByRole("button", { name: "重置流量" }));
		const reset = await screen.findByRole("dialog", { name: "重置流量" });
		expect(screen.getAllByRole("dialog")).toHaveLength(1);
		const reason = within(reset).getByLabelText("重置原因（可选）");
		await user.click(reason);
		await user.paste("客服确认");
		expect(reason).toHaveValue("客服确认");
		await user.click(within(reset).getByRole("button", { name: "确认重置流量" }));
		await waitFor(() => expect(api.resetAdminUserTraffic).toHaveBeenCalledWith(account.id, "客服确认", expect.any(String)));
		expect(within(reset).getByRole("status")).toHaveTextContent("流量已重置");
	});

	it("reuses the traffic reset idempotency key after a retryable request failure", async () => {
		const api = baseAPI();
		api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
		api.resetAdminUserTraffic
			.mockRejectedValueOnce(new Error("临时网络错误"))
			.mockResolvedValueOnce({
				user_id: account.id, email: account.email, upload_before: 100, download_before: 200,
				upload_after: 0, download_after: 0, reset_count: 5, reset_at: "2026-08-28T04:00:00Z",
				next_reset_at: null, reason: "重试验证", idempotent: true
			});
		const user = userEvent.setup();
		render(<UsersPage api={api} currentUserID={1} />);
		expect(await screen.findByText(account.email)).toBeVisible();
		await user.click(screen.getByRole("button", { name: `用户操作：${account.email}` }));
		await user.click(within(screen.getByRole("dialog", { name: "用户操作" })).getByRole("button", { name: "重置流量" }));
		const reset = screen.getByRole("dialog", { name: "重置流量" });
		const reason = within(reset).getByLabelText("重置原因（可选）");
		await user.click(reason);
		await user.paste("重试验证");
		expect(reason).toHaveValue("重试验证");
		await user.click(within(reset).getByRole("button", { name: "确认重置流量" }));
		expect(await within(reset).findByRole("alert")).toHaveTextContent("临时网络错误");
		const firstKey = api.resetAdminUserTraffic.mock.calls[0]?.[2];
		expect(firstKey).toEqual(expect.any(String));
		await user.click(within(reset).getByRole("button", { name: "确认重置流量" }));
		await waitFor(() => expect(api.resetAdminUserTraffic).toHaveBeenCalledTimes(2));
		expect(api.resetAdminUserTraffic.mock.calls[1]?.[2]).toBe(firstKey);
		expect(within(reset).getByRole("status")).toHaveTextContent("流量已重置");
	});

	it("submits the live traffic reset form value before controlled state synchronization", async () => {
		const api = baseAPI();
		api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
		api.resetAdminUserTraffic.mockResolvedValue({
			user_id: account.id, email: account.email, upload_before: 100, download_before: 200,
			upload_after: 0, download_after: 0, reset_count: 5, reset_at: "2026-08-28T04:00:00Z",
			next_reset_at: null, reason: "客服确认", idempotent: false
		});
		const user = userEvent.setup();
		render(<UsersPage api={api} currentUserID={1} />);
		expect(await screen.findByText(account.email)).toBeVisible();
		await user.click(screen.getByRole("button", { name: `用户操作：${account.email}` }));
		await user.click(within(screen.getByRole("dialog", { name: "用户操作" })).getByRole("button", { name: "重置流量" }));
		const reset = screen.getByRole("dialog", { name: "重置流量" });
		const input = within(reset).getByLabelText("重置原因（可选）");
		const form = input.closest("form");
		if (form === null) throw new Error("traffic reset form is missing");
		const valueSetter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.bind(input);
		if (valueSetter === undefined) throw new Error("textarea value setter is unavailable");

		act(() => {
			valueSetter("客服确认");
			form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
		});

		await waitFor(() => expect(api.resetAdminUserTraffic).toHaveBeenCalledWith(account.id, "客服确认", expect.any(String)));
	});

  it("matches the legacy selected-scope menu and queues a templated mail job", async () => {
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account], total: 19, page: 1, page_size: 20 });
    api.createAdminUserBulkMail.mockResolvedValue(bulkJob);
    api.listAdminUserBulkJobs.mockResolvedValue({ items: [bulkJob], total: 1, page: 1, page_size: 50 });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);

    expect(await screen.findByText("已选择 0 项，共 19 项")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "批量操作" }));
    expect(screen.getByRole("menuitem", { name: "发送邮件(全部)" })).toBeVisible();
    await user.click(screen.getByLabelText(`选择用户：${account.email}`));
    expect(screen.getByText("已选择 1 项，共 19 项")).toBeVisible();
    await user.click(screen.getByRole("menuitem", { name: "发送邮件(1)" }));

    const dialog = screen.getByRole("dialog", { name: "发送邮件" });
    expect(within(dialog).getByText("向所选或已筛选用户发送邮件")).toBeVisible();
    expect(within(dialog).getByLabelText("仅选中（1）")).toBeChecked();
    expect(within(dialog).getByLabelText("筛选后的用户")).toBeDisabled();
    expect(within(dialog).getByLabelText("全部用户")).toBeEnabled();
    await user.type(within(dialog).getByPlaceholderText("例如：系统通知（支持占位符）"), "系统通知");
    const content = within(dialog).getByPlaceholderText("请输入邮件正文（可使用占位符）");
    await user.click(content);
    await user.paste("您好 {{user.email|用户}}，套餐 {{user.plan_name}}");
    await user.click(within(dialog).getByRole("button", { name: "发送" }));

    await waitFor(() => expect(api.createAdminUserBulkMail).toHaveBeenCalledWith(
      { scope: "selected", user_ids: [account.id] }, "系统通知", "您好 {{user.email|用户}}，套餐 {{user.plan_name}}"
    ));
    expect(await screen.findByRole("dialog", { name: "批量任务" })).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("邮件任务已创建，共 1 项");
  });

  it("uses the current server filter for CSV and keeps paging and sorting out of the bulk scope", async () => {
    const csvJob: AdminUserBulkJob = { ...bulkJob, id: "00000000-0000-4000-8000-000000000042", kind: "csv", scope: "filtered", total_count: 3 };
    const api = baseAPI();
    api.listAdminUsers
      .mockResolvedValueOnce({ items: [account], total: 19, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ items: [account], total: 3, page: 1, page_size: 20 });
    api.createAdminUserBulkCSV.mockResolvedValue(csvJob);
    api.listAdminUserBulkJobs.mockResolvedValue({ items: [csvJob], total: 1, page: 1, page_size: 50 });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText(account.email)).toBeVisible();
    await user.type(screen.getByRole("searchbox", { name: "邮箱前缀" }), "ticket-");
    await user.click(screen.getByRole("button", { name: "查询用户" }));
    expect(await screen.findByText("已选择 0 项，共 3 项")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "批量操作" }));
    await user.click(screen.getByRole("menuitem", { name: "导出 CSV(筛选)" }));
    await waitFor(() => expect(api.createAdminUserBulkCSV).toHaveBeenCalledWith({ scope: "filtered", email_prefix: "ticket-" }));
    expect(await screen.findByRole("dialog", { name: "批量任务" })).toBeVisible();
  });

  it("uses an alertdialog and a stable retry key for all-user bulk ban", async () => {
    const completed = { ...bulkJob, id: "00000000-0000-4000-8000-000000000043", kind: "ban" as const, scope: "all" as const, status: "succeeded" as const, total_count: 19, processed_count: 19, success_count: 17, skipped_count: 2 };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account], total: 19, page: 1, page_size: 20 });
    api.banAdminUsers.mockResolvedValue(completed);
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText(account.email)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "批量操作" }));
    await user.click(screen.getByRole("menuitem", { name: "批量封禁(全部)" }));
    const dialog = screen.getByRole("alertdialog", { name: "确认批量封禁" });
    expect(within(dialog).getByText("此操作将封禁系统中的所有用户。")).toBeVisible();
    expect(within(dialog).getByText(/此操作无法撤销/)).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "确认封禁" }));
    await waitFor(() => expect(api.banAdminUsers).toHaveBeenCalledWith({ scope: "all" }, expect.any(String)));
    expect(await screen.findByRole("status")).toHaveTextContent("批量封禁完成：成功 17 项，跳过 2 项");
  });

  it("shows observable job progress and allows cancellation and authenticated download", async () => {
    const queued = { ...bulkJob, total_count: 10, processed_count: 4, success_count: 4 };
    const csv = { ...bulkJob, id: "00000000-0000-4000-8000-000000000044", kind: "csv" as const, status: "succeeded" as const, output_filename: "users.csv", output_size: 128, processed_count: 1, success_count: 1 };
    const cancelled = { ...queued, status: "cancelled" as const, cancelled_count: 6, processed_count: 10 };
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20 });
    api.listAdminUserBulkJobs.mockResolvedValue({ items: [queued, csv], total: 2, page: 1, page_size: 50 });
    api.cancelAdminUserBulkJob.mockResolvedValue(cancelled);
    api.downloadAdminUserBulkCSV.mockResolvedValue(new Blob(["csv"]));
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:user-export");
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    expect(await screen.findByText(account.email)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "批量操作" }));
    await user.click(screen.getByRole("menuitem", { name: "查看批量任务" }));
    const dialog = screen.getByRole("dialog", { name: "批量任务" });
    expect(await within(dialog).findByText("4 / 10")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "取消任务" }));
    await waitFor(() => expect(api.cancelAdminUserBulkJob).toHaveBeenCalledWith(queued.id));
    await user.click(within(dialog).getByRole("button", { name: "下载" }));
    await waitFor(() => expect(api.downloadAdminUserBulkCSV).toHaveBeenCalledWith(csv.id));
    expect(createObjectURL).toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
  });

  it("exports a fixed UTF-8 CSV and neutralizes spreadsheet formulas", () => {
    const credential: AdminUserGeneratedCredential = {
      id: 1, email: " =cmd@example.test", password: "\t=WEBSERVICE()", expired_at: null,
      uuid: "-dangerous-uuid", created_at: "2026-08-27T12:00:00Z", subscribe_url: "@unsafe-url"
    };
    const csv = generatedUsersCSV([credential]);
    expect(csv.startsWith("\uFEFF\"账号\",\"密码\",\"过期时间\",\"UUID\",\"创建时间\",\"订阅地址\"\r\n")).toBe(true);
    expect(csv).toContain("\"' =cmd@example.test\"");
    expect(csv).toContain("\"'\t=WEBSERVICE()\"");
    expect(csv).toContain("\"'-dangerous-uuid\"");
    expect(csv).toContain("\"'@unsafe-url\"");
    expect(csv.endsWith("\r\n")).toBe(true);

    const emptyCells = generatedUsersCSV([{ ...credential, email: "", password: "", uuid: "", subscribe_url: "" }]);
    expect(emptyCells).not.toContain("\"'\"");
  });

  it("exposes bounded prefix batches without a shared password field", async () => {
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
    api.generateAdminUsers.mockResolvedValue({ items: [1, 2].map((index) => ({
      id: index, email: `team_${index}@example.test`, password: `independent-${index}-password`, expired_at: null,
      uuid: `00000000-0000-4000-8000-00000000000${index}`, created_at: "2026-08-27T12:00:00Z",
      subscribe_url: `https://panel.example.test/s/token-${index}`
    })) });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    await user.click(await screen.findByRole("button", { name: "新增用户" }));
    const dialog = screen.getByRole("dialog", { name: "新增用户" });
    await user.selectOptions(within(dialog).getByLabelText("生成方式"), "prefixed_batch");
    expect(within(dialog).queryByLabelText(/初始密码/)).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("账号前缀"), "team");
    await user.type(within(dialog).getByLabelText("邮箱域"), "example.test");
    await user.clear(within(dialog).getByLabelText(/生成数量/));
    await user.type(within(dialog).getByLabelText(/生成数量/), "2");
    await user.click(within(dialog).getByRole("button", { name: "生成账号" }));
    await waitFor(() => expect(api.generateAdminUsers).toHaveBeenCalledWith({
      mode: "prefixed_batch", email_prefix: "team", email_domain: "example.test", count: 2,
      plan_id: null, expired_at: null, is_distributor: false, distributor_name: null
    }));
    expect(within(dialog).getAllByRole("row")).toHaveLength(3);
    expect(within(dialog).getByText("independent-1-password")).toBeVisible();
    expect(within(dialog).getByText("independent-2-password")).toBeVisible();
  });

  it("makes the Xboard distributor-without-subscription rule explicit", async () => {
    const api = baseAPI();
    api.listAdminUsers.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 });
    api.generateAdminUsers.mockResolvedValue({ items: [{
      id: 5, email: "seller@example.test", password: "one-time-seller-password", expired_at: null,
      uuid: "00000000-0000-4000-8000-000000000005", created_at: "2026-08-27T12:00:00Z",
      subscribe_url: "https://panel.example.test/s/seller-token"
    }] });
    const user = userEvent.setup();
    render(<UsersPage api={api} currentUserID={1} />);
    await user.click(await screen.findByRole("button", { name: "新增用户" }));
    const dialog = screen.getByRole("dialog", { name: "新增用户" });
    await user.type(within(dialog).getByLabelText("邮箱"), "seller@example.test");
    await user.selectOptions(within(dialog).getByLabelText("订阅计划"), "3");
    await user.click(within(dialog).getByLabelText("分销商"));
    expect(within(dialog).getByLabelText("订阅计划")).toBeDisabled();
    expect(within(dialog).getByLabelText("订阅计划")).toHaveValue("");
    expect(within(dialog).getByText(/分销商账号仅用于下单/)).toBeVisible();
    await user.type(within(dialog).getByLabelText("分销商名称"), "星河分销");
    await user.click(within(dialog).getByRole("button", { name: "创建" }));
    await waitFor(() => expect(api.generateAdminUsers).toHaveBeenCalledWith(expect.objectContaining({
      mode: "single", email: "seller@example.test", plan_id: null,
      is_distributor: true, distributor_name: "星河分销"
    })));
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
    listAdminUsers: vi.fn(), getAdminUser: vi.fn(), createAdminUser: vi.fn(), generateAdminUsers: vi.fn(), updateAdminUser: vi.fn(), resetAdminUserPassword: vi.fn(),
		getAdminUserSubscriptionURL: vi.fn(), listAdminUserOrders: vi.fn(), assignAdminUserOrder: vi.fn(), listAdminUserInvitations: vi.fn(),
		listAdminUserTraffic: vi.fn(), listAdminUserTrafficResets: vi.fn(), resetAdminUserTraffic: vi.fn(),
    createAdminUserBulkMail: vi.fn(), createAdminUserBulkCSV: vi.fn(), banAdminUsers: vi.fn(),
    listAdminUserBulkJobs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }), getAdminUserBulkJob: vi.fn(),
    cancelAdminUserBulkJob: vi.fn(), downloadAdminUserBulkCSV: vi.fn(),
    listServerGroups: vi.fn().mockResolvedValue([group, groupTwo]), listPlans: vi.fn().mockResolvedValue([plan, planTwo])
  };
}
