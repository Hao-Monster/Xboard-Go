import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AdminOrderDetail, AdminOrderPage, AdminOrderQuery, Plan } from "../../lib/api";
import { adminDataCache } from "../../lib/dataPrefetchCache";
import { OrderManagementPage } from "./OrderManagementPage";
import { deferred } from "../../test/deferred";

const first: AdminOrderDetail = {
  id: 11, user_id: 42, plan_id: 7, payment_id: null, period: "monthly", trade_no: "QA-ORDER-A",
  original_amount: 0, total_amount: 0, handling_amount: null, balance_amount: 0, surplus_credit: 0, surplus_amount: 0,
  type: 1, status: 0, surplus_order_ids: [], coupon_id: null, commission_status: 0, invite_user_id: null,
  actual_commission_balance: null, commission_rate: null, commission_auto_check: null, commission_balance: 0,
  discount_amount: 0, paid_at: null, callback_no: null, entitlement_expired_at_before: null,
  entitlement_expired_at_after: null, created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z",
  user_email: "qa-a@example.test", plan_name: "QA Plan", invite_user: null, commission_log: [], subscribe_url: null
};
const second: AdminOrderDetail = { ...first, id: 12, trade_no: "QA-ORDER-B", user_email: "qa-b@example.test" };

afterEach(() => adminDataCache.invalidatePrefix(""));

function orderAPI() {
  return {
    listAdminOrders: vi.fn<(query?: AdminOrderQuery) => Promise<AdminOrderPage>>().mockResolvedValue({ items: [first, second], total: 2, page: 1, page_size: 20 }),
    getAdminOrder: vi.fn<(tradeNo: string) => Promise<AdminOrderDetail>>(),
    assignOrder: vi.fn(), paidAdminOrder: vi.fn(), cancelAdminOrder: vi.fn(), updateAdminOrderCommissionStatus: vi.fn(),
    listPlans: vi.fn().mockResolvedValue([])
  };
}

describe("administrator order detail request lifetime", () => {
  it.each(["resolve", "reject"] as const)("keeps B selected when the older A request settles by %s", async settlement => {
    const a = deferred<AdminOrderDetail>();
    const b = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockImplementation(tradeNo => tradeNo === first.trade_no ? a.promise : b.promise);
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(screen.getByRole("button", { name: `查看订单：${second.trade_no}` }));
    await act(async () => { b.resolve(second); await b.promise; });
    expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText(second.trade_no)).toBeVisible();
    await act(async () => {
      if (settlement === "resolve") a.resolve(first);
      else a.reject(new Error("obsolete A failure"));
      await a.promise.catch(() => undefined);
    });
    expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText(second.trade_no)).toBeVisible();
    expect(screen.queryByText("obsolete A failure")).not.toBeInTheDocument();
  });

  it("does not finish B's loading indicator when the old A request settles first", async () => {
    const a = deferred<AdminOrderDetail>();
    const b = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockImplementation(tradeNo => tradeNo === first.trade_no ? a.promise : b.promise);
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(screen.getByRole("button", { name: `查看订单：${second.trade_no}` }));
    await act(async () => { a.resolve(first); await a.promise; });
    expect(screen.getByRole("status")).toHaveTextContent("正在读取订单详情");
    expect(screen.queryByRole("dialog", { name: "订单详情" })).not.toBeInTheDocument();
    await act(async () => { b.resolve(second); await b.promise; });
    expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText(second.trade_no)).toBeVisible();
  });

  it("does not reopen a closed dialog when an older read returns", async () => {
    const a = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockImplementation(tradeNo => tradeNo === first.trade_no ? a.promise : Promise.resolve(second));
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(screen.getByRole("button", { name: `查看订单：${second.trade_no}` }));
    await user.click(within(await screen.findByRole("dialog", { name: "订单详情" })).getByRole("button", { name: "关闭" }));
    await act(async () => { a.resolve(first); await a.promise; });
    expect(screen.queryByRole("dialog", { name: "订单详情" })).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it.each([false, true])("keeps old action responses out of the dialog but synchronizes the list (reopen=%s)", async reopen => {
    const action = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockImplementation(tradeNo => Promise.resolve(tradeNo === first.trade_no ? first : second));
    api.cancelAdminOrder.mockReturnValue(action.promise);
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    const dialog = within(await screen.findByRole("dialog", { name: "订单详情" }));
    await user.click(dialog.getByRole("button", { name: "取消订单" }));
    expect(api.cancelAdminOrder).toHaveBeenCalledExactlyOnceWith(first.trade_no);
    await user.click(dialog.getByRole("button", { name: "关闭" }));
    if (reopen) {
      await user.click(screen.getByRole("button", { name: `查看订单：${first.trade_no}` }));
      await screen.findByRole("dialog", { name: "订单详情" });
    }
    await adminDataCache.prefetch("orders", () => Promise.resolve([{ items: [first, second], total: 2, page: 1, page_size: 20 }, []]));
    const canceled: AdminOrderDetail = { ...first, status: 2 };
    api.listAdminOrders.mockResolvedValue({ items: [canceled, second], total: 2, page: 1, page_size: 20 });
    await act(async () => { action.resolve(canceled); await action.promise; });
    if (reopen) expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText("待支付", { exact: true })).toBeVisible();
    else expect(screen.queryByRole("dialog", { name: "订单详情" })).not.toBeInTheDocument();
    expect(within(screen.getByRole("row", { name: new RegExp(first.trade_no) })).getByText("已取消")).toBeVisible();
    expect(api.listAdminOrders).toHaveBeenCalledTimes(2);
    expect(adminDataCache.consume("orders")).toBeUndefined();
  });

  it("refreshes the current search rather than the query captured by a closed dialog", async () => {
    const action = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockResolvedValue(first);
    api.cancelAdminOrder.mockReturnValue(action.promise);
    api.listAdminOrders.mockImplementation(query => Promise.resolve({
      items: query?.query === second.trade_no ? [second] : [first, second],
      total: query?.query === second.trade_no ? 1 : 2, page: 1, page_size: 20
    }));
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    const dialog = within(await screen.findByRole("dialog", { name: "订单详情" }));
    await user.click(dialog.getByRole("button", { name: "取消订单" }));
    await user.click(dialog.getByRole("button", { name: "关闭" }));
    await user.type(screen.getByRole("searchbox", { name: "搜索订单" }), second.trade_no);
    await user.click(screen.getByRole("button", { name: "查询订单" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: `查看订单：${first.trade_no}` })).not.toBeInTheDocument());
    await act(async () => { action.resolve({ ...first, status: 2 }); await action.promise; });
    expect(api.listAdminOrders).toHaveBeenCalledTimes(3);
    expect(api.listAdminOrders).toHaveBeenLastCalledWith({ page: 1, page_size: 20, query: second.trade_no });
    expect(screen.queryByRole("button", { name: `查看订单：${first.trade_no}` })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: `查看订单：${second.trade_no}` })).toBeVisible();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it.each(["resolve", "reject"] as const)("ignores an older action-triggered list refresh that settles by %s", async settlement => {
    const action = deferred<AdminOrderDetail>();
    const oldRefresh = deferred<AdminOrderPage>();
    const api = orderAPI();
    const canceled: AdminOrderDetail = { ...first, status: 2 };
    api.getAdminOrder.mockResolvedValue(first);
    api.cancelAdminOrder.mockReturnValue(action.promise);
    api.listAdminOrders.mockResolvedValueOnce({ items: [first, second], total: 2, page: 1, page_size: 20 })
      .mockReturnValueOnce(oldRefresh.promise)
      .mockResolvedValueOnce({ items: [second, canceled], total: 2, page: 1, page_size: 20 });
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    const dialog = within(await screen.findByRole("dialog", { name: "订单详情" }));
    await user.click(dialog.getByRole("button", { name: "取消订单" }));
    await user.click(dialog.getByRole("button", { name: "关闭" }));
    await act(async () => { action.resolve(canceled); await action.promise; });
    expect(api.listAdminOrders).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole("button", { name: "按订单金额排序" }));
    await act(async () => {
      if (settlement === "resolve") oldRefresh.resolve({ items: [first, second], total: 2, page: 1, page_size: 20 });
      else oldRefresh.reject(new Error("obsolete list failure"));
      await oldRefresh.promise.catch(() => undefined);
    });
    expect(screen.getAllByRole("button", { name: /^查看订单：/ }).map(button => button.getAttribute("aria-label")))
      .toEqual([`查看订单：${second.trade_no}`, `查看订单：${first.trade_no}`]);
    expect(within(screen.getByRole("row", { name: new RegExp(first.trade_no) })).getByText("已取消")).toBeVisible();
    expect(screen.queryByText("obsolete list failure")).not.toBeInTheDocument();
    expect(api.listAdminOrders).toHaveBeenLastCalledWith({ page: 1, page_size: 20, sort_by: "total_amount", sort_desc: false });
  });

  it("discards an old API's dialog and callbacks while new API actions still synchronize", async () => {
    const action = deferred<AdminOrderDetail>();
    const previousAPI = orderAPI();
    previousAPI.getAdminOrder.mockResolvedValue(first);
    previousAPI.cancelAdminOrder.mockReturnValue(action.promise);
    const api = orderAPI();
    const canceled: AdminOrderDetail = { ...first, status: 2 };
    api.getAdminOrder.mockResolvedValue(first);
    api.cancelAdminOrder.mockResolvedValue(canceled);
    api.listAdminOrders.mockResolvedValueOnce({ items: [first, second], total: 2, page: 1, page_size: 20 })
      .mockResolvedValue({ items: [canceled, second], total: 2, page: 1, page_size: 20 });
    const user = userEvent.setup();
    const view = render(<OrderManagementPage api={previousAPI} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(within(await screen.findByRole("dialog", { name: "订单详情" })).getByRole("button", { name: "取消订单" }));
    view.rerender(<OrderManagementPage api={api} />);
    expect(screen.queryByRole("dialog", { name: "订单详情" })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(within(await screen.findByRole("dialog", { name: "订单详情" })).getByRole("button", { name: "取消订单" }));
    expect(within(screen.getByRole("row", { name: new RegExp(first.trade_no) })).getByText("已取消")).toBeVisible();
    await act(async () => { action.resolve(canceled); await action.promise; });
    expect(previousAPI.listAdminOrders).toHaveBeenCalledOnce();
    expect(api.listAdminOrders).toHaveBeenCalledTimes(2);
  });

  it("keeps new API plan choices when its initial order query is superseded by sorting", async () => {
    const plan: Plan = {
      id: 7, group_id: null, transfer_enable: 100, name: "QA New API Plan", speed_limit: 0,
      show: true, sort: 0, renew: true, content: "", reset_traffic_method: 1, capacity_limit: 0,
      prices: { monthly: 123 }, sell: true, device_limit: 3, tags: [], users_count: 0,
      active_users_count: 0, capacity_users_count: 0, revision: 1,
      created_at: "2026-10-10T00:00:00Z", updated_at: "2026-10-10T00:00:00Z"
    };
    const pendingPlans = deferred<Plan[]>();
    const previousAPI = orderAPI();
    previousAPI.listPlans.mockResolvedValue([{ ...plan, id: 6, name: "QA Old Plan" }]);
    const api = orderAPI();
    api.listPlans.mockReturnValue(pendingPlans.promise);
    api.listAdminOrders.mockResolvedValueOnce({ items: [first, second], total: 2, page: 1, page_size: 20 })
      .mockResolvedValueOnce({ items: [second, first], total: 2, page: 1, page_size: 20 });
    const user = userEvent.setup();
    const view = render(<OrderManagementPage api={previousAPI} />);
    await screen.findByRole("button", { name: `查看订单：${first.trade_no}` });
    view.rerender(<OrderManagementPage api={api} />);
    await user.click(screen.getByRole("button", { name: "按订单金额排序" }));
    await act(async () => { pendingPlans.resolve([plan]); await pendingPlans.promise; });
    expect(screen.getAllByRole("button", { name: /^查看订单：/ }).map(button => button.getAttribute("aria-label")))
      .toEqual([`查看订单：${second.trade_no}`, `查看订单：${first.trade_no}`]);
    expect(api.listAdminOrders).toHaveBeenLastCalledWith({ page: 1, page_size: 20, sort_by: "total_amount", sort_desc: false });
    await user.click(screen.getByRole("button", { name: "添加订单" }));
    expect(screen.getByRole("option", { name: plan.name })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "QA Old Plan" })).not.toBeInTheDocument();
  });

  it("ignores old reads and action callbacks after unmount", async () => {
    const read = deferred<AdminOrderDetail>();
    const action = deferred<AdminOrderDetail>();
    const api = orderAPI();
    api.getAdminOrder.mockImplementation(tradeNo => tradeNo === first.trade_no ? read.promise : Promise.resolve(second));
    api.cancelAdminOrder.mockReturnValue(action.promise);
    const user = userEvent.setup();
    const view = render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    await user.click(screen.getByRole("button", { name: `查看订单：${second.trade_no}` }));
    await user.click(within(await screen.findByRole("dialog", { name: "订单详情" })).getByRole("button", { name: "取消订单" }));
    await adminDataCache.prefetch("orders", () => Promise.resolve([{ items: [first, second], total: 2, page: 1, page_size: 20 }, []]));
    view.unmount();
    await act(async () => { read.resolve(first); action.resolve({ ...second, status: 2 }); await Promise.all([read.promise, action.promise]); });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(api.listAdminOrders).toHaveBeenCalledOnce();
    expect(adminDataCache.consume("orders")).toBeUndefined();
  });

  it("keeps current errors visible and permits a fresh retry", async () => {
    const api = orderAPI();
    api.getAdminOrder.mockRejectedValueOnce(new Error("current request failed")).mockResolvedValueOnce(first);
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: `查看订单：${first.trade_no}` }));
    expect(await screen.findByRole("alert")).toHaveTextContent("current request failed");
    await user.click(screen.getByRole("button", { name: `查看订单：${first.trade_no}` }));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(within(await screen.findByRole("dialog", { name: "订单详情" })).getByText(first.trade_no)).toBeVisible();
  });
});
