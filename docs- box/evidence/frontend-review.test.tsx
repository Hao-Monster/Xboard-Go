// Diagnostic assertions intentionally document CURRENT defects, not acceptance
// criteria for the desired fix. PASS means the reported symptom was reproduced.
import { createElement as h, useState } from "../../web/node_modules/react/index.js";
import { act, fireEvent, render, screen, waitFor, within } from "../../web/node_modules/@testing-library/react/dist/index.js";
import { afterEach, expect, it, vi } from "../../web/node_modules/vitest/dist/index.js";
import { Modal } from "../../web/src/components/Overlay";
import { NodeFilter } from "../../web/src/features/nodes/NodeFilter";
import { UsersPage } from "../../web/src/features/users/UsersPage";
import { GiftCardManagementPage } from "../../web/src/features/giftcards/GiftCardManagementPage";
import { UserOrdersPage } from "../../web/src/features/orders/UserOrdersPage";
import { adminDataCache } from "../../web/src/lib/dataPrefetchCache";
import type { GiftCardTemplate, Order, Plan, ServerGroup } from "../../web/src/lib/api";

afterEach(() => adminDataCache.invalidatePrefix(""));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

it("R24 reproduces parent-first Escape with an unsuspended nested Modal", () => {
  const parentClose = vi.fn(); const childClose = vi.fn();
  function Harness() {
    const [child, setChild] = useState(false);
    return h(Modal, { title: "parent", onClose: parentClose, children: [
      h("button", { key: "open", onClick: () => setChild(true) }, "open child"),
      child ? h(Modal, { key: "child", title: "child", onClose: childClose, children: h("input", { "aria-label": "draft" }) }) : null,
    ] });
  }
  render(h(Harness));
  fireEvent.click(screen.getByRole("button", { name: "open child" }));
  fireEvent.keyDown(screen.getByLabelText("draft"), { key: "Escape" });
  expect(parentClose).toHaveBeenCalledTimes(1);
  expect(childClose).not.toHaveBeenCalled();
});

it("R24 control: explicitly suspending the parent routes Escape to the child", () => {
  const parentClose = vi.fn(); const childClose = vi.fn();
  function Harness() {
    const [child, setChild] = useState(false);
    return h(Modal, { title: "parent", suspended: child, onClose: parentClose, children: [
      h("button", { key: "open", onClick: () => setChild(true) }, "open child"),
      child ? h(Modal, { key: "child", title: "child", onClose: childClose, children: h("input", { "aria-label": "draft" }) }) : null,
    ] });
  }
  render(h(Harness));
  fireEvent.click(screen.getByRole("button", { name: "open child" }));
  fireEvent.keyDown(screen.getByLabelText("draft"), { key: "Escape" });
  expect(parentClose).not.toHaveBeenCalled();
  expect(childClose).toHaveBeenCalledTimes(1);
});

it("R24 reproduces Modal capture beating NodeFilter Escape handling", () => {
  const parentClose = vi.fn();
  render(h(Modal, { title: "editor", onClose: parentClose, children: h(NodeFilter, {
    label: "protocol", options: [{ value: "h3", label: "h3" }], value: [], onChange: vi.fn(),
  }) }));
  fireEvent.click(screen.getByRole("button", { name: "protocol" }));
  fireEvent.keyDown(screen.getByLabelText("搜索protocol"), { key: "Escape" });
  expect(parentClose).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("dialog", { name: "protocol筛选" })).toBeInTheDocument();
});

it("R25.1 reproduces successful metadata responses discarded after quick-search input", async () => {
  const groups = deferred<ServerGroup[]>(); const plans = deferred<Plan[]>();
  const api = {
    listAdminUsers: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getAdminUser: vi.fn(), createAdminUser: vi.fn(), generateAdminUsers: vi.fn(), updateAdminUser: vi.fn(), resetAdminUserPassword: vi.fn(),
    deactivateAdminUser: vi.fn(), restoreAdminUser: vi.fn(), anonymizeAdminUser: vi.fn(),
    getAdminUserSubscriptionURL: vi.fn(), resetAdminUserSubscriptionSecurity: vi.fn(), listAdminUserOrders: vi.fn(), assignAdminUserOrder: vi.fn(), listAdminUserInvitations: vi.fn(),
    listAdminUserTraffic: vi.fn(), listAdminUserTrafficResets: vi.fn(), resetAdminUserTraffic: vi.fn(),
    createAdminUserBulkMail: vi.fn(), createAdminUserBulkCSV: vi.fn(), banAdminUsers: vi.fn(),
    listAdminUserBulkJobs: vi.fn(), getAdminUserBulkJob: vi.fn(), cancelAdminUserBulkJob: vi.fn(), downloadAdminUserBulkCSV: vi.fn(),
    listServerGroups: vi.fn().mockReturnValue(groups.promise), listPlans: vi.fn().mockReturnValue(plans.promise),
  };
  render(h(UsersPage, { api, currentUserID: 1 }));
  fireEvent.change(screen.getByRole("searchbox", { name: "邮箱前缀" }), { target: { value: "qa" } });
  await waitFor(() => expect(api.listAdminUsers).toHaveBeenCalledTimes(2));
  await act(async () => {
    groups.resolve([{ id: 71, name: "QA loaded group", users_count: 0, server_count: 0, created_at: "2026-10-14T00:00:00Z", updated_at: "2026-10-14T00:00:00Z" }]);
    plans.resolve([]);
    await Promise.all([groups.promise, plans.promise]);
  });
  fireEvent.click(screen.getByRole("button", { name: "高级筛选" }));
  fireEvent.click(screen.getByRole("button", { name: "添加条件" }));
  fireEvent.change(screen.getByLabelText("筛选字段 1"), { target: { value: "group_id" } });
  expect(screen.queryByRole("option", { name: "QA loaded group" })).not.toBeInTheDocument();
  expect(screen.queryByText(/权限组加载失败/)).not.toBeInTheDocument();
  expect(api.listServerGroups).toHaveBeenCalledTimes(1);
});

it("R25.2 reproduces deleting page two's final template hiding pagination", async () => {
  const template: GiftCardTemplate = { id: 21, name: "QA last template", description: "", type: 1, status: true, conditions: {}, rewards: {}, limits: {}, special_config: {}, icon: "", background_image: "", theme: "", sort: 0, admin_id: 1, revision: 1, created_at: "2026-10-14T00:00:00Z", updated_at: "2026-10-14T00:00:00Z" };
  let deleted = false;
  const api = {
    listGiftCardTemplates: vi.fn().mockImplementation((page = 1) => Promise.resolve({ items: deleted && page === 2 ? [] : [template], total: deleted ? 20 : 21, page, page_size: 20 })),
    listPlans: vi.fn().mockResolvedValue([]), createGiftCardTemplate: vi.fn(), updateGiftCardTemplate: vi.fn(),
    deleteGiftCardTemplate: vi.fn().mockImplementation(() => { deleted = true; return Promise.resolve(); }),
    generateGiftCardCodes: vi.fn(), generateGiftCardCodesCSV: vi.fn(), listGiftCardCodes: vi.fn(), updateGiftCardCode: vi.fn(), exportGiftCardCodes: vi.fn(), toggleGiftCardCode: vi.fn(), deleteGiftCardCode: vi.fn(), listGiftCardUsages: vi.fn(), getGiftCardStatistics: vi.fn(),
  };
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(h(GiftCardManagementPage, { api }));
  await screen.findByText("QA last template");
  fireEvent.click(screen.getByRole("button", { name: "下一页" }));
  await waitFor(() => expect(screen.getByText(/2 \/ 2 页/)).toBeInTheDocument());
  fireEvent.click(screen.getByRole("button", { name: "删除" }));
  await screen.findByText("暂无礼品卡模板");
  expect(screen.queryByRole("button", { name: "上一页" })).not.toBeInTheDocument();
  expect(api.listGiftCardTemplates.mock.calls.at(-1)?.[0]).toBe(2);
  // Recovery boundary: changing a filter really does reset the page.
  fireEvent.change(screen.getByLabelText("搜索礼品卡"), { target: { value: "QA" } });
  await screen.findByText("QA last template");
  expect(api.listGiftCardTemplates.mock.calls.at(-1)?.[0]).toBe(1);
});

const makeOrder = (id: number): Order => ({ id, user_id: 1, plan_id: 1, payment_id: null, handling_amount: null, trade_no: `QA-ORDER-${id}`, period: "monthly", type: 1, status: 0, original_amount: 0, total_amount: 0, balance_amount: 0, discount_amount: 0, surplus_amount: 0, surplus_credit: 0, surplus_order_ids: [], coupon_id: null, commission_status: null, invite_user_id: null, actual_commission_balance: null, commission_rate: null, commission_auto_check: null, commission_balance: 0, callback_no: null, entitlement_expired_at_before: null, entitlement_expired_at_after: null, paid_at: null, created_at: "2026-10-14T00:00:00Z", updated_at: "2026-10-14T00:00:00Z" });

it("R25.3a reproduces a slow older order-detail response replacing the latest selection", async () => {
  const a = makeOrder(1); const b = makeOrder(2); const old = deferred<Order>(); const latest = deferred<Order>();
  const api = { listOrders: vi.fn().mockResolvedValue([a, b]), getOrder: vi.fn().mockImplementation((trade: string) => trade === a.trade_no ? old.promise : latest.promise), listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn(), cancelOrder: vi.fn() };
  render(h(UserOrdersPage, { api }));
  fireEvent.click(await screen.findByRole("button", { name: `查看订单：${a.trade_no}` }));
  fireEvent.click(screen.getByRole("button", { name: `查看订单：${b.trade_no}` }));
  await act(async () => { latest.resolve(b); await latest.promise; });
  expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText(b.trade_no)).toBeVisible();
  await act(async () => { old.resolve(a); await old.promise; });
  expect(within(screen.getByRole("dialog", { name: "订单详情" })).getByText(a.trade_no)).toBeVisible();
});

it("R25.3b reproduces a cancelled order remaining in the pending-only table", async () => {
  const a = makeOrder(1);
  const api = { listOrders: vi.fn().mockResolvedValue([a]), getOrder: vi.fn().mockResolvedValue(a), listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn(), cancelOrder: vi.fn().mockResolvedValue({ ...a, status: 2 }) };
  render(h(UserOrdersPage, { api }));
  await screen.findByRole("button", { name: `查看订单：${a.trade_no}` });
  fireEvent.click(within(screen.getByRole("group", { name: "订单状态筛选" })).getByRole("button", { name: "待支付" }));
  await waitFor(() => expect(api.listOrders).toHaveBeenLastCalledWith(0, 100));
  fireEvent.click(screen.getByRole("button", { name: `查看订单：${a.trade_no}` }));
  const dialog = await screen.findByRole("dialog", { name: "订单详情" });
  fireEvent.click(within(dialog).getByRole("button", { name: "关闭订单" }));
  await waitFor(() => expect(within(screen.getByRole("table")).getByText("已取消")).toBeVisible());
  expect(screen.getByRole("button", { name: `查看订单：${a.trade_no}` })).toBeInTheDocument();
});
