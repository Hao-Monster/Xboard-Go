import { useEffect, useState } from "react";

import type { InvitationCode, InvitationSummary } from "../../lib/api";

interface InvitationPageAPI {
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
}

const emptySummary: InvitationSummary = { codes: [], invited_count: 0 };

export function InvitationPage({ api }: { api: InvitationPageAPI }) {
  const [summary, setSummary] = useState(emptySummary);
  const [loading, setLoading] = useState(true);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const load = async () => {
    setError("");
    try {
      setSummary(await api.getInvitations());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.getInvitations().then((next) => {
      if (active) setSummary(next);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const generate = async () => {
    setGenerating(true);
    setError("");
    setMessage("");
    try {
      await api.createInvitation();
      setSummary(await api.getInvitations());
      setMessage("邀请码已生成");
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setGenerating(false);
    }
  };

  const copyLink = async (code: string) => {
    setError("");
    setMessage("");
    try {
      await navigator.clipboard.writeText(`${window.location.origin}/#/register?code=${code}`);
      setMessage("邀请链接已复制");
    } catch {
      setError("复制邀请链接失败");
    }
  };

  return <main className="page-shell invitation-page">
    <header className="page-header"><div><p className="eyebrow">Referral</p><h1>我的邀请</h1><p className="muted">生成邀请码并查看已完成注册的邀请关系。</p></div></header>
    <div className="system-overview-grid invitation-overview">
      <article className="overview-metric good"><span>已注册用户数</span><strong>{summary.invited_count}</strong></article>
      <article className="overview-metric"><span>可用邀请码</span><strong>{summary.codes.length}</strong></article>
    </div>
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {message !== "" && <div className="alert success global-alert" role="status">{message}</div>}
    <section className="system-section" aria-labelledby="invitation-codes-heading">
      <div className="section-heading"><div><h2 id="invitation-codes-heading">邀请码管理</h2><p className="muted">访问次数与旧 Xboard 的 PV 口径一致。</p></div><button className="button primary" type="button" disabled={generating} onClick={() => void generate()}>{generating ? "正在生成…" : "生成邀请码"}</button></div>
      {loading && summary.codes.length === 0 ? <div className="empty-card">正在加载邀请码…</div> : summary.codes.length === 0 ? <div className="empty-card">暂无可用邀请码</div> : <div className="resource-table-wrap">
        <table className="resource-table"><thead><tr><th>邀请码</th><th>访问次数</th><th>创建时间</th><th>操作</th></tr></thead><tbody>
          {summary.codes.map((code) => <tr key={code.code}><td><code className="monospace">{code.code}</code></td><td>{code.pv}</td><td>{formatDate(code.created_at)}</td><td><button className="button secondary compact" type="button" onClick={() => void copyLink(code.code)}>复制链接</button></td></tr>)}
        </tbody></table>
      </div>}
      {!loading && error !== "" && summary.codes.length === 0 && <button className="button secondary" type="button" onClick={() => void load()}>重新加载</button>}
    </section>
  </main>;
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function messageOf(cause: unknown) {
  return cause instanceof Error ? cause.message : "邀请码请求失败";
}
