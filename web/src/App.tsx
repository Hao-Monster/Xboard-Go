import { lazy, Suspense, useEffect, useRef, useState, type FormEvent } from "react";

import { AccountSecurityPage } from "./features/account/AccountSecurityPage";
import { RoutingRulesPage } from "./features/admin/RoutingRulesPage";
import { UsersPage } from "./features/users/UsersPage";
import { ServerGroupsPage } from "./features/admin/ServerGroupsPage";
import { ServerManagementPage } from "./features/servers/ServerManagementPage";
import { APIClient, type GuestConfig, type LoginLinkRedirect, type SiteSettings, type UserSession } from "./lib/api";
import { NoticeManagementPage } from "./features/notices/NoticeManagementPage";
import { ClientCatalogManagementPage } from "./features/clients/ClientCatalogManagementPage";
import { KnowledgeManagementPage } from "./features/knowledge/KnowledgeManagementPage";
import { TicketManagementPage } from "./features/tickets/TicketManagementPage";
import { SystemOperationsPage } from "./features/system/SystemOperationsPage";
import { SiteSettingsPage } from "./features/settings/SiteSettingsPage";
import { SubscriptionSettingsPage } from "./features/settings/SubscriptionSettingsPage";
import { resetCaptchaProviderScripts, useCaptchaChallenge } from "./features/auth/CaptchaChallenge";
import { BrandMark } from "./components/BrandMark";
import { OrderManagementPage } from "./features/orders/OrderManagementPage";
import { CouponManagementPage } from "./features/coupons/CouponManagementPage";

const api = new APIClient();
const PlanManagementPage = lazy(async () => import("./features/plans/PlanManagementPage").then((module) => ({ default: module.PlanManagementPage })));
const PaymentManagementPage = lazy(async () => import("./features/payments/PaymentManagementPage").then((module) => ({ default: module.PaymentManagementPage })));
const GiftCardManagementPage = lazy(async () => import("./features/giftcards/GiftCardManagementPage").then((module) => ({ default: module.GiftCardManagementPage })));
const UserPortal = lazy(async () => import("./features/user/UserPortal").then((module) => ({ default: module.UserPortal })));
const defaultGuestConfig: GuestConfig = {
  app_name: "Xboard-Go", app_description: null, app_url: null, tos_url: null, logo: null,
  is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
  captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
  recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
};
type AuthMode = "login" | "register" | "recover";

export function App() {
  const [session, setSession] = useState<UserSession | null>(null);
  const [guestConfig, setGuestConfig] = useState<GuestConfig>(defaultGuestConfig);
  const [loading, setLoading] = useState(true);
  const [bootstrapAuthError, setBootstrapAuthError] = useState("");
  const [userLanding, setUserLanding] = useState<LoginLinkRedirect>(() => loginLandingFromHash());
  const [authLocation, setAuthLocation] = useState(() => window.location.hash);
  const authMode = authModeFromHash(authLocation);
  const [page, setPage] = useState<"system" | "settings" | "subscriptions" | "servers" | "plans" | "orders" | "payments" | "coupons" | "gift-cards" | "users" | "tickets" | "groups" | "routes" | "notices" | "knowledge" | "clients" | "account">("servers");
  const authenticationSequence = useRef(0);

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
      setSession({ id: nextSession.id, email: nextSession.email, is_admin: nextSession.is_admin });
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
        setSession({ id: nextSession.id, email: nextSession.email, is_admin: nextSession.is_admin });
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
  }, []);

  const switchAuthMode = (mode: AuthMode) => {
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

  if (loading) {
    return <div className="app-loading">正在加载 {guestConfig.app_name}…</div>;
  }
  if (session === null) {
    return <AuthPage config={guestConfig} mode={authMode} initialError={bootstrapAuthError} onAuthenticated={authenticated} onModeChange={switchAuthMode} />;
  }
  if (!session.is_admin) {
    return <Suspense fallback={<div className="app-loading">正在加载用户面板…</div>}><UserPortal api={api} session={session} siteName={guestConfig.app_name} siteLogo={guestConfig.logo} couponEnabled={guestConfig.enable_coupon_system === 1} initialPage={userLanding} onSignedOut={() => setSession(null)} /></Suspense>;
  }
  return (
    <div className="app-frame">
      <nav className="topbar" aria-label="管理端导航">
        <div className="brand"><span className="brand-mark">X</span><span>{guestConfig.app_name}</span></div>
        <div className="admin-nav">
          <button className="nav-link" aria-current={page === "system" ? "page" : undefined} onClick={() => setPage("system")}>系统状态</button>
          <button className="nav-link" aria-current={page === "settings" ? "page" : undefined} onClick={() => setPage("settings")}>系统设置</button>
          <button className="nav-link" aria-current={page === "subscriptions" ? "page" : undefined} onClick={() => setPage("subscriptions")}>订阅设置</button>
          <button className="nav-link" aria-current={page === "servers" ? "page" : undefined} onClick={() => setPage("servers")}>服务器管理</button>
          <button className="nav-link" aria-current={page === "plans" ? "page" : undefined} onClick={() => setPage("plans")}>套餐管理</button>
          <button className="nav-link" aria-current={page === "orders" ? "page" : undefined} onClick={() => setPage("orders")}>订单管理</button>
          <button className="nav-link" aria-current={page === "payments" ? "page" : undefined} onClick={() => setPage("payments")}>支付配置</button>
          <button className="nav-link" aria-current={page === "coupons" ? "page" : undefined} onClick={() => setPage("coupons")}>优惠券管理</button>
          <button className="nav-link" aria-current={page === "gift-cards" ? "page" : undefined} onClick={() => setPage("gift-cards")}>礼品卡管理</button>
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
      {page === "subscriptions" && <SubscriptionSettingsPage api={api} />}
      {page === "servers" && <ServerManagementPage api={api} />}
      {page === "plans" && <PlanManagementPage api={api} />}
      {page === "orders" && <OrderManagementPage api={api} />}
      {page === "payments" && <PaymentManagementPage api={api} />}
      {page === "coupons" && <CouponManagementPage api={api} />}
      {page === "gift-cards" && <Suspense fallback={<div className="app-loading">正在加载礼品卡管理…</div>}><GiftCardManagementPage api={api} /></Suspense>}
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

function AuthPage({ config, mode, initialError, onAuthenticated, onModeChange }: {
  config: GuestConfig;
  mode: AuthMode;
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
        <button className="button ghost full auth-mode-switch" type="button" disabled={submitting || sendingCode} onClick={() => {
          setError("");
          setMessage("");
          setEmailCode("");
          setInvitationCode("");
          setCooldown(0);
          setResetComplete(false);
          onModeChange(mode === "login" ? "register" : "login");
        }}>{mode === "login" ? "注册账号" : "返回登入"}</button>
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
