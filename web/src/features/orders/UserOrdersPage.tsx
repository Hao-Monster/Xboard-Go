import { useCallback, useEffect, useState } from "react";

import { Modal } from "../../components/Overlay";
import type { Order, OrderStatus } from "../../lib/api";

export interface UserOrdersAPI {
  listOrders: (status?: OrderStatus, limit?: number) => Promise<Order[]>;
  getOrder: (tradeNo: string) => Promise<Order>;
  checkoutOrder: (tradeNo: string) => Promise<Order>;
  cancelOrder: (tradeNo: string) => Promise<Order>;
}

export function UserOrdersPage({ api, initialTradeNo = null, onInitialHandled }: { api: UserOrdersAPI; initialTradeNo?: string | null; onInitialHandled?: () => void }) {
  const [orders, setOrders] = useState<Order[]>([]);
  const [status, setStatus] = useState<"" | OrderStatus>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<Order | null>(null);
  const [detailLoading, setDetailLoading] = useState(initialTradeNo !== null);

  const load = useCallback(async (nextStatus: "" | OrderStatus = status) => {
    setLoading(true);
    setError("");
    try {
      setOrders(await api.listOrders(nextStatus === "" ? undefined : nextStatus, 100));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api, status]);

  const open = useCallback(async (tradeNo: string) => {
    setDetailLoading(true);
    setError("");
    try {
      setSelected(await api.getOrder(tradeNo));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setDetailLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    void api.listOrders(undefined, 100).then((result) => { if (active) setOrders(result); })
      .catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  useEffect(() => {
    if (initialTradeNo === null) return;
    let active = true;
    void api.getOrder(initialTradeNo).then((result) => { if (active) setSelected(result); })
      .catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) { setDetailLoading(false); onInitialHandled?.(); } });
    return () => { active = false; };
  }, [api, initialTradeNo, onInitialHandled]);

  const replace = (order: Order) => {
    setOrders((current) => current.map((item) => item.id === order.id ? { ...item, ...order } : item));
    setSelected(order);
  };

  return <main className="page-shell resource-page order-page">
    <header className="page-header"><div><p className="eyebrow">Orders</p><h1>我的订单</h1><p className="muted">查看订单金额、支付状态与套餐开通结果。</p></div><button className="button secondary" disabled={loading} onClick={() => void load()}>{loading ? "正在刷新…" : "刷新"}</button></header>
    <div className="ticket-status-tabs" role="group" aria-label="订单状态筛选">{([ ["", "全部"], [0, "待支付"], [1, "开通中"], [2, "已取消"], [3, "已完成"], [4, "已折抵"] ] as const).map(([value, label]) => <button key={String(value)} className={`button ${status === value ? "primary" : "secondary"}`} onClick={() => { setStatus(value); void load(value); }}>{label}</button>)}</div>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load()}>重试</button></div>}
    {detailLoading && <div className="alert" role="status">正在读取订单详情…</div>}
    {loading && orders.length === 0 ? <div className="empty-card">正在加载订单…</div> : orders.length === 0 ? <div className="empty-card">没有符合条件的订单。</div> : <section className="resource-table-wrap" aria-label="订单列表"><table className="resource-table"><thead><tr><th>订单号</th><th>套餐</th><th>类型 / 周期</th><th>金额</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead><tbody>{orders.map((order) => <tr key={order.id}>
      <td data-label="订单号"><strong className="monospace">{order.trade_no}</strong></td><td data-label="套餐">{order.plan?.name ?? `套餐 #${order.plan_id}`}</td><td data-label="类型 / 周期">{orderTypeLabel(order.type)}<small className="muted">{periodLabel(order.period)}</small></td><td data-label="金额">¥{formatCents(order.total_amount)}{order.balance_amount > 0 && <small className="muted">余额支付 ¥{formatCents(order.balance_amount)}</small>}</td><td data-label="状态"><span className={`order-status status-${order.status}`}>{orderStatusLabel(order.status)}</span></td><td data-label="创建时间">{formatDate(order.created_at)}</td><td data-label="操作"><button className="button secondary compact" aria-label={`查看订单：${order.trade_no}`} onClick={() => void open(order.trade_no)}>订单详情</button></td>
    </tr>)}</tbody></table></section>}
    {selected !== null && <OrderDetailDialog api={api} order={selected} onUpdated={replace} onClose={() => setSelected(null)} />}
  </main>;
}

function OrderDetailDialog({ api, order, onUpdated, onClose }: { api: UserOrdersAPI; order: Order; onUpdated: (order: Order) => void; onClose: () => void }) {
  const [busy, setBusy] = useState<"checkout" | "cancel" | "">("");
  const [error, setError] = useState("");
  const act = async (action: "checkout" | "cancel") => {
    if (busy !== "") return;
    setBusy(action);
    setError("");
    try {
      onUpdated(action === "checkout" ? await api.checkoutOrder(order.trade_no) : await api.cancelOrder(order.trade_no));
    } catch (cause) {
      setError(messageOf(cause));
      setBusy("");
    }
  };
  return <Modal title="订单详情" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Order detail</p><h2>订单详情</h2></div><button className="icon-button" aria-label="关闭订单详情" onClick={onClose}>×</button></div>
    <div className="detail-list order-detail-list"><div><span>订单号</span><strong className="monospace">{order.trade_no}</strong></div><div><span>套餐</span><strong>{order.plan?.name ?? `#${order.plan_id}`}</strong></div><div><span>类型</span><strong>{orderTypeLabel(order.type)} · {periodLabel(order.period)}</strong></div><div><span>状态</span><strong>{orderStatusLabel(order.status)}</strong></div><div><span>套餐标价</span><strong>¥{formatCents(order.original_amount)}</strong></div>{order.discount_amount > 0 && <div><span>优惠</span><strong>-¥{formatCents(order.discount_amount)}</strong></div>}{order.surplus_amount > 0 && <div><span>套餐折抵</span><strong>-¥{formatCents(order.surplus_amount)}</strong></div>}{order.balance_amount > 0 && <div><span>余额支付</span><strong>-¥{formatCents(order.balance_amount)}</strong></div>}<div><span>待支付</span><strong>¥{formatCents(order.total_amount + (order.handling_amount ?? 0))}</strong></div><div><span>创建时间</span><strong>{formatDate(order.created_at)}</strong></div>{order.paid_at !== null && <div><span>支付时间</span><strong>{formatDate(order.paid_at)}</strong></div>}</div>
    {order.status === 0 && order.total_amount > 0 && <div className="alert" role="status">当前没有可用支付方式。你可以关闭订单，待支付方式配置后重新下单。</div>}
    {order.status === 0 && order.total_amount === 0 && <div className="alert success" role="status">该订单无需在线支付，可直接开通。</div>}
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" onClick={onClose}>关闭</button>{order.status === 0 && <button className="button secondary" disabled={busy !== ""} onClick={() => void act("cancel")}>{busy === "cancel" ? "正在关闭…" : "关闭订单"}</button>}{order.status === 0 && order.total_amount === 0 && <button className="button primary" disabled={busy !== ""} onClick={() => void act("checkout")}>{busy === "checkout" ? "正在开通…" : "立即开通"}</button>}</div>
  </Modal>;
}

export function orderStatusLabel(value: OrderStatus): string { return ["待支付", "开通中", "已取消", "已完成", "已折抵"][value] ?? "未知"; }
export function orderTypeLabel(value: Order["type"]): string { return ({ 1: "新购", 2: "续费", 3: "升级", 4: "流量重置" } as const)[value]; }
export function periodLabel(value: Order["period"]): string { return ({ monthly: "月付", quarterly: "季付", half_yearly: "半年付", yearly: "年付", two_yearly: "两年付", three_yearly: "三年付", onetime: "一次性", reset_traffic: "流量重置包" } as const)[value]; }
export function formatCents(value: number): string { return `${Math.trunc(value / 100)}.${String(value % 100).padStart(2, "0")}`; }
export function formatDate(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false }); }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "订单请求失败"; }
