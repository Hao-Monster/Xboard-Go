import { translateAdmin } from "../../lib/adminLocale";
import { useCallback, useEffect, useRef, useState } from "react";

import type { AdminTicketQuery, Ticket, TicketLevel, TicketPage, TicketSettings, TicketSettingsInput, TicketStatus } from "../../lib/api";
import { formatDate, levelLabel, TicketDetailDialog } from "./TicketDetailDialog";
import { NodeFilter } from "../nodes/NodeFilter";
import type { TransitionWithdrawal } from "./CommissionWithdrawalPanel";

interface TicketManagementAPI {
  listAdminTickets: (query?: AdminTicketQuery) => Promise<TicketPage>;
  getAdminTicket: (id: number) => Promise<Ticket>;
  replyAdminTicket: (id: number, message: string) => Promise<Ticket>;
  closeAdminTicket: (id: number) => Promise<Ticket>;
  transitionCommissionWithdrawal: TransitionWithdrawal;
  getTicketSettings: () => Promise<TicketSettings>;
  updateTicketSettings: (input: TicketSettingsInput) => Promise<TicketSettings>;
}

export function TicketManagementPage({ api, initialStatus = 0 }: { api: TicketManagementAPI; initialStatus?: TicketStatus }) {
  const [status, setStatus] = useState<TicketStatus>(initialStatus);
  const [page, setPage] = useState<TicketPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [level, setLevel] = useState("");
  const [applied, setApplied] = useState<AdminTicketQuery>({ page: 1, page_size: 20, status: initialStatus });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<number | null>(null);
  const requestSequence = useRef(0);
  const getTicket = useCallback((id: number) => api.getAdminTicket(id), [api]);
  const replyTicket = useCallback((id: number, message: string) => api.replyAdminTicket(id, message), [api]);
  const closeTicket = useCallback((id: number) => api.closeAdminTicket(id), [api]);
  const transitionWithdrawal: TransitionWithdrawal = useCallback((id, nextStatus, reference) => api.transitionCommissionWithdrawal(id, nextStatus, reference), [api]);

  const load = useCallback(async (next: AdminTicketQuery) => {
    const sequence = ++requestSequence.current;
    setLoading(true);
    setError("");
    try {
      const result = await api.listAdminTickets(next);
      if (sequence !== requestSequence.current) return;
      setPage(result);
      setApplied(next);
    } catch (cause) {
      if (sequence === requestSequence.current) setError(errorMessage(cause));
    } finally {
      if (sequence === requestSequence.current) setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    const sequence = ++requestSequence.current;
    const initial = { page: 1, page_size: 20, status: initialStatus } satisfies AdminTicketQuery;
    void api.listAdminTickets(initial).then((value) => {
      if (active && sequence === requestSequence.current) {
        setPage(value);
        setApplied(initial);
        setError("");
      }
    }).catch((cause) => {
      if (active && sequence === requestSequence.current) setError(errorMessage(cause));
    }).finally(() => {
      if (active && sequence === requestSequence.current) setLoading(false);
    });
    return () => { active = false; };
  }, [api, initialStatus]);

  const switchStatus = (nextStatus: TicketStatus) => {
    setStatus(nextStatus);
    setLevel("");
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
    <header className="page-header"><div><h1>{translateAdmin("工单管理")}</h1></div></header>
    <div className="ticket-status-tabs" role="tablist" aria-label="工单状态"><button role="tab" aria-selected={status === 0} className={`button ${status === 0 ? "primary" : "secondary"}`} onClick={() => switchStatus(0)}>{translateAdmin("处理中")}</button><button role="tab" aria-selected={status === 1} className={`button ${status === 1 ? "primary" : "secondary"}`} onClick={() => switchStatus(1)}>{translateAdmin("已关闭")}</button></div>
    <div className="ticket-filter-bar"><NodeFilter label={translateAdmin("优先级")} value={level === "" ? [] : [level]} options={[{value:"0",label:"低"},{value:"1",label:"中"},{value:"2",label:"高"}]} onChange={values => {
      const nextLevel = values.at(-1) ?? ""; setLevel(nextLevel);
      void load({page:1,page_size:20,status,...(nextLevel === "" ? {} : {level:Number(nextLevel) as TicketLevel})});
    }} /></div>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load(applied)}>{translateAdmin("重试")}</button></div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载工单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的工单。</div> : <div className="resource-table-wrap">
      <table className="resource-table ticket-table"><thead><tr><th>{translateAdmin("工单号")}</th><th>{translateAdmin("主题")}</th><th>{translateAdmin("优先级")}</th><th>{translateAdmin("状态")}</th><th>{translateAdmin("最后更新")}</th><th>{translateAdmin("创建时间")}</th><th>{translateAdmin("操作")}</th></tr></thead>
        <tbody>{page.items.map((ticket) => <tr key={ticket.id}>
          <td data-label="工单号">#{ticket.id}</td><td data-label="主题"><strong>{ticket.subject}</strong></td>
          <td data-label="优先级">{levelLabel(ticket.level)}</td><td data-label="状态"><span className={`ticket-reply-badge ${ticket.reply_status === 0 ? "waiting" : "answered"}`}>{ticket.reply_status === 0 ? translateAdmin("待回复") : translateAdmin("已回复")}</span></td>
          <td data-label="最后更新">{formatDate(ticket.updated_at)}</td><td data-label="创建时间">{formatDate(ticket.created_at)}</td>
          <td data-label="操作"><div className="row-actions"><button className="button ghost compact" aria-label={`查看工单：${ticket.subject}`} onClick={() => setSelected(ticket.id)}>{translateAdmin("查看")}</button></div></td>
        </tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>{translateAdmin("上一页")}</button><span>{translateAdmin("第")}{page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>{translateAdmin("下一页")}</button></div>}
    </div>}
    {selected !== null && <TicketDetailDialog ticketID={selected} administrator load={getTicket} reply={replyTicket} close={closeTicket} transitionWithdrawal={transitionWithdrawal} onUpdated={replace} onClose={() => setSelected(null)} />}
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
