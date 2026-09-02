import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { DistributorOrder, DistributorQR, PlanOffer } from "../../lib/api";
import { DistributorOrdersPage } from "./DistributorOrdersPage";
import { DistributorPlansPage } from "./DistributorPlansPage";
import { DistributorPortal } from "./DistributorPortal";

const plan: PlanOffer = {
  id: 7, group_id: 2, transfer_enable: 100, name: "分销标准套餐", speed_limit: null, content: "",
  reset_traffic_method: null, capacity_limit: null, prices: { monthly: 10_000, yearly: 100_000 }, device_limit: null,
  tags: [], show: true, sell: true, renew: true, revision: 1, created_at: "2026-08-26T00:00:00Z",
  updated_at: "2026-08-26T00:00:00Z", sort: 1, capacity_remaining: null, can_purchase: true, can_renew: true
};
const original = distributorOrder();
const qr: DistributorQR = { trade_no: original.order.trade_no, customer_name: null, qr_code: "data:image/svg+xml,%3Csvg%3E%3C/svg%3E", hwid_enabled: true, hwid_devices: [] };

describe("Distributor portal", () => {
  it("exposes only the fixed distributor allowlist", async () => {
    const api = portalAPI();
    const user = userEvent.setup();
    render(<DistributorPortal api={api} session={{ id: 9, email: "seller@example.test", is_admin: false, is_distributor: true, distributor_name: "星河分销" }} siteName="Tenant Board" siteLogo={null} onSignedOut={vi.fn()} />);

    expect(await screen.findByRole("heading", { name: "分销订阅中心" })).toBeVisible();
    for (const label of ["购买订阅", "我的订单", "我的邀请", "使用文档", "客户端下载"]) expect(screen.getByRole("button", { name: label })).toBeVisible();
    for (const forbidden of ["我的订阅", "我的工单", "礼品卡", "公告"]) expect(screen.queryByRole("button", { name: forbidden })).not.toBeInTheDocument();
    expect(screen.getByText("星河分销")).toBeVisible();
    expect(document.documentElement).toHaveAttribute("data-distributor-theme", "light");
    await user.click(screen.getByRole("button", { name: "深色模式" }));
    expect(document.documentElement).not.toHaveAttribute("data-distributor-theme");
    expect(localStorage.getItem("xboard_distributor_dark")).toBe("1");
    await user.click(screen.getByRole("button", { name: "浅色模式" }));
    expect(document.documentElement).toHaveAttribute("data-distributor-theme", "light");
    expect(localStorage.getItem("xboard_distributor_dark")).toBe("0");

    await user.click(screen.getByRole("button", { name: "语言" }));
    expect(await screen.findByRole("heading", { name: "Distributor Center" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Confirmed — place order" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "My Orders" }));
    expect(await screen.findByRole("heading", { name: "My Orders" })).toBeVisible();
    expect(screen.getByPlaceholderText("Search by order or customer name")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Documentation" }));
    expect(await screen.findByRole("heading", { name: "Documentation" })).toBeVisible();
    expect(api.listKnowledge).toHaveBeenLastCalledWith("en-US", "");
    await user.click(screen.getByRole("button", { name: "Client downloads" }));
    expect(await screen.findByRole("heading", { name: "Client downloads" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "My Invitations" }));
    expect(await screen.findByRole("heading", { name: "My Invitations" })).toBeVisible();
    expect(screen.getAllByText("Valid commission", { exact: true })[0]).toBeVisible();
    expect(screen.getByRole("button", { name: "Transfer commission" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Withdraw commission" })).not.toBeInTheDocument();
  });

  it("creates an independent order and immediately presents a repeatable QR delivery", async () => {
    const api = { listPlanOffers: vi.fn().mockResolvedValue([plan]), createDistributorOrder: vi.fn().mockResolvedValue(original), getDistributorOrderQR: vi.fn().mockResolvedValue(qr) };
    const user = userEvent.setup();
    render(<DistributorPlansPage api={api} />);

    await user.click(await screen.findByRole("button", { name: "已确认，直接下单" }));
    const dialog = await screen.findByRole("dialog", { name: "订阅交付" });
    expect(within(dialog).getByRole("img", { name: "客户订阅二维码" })).toHaveAttribute("src", qr.qr_code);
    expect(api.createDistributorOrder).toHaveBeenCalledWith(7, "monthly");
    expect(api.getDistributorOrderQR).toHaveBeenCalledWith(original.order.trade_no);

    await user.click(within(dialog).getByRole("button", { name: "再次购买该套餐" }));
    await waitFor(() => expect(api.createDistributorOrder).toHaveBeenCalledTimes(2));
    expect(api.createDistributorOrder).toHaveBeenLastCalledWith(7, "monthly");
  });

  it("searches, filters, expands entitlement, opens QR, and retries renewal with one idempotency key", async () => {
    const renewed = { ...original, order: { ...original.order, id: 12, trade_no: "202608260002", type: 2 as const } };
    const api = {
      listDistributorOrders: vi.fn().mockResolvedValue({ items: [original], total: 1, page: 1, page_size: 20 }),
      getDistributorOrderQR: vi.fn().mockResolvedValue(qr),
      renewDistributorOrder: vi.fn().mockRejectedValueOnce(new Error("临时失败")).mockResolvedValueOnce(renewed),
      exportDistributorOrders: vi.fn().mockResolvedValue(new Blob(["xlsx"])),
      listPlanOffers: vi.fn().mockResolvedValue([plan])
    };
    const user = userEvent.setup();
    render(<DistributorOrdersPage api={api} />);
    expect(await screen.findByText(original.order.trade_no)).toBeVisible();

    await user.type(screen.getByPlaceholderText("订单号或客户名称"), "202608");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listDistributorOrders).toHaveBeenLastCalledWith(expect.objectContaining({ search: "202608" })));
    await user.selectOptions(screen.getByLabelText("结算状态"), "0");
    await waitFor(() => expect(api.listDistributorOrders).toHaveBeenLastCalledWith(expect.objectContaining({ settlement_status: 0 })));

    await user.click(screen.getByRole("button", { name: "查看权益" }));
    expect(screen.getByText("当前订阅权益")).toBeVisible();
    expect(screen.getByText("尚未绑定设备")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "订阅二维码" }));
    expect(await screen.findByRole("dialog", { name: "订阅二维码" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "关闭订阅二维码" }));

    await user.click(screen.getByRole("button", { name: "续费" }));
    const renewal = await screen.findByRole("dialog", { name: "续费现有订阅" });
    await waitFor(() => expect(within(renewal).getByRole("button", { name: "确认续费" })).toBeEnabled());
    await user.click(within(renewal).getByRole("button", { name: "确认续费" }));
    expect(await within(renewal).findByRole("alert")).toHaveTextContent("临时失败");
    await user.click(within(renewal).getByRole("button", { name: "确认续费" }));
    await waitFor(() => expect(api.renewDistributorOrder).toHaveBeenCalledTimes(2));
    expect(api.renewDistributorOrder.mock.calls[0]?.[2]).toBe(api.renewDistributorOrder.mock.calls[1]?.[2]);
  });
});

function distributorOrder(): DistributorOrder {
  return {
    order: {
      id: 11, user_id: 9, plan_id: 7, payment_id: null, period: "monthly", trade_no: "202608260001",
      original_amount: 10_000, total_amount: 10_000, handling_amount: null, balance_amount: 0, surplus_credit: 0,
      surplus_amount: 0, type: 1, status: 3, surplus_order_ids: [], coupon_id: null, commission_status: null,
      invite_user_id: null, actual_commission_balance: null, commission_rate: null, commission_auto_check: null,
      commission_balance: 0, discount_amount: 0, paid_at: null, callback_no: "", entitlement_expired_at_before: null,
      entitlement_expired_at_after: "2026-09-26T00:00:00Z", created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z"
    },
    plan_name: "分销标准套餐",
    subscription: { id: 3, original_order_id: 11, trade_no: "202608260001", distributor_user_id: 9, customer_name: null, remark: null, delivery_status: 0, settlement_status: 0, config_issued_at: null, connected_at: null, connected_node_id: null, connected_node_name: null, claimed_at: null, closed_at: null, hwid_enabled: true, hwid_limit: 1, revision: 1, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" },
    settlement_status: 0,
    subscription_entitlement: { plan_id: 7, plan_name: "分销标准套餐", transfer_enable: 107374182400, used_traffic: 0, remaining_traffic: 107374182400, expired_at: "2026-09-26T00:00:00Z", speed_limit: 0, device_limit: 0 },
    bound_devices: [], is_subscription_origin: true, can_view_subscription_qr: true, can_renew: true
  };
}

function portalAPI() {
  return {
    listPlanOffers: vi.fn().mockResolvedValue([plan]), createDistributorOrder: vi.fn(),
    listDistributorOrders: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
    getDistributorOrderQR: vi.fn(), renewDistributorOrder: vi.fn(), exportDistributorOrders: vi.fn(),
    getInvitations: vi.fn().mockResolvedValue({ codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0, commission_rate: 10, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0, withdraw_enabled: false, withdraw_limit: 100, withdraw_methods: [] }), createInvitation: vi.fn(),
    listCommissionLogs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }), transferCommission: vi.fn(), requestCommissionWithdrawal: vi.fn(),
    listKnowledge: vi.fn().mockResolvedValue([]), getKnowledge: vi.fn(), listClientCatalog: vi.fn().mockResolvedValue([]), clientCatalogQR: vi.fn(), logout: vi.fn()
  };
}
