import { lazy, Suspense, useEffect, useState, type FormEvent } from "react";

import { AccountSecurityPage } from "./features/account/AccountSecurityPage";
import { RoutingRulesPage } from "./features/admin/RoutingRulesPage";
import { UsersPage } from "./features/users/UsersPage";
import { ServerGroupsPage } from "./features/admin/ServerGroupsPage";
import { ServerManagementPage } from "./features/servers/ServerManagementPage";
import { APIClient, type UserSession } from "./lib/api";
import { NoticeManagementPage } from "./features/notices/NoticeManagementPage";
import { ClientCatalogManagementPage } from "./features/clients/ClientCatalogManagementPage";
import { KnowledgeManagementPage } from "./features/knowledge/KnowledgeManagementPage";

const api = new APIClient();
const UserPortal = lazy(async () => import("./features/user/UserPortal").then((module) => ({ default: module.UserPortal })));

export function App() {
  const [session, setSession] = useState<UserSession | null>(null);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState<"servers" | "users" | "groups" | "routes" | "notices" | "knowledge" | "clients" | "account">("servers");

  useEffect(() => {
    void api.session().then(setSession).catch(() => setSession(null)).finally(() => setLoading(false));
  }, []);

  if (loading) {
    return <div className="app-loading">正在加载 Xboard-Go…</div>;
  }
  if (session === null) {
    return <LoginPage onLogin={setSession} />;
  }
  if (!session.is_admin) {
    return <Suspense fallback={<div className="app-loading">正在加载用户面板…</div>}><UserPortal api={api} session={session} onSignedOut={() => setSession(null)} /></Suspense>;
  }
  return (
    <div className="app-frame">
      <nav className="topbar" aria-label="管理端导航">
        <div className="brand"><span className="brand-mark">X</span><span>Xboard-Go</span></div>
        <div className="admin-nav">
          <button className="nav-link" aria-current={page === "servers" ? "page" : undefined} onClick={() => setPage("servers")}>服务器管理</button>
          <button className="nav-link" aria-current={page === "users" ? "page" : undefined} onClick={() => setPage("users")}>用户管理</button>
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
      {page === "servers" && <ServerManagementPage api={api} />}
      {page === "users" && <UsersPage api={api} currentUserID={session.id} />}
      {page === "groups" && <ServerGroupsPage api={api} />}
      {page === "routes" && <RoutingRulesPage api={api} />}
      {page === "notices" && <NoticeManagementPage api={api} />}
      {page === "knowledge" && <KnowledgeManagementPage api={api} />}
      {page === "clients" && <ClientCatalogManagementPage api={api} />}
      {page === "account" && <AccountSecurityPage api={api} onSignedOut={() => setSession(null)} />}
    </div>
  );
}

function LoginPage({ onLogin }: { onLogin: (session: UserSession) => void }) {
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
        <div className="brand large"><span className="brand-mark">X</span><span>Xboard-Go</span></div>
        <h1>登录 Xboard-Go</h1>
        <p className="muted">使用账号进入控制面板。</p>
        <form className="form-stack" onSubmit={(event) => void submit(event)}>
          <label>邮箱<input type="email" autoComplete="username" value={email} required onChange={(event) => setEmail(event.target.value)} /></label>
          <label>密码<input type="password" autoComplete="current-password" value={password} required onChange={(event) => setPassword(event.target.value)} /></label>
          {error !== "" && <div className="alert error" role="alert">{error}</div>}
          <button className="button primary full" type="submit" disabled={submitting}>{submitting ? "正在登录…" : "登录"}</button>
        </form>
      </section>
    </main>
  );
}
