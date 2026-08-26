import { useEffect, useState } from "react";

import { BrandMark } from "../../components/BrandMark";
import type { ClientCatalogEntry, ClientCatalogQR, CommissionLogPage, CommissionTransferResult, DistributorOrder, DistributorOrderPage, DistributorOrderQuery, DistributorQR, InvitationCode, InvitationSummary, KnowledgeArticle, KnowledgeLanguage, LoginLinkRedirect, PlanOffer, PlanPeriod, UserSession } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { InvitationPage } from "../invitations/InvitationPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { DistributorOrdersPage } from "./DistributorOrdersPage";
import { DistributorPlansPage } from "./DistributorPlansPage";

interface DistributorPortalAPI {
  listPlanOffers: () => Promise<PlanOffer[]>;
  createDistributorOrder: (planID: number, period: PlanPeriod) => Promise<DistributorOrder>;
  listDistributorOrders: (query?: DistributorOrderQuery) => Promise<DistributorOrderPage>;
  getDistributorOrderQR: (tradeNo: string) => Promise<DistributorQR>;
  renewDistributorOrder: (tradeNo: string, period: PlanPeriod, idempotencyKey: string) => Promise<DistributorOrder>;
  exportDistributorOrders: (query?: DistributorOrderQuery) => Promise<Blob>;
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
  listCommissionLogs: (page?: number, pageSize?: number) => Promise<CommissionLogPage>;
  transferCommission: (amount: number) => Promise<CommissionTransferResult>;
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
  logout: () => Promise<void>;
}

type DistributorPage = "plans" | "orders" | "invitations" | "knowledge" | "clients";
type Locale = "zh-CN" | "en-US";

const navigation: Array<[DistributorPage, Record<Locale, string>]> = [
  ["plans", { "zh-CN": "购买订阅", "en-US": "Buy subscriptions" }],
  ["orders", { "zh-CN": "我的订单", "en-US": "My orders" }],
  ["invitations", { "zh-CN": "我的邀请", "en-US": "Invitations" }],
  ["knowledge", { "zh-CN": "使用文档", "en-US": "Guides" }],
  ["clients", { "zh-CN": "客户端下载", "en-US": "Downloads" }]
];

export function DistributorPortal({ api, session, siteName, siteLogo, initialPage = "dashboard", onSignedOut }: {
  api: DistributorPortalAPI;
  session: UserSession;
  siteName: string;
  siteLogo: string | null;
  initialPage?: LoginLinkRedirect;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<DistributorPage>(() => landingPage(initialPage));
  const [locale, setLocale] = useState<Locale>(() => localStorage.getItem("xboard-distributor-locale") === "en-US" ? "en-US" : "zh-CN");
  const [light, setLight] = useState(() => localStorage.getItem("xboard-distributor-theme") === "light");
  const [logoutError, setLogoutError] = useState("");

  useEffect(() => {
    const root = document.documentElement;
    if (light) root.dataset.distributorTheme = "light"; else delete root.dataset.distributorTheme;
    localStorage.setItem("xboard-distributor-theme", light ? "light" : "dark");
    return () => { delete root.dataset.distributorTheme; };
  }, [light]);

  const switchLocale = () => {
    setLocale((current) => {
      const next = current === "zh-CN" ? "en-US" : "zh-CN";
      localStorage.setItem("xboard-distributor-locale", next);
      return next;
    });
  };
  const logout = async () => {
    setLogoutError("");
    try { await api.logout(); onSignedOut(); }
    catch (cause) { setLogoutError(cause instanceof Error ? cause.message : "退出失败"); }
  };

  return <div className="app-frame distributor-portal" lang={locale}>
    <nav className="topbar" aria-label="分销端导航">
      <div className="brand"><BrandMark appName={siteName} logo={siteLogo} /><span>{siteName}</span></div>
      <div className="admin-nav">{navigation.map(([key, labels]) => <button className="nav-link" aria-current={page === key ? "page" : undefined} key={key} onClick={() => setPage(key)}>{labels[locale]}</button>)}</div>
      <div className="account distributor-account"><button className="button ghost compact" aria-label={light ? "切换深色主题" : "切换浅色主题"} onClick={() => setLight((current) => !current)}>{light ? "☾" : "☀"}</button><button className="button ghost compact" aria-label="切换语言" onClick={switchLocale}>{locale === "zh-CN" ? "中" : "EN"}</button><span>{session.distributor_name ?? session.email}</span><button className="button ghost compact" onClick={() => void logout()}>{locale === "zh-CN" ? "退出" : "Log out"}</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "plans" && <DistributorPlansPage api={api} />}
    {page === "orders" && <DistributorOrdersPage api={api} />}
    {page === "invitations" && <InvitationPage api={api} locale={locale} />}
    {page === "knowledge" && <UserKnowledgePage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
  </div>;
}

function landingPage(value: LoginLinkRedirect): DistributorPage {
  if (value === "invite") return "invitations";
  if (value === "knowledge") return "knowledge";
  return "plans";
}
