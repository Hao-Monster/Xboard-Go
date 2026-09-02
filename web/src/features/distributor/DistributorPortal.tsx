import { useEffect, useState } from "react";

import { BrandMark } from "../../components/BrandMark";
import type { ClientCatalogEntry, ClientCatalogQR, CommissionLogPage, CommissionTransferResult, CommissionWithdrawal, CommissionWithdrawalPage, CommissionWithdrawalPolicy, DistributorOrder, DistributorOrderPage, DistributorOrderQuery, DistributorQR, InvitationCode, InvitationSummary, KnowledgeArticle, KnowledgeLanguage, LoginLinkRedirect, PlanOffer, PlanPeriod, UserSession } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { InvitationPage } from "../invitations/InvitationPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { DistributorOrdersPage } from "./DistributorOrdersPage";
import { DistributorPlansPage } from "./DistributorPlansPage";
import { distributorCopy, type DistributorLocale } from "./locale";

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
  transferCommission: (amount: number, idempotencyKey: string) => Promise<CommissionTransferResult>;
  getCommissionWithdrawalPolicy: () => Promise<CommissionWithdrawalPolicy>;
  listCommissionWithdrawals: (page?: number, pageSize?: number) => Promise<CommissionWithdrawalPage>;
  createCommissionWithdrawal: (idempotencyKey: string, method: string, account: string) => Promise<CommissionWithdrawal>;
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
  logout: () => Promise<void>;
}

type DistributorPage = "plans" | "orders" | "invitations" | "knowledge" | "clients";
const navigation: Array<[DistributorPage, keyof Pick<typeof distributorCopy["zh-CN"], "buy" | "orders" | "invite" | "knowledge" | "clients">]> = [
  ["plans", "buy"], ["orders", "orders"], ["invitations", "invite"], ["knowledge", "knowledge"], ["clients", "clients"]
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
  const [locale, setLocale] = useState<DistributorLocale>(() => localStorage.getItem("xboard_distributor_locale") === "en-US" ? "en-US" : "zh-CN");
  const [light, setLight] = useState(() => localStorage.getItem("xboard_distributor_dark") !== "1");
  const [logoutError, setLogoutError] = useState("");

  useEffect(() => {
    const root = document.documentElement;
    if (light) root.dataset.distributorTheme = "light"; else delete root.dataset.distributorTheme;
    localStorage.setItem("xboard_distributor_dark", light ? "0" : "1");
    return () => { delete root.dataset.distributorTheme; };
  }, [light]);

  const switchLocale = () => {
    setLocale((current) => {
      const next = current === "zh-CN" ? "en-US" : "zh-CN";
      localStorage.setItem("xboard_distributor_locale", next);
      return next;
    });
  };
  const logout = async () => {
    setLogoutError("");
    try { await api.logout(); onSignedOut(); }
    catch (cause) { setLogoutError(cause instanceof Error ? cause.message : locale === "en-US" ? "Sign out failed" : "退出失败"); }
  };
  const copy = distributorCopy[locale];

  return <div className="app-frame distributor-portal" lang={locale}>
    <nav className="topbar" aria-label={locale === "en-US" ? "Distributor navigation" : "分销端导航"}>
      <div className="brand"><BrandMark appName={siteName} logo={siteLogo} /><span>{siteName}</span></div>
      <div className="admin-nav">{navigation.map(([key, label]) => <button className="nav-link" aria-current={page === key ? "page" : undefined} key={key} onClick={() => setPage(key)}>{copy[label]}</button>)}</div>
      <div className="account distributor-account"><button className="button ghost compact" aria-label={light ? copy.dark : copy.light} onClick={() => setLight((current) => !current)}>{light ? "☾" : "☀"}</button><button className="button ghost compact" aria-label={copy.language} onClick={switchLocale}>{locale === "zh-CN" ? "中" : "EN"}</button><span>{session.distributor_name ?? session.email}</span><button className="button ghost compact" onClick={() => void logout()}>{copy.logout}</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "plans" && <DistributorPlansPage api={api} locale={locale} />}
    {page === "orders" && <DistributorOrdersPage api={api} locale={locale} />}
    {page === "invitations" && <InvitationPage api={api} locale={locale} />}
    {page === "knowledge" && <UserKnowledgePage key={locale} api={api} locale={locale} fixedLocale />}
    {page === "clients" && <ClientCatalogPage api={api} locale={locale} />}
  </div>;
}

function landingPage(value: LoginLinkRedirect): DistributorPage {
  if (value === "invite") return "invitations";
  if (value === "knowledge") return "knowledge";
  return "plans";
}
