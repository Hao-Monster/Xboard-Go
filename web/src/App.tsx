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
type AuthMode = "login" | "register" | "recover";

export function App() {
  const [session, setSession] = useState<UserSession | null>(null);
  const [guestConfig, setGuestConfig] = useState<GuestConfig>(defaultGuestConfig);
  const [loading, setLoading] = useState(true);
  const [authMode, setAuthMode] = useState<AuthMode>(() => authModeFromHash());
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
    const followHash = () => setAuthMode(authModeFromHash());
    window.addEventListener("hashchange", followHash);
    return () => window.removeEventListener("hashchange", followHash);
  }, []);

  const switchAuthMode = (mode: AuthMode) => {
    setAuthMode(mode);
    window.history.replaceState(null, "", mode === "register" ? "#/register" : mode === "recover" ? "#/forgetpassword" : "#/login");
  };

  const authenticated = (nextSession: UserSession) => {
    setSession(nextSession);
    setAuthMode("login");
    window.history.replaceState(null, "", "#/");
  };

  const identityChanged = (settings: SiteSettings) => {
    setGuestConfig((current) => ({
      ...current, app_name: settings.app_name, app_description: settings.app_description || null,
      app_url: settings.app_url || null, tos_url: settings.tos_url || null, logo: settings.logo || null,
      email_whitelist_suffix: settings.email_whitelist_enable ? settings.email_whitelist_suffix : 0
    }));
  };

  if (loading) {
    return <div className="app-loading">正在加载 {guestConfig.app_name}…</div>;
  }
  if (session === null) {
    return <AuthPage config={guestConfig} mode={authMode} onAuthenticated={authenticated} onModeChange={switchAuthMode} />;
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

function AuthPage({ config, mode, onAuthenticated, onModeChange }: {
  config: GuestConfig;
  mode: AuthMode;
  onAuthenticated: (session: UserSession) => void;
  onModeChange: (mode: AuthMode) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [emailCode, setEmailCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [sendingCode, setSendingCode] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [message, setMessage] = useState("");
  const [resetComplete, setResetComplete] = useState(false);
  const [error, setError] = useState("");

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
      onModeChange("login");
    }, 500);
    return () => window.clearTimeout(timer);
  }, [onModeChange, resetComplete]);

  const sendPasswordResetCode = async () => {
    setError("");
    setMessage("");
    if (email.trim() === "") {
      setError("请输入邮箱");
      return;
    }
    setSendingCode(true);
    try {
      await api.requestPasswordReset(email);
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
        onAuthenticated(await api.register(email, password, confirmation));
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

  return (
    <main className="login-shell">
      <section className="login-card">
        <div className="brand large"><BrandMark appName={config.app_name} logo={config.logo} /><span>{config.app_name}</span></div>
        <h1>{mode === "register" ? "注册" : mode === "recover" ? "重置密码" : "登录"} {config.app_name}</h1>
        <p className="muted">{config.app_description ?? (mode === "register" ? "创建账号进入用户面板。" : mode === "recover" ? "使用邮箱验证码重置账号密码。" : "使用账号进入控制面板。")}</p>
        <form className="form-stack" onSubmit={(event) => void submit(event)}>
          <label>邮箱<input type="email" autoComplete="email" maxLength={320} value={email} required onChange={(event) => setEmail(event.target.value)} /></label>
          {mode === "register" && Array.isArray(config.email_whitelist_suffix) && config.email_whitelist_suffix.length > 0 &&
            <p className="small muted registration-domain-hint">允许邮箱后缀：{config.email_whitelist_suffix.join("、")}</p>}
          {mode === "recover" && <div className="verification-field-row"><label>邮箱验证码<input autoComplete="one-time-code" inputMode="numeric" pattern="[0-9]{6}" minLength={6} maxLength={6} value={emailCode} required onChange={(event) => setEmailCode(event.target.value.replace(/\D/g, "").slice(0, 6))} /></label><button className="button secondary" type="button" disabled={sendingCode || cooldown > 0 || resetComplete} onClick={() => void sendPasswordResetCode()}>{sendingCode ? "正在发送…" : cooldown > 0 ? `${cooldown} 秒` : "发送"}</button></div>}
          <label>密码<input type="password" autoComplete={mode === "login" ? "current-password" : "new-password"} minLength={mode === "login" ? undefined : 8} maxLength={1024} value={password} required onChange={(event) => setPassword(event.target.value)} /></label>
          {(mode === "register" || mode === "recover") && <label>再次输入密码<input type="password" autoComplete="new-password" minLength={8} maxLength={1024} value={confirmation} required onChange={(event) => setConfirmation(event.target.value)} /></label>}
          {error !== "" && <div className="alert error" role="alert">{error}</div>}
          {message !== "" && <div className="alert success" role="status">{message}</div>}
          <button className="button primary full" type="submit" disabled={submitting || resetComplete}>{submitting ? (mode === "register" ? "正在注册…" : mode === "recover" ? "正在重置…" : "正在登录…") : (mode === "register" ? "注册" : mode === "recover" ? "重置密码" : "登录")}</button>
        </form>
        {mode === "login" && <button className="button ghost full auth-mode-switch" type="button" disabled={submitting} onClick={() => {
          setError(""); setMessage(""); setResetComplete(false); onModeChange("recover");
        }}>忘记密码</button>}
        <button className="button ghost full auth-mode-switch" type="button" disabled={submitting || sendingCode} onClick={() => {
          setError("");
          setMessage("");
          setResetComplete(false);
          onModeChange(mode === "login" ? "register" : "login");
        }}>{mode === "login" ? "注册账号" : "返回登入"}</button>
        {config.tos_url !== null && <p className="login-terms"><a href={config.tos_url} target="_blank" rel="noreferrer noopener">用户条款</a></p>}
      </section>
    </main>
  );
}

function authModeFromHash(): AuthMode {
  if (window.location.hash.startsWith("#/register")) return "register";
  if (window.location.hash.startsWith("#/forgetpassword")) return "recover";
  return "login";
}
