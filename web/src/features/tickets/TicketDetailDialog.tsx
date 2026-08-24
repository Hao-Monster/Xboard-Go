import { useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { Ticket } from "../../lib/api";

interface TicketDetailDialogProps {
  ticketID: number;
  administrator?: boolean;
  load: (id: number) => Promise<Ticket>;
  reply: (id: number, message: string) => Promise<Ticket>;
  close: (id: number) => Promise<Ticket>;
  onClose: () => void;
  onUpdated: (ticket: Ticket) => void;
}

export function TicketDetailDialog({ ticketID, administrator = false, load, reply, close, onClose, onUpdated }: TicketDetailDialogProps) {
  const [ticket, setTicket] = useState<Ticket | null>(null);
  const [loading, setLoading] = useState(true);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmingClose, setConfirmingClose] = useState(false);

  useEffect(() => {
    let active = true;
    void load(ticketID).then((value) => {
      if (active) {
        setTicket(value);
        setError("");
      }
    }).catch((cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [load, ticketID]);

  const submitReply = async (event: FormEvent) => {
    event.preventDefault();
    if (ticket === null) return;
    setBusy(true);
    setError("");
    try {
      const updated = await reply(ticket.id, message.trim());
      setTicket(updated);
      setMessage("");
      onUpdated(updated);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  };

  const confirmClose = async () => {
    if (ticket === null) return;
    setBusy(true);
    setError("");
    try {
      const updated = await close(ticket.id);
      setTicket(updated);
      setConfirmingClose(false);
      onUpdated(updated);
    } catch (cause) {
      setError(errorMessage(cause));
      setConfirmingClose(false);
    } finally {
      setBusy(false);
    }
  };

  const canReply = ticket !== null && (administrator || ticket.status === 0);
  return <>
    <Modal title="工单详情" onClose={onClose}>
      <div className="modal-header">
        <div><h2>工单详情</h2>{ticket !== null && <p className="muted small">#{ticket.id} · {ticket.user_email ?? ticket.subject}</p>}</div>
        <button className="icon-button" aria-label="关闭工单详情" onClick={onClose}>×</button>
      </div>
      {loading ? <div className="ticket-loading">正在加载工单…</div> : ticket === null ? <div className="alert error" role="alert">{error || "工单不存在"}</div> : <>
        <div className="ticket-detail-heading">
          <div><h3>{ticket.subject}</h3><p className="muted small">{levelLabel(ticket.level)} · 更新于 {formatDate(ticket.updated_at)}</p></div>
          <div className="ticket-badges"><span className={`status-badge ${ticket.status === 0 ? "enabled" : "blocked"}`}>{ticket.status === 0 ? "处理中" : "已关闭"}</span><span className={`ticket-reply-badge ${ticket.reply_status === 0 ? "waiting" : "answered"}`}>{ticket.reply_status === 0 ? "待回复" : "已回复"}</span></div>
        </div>
        <div className="ticket-message-list" aria-label="工单消息">
          {(ticket.messages ?? []).map((item) => <article key={item.id} className={`ticket-message ${item.is_me ? "from-user" : "from-admin"}`}>
            <header><strong>{item.is_me ? "用户" : "管理员"}</strong><time dateTime={item.created_at}>{formatDate(item.created_at)}</time></header>
            <p>{item.message}</p>
          </article>)}
        </div>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        {canReply && <form className="form-stack ticket-reply-form" onSubmit={(event) => void submitReply(event)}>
          <label>回复内容<textarea value={message} maxLength={65_536} required onChange={(event) => setMessage(event.target.value)} /></label>
          <div className="form-actions split">
            {ticket.status === 0 ? <button className="button destructive" type="button" disabled={busy} onClick={() => setConfirmingClose(true)}>关闭工单</button> : <span className="muted small">管理员回复不会重新开启已关闭工单。</span>}
            <button className="button primary" type="submit" disabled={busy || message.trim() === ""}>{busy ? "正在发送…" : "回复"}</button>
          </div>
        </form>}
      </>}
    </Modal>
    {confirmingClose && <Modal title="关闭工单" onClose={() => setConfirmingClose(false)}>
      <div className="modal-header"><h2>关闭工单</h2><button className="icon-button" aria-label="取消关闭工单" onClick={() => setConfirmingClose(false)}>×</button></div>
      <p>关闭后普通用户不能继续回复，但管理员仍可补充处理结果。</p>
      <div className="form-actions"><button className="button ghost" type="button" onClick={() => setConfirmingClose(false)}>取消</button><button className="button destructive" type="button" disabled={busy} onClick={() => void confirmClose()}>{busy ? "正在关闭…" : "确认关闭"}</button></div>
    </Modal>}
  </>;
}

export function levelLabel(level: Ticket["level"]) {
  return level === 0 ? "低" : level === 1 ? "中" : "高";
}

export function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
