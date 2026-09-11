import { lazy, Suspense, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { APIClient, type GuestConfig, type LoginLinkRedirect, type SiteSettings, type ThemeAppearance, type UserSession } from "./lib/api";
import { resetCaptchaProviderScripts, useCaptchaChallenge } from "./features/auth/CaptchaChallenge";
import { BrandMark } from "./components/BrandMark";
import { TopProgressBar } from "./components/TopProgressBar";

const AccountSecurityPage = lazy(async () => import("./features/account/AccountSecurityPage").then((module) => ({ default: module.AccountSecurityPage })));
const RoutingRulesPage = lazy(async () => import("./features/admin/RoutingRulesPage").then((module) => ({ default: module.RoutingRulesPage })));
const ServerGroupsPage = lazy(async () => import("./features/admin/ServerGroupsPage").then((module) => ({ default: module.ServerGroupsPage })));
const ClientCatalogManagementPage = lazy(async () => import("./features/clients/ClientCatalogManagementPage").then((module) => ({ default: module.ClientCatalogManagementPage })));
const CouponManagementPage = lazy(async () => import("./features/coupons/CouponManagementPage").then((module) => ({ default: module.CouponManagementPage })));
const KnowledgeManagementPage = lazy(async () => import("./features/knowledge/KnowledgeManagementPage").then((module) => ({ default: module.KnowledgeManagementPage })));
const NoticeManagementPage = lazy(async () => import("./features/notices/NoticeManagementPage").then((module) => ({ default: module.NoticeManagementPage })));
const OrderManagementPage = lazy(async () => import("./features/orders/OrderManagementPage").then((module) => ({ default: module.OrderManagementPage })));
const PlanManagementPage = lazy(async () => import("./features/plans/PlanManagementPage").then((module) => ({ default: module.PlanManagementPage })));
const ServerManagementPage = lazy(async () => import("./features/servers/ServerManagementPage").then((module) => ({ default: module.ServerManagementPage })));
const SystemConfigShell = lazy(async () => import("./features/settings/SystemConfigShell").then((module) => ({ default: module.SystemConfigShell })));
const SiteSettingsPage = lazy(async () => import("./features/settings/SiteSettingsPage").then((module) => ({ default: module.SiteSettingsPage })));
const SubscriptionSettingsPage = lazy(async () => import("./features/settings/SubscriptionSettingsPage").then((module) => ({ default: module.SubscriptionSettingsPage })));
const SystemOperationsPage = lazy(async () => import("./features/system/SystemOperationsPage").then((module) => ({ default: module.SystemOperationsPage })));
const AdminDashboardPage = lazy(async () => import("./features/system/AdminDashboardPage").then((module) => ({ default: module.AdminDashboardPage })));
const TicketManagementPage = lazy(async () => import("./features/tickets/TicketManagementPage").then((module) => ({ default: module.TicketManagementPage })));
const UsersPage = lazy(async () => import("./features/users/UsersPage").then((module) => ({ default: module.UsersPage })));
const NodeManagementPage = lazy(async () => import("./features/nodes/NodeManagementPage").then((module) => ({ default: module.NodeManagementPage })));
const NodeAgentSettingsPage = lazy(async () => import("./features/settings/NodeAgentSettingsPage").then((module) => ({ default: module.NodeAgentSettingsPage })));
const CommissionSettingsPage = lazy(async () => import("./features/settings/CommissionSettingsPage").then((module) => ({ default: module.CommissionSettingsPage })));
const EmailSettingsPage = lazy(async () => import("./features/settings/EmailSettingsPage").then((module) => ({ default: module.EmailSettingsPage })));
const TelegramSettingsPage = lazy(async () => import("./features/settings/TelegramSettingsPage").then((module) => ({ default: module.TelegramSettingsPage })));
const ClientAppSettingsPage = lazy(async () => import("./features/settings/ClientAppSettingsPage").then((module) => ({ default: module.ClientAppSettingsPage })));
const ThemeManagementPage = lazy(async () => import("./features/settings/ThemeManagementPage").then((module) => ({ default: module.ThemeManagementPage })));
const PaymentManagementPage = lazy(async () => import("./features/payments/PaymentManagementPage").then((module) => ({ default: module.PaymentManagementPage })));
const PluginManagementPage = lazy(async () => import("./features/plugins/PluginManagementPage").then((module) => ({ default: module.PluginManagementPage })));
const GiftCardManagementPage = lazy(async () => import("./features/giftcards/GiftCardManagementPage").then((module) => ({ default: module.GiftCardManagementPage })));
const UserPortal = lazy(async () => import("./features/user/UserPortal").then((module) => ({ default: module.UserPortal })));
const DistributorPortal = lazy(async () => import("./features/distributor/DistributorPortal").then((module) => ({ default: module.DistributorPortal })));
const AdminDistributorPage = lazy(async () => import("./features/distributor/AdminDistributorPage").then((module) => ({ default: module.AdminDistributorPage })));
const defaultThemeAppearance: ThemeAppearance = {
  name: "Xboard", revision: 1, package_sha256: "0".repeat(64),
  palette: { background: "#0b0d12", surface: "#151922", text: "#e8ebf2", muted: "#9ba3b5", primary: "#9ab2ff", primary_text: "#101218", border: "#303746" },
  config: { theme_color: "default", background_url: "", font_scale: "normal", radius: "rounded" },
  sidebar_style: "light", header_style: "dark"
};
const defaultGuestConfig: GuestConfig = {
  app_name: "Xboard-Go", app_description: null, app_url: null, tos_url: null, logo: null,
  is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
  captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
  recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0, theme: defaultThemeAppearance
};
type AuthMode = "login" | "register" | "recover";
type AdminPage = "system" | "settings" | "themes" | "mail" | "telegram" | "client-app" | "commissions" | "subscriptions" | "node-settings" | "servers" | "nodes" | "plans" | "orders" | "distributors" | "plugins" | "payments" | "coupons" | "gift-cards" | "users" | "tickets" | "groups" | "routes" | "notices" | "knowledge" | "clients" | "account";

type AdminNavItem = { key: AdminPage; label: string };
type AdminNavGroup = { key: string; label: string; items: AdminNavItem[] };

const adminNavGroups: AdminNavGroup[] = [
  { key: "system", label: "系统管理", items: [
    { key: "settings", label: "系统配置" }, { key: "plugins", label: "插件管理" },
    { key: "themes", label: "主题配置" }, { key: "notices", label: "公告管理" },
    { key: "payments", label: "支付配置" }, { key: "knowledge", label: "知识库管理" },
    { key: "clients", label: "客户端管理" }
  ] },
  { key: "nodes", label: "节点管理", items: [
    { key: "servers", label: "服务器管理" }, { key: "nodes", label: "节点管理" },
    { key: "groups", label: "权限组管理" }, { key: "routes", label: "路由管理" }
  ] },
  { key: "subscriptions", label: "订阅管理", items: [
    { key: "plans", label: "套餐管理" }, { key: "orders", label: "订单管理" },
    { key: "coupons", label: "优惠券管理" }, { key: "gift-cards", label: "礼品卡管理" },
    { key: "distributors", label: "分销管理" }
  ] },
  { key: "users", label: "用户管理", items: [
    { key: "users", label: "用户管理" }, { key: "tickets", label: "工单管理" }
  ] }
];

const pageLoaders: Record<AdminPage, () => Promise<unknown>> = {
  system: () => import("./features/system/AdminDashboardPage"),
  settings: () => import("./features/settings/SystemConfigShell"),
  themes: () => import("./features/settings/ThemeManagementPage"),
  mail: () => import("./features/settings/EmailSettingsPage"),
  telegram: () => import("./features/settings/TelegramSettingsPage"),
  "client-app": () => import("./features/settings/ClientAppSettingsPage"),
  commissions: () => import("./features/settings/CommissionSettingsPage"),
  subscriptions: () => import("./features/settings/SubscriptionSettingsPage"),
  "node-settings": () => import("./features/settings/NodeAgentSettingsPage"),
  servers: () => import("./features/servers/ServerManagementPage"),
  nodes: () => import("./features/nodes/NodeManagementPage"),
  plans: () => import("./features/plans/PlanManagementPage"),
  orders: () => import("./features/orders/OrderManagementPage"),
  distributors: () => import("./features/distributor/AdminDistributorPage"),
  plugins: () => import("./features/plugins/PluginManagementPage"),
  payments: () => import("./features/payments/PaymentManagementPage"),
  coupons: () => import("./features/coupons/CouponManagementPage"),
  "gift-cards": () => import("./features/giftcards/GiftCardManagementPage"),
  users: () => import("./features/users/UsersPage"),
  tickets: () => import("./features/tickets/TicketManagementPage"),
  groups: () => import("./features/admin/ServerGroupsPage"),
  routes: () => import("./features/admin/RoutingRulesPage"),
  notices: () => import("./features/notices/NoticeManagementPage"),
  knowledge: () => import("./features/knowledge/KnowledgeManagementPage"),
  clients: () => import("./features/clients/ClientCatalogManagementPage"),
  account: () => import("./features/account/AccountSecurityPage")
};

function initialAdminPage(): AdminPage {
  const saved = window.localStorage.getItem("xboard-go-admin-page");
  return saved !== null && Object.prototype.hasOwnProperty.call(pageLoaders, saved) ? saved as AdminPage : "servers";
}

function prefetchAdminAPI(target: AdminPage, client: APIClient): void {
  switch (target) {
    case "node-settings":
      void client.getNodeAgentSettings().catch(() => undefined);
      break;
    case "settings":
      void client.getSiteSettings().catch(() => undefined);
      void client.listPlans().catch(() => undefined);
      break;
    case "plans":
      void client.listPlans().catch(() => undefined);
      break;
    case "orders":
      void client.listAdminOrders().catch(() => undefined);
      break;
    case "servers":
      void client.listMachines().catch(() => undefined);
      break;
    case "nodes":
      void client.listAdminNodes().catch(() => undefined);
      break;
    case "system":
      void client.getSystemStatus().catch(() => undefined);
      break;
    case "commissions":
      void client.getCommissionSettings().catch(() => undefined);
      break;
    case "mail":
      void client.getMailSettings().catch(() => undefined);
      break;
    case "telegram":
      void client.getTelegramSettings().catch(() => undefined);
      break;
    case "subscriptions":
      void client.getSubscriptionSettings().catch(() => undefined);
      break;
    case "client-app":
      void client.getClientAppSettings().catch(() => undefined);
      break;
    case "coupons":
      void client.listCoupons().catch(() => undefined);
      break;
    case "groups":
      void client.listServerGroups().catch(() => undefined);
      break;
    case "routes":
      void client.listRoutingRules().catch(() => undefined);
      break;
    case "notices":
      void client.listNotices().catch(() => undefined);
      break;
    case "knowledge":
      void client.listKnowledgeAdmin().catch(() => undefined);
      break;
    case "clients":
      void client.listClientCatalogAdmin().catch(() => undefined);
      break;
    case "payments":
      void client.listPaymentProviders().catch(() => undefined);
      void client.listAdminPayments().catch(() => undefined);
      break;
    case "gift-cards":
      void client.listGiftCardTemplates().catch(() => undefined);
      break;
    default:
      break;
  }
}

function IconSearch() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" />
    </svg>
  );
}

function IconMoon() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z" />
    </svg>
  );
}

function IconChevronRight() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m9 18 6-6-6-6" />
    </svg>
  );
}

function IconChevronDown() {
  return (
    <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m6 9 6 6 6-6" />
    </svg>
  );
}

function IconDashboard() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="7" height="9" x="3" y="3" rx="1" /><rect width="7" height="5" x="14" y="3" rx="1" /><rect width="7" height="9" x="14" y="12" rx="1" /><rect width="7" height="5" x="3" y="16" rx="1" />
    </svg>
  );
}

function IconSettings() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" /><circle cx="12" cy="12" r="3" />
    </svg>
  );
}

function IconServer() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="20" height="8" x="2" y="2" rx="2" ry="2" /><rect width="20" height="8" x="2" y="14" rx="2" ry="2" /><line x1="6" x2="6.01" y1="6" y2="6" /><line x1="6" x2="6.01" y1="18" y2="18" />
    </svg>
  );
}

function IconCreditCard() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="20" height="14" x="2" y="5" rx="2" /><line x1="2" x2="22" y1="10" y2="10" />
    </svg>
  );
}

function IconUsers() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" /><circle cx="9" cy="7" r="4" /><path d="M22 21v-2a4 4 0 0 0-3-3.87" /><path d="M16 3.13a4 4 0 0 1 0 7.75" />
    </svg>
  );
}

function IconUser() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2" /><circle cx="12" cy="7" r="4" />
    </svg>
  );
}

function IconLogOut() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" /><polyline points="16 17 21 12 16 7" /><line x1="21" x2="9" y1="12" y2="12" />
    </svg>
  );
}

function GroupIcon({ kind }: { kind: string }) {
  switch (kind) {
    case "system": return <IconSettings />;
    case "nodes": return <IconServer />;
    case "subscriptions": return <IconCreditCard />;
    case "users": return <IconUsers />;
    default: return <IconDashboard />;
  }
}

function IconSliders() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <line x1="4" x2="4" y1="21" y2="14" /><line x1="4" x2="4" y1="10" y2="3" />
      <line x1="12" x2="12" y1="21" y2="12" /><line x1="12" x2="12" y1="8" y2="3" />
      <line x1="20" x2="20" y1="21" y2="16" /><line x1="20" x2="20" y1="12" y2="3" />
      <line x1="1" x2="7" y1="14" y2="14" /><line x1="9" x2="15" y1="8" y2="8" /><line x1="17" x2="23" y1="16" y2="16" />
    </svg>
  );
}

function IconPackage() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M16.5 9.4 7.55 4.24" />
      <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
      <polyline points="3.29 7 12 12 20.71 7" /><line x1="12" x2="12" y1="22" y2="12" />
    </svg>
  );
}

function IconMonitor() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="20" height="14" x="2" y="3" rx="2" /><line x1="8" x2="16" y1="21" y2="21" /><line x1="12" x2="12" y1="17" y2="21" />
    </svg>
  );
}

function IconFileText() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><polyline points="14 2 14 8 20 8" />
      <line x1="16" x2="8" y1="13" y2="13" /><line x1="16" x2="8" y1="17" y2="17" /><polyline points="10 9 9 9 8 9" />
    </svg>
  );
}

function IconBook() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" /><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
    </svg>
  );
}

function IconAppWindow() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="2" y="4" width="20" height="16" rx="2" /><path d="M10 4v4" /><path d="M2 8h20" /><path d="M6 4v4" />
    </svg>
  );
}

function IconNetwork() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" />
      <line x1="8.59" x2="15.42" y1="13.51" y2="17.49" /><line x1="15.41" x2="8.59" y1="6.51" y2="10.49" />
    </svg>
  );
}

function IconShield() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
    </svg>
  );
}

function IconRoute() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="12" cy="19" r="2" /><circle cx="6" cy="5" r="2" /><circle cx="18" cy="5" r="2" />
      <path d="M18 7v2a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V7" /><path d="M12 11v6" />
    </svg>
  );
}

function IconShoppingBag() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M6 2 3 6v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V6l-3-4z" /><line x1="3" x2="21" y1="6" y2="6" /><path d="M16 10a4 4 0 0 1-8 0" />
    </svg>
  );
}

function IconReceipt() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4 2v20l2-1 2 1 2-1 2 1 2-1 2 1 2-1 2 1V2l-2 1-2-1-2 1-2-1-2 1-2-1-2 1z" />
      <line x1="8" x2="16" y1="8" y2="8" /><line x1="8" x2="16" y1="12" y2="12" /><line x1="8" x2="12" y1="16" y2="16" />
    </svg>
  );
}

function IconTicket() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2z" />
      <line x1="13" x2="13" y1="5" y2="7" /><line x1="13" x2="13" y1="11" y2="13" /><line x1="13" x2="13" y1="17" y2="19" />
    </svg>
  );
}

function IconGift() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <polyline points="20 12 20 22 4 22 4 12" /><rect width="20" height="5" x="2" y="7" /><line x1="12" x2="12" y1="22" y2="7" />
      <path d="M12 7H7.5a2.5 2.5 0 0 1 0-5C11 2 12 7 12 7z" /><path d="M12 7h4.5a2.5 2.5 0 0 0 0-5C13 2 12 7 12 7z" />
    </svg>
  );
}

function IconLayers() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <polygon points="12 2 2 7 12 12 22 7 12 2" /><polyline points="2 17 12 22 22 17" /><polyline points="2 12 12 17 22 12" />
    </svg>
  );
}

function IconMessageSquare() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
    </svg>
  );
}

function ItemIcon({ page }: { page: AdminPage }) {
  switch (page) {
    case "settings": return <IconSliders />;
    case "plugins": return <IconPackage />;
    case "themes": return <IconMonitor />;
    case "notices": return <IconFileText />;
    case "payments": return <IconCreditCard />;
    case "knowledge": return <IconBook />;
    case "clients": return <IconAppWindow />;
    case "servers": return <IconServer />;
    case "nodes": return <IconNetwork />;
    case "groups": return <IconShield />;
    case "routes": return <IconRoute />;
    case "plans": return <IconShoppingBag />;
    case "orders": return <IconReceipt />;
    case "coupons": return <IconTicket />;
    case "gift-cards": return <IconGift />;
    case "distributors": return <IconLayers />;
    case "users": return <IconUser />;
    case "tickets": return <IconMessageSquare />;
    default: return null;
  }
}

export type AppSurface = { kind: "public" } | { kind: "admin"; path: string };

export function surfaceFromPathname(pathname = window.location.pathname): AppSurface {
  if (pathname === "/" || pathname === "/index.html") return { kind: "public" };
  const match = pathname.match(/^\/([0-9A-Za-z_-]{1,64})\/?$/);
  return match === null ? { kind: "public" } : { kind: "admin", path: match[1]! };
}

export function App({ surface = surfaceFromPathname() }: { surface?: AppSurface } = {}) {
  const adminPath = surface.kind === "admin" ? surface.path : undefined;
  const api = useMemo(() => new APIClient(adminPath), [adminPath]);
  const [session, setSession] = useState<UserSession | null>(null);
  const [guestConfig, setGuestConfig] = useState<GuestConfig>(defaultGuestConfig);
  const [loading, setLoading] = useState(true);
  const [bootstrapAuthError, setBootstrapAuthError] = useState("");
  const [userLanding, setUserLanding] = useState<LoginLinkRedirect>(() => loginLandingFromHash());
  const [authLocation, setAuthLocation] = useState(() => window.location.hash);
  const authMode = authModeFromHash(authLocation);
  const [page, setPage] = useState<AdminPage>(initialAdminPage);
  const [expandedAdminGroups, setExpandedAdminGroups] = useState<Record<string, boolean>>(() => Object.fromEntries(adminNavGroups.map((group) => [group.key, true])));
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [clientAppSettingsDirty, setClientAppSettingsDirty] = useState(false);
  const [themeSettingsDirty, setThemeSettingsDirty] = useState(false);
  const authenticationSequence = useRef(0);

  const searchResults = useMemo(() => {
    if (!searchQuery.trim()) return [];
    const q = searchQuery.toLowerCase().trim();
    const results: { key: AdminPage; label: string; group: string }[] = [];
    if ("仪表盘".includes(q) || "dashboard".includes(q)) {
      results.push({ key: "system", label: "仪表盘", group: "概览" });
    }
    for (const g of adminNavGroups) {
      for (const item of g.items) {
        if (item.label.toLowerCase().includes(q) || item.key.toLowerCase().includes(q)) {
          results.push({ key: item.key, label: item.label, group: g.label });
        }
      }
    }
    const systemSubTabs: { key: AdminPage; label: string }[] = [
      { key: "settings", label: "站点设置" },
      { key: "settings", label: "安全设置" },
      { key: "subscriptions", label: "订阅设置" },
      { key: "commissions", label: "邀请&佣金设置" },
      { key: "node-settings", label: "节点配置" },
      { key: "mail", label: "邮件设置" },
      { key: "telegram", label: "Telegram 设置" },
      { key: "client-app", label: "APP设置" },
      { key: "client-app", label: "客户端版本" },
      { key: "settings", label: "订阅模板" }
    ];
    for (const item of systemSubTabs) {
      if (item.label.toLowerCase().includes(q)) {
        if (!results.some((r) => r.key === item.key && r.label === item.label)) {
          results.push({ key: item.key, label: item.label, group: "系统配置" });
        }
      }
    }
    return results.slice(0, 8);
  }, [searchQuery]);

  const toggleThemeMode = () => {
    const isLight = document.documentElement.dataset.themeHeaderStyle === "light";
    document.documentElement.dataset.themeHeaderStyle = isLight ? "dark" : "light";
    document.documentElement.dataset.themeSidebarStyle = isLight ? "dark" : "light";
  };

  useEffect(() => {
    let active = true;
    const loginLink = loginLinkFromHash();
    const sequence = ++authenticationSequence.current;
    void api.guestConfig().then((config) => {
      if (active) setGuestConfig(config);
    }).catch(() => undefined);
    const authentication = loginLink === null
      ? api.session().then((nextSession) => ({ ...nextSession, redirect: "dashboard" as LoginLinkRedirect }))
      : api.exchangeLoginLink(loginLink.token);
    void authentication.then((nextSession) => {
      if (!active || sequence !== authenticationSequence.current) return;
      setSession(nextSession);
      if (loginLink !== null) {
        setUserLanding(nextSession.redirect);
        window.history.replaceState(null, "", nextSession.is_admin ? "#/" : loginLinkLandingHash(nextSession.redirect));
        setAuthLocation(window.location.hash);
      }
    }).catch((cause: unknown) => {
      if (!active || sequence !== authenticationSequence.current) return;
      setSession(null);
      if (loginLink !== null) {
        window.history.replaceState(null, "", "#/login");
        setAuthLocation(window.location.hash);
        setBootstrapAuthError(cause instanceof Error ? cause.message : "登录链接无效或已过期");
      }
    }).finally(() => {
      if (active && sequence === authenticationSequence.current) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  useEffect(() => {
    const authTitle = authMode === "register" ? "注册" : authMode === "recover" ? "重置密码" : "登录";
    document.title = session === null
      ? `${authTitle} | ${guestConfig.app_name}`
      : `${guestConfig.app_name} 控制面板`;
    let description = document.querySelector<HTMLMetaElement>('meta[name="description"]');
    if (description === null) {
      description = document.createElement("meta");
      description.name = "description";
      document.head.append(description);
    }
    description.content = guestConfig.app_description ?? `${guestConfig.app_name} 控制面板`;
  }, [authMode, guestConfig, session]);

  useEffect(() => {
    const current = guestConfig.theme ?? defaultThemeAppearance;
    const root = document.documentElement;
    root.style.setProperty("--theme-background", current.palette.background);
    root.style.setProperty("--theme-surface", current.palette.surface);
    root.style.setProperty("--theme-text", current.palette.text);
    root.style.setProperty("--theme-muted", current.palette.muted);
    root.style.setProperty("--theme-primary", current.palette.primary);
    root.style.setProperty("--theme-primary-text", current.palette.primary_text);
    root.style.setProperty("--theme-border", current.palette.border);
    root.style.setProperty("--theme-background-image", current.config.background_url === "" ? "none" : `url(${JSON.stringify(current.config.background_url)})`);
    root.dataset.themeFontScale = current.config.font_scale;
    root.dataset.themeRadius = current.config.radius;
    root.dataset.themeName = current.name;
    root.dataset.themeSidebarStyle = current.sidebar_style;
    root.dataset.themeHeaderStyle = current.header_style;
  }, [guestConfig.theme]);

  useEffect(() => {
    let active = true;
    const followHash = () => {
      const nextHash = window.location.hash;
      setAuthLocation(nextHash);
      const loginLink = loginLinkFromHash(nextHash);
      if (loginLink === null) return;
      const sequence = ++authenticationSequence.current;
      setLoading(true);
      setBootstrapAuthError("");
      void api.exchangeLoginLink(loginLink.token).then((nextSession) => {
        if (!active || sequence !== authenticationSequence.current) return;
        setSession(nextSession);
        setUserLanding(nextSession.redirect);
        window.history.replaceState(null, "", nextSession.is_admin ? "#/" : loginLinkLandingHash(nextSession.redirect));
        setAuthLocation(window.location.hash);
      }).catch((cause: unknown) => {
        if (!active || sequence !== authenticationSequence.current) return;
        setSession(null);
        window.history.replaceState(null, "", "#/login");
        setAuthLocation(window.location.hash);
        setBootstrapAuthError(cause instanceof Error ? cause.message : "登录链接无效或已过期");
      }).finally(() => {
        if (active && sequence === authenticationSequence.current) setLoading(false);
      });
    };
    window.addEventListener("hashchange", followHash);
    return () => {
      active = false;
      window.removeEventListener("hashchange", followHash);
    };
  }, [api]);

  const switchAuthMode = (mode: AuthMode) => {
    if (surface.kind === "admin" && mode === "register") return;
    setBootstrapAuthError("");
    window.history.replaceState(null, "", mode === "register" ? "#/register" : mode === "recover" ? "#/forgetpassword" : "#/login");
    setAuthLocation(window.location.hash);
  };

  const authenticated = (nextSession: UserSession) => {
    setBootstrapAuthError("");
    setSession(nextSession);
    window.history.replaceState(null, "", "#/");
    setAuthLocation(window.location.hash);
  };

  const identityChanged = (settings: SiteSettings) => {
    resetCaptchaProviderScripts();
    setGuestConfig((current) => ({
      ...current, app_name: settings.app_name, app_description: settings.app_description || null,
      app_url: settings.app_url || null, tos_url: settings.tos_url || null, logo: settings.logo || null,
      is_email_verify: settings.email_verify ? 1 : 0,
      is_invite_force: settings.invite_force ? 1 : 0,
      enable_coupon_system: settings.coupon_enabled ? 1 : 0,
      email_whitelist_suffix: settings.email_whitelist_enable ? settings.email_whitelist_suffix : 0,
      is_captcha: settings.captcha_enable ? 1 : 0,
      is_recaptcha: settings.captcha_enable ? 1 : 0,
      captcha_type: settings.captcha_type,
      recaptcha_site_key: settings.recaptcha_site_key || null,
      recaptcha_v3_site_key: settings.recaptcha_v3_site_key || null,
      recaptcha_v3_score_threshold: settings.recaptcha_v3_score_threshold,
      turnstile_site_key: settings.turnstile_site_key || null
    }));
  };

  const canLeaveAdminPage = () => {
    if ((page === "client-app" || clientAppSettingsDirty) && clientAppSettingsDirty) return window.confirm("客户端版本有未保存的修改，确认离开并放弃这些修改吗？");
    if (page === "themes" && themeSettingsDirty) return window.confirm("主题设置有未保存的修改，确认离开并放弃这些修改吗？");
    return true;
  };
  const refreshTheme = () => { void api.guestConfig().then(setGuestConfig).catch(() => undefined); };
  const navigateAdminPage = (nextPage: AdminPage) => {
    if (nextPage !== page && canLeaveAdminPage()) {
      window.localStorage.setItem("xboard-go-admin-page", nextPage);
      setPage(nextPage);
    }
  };
  const prefetchAdminPage = (target: AdminPage) => {
    void pageLoaders[target]?.();
    prefetchAdminAPI(target, api);
  };
  const signOut = () => {
    if (!canLeaveAdminPage()) return;
    void api.logout().catch(() => undefined).then(() => setSession(null));
  };

  if (loading) {
    return <div className="app-loading">正在加载 {guestConfig.app_name}…</div>;
  }
  if (session === null) {
    return <AuthPage api={api} config={guestConfig} mode={authMode} allowRegistration={surface.kind === "public"} initialError={bootstrapAuthError} onAuthenticated={authenticated} onModeChange={switchAuthMode} />;
  }
  if (surface.kind === "public") {
    if (session.is_distributor) {
      return <Suspense fallback={<div className="app-loading">正在加载分销面板…</div>}><DistributorPortal api={api} session={session} siteName={guestConfig.app_name} siteLogo={guestConfig.logo} initialPage={userLanding} onSignedOut={() => setSession(null)} /></Suspense>;
    }
    return <Suspense fallback={<div className="app-loading">正在加载用户面板…</div>}><UserPortal api={api} session={session} siteName={guestConfig.app_name} siteLogo={guestConfig.logo} couponEnabled={guestConfig.enable_coupon_system === 1} initialPage={userLanding} onSignedOut={() => setSession(null)} /></Suspense>;
  }
  if (!session.is_admin) {
    return <main className="login-shell"><section className="login-card"><h1>无权访问管理面板</h1><p className="muted">当前账号不具备管理员权限。</p><button className="button primary full" type="button" onClick={signOut}>退出登录</button></section></main>;
  }
  const isItemActive = (key: AdminPage) => {
    if (key === "settings") {
      return page === "settings" || page === "mail" || page === "telegram" || page === "client-app" || page === "commissions" || page === "subscriptions" || page === "node-settings";
    }
    return page === key;
  };

  return (
    <div className="app-frame">
      <TopProgressBar />
      <div className={`admin-layout ${sidebarCollapsed ? "sidebar-collapsed" : ""}`}>
        <nav className="admin-sidebar" aria-label="管理端导航">
          <div className="admin-sidebar-header">
            <div className="brand">
              <span className="brand-slash">//</span>
              {!sidebarCollapsed && <span className="brand-name">{guestConfig.app_name}</span>}
            </div>
          </div>
          <button
            type="button"
            className="sidebar-collapse-toggle"
            aria-label={sidebarCollapsed ? "展开侧边栏" : "折叠侧边栏"}
            onClick={() => setSidebarCollapsed((c) => !c)}
          >
            <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              {sidebarCollapsed ? <path d="m9 18 6-6-6-6" /> : <path d="m15 18-6-6 6-6" />}
            </svg>
          </button>
          <div className="admin-sidebar-content">
            <div className="admin-nav">
              <button
                className="nav-link nav-dashboard"
                aria-current={page === "system" ? "page" : undefined}
                onClick={() => navigateAdminPage("system")}
                onMouseEnter={() => prefetchAdminPage("system")}
                onFocus={() => prefetchAdminPage("system")}
                title="仪表盘"
              >
                <IconDashboard />
                {!sidebarCollapsed && <span>仪表盘</span>}
              </button>
              {adminNavGroups.map((group) => {
                const expanded = expandedAdminGroups[group.key] ?? true;
                return (
                  <section className="admin-nav-group" key={group.key}>
                    <button
                      className="admin-nav-group-toggle"
                      aria-expanded={expanded}
                      onClick={() => setExpandedAdminGroups((current) => ({ ...current, [group.key]: !expanded }))}
                      title={group.label}
                    >
                      <span className="group-left">
                        <GroupIcon kind={group.key} />
                        {!sidebarCollapsed && <span>{group.label}</span>}
                      </span>
                      {!sidebarCollapsed && (
                        <span className={`admin-nav-chevron ${expanded ? "open" : ""}`} aria-hidden="true">
                          <IconChevronRight />
                        </span>
                      )}
                    </button>
                    {expanded && !sidebarCollapsed && (
                      <div className="admin-nav-group-items">
                        {group.items.map(({ key, label }) => (
                          <button
                            key={key}
                            className="nav-link"
                            aria-current={isItemActive(key) ? "page" : undefined}
                            onClick={() => navigateAdminPage(key)}
                            onMouseEnter={() => prefetchAdminPage(key)}
                            onFocus={() => prefetchAdminPage(key)}
                          >
                            <span className="nav-item-icon" aria-hidden="true">
                              <ItemIcon page={key} />
                            </span>
                            <span>{label}</span>
                          </button>
                        ))}
                      </div>
                    )}
                  </section>
                );
              })}
            </div>
          </div>
        </nav>
        <div className="admin-main">
          <header className="topbar">
            <div className="topbar-search-wrap">
              <div className="topbar-search-box">
                <IconSearch />
                <input
                  type="text"
                  placeholder="搜索菜单和功能..."
                  aria-label="搜索菜单和功能"
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                />
                <kbd className="topbar-shortcut-badge">⌘K</kbd>
              </div>
              {searchResults.length > 0 && (
                <div className="topbar-search-results">
                  {searchResults.map((item) => (
                    <button
                      key={item.key}
                      type="button"
                      onClick={() => {
                        navigateAdminPage(item.key);
                        setSearchQuery("");
                      }}
                    >
                      <span>{item.label}</span>
                      <small className="muted">{item.group}</small>
                    </button>
                  ))}
                </div>
              )}
            </div>
            <div className="topbar-actions">
              <button
                type="button"
                className="topbar-icon-button"
                aria-label="切换明暗主题"
                onClick={toggleThemeMode}
              >
                <IconMoon />
              </button>
              <div className="topbar-lang-badge">
                <span>🇨🇳 CN</span>
                <IconChevronDown />
              </div>
              <div className="topbar-user-wrap">
                <button
                  type="button"
                  className="topbar-user-btn"
                  onClick={() => setUserMenuOpen((open) => !open)}
                  aria-expanded={userMenuOpen}
                >
                  <span className="user-avatar-circle">
                    {session.email.charAt(0).toUpperCase()}
                  </span>
                  <span className="user-email-text">{session.email}</span>
                  <IconChevronDown />
                </button>
                {userMenuOpen && (
                  <div className="user-dropdown-popover">
                    <div className="user-dropdown-header">
                      <strong>{session.email}</strong>
                      <small>{session.is_admin ? "超级管理员" : "管理人员"}</small>
                    </div>
                    <button
                      type="button"
                      onClick={() => {
                        setUserMenuOpen(false);
                        navigateAdminPage("account");
                      }}
                    >
                      <IconUser />
                      <span>账号安全</span>
                    </button>
                    <button
                      type="button"
                      className="danger"
                      onClick={() => {
                        setUserMenuOpen(false);
                        signOut();
                      }}
                    >
                      <IconLogOut />
                      <span>退出登录</span>
                    </button>
                  </div>
                )}
              </div>
              <button className="button ghost compact" onClick={signOut}>退出</button>
            </div>
          </header>
          <div className="admin-content">
            <Suspense fallback={<div className="app-loading">正在加载管理页面…</div>}>
              {page === "system" && <AdminDashboardPage api={api} />}
              {(page === "settings" || page === "mail" || page === "telegram" || page === "client-app" || page === "commissions" || page === "subscriptions" || page === "node-settings") && (
                <SystemConfigShell
                  api={api}
                  activeTab={
                    page === "mail" ? "mail" :
                    page === "telegram" ? "telegram" :
                    page === "client-app" ? "client-app" :
                    page === "commissions" ? "commissions" :
                    page === "subscriptions" ? "subscriptions" :
                    page === "node-settings" ? "node-settings" :
                    "site"
                  }
                  onIdentityChanged={identityChanged}
                  onBeforeTabChange={canLeaveAdminPage}
                  onSecurePathChanged={(nextPath) => window.location.replace(`/${nextPath}/#/`)}
                  onClientAppDirtyChange={setClientAppSettingsDirty}
                />
              )}
              {page === "themes" && <ThemeManagementPage api={api} onDirtyChange={setThemeSettingsDirty} onThemeChanged={refreshTheme} />}
              {page === "servers" && <ServerManagementPage api={api} />}
              {page === "nodes" && <NodeManagementPage api={api} />}
              {page === "plans" && <PlanManagementPage api={api} />}
              {page === "orders" && <OrderManagementPage api={api} />}
              {page === "distributors" && <AdminDistributorPage api={api} />}
              {page === "plugins" && <PluginManagementPage api={api} onNavigate={navigateAdminPage} />}
              {page === "payments" && <PaymentManagementPage api={api} />}
              {page === "coupons" && <CouponManagementPage api={api} />}
              {page === "gift-cards" && <GiftCardManagementPage api={api} />}
              {page === "users" && <UsersPage api={api} currentUserID={session.id} />}
              {page === "tickets" && <TicketManagementPage api={api} />}
              {page === "groups" && <ServerGroupsPage api={api} />}
              {page === "routes" && <RoutingRulesPage api={api} />}
              {page === "notices" && <NoticeManagementPage api={api} />}
              {page === "knowledge" && <KnowledgeManagementPage api={api} />}
              {page === "clients" && <ClientCatalogManagementPage api={api} />}
              {page === "account" && <AccountSecurityPage api={api} onSignedOut={() => setSession(null)} />}
            </Suspense>
            {page !== "distributors" && (
              <button
                className="floating-distributor-badge"
                type="button"
                onClick={() => navigateAdminPage("distributors")}
                title="进入分销管理"
              >
                <span className="distributor-badge-icon">分</span>
                <span className="distributor-badge-text">分销管理</span>
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function AuthPage({ api, config, mode, allowRegistration, initialError, onAuthenticated, onModeChange }: {
  api: APIClient;
  config: GuestConfig;
  mode: AuthMode;
  allowRegistration: boolean;
  initialError: string;
  onAuthenticated: (session: UserSession) => void;
  onModeChange: (mode: AuthMode) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [emailCode, setEmailCode] = useState("");
  const linkedInvitationCode = mode === "register" ? invitationCodeFromHash() : null;
  const [invitationCode, setInvitationCode] = useState("");
  const effectiveInvitationCode = linkedInvitationCode ?? invitationCode;
  const [submitting, setSubmitting] = useState(false);
  const [sendingCode, setSendingCode] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [message, setMessage] = useState("");
  const [resetComplete, setResetComplete] = useState(false);
  const [error, setError] = useState("");
  const visibleError = initialError !== "" ? initialError : error;
  const { requestCaptcha, challenge } = useCaptchaChallenge(config);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = window.setTimeout(() => setCooldown((current) => Math.max(0, current - 1)), 1_000);
    return () => window.clearTimeout(timer);
  }, [cooldown]);

  useEffect(() => {
    if (!resetComplete) return;
    const timer = window.setTimeout(() => {
      setResetComplete(false);
      setMessage("");
      setEmailCode("");
      setCooldown(0);
      setError("");
      onModeChange("login");
    }, 500);
    return () => window.clearTimeout(timer);
  }, [onModeChange, resetComplete]);

  const sendEmailCode = async () => {
    setError("");
    setMessage("");
    if (email.trim() === "") {
      setError("请输入邮箱");
      return;
    }
    setSendingCode(true);
    try {
      const captchaToken = await requestCaptcha("sendEmailVerify");
      if (mode === "register") {
        await api.requestRegistrationEmailVerification(email, captchaToken);
      } else {
        await api.requestPasswordReset(email, captchaToken);
      }
      setCooldown(60);
      setMessage("验证码已发送，请检查邮箱");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "验证码发送失败");
    } finally {
      setSendingCode(false);
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    setMessage("");
    try {
      if (mode === "register" || mode === "recover") {
        if (password !== confirmation) {
          setError("两次输入的密码不一致");
          return;
        }
      }
      if (mode === "register") {
        const captchaToken = await requestCaptcha("register");
        onAuthenticated(await api.register(email, password, confirmation, emailCode, effectiveInvitationCode, captchaToken));
      } else if (mode === "recover") {
        await api.resetPassword(email, emailCode, password);
        setMessage("重置密码成功,正在返回登录");
        setResetComplete(true);
      } else {
        onAuthenticated(await api.login(email, password));
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : (mode === "register" ? "注册失败" : mode === "recover" ? "重置密码失败" : "登录失败"));
    } finally {
      setSubmitting(false);
    }
  };

  return <>
    <main className="login-shell">
      <section className="login-card">
        <div className="brand large"><BrandMark appName={config.app_name} logo={config.logo} /><span>{config.app_name}</span></div>
        <h1>{mode === "register" ? "注册" : mode === "recover" ? "重置密码" : "登录"} {config.app_name}</h1>
        <p className="muted">{config.app_description ?? (mode === "register" ? "创建账号进入用户面板。" : mode === "recover" ? "使用邮箱验证码重置账号密码。" : "使用账号进入控制面板。")}</p>
        <form className="form-stack" onSubmit={(event) => void submit(event)}>
          <label>邮箱<input type="email" autoComplete="email" maxLength={320} value={email} required onChange={(event) => setEmail(event.target.value)} /></label>
          {mode === "register" && Array.isArray(config.email_whitelist_suffix) && config.email_whitelist_suffix.length > 0 &&
            <p className="small muted registration-domain-hint">允许邮箱后缀：{config.email_whitelist_suffix.join("、")}</p>}
          {(mode === "recover" || (mode === "register" && config.is_email_verify === 1)) && <div className="verification-field-row"><label>邮箱验证码<input autoComplete="one-time-code" inputMode="numeric" pattern="[0-9]{6}" minLength={6} maxLength={6} value={emailCode} required onChange={(event) => setEmailCode(event.target.value.replace(/\D/g, "").slice(0, 6))} /></label><button className="button secondary" type="button" disabled={sendingCode || cooldown > 0 || resetComplete} onClick={() => void sendEmailCode()}>{sendingCode ? "正在发送…" : cooldown > 0 ? `${cooldown} 秒` : "发送"}</button></div>}
          {mode === "register" && <label>邀请码<input aria-label="邀请码" placeholder={config.is_invite_force === 1 ? "邀请码,（必填）" : "邀请码,（选填）"} autoComplete="off" maxLength={20} value={effectiveInvitationCode} required={config.is_invite_force === 1} disabled={linkedInvitationCode !== null} onChange={(event) => setInvitationCode(event.target.value)} /></label>}
          <label>密码<input type="password" autoComplete={mode === "login" ? "current-password" : "new-password"} minLength={mode === "login" ? undefined : 8} maxLength={1024} value={password} required onChange={(event) => setPassword(event.target.value)} /></label>
          {(mode === "register" || mode === "recover") && <label>再次输入密码<input type="password" autoComplete="new-password" minLength={8} maxLength={1024} value={confirmation} required onChange={(event) => setConfirmation(event.target.value)} /></label>}
          {visibleError !== "" && <div className="alert error" role="alert">{visibleError}</div>}
          {message !== "" && <div className="alert success" role="status">{message}</div>}
          <button className="button primary full" type="submit" disabled={submitting || sendingCode || resetComplete}>{submitting ? (mode === "register" ? "正在注册…" : mode === "recover" ? "正在重置…" : "正在登录…") : (mode === "register" ? "注册" : mode === "recover" ? "重置密码" : "登录")}</button>
        </form>
        {mode === "login" && <button className="button ghost full auth-mode-switch" type="button" disabled={submitting} onClick={() => {
          setError(""); setMessage(""); setEmailCode(""); setInvitationCode(""); setCooldown(0); setResetComplete(false); onModeChange("recover");
        }}>忘记密码</button>}
        {(allowRegistration || mode !== "login") && <button className="button ghost full auth-mode-switch" type="button" disabled={submitting || sendingCode} onClick={() => {
          setError("");
          setMessage("");
          setEmailCode("");
          setInvitationCode("");
          setCooldown(0);
          setResetComplete(false);
          onModeChange(mode === "login" ? "register" : "login");
        }}>{mode === "login" ? "注册账号" : "返回登入"}</button>}
        {config.tos_url !== null && <p className="login-terms"><a href={config.tos_url} target="_blank" rel="noreferrer noopener">用户条款</a></p>}
      </section>
    </main>
    {challenge}
  </>;
}

function authModeFromHash(hash = window.location.hash): AuthMode {
  if (hash.startsWith("#/register")) return "register";
  if (hash.startsWith("#/forgetpassword")) return "recover";
  return "login";
}

function invitationCodeFromHash(): string | null {
  const queryIndex = window.location.hash.indexOf("?");
  if (queryIndex < 0) return null;
  const code = new URLSearchParams(window.location.hash.slice(queryIndex + 1)).get("code");
  return code === null || code === "" ? null : code;
}

function loginLinkFromHash(hash = window.location.hash): { token: string } | null {
  if (!hash.startsWith("#/login?")) return null;
  const token = new URLSearchParams(hash.slice(hash.indexOf("?") + 1)).get("verify");
  return token === null || token === "" ? null : { token };
}

function loginLinkLandingHash(redirect: LoginLinkRedirect): string {
  return redirect === "dashboard" ? "#/" : `#/${redirect}`;
}

function loginLandingFromHash(hash = window.location.hash): LoginLinkRedirect {
  const route = hash.slice(2).split("?", 1)[0];
  switch (route) {
    case "invite":
    case "knowledge":
    case "ticket":
    case "subscribe":
      return route;
    default:
      return "dashboard";
  }
}
