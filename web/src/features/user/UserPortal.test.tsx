import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { UserSession } from "../../lib/api";
import { UserPortal } from "./UserPortal";

const session: UserSession = { id: 12, email: "user@example.test", is_admin: false };

describe("UserPortal", () => {
  it("navigates between notices and clients and signs out through one shared shell", async () => {
    const api = {
      listVisibleNotices: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 }),
      listClientCatalog: vi.fn().mockResolvedValue([]),
      clientCatalogQR: vi.fn(),
      listKnowledge: vi.fn().mockResolvedValue([]),
      getKnowledge: vi.fn(),
      listTickets: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      createTicket: vi.fn(), getTicket: vi.fn(), replyTicket: vi.fn(), closeTicket: vi.fn(),
      getInvitations: vi.fn().mockResolvedValue({ codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0, commission_rate: 10, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0 }), createInvitation: vi.fn(),
      listCommissionLogs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }), transferCommission: vi.fn(),
      getCommissionWithdrawalPolicy: vi.fn().mockResolvedValue(emptyWithdrawalPolicy), listCommissionWithdrawals: vi.fn().mockResolvedValue(emptyWithdrawalPage), createCommissionWithdrawal: vi.fn(),
      listPlanOffers: vi.fn().mockResolvedValue([]),
      checkCoupon: vi.fn(), createOrder: vi.fn(), listOrders: vi.fn().mockResolvedValue([]), getOrder: vi.fn(), listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn(), cancelOrder: vi.fn(),
      getSubscription: vi.fn().mockResolvedValue({ plan_id: null, token: "1".repeat(32), expired_at: null, u: 0, d: 0, transfer_enable: 0, email: session.email, uuid: "11111111-1111-4111-8111-111111111111", device_limit: 0, speed_limit: 0, next_reset_at: null, plan: null, subscribe_url: "https://panel.example.test/s/token", reset_day: null, subscription_valid: false }),
      getSubscriptionQR: vi.fn(), resetSubscriptionSecurity: vi.fn(),
      checkGiftCard: vi.fn(), redeemGiftCard: vi.fn(), listMyGiftCardUsages: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 15 }),
      logout: vi.fn().mockResolvedValue(undefined)
    };
    const onSignedOut = vi.fn();
    const user = userEvent.setup();
    render(<UserPortal api={api} session={session} siteName="Tenant Board" siteLogo="https://images.example.test/tenant.svg" couponEnabled onSignedOut={onSignedOut} />);

    expect(screen.getByText("Tenant Board", { exact: true })).toBeVisible();
    expect(screen.getByRole("img", { name: "Tenant Board LOGO" })).toHaveAttribute("src", "https://images.example.test/tenant.svg");

    expect(await screen.findByRole("heading", { name: "我的订阅" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "公告" }));
    expect(await screen.findByRole("heading", { name: "公告" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "客户端下载" }));
    expect(await screen.findByRole("heading", { name: "客户端下载" })).toBeVisible();
    expect(api.listClientCatalog).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "知识库" }));
    expect(await screen.findByRole("heading", { name: "知识库" })).toBeVisible();
    expect(api.listKnowledge).toHaveBeenCalledWith("zh-CN", "");
    await user.click(screen.getByRole("button", { name: "我的工单" }));
    expect(await screen.findByRole("heading", { name: "我的工单" })).toBeVisible();
    expect(api.listTickets).toHaveBeenCalledWith(1, 20);
    await user.click(screen.getByRole("button", { name: "我的邀请" }));
    expect(await screen.findByRole("heading", { name: "我的邀请" })).toBeVisible();
    expect(api.getInvitations).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "我的订单" }));
    expect(await screen.findByRole("heading", { name: "我的订单" })).toBeVisible();
    expect(api.listOrders).toHaveBeenCalledWith(undefined, 100);

    await user.click(screen.getByRole("button", { name: "退出" }));
    await waitFor(() => expect(onSignedOut).toHaveBeenCalledTimes(1));
  });

  it("keeps the session visible when logout fails", async () => {
    const api = {
      listVisibleNotices: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 }),
      listClientCatalog: vi.fn().mockResolvedValue([]),
      clientCatalogQR: vi.fn(),
      listKnowledge: vi.fn().mockResolvedValue([]),
      getKnowledge: vi.fn(),
      listTickets: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 }),
      createTicket: vi.fn(), getTicket: vi.fn(), replyTicket: vi.fn(), closeTicket: vi.fn(),
      getInvitations: vi.fn().mockResolvedValue({ codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0, commission_rate: 10, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0 }), createInvitation: vi.fn(),
      listCommissionLogs: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 50 }), transferCommission: vi.fn(),
      getCommissionWithdrawalPolicy: vi.fn().mockResolvedValue(emptyWithdrawalPolicy), listCommissionWithdrawals: vi.fn().mockResolvedValue(emptyWithdrawalPage), createCommissionWithdrawal: vi.fn(),
      listPlanOffers: vi.fn().mockResolvedValue([]),
      checkCoupon: vi.fn(), createOrder: vi.fn(), listOrders: vi.fn().mockResolvedValue([]), getOrder: vi.fn(), listPaymentMethods: vi.fn().mockResolvedValue([]), checkoutOrder: vi.fn(), cancelOrder: vi.fn(),
      getSubscription: vi.fn().mockResolvedValue({ plan_id: null, token: "1".repeat(32), expired_at: null, u: 0, d: 0, transfer_enable: 0, email: session.email, uuid: "11111111-1111-4111-8111-111111111111", device_limit: 0, speed_limit: 0, next_reset_at: null, plan: null, subscribe_url: "https://panel.example.test/s/token", reset_day: null, subscription_valid: false }),
      getSubscriptionQR: vi.fn(), resetSubscriptionSecurity: vi.fn(),
      checkGiftCard: vi.fn(), redeemGiftCard: vi.fn(), listMyGiftCardUsages: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 15 }),
      logout: vi.fn().mockRejectedValue(new Error("会话注销失败"))
    };
    const onSignedOut = vi.fn();
    const user = userEvent.setup();
    render(<UserPortal api={api} session={session} siteName="Tenant Board" siteLogo={null} couponEnabled onSignedOut={onSignedOut} />);

    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("会话注销失败");
    expect(onSignedOut).not.toHaveBeenCalled();
  });
});

const emptyWithdrawalPolicy = { currency: "CNY", minimum_amount: 10_000, methods: ["USDT"], available_commission: 0, frozen_commission: 0, active: null };
const emptyWithdrawalPage = { items: [], total: 0, page: 1, page_size: 20 };
