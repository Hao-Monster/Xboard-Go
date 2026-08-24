import { lazy, Suspense, useEffect, useState, type FormEvent } from "react";

import { AccountSecurityPage } from "./features/account/AccountSecurityPage";
import { RoutingRulesPage } from "./features/admin/RoutingRulesPage";
import { UsersPage } from "./features/users/UsersPage";
import { ServerGroupsPage } from "./features/admin/ServerGroupsPage";
import { ServerManagementPage } from "./features/servers/ServerManagementPage";
import { APIClient, type GuestConfig, type SiteSettings, type UserSession } from "./lib/api";
import { NoticeManagementPage } from "./features/notices/NoticeManagementPage";
import { ClientCatalogManagementPage } from "./features/clients/ClientCatalogManagementPage";
import { KnowledgeManagementPage } from "./features/knowledge/KnowledgeManagementPage";
import { TicketManagementPage } from "./features/tickets/TicketManagementPage";
import { SystemOperationsPage } from "./features/system/SystemOperationsPage";
import { SiteSettingsPage } from "./features/settings/SiteSettingsPage";
import { BrandMark } from "./components/BrandMark";

const api = new APIClient();
const UserPortal = lazy(async () => import("./features/user/UserPortal").then((module) => ({ default: module.UserPortal })));
const defaultGuestConfig: GuestConfig = {
  app_name: "Xboard-Go", app_description: null, app_url: null, tos_url: null, logo: null,
  is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
  captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
  recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
};

export function App() {
  const [session, setSession] = useState<UserSession | null>(null);
  const [guestConfig, setGuestConfig] = useState<GuestConfig>(defaultGuestConfig);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState<"system" | "settings" | "servers" | "users" | "tickets" | "groups" | "routes" | "notices" | "knowledge" | "clients" | "account">("servers");

  useEffect(() => {
    let active = true;
    void api.guestConfig().then((config) => {
      if (active) setGuestConfig(config);
    }).catch(() => undefined);
    void api.session().then((nextSession) => {
      if (active) setSession(nextSession);
    }).catch(() => {
      if (active) setSession(null);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    document.title = `${guestConfig.app_name} 控制面板`;
    let description = document.querySelector<HTMLMetaElement>('meta[name="description"]');
    if (description === null) {
      description = document.createElement("meta");
      description.name = "description";
      document.head.append(description);
    }
    description.content = guestConfig.app_description ?? `${guestConfig.app_name} 控制面板`;
  }, [guestConfig]);

  const identityChanged = (settings: SiteSettings) => {
    setGuestConfig((current) => ({
      ...current, app_name: settings.app_name, app_description: settings.app_description || null,
      app_url: settings.app_url || null, tos_url: settings.tos_url || null, logo: settings.logo || null
    }));
  };

  if (loading) {
    return <div className="app-loading">正在加载 {guestConfig.app_name}…</div>;
  }
  if (session === null) {
    return <LoginPage config={guestConfig} onLogin={setSession} />;
  }
  if (!session.is_admin) {
    return <Suspense fallback={<div className="app-loading">正在加载用户面板…</div>}><UserPortal api={api} session={session} siteName={guestConfig.app_name} siteLogo={guestConfig.logo} onSignedOut={() => setSession(null)} /></Suspense>;
  }
  return (
    <div className="app-frame">
      <nav className="topbar" aria-label="管理端导航">
        <div className="brand"><span className="brand-mark">X</span><span>{guestConfig.app_name}</span></div>
        <div className="admin-nav">
          <button className="nav-link" aria-current={page === "system" ? "page" : undefined} onClick={() => setPage("system")}>系统状态</button>
          <button className="nav-link" aria-current={page === "settings" ? "page" : undefined} onClick={() => setPage("settings")}>系统设置</button>
          <button className="nav-link" aria-current={page === "servers" ? "page" : undefined} onClick={() => setPage("servers")}>服务器管理</button>
          <button className="nav-link" aria-current={page === "users" ? "page" : undefined} onClick={() => setPage("users")}>用户管理</button>
          <button className="nav-link" aria-current={page === "tickets" ? "page" : undefined} onClick={() => setPage("tickets")}>工单管理</button>
          <button className="nav-link" aria-current={page === "groups" ? "page" : undefined} onClick={() => setPage("groups")}>权限组</button>
          <button className="nav-link" aria-current={page === "routes" ? "page" : undefined} onClick={() => setPage("routes")}>路由规则</button>
          <button className="nav-link" aria-current={page === "notices" ? "page" : undefined} onClick={() => setPage("notices")}>公告管理</button>
          <button className="nav-link" aria-current={page === "knowledge" ? "page" : undefined} onClick={() => setPage("knowledge")}>知识库管理</button>
          <button className="nav-link" aria-current={page === "clients" ? "page" : undefined} onClick={() => setPage("clients")}>客户端管理</button>
          <button className="nav-link" aria-current={page === "account" ? "page" : undefined} onClick={() => setPage("account")}>账号安全</button>
        </div>
        <div className="account">
          <span>{session.email}</span>
          <button className="button ghost compact" onClick={() => void api.logout().catch(() => undefined).then(() => setSession(null))}>退出</button>
        </div>
      </nav>
      {page === "system" && <SystemOperationsPage api={api} />}
      {page === "settings" && <SiteSettingsPage api={api} onIdentityChanged={identityChanged} />}
      {page === "servers" && <ServerManagementPage api={api} />}
      {page === "users" && <UsersPage api={api} currentUserID={session.id} />}
      {page === "tickets" && <TicketManagementPage api={api} />}
      {page === "groups" && <ServerGroupsPage api={api} />}
      {page === "routes" && <RoutingRulesPage api={api} />}
      {page === "notices" && <NoticeManagementPage api={api} />}
      {page === "knowledge" && <KnowledgeManagementPage api={api} />}
      {page === "clients" && <ClientCatalogManagementPage api={api} />}
      {page === "account" && <AccountSecurityPage api={api} onSignedOut={() => setSession(null)} />}
    </div>
  );
}

function LoginPage({ config, onLogin }: { config: GuestConfig; onLogin: (session: UserSession) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      onLogin(await api.login(email, password));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "登录失败");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="login-shell">
      <section className="login-card">
        <div className="brand large"><BrandMark appName={config.app_name} logo={config.logo} /><span>{config.app_name}</span></div>
        <h1>登录 {config.app_name}</h1>
        <p className="muted">{config.app_description ?? "使用账号进入控制面板。"}</p>
        <form className="form-stack" onSubmit={(event) => void submit(event)}>
          <label>邮箱<input type="email" autoComplete="username" value={email} required onChange={(event) => setEmail(event.target.value)} /></label>
          <label>密码<input type="password" autoComplete="current-password" value={password} required onChange={(event) => setPassword(event.target.value)} /></label>
          {error !== "" && <div className="alert error" role="alert">{error}</div>}
          <button className="button primary full" type="submit" disabled={submitting}>{submitting ? "正在登录…" : "登录"}</button>
        </form>
        {config.tos_url !== null && <p className="login-terms"><a href={config.tos_url} target="_blank" rel="noreferrer noopener">用户条款</a></p>}
      </section>
    </main>
  );
}
