import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { DistributorEntitlement, DistributorOrder, DistributorOrderPage, DistributorOrderQuery, DistributorQR, DistributorSettlementStatus, PlanOffer, PlanPeriod } from "../../lib/api";
import { formatDistributorCents } from "./DistributorPlansPage";

export interface DistributorOrdersAPI {
  listDistributorOrders: (query?: DistributorOrderQuery) => Promise<DistributorOrderPage>;
  getDistributorOrderQR: (tradeNo: string) => Promise<DistributorQR>;
  renewDistributorOrder: (tradeNo: string, period: PlanPeriod, idempotencyKey: string) => Promise<DistributorOrder>;
  exportDistributorOrders: (query?: DistributorOrderQuery) => Promise<Blob>;
  listPlanOffers: () => Promise<PlanOffer[]>;
}

const periodLabels: Partial<Record<PlanPeriod, string>> = {
  monthly: "月付", quarterly: "季付", half_yearly: "半年付", yearly: "年付", two_yearly: "两年付", three_yearly: "三年付", onetime: "一次性"
};

export function DistributorOrdersPage({ api }: { api: DistributorOrdersAPI }) {
  const [page, setPage] = useState<DistributorOrderPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [search, setSearch] = useState("");
  const [appliedSearch, setAppliedSearch] = useState("");
  const [settlement, setSettlement] = useState<"" | DistributorSettlementStatus>("");
  const [loading, setLoading] = useState(true);
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const [qr, setQR] = useState<DistributorQR | null>(null);
  const [qrLoading, setQRLoading] = useState<string | null>(null);
  const [renewing, setRenewing] = useState<DistributorOrder | null>(null);

  const query = useCallback((pageNumber = 1): DistributorOrderQuery => ({
    page: pageNumber, page_size: 20,
    ...(appliedSearch === "" ? {} : { search: appliedSearch }),
    ...(settlement === "" ? {} : { settlement_status: settlement })
  }), [appliedSearch, settlement]);

  const load = useCallback(async (pageNumber = 1) => {
    setLoading(true); setError("");
    try { setPage(await api.listDistributorOrders(query(pageNumber))); }
    catch (cause) { setError(messageOf(cause)); }
    finally { setLoading(false); }
  }, [api, query]);

  useEffect(() => {
    let active = true;
    void api.listDistributorOrders(query(1)).then((next) => {
      if (active) setPage(next);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api, query]);

  const submitSearch = (event: FormEvent) => { event.preventDefault(); setAppliedSearch(search.trim()); };
  const chooseSettlement = (value: string) => { setSettlement(value === "" ? "" : Number(value) as DistributorSettlementStatus); };
  const openQR = async (tradeNo: string) => {
    setQRLoading(tradeNo); setError("");
    try { setQR(await api.getDistributorOrderQR(tradeNo)); }
    catch (cause) { setError(messageOf(cause)); }
    finally { setQRLoading(null); }
  };
  const exportOrders = async () => {
    setExporting(true); setError("");
    try {
      const blob = await api.exportDistributorOrders(query(page.page));
      downloadBlob(blob, "我的分销订单.xlsx");
    } catch (cause) { setError(messageOf(cause)); }
    finally { setExporting(false); }
  };

  return <main className="page-shell distributor-order-page">
    <header className="page-header"><div><p className="eyebrow">Distributor orders</p><h1>我的订单</h1><p className="muted">原始订单与续费订单按同一份客户订阅分组展示。</p></div><button className="button secondary" disabled={loading} onClick={() => void load(page.page)}>刷新</button></header>
    <form className="resource-toolbar distributor-order-toolbar" onSubmit={submitSearch}>
      <label className="search-field">搜索订单<input type="search" maxLength={512} value={search} placeholder="订单号或客户名称" onChange={(event) => setSearch(event.target.value)} /></label>
      <button className="button secondary" type="submit" disabled={loading}>搜索</button>
      <button className="button ghost" type="button" disabled={search === "" && appliedSearch === ""} onClick={() => { setSearch(""); setAppliedSearch(""); }}>清空</button>
      <label>结算状态<select aria-label="结算状态" value={settlement} onChange={(event) => chooseSettlement(event.target.value)}><option value="">全部</option><option value="0">未结算</option><option value="1">已结算</option></select></label>
      <button className="button primary" type="button" disabled={exporting} onClick={() => void exportOrders()}>{exporting ? "正在导出…" : "导出 Excel"}</button>
    </form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载订单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的订单。</div> : <section className="resource-table-wrap" aria-label="分销订单列表"><table className="resource-table distributor-orders-table"><thead><tr><th>订单号</th><th>创建时间</th><th>客户名称</th><th>套餐 / 周期</th><th>金额</th><th>结算状态</th><th>备注</th><th>操作</th></tr></thead><tbody>{page.items.map((item) => <OrderRows key={item.order.id} item={item} expanded={expanded.has(item.order.id)} qrLoading={qrLoading} onToggle={() => setExpanded((current) => toggleSet(current, item.order.id))} onQR={openQR} onRenew={() => setRenewing(item)} />)}</tbody></table></section>}
    {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary" disabled={loading || page.page <= 1} onClick={() => void load(page.page - 1)}>上一页</button><span>第 {page.page} 页 · 共 {page.total} 条</span><button className="button secondary" disabled={loading || page.page * page.page_size >= page.total} onClick={() => void load(page.page + 1)}>下一页</button></div>}
    {qr !== null && <SubscriptionQRDialog qr={qr} onClose={() => setQR(null)} />}
    {renewing !== null && <RenewDialog api={api} order={renewing} onClose={() => setRenewing(null)} onRenewed={() => { setRenewing(null); void load(page.page); }} />}
  </main>;
}

function OrderRows({ item, expanded, qrLoading, onToggle, onQR, onRenew }: { item: DistributorOrder; expanded: boolean; qrLoading: string | null; onToggle: () => void; onQR: (tradeNo: string) => Promise<void>; onRenew: () => void }) {
  const rootTradeNo = item.subscription.trade_no;
  return <>
    <tr className={item.is_subscription_origin ? "distributor-origin-row" : "distributor-renewal-row"}>
      <td data-label="订单号"><strong className="monospace">{item.order.trade_no}</strong><small>{item.is_subscription_origin ? "新购" : "续费"}</small>{!item.is_subscription_origin && <small className="muted">原始订单：{rootTradeNo}</small>}</td>
      <td data-label="创建时间">{formatDate(item.order.created_at)}</td>
      <td data-label="客户名称">{item.subscription.customer_name ?? "-"}</td>
      <td data-label="套餐 / 周期"><strong>{item.plan_name}</strong><small className="muted">{periodLabels[item.order.period] ?? item.order.period}</small></td>
      <td data-label="金额">¥{formatDistributorCents(item.order.total_amount)}</td>
      <td data-label="结算状态"><span className={`status-badge ${item.settlement_status === 1 ? "enabled" : "warning"}`}>{item.settlement_status === 1 ? "已结算" : "未结算"}</span></td>
      <td data-label="备注"><span className="distributor-order-remark">{item.subscription.remark ?? "—"}</span></td>
      <td data-label="操作"><div className="row-actions">{item.can_view_subscription_qr && <button className="button ghost compact" disabled={qrLoading !== null} onClick={() => void onQR(rootTradeNo)}>{qrLoading === rootTradeNo ? "读取中…" : "订阅二维码"}</button>}{item.is_subscription_origin && <button className="button ghost compact" aria-expanded={expanded} onClick={onToggle}>{expanded ? "隐藏权益" : "查看权益"}</button>}{item.can_renew && <button className="button primary compact" onClick={onRenew}>续费</button>}</div></td>
    </tr>
    {item.is_subscription_origin && expanded && <tr className="distributor-entitlement-row"><td colSpan={8}><EntitlementDetails item={item} /></td></tr>}
  </>;
}

function EntitlementDetails({ item }: { item: DistributorOrder }) {
  const value = item.subscription_entitlement;
  const devices = item.bound_devices ?? [];
  const connection = item.subscription.connected_at === null ? "尚未连接" : `${item.subscription.connected_node_name ?? "未知节点"} · ${formatDate(item.subscription.connected_at)}`;
  return <div className="distributor-entitlement"><strong>当前订阅权益</strong><dl><Detail label="套餐" value={value.plan_name} /><Detail label="总流量" value={formatTraffic(value.transfer_enable)} /><Detail label="已用流量" value={formatTraffic(value.used_traffic)} /><Detail label="剩余流量" value={formatTraffic(value.remaining_traffic)} /><Detail label="到期时间" value={value.expired_at === null ? "长期有效" : formatDate(value.expired_at)} /><Detail label="限速" value={formatLimit(value.speed_limit, "Mbps")} /><Detail label="设备限制" value={formatLimit(value.device_limit, "台")} /><Detail label="连接状态" value={connection} /><Detail label="已绑定设备" value={!item.subscription.hwid_enabled ? "未启用设备绑定" : devices.length === 0 ? "尚未绑定设备" : devices.join("、")} /></dl></div>;
}

function SubscriptionQRDialog({ qr, onClose }: { qr: DistributorQR; onClose: () => void }) {
  const devices = qr.hwid_devices ?? [];
  return <Modal title="订阅二维码" onClose={onClose}><div className="modal-header"><h2>订阅二维码</h2><button className="icon-button" aria-label="关闭订阅二维码" onClick={onClose}>×</button></div><p className="muted">续费不会改变此二维码、订阅凭据或已绑定设备。</p><img className="distributor-qr" src={qr.qr_code} alt="订阅二维码" /><div className="detail-list"><div><span>原始订单</span><strong className="monospace">{qr.trade_no}</strong></div><div><span>设备状态</span><strong>{qr.hwid_enabled ? devices.length > 0 ? devices.join("、") : "尚未绑定设备" : "未启用设备绑定"}</strong></div></div><div className="form-actions"><a className="button secondary" href={qr.qr_code} download={`subscription-${qr.trade_no}.svg`}>下载图片</a><button className="button primary" onClick={onClose}>关闭</button></div></Modal>;
}

function RenewDialog({ api, order, onClose, onRenewed }: { api: DistributorOrdersAPI; order: DistributorOrder; onClose: () => void; onRenewed: (value: DistributorOrder) => void }) {
  const [plan, setPlan] = useState<PlanOffer | null>(null);
  const [period, setPeriod] = useState<PlanPeriod>(order.order.period);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const idempotencyKey = useRef(createIdempotencyKey());
  useEffect(() => {
    let active = true;
    void api.listPlanOffers().then((plans) => {
      if (!active) return;
      const found = plans.find((item) => item.id === order.subscription_entitlement.plan_id) ?? null;
      setPlan(found);
      const options = found === null ? [] : renewalPeriods(found);
      if (!options.some(([key]) => key === order.order.period)) setPeriod(options[0]?.[0] ?? "monthly");
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, order.order.period, order.subscription_entitlement.plan_id]);
  const options = useMemo(() => plan === null ? [] : renewalPeriods(plan), [plan]);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { onRenewed(await api.renewDistributorOrder(order.order.trade_no, period, idempotencyKey.current)); }
    catch (cause) { setError(messageOf(cause)); setBusy(false); }
  };
  return <Modal title="续费现有订阅" onClose={onClose}><div className="modal-header"><h2>续费现有订阅</h2><button className="icon-button" aria-label="关闭续费现有订阅" onClick={onClose}>×</button></div><p className="muted">续费后订阅链接、二维码、UUID 和已绑定设备保持不变。</p>{loading ? <div className="alert" role="status">正在加载续费周期…</div> : <form className="form-stack" onSubmit={(event) => void submit(event)}><div className="detail-list"><div><span>原始订单</span><strong className="monospace">{order.subscription.trade_no}</strong></div><div><span>当前到期</span><strong>{order.subscription_entitlement.expired_at === null ? "长期有效" : formatDate(order.subscription_entitlement.expired_at)}</strong></div></div><label>续费周期<select value={period} onChange={(event) => setPeriod(event.target.value as PlanPeriod)}>{options.map(([key, cents]) => <option key={key} value={key}>{periodLabels[key] ?? key} · ¥{formatDistributorCents(cents)}</option>)}</select></label>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" disabled={busy} onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={busy || options.length === 0}>{busy ? "正在续费…" : "确认续费"}</button></div></form>}</Modal>;
}

function renewalPeriods(plan: PlanOffer): Array<[PlanPeriod, number]> { return Object.entries(plan.prices).filter((entry): entry is [PlanPeriod, number] => entry[1] !== undefined && entry[1] > 0 && !["onetime", "reset_traffic"].includes(entry[0])); }
function createIdempotencyKey(): string { return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}-${Math.random().toString(16).slice(2)}`; }
function toggleSet(current: Set<number>, id: number): Set<number> { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next; }
function Detail({ label, value }: { label: string; value: string }) { return <div><dt>{label}</dt><dd>{value}</dd></div>; }
function formatTraffic(value: number): string { if (value < 1024) return `${value} B`; const units = ["KiB", "MiB", "GiB", "TiB", "PiB"]; let amount = value; let index = -1; do { amount /= 1024; index++; } while (amount >= 1024 && index < units.length - 1); return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[index]}`; }
function formatLimit(value: number, unit: string): string { return value === 0 ? "不限" : `${value} ${unit}`; }
function formatDate(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false }); }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "分销订单请求失败"; }
function downloadBlob(blob: Blob, filename: string) { const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url); }

export function distributorEntitlementForTest(value: DistributorEntitlement): DistributorEntitlement { return value; }
