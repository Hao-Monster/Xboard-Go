import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Order, Plan, PlanOffer, ServerGroup } from "../../lib/api";
import { PlanCatalogPage } from "./PlanCatalogPage";
import { PlanManagementPage } from "./PlanManagementPage";

const group: ServerGroup = { id: 7, name: "Premium", users_count: 0, server_count: 0, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" };
const plan: Plan = {
  id: 11, group_id: 7, transfer_enable: 100, name: "Pro", speed_limit: 200, show: true, sort: 0,
  renew: true, content: "稳定套餐", reset_traffic_method: 1, capacity_limit: 0, prices: { monthly: 123 },
  sell: true, device_limit: 3, tags: ["推荐"], users_count: 3, active_users_count: 2, capacity_users_count: 2, revision: 1,
  created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z"
};

describe("PlanManagementPage", () => {
  it("exposes the legacy plan fields and converts major-unit prices to integer cents", async () => {
    const created: Plan = { ...plan, id: 12, name: "Starter", show: false, sell: false, renew: true, prices: { monthly: 199 } };
    const api = planAPI([plan]);
    api.createPlan.mockResolvedValue(created);
    const user = userEvent.setup();
    render(<PlanManagementPage api={api} />);

    expect(await screen.findByText("Pro")).toBeVisible();
    expect(screen.getByText("不限量")).toBeVisible();
    expect(screen.getByText("总 3")).toBeVisible();
    expect(screen.getByText("有效 2 · 活跃率 67%")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加套餐" }));
    const dialog = screen.getByRole("dialog", { name: "添加套餐" });
    for (const label of ["套餐名称", "标签", "服务器分组", "流量（GiB）", "速度限制", "设备限制", "容量限制", "流量重置方式", "月付", "季付", "半年付", "年付", "两年付", "三年付", "流量包", "重置包", "套餐描述"]) {
      expect(within(dialog).getByLabelText(label)).toBeVisible();
    }
    await user.click(within(dialog).getByRole("button", { name: "使用模板" }));
    const description = within(dialog).getByLabelText("套餐描述");
    expect(within(dialog).getByDisplayValue(/## 套餐特点/)).toBe(description);
    await user.click(within(dialog).getByRole("button", { name: "显示预览" }));
    expect(within(dialog).getByRole("heading", { name: "套餐特点" })).toBeVisible();
    await user.clear(description);
    await user.type(within(dialog).getByLabelText("套餐名称"), "Starter");
    const traffic = within(dialog).getByLabelText("流量（GiB）");
    await user.clear(traffic);
    await user.type(traffic, "50");
    await user.selectOptions(within(dialog).getByLabelText("服务器分组"), "7");
    await user.type(within(dialog).getByLabelText("月付"), "1.99");
    await user.type(within(dialog).getByLabelText("标签"), "入门, 稳定");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));

    await waitFor(() => expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({
      name: "Starter", transfer_enable: 50, group_id: 7, prices: { monthly: 199 }, tags: ["入门", "稳定"]
    })));
    expect(await screen.findByText("Starter")).toBeVisible();
  });

  it("updates state, reorders, searches, and keeps delete failures visible", async () => {
    const second: Plan = { ...plan, id: 12, name: "Enterprise", sort: 1, revision: 3 };
    const api = planAPI([plan, second]);
    api.setPlanState.mockResolvedValue({ ...plan, sell: false, revision: 2 });
    api.reorderPlans.mockResolvedValue([{ ...second, sort: 0, revision: 4 }, { ...plan, sort: 1, revision: 3 }]);
    api.deletePlan.mockRejectedValue(new Error("套餐仍被用户使用"));
    const user = userEvent.setup();
    render(<PlanManagementPage api={api} />);
    expect(await screen.findByText("Enterprise")).toBeVisible();

    await user.click(screen.getAllByRole("checkbox", { name: "销售" })[0]!);
    await waitFor(() => expect(api.setPlanState).toHaveBeenCalledWith(11, 1, true, false, true));
    await user.type(screen.getByRole("searchbox", { name: "搜索套餐" }), "enterprise");
    expect(screen.queryByText("Pro")).not.toBeInTheDocument();
    await user.clear(screen.getByRole("searchbox", { name: "搜索套餐" }));

    await user.click(screen.getByRole("button", { name: "编辑排序" }));
    await user.click(screen.getByRole("button", { name: "上移套餐：Enterprise" }));
    await user.click(screen.getByRole("button", { name: "保存排序" }));
    await waitFor(() => expect(api.reorderPlans).toHaveBeenCalledWith([12, 11]));

    await user.click(screen.getByRole("button", { name: "删除套餐：Pro" }));
    const dialog = screen.getByRole("dialog", { name: "删除套餐" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("套餐仍被用户使用");
  });

  it("updates a state checkbox immediately and rolls it back when persistence fails", async () => {
    const api = planAPI([plan]);
    let rejectState: ((reason: Error) => void) | undefined;
    api.setPlanState.mockImplementation(() => new Promise<Plan>((_resolve, reject) => { rejectState = reject; }));
    const user = userEvent.setup();
    render(<PlanManagementPage api={api} />);
    const sell = await screen.findByRole("checkbox", { name: "销售" });

    await user.click(sell);
    expect(sell).not.toBeChecked();
    expect(sell).toBeDisabled();
    rejectState?.(new Error("状态保存失败"));

    expect(await screen.findByRole("alert")).toHaveTextContent("状态保存失败");
    await waitFor(() => expect(sell).toBeChecked());
    expect(sell).toBeEnabled();
  });

  it("rejects fractional cents instead of rounding a displayed price", async () => {
    const api = planAPI([]);
    const user = userEvent.setup();
    render(<PlanManagementPage api={api} />);
    await screen.findByText("尚未创建套餐。");
    await user.click(screen.getByRole("button", { name: "添加套餐" }));
    const dialog = screen.getByRole("dialog", { name: "添加套餐" });
    await user.type(within(dialog).getByLabelText("套餐名称"), "Exact price");
    fireEvent.change(within(dialog).getByLabelText("月付"), { target: { value: "1.001" } });
    const form = dialog.querySelector("form");
    if (form === null) throw new Error("plan form is missing");
    fireEvent.submit(form);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("月付价格无效");
    expect(api.createPlan).not.toHaveBeenCalled();
  });
});

describe("PlanCatalogPage", () => {
  it("renders unlimited capacity without the legacy false sold-out label", async () => {
    const offer: PlanOffer = { ...plan, content: "## 稳定套餐", prices: { monthly: 123, reset_traffic: 50 }, capacity_remaining: null, can_purchase: true, can_renew: false };
    const order = { id: 1, trade_no: "2026082612345600000000001", plan_id: offer.id, period: "monthly", total_amount: 123, status: 0 } as Order;
    const createOrder = vi.fn().mockResolvedValue(order);
    const onOrderCreated = vi.fn();
    const user = userEvent.setup();
    render(<PlanCatalogPage api={{ listPlanOffers: vi.fn().mockResolvedValue([offer]), checkCoupon: vi.fn(), createOrder }} couponEnabled={false} onOrderCreated={onOrderCreated} />);
    expect(await screen.findByRole("heading", { name: "Pro" })).toBeVisible();
    expect(screen.getByText("不限量")).toBeVisible();
    expect(screen.getByText("月付 ¥1.23")).toBeVisible();
    expect(screen.getByText("重置包 ¥0.50")).toBeVisible();
    expect(screen.getByRole("heading", { name: "稳定套餐" })).toBeVisible();
    expect(screen.getByText("可购买")).toBeVisible();
    expect(screen.queryByText(/sold out/i)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "立即订阅" }));
    const dialog = screen.getByRole("dialog", { name: "配置订阅" });
    expect(within(dialog).getByText("套餐标价")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "下单" }));
    await waitFor(() => expect(createOrder).toHaveBeenCalledWith(offer.id, "monthly", undefined));
    expect(onOrderCreated).toHaveBeenCalledWith(order);
  });
});

function planAPI(plans: Plan[]) {
  return {
    listPlans: vi.fn().mockResolvedValue(plans), listServerGroups: vi.fn().mockResolvedValue([group]),
    createPlan: vi.fn(), updatePlan: vi.fn(), setPlanState: vi.fn(), reorderPlans: vi.fn(), deletePlan: vi.fn()
  };
}
