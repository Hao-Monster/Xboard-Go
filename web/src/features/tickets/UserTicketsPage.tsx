import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { Ticket, TicketInput, TicketLevel, TicketPage } from "../../lib/api";
import { formatDate, levelLabel, TicketDetailDialog } from "./TicketDetailDialog";

interface UserTicketsAPI {
  listTickets: (page?: number, pageSize?: number) => Promise<TicketPage>;
  createTicket: (input: TicketInput) => Promise<Ticket>;
  getTicket: (id: number) => Promise<Ticket>;
  replyTicket: (id: number, message: string) => Promise<Ticket>;
  closeTicket: (id: number) => Promise<Ticket>;
}

export function UserTicketsPage({ api }: { api: UserTicketsAPI }) {
  const [page, setPage] = useState<TicketPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<number | null>(null);
  const getTicket = useCallback((id: number) => api.getTicket(id), [api]);
  const replyTicket = useCallback((id: number, message: string) => api.replyTicket(id, message), [api]);
  const closeTicket = useCallback((id: number) => api.closeTicket(id), [api]);

  const load = async (pageNumber = 1) => {
    setLoading(true);
    setError("");
    try {
      setPage(await api.listTickets(pageNumber, 20));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.listTickets(1, 20).then((value) => {
      if (active) {
        setPage(value);
        setError("");
      }
    }).catch((cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const replace = (ticket: Ticket) => {
    setPage((current) => ({ ...current, items: current.items.map((item) => item.id === ticket.id ? ticket : item) }));
  };

  return <main className="page-shell ticket-page">
    <header className="page-header">
      <div><p className="eyebrow">Support</p><h1>我的工单</h1><p className="muted">创建问题并持续查看管理员处理结果。</p></div>
      <button className="button primary" onClick={() => setCreating(true)}>新建工单</button>
    </header>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load(page.page)}>重试</button></div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载工单…</div> : page.items.length === 0 ? <div className="empty-card">暂无工单。</div> : <>
      <div className="resource-table-wrap">
        <table className="resource-table ticket-table">
          <thead><tr><th>主题</th><th>工单级别</th><th>回复状态</th><th>工单状态</th><th>创建时间</th><th>最后回复时间</th><th>操作</th></tr></thead>
          <tbody>{page.items.map((ticket) => <tr key={ticket.id}>
            <td data-label="主题"><strong>{ticket.subject}</strong><small className="muted">#{ticket.id}</small></td>
            <td data-label="工单级别">{levelLabel(ticket.level)}</td>
            <td data-label="回复状态"><span className={`ticket-reply-badge ${ticket.reply_status === 0 ? "waiting" : "answered"}`}>{ticket.reply_status === 0 ? "待回复" : "已回复"}</span></td>
            <td data-label="工单状态"><span className={`status-badge ${ticket.status === 0 ? "enabled" : "blocked"}`}>{ticket.status === 0 ? "处理中" : "已关闭"}</span></td>
            <td data-label="创建时间">{formatDate(ticket.created_at)}</td>
            <td data-label="最后回复时间">{formatDate(ticket.updated_at)}</td>
            <td data-label="操作"><div className="row-actions"><button className="button ghost compact" aria-label={`查看工单：${ticket.subject}`} onClick={() => setSelected(ticket.id)}>查看</button></div></td>
          </tr>)}</tbody>
        </table>
      </div>
      {page.total > page.page_size && <div className="notice-pagination"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load(page.page - 1)}>上一页</button><span>第 {page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load(page.page + 1)}>下一页</button></div>}
    </>}
    {creating && <CreateTicketDialog api={api} onClose={() => setCreating(false)} onCreated={(ticket) => {
      setPage((current) => ({ ...current, total: current.total + 1, items: [ticket, ...current.items].slice(0, current.page_size) }));
      setCreating(false);
    }} />}
    {selected !== null && <TicketDetailDialog ticketID={selected} load={getTicket} reply={replyTicket} close={closeTicket} onUpdated={replace} onClose={() => setSelected(null)} />}
  </main>;
}

function CreateTicketDialog({ api, onClose, onCreated }: { api: UserTicketsAPI; onClose: () => void; onCreated: (ticket: Ticket) => void }) {
  const [subject, setSubject] = useState("");
  const [level, setLevel] = useState<TicketLevel>(0);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      onCreated(await api.createTicket({ subject: subject.trim(), level, message: message.trim() }));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  };
  return <Modal title="新建工单" onClose={onClose}>
    <div className="modal-header"><h2>新建工单</h2><button className="icon-button" aria-label="关闭新建工单" onClick={onClose}>×</button></div>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>主题<input value={subject} maxLength={255} required onChange={(event) => setSubject(event.target.value)} /></label>
      <label>工单级别<select value={level} onChange={(event) => setLevel(Number(event.target.value) as TicketLevel)}><option value={0}>低</option><option value={1}>中</option><option value={2}>高</option></select></label>
      <label>消息<textarea value={message} maxLength={65_536} required onChange={(event) => setMessage(event.target.value)} /></label>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={busy}>{busy ? "正在创建…" : "创建工单"}</button></div>
    </form>
  </Modal>;
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
