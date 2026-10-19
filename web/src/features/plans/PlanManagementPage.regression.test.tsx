import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AdminOrderPage, Plan, ServerGroup } from "../../lib/api";
import { adminDataCache } from "../../lib/dataPrefetchCache";
import { OrderManagementPage } from "../orders/OrderManagementPage";
import { PlanManagementPage } from "./PlanManagementPage";
import { deferred } from "../../test/deferred";

const plan: Plan = {
  id: 11, group_id: null, transfer_enable: 100, name: "QA Original", speed_limit: 0, show: true, sort: 0,
  renew: true, content: "", reset_traffic_method: 1, capacity_limit: 0, prices: { monthly: 123 },
  sell: true, device_limit: 3, tags: [], users_count: 0, active_users_count: 0, capacity_users_count: 0, revision: 1,
  created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z"
};
const second: Plan = { ...plan, id: 12, name: "QA Second", sort: 1 };

beforeEach(() => {
  adminDataCache.invalidatePrefix("");
  vi.spyOn(Date, "now").mockReturnValue(1_000);
});
afterEach(() => adminDataCache.invalidatePrefix(""));

function planAPI(plans: Plan[] = [plan]) {
  return {
    listPlans: vi.fn().mockResolvedValue(plans), listServerGroups: vi.fn().mockResolvedValue([]),
    createPlan: vi.fn(), updatePlan: vi.fn(), setPlanState: vi.fn(), reorderPlans: vi.fn(), deletePlan: vi.fn()
  };
}

async function seed(plans: Plan[] = [plan]) {
  await adminDataCache.prefetch("plans", () => Promise.resolve<[Plan[], ServerGroup[]]>([plans, []]));
}

describe("plan prefetch invalidation", () => {
  it("shows the persisted state and latest revision after remounting within the prefetch TTL", async () => {
    await seed();
    const saved = { ...plan, sell: false, revision: 2 };
    const api = planAPI([saved]);
    api.setPlanState.mockResolvedValueOnce(saved).mockResolvedValueOnce({ ...saved, sell: true, revision: 3 });
    const user = userEvent.setup();
    const first = render(<PlanManagementPage api={api} />);
    await user.click(await screen.findByRole("switch", { name: "新购" }));
    await waitFor(() => expect(screen.getByRole("switch", { name: "新购" })).toBeEnabled());
    first.unmount();
    render(<PlanManagementPage api={api} />);
    const sell = await screen.findByRole("switch", { name: "新购" });
    expect(sell).not.toBeChecked();
    await user.click(sell);
    expect(api.setPlanState).toHaveBeenLastCalledWith(plan.id, 2, true, true, true);
  });

  it.each(["edit", "create", "delete", "reorder"] as const)("does not revive the old list after %s succeeds", async (action) => {
    await seed([plan, second]);
    const saved = { ...plan, name: "QA Saved", revision: 2 };
    const expected = action === "edit" ? [saved, second] : action === "create" ? [plan, second, { ...saved, id: 13 }]
      : action === "delete" ? [second] : [second, plan];
    const api = planAPI(expected);
    api.updatePlan.mockResolvedValue(saved);
    api.createPlan.mockResolvedValue(expected[2]);
    api.deletePlan.mockResolvedValue(undefined);
    api.reorderPlans.mockResolvedValue(expected);
    const user = userEvent.setup();
    const first = render(<PlanManagementPage api={api} />);
    await screen.findByText(plan.name);
    if (action === "edit" || action === "create") {
      await user.click(screen.getByRole("button", { name: action === "edit" ? `编辑套餐：${plan.name}` : "添加套餐" }));
      const dialog = within(screen.getByRole("dialog", { name: action === "edit" ? "编辑套餐" : "添加套餐" }));
      fireEvent.change(dialog.getByLabelText("套餐名称"), { target: { value: saved.name } });
      if (action === "create") fireEvent.change(dialog.getByLabelText("流量（GB）"), { target: { value: "100" } });
      await user.click(dialog.getByRole("button", { name: "提交" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    } else if (action === "delete") {
      await user.click(screen.getByRole("button", { name: `删除套餐：${plan.name}` }));
      await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认删除" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    } else {
      await user.click(screen.getByRole("button", { name: "编辑排序" }));
      await user.click(screen.getByRole("button", { name: `上移套餐：${second.name}` }));
      await user.click(screen.getByRole("button", { name: "保存排序" }));
      await waitFor(() => expect(screen.queryByRole("button", { name: "保存排序" })).not.toBeInTheDocument());
    }
    first.unmount();
    render(<PlanManagementPage api={api} />);
    await screen.findByText(expected[0]!.name);
    expect(screen.getAllByRole("button", { name: /^编辑套餐：/ }).map(button => button.getAttribute("aria-label")))
      .toEqual(expected.map(item => `编辑套餐：${item.name}`));
    expect(api.listPlans).toHaveBeenCalledOnce();
  });

  it("refreshes the order page's prefetched plan choices after editing a plan", async () => {
    await seed();
    const orders: AdminOrderPage = { items: [], total: 0, page: 1, page_size: 20 };
    await adminDataCache.prefetch("orders", () => Promise.resolve([orders, [plan]]));
    const saved = { ...plan, name: "QA Renamed", revision: 2 };
    const api = planAPI([saved]);
    api.updatePlan.mockResolvedValue(saved);
    const user = userEvent.setup();
    const first = render(<PlanManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `编辑套餐：${plan.name}` }));
    fireEvent.change(screen.getByLabelText("套餐名称"), { target: { value: saved.name } });
    await user.click(screen.getByRole("button", { name: "提交" }));
    await screen.findByText(saved.name);
    first.unmount();
    const orderAPI = { listAdminOrders: vi.fn().mockResolvedValue(orders), listPlans: api.listPlans,
      getAdminOrder: vi.fn(), assignOrder: vi.fn(), paidAdminOrder: vi.fn(), cancelAdminOrder: vi.fn(), updateAdminOrderCommissionStatus: vi.fn() };
    render(<OrderManagementPage api={orderAPI} />);
    await screen.findByText("没有符合条件的订单。");
    await user.click(screen.getByRole("button", { name: "添加订单" }));
    expect(screen.getByRole("option", { name: saved.name })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: plan.name })).not.toBeInTheDocument();
  });

  it.each(["resolve", "reject"] as const)("ignores an initial request that settles by %s after a create and fresh reload", async (settlement) => {
    const old = deferred<[Plan[], ServerGroup[]]>();
    void adminDataCache.prefetch("plans", () => old.promise);
    const saved = { ...plan, id: 13, name: "QA Created" };
    const api = planAPI([plan, saved]);
    api.createPlan.mockResolvedValue(saved);
    const user = userEvent.setup();
    render(<PlanManagementPage api={api} />);
    await user.click(screen.getByRole("button", { name: "添加套餐" }));
    fireEvent.change(screen.getByLabelText("套餐名称"), { target: { value: saved.name } });
    fireEvent.change(screen.getByLabelText("流量（GB）"), { target: { value: "100" } });
    await user.click(screen.getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.createPlan).toHaveBeenCalledOnce());
    await act(async () => {
      if (settlement === "resolve") old.resolve([[plan], []]);
      else old.reject(new Error("obsolete initial request"));
      await old.promise.catch(() => undefined);
    });
    expect(await screen.findByText(saved.name)).toBeVisible();
    expect(screen.getByText(plan.name)).toBeVisible();
    expect(screen.queryByText("obsolete initial request")).not.toBeInTheDocument();
  });
});
