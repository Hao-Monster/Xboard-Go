import { useState } from "react";

import type { ClientCatalogEntry, ClientCatalogQR, CommissionLogPage, CommissionTransferResult, CouponQuote, GiftCardPreview, GiftCardRedeemResult, GiftCardUsagePage, InvitationCode, InvitationSummary, KnowledgeArticle, KnowledgeLanguage, LoginLinkRedirect, NoticePage, Order, OrderStatus, PaymentCheckout, PlanOffer, PlanPeriod, SubscriptionQR, Ticket, TicketInput, TicketPage, UserPaymentMethod, UserSession, UserSubscription } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { UserNoticesPage } from "../notices/UserNoticesPage";
import { UserTicketsPage } from "../tickets/UserTicketsPage";
import { BrandMark } from "../../components/BrandMark";
import { InvitationPage } from "../invitations/InvitationPage";
import { PlanCatalogPage } from "../plans/PlanCatalogPage";
import { UserSubscriptionPage } from "../subscription/UserSubscriptionPage";
import { UserOrdersPage } from "../orders/UserOrdersPage";
import { UserGiftCardPage } from "../giftcards/UserGiftCardPage";

interface UserPortalAPI {
  listVisibleNotices: (page?: number) => Promise<NoticePage>;
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
  listTickets: (page?: number, pageSize?: number) => Promise<TicketPage>;
  createTicket: (input: TicketInput) => Promise<Ticket>;
  getTicket: (id: number) => Promise<Ticket>;
  replyTicket: (id: number, message: string) => Promise<Ticket>;
  closeTicket: (id: number) => Promise<Ticket>;
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
  listCommissionLogs: (page?: number, pageSize?: number) => Promise<CommissionLogPage>;
  transferCommission: (amount: number) => Promise<CommissionTransferResult>;
  requestCommissionWithdrawal: (withdrawMethod: string, withdrawAccount: string) => Promise<Ticket>;
  listPlanOffers: () => Promise<PlanOffer[]>;
  checkCoupon: (code: string, planID: number, period: PlanPeriod) => Promise<CouponQuote>;
  createOrder: (planID: number, period: PlanPeriod, couponCode?: string) => Promise<Order>;
  listOrders: (status?: OrderStatus, limit?: number) => Promise<Order[]>;
  getOrder: (tradeNo: string) => Promise<Order>;
  listPaymentMethods: () => Promise<UserPaymentMethod[]>;
  checkoutOrder: (tradeNo: string, paymentID?: number) => Promise<Order | PaymentCheckout>;
  cancelOrder: (tradeNo: string) => Promise<Order>;
  getSubscription: () => Promise<UserSubscription>;
  getSubscriptionQR: () => Promise<SubscriptionQR>;
  resetSubscriptionSecurity: () => Promise<UserSubscription>;
  checkGiftCard: (code: string) => Promise<GiftCardPreview>;
  redeemGiftCard: (code: string) => Promise<GiftCardRedeemResult>;
  listMyGiftCardUsages: (page?: number, pageSize?: number) => Promise<GiftCardUsagePage>;
  logout: () => Promise<void>;
}

export function UserPortal({ api, session, siteName, siteLogo, couponEnabled, initialPage = "dashboard", onSignedOut }: {
  api: UserPortalAPI;
  session: UserSession;
  siteName: string;
  siteLogo: string | null;
  couponEnabled: boolean;
  initialPage?: LoginLinkRedirect;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<"subscription" | "plans" | "orders" | "gift-cards" | "notices" | "knowledge" | "tickets" | "clients" | "invitations">(() => portalPage(initialPage));
  const [openOrderTradeNo, setOpenOrderTradeNo] = useState<string | null>(null);
  const [logoutError, setLogoutError] = useState("");

  const logout = async () => {
    setLogoutError("");
    try {
      await api.logout();
      onSignedOut();
    } catch (cause) {
      setLogoutError(cause instanceof Error ? cause.message : "退出失败");
    }
  };

  return <div className="app-frame">
    <nav className="topbar" aria-label="用户导航">
      <div className="brand"><BrandMark appName={siteName} logo={siteLogo} /><span>{siteName}</span></div>
      <div className="admin-nav">
        <button className="nav-link" aria-current={page === "subscription" ? "page" : undefined} onClick={() => setPage("subscription")}>我的订阅</button>
        <button className="nav-link" aria-current={page === "plans" ? "page" : undefined} onClick={() => setPage("plans")}>订阅套餐</button>
        <button className="nav-link" aria-current={page === "orders" ? "page" : undefined} onClick={() => setPage("orders")}>我的订单</button>
        <button className="nav-link" aria-current={page === "gift-cards" ? "page" : undefined} onClick={() => setPage("gift-cards")}>礼品卡</button>
        <button className="nav-link" aria-current={page === "notices" ? "page" : undefined} onClick={() => setPage("notices")}>公告</button>
        <button className="nav-link" aria-current={page === "knowledge" ? "page" : undefined} onClick={() => setPage("knowledge")}>知识库</button>
        <button className="nav-link" aria-current={page === "tickets" ? "page" : undefined} onClick={() => setPage("tickets")}>我的工单</button>
        <button className="nav-link" aria-current={page === "clients" ? "page" : undefined} onClick={() => setPage("clients")}>客户端下载</button>
        <button className="nav-link" aria-current={page === "invitations" ? "page" : undefined} onClick={() => setPage("invitations")}>我的邀请</button>
      </div>
      <div className="account"><span>{session.email}</span><button className="button ghost compact" onClick={() => void logout()}>退出</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "subscription" && <UserSubscriptionPage api={api} onOpenTutorial={() => setPage("knowledge")} />}
    {page === "plans" && <PlanCatalogPage api={api} couponEnabled={couponEnabled} onOrderCreated={(order) => { setOpenOrderTradeNo(order.trade_no); setPage("orders"); }} />}
    {page === "orders" && <UserOrdersPage api={api} initialTradeNo={openOrderTradeNo} onInitialHandled={() => setOpenOrderTradeNo(null)} />}
    {page === "gift-cards" && <UserGiftCardPage api={api} />}
    {page === "notices" && <UserNoticesPage api={api} />}
    {page === "knowledge" && <UserKnowledgePage api={api} />}
    {page === "tickets" && <UserTicketsPage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
    {page === "invitations" && <InvitationPage api={api} />}
  </div>;
}

function portalPage(redirect: LoginLinkRedirect): "subscription" | "plans" | "orders" | "notices" | "knowledge" | "tickets" | "clients" | "invitations" {
  switch (redirect) {
    case "invite": return "invitations";
    case "knowledge": return "knowledge";
    case "ticket": return "tickets";
    case "subscribe": return "subscription";
    case "dashboard": return "subscription";
    default: return "subscription";
  }
}
