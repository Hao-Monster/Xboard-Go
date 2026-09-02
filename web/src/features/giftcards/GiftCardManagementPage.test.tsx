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
  it("matches the four legacy tabs and converts Yuan/GiB before creating a general template", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />);
    for (const tab of ["模板管理", "兑换码管理", "使用记录", "统计数据"]) expect(screen.getByRole("button", { name: tab })).toBeVisible();
    expect(await screen.findByText("新人礼品卡")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加模板" }));
    const dialog = screen.getByRole("dialog", { name: "添加礼品卡模板" });
    for (const field of ["模板名称", "礼品卡类型", "模板描述", "余额（CNY）", "流量（GB）", "有效期（天）", "设备数", "每用户最多使用次数", "冷却时间（小时）", "邀请奖励比例", "节日奖励倍率", "活动开始时间", "活动结束时间", "图标", "背景图片", "主题色"]) expect(within(dialog).getByLabelText(field)).toBeVisible();
    await user.type(within(dialog).getByLabelText("模板名称"), "精准奖励");
    await user.clear(within(dialog).getByLabelText("余额（CNY）")); await user.type(within(dialog).getByLabelText("余额（CNY）"), "12.34");
    await user.clear(within(dialog).getByLabelText("流量（GB）")); await user.type(within(dialog).getByLabelText("流量（GB）"), "2.5");
    await user.click(within(dialog).getByRole("button", { name: "保存模板" }));
    await waitFor(() => expect(api.createGiftCardTemplate).toHaveBeenCalledWith(expect.objectContaining({ name: "精准奖励", rewards: expect.objectContaining({ balance: 1234, transfer_enable: 2_684_354_560 }) })));
  });

  it("loads each management surface lazily", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("button", { name: "兑换码管理" })); await waitFor(() => expect(api.listGiftCardCodes).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: "使用记录" })); await waitFor(() => expect(api.listGiftCardUsages).toHaveBeenCalled());
    await user.click(screen.getByRole("button", { name: "统计数据" })); await waitFor(() => expect(api.getGiftCardStatistics).toHaveBeenCalled());
    expect(await screen.findByText("模板总数")).toBeVisible();
  });

  it("filters and paginates template records with server-side query values", async () => {
    const api = createAPI(); api.listGiftCardTemplates.mockResolvedValue({ items: [template], total: 21, page: 1, page_size: 20 });
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.selectOptions(screen.getByLabelText("模板类型"), "1");
    await user.selectOptions(screen.getByLabelText("模板状态"), "true");
    await waitFor(() => expect(api.listGiftCardTemplates).toHaveBeenCalledWith(1, 20, 1, true));
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.listGiftCardTemplates).toHaveBeenCalledWith(2, 20, 1, true));
  });

  it("edits codes, clears expiry, and exports the selected legacy batch", async () => {
    const api = createAPI();
    const code = { id: 9, template_id: 7, template_name: "新人礼品卡", code: "LEGACYGC00000009", batch_no: "legacy_batch_0009", status: 0 as const, user_id: null, used_at: null, expires_at: null, usage_count: 0, max_usage: 2, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" };
    api.listGiftCardCodes.mockResolvedValue({ items: [code], total: 1, page: 1, page_size: 20 }); api.updateGiftCardCode.mockResolvedValue(code);
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:gift-codes"); vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "兑换码管理" })); await screen.findByText("LEGACYGC00000009");
    await user.click(screen.getByRole("button", { name: "编辑" })); const dialog = screen.getByRole("dialog", { name: "编辑兑换码" }); await user.click(within(dialog).getByRole("button", { name: "保存兑换码" }));
    await waitFor(() => expect(api.updateGiftCardCode).toHaveBeenCalledWith(9, expect.objectContaining({ expires_at: null, max_usage: 2 })));
    await user.click(screen.getByRole("button", { name: "导出批次" })); await waitFor(() => expect(api.exportGiftCardCodes).toHaveBeenCalledWith("legacy_batch_0009")); expect(createObjectURL).toHaveBeenCalled();
  });

  it("exposes activity window and mystery reward expiry fields", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡"); await user.click(screen.getByRole("button", { name: "添加模板" }));
    const dialog = screen.getByRole("dialog", { name: "添加礼品卡模板" }); await user.selectOptions(within(dialog).getByLabelText("礼品卡类型"), "3"); await user.click(within(dialog).getByRole("button", { name: "添加随机奖励" }));
    expect(within(dialog).getByLabelText("有效期（天）")).toBeVisible(); expect(within(dialog).getByLabelText("活动开始时间")).toBeVisible(); expect(within(dialog).getByLabelText("活动结束时间")).toBeVisible();
  });

  it("reloads the current code page immediately after generating a code", async () => {
    const api = createAPI(); const user = userEvent.setup(); render(<GiftCardManagementPage api={api} />); await screen.findByText("新人礼品卡");
    await user.click(screen.getByRole("button", { name: "兑换码管理" })); await screen.findByText("暂无兑换码");
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
    await user.click(screen.getByRole("button", { name: "兑换码管理" })); await screen.findByText("暂无兑换码");
    await user.click(screen.getByRole("button", { name: "生成兑换码" })); const dialog = screen.getByRole("dialog", { name: "生成兑换码" });
    await user.click(within(dialog).getByLabelText("导出CSV")); await user.click(within(dialog).getByRole("button", { name: "生成兑换码" }));
    await waitFor(() => expect(api.generateGiftCardCodesCSV).toHaveBeenCalledWith(7, 1, "GC", null, 1));
    expect(api.generateGiftCardCodes).not.toHaveBeenCalled(); expect(createObjectURL).toHaveBeenCalled();
  });
});
