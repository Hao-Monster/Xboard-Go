import { useState } from "react";

import { Modal } from "../../components/Overlay";
import type { Ticket } from "../../lib/api";

export type WithdrawalAction = "approved" | "paid" | "rejected";
export type TransitionWithdrawal = (id: number, status: WithdrawalAction, reference?: string) => Promise<Ticket>;

const states = { pending: "待审批", approved: "待付款", paid: "已付款", rejected: "已拒绝（金额已退回）" };
const actions = { approved: "批准提现", paid: "确认已付款", rejected: "拒绝并退回佣金" };

export function CommissionWithdrawalPanel({ ticket, transition, onUpdated }: {
  ticket: Ticket;
  transition?: TransitionWithdrawal;
  onUpdated: (value: Ticket) => void;
}) {
  const [action, setAction] = useState<WithdrawalAction | null>(null);
  const [reference, setReference] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const withdrawal = ticket.withdrawal;
  if (!withdrawal) return null;

  const confirm = async () => {
    if (action === null || transition === undefined || busy) return;
    setBusy(true);
    setError("");
    try {
      onUpdated(await transition(ticket.id, action, action === "paid" ? reference.trim() : ""));
      setAction(null);
      setReference("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "提现操作失败，请刷新后重试");
    } finally {
      setBusy(false);
    }
  };

  return <section className="system-section" aria-label="提现账本">
    <h3>佣金提现 · {states[withdrawal.status]}</h3>
    <p>申请金额：¥{(withdrawal.amount / 100).toFixed(2)} · {withdrawal.method}</p>
    <p>提现账号：{withdrawal.account}</p>
    {withdrawal.payment_reference && <p>付款凭据：{withdrawal.payment_reference}</p>}
    <p className="small muted">申请金额已从可用佣金中冻结。关闭或回复工单不会改变提现状态。</p>
    {transition && <div className="form-actions">
      {withdrawal.status === "pending" && <button className="button primary" onClick={() => { setError(""); setAction("approved"); }}>{actions.approved}</button>}
      {withdrawal.status === "approved" && <button className="button primary" onClick={() => { setError(""); setAction("paid"); }}>{actions.paid}</button>}
      {(withdrawal.status === "pending" || withdrawal.status === "approved") && <button className="button destructive" onClick={() => { setError(""); setAction("rejected"); }}>{actions.rejected}</button>}
    </div>}
    {action && <Modal title={actions[action]} onClose={() => { if (!busy) setAction(null); }}>
      <div className="modal-header"><h2>{actions[action]}</h2></div>
      <p>提现金额：¥{(withdrawal.amount / 100).toFixed(2)}</p>
      {action === "approved" && <p>审批后金额继续冻结，实际付款完成后需单独确认并填写付款凭据。</p>}
      {action === "rejected" && <p>确认拒绝后，冻结金额将退回该用户的可用佣金。</p>}
      {action === "paid" && <>
        <p>请仅在已完成实际付款后确认。本操作记录付款事实，不会发起转账，确认后不可退回冻结金额。</p>
        <label>付款凭据<input value={reference} maxLength={128} autoComplete="off" onChange={(event) => setReference(event.target.value)} /></label>
      </>}
      {error && <p className="alert error" role="alert">{error}</p>}
      <div className="form-actions"><button className="button secondary" disabled={busy} onClick={() => setAction(null)}>取消</button><button className="button primary" disabled={busy || action === "paid" && reference.trim() === ""} onClick={() => void confirm()}>{busy ? "正在处理…" : "确认"}</button></div>
    </Modal>}
  </section>;
}
