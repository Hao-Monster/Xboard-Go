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

import "./UserPortal.css";

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

type PortalPageKey = "subscription" | "plans" | "orders" | "gift-cards" | "notices" | "knowledge" | "tickets" | "clients" | "invitations";

function IconHome() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" /><polyline points="9 22 9 12 15 12 15 22" /></svg>;
}

function IconBook() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" /><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" /></svg>;
}

function IconOrders() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="8" y1="6" x2="21" y2="6" /><line x1="8" y1="12" x2="21" y2="12" /><line x1="8" y1="18" x2="21" y2="18" /><line x1="3" y1="6" x2="3.01" y2="6" /><line x1="3" y1="12" x2="3.01" y2="12" /><line x1="3" y1="18" x2="3.01" y2="18" /></svg>;
}

function IconInvite() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" /><circle cx="8.5" cy="7" r="4" /><line x1="20" y1="8" x2="20" y2="14" /><line x1="23" y1="11" x2="17" y2="11" /></svg>;
}

function IconPlans() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M6 2L3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4z" /><line x1="3" y1="6" x2="21" y2="6" /><path d="M16 10a4 4 0 0 1-8 0" /></svg>;
}

function IconGift() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="20 12 20 22 4 22 4 12" /><rect x="2" y="7" width="20" height="5" /><line x1="12" y1="22" x2="12" y2="7" /><path d="M12 7H7.5a2.5 2.5 0 0 1 0-5C11 2 12 7 12 7z" /><path d="M12 7h4.5a2.5 2.5 0 0 0 0-5C13 2 12 7 12 7z" /></svg>;
}

function IconDownload() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><polyline points="7 10 12 15 17 10" /><line x1="12" y1="15" x2="12" y2="3" /></svg>;
}

function IconTicket() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" /></svg>;
}

function IconNotice() {
  return <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" /><path d="M13.73 21a2 2 0 0 1-3.46 0" /></svg>;
}

function IconSidebarToggle() {
  return <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" /><line x1="9" y1="3" x2="9" y2="21" /></svg>;
}

interface NavItemDef {
  key: PortalPageKey;
  label: string;
  ariaLabel: string;
  icon: () => React.JSX.Element;
}

interface NavGroupDef {
  title?: string;
  items: NavItemDef[];
}

const userNavGroups: NavGroupDef[] = [
  {
    items: [
      { key: "subscription", label: "仪表盘", ariaLabel: "我的订阅", icon: IconHome },
      { key: "knowledge", label: "使用文档", ariaLabel: "知识库", icon: IconBook },
    ],
  },
  {
    title: "财务",
    items: [
      { key: "orders", label: "我的订单", ariaLabel: "我的订单", icon: IconOrders },
      { key: "invitations", label: "我的邀请", ariaLabel: "我的邀请", icon: IconInvite },
    ],
  },
  {
    title: "订阅",
    items: [
      { key: "plans", label: "购买订阅", ariaLabel: "订阅套餐", icon: IconPlans },
      { key: "gift-cards", label: "礼品卡", ariaLabel: "礼品卡", icon: IconGift },
      { key: "clients", label: "客户端下载", ariaLabel: "客户端下载", icon: IconDownload },
    ],
  },
  {
    title: "用户",
    items: [
      { key: "tickets", label: "我的工单", ariaLabel: "我的工单", icon: IconTicket },
      { key: "notices", label: "公告", ariaLabel: "公告", icon: IconNotice },
    ],
  },
];

const pageMeta: Record<PortalPageKey, { title: string; icon: () => React.JSX.Element }> = {
  subscription: { title: "仪表盘", icon: IconHome },
  knowledge: { title: "使用文档", icon: IconBook },
  orders: { title: "我的订单", icon: IconOrders },
  invitations: { title: "我的邀请", icon: IconInvite },
  plans: { title: "购买订阅", icon: IconPlans },
  "gift-cards": { title: "礼品卡", icon: IconGift },
  clients: { title: "客户端下载", icon: IconDownload },
  tickets: { title: "我的工单", icon: IconTicket },
  notices: { title: "公告", icon: IconNotice },
};

export function UserPortal({ api, session, siteName, siteLogo, couponEnabled, initialPage = "dashboard", onSignedOut }: {
  api: UserPortalAPI;
  session: UserSession;
  siteName: string;
  siteLogo: string | null;
  couponEnabled: boolean;
  initialPage?: LoginLinkRedirect;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<PortalPageKey>(() => portalPage(initialPage));
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
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

  const CurrentPageIcon = pageMeta[page].icon;

  return (
    <div className="app-frame user-portal-frame">
      <div className={`user-portal-layout ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
        {/* Left Sidebar */}
        <aside className={`user-sidebar ${sidebarCollapsed ? "collapsed" : ""}`} aria-label="用户侧边栏">
          <div className="user-sidebar-header">
            <div className="brand">
              <BrandMark appName={siteName} logo={siteLogo} />
              {!sidebarCollapsed && <span className="brand-name">{siteName}</span>}
            </div>
          </div>
          <div className="user-sidebar-content">
            <nav className="user-nav" aria-label="用户导航">
              {userNavGroups.map((group, groupIdx) => (
                <div key={groupIdx} className="user-nav-group">
                  {group.title && !sidebarCollapsed && (
                    <div className="user-nav-group-title">{group.title}</div>
                  )}
                  {group.items.map((item) => {
                    const ItemIcon = item.icon;
                    const isActive = page === item.key;
                    return (
                      <button
                        key={item.key}
                        className="nav-link user-nav-item"
                        aria-label={item.ariaLabel}
                        aria-current={isActive ? "page" : undefined}
                        title={item.label}
                        onClick={() => setPage(item.key)}
                      >
                        <span className="nav-item-icon" aria-hidden="true">
                          <ItemIcon />
                        </span>
                        {!sidebarCollapsed && <span className="user-nav-item-text">{item.label}</span>}
                      </button>
                    );
                  })}
                </div>
              ))}
            </nav>
          </div>
        </aside>

        {/* Right Main Content */}
        <div className="user-portal-main">
          <header className="user-portal-topbar">
            <div className="topbar-left">
              <button
                type="button"
                className="user-sidebar-toggle-btn"
                aria-label={sidebarCollapsed ? "展开侧边栏" : "折叠侧边栏"}
                onClick={() => setSidebarCollapsed(!sidebarCollapsed)}
                title={sidebarCollapsed ? "展开侧边栏" : "折叠侧边栏"}
              >
                <IconSidebarToggle />
              </button>
              <div className="page-breadcrumb">
                <span className="breadcrumb-icon" aria-hidden="true">
                  <CurrentPageIcon />
                </span>
                <span className="breadcrumb-title">{pageMeta[page].title}</span>
              </div>
            </div>
            <div className="topbar-right">
              <div className="account-info">
                <span className="user-email">{session.email}</span>
                <button type="button" className="button ghost compact logout-btn" onClick={() => void logout()}>
                  退出
                </button>
              </div>
            </div>
          </header>

          {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}

          <div className="user-portal-body">
            {page === "subscription" && <UserSubscriptionPage api={api} onOpenTutorial={() => setPage("knowledge")} />}
            {page === "plans" && <PlanCatalogPage api={api} couponEnabled={couponEnabled} onOrderCreated={(order) => { setOpenOrderTradeNo(order.trade_no); setPage("orders"); }} />}
            {page === "orders" && <UserOrdersPage api={api} initialTradeNo={openOrderTradeNo} onInitialHandled={() => setOpenOrderTradeNo(null)} />}
            {page === "gift-cards" && <UserGiftCardPage api={api} />}
            {page === "notices" && <UserNoticesPage api={api} />}
            {page === "knowledge" && <UserKnowledgePage api={api} />}
            {page === "tickets" && <UserTicketsPage api={api} />}
            {page === "clients" && <ClientCatalogPage api={api} />}
            {page === "invitations" && <InvitationPage api={api} />}
          </div>
        </div>
      </div>
    </div>
  );
}

function portalPage(redirect: LoginLinkRedirect): PortalPageKey {
  switch (redirect) {
    case "invite": return "invitations";
    case "knowledge": return "knowledge";
    case "ticket": return "tickets";
    case "subscribe": return "subscription";
    case "dashboard": return "subscription";
    default: return "subscription";
  }
}
