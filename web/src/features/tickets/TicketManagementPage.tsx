import { useCallback, useEffect, useState, type FormEvent } from "react";

import type { AdminTicketQuery, Ticket, TicketLevel, TicketPage, TicketReplyStatus, TicketStatus } from "../../lib/api";
import { formatDate, levelLabel, TicketDetailDialog } from "./TicketDetailDialog";

interface TicketManagementAPI {
  listAdminTickets: (query?: AdminTicketQuery) => Promise<TicketPage>;
  getAdminTicket: (id: number) => Promise<Ticket>;
  replyAdminTicket: (id: number, message: string) => Promise<Ticket>;
  closeAdminTicket: (id: number) => Promise<Ticket>;
}

export function TicketManagementPage({ api, initialStatus = 0 }: { api: TicketManagementAPI; initialStatus?: TicketStatus }) {
  const [status, setStatus] = useState<TicketStatus>(initialStatus);
  const [page, setPage] = useState<TicketPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState("");
  const [replyStatus, setReplyStatus] = useState("");
  const [applied, setApplied] = useState<AdminTicketQuery>({ page: 1, page_size: 20, status: initialStatus });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<number | null>(null);
  const getTicket = useCallback((id: number) => api.getAdminTicket(id), [api]);
  const replyTicket = useCallback((id: number, message: string) => api.replyAdminTicket(id, message), [api]);
  const closeTicket = useCallback((id: number) => api.closeAdminTicket(id), [api]);

  const load = useCallback(async (next: AdminTicketQuery) => {
    setLoading(true);
    setError("");
    try {
      setPage(await api.listAdminTickets(next));
      setApplied(next);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    const initial = { page: 1, page_size: 20, status: initialStatus } satisfies AdminTicketQuery;
    void api.listAdminTickets(initial).then((value) => {
      if (active) {
        setPage(value);
        setApplied(initial);
        setError("");
      }
    }).catch((cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api, initialStatus]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const next: AdminTicketQuery = { page: 1, page_size: 20, status };
    if (level !== "") next.level = Number(level) as TicketLevel;
    if (replyStatus !== "") next.reply_status = Number(replyStatus) as TicketReplyStatus;
    if (query.trim() !== "") next.query = query.trim();
    void load(next);
  };

  const switchStatus = (nextStatus: TicketStatus) => {
    setStatus(nextStatus);
    setQuery("");
    setLevel("");
    setReplyStatus("");
    void load({ page: 1, page_size: 20, status: nextStatus });
  };

  const replace = (ticket: Ticket) => {
    setPage((current) => {
      const present = current.items.some((item) => item.id === ticket.id);
      const shouldRemain = matchesTicketQuery(ticket, applied);
      return {
        ...current,
        total: shouldRemain || !present ? current.total : Math.max(0, current.total - 1),
        items: shouldRemain ? current.items.map((item) => item.id === ticket.id ? ticket : item) : current.items.filter((item) => item.id !== ticket.id)
      };
    });
  };

  return <main className="page-shell ticket-page">
    <header className="page-header"><div><p className="eyebrow">Support operations</p><h1>工单管理</h1><p className="muted">搜索、查看、回复和关闭用户工单。</p></div></header>
    <div className="ticket-status-tabs" role="group" aria-label="工单状态"><button className={`button ${status === 0 ? "primary" : "secondary"}`} onClick={() => switchStatus(0)}>处理中</button><button className={`button ${status === 1 ? "primary" : "secondary"}`} onClick={() => switchStatus(1)}>已关闭</button></div>
    <form className="ticket-filter-bar" onSubmit={submit}>
      <label className="search-field">搜索工单<input type="search" role="searchbox" aria-label="搜索工单" value={query} placeholder="工单主题或用户邮箱" onChange={(event) => setQuery(event.target.value)} /></label>
      <label>工单级别<select aria-label="工单级别" value={level} onChange={(event) => setLevel(event.target.value)}><option value="">全部</option><option value="0">低</option><option value="1">中</option><option value="2">高</option></select></label>
      <label>回复状态<select aria-label="回复状态" value={replyStatus} onChange={(event) => setReplyStatus(event.target.value)}><option value="">全部</option><option value="0">待回复</option><option value="1">已回复</option></select></label>
      <button className="button secondary" type="submit" disabled={loading}>查询工单</button>
    </form>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load(applied)}>重试</button></div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载工单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的工单。</div> : <div className="resource-table-wrap">
      <table className="resource-table ticket-table"><thead><tr><th>工单号</th><th>主题</th><th>用户</th><th>优先级</th><th>回复状态</th><th>最后更新</th><th>创建时间</th><th>操作</th></tr></thead>
        <tbody>{page.items.map((ticket) => <tr key={ticket.id}>
          <td data-label="工单号">#{ticket.id}</td><td data-label="主题"><strong>{ticket.subject}</strong></td><td data-label="用户">{ticket.user_email}</td>
          <td data-label="优先级">{levelLabel(ticket.level)}</td><td data-label="回复状态"><span className={`ticket-reply-badge ${ticket.reply_status === 0 ? "waiting" : "answered"}`}>{ticket.reply_status === 0 ? "待回复" : "已回复"}</span></td>
          <td data-label="最后更新">{formatDate(ticket.updated_at)}</td><td data-label="创建时间">{formatDate(ticket.created_at)}</td>
          <td data-label="操作"><div className="row-actions"><button className="button ghost compact" aria-label={`查看工单：${ticket.subject}`} onClick={() => setSelected(ticket.id)}>查看</button></div></td>
        </tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>上一页</button><span>第 {page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>下一页</button></div>}
    </div>}
    {selected !== null && <TicketDetailDialog ticketID={selected} administrator load={getTicket} reply={replyTicket} close={closeTicket} onUpdated={replace} onClose={() => setSelected(null)} />}
  </main>;
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}

function matchesTicketQuery(ticket: Ticket, query: AdminTicketQuery) {
  if (query.status !== undefined && ticket.status !== query.status) return false;
  if (query.reply_status !== undefined && ticket.reply_status !== query.reply_status) return false;
  if (query.level !== undefined && ticket.level !== query.level) return false;
  const keyword = query.query?.trim().toLocaleLowerCase();
  return keyword === undefined || keyword === "" || ticket.subject.toLocaleLowerCase().includes(keyword) || (ticket.user_email ?? "").toLocaleLowerCase().includes(keyword);
}
