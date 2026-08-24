import { useEffect, useState, type FormEvent } from "react";

import type { AccountAccessToken, AccountSecurityAPI, AccountSession, IssuedAccessToken } from "../../lib/api";

interface Props {
  api: AccountSecurityAPI;
  onSignedOut: () => void;
}

export function AccountSecurityPage({ api, onSignedOut }: Props) {
  const [sessions, setSessions] = useState<AccountSession[] | null>(null);
  const [loadError, setLoadError] = useState("");

  const retryLoad = async () => {
    setLoadError("");
    try {
      setSessions(await api.listAccountSessions());
    } catch (cause) {
      setLoadError(errorMessage(cause, "活动会话加载失败"));
    }
  };

  useEffect(() => {
    let cancelled = false;
    void api.listAccountSessions().then((result) => {
      if (!cancelled) setSessions(result);
    }).catch((cause: unknown) => {
      if (!cancelled) setLoadError(errorMessage(cause, "活动会话加载失败"));
    });
    return () => {
      cancelled = true;
    };
  }, [api]);

  return (
    <main className="page-shell">
      <header className="page-header">
        <div>
          <p className="eyebrow">Account security</p>
          <h1>账号安全</h1>
          <p className="muted">管理登录会话和密码。修改密码后，所有设备都需要重新登录。</p>
        </div>
      </header>

      <div className="account-security-grid">
        <SessionsPanel api={api} sessions={sessions} setSessions={setSessions} loadError={loadError} onRetry={() => void retryLoad()} onSignedOut={onSignedOut} />
        <PasswordPanel api={api} onSignedOut={onSignedOut} />
        <AccessTokensPanel api={api} onSignedOut={onSignedOut} />
      </div>
    </main>
  );
}

function AccessTokensPanel({ api, onSignedOut }: Pick<Props, "api" | "onSignedOut">) {
  const [tokens, setTokens] = useState<AccountAccessToken[] | null>(null);
  const [name, setName] = useState("");
  const [lifetime, setLifetime] = useState("permanent");
  const [issued, setIssued] = useState<IssuedAccessToken | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [revokingID, setRevokingID] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  const load = async () => {
    setError("");
    try {
      setTokens(await api.listAccessTokens());
    } catch (cause) {
      setError(errorMessage(cause, "长期访问凭证加载失败"));
    }
  };

  useEffect(() => {
    let cancelled = false;
    void api.listAccessTokens().then((result) => {
      if (!cancelled) setTokens(result);
    }).catch((cause: unknown) => {
      if (!cancelled) setError(errorMessage(cause, "长期访问凭证加载失败"));
    });
    return () => {
      cancelled = true;
    };
  }, [api]);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    const normalizedName = name.trim();
    if (normalizedName === "") {
      setError("请输入凭证名称");
      return;
    }
    setSubmitting(true);
    setError("");
    setCopied(false);
    try {
      const created = await api.createAccessToken(normalizedName, accessTokenExpiry(lifetime));
      setIssued(created);
      setTokens((current) => [{
        id: created.id,
        name: created.name,
        is_current: false,
        created_at: created.created_at,
        updated_at: created.created_at,
        last_used_at: null,
        expires_at: created.expires_at
      }, ...(current ?? [])]);
      setName("");
    } catch (cause) {
      setError(errorMessage(cause, "长期访问凭证创建失败"));
    } finally {
      setSubmitting(false);
    }
  };

  const copy = async () => {
    if (issued === null) return;
    try {
      await navigator.clipboard.writeText(`${issued.token_type} ${issued.token}`);
      setCopied(true);
    } catch (cause) {
      setError(errorMessage(cause, "复制失败，请手动复制"));
    }
  };

  const revoke = async (token: AccountAccessToken) => {
    setRevokingID(token.id);
    setError("");
    try {
      await api.revokeAccessToken(token.id);
      setTokens((current) => (current ?? []).filter((candidate) => candidate.id !== token.id));
      if (token.is_current) onSignedOut();
    } catch (cause) {
      setError(errorMessage(cause, "长期访问凭证撤销失败"));
    } finally {
      setRevokingID(null);
    }
  };

  return (
    <section className="security-card access-token-card" aria-labelledby="access-tokens-heading">
      <div className="card-heading">
        <div><h2 id="access-tokens-heading">长期访问凭证</h2><p className="muted">用于旧版客户端或自动化。明文只显示一次，请像密码一样保管。</p></div>
        {tokens !== null && <span className="count-badge">{tokens.length}</span>}
      </div>
      <form className="access-token-form" onSubmit={(event) => void create(event)}>
        <label>凭证名称<input value={name} maxLength={80} placeholder="例如：家庭服务器" required onChange={(event) => setName(event.target.value)} /></label>
        <label>有效期<select value={lifetime} onChange={(event) => setLifetime(event.target.value)}><option value="permanent">永久（手动撤销）</option><option value="30">30 天</option><option value="90">90 天</option><option value="365">365 天</option></select></label>
        <button className="button primary" type="submit" disabled={submitting}>{submitting ? "正在创建…" : "创建凭证"}</button>
      </form>
      {issued !== null && (
        <div className="issued-token" role="status">
          <strong>请立即保存，此凭证关闭后无法再次查看</strong>
          <code className="monospace">{issued.token_type} {issued.token}</code>
          <div className="form-actions"><button className="button secondary compact" type="button" onClick={() => void copy()}>{copied ? "已复制" : "复制凭证"}</button><button className="button ghost compact" type="button" onClick={() => setIssued(null)}>关闭</button></div>
        </div>
      )}
      {error !== "" && <div className="alert error" role="alert">{error}{tokens === null && <button className="button ghost compact" type="button" onClick={() => void load()}>重试</button>}</div>}
      {tokens === null && error === "" && <p className="muted" role="status">正在加载长期访问凭证…</p>}
      {tokens !== null && tokens.length === 0 && <p className="muted">没有长期访问凭证。</p>}
      {tokens !== null && tokens.length > 0 && <div className="session-list">{tokens.map((token) => (
        <article className="session-row" data-testid={`access-token-${token.id}`} key={token.id}>
          <div className="session-summary"><div className="session-title"><strong>{token.name}</strong>{token.is_current && <span className="status-badge current">当前凭证</span>}</div><dl className="session-times"><div><dt>创建</dt><dd>{formatDate(token.created_at)}</dd></div><div><dt>最近使用</dt><dd>{token.last_used_at === null ? "尚无记录" : formatDate(token.last_used_at)}</dd></div><div><dt>到期</dt><dd>{token.expires_at === null ? "永久" : formatDate(token.expires_at)}</dd></div></dl></div>
          <button className="button destructive compact" type="button" disabled={revokingID !== null} onClick={() => void revoke(token)}>{revokingID === token.id ? "正在撤销…" : "撤销凭证"}</button>
        </article>
      ))}</div>}
    </section>
  );
}

function SessionsPanel({ api, sessions, setSessions, loadError, onRetry, onSignedOut }: {
  api: AccountSecurityAPI;
  sessions: AccountSession[] | null;
  setSessions: (sessions: AccountSession[]) => void;
  loadError: string;
  onRetry: () => void;
  onSignedOut: () => void;
}) {
  const [revokingID, setRevokingID] = useState<number | null>(null);
  const [error, setError] = useState("");

  const revoke = async (session: AccountSession) => {
    setRevokingID(session.id);
    setError("");
    try {
      await api.revokeAccountSession(session.id);
      if (session.is_current) {
        onSignedOut();
        return;
      }
      setSessions((sessions ?? []).filter((candidate) => candidate.id !== session.id));
    } catch (cause) {
      setError(errorMessage(cause, "会话撤销失败"));
    } finally {
      setRevokingID(null);
    }
  };

  return (
    <section className="security-card" aria-labelledby="active-sessions-heading">
      <div className="card-heading">
        <div><h2 id="active-sessions-heading">活动会话</h2><p className="muted">仅显示尚未过期或撤销的登录。</p></div>
        {sessions !== null && <span className="count-badge">{sessions.length}</span>}
      </div>
      {loadError !== "" && <div className="alert error" role="alert">{loadError}<button className="button ghost compact" type="button" onClick={onRetry}>重试</button></div>}
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      {sessions === null && loadError === "" && <p className="muted" role="status">正在加载活动会话…</p>}
      {sessions !== null && sessions.length === 0 && <p className="muted">没有活动会话。</p>}
      {sessions !== null && sessions.length > 0 && (
        <div className="session-list">
          {sessions.map((session) => (
            <article className="session-row" data-testid={`account-session-${session.id}`} key={session.id}>
              <div className="session-summary">
                <div className="session-title">
                  <strong>登录会话 #{session.id}</strong>
                  {session.is_current && <span className="status-badge current">当前会话</span>}
                </div>
                <dl className="session-times">
                  <div><dt>创建</dt><dd>{formatDate(session.created_at)}</dd></div>
                  <div><dt>最近使用</dt><dd>{session.last_used_at === null ? "尚无记录" : formatDate(session.last_used_at)}</dd></div>
                  <div><dt>到期</dt><dd>{formatDate(session.expires_at)}</dd></div>
                </dl>
              </div>
              <button className={session.is_current ? "button ghost compact" : "button destructive compact"} type="button" disabled={revokingID !== null} onClick={() => void revoke(session)}>
                {revokingID === session.id ? "正在撤销…" : session.is_current ? "退出当前会话" : "撤销会话"}
              </button>
            </article>
          ))}
        </div>
      )}
    </section>
  );
}

function PasswordPanel({ api, onSignedOut }: Pick<Props, "api" | "onSignedOut">) {
  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    if (newPassword.length < 12) {
      setError("新密码至少需要 12 个字符");
      return;
    }
    if (newPassword !== confirmation) {
      setError("两次输入的新密码不一致");
      return;
    }
    setSubmitting(true);
    try {
      await api.changePassword(oldPassword, newPassword);
      onSignedOut();
    } catch (cause) {
      setError(errorMessage(cause, "密码修改失败"));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="security-card" aria-labelledby="change-password-heading">
      <div className="card-heading"><div><h2 id="change-password-heading">修改密码</h2><p className="muted">新密码至少 12 个字符。</p></div></div>
      <form className="form-stack" onSubmit={(event) => void submit(event)}>
        <label>当前密码<input type="password" autoComplete="current-password" value={oldPassword} required onChange={(event) => setOldPassword(event.target.value)} /></label>
        <label>新密码<input type="password" autoComplete="new-password" minLength={12} value={newPassword} required onChange={(event) => setNewPassword(event.target.value)} /></label>
        <label>确认新密码<input type="password" autoComplete="new-password" minLength={12} value={confirmation} required onChange={(event) => setConfirmation(event.target.value)} /></label>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <button className="button primary" type="submit" disabled={submitting}>{submitting ? "正在修改…" : "修改密码"}</button>
      </form>
    </section>
  );
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function accessTokenExpiry(lifetime: string): string | null {
  if (lifetime === "permanent") return null;
  const days = Number(lifetime);
  return new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString();
}

function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error ? cause.message : fallback;
}
