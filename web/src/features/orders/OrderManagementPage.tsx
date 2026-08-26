import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminOrder, AdminOrderPage, AdminOrderQuery, AssignOrderInput, Order, OrderStatus, OrderType, Plan, PlanPeriod } from "../../lib/api";
import { formatCents, formatDate, orderStatusLabel, orderTypeLabel, periodLabel } from "./UserOrdersPage";

export interface OrderManagementAPI {
  listAdminOrders: (query?: AdminOrderQuery) => Promise<AdminOrderPage>;
  getAdminOrder: (tradeNo: string) => Promise<AdminOrder>;
  assignOrder: (input: AssignOrderInput) => Promise<Order>;
  paidAdminOrder: (tradeNo: string) => Promise<Order>;
  cancelAdminOrder: (tradeNo: string) => Promise<Order>;
  listPlans: () => Promise<Plan[]>;
}

const periods: Array<[PlanPeriod, string]> = [["monthly", "月付"], ["quarterly", "季付"], ["half_yearly", "半年付"], ["yearly", "年付"], ["two_yearly", "两年付"], ["three_yearly", "三年付"], ["onetime", "一次性"], ["reset_traffic", "流量重置包"]];

export function OrderManagementPage({ api }: { api: OrderManagementAPI }) {
  const [page, setPage] = useState<AdminOrderPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [plans, setPlans] = useState<Plan[]>([]);
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [period, setPeriod] = useState("");
  const [applied, setApplied] = useState<AdminOrderQuery>({ page: 1, page_size: 20 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [assigning, setAssigning] = useState(false);
  const [selected, setSelected] = useState<AdminOrder | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = useCallback(async (query: AdminOrderQuery) => {
    setLoading(true);
    setError("");
    try {
      setPage(await api.listAdminOrders(query));
      setApplied(query);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    const query = { page: 1, page_size: 20 } satisfies AdminOrderQuery;
    void Promise.all([api.listAdminOrders(query), api.listPlans()]).then(([orders, nextPlans]) => {
      if (!active) return;
      setPage(orders);
      setPlans(nextPlans);
      setApplied(query);
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const query: AdminOrderQuery = { page: 1, page_size: 20 };
    if (search.trim() !== "") query.query = search.trim();
    if (status !== "") query.status = Number(status) as OrderStatus;
    if (type !== "") query.type = Number(type) as OrderType;
    if (period !== "") query.period = period as PlanPeriod;
    void load(query);
  };

  const open = async (tradeNo: string) => {
    setDetailLoading(true);
    setError("");
    try { setSelected(await api.getAdminOrder(tradeNo)); } catch (cause) { setError(messageOf(cause)); } finally { setDetailLoading(false); }
  };

  const refreshCurrent = () => load(applied);

  return <main className="page-shell resource-page order-page">
    <header className="page-header"><div><p className="eyebrow">Finance</p><h1>订单管理</h1><p className="muted">查询订单、为用户分配订阅，并对待支付订单执行人工开通或取消。</p></div><button className="button primary" onClick={() => setAssigning(true)}>添加订单</button></header>
    <form className="ticket-filter-bar" onSubmit={submit}><label className="search-field">搜索订单<input type="search" aria-label="搜索订单" placeholder="订单号或用户邮箱" value={search} maxLength={128} onChange={(event) => setSearch(event.target.value)} /></label><label>订单状态<select value={status} onChange={(event) => setStatus(event.target.value)}><option value="">全部</option><option value="0">待支付</option><option value="1">开通中</option><option value="2">已取消</option><option value="3">已完成</option><option value="4">已折抵</option></select></label><label>订单类型<select value={type} onChange={(event) => setType(event.target.value)}><option value="">全部</option><option value="1">新购</option><option value="2">续费</option><option value="3">升级</option><option value="4">流量重置</option></select></label><label>付款周期<select value={period} onChange={(event) => setPeriod(event.target.value)}><option value="">全部</option>{periods.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><button className="button secondary" type="submit" disabled={loading}>查询订单</button></form>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void refreshCurrent()}>重试</button></div>}
    {detailLoading && <div className="alert" role="status">正在读取订单详情…</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载订单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的订单。</div> : <section className="resource-table-wrap"><table className="resource-table"><thead><tr><th>订单号</th><th>用户</th><th>套餐</th><th>类型 / 周期</th><th>金额</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{page.items.map((order) => <tr key={order.id}><td data-label="订单号"><strong className="monospace">{order.trade_no}</strong></td><td data-label="用户">{order.user_email}</td><td data-label="套餐">{order.plan_name}</td><td data-label="类型 / 周期">{orderTypeLabel(order.type)}<small className="muted">{periodLabel(order.period)}</small></td><td data-label="金额">¥{formatCents(order.total_amount)}</td><td data-label="状态"><span className={`order-status status-${order.status}`}>{orderStatusLabel(order.status)}</span></td><td data-label="创建时间">{formatDate(order.created_at)}</td><td data-label="操作"><button className="button secondary compact" aria-label={`查看订单：${order.trade_no}`} onClick={() => void open(order.trade_no)}>订单详情</button></td></tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>上一页</button><span>第 {page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>下一页</button></div>}
    </section>}
    {assigning && <AssignOrderDialog api={api} plans={plans} onClose={() => setAssigning(false)} onCreated={() => { setAssigning(false); void load({ page: 1, page_size: 20 }); }} />}
    {selected !== null && <AdminOrderDetail api={api} order={selected} onClose={() => setSelected(null)} onUpdated={(order) => { setSelected((current) => current === null ? null : { ...current, ...order }); void refreshCurrent(); }} />}
  </main>;
}

function AssignOrderDialog({ api, plans, onClose, onCreated }: { api: OrderManagementAPI; plans: Plan[]; onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState("");
  const [planID, setPlanID] = useState(plans[0]?.id ?? 0);
  const [period, setPeriod] = useState<PlanPeriod>("monthly");
  const [amount, setAmount] = useState("0.00");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const selectedPlan = plans.find((plan) => plan.id === planID);
  const availablePeriods = periods.filter(([value]) => selectedPlan?.prices[value] !== undefined);
  const selectablePeriods = availablePeriods.length === 0 ? periods : availablePeriods;
  const effectivePeriod = selectablePeriods.some(([value]) => value === period) ? period : selectablePeriods[0]?.[0] ?? "monthly";
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api.assignOrder({ email: email.trim(), plan_id: planID, period: effectivePeriod, total_amount: parseCNY(amount) });
      onCreated();
    } catch (cause) {
      setError(messageOf(cause));
      setSaving(false);
    }
  };
  const selectPlan = (nextID: number) => {
    setPlanID(nextID);
    const next = plans.find((plan) => plan.id === nextID);
    const first = periods.find(([value]) => next?.prices[value] !== undefined);
    if (first !== undefined) setPeriod(first[0]);
  };
  return <Modal title="添加订单" onClose={onClose}><div className="modal-header"><h2>添加订单</h2><button className="icon-button" aria-label="关闭添加订单" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>用户邮箱<input type="email" required maxLength={254} value={email} onChange={(event) => setEmail(event.target.value)} /></label><label>订阅套餐<select required value={planID || ""} onChange={(event) => selectPlan(Number(event.target.value))}><option value="" disabled>请选择套餐</option>{plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name}</option>)}</select></label><label>付款周期<select value={effectivePeriod} onChange={(event) => setPeriod(event.target.value as PlanPeriod)}>{selectablePeriods.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><label>支付金额（CNY）<input inputMode="decimal" required value={amount} onChange={(event) => setAmount(event.target.value)} /></label><p className="small muted">管理员分配订单沿用旧 Xboard 规则：金额由管理员明确指定，不自动使用套餐标价或用户余额。</p>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving || planID === 0}>{saving ? "正在创建…" : "创建订单"}</button></div></form></Modal>;
}

function AdminOrderDetail({ api, order, onClose, onUpdated }: { api: OrderManagementAPI; order: AdminOrder; onClose: () => void; onUpdated: (order: Order) => void }) {
  const [busy, setBusy] = useState<"paid" | "cancel" | "">("");
  const [error, setError] = useState("");
  const act = async (action: "paid" | "cancel") => {
    if (busy !== "") return;
    setBusy(action); setError("");
    try { onUpdated(action === "paid" ? await api.paidAdminOrder(order.trade_no) : await api.cancelAdminOrder(order.trade_no)); }
    catch (cause) { setError(messageOf(cause)); setBusy(""); }
  };
  return <Modal title="订单详情" onClose={onClose}><div className="modal-header"><h2>订单详情</h2><button className="icon-button" aria-label="关闭订单详情" onClick={onClose}>×</button></div><div className="detail-list order-detail-list"><div><span>订单号</span><strong className="monospace">{order.trade_no}</strong></div><div><span>用户</span><strong>{order.user_email}</strong></div><div><span>套餐</span><strong>{order.plan_name}</strong></div><div><span>类型 / 周期</span><strong>{orderTypeLabel(order.type)} · {periodLabel(order.period)}</strong></div><div><span>状态</span><strong>{orderStatusLabel(order.status)}</strong></div><div><span>订单金额</span><strong>¥{formatCents(order.total_amount)}</strong></div><div><span>余额支付</span><strong>¥{formatCents(order.balance_amount)}</strong></div><div><span>创建时间</span><strong>{formatDate(order.created_at)}</strong></div></div>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>关闭</button>{order.status === 0 && <button className="button secondary" disabled={busy !== ""} onClick={() => void act("cancel")}>{busy === "cancel" ? "正在取消…" : "取消订单"}</button>}{order.status === 0 && <button className="button primary" disabled={busy !== ""} onClick={() => void act("paid")}>{busy === "paid" ? "正在开通…" : "标记已支付并开通"}</button>}</div></Modal>;
}

function parseCNY(value: string): number {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim());
  if (match === null || match[1] === undefined) throw new Error("支付金额格式无效");
  const cents = BigInt(match[1]) * 100n + BigInt((match[2] ?? "").padEnd(2, "0") || "0");
  if (cents > 9_000_000_000_000_000n) throw new Error("支付金额超出范围");
  return Number(cents);
}

function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "订单请求失败"; }
