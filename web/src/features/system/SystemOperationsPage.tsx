import { useEffect, useState, type FormEvent } from "react";

import type {
  AdminAuditPage, AuditMethod, SystemOperationsAPI, SystemStatus, TicketMailFailure, TicketMailFailurePage, WorkerStatus
} from "../../lib/api";

const pageSize = 20;

export function SystemOperationsPage({ api }: { api: SystemOperationsAPI }) {
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [audit, setAudit] = useState<AdminAuditPage | null>(null);
  const [failures, setFailures] = useState<TicketMailFailurePage | null>(null);
  const [method, setMethod] = useState<AuditMethod | "">("");
  const [query, setQuery] = useState("");
  const [appliedMethod, setAppliedMethod] = useState<AuditMethod | "">("");
  const [appliedQuery, setAppliedQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const loadAudit = async (page: number, nextMethod = appliedMethod, nextQuery = appliedQuery) => {
    const result = await api.listAdminAudit(page, pageSize, nextMethod, nextQuery);
    setAudit(result);
  };

  const loadFailures = async (page: number) => {
    const result = await api.listTicketMailFailures(page, pageSize);
    setFailures(result);
  };

  const loadAll = async () => {
    setLoading(true);
    setError("");
    try {
      const [nextStatus, nextAudit, nextFailures] = await Promise.all([
        api.getSystemStatus(), api.listAdminAudit(1, pageSize, appliedMethod, appliedQuery), api.listTicketMailFailures(1, pageSize)
      ]);
      setStatus(nextStatus);
      setAudit(nextAudit);
      setFailures(nextFailures);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void Promise.all([
      api.getSystemStatus(), api.listAdminAudit(1, pageSize, "", ""), api.listTicketMailFailures(1, pageSize)
    ]).then(([nextStatus, nextAudit, nextFailures]) => {
      if (!active) return;
      setStatus(nextStatus);
      setAudit(nextAudit);
      setFailures(nextFailures);
      setError("");
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const filterAudit = async (event: FormEvent) => {
    event.preventDefault();
    const normalizedQuery = query.trim();
    setAppliedMethod(method);
    setAppliedQuery(normalizedQuery);
    setError("");
    try {
      await loadAudit(1, method, normalizedQuery);
    } catch (cause) {
      setError(messageOf(cause));
    }
  };

  const changeAuditPage = async (page: number) => {
    setError("");
    try {
      await loadAudit(page);
    } catch (cause) {
      setError(messageOf(cause));
    }
  };

  const changeFailurePage = async (page: number) => {
    setError("");
    try {
      await loadFailures(page);
    } catch (cause) {
      setError(messageOf(cause));
    }
  };

  return <main className="page-shell system-operations-page">
    <header className="page-header"><div><p className="eyebrow">Operations</p><h1>系统状态</h1><p className="muted">查看调度器、邮件与 Telegram 队列、失败任务和管理员操作审计。</p></div>
      <button className="button secondary" disabled={loading} onClick={() => void loadAll()}>{loading ? "正在刷新…" : "刷新系统状态"}</button>
    </header>
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {status === null ? <div className="empty-card">{loading ? "正在加载系统状态…" : "暂无系统状态。"}</div> : <>
      <section className="system-overview-grid" aria-label="运行概览">
        <WorkerMetric label="调度器" status={status.scheduler} />
        <WorkerMetric label="邮件任务" status={status.mail_worker} />
        <Metric label="数据库" value={`Schema v${status.schema_version}`} tone="good" />
        <Metric label="运行时间" value={formatDuration(status.uptime_seconds)} hint={`启动于 ${formatDate(status.started_at)}`} />
        <Metric label="邮件队列" value={`待处理 ${status.mail_queue.pending}`} hint={`执行中 ${status.mail_queue.claimed} · 已发送 ${status.mail_queue.sent}`} tone={status.mail_queue.pending > 0 ? "warning" : "good"} />
        <Metric label="失败邮件" value={String(status.mail_queue.failed)} hint={status.mail_queue.oldest_pending_at === null ? "当前无待发送任务" : `最早待发送 ${formatDate(status.mail_queue.oldest_pending_at)}`} tone={status.mail_queue.failed > 0 ? "danger" : "good"} />
        <Metric label="Telegram 队列" value={`待处理 ${status.telegram_queue.pending}`} hint={`执行中 ${status.telegram_queue.claimed} · 已发送 ${status.telegram_queue.sent}`} tone={status.telegram_queue.pending > 0 ? "warning" : "good"} />
        <Metric label="Telegram 失败" value={String(status.telegram_queue.failed)} hint={status.telegram_queue.oldest_pending_at === null ? "当前无待发送任务" : `最早待发送 ${formatDate(status.telegram_queue.oldest_pending_at)}`} tone={status.telegram_queue.failed > 0 ? "danger" : "good"} />
      </section>
    </>}

    <section className="system-section" aria-labelledby="audit-heading">
      <div className="section-heading"><div><h2 id="audit-heading">管理员审计日志</h2><p className="muted">仅记录身份、操作、路由、结果与时间；不保存请求正文或密码。</p></div></div>
      <form className="system-filter-bar" onSubmit={(event) => void filterAudit(event)}>
        <label>审计操作<select value={method} onChange={(event) => setMethod(event.target.value as AuditMethod | "")}><option value="">全部</option><option value="POST">POST</option><option value="PUT">PUT</option><option value="PATCH">PATCH</option><option value="DELETE">DELETE</option></select></label>
        <label>搜索审计日志<input type="search" aria-label="搜索审计日志" value={query} maxLength={200} placeholder="路由或管理员邮箱" onChange={(event) => setQuery(event.target.value)} /></label>
        <button className="button primary" type="submit">查询审计日志</button>
      </form>
      <div className="resource-table-wrap"><table className="resource-table system-audit-table"><thead><tr><th>管理员</th><th>操作</th><th>路由</th><th>结果</th><th>时间</th></tr></thead><tbody>
        {audit?.items.map((item) => <tr key={item.id}>
          <td data-label="管理员"><strong>{item.administrator_email}</strong><small className="muted">#{item.administrator_id ?? "已删除"}</small></td>
          <td data-label="操作"><span className="count-pill">{item.method}</span></td>
          <td data-label="路由"><code className="system-route">{item.route}</code></td>
          <td data-label="结果"><span className={`status-badge ${item.status_code < 400 ? "enabled" : "blocked"}`}>{item.status_code}</span></td>
          <td data-label="时间">{formatDate(item.created_at)}</td>
        </tr>)}
        {audit !== null && audit.items.length === 0 && <tr><td colSpan={5}>暂无管理员操作记录。</td></tr>}
      </tbody></table>
        {audit !== null && audit.total > audit.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={audit.page <= 1} onClick={() => void changeAuditPage(audit.page - 1)}>上一页</button><span>第 {audit.page} 页</span><button className="button secondary compact" disabled={audit.page * audit.page_size >= audit.total} onClick={() => void changeAuditPage(audit.page + 1)}>下一页</button></div>}
      </div>
    </section>

    <section className="system-section" aria-labelledby="mail-failures-heading">
      <div className="section-heading"><div><h2 id="mail-failures-heading">失败邮件任务</h2><p className="muted">展示投递诊断元数据，不展示工单回复正文、验证码或 SMTP 凭据。</p></div></div>
      <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>邮件主题</th><th>类型</th><th>收件人</th><th>尝试</th><th>错误</th><th>失败时间</th></tr></thead><tbody>
        {failures?.items.map((item) => <tr key={`${item.kind}-${item.id}`}><td data-label="邮件主题"><strong>{item.ticket_subject}</strong><small className="muted">任务 #{Math.abs(item.id)}</small></td><td data-label="类型">{mailFailureKindLabel(item.kind)}</td><td data-label="收件人">{item.recipient}</td><td data-label="尝试">{item.attempt_count}</td><td data-label="错误"><span className="system-error-text">{item.last_error}</span></td><td data-label="失败时间">{formatDate(item.failed_at)}</td></tr>)}
        {failures !== null && failures.items.length === 0 && <tr><td colSpan={6}>暂无失败邮件任务。</td></tr>}
      </tbody></table>
        {failures !== null && failures.total > failures.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={failures.page <= 1} onClick={() => void changeFailurePage(failures.page - 1)}>失败任务上一页</button><span>第 {failures.page} 页</span><button className="button secondary compact" disabled={failures.page * failures.page_size >= failures.total} onClick={() => void changeFailurePage(failures.page + 1)}>失败任务下一页</button></div>}
      </div>
    </section>
  </main>;
}

function mailFailureKindLabel(kind: TicketMailFailure["kind"]): string {
  switch (kind) {
    case "password_reset": return "密码重置";
    case "registration_email_verification": return "注册验证";
    case "login_link": return "登录链接";
    case "subscription_reminder_expire": return "到期提醒";
    case "subscription_reminder_traffic": return "流量提醒";
    default: return "工单通知";
  }
}

function WorkerMetric({ label, status }: { label: string; status: WorkerStatus }) {
  return <Metric label={label} value={status.healthy ? "正常" : "异常"} hint={status.last_run_at === null ? "尚无心跳" : `最后运行 ${formatDate(status.last_run_at)}`} tone={status.healthy ? "good" : "danger"} />;
}

function Metric({ label, value, hint, tone = "" }: { label: string; value: string; hint?: string; tone?: "" | "good" | "warning" | "danger" }) {
  return <article className={`overview-metric ${tone}`}><span>{label}</span><strong>{value}</strong>{hint !== undefined && <small>{hint}</small>}</article>;
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const days = Math.floor(seconds / 86_400);
  const hours = Math.floor((seconds % 86_400) / 3_600);
  const minutes = Math.floor((seconds % 3_600) / 60);
  return [days > 0 ? `${days}天` : "", hours > 0 ? `${hours}小时` : "", `${minutes}分钟`].filter(Boolean).join(" ");
}

function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString("zh-CN", { hour12: false });
}

function messageOf(cause: unknown) {
  return cause instanceof Error ? cause.message : "系统状态请求失败";
}
