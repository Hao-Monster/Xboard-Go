import { useEffect, useState, type FormEvent } from "react";

import type { AccountSecurityAPI, AccountSession } from "../../lib/api";

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
      </div>
    </main>
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

function errorMessage(cause: unknown, fallback: string): string {
  return cause instanceof Error ? cause.message : fallback;
}
