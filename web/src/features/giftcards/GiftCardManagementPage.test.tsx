import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { GiftCardTemplate } from "../../lib/api";
import { GiftCardManagementPage } from "./GiftCardManagementPage";

const template: GiftCardTemplate = {
  id: 7, name: "新人礼品卡", description: "欢迎奖励", type: 1, status: true,
  conditions: {}, rewards: { balance: 1234, transfer_enable: 1_073_741_824 }, limits: { max_use_per_user: 1 },
  special_config: { festival_multiplier_basis_points: 10_000 }, icon: "", background_image: "", theme: "#1890ff",
  sort: 0, admin_id: 1, revision: 1, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z"
};

function createAPI() {
  return {
    listGiftCardTemplates: vi.fn().mockResolvedValue({ items: [template], total: 1, page: 1, page_size: 20 }),
    createGiftCardTemplate: vi.fn().mockResolvedValue(template), updateGiftCardTemplate: vi.fn(), deleteGiftCardTemplate: vi.fn(),
    generateGiftCardCodes: vi.fn().mockResolvedValue([]), generateGiftCardCodesCSV: vi.fn().mockResolvedValue(new Blob(["code"])), listGiftCardCodes: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    updateGiftCardCode: vi.fn(), exportGiftCardCodes: vi.fn().mockResolvedValue(new Blob(["code\n"])), toggleGiftCardCode: vi.fn(), deleteGiftCardCode: vi.fn(), listGiftCardUsages: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getGiftCardStatistics: vi.fn().mockResolvedValue({ template_total: 1, active_templates: 1, code_total: 0, used_codes: 0, usage_total: 0, daily_usages: [], type_stats: [] }),
    listPlans: vi.fn().mockResolvedValue([])
  };
}

describe("GiftCardManagementPage", () => {
  it("keeps legacy plan defaults empty and submits zero validity only after choosing a plan", async () => {
    const api = createAPI(); api.listPlans.mockResolvedValue([{ id: 12, name: "测试套餐" }]);
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "添加模板" }));
    const form = within(screen.getByRole("dialog", { name: "添加模板" }));
    for (const name of ["基础配置", "奖励内容", "使用条件", "使用限制", "特殊配置", "显示效果"]) expect(form.getByRole("group", { name })).toBeVisible();
    expect(form.getByRole("switch", { name: "状态" })).toBeChecked();
    await user.type(form.getByLabelText("模板名称"), "套餐测试");
    await user.selectOptions(form.getByLabelText("类型", { exact: true }), "2");
    expect(form.getByLabelText("指定套餐")).toHaveValue("");
    expect(form.getByLabelText("套餐有效期 (天)")).toHaveValue(null);
    await user.click(form.getByRole("button", { name: "确认" }));
    expect(api.createGiftCardTemplate).not.toHaveBeenCalled();
    await user.selectOptions(form.getByLabelText("指定套餐"), "12");
    await user.click(form.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ rewards: { plan_id: 12, plan_validity_days: 0 }, limits: { invite_reward_basis_points: undefined }, special_config: expect.objectContaining({ festival_multiplier_basis_points: undefined }) })));
  });

  it("validates fractional invitation rewards and retains inputs after save failure", async () => {
    const api = createAPI(); api.createGiftCardTemplate.mockRejectedValueOnce(new Error("保存暂时失败"));
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "添加模板" }));
    const form = within(screen.getByRole("dialog", { name: "添加模板" }));
    await user.type(form.getByLabelText("模板名称"), "比例测试");
    const ratio = form.getByLabelText("邀请人奖励比例");
    await user.type(ratio, "1.1"); await user.click(form.getByRole("button", { name: "确认" }));
    expect(await form.findByRole("alert")).toHaveTextContent("必须在 0 到 1 之间");
    expect(api.createGiftCardTemplate).not.toHaveBeenCalled();
    await user.clear(ratio); await user.type(ratio, "0.2");
    await user.type(form.getByLabelText("节日奖励乘数"), "1.5");
    await user.click(form.getByRole("button", { name: "确认" }));
    expect(await form.findByRole("alert")).toHaveTextContent("保存暂时失败");
    expect(ratio).toHaveValue("0.2");
    expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ limits: expect.objectContaining({ invite_reward_basis_points: 2000 }), special_config: expect.objectContaining({ festival_multiplier_basis_points: 15000 }) }));
  });

  it("round trips existing basis points and allows clearing optional multipliers", async () => {
    const api = createAPI(); api.listGiftCardTemplates.mockResolvedValue({ items: [{ ...template, limits: { invite_reward_basis_points: 2000 } }], total: 1, page: 1, page_size: 20 });
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "编辑" }));
    const form = within(screen.getByRole("dialog", { name: "编辑模板" }));
    expect(form.getByLabelText("邀请人奖励比例")).toHaveValue("0.2");
    expect(form.getByLabelText("节日奖励乘数")).toHaveValue("1");
    await user.clear(form.getByLabelText("节日奖励乘数"));
    await user.click(form.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.updateGiftCardTemplate).toHaveBeenCalledWith(7, expect.objectContaining({ limits: { invite_reward_basis_points: 2000 }, special_config: expect.objectContaining({ festival_multiplier_basis_points: undefined }) })));
  });

  it("exports a batch from the toolbar dialog without adding batch filters to the list", async () => {
    const api = createAPI(); api.exportGiftCardCodes.mockRejectedValueOnce(new Error("导出暂时失败"));
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:batch-export");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("tab", { name: "兑换码管理" })); await screen.findByText("暂无兑换码");
    expect(screen.queryByLabelText("批次号")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "导出" }));
    const form = within(screen.getByRole("dialog", { name: "导出" }));
    expect(form.getByRole("button", { name: "导出" })).toBeDisabled();
    await user.type(form.getByLabelText("批次号"), "fixture_batch");
    await user.click(form.getByRole("button", { name: "导出" }));
    expect(await form.findByRole("alert")).toHaveTextContent("导出暂时失败");
    expect(form.getByLabelText("批次号")).toHaveValue("fixture_batch");
    await user.click(form.getByRole("button", { name: "导出" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "导出" })).not.toBeInTheDocument());
    expect(api.exportGiftCardCodes).toHaveBeenLastCalledWith("fixture_batch");
    expect(api.listGiftCardCodes).toHaveBeenLastCalledWith(1, 20, "", undefined, undefined, "");
  });
  it("keeps blind reward decimal drafts editable and validates only on submission", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("button", { name: "添加模板" }));
    const form = within(screen.getByRole("dialog", { name: "添加模板" }));
    await user.type(form.getByLabelText("模板名称"), "盲盒测试");
    await user.selectOptions(form.getByLabelText("类型", { exact: true }), "3");
    await user.click(form.getByRole("button", { name: "添加随机奖励项" }));
    const balance = form.getByLabelText("奖励余额 (元)");
    await user.type(balance, "1."); expect(balance).toHaveValue("1.");
    await user.click(form.getByRole("button", { name: "确认" }));
    expect(await form.findByRole("alert")).toHaveTextContent("金额最多保留两位小数");
    expect(api.createGiftCardTemplate).not.toHaveBeenCalled();
    await user.type(balance, "25");
    await user.type(form.getByLabelText("奖励流量 (GB)"), "0.5");
    await user.click(form.getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ rewards: { random_rewards: [{ weight: 10, rewards: { balance: 125, transfer_enable: 536870912, expire_days: 0 } }] } })));
  });

  it("searches template names and lets administrators choose visible columns", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡");
    await user.type(screen.getByRole("textbox", { name: "搜索礼品卡" }), "新人");
    await waitFor(() => expect(api.listGiftCardTemplates).toHaveBeenLastCalledWith(1, 20, undefined, undefined, "新人"));
    await user.click(screen.getByText("显示列"));
    await user.click(screen.getByRole("checkbox", { name: "创建时间" }));
    expect(screen.queryByRole("columnheader", { name: "创建时间" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "创建时间" }));
    expect(screen.getByRole("columnheader", { name: "创建时间" })).toBeVisible();
  });

  it("selects eligible plans by name and submits their identifiers", async () => {
    const api = createAPI();
    api.listPlans.mockResolvedValue([{ id: 12, name: "可选套餐" }]);
    const user = userEvent.setup();
    render(<GiftCardManagementPage api={api} />);
    await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("button", { name: "添加模板" }));
    const dialog = screen.getByRole("dialog", { name: "添加模板" });
    await user.type(within(dialog).getByLabelText("模板名称"), "指定套餐礼品卡");
    await user.click(within(dialog).getByRole("button", { name: "允许的套餐" }));
    await user.click(within(dialog).getByRole("checkbox", { name: "可选套餐" }));
    await user.click(within(dialog).getByRole("button", { name: "允许的套餐" }));
    await user.click(within(dialog).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ conditions: expect.objectContaining({ allowed_plans: [12] }) })));
  });

  it("matches the four legacy tabs and converts Yuan/GiB before creating a general template", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    for (const tab of ["模板管理", "兑换码管理", "使用记录", "统计数据"]) expect(screen.getByRole("tab", { name: tab })).toBeVisible();
    expect(await screen.findByText("新人礼品卡")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加模板" }));
    const dialog = screen.getByRole("dialog", { name: "添加模板" });
    for (const field of ["模板名称", "类型", "描述", "奖励余额 (元)", "奖励流量 (GB)", "延长有效期 (天)", "增加设备数", "单用户最大使用次数", "同类卡冷却时间(小时)", "邀请人奖励比例", "节日奖励乘数", "活动开始时间", "活动结束时间", "图标", "背景图片"]) expect(within(dialog).getByLabelText(field)).toBeVisible();
    expect(within(dialog).queryByLabelText("主题色")).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText("单用户最大使用次数")).toHaveValue(null);
    await user.type(within(dialog).getByLabelText("邀请人奖励比例"), "0.1");
    await user.type(within(dialog).getByLabelText("模板名称"), "精准奖励");
    await user.clear(within(dialog).getByLabelText("奖励余额 (元)")); await user.type(within(dialog).getByLabelText("奖励余额 (元)"), "12.34");
    await user.clear(within(dialog).getByLabelText("奖励流量 (GB)")); await user.type(within(dialog).getByLabelText("奖励流量 (GB)"), "2.5");
    await user.click(within(dialog).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ name: "精准奖励", limits: expect.objectContaining({ invite_reward_basis_points: 1000 }), rewards: expect.objectContaining({ balance: 1234, transfer_enable: 2_684_354_560 }) })));
  });

  it("loads each management surface lazily", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("tab", { name: "兑换码管理" })); await waitFor(() => expect(api.listGiftCardCodes).toHaveBeenCalled());
    await user.click(screen.getByRole("tab", { name: "使用记录" })); await waitFor(() => expect(api.listGiftCardUsages).toHaveBeenCalled());
    await user.click(screen.getByRole("tab", { name: "统计数据" })); await waitFor(() => expect(api.getGiftCardStatistics).toHaveBeenCalled());
    expect(await screen.findByText("模板总数")).toBeVisible();
  });

  it("filters and paginates template records with server-side query values", async () => {
    const api = createAPI(); api.listGiftCardTemplates.mockResolvedValue({ items: [template], total: 21, page: 1, page_size: 20 });
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("button", {name:"模板类型"}));
    await user.click(screen.getByRole("checkbox", {name:"通用礼品卡"}));
    await user.keyboard("{Escape}");
    await user.click(screen.getByRole("button", {name:"模板状态"}));
    await user.click(screen.getByRole("checkbox", {name:"启用"}));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(api.listGiftCardTemplates).toHaveBeenCalledWith(1, 20, 1, true, ""));
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.listGiftCardTemplates).toHaveBeenCalledWith(2, 20, 1, true, ""));
  });

  it("edits codes, clears expiry, and exports the selected legacy batch", async () => {
    const api = createAPI();
    const code = { id: 9, template_id: 7, template_name: "新人礼品卡", code: "LEGACYGC00000009", batch_no: "legacy_batch_0009", status: 0 as const, user_id: null, used_at: null, expires_at: null, usage_count: 0, max_usage: 2, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" };
    api.listGiftCardCodes.mockResolvedValue({ items: [code], total: 1, page: 1, page_size: 20 }); api.updateGiftCardCode.mockResolvedValue(code);
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:gift-codes"); vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("tab", { name: "兑换码管理" })); await screen.findByText("LEGACYGC00000009");
    await user.click(screen.getByRole("button", { name: "编辑" })); const dialog = screen.getByRole("dialog", { name: "编辑兑换码" }); await user.click(within(dialog).getByRole("button", { name: "保存兑换码" }));
    await waitFor(() => expect(api.updateGiftCardCode).toHaveBeenCalledWith(9, expect.objectContaining({ expires_at: null, max_usage: 2 })));
    await user.click(screen.getByRole("button", { name: "导出批次" })); await waitFor(() => expect(api.exportGiftCardCodes).toHaveBeenCalledWith("legacy_batch_0009")); expect(createObjectURL).toHaveBeenCalled();
  });

  it("exposes activity window and mystery reward expiry fields", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "添加模板" }));
    const dialog = screen.getByRole("dialog", { name: "添加模板" }); await user.selectOptions(within(dialog).getByLabelText("类型"), "3"); await user.click(within(dialog).getByRole("button", { name: "添加随机奖励项" }));
    expect(within(dialog).getByLabelText("延长有效期 (天)")).toBeVisible(); expect(within(dialog).getByLabelText("活动开始时间")).toBeVisible(); expect(within(dialog).getByLabelText("活动结束时间")).toBeVisible();
  });

  it("reloads the current code page immediately after generating a code", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("tab", { name: "兑换码管理" })); await screen.findByText("暂无兑换码");
    await user.click(screen.getByRole("button", { name: "生成兑换码" })); const dialog = screen.getByRole("dialog", { name: "生成兑换码" });
    await user.click(within(dialog).getByRole("button", { name: "生成兑换码" }));
    await waitFor(() => expect(api.generateGiftCardCodes).toHaveBeenCalled());
    await waitFor(() => expect(api.listGiftCardCodes.mock.calls.length).toBeGreaterThanOrEqual(2));
  });

  it("generates and downloads CSV when the legacy export option is selected", async () => {
    const api = createAPI(); const user = userEvent.setup();
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:generated-gift-cards");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("tab", { name: "兑换码管理" })); await screen.findByText("暂无兑换码");
    await user.click(screen.getByRole("button", { name: "生成兑换码" })); const dialog = screen.getByRole("dialog", { name: "生成兑换码" });
    await user.click(within(dialog).getByLabelText("导出CSV")); await user.click(within(dialog).getByRole("button", { name: "生成兑换码" }));
    await waitFor(() => expect(api.generateGiftCardCodesCSV).toHaveBeenCalledWith(7, 1, "GC", null, 1));
    expect(api.generateGiftCardCodes).not.toHaveBeenCalled(); expect(createObjectURL).toHaveBeenCalled();
  });
});
