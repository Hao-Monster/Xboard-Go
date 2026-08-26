import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdminOrder, Order, Plan } from "../../lib/api";
import { OrderManagementPage } from "./OrderManagementPage";
import { UserOrdersPage } from "./UserOrdersPage";

const plan = {
  id: 7, group_id: 1, transfer_enable: 100, name: "Premium", speed_limit: 200, show: true, sort: 0,
  renew: true, content: "", reset_traffic_method: 1, capacity_limit: null, prices: { monthly: 0 }, sell: true,
  device_limit: 3, tags: [], revision: 1, users_count: 0, active_users_count: 0, capacity_users_count: 0,
  created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z"
} satisfies Plan;

const pending = {
  id: 11, user_id: 42, plan_id: plan.id, payment_id: null, period: "monthly", trade_no: "2026082612345600000000001",
  original_amount: 0, total_amount: 0, handling_amount: null, balance_amount: 0, surplus_credit: 0, surplus_amount: 0,
  type: 1, status: 0, surplus_order_ids: [], coupon_id: null, commission_status: 0, invite_user_id: null,
  actual_commission_balance: null, commission_rate: null, commission_auto_check: null, commission_balance: 0,
  discount_amount: 0, paid_at: null, callback_no: null, entitlement_expired_at_before: null,
  entitlement_expired_at_after: null, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z", plan
} satisfies Order;

describe("UserOrdersPage", () => {
  it("shows the pending order, completes a free checkout once, and removes invalid actions", async () => {
    const completed = { ...pending, status: 3 as const, paid_at: "2026-08-26T00:01:00Z", callback_no: pending.trade_no };
    const api = {
      listOrders: vi.fn().mockResolvedValue([pending]), getOrder: vi.fn().mockResolvedValue(pending),
      listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn().mockResolvedValue(completed), cancelOrder: vi.fn()
    };
    const user = userEvent.setup();
    render(<UserOrdersPage api={api} />);

    expect(await screen.findByText(pending.trade_no)).toBeVisible();
    await user.click(screen.getByRole("button", { name: `查看订单：${pending.trade_no}` }));
    const dialog = await screen.findByRole("dialog", { name: "订单详情" });
    expect(within(dialog).getByText("该订单无需在线支付，可直接开通。")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "立即开通" }));
    await waitFor(() => expect(api.checkoutOrder).toHaveBeenCalledWith(pending.trade_no));
    expect(within(dialog).getByText("已完成")).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: "立即开通" })).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "关闭订单" })).not.toBeInTheDocument();
  });

  it("cancels a paid pending order and removes every pending-only action", async () => {
    const payable = { ...pending, original_amount: 1_000, total_amount: 1_000 };
    const cancelled = { ...payable, status: 2 as const };
    const api = {
      listOrders: vi.fn().mockResolvedValue([payable]), getOrder: vi.fn().mockResolvedValue(payable),
      listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn(), cancelOrder: vi.fn().mockResolvedValue(cancelled)
    };
    const user = userEvent.setup();
    render(<UserOrdersPage api={api} />);

    await user.click(await screen.findByRole("button", { name: `查看订单：${payable.trade_no}` }));
    const dialog = await screen.findByRole("dialog", { name: "订单详情" });
    expect(await within(dialog).findByText("当前没有可用支付方式。你可以关闭订单，待支付方式配置后重新下单。")).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: "立即开通" })).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "关闭订单" }));
    await waitFor(() => expect(api.cancelOrder).toHaveBeenCalledWith(payable.trade_no));
    expect(within(dialog).getByText("已取消")).toBeVisible();
    expect(within(dialog).queryByRole("button", { name: "关闭订单" })).not.toBeInTheDocument();
  });

  it("selects an enabled payment method, shows the exact fee, and exposes the created checkout safely", async () => {
    const payable = { ...pending, original_amount: 1_000, total_amount: 1_000 };
		const completed = { ...payable, status: 3 as const, payment_id: 9, handling_amount: 148, paid_at: "2026-08-26T00:01:00Z" };
    const method = { id: 9, name: "易支付", payment: "EPay" as const, handling_fee_fixed: 123, handling_fee_basis_points: 250 };
    const checkout = { type: 1 as const, data: "https://checkout.example.test/pay/one", payment_id: method.id, handling_amount: 148, total_amount: 1_148 };
    const api = {
			listOrders: vi.fn().mockResolvedValue([payable]), getOrder: vi.fn().mockResolvedValueOnce(payable).mockResolvedValue(completed),
      listPaymentMethods: vi.fn().mockResolvedValue([method]), checkoutOrder: vi.fn().mockResolvedValue(checkout), cancelOrder: vi.fn()
    };
    const user = userEvent.setup();
    render(<UserOrdersPage api={api} />);

    await user.click(await screen.findByRole("button", { name: `查看订单：${payable.trade_no}` }));
    const dialog = await screen.findByRole("dialog", { name: "订单详情" });
    expect(await within(dialog).findByText("手续费 ¥1.48")).toBeVisible();
    expect(within(dialog).getByText("¥11.48")).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "立即支付" }));
    await waitFor(() => expect(api.checkoutOrder).toHaveBeenCalledWith(payable.trade_no, method.id));
    expect(within(dialog).getByRole("link", { name: "前往支付" })).toHaveAttribute("href", checkout.data);
    expect(within(dialog).getByText("应付 ¥11.48")).toBeVisible();
		await waitFor(() => expect(api.getOrder).toHaveBeenCalledTimes(2), { timeout: 3_500 });
		expect(within(dialog).getByText("已完成")).toBeVisible();
		expect(within(dialog).queryByRole("link", { name: "前往支付" })).not.toBeInTheDocument();
  });
});

describe("OrderManagementPage", () => {
  it("filters, assigns integer-cent orders, and manually completes only pending orders", async () => {
    const adminOrder = { ...pending, user_email: "buyer@example.test", plan_name: plan.name } satisfies AdminOrder;
    const completed = { ...pending, status: 3 as const, paid_at: "2026-08-26T00:01:00Z", callback_no: "manual_operation" };
    const api = {
      listAdminOrders: vi.fn().mockResolvedValue({ items: [adminOrder], total: 1, page: 1, page_size: 20 }),
      getAdminOrder: vi.fn().mockResolvedValue(adminOrder), assignOrder: vi.fn().mockResolvedValue(pending),
      paidAdminOrder: vi.fn().mockResolvedValue(completed), cancelAdminOrder: vi.fn(), listPlans: vi.fn().mockResolvedValue([plan])
    };
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);

    expect(await screen.findByText("buyer@example.test")).toBeVisible();
    await user.type(screen.getByRole("searchbox", { name: "搜索订单" }), "buyer");
    await user.selectOptions(screen.getByLabelText("订单状态"), "0");
    await user.click(screen.getByRole("button", { name: "查询订单" }));
    await waitFor(() => expect(api.listAdminOrders).toHaveBeenLastCalledWith(expect.objectContaining({ query: "buyer", status: 0 })));

    await user.click(screen.getByRole("button", { name: "添加订单" }));
    const addDialog = screen.getByRole("dialog", { name: "添加订单" });
    await user.type(within(addDialog).getByLabelText("用户邮箱"), "new@example.test");
    await user.clear(within(addDialog).getByLabelText("支付金额（CNY）"));
    await user.type(within(addDialog).getByLabelText("支付金额（CNY）"), "12.34");
    await user.click(within(addDialog).getByRole("button", { name: "创建订单" }));
    await waitFor(() => expect(api.assignOrder).toHaveBeenCalledWith({ email: "new@example.test", plan_id: plan.id, period: "monthly", total_amount: 1234 }));

    await user.click(screen.getByRole("button", { name: `查看订单：${pending.trade_no}` }));
    const detail = await screen.findByRole("dialog", { name: "订单详情" });
    await user.click(within(detail).getByRole("button", { name: "标记已支付并开通" }));
    await waitFor(() => expect(api.paidAdminOrder).toHaveBeenCalledWith(pending.trade_no));
    expect(within(detail).getByText("已完成")).toBeVisible();
    expect(within(detail).queryByRole("button", { name: "标记已支付并开通" })).not.toBeInTheDocument();
  });

  it("rejects non-cent administrator amounts before sending an order", async () => {
    const api = {
      listAdminOrders: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      getAdminOrder: vi.fn(), assignOrder: vi.fn(), paidAdminOrder: vi.fn(), cancelAdminOrder: vi.fn(),
      listPlans: vi.fn().mockResolvedValue([plan])
    };
    const user = userEvent.setup();
    render(<OrderManagementPage api={api} />);

    await screen.findByText("没有符合条件的订单。");
    await user.click(screen.getByRole("button", { name: "添加订单" }));
    const dialog = screen.getByRole("dialog", { name: "添加订单" });
    await user.type(within(dialog).getByLabelText("用户邮箱"), "buyer@example.test");
    await user.clear(within(dialog).getByLabelText("支付金额（CNY）"));
    await user.type(within(dialog).getByLabelText("支付金额（CNY）"), "12.345");
    await user.click(within(dialog).getByRole("button", { name: "创建订单" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent("支付金额格式无效");
    expect(api.assignOrder).not.toHaveBeenCalled();
  });
});
