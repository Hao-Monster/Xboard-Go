import { useEffect, useState } from "react";

import type { ClientCatalogEntry, ClientCatalogQR, CommissionLogPage, CommissionTransferResult, CouponQuote, GiftCardPreview, GiftCardRedeemResult, GiftCardUsagePage, InvitationCode, InvitationSummary, KnowledgeArticle, KnowledgeLanguage, LoginLinkRedirect, NoticePage, Order, OrderStatus, PaymentCheckout, PlanOffer, PlanPeriod, SubscriptionQR, Ticket, TicketInput, TicketPage, UserPaymentMethod, UserSession, UserSubscription } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { UserNoticesPage } from "../notices/UserNoticesPage";
import { UserTicketsPage } from "../tickets/UserTicketsPage";
import { Modal } from "../../components/Overlay";
import { PortalIcon, type PortalIconName } from "./PortalIcon";
import "./UserPortal.css";
import { PortalAnnouncement } from "./PortalAnnouncement";
import { BrandMark } from "../../components/BrandMark";
import { InvitationPage } from "../invitations/InvitationPage";
import { PlanCatalogPage } from "../plans/PlanCatalogPage";
import { hasSubscription, UserSubscriptionPage } from "../subscription/UserSubscriptionPage";
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
  const [page, setPage] = useState<"loading" | "subscription" | "plans" | "orders" | "gift-cards" | "notices" | "knowledge" | "tickets" | "clients" | "invitations">(() => initialPage === "dashboard" ? "loading" : portalPage(initialPage));
  const [homeError, setHomeError] = useState("");
  const [homeAttempt, setHomeAttempt] = useState(0);
  useEffect(() => {
    if (initialPage !== "dashboard") return;
    let live = true;
    void api.getSubscription().then(subscription => {
      if (live) setPage(current => current === "loading" ? (hasSubscription(subscription) ? "subscription" : "plans") : current);
    }).catch((cause: unknown) => {
      if (live) setHomeError(cause instanceof Error ? cause.message : "订阅信息加载失败");
    });
    return () => { live = false; };
  }, [api, initialPage, homeAttempt]);
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

  const [collapsed, setCollapsed] = useState(false);
  const [mobileMenu, setMobileMenu] = useState(false);
  const [mobile, setMobile] = useState(() => window.innerWidth <= 720);
  useEffect(() => {
    const resize = () => { setMobile(window.innerWidth <= 720); if (window.innerWidth > 720) setMobileMenu(false); };
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  const navigation = <nav aria-label="用户导航">{navigationGroups.map(group => <div className="portal-nav-group" key={group.title}>
    {group.title !== "首页" && <p className="portal-group-title">{group.title}</p>}
    {group.items.map(item => <button key={item.page} className="portal-nav-link" title={item.label} aria-current={page === item.page ? "page" : undefined} onClick={() => { setPage(item.page); setMobileMenu(false); }}><PortalIcon name={item.icon} /><span>{item.label}</span></button>)}
  </div>)}</nav>;
  const activeItem = navigationGroups.flatMap(group => group.items).find(item => item.page === page);
  return <div className={`app-frame user-portal${collapsed ? " portal-collapsed" : ""}`}>
    <aside className="portal-sidebar">
      <div className="portal-brand"><BrandMark appName={siteName} logo={siteLogo} /><span>{siteName}</span></div>
      {navigation}
    </aside>
    <div className="portal-workspace">
    <header className="portal-topbar">
      <div className="portal-breadcrumb"><button className="portal-tool" aria-label="切换导航" aria-expanded={mobile ? mobileMenu : !collapsed} onClick={() => { if (mobile) setMobileMenu(true); else setCollapsed(value => !value); }}><PortalIcon name="menu" /></button><PortalIcon name={activeItem?.icon ?? "home"} /><span>{activeItem?.label ?? "加载中"}</span></div>
      <div className="portal-account"><PortalIcon name="user" /><span title={session.email}>{session.email}</span><button className="portal-tool" title="退出" aria-label="退出" onClick={() => void logout()}><PortalIcon name="logout" /></button></div>
    </header>
    {mobileMenu && <Modal className="portal-mobile-menu" title="用户导航" onClose={() => setMobileMenu(false)}><div className="modal-header"><h2>{siteName}</h2><button className="icon-button" aria-label="关闭导航" onClick={() => setMobileMenu(false)}>×</button></div>{navigation}</Modal>}
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "loading" && <main className="page-shell">{homeError ? <><p role="alert">{homeError}</p><button className="button secondary" onClick={() => { setHomeError(""); setHomeAttempt(attempt => attempt + 1); }}>重新加载订阅信息</button></> : <p role="status">正在加载订阅信息…</p>}</main>}
    {page === "subscription" && <UserSubscriptionPage announcement={<PortalAnnouncement api={api} onOpen={() => setPage("notices")} />} api={api} onOpenTutorial={() => setPage("knowledge")} onOpenPlans={() => setPage("plans")} onOpenTickets={() => setPage("tickets")} />}
    {page === "plans" && <PlanCatalogPage api={api} couponEnabled={couponEnabled} onOrderCreated={(order) => { setOpenOrderTradeNo(order.trade_no); setPage("orders"); }} />}
    {page === "orders" && <UserOrdersPage api={api} initialTradeNo={openOrderTradeNo} onInitialHandled={() => setOpenOrderTradeNo(null)} />}
    {page === "gift-cards" && <UserGiftCardPage api={api} />}
    {page === "notices" && <UserNoticesPage api={api} />}
    {page === "knowledge" && <UserKnowledgePage api={api} />}
    {page === "tickets" && <UserTicketsPage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
    {page === "invitations" && <InvitationPage api={api} />}
    </div>
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

type PortalPage = "subscription" | "plans" | "orders" | "gift-cards" | "notices" | "knowledge" | "tickets" | "clients" | "invitations";
const navigationGroups: { title: string; items: { page: PortalPage; label: string; icon: PortalIconName }[] }[] = [
  { title: "首页", items: [{ page: "subscription", label: "我的订阅", icon: "home" }, { page: "knowledge", label: "知识库", icon: "book" }] },
  { title: "财务", items: [{ page: "orders", label: "我的订单", icon: "orders" }, { page: "invitations", label: "我的邀请", icon: "invite" }] },
  { title: "订阅", items: [{ page: "plans", label: "订阅套餐", icon: "bag" }, { page: "clients", label: "客户端下载", icon: "download" }] },
  { title: "用户", items: [{ page: "gift-cards", label: "礼品卡", icon: "gift" }, { page: "tickets", label: "我的工单", icon: "ticket" }, { page: "notices", label: "公告", icon: "notice" }] },
];
