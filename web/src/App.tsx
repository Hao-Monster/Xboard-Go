import { lazy, Suspense, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { APIClient, type GuestConfig, type LoginLinkRedirect, type SiteSettings, type ThemeAppearance, type UserSession } from "./lib/api";
import { resetCaptchaProviderScripts, useCaptchaChallenge } from "./features/auth/CaptchaChallenge";
import { BrandMark } from "./components/BrandMark";

import type { SystemConfigTab } from "./features/settings/SystemConfigShell";
const SystemConfigShell = lazy(async () => import("./features/settings/SystemConfigShell").then(module => ({ default: module.SystemConfigShell })));

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
const SystemOperationsPage = lazy(async () => import("./features/system/SystemOperationsPage").then((module) => ({ default: module.SystemOperationsPage })));
const TicketManagementPage = lazy(async () => import("./features/tickets/TicketManagementPage").then((module) => ({ default: module.TicketManagementPage })));
const UsersPage = lazy(async () => import("./features/users/UsersPage").then((module) => ({ default: module.UsersPage })));
const NodeManagementPage = lazy(async () => import("./features/nodes/NodeManagementPage").then((module) => ({ default: module.NodeManagementPage })));
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
type AdminPage = "security" | "templates" | "system" | "settings" | "themes" | "mail" | "telegram" | "client-app" | "commissions" | "subscriptions" | "node-settings" | "servers" | "nodes" | "plans" | "orders" | "distributors" | "plugins" | "payments" | "coupons" | "gift-cards" | "users" | "tickets" | "groups" | "routes" | "notices" | "knowledge" | "clients" | "account";

type NavGroup = {
  id: string;
  title: string;
  items: { page: AdminPage; label: string }[];
};

const adminNavGroups: NavGroup[] = [
  {
    id: "system",
    title: "系统管理",
    items: [
      { page: "settings", label: "系统配置" },
      { page: "plugins", label: "插件管理" },
      { page: "themes", label: "主题配置" },
      { page: "notices", label: "公告管理" },
      { page: "payments", label: "支付配置" },
      { page: "knowledge", label: "知识库管理" },
      { page: "clients", label: "客户端管理" },
    ],
  },
  {
    id: "nodes",
    title: "节点管理",
    items: [
      { page: "servers", label: "服务器管理" },
      { page: "nodes", label: "节点管理" },
      { page: "groups", label: "权限组管理" },
      { page: "routes", label: "路由管理" },
    ],
  },
  {
    id: "finance",
    title: "订阅管理",
    items: [
      { page: "plans", label: "套餐管理" },
      { page: "orders", label: "订单管理" },
      { page: "distributors", label: "分销管理" },
      { page: "coupons", label: "优惠券管理" },
      { page: "gift-cards", label: "礼品卡管理" },
    ],
  },
  {
    id: "users",
    title: "用户管理",
    items: [
      { page: "users", label: "用户管理" },
      { page: "tickets", label: "工单管理" },
    ],
  },

];

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
  const [machineNodeTarget, setMachineNodeTarget] = useState<{ id: number; create: boolean } | null>(null);
  const [page, setPage] = useState<AdminPage>("servers");
  const [expandedAdminGroups, setExpandedAdminGroups] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(adminNavGroups.map((g) => [g.id, true]))
  );
  const [clientAppSettingsDirty, setClientAppSettingsDirty] = useState(false);
  const [themeSettingsDirty, setThemeSettingsDirty] = useState(false);
  const authenticationSequence = useRef(0);

  const toggleGroup = (groupId: string) => {
    setExpandedAdminGroups((prev) => ({
      ...prev,
      [groupId]: !prev[groupId],
    }));
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
    setPage("servers");
    window.scrollTo(0, 0);
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
    if (page === "client-app" && clientAppSettingsDirty) return window.confirm("客户端版本有未保存的修改，确认离开并放弃这些修改吗？");
    if (page === "themes" && themeSettingsDirty) return window.confirm("主题设置有未保存的修改，确认离开并放弃这些修改吗？");
    return true;
  };
  const refreshTheme = () => { void api.guestConfig().then(setGuestConfig).catch(() => undefined); };
  const navigateAdminPage = (nextPage: AdminPage) => {
    if (nextPage !== page && canLeaveAdminPage()) { setMachineNodeTarget(null); setPage(nextPage); }
  };
  const signOut = () => {
    if (!canLeaveAdminPage()) return;
    void api.logout().catch(() => undefined).then(() => {
      setPage("servers");
      window.scrollTo(0, 0);
      setSession(null);
    });
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
  const configPages: Partial<Record<AdminPage, SystemConfigTab>> = {
    settings: "site", security: "security", subscriptions: "subscriptions", commissions: "commissions",
    "node-settings": "node-settings", mail: "mail", telegram: "telegram", "client-app": "client-app", templates: "templates"
  };
  const activeConfigTab = configPages[page];
  const configDestinations: Record<SystemConfigTab, AdminPage> = {
    site: "settings", security: "security", subscriptions: "subscriptions", commissions: "commissions",
    "node-settings": "node-settings", mail: "mail", telegram: "telegram", "client-app": "client-app", templates: "templates"
  };

  return (
    <div className="app-frame">
      <header className="topbar">
        <div className="brand"><span className="brand-mark">X</span><span>{guestConfig.app_name}</span></div>
        <div className="account">
          <details className="admin-account-menu"><summary>{session.email}</summary><button type="button" className="button secondary compact" onClick={() => navigateAdminPage("account")}>账号安全</button></details>
          <button className="button ghost compact" onClick={signOut}>退出</button>
        </div>
      </header>
      <div className="admin-layout">
        <nav className="admin-sidebar" aria-label="管理端导航">
          <div className="admin-nav">
            <button className="nav-link" aria-current={page === "system" ? "page" : undefined} onClick={() => navigateAdminPage("system")}>仪表盘</button>
            {adminNavGroups.map((group) => {
              const isExpanded = expandedAdminGroups[group.id] ?? true;
              return (
                <div key={group.id} className="nav-group" role="group" aria-label={group.title}>
                  <button
                    type="button"
                    className="nav-group-header"
                    aria-label={`${group.title} 菜单`}
                    onClick={() => toggleGroup(group.id)}
                    aria-expanded={isExpanded}
                    aria-controls={`admin-group-${group.id}`}
                  >
                    <span className="nav-group-title">{group.title}</span>
                    <span className={`nav-group-chevron ${isExpanded ? "open" : ""}`} aria-hidden="true">
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                        <polyline points="6 9 12 15 18 9"></polyline>
                      </svg>
                    </span>
                  </button>
                  {isExpanded && (
                    <div className="nav-group-items" id={`admin-group-${group.id}`}>
                      {group.items.map((item) => {
                        const isActive = page === item.page || (item.page === "settings" && activeConfigTab !== undefined);
                        return (
                          <button
                            key={item.page}
                            className={`nav-link ${isActive ? "active" : ""}`}
                            aria-current={isActive ? "page" : undefined}
                            onClick={() => navigateAdminPage(item.page)}
                          >
                            {item.label}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </nav>
        <div className="admin-content">
          <Suspense fallback={<div className="app-loading">正在加载管理页面…</div>}>
            {activeConfigTab !== undefined && <SystemConfigShell
              api={api} activeTab={activeConfigTab}
              onTabChange={(tab) => navigateAdminPage(configDestinations[tab])}
              onIdentityChanged={identityChanged}
              onSecurePathChanged={(nextPath) => window.location.replace(`/${nextPath}/#/`)}
              onClientAppDirtyChange={setClientAppSettingsDirty}
            />}
            {page === "system" && <SystemOperationsPage api={api} />}
            {page === "themes" && <ThemeManagementPage api={api} onDirtyChange={setThemeSettingsDirty} onThemeChanged={refreshTheme} />}
            {page === "servers" && <ServerManagementPage api={api} onNavigateNodes={(id, create) => { setMachineNodeTarget({ id, create }); setPage("nodes"); }} />}
            {page === "nodes" && <NodeManagementPage api={api} initialMachineID={machineNodeTarget?.id} initiallyCreating={machineNodeTarget?.create} />}
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
            {page === "account" && <AccountSecurityPage api={api} onSignedOut={() => { setPage("servers"); window.scrollTo(0, 0); setSession(null); }} />}
          </Suspense>
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
