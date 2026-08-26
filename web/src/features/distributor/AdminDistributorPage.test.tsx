import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdminDistributorOrderDetail, AdminUser, DistributorOrder } from "../../lib/api";
import { AdminDistributorPage } from "./AdminDistributorPage";

const distributor: AdminUser = {
  id: 9, email: "seller@example.test", is_admin: false, is_staff: false, is_distributor: true, distributor_name: "星河分销", banned: false,
  group_id: null, transfer_enable: 0, traffic_upload: 0, traffic_download: 0, expired_at: null, speed_limit: 0, device_limit: 0,
  online_count: 0, last_online_at: null, last_login_at: null, revision: 1, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z"
};
const device = { id: 6, hwid: "ABCDEF123456", device_os: "Android", os_version: "15", device_model: "Pixel 7", user_agent: null, ip_address: "192.0.2.1", first_seen_at: "2026-08-26T00:00:00Z", last_seen_at: "2026-08-26T01:00:00Z" };
const order = makeOrder();
const detail: AdminDistributorOrderDetail = { order, hwid: { enabled: true, limit: 1, registered_count: 1 }, subscribe_url: "https://board.example.test/api/v1/client/subscribe?token=administrator-only" };

describe("AdminDistributorPage", () => {
  it("covers filtering, detail mutations, HWID devices, and transactional settlement", async () => {
    const api = {
      listAdminDistributorOptions: vi.fn().mockResolvedValue([distributor]),
      listAdminDistributorOrders: vi.fn().mockResolvedValue({ items: [order], total: 1, page: 1, page_size: 20 }),
      getAdminDistributorOrder: vi.fn().mockResolvedValue(detail),
      updateAdminDistributorRemark: vi.fn().mockResolvedValue({ order_id: 11, remark: "需要跟进" }),
      updateAdminDistributorEntitlement: vi.fn().mockResolvedValue({ ...order.subscription_entitlement, transfer_enable: 214748364800 }),
      updateAdminDistributorHWID: vi.fn().mockResolvedValue({ enabled: false, limit: 2, registered_count: 1 }),
      listAdminDistributorHWIDDevices: vi.fn().mockResolvedValue([device]),
      deleteAdminDistributorHWIDDevice: vi.fn().mockResolvedValue(undefined),
      previewAdminDistributorSettlement: vi.fn().mockResolvedValue({ count: 1, total_amount: 10_000, settled_at: null }),
      settleAdminDistributorOrders: vi.fn().mockResolvedValue({ count: 1, total_amount: 10_000, settled_at: "2026-08-26T02:00:00Z" }),
      exportAdminDistributorOrders: vi.fn()
    };
    const user = userEvent.setup();
    render(<AdminDistributorPage api={api} />);
    expect(await screen.findByText(order.order.trade_no)).toBeVisible();

    await user.selectOptions(screen.getByLabelText("分销商"), "9");
    await waitFor(() => expect(api.listAdminDistributorOrders).toHaveBeenLastCalledWith(expect.objectContaining({ distributor_user_id: 9 })));
    await user.type(screen.getByPlaceholderText("订单、客户名或订阅凭据"), "administrator-only");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listAdminDistributorOrders).toHaveBeenLastCalledWith(expect.objectContaining({ search: "administrator-only" })));

    await user.click(screen.getByRole("button", { name: `分销订单详情：${order.order.trade_no}` }));
    const modal = await screen.findByRole("dialog", { name: "分销订单详情" });
    expect(within(modal).getByText(detail.subscribe_url)).toBeVisible();
    expect(within(modal).getByText("Pixel 7 · Android · 15")).toBeVisible();

    await user.type(within(modal).getByLabelText("内部备注"), "需要跟进");
    await user.click(within(modal).getByRole("button", { name: "保存备注" }));
    await waitFor(() => expect(api.updateAdminDistributorRemark).toHaveBeenCalledWith(11, "需要跟进"));
    await user.clear(within(modal).getByLabelText("总流量（字节）"));
    await user.type(within(modal).getByLabelText("总流量（字节）"), "214748364800");
    await user.click(within(modal).getByRole("button", { name: "保存权益" }));
    await waitFor(() => expect(api.updateAdminDistributorEntitlement).toHaveBeenCalledWith(11, expect.objectContaining({ transfer_enable: 214748364800 })));
    await user.click(within(modal).getByLabelText("启用设备绑定"));
    await user.clear(within(modal).getByLabelText("HWID 上限"));
    await user.type(within(modal).getByLabelText("HWID 上限"), "2");
    await user.click(within(modal).getByRole("button", { name: "保存 HWID 设置" }));
    await waitFor(() => expect(api.updateAdminDistributorHWID).toHaveBeenCalledWith(11, false, 2));
    await user.click(within(modal).getByRole("button", { name: "删除" }));
    await waitFor(() => expect(api.deleteAdminDistributorHWIDDevice).toHaveBeenCalledWith(11, 6));
    await user.click(within(modal).getByRole("button", { name: "关闭分销订单详情" }));

    await user.click(screen.getByRole("button", { name: "结算所选分销商" }));
    const settlement = await screen.findByRole("dialog", { name: "分销订单结算" });
    expect(await within(settlement).findByText("¥100.00")).toBeVisible();
    await user.click(within(settlement).getByRole("button", { name: "确认结算" }));
    await waitFor(() => expect(api.settleAdminDistributorOrders).toHaveBeenCalledWith(9));
  });
});

function makeOrder(): DistributorOrder {
  return {
    order: { id: 11, user_id: 9, plan_id: 7, payment_id: null, period: "monthly", trade_no: "202608260001", original_amount: 10_000, total_amount: 10_000, handling_amount: null, balance_amount: 0, surplus_credit: 0, surplus_amount: 0, type: 1, status: 3, surplus_order_ids: [], coupon_id: null, commission_status: null, invite_user_id: null, actual_commission_balance: null, commission_rate: null, commission_auto_check: null, commission_balance: 0, discount_amount: 0, paid_at: null, callback_no: "", entitlement_expired_at_before: null, entitlement_expired_at_after: "2026-09-26T00:00:00Z", created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" },
    plan_name: "分销标准套餐", distributor_email: distributor.email, distributor_name: "星河分销",
    subscription: { id: 3, original_order_id: 11, trade_no: "202608260001", distributor_user_id: 9, customer_name: null, remark: null, delivery_status: 0, settlement_status: 0, config_issued_at: null, connected_at: null, connected_node_id: null, connected_node_name: null, claimed_at: null, closed_at: null, hwid_enabled: true, hwid_limit: 1, revision: 1, created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z" },
    settlement_status: 0, subscription_entitlement: { plan_id: 7, plan_name: "分销标准套餐", transfer_enable: 107374182400, used_traffic: 0, remaining_traffic: 107374182400, expired_at: "2026-09-26T00:00:00Z", speed_limit: 0, device_limit: 0 }, bound_devices: [device.hwid], is_subscription_origin: true, can_view_subscription_qr: true, can_renew: true
  };
}
