import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdminAPI, Machine, Node } from "../../lib/api";
import { ServerManagementPage } from "./ServerManagementPage";

const machine: Machine = {
  id: 7,
  name: "edge-sg-01",
  notes: "Singapore edge",
  is_active: true,
  last_seen_at: null,
  load_status: null,
  servers_count: 1,
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z"
};

const node: Node = {
  id: 41,
  revision: 1,
  name: "SG VLESS",
  type: "vless",
  host: "sg.example.test",
  port: "443",
  show: true,
  enabled: true,
  sort: 0,
  rate: 1,
  traffic_upload: 0,
  traffic_download: 0,
  runtime_configured: true,
  last_check_at: null,
  last_push_at: null,
  machine_id: 7,
  created_at: "2026-08-20T00:00:00Z",
  updated_at: "2026-08-20T00:00:00Z"
};

describe("ServerManagementPage", () => {
  it("shows exactly one schedule button on the first and every later drawer open", async () => {
    const api = createAPI();
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    const details = await screen.findByRole("button", { name: "服务器详情" });
    await user.click(details);
    expect(await screen.findByRole("button", { name: "定时设置：SG VLESS" })).toBeVisible();
    expect(screen.getAllByRole("button", { name: "定时设置：SG VLESS" })).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "关闭服务器详情" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "服务器详情" })).not.toBeInTheDocument());
    await user.click(details);
    expect(await screen.findByRole("button", { name: "定时设置：SG VLESS" })).toBeVisible();
    expect(screen.getAllByRole("button", { name: "定时设置：SG VLESS" })).toHaveLength(1);
  });

  it("keeps the drawer open while the nested schedule modal is interactive", async () => {
    const api = createAPI();
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    const scheduleButton = await screen.findByRole("button", { name: "定时设置：SG VLESS" });
    await user.click(scheduleButton);

    const modal = await screen.findByRole("dialog", { name: "激活计划设置" });
    const enableTime = within(modal).getByLabelText("启用时间");
    await user.clear(enableTime);
    await user.type(enableTime, "20:30");
    await user.click(within(modal).getByRole("button", { name: "保存计划" }));

    await waitFor(() => expect(api.saveActivationSchedule).toHaveBeenCalledWith(41, {
      schedule_type: "daily",
      timezone: "Asia/Singapore",
      enable_time: "20:30",
      disable_time: "01:00"
    }));
    expect(screen.getByRole("dialog", { name: "服务器详情" })).toBeVisible();
  });

  it("Escape closes only the top modal and restores focus to its schedule button", async () => {
    const api = createAPI();
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    const scheduleButton = await screen.findByRole("button", { name: "定时设置：SG VLESS" });
    await user.click(scheduleButton);
    expect(await screen.findByRole("dialog", { name: "激活计划设置" })).toBeVisible();

    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "激活计划设置" })).not.toBeInTheDocument());
    expect(screen.getByRole("dialog", { name: "服务器详情" })).toBeVisible();
    await waitFor(() => expect(scheduleButton).toHaveFocus());
  });

  it("creates, edits, and deletes a machine through accessible modals", async () => {
    const api = createAPI();
    const created = {
      ...machine,
      id: 8,
      name: "edge-new",
      token: "one-time-enrollment",
      token_type: "enrollment_code" as const,
      expires_at: "2026-08-20T00:15:00Z",
      install_command: "install --enrollment-code one-time-enrollment"
    };
    vi.mocked(api.createMachine).mockResolvedValue(created);
    vi.mocked(api.updateMachine).mockResolvedValue({ ...machine, name: "edge-sg-renamed", is_active: false });
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    await screen.findByRole("button", { name: "服务器详情" });
    await user.click(screen.getByRole("button", { name: "新增服务器" }));
    const createDialog = screen.getByRole("dialog", { name: "新增服务器" });
    await user.type(within(createDialog).getByLabelText("服务器名称"), "edge-new");
    await user.type(within(createDialog).getByLabelText("备注"), "new edge");
    await user.click(within(createDialog).getByRole("button", { name: "创建服务器" }));
    await waitFor(() => expect(api.createMachine).toHaveBeenCalledWith({ name: "edge-new", notes: "new edge", is_active: true }));
    expect(await screen.findByRole("dialog", { name: "服务器接入命令" })).toHaveTextContent("--enrollment-code");
    await user.click(screen.getByRole("button", { name: "关闭服务器接入命令" }));

    await user.click(screen.getByRole("button", { name: "服务器详情" }));
    await user.click(await screen.findByRole("button", { name: "编辑信息" }));
    const editDialog = screen.getByRole("dialog", { name: "编辑服务器" });
    const nameInput = within(editDialog).getByLabelText("服务器名称");
    await user.clear(nameInput);
    await user.type(nameInput, "edge-sg-renamed");
    await user.click(within(editDialog).getByLabelText("允许机器接入"));
    await user.click(within(editDialog).getByRole("button", { name: "保存修改" }));
    await waitFor(() => expect(api.updateMachine).toHaveBeenCalledWith(7, { name: "edge-sg-renamed", notes: "Singapore edge", is_active: false }));
    expect(await screen.findByRole("heading", { name: "edge-sg-renamed" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "删除服务器" }));
    const deleteDialog = screen.getByRole("dialog", { name: "删除服务器" });
    await user.click(within(deleteDialog).getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(api.deleteMachine).toHaveBeenCalledWith(7));
    expect(screen.queryByRole("dialog", { name: "服务器详情" })).not.toBeInTheDocument();
  });

  it("toggles and unassigns a linked node without losing the schedule control", async () => {
    const api = createAPI();
    vi.mocked(api.listMachineNodes).mockResolvedValueOnce([node]).mockResolvedValue([]);
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    const enabled = await screen.findByRole("checkbox", { name: "启用节点：SG VLESS" });
    await user.click(enabled);
    await waitFor(() => expect(api.setNodeEnabled).toHaveBeenCalledWith(7, 41, 1, false));
    expect(screen.getByRole("button", { name: "定时设置：SG VLESS" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "解除关联" }));
    await waitFor(() => expect(api.unassignNode).toHaveBeenCalledWith(7, 41, 2));
    await waitFor(() => expect(screen.queryByRole("button", { name: "定时设置：SG VLESS" })).not.toBeInTheDocument());
  });

  it("shows legacy once schedules and converts their times before replacement", async () => {
    const api = createAPI();
    vi.mocked(api.getActivationSchedule).mockResolvedValue({
      server_id: 41,
      schedule_type: "once",
      timezone: "",
      enable_time: "",
      disable_time: "",
      enable_at: "2026-08-20T10:45:00Z",
      disable_at: "2026-08-20T16:15:00Z",
      revision: "legacy-once",
      next_transition_at: "2026-08-20T10:45:00Z",
      next_target_enabled: true,
      phase: "inactive"
    });
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    await user.click(await screen.findByRole("button", { name: "定时设置：SG VLESS" }));
    const modal = await screen.findByRole("dialog", { name: "激活计划设置" });
    expect(modal).toHaveTextContent("旧版单次计划");
    expect(within(modal).getByLabelText("启用时间")).toHaveValue("18:45");
    expect(within(modal).getByLabelText("停用时间")).toHaveValue("00:15");
  });

  it("keeps schedule input and prevents a false success when saving fails", async () => {
    const api = createAPI();
    vi.mocked(api.saveActivationSchedule).mockRejectedValue(new Error("计划保存失败"));
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    await user.click(await screen.findByRole("button", { name: "定时设置：SG VLESS" }));
    const modal = await screen.findByRole("dialog", { name: "激活计划设置" });
    const enableTime = within(modal).getByLabelText("启用时间");
    await user.clear(enableTime);
    await user.type(enableTime, "21:15");
    await user.click(within(modal).getByRole("button", { name: "保存计划" }));
    expect(await within(modal).findByRole("alert")).toHaveTextContent("计划保存失败");
    expect(enableTime).toHaveValue("21:15");
    expect(modal).toBeVisible();
  });

  it("filters the overview and renders load and network trends", async () => {
    const api = createAPI();
    vi.mocked(api.listMachines).mockResolvedValue([{ ...machine, load_status: {
      cpu: 85,
      mem: { total: 1000, used: 920 },
      disk: { total: 2000, used: 500 },
      net: { in_speed: 2048, out_speed: 4096 },
      updated_at: 1787198400
    } }]);
    vi.mocked(api.listLoadHistory).mockResolvedValue([
      { id: 1, machine_id: 7, cpu: 40, mem_total: 1000, mem_used: 500, disk_total: 2000, disk_used: 400, net_in_speed: 1024, net_out_speed: 2048, recorded_at: "2026-08-20T11:00:00Z" },
      { id: 2, machine_id: 7, cpu: 85, mem_total: 1000, mem_used: 920, disk_total: 2000, disk_used: 500, net_in_speed: 2048, net_out_speed: 4096, recorded_at: "2026-08-20T12:00:00Z" }
    ]);
    const user = userEvent.setup();
    render(<ServerManagementPage api={api} />);

    const overview = await screen.findByRole("region", { name: "服务器概览" });
    const highLoad = within(overview).getByText("高负载");
    expect(highLoad.parentElement).toHaveTextContent("1");
    await user.type(screen.getByRole("searchbox", { name: "搜索" }), "不存在");
    expect(screen.getByText("没有符合当前筛选条件的服务器。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重置" }));
    await user.click(await screen.findByRole("button", { name: "服务器详情" }));
    expect(await screen.findByRole("img", { name: "CPU（蓝）和内存（绿）趋势" })).toBeVisible();
    expect(screen.getByRole("img", { name: "网络入站（蓝）和出站（绿）趋势" })).toBeVisible();
    expect(screen.getByText("2.0 KiB/s / 4.0 KiB/s")).toBeVisible();
  });
});

function createAPI(): AdminAPI & { saveActivationSchedule: ReturnType<typeof vi.fn> } {
  const saveActivationSchedule = vi.fn().mockResolvedValue({
    server_id: 41,
    schedule_type: "daily",
    timezone: "Asia/Singapore",
    enable_time: "20:30",
    disable_time: "01:00",
    revision: "revision-2",
    next_transition_at: "2026-08-21T17:00:00Z",
    next_target_enabled: false,
    phase: "active"
  });
  return {
    getNodeAgentSettings: vi.fn(),
    updateNodeAgentSettings: vi.fn(),
    listMachines: vi.fn().mockResolvedValue([machine]),
    createMachine: vi.fn().mockResolvedValue(undefined),
    updateMachine: vi.fn().mockResolvedValue(undefined),
    deleteMachine: vi.fn().mockResolvedValue(undefined),
    createEnrollment: vi.fn(),
    listMachineNodes: vi.fn().mockResolvedValue([node]),
    listLoadHistory: vi.fn().mockResolvedValue([]),
    listAdminOrders: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getAdminOrder: vi.fn(),
    assignOrder: vi.fn(),
    paidAdminOrder: vi.fn(),
    cancelAdminOrder: vi.fn(),
		updateAdminOrderCommissionStatus: vi.fn(),
    listAdminDistributorOptions: vi.fn().mockResolvedValue([]),
    listAdminDistributorOrders: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getAdminDistributorOrder: vi.fn(),
    updateAdminDistributorRemark: vi.fn(),
    updateAdminDistributorEntitlement: vi.fn(),
    updateAdminDistributorHWID: vi.fn(),
    listAdminDistributorHWIDDevices: vi.fn().mockResolvedValue([]),
    deleteAdminDistributorHWIDDevice: vi.fn(),
    previewAdminDistributorSettlement: vi.fn(),
    settleAdminDistributorOrders: vi.fn(),
    exportAdminDistributorOrders: vi.fn(),
    listCoupons: vi.fn(),
    createCoupon: vi.fn(),
    updateCoupon: vi.fn(),
    setCouponVisibility: vi.fn(),
    deleteCoupon: vi.fn(),
    createCouponBatch: vi.fn(),
    listUnassignedNodes: vi.fn().mockResolvedValue([]),
    assignNode: vi.fn(),
    unassignNode: vi.fn().mockResolvedValue(undefined),
    setNodeEnabled: vi.fn().mockResolvedValue(undefined),
    listAdminNodes: vi.fn(),
    getAdminNodeDefinition: vi.fn(),
    createAdminNodeDefinition: vi.fn(),
    replaceAdminNodeDefinition: vi.fn(),
    updateAdminNode: vi.fn(),
    copyAdminNode: vi.fn(),
    reorderAdminNodes: vi.fn(),
    updateAdminNodeStates: vi.fn(),
    resetAdminNodeTraffic: vi.fn(),
    deleteAdminNodes: vi.fn(),
    getActivationSchedule: vi.fn().mockRejectedValue(Object.assign(new Error("not found"), { status: 404 })),
    saveActivationSchedule,
    deleteActivationSchedule: vi.fn(),
    listServerGroups: vi.fn().mockResolvedValue([]),
    createServerGroup: vi.fn(),
    updateServerGroup: vi.fn(),
    deleteServerGroup: vi.fn(),
	listPlans: vi.fn().mockResolvedValue([]),
	createPlan: vi.fn(),
	updatePlan: vi.fn(),
	setPlanState: vi.fn(),
	reorderPlans: vi.fn(),
	deletePlan: vi.fn(),
	listPaymentProviders: vi.fn(),
	listAdminPayments: vi.fn(),
	createPayment: vi.fn(),
	updatePayment: vi.fn(),
	setPaymentEnabled: vi.fn(),
	reorderPayments: vi.fn(),
	deletePayment: vi.fn(),
    listRoutingRules: vi.fn().mockResolvedValue([]),
    createRoutingRule: vi.fn(),
    updateRoutingRule: vi.fn(),
    deleteRoutingRule: vi.fn(),
    listAdminUsers: vi.fn().mockResolvedValue({ items: [] }),
    getAdminUser: vi.fn(),
    createAdminUser: vi.fn(),
    generateAdminUsers: vi.fn(),
    updateAdminUser: vi.fn(),
    resetAdminUserPassword: vi.fn(),
		getAdminUserSubscriptionURL: vi.fn(),
		listAdminUserOrders: vi.fn(),
		assignAdminUserOrder: vi.fn(),
		listAdminUserInvitations: vi.fn(),
		listAdminUserTraffic: vi.fn(),
		listAdminUserTrafficResets: vi.fn(),
		resetAdminUserTraffic: vi.fn(),
		createAdminUserBulkMail: vi.fn(),
		createAdminUserBulkCSV: vi.fn(),
		banAdminUsers: vi.fn(),
		listAdminUserBulkJobs: vi.fn(),
		getAdminUserBulkJob: vi.fn(),
		cancelAdminUserBulkJob: vi.fn(),
		downloadAdminUserBulkCSV: vi.fn(),
    listAdminTickets: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getAdminTicket: vi.fn(),
    replyAdminTicket: vi.fn(),
    closeAdminTicket: vi.fn(),
    getTicketSettings: vi.fn(),
    updateTicketSettings: vi.fn(),
    getSiteSettings: vi.fn(),
    updateSiteSettings: vi.fn(),
    getCommissionSettings: vi.fn(),
    updateCommissionSettings: vi.fn(),
    listNotices: vi.fn().mockResolvedValue([]),
    createNotice: vi.fn(),
    updateNotice: vi.fn(),
    setNoticeVisibility: vi.fn(),
    reorderNotices: vi.fn(),
    deleteNotice: vi.fn(),
    listKnowledgeAdmin: vi.fn().mockResolvedValue([]),
    getKnowledgeAdmin: vi.fn(),
    listKnowledgeCategories: vi.fn().mockResolvedValue([]),
    createKnowledge: vi.fn(),
    updateKnowledge: vi.fn(),
    setKnowledgeVisibility: vi.fn(),
    reorderKnowledge: vi.fn(),
    deleteKnowledge: vi.fn(),
    initializeKnowledgeAttachment: vi.fn(),
    uploadKnowledgeAttachmentChunk: vi.fn(),
    getKnowledgeAttachmentUpload: vi.fn(),
    completeKnowledgeAttachmentUpload: vi.fn(),
    cancelKnowledgeAttachmentUpload: vi.fn(),
    listKnowledgeAttachments: vi.fn(),
    dropKnowledgeAttachment: vi.fn(),
    cloneKnowledgeAttachments: vi.fn(),
    generateKnowledgeAttachmentQRCode: vi.fn(),
    listClientCatalogAdmin: vi.fn(),
    saveClientCatalog: vi.fn(),
    getSystemStatus: vi.fn(),
    listAdminAudit: vi.fn(),
    listTicketMailFailures: vi.fn()
  };
}
