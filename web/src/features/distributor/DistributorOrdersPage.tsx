import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { DistributorEntitlement, DistributorOrder, DistributorOrderPage, DistributorOrderQuery, DistributorQR, DistributorSettlementStatus, PlanOffer, PlanPeriod } from "../../lib/api";
import { secureRandomUUID } from "../../lib/random";
import { formatDistributorCents } from "./DistributorPlansPage";
import { distributorCloseLabel, distributorCopy, distributorPeriodLabels, type DistributorCopy, type DistributorLocale } from "./locale";

export interface DistributorOrdersAPI {
  listDistributorOrders: (query?: DistributorOrderQuery) => Promise<DistributorOrderPage>;
  getDistributorOrderQR: (tradeNo: string) => Promise<DistributorQR>;
  renewDistributorOrder: (tradeNo: string, period: PlanPeriod, idempotencyKey: string) => Promise<DistributorOrder>;
  exportDistributorOrders: (query?: DistributorOrderQuery) => Promise<Blob>;
  listPlanOffers: () => Promise<PlanOffer[]>;
}

export function DistributorOrdersPage({ api, locale = "zh-CN" }: { api: DistributorOrdersAPI; locale?: DistributorLocale }) {
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
  const copy = distributorCopy[locale];

  const query = useCallback((pageNumber = 1): DistributorOrderQuery => ({
    page: pageNumber, page_size: 20,
    ...(appliedSearch === "" ? {} : { search: appliedSearch }),
    ...(settlement === "" ? {} : { settlement_status: settlement })
  }), [appliedSearch, settlement]);

  const load = useCallback(async (pageNumber = 1) => {
    setLoading(true); setError("");
    try { setPage(await api.listDistributorOrders(query(pageNumber))); }
    catch (cause) { setError(messageOf(cause, locale)); }
    finally { setLoading(false); }
  }, [api, locale, query]);

  useEffect(() => {
    let active = true;
    void api.listDistributorOrders(query(1)).then((next) => {
      if (active) setPage(next);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause, locale));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api, locale, query]);

  const submitSearch = (event: FormEvent) => { event.preventDefault(); setAppliedSearch(search.trim()); };
  const chooseSettlement = (value: string) => { setSettlement(value === "" ? "" : Number(value) as DistributorSettlementStatus); };
  const openQR = async (tradeNo: string) => {
    setQRLoading(tradeNo); setError("");
    try { setQR(await api.getDistributorOrderQR(tradeNo)); }
    catch (cause) { setError(messageOf(cause, locale)); }
    finally { setQRLoading(null); }
  };
  const exportOrders = async () => {
    setExporting(true); setError("");
    try {
      const blob = await api.exportDistributorOrders(query(page.page));
      downloadBlob(blob, locale === "en-US" ? "my-distributor-orders.xlsx" : "我的分销订单.xlsx");
    } catch (cause) { setError(messageOf(cause, locale)); }
    finally { setExporting(false); }
  };

  return <main className="page-shell distributor-order-page">
    <header className="page-header"><div><p className="eyebrow">Distributor orders</p><h1>{copy.ordersTitle}</h1><p className="muted">{copy.ordersSubtitle}</p></div><button className="button secondary" disabled={loading} onClick={() => void load(page.page)}>{copy.refresh}</button></header>
    <form className="resource-toolbar distributor-order-toolbar" onSubmit={submitSearch}>
      <label className="search-field">{copy.searchOrders}<input type="search" maxLength={512} value={search} placeholder={copy.orderSearchPlaceholder} onChange={(event) => setSearch(event.target.value)} /></label>
      <button className="button secondary" type="submit" disabled={loading}>{copy.search}</button>
      <button className="button ghost" type="button" disabled={search === "" && appliedSearch === ""} onClick={() => { setSearch(""); setAppliedSearch(""); }}>{copy.clear}</button>
      <label>{copy.settlement}<select aria-label={copy.settlement} value={settlement} onChange={(event) => chooseSettlement(event.target.value)}><option value="">{copy.all}</option><option value="0">{copy.unsettled}</option><option value="1">{copy.settled}</option></select></label>
      <button className="button primary" type="button" disabled={exporting} onClick={() => void exportOrders()}>{exporting ? copy.exporting : copy.exportExcel}</button>
    </form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">{copy.loadingOrders}</div> : page.items.length === 0 ? <div className="empty-card">{copy.noOrders}</div> : <section className="resource-table-wrap" aria-label={copy.orderList}><table className="resource-table distributor-orders-table"><thead><tr><th>{copy.orderNo}</th><th>{copy.created}</th><th>{copy.customerName}</th><th>{copy.planPeriod}</th><th>{copy.amount}</th><th>{copy.settlement}</th><th>{copy.remark}</th><th>{copy.actions}</th></tr></thead><tbody>{page.items.map((item) => <OrderRows key={item.order.id} item={item} expanded={expanded.has(item.order.id)} qrLoading={qrLoading} locale={locale} onToggle={() => setExpanded((current) => toggleSet(current, item.order.id))} onQR={openQR} onRenew={() => setRenewing(item)} />)}</tbody></table></section>}
    {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary" disabled={loading || page.page <= 1} onClick={() => void load(page.page - 1)}>{copy.previous}</button><span>{locale === "en-US" ? `Page ${page.page} · ${page.total} total` : `第 ${page.page} 页 · 共 ${page.total} 条`}</span><button className="button secondary" disabled={loading || page.page * page.page_size >= page.total} onClick={() => void load(page.page + 1)}>{copy.next}</button></div>}
    {qr !== null && <SubscriptionQRDialog qr={qr} locale={locale} onClose={() => setQR(null)} />}
    {renewing !== null && <RenewDialog api={api} order={renewing} locale={locale} onClose={() => setRenewing(null)} onRenewed={() => { setRenewing(null); void load(page.page); }} />}
  </main>;
}

function OrderRows({ item, expanded, qrLoading, locale, onToggle, onQR, onRenew }: { item: DistributorOrder; expanded: boolean; qrLoading: string | null; locale: DistributorLocale; onToggle: () => void; onQR: (tradeNo: string) => Promise<void>; onRenew: () => void }) {
  const rootTradeNo = item.subscription.trade_no;
  const copy = distributorCopy[locale];
  const periodLabels = distributorPeriodLabels(locale);
  return <>
    <tr className={item.is_subscription_origin ? "distributor-origin-row" : "distributor-renewal-row"}>
      <td data-label={copy.orderNo}><strong className="monospace">{item.order.trade_no}</strong><small>{item.is_subscription_origin ? copy.newPurchase : copy.renewalOrder}</small>{!item.is_subscription_origin && <small className="muted">{copy.originalOrder}: {rootTradeNo}</small>}</td>
      <td data-label={copy.created}>{formatDate(item.order.created_at, locale)}</td>
      <td data-label={copy.customerName}>{item.subscription.customer_name ?? "-"}</td>
      <td data-label={copy.planPeriod}><strong>{item.plan_name}</strong><small className="muted">{periodLabels[item.order.period] ?? item.order.period}</small></td>
      <td data-label={copy.amount}>¥{formatDistributorCents(item.order.total_amount)}</td>
      <td data-label={copy.settlement}><span className={`status-badge ${item.settlement_status === 1 ? "enabled" : "warning"}`}>{item.settlement_status === 1 ? copy.settled : copy.unsettled}</span></td>
      <td data-label={copy.remark}><span className="distributor-order-remark">{item.subscription.remark ?? "—"}</span></td>
      <td data-label={copy.actions}><div className="row-actions">{item.can_view_subscription_qr && <button className="button ghost compact" disabled={qrLoading !== null} onClick={() => void onQR(rootTradeNo)}>{qrLoading === rootTradeNo ? copy.reading : copy.subscriptionQR}</button>}{item.is_subscription_origin && <button className="button ghost compact" aria-expanded={expanded} onClick={onToggle}>{expanded ? copy.hideEntitlement : copy.viewEntitlement}</button>}{item.can_renew && <button className="button primary compact" onClick={onRenew}>{copy.renew}</button>}</div></td>
    </tr>
    {item.is_subscription_origin && expanded && <tr className="distributor-entitlement-row"><td colSpan={8}><EntitlementDetails item={item} locale={locale} /></td></tr>}
  </>;
}

function EntitlementDetails({ item, locale }: { item: DistributorOrder; locale: DistributorLocale }) {
  const value = item.subscription_entitlement;
  const devices = item.bound_devices ?? [];
  const copy = distributorCopy[locale];
  const connection = item.subscription.connected_at === null ? copy.notConnected : `${item.subscription.connected_node_name ?? copy.unknownNode} · ${formatDate(item.subscription.connected_at, locale)}`;
  return <div className="distributor-entitlement"><strong>{copy.entitlement}</strong><dl><Detail label={copy.plan} value={value.plan_name} /><Detail label={copy.totalTraffic} value={formatTraffic(value.transfer_enable)} /><Detail label={copy.usedTraffic} value={formatTraffic(value.used_traffic)} /><Detail label={copy.remainingTraffic} value={formatTraffic(value.remaining_traffic)} /><Detail label={copy.expiresAt} value={value.expired_at === null ? copy.permanent : formatDate(value.expired_at, locale)} /><Detail label={copy.speedLimit} value={formatLimit(value.speed_limit, "Mbps", copy)} /><Detail label={copy.deviceLimit} value={formatLimit(value.device_limit, locale === "zh-CN" ? "台" : "devices", copy)} /><Detail label={copy.connectionStatus} value={connection} /><Detail label={copy.boundDevices} value={!item.subscription.hwid_enabled ? copy.hwidDisabled : devices.length === 0 ? copy.unboundDevice : devices.join(", ")} /></dl></div>;
}

function SubscriptionQRDialog({ qr, locale, onClose }: { qr: DistributorQR; locale: DistributorLocale; onClose: () => void }) {
  const devices = qr.hwid_devices ?? [];
  const copy = distributorCopy[locale];
  return <Modal title={copy.subscriptionQR} onClose={onClose}><div className="modal-header"><h2>{copy.subscriptionQR}</h2><button className="icon-button" aria-label={distributorCloseLabel(locale, copy.subscriptionQR)} onClick={onClose}>×</button></div><p className="muted">{copy.qrRenewHint}</p><img className="distributor-qr" src={qr.qr_code} alt={copy.subscriptionQR} /><div className="detail-list"><div><span>{copy.originalOrder}</span><strong className="monospace">{qr.trade_no}</strong></div><div><span>{copy.deviceStatus}</span><strong>{qr.hwid_enabled ? devices.length > 0 ? devices.join(", ") : copy.unboundDevice : copy.hwidDisabled}</strong></div></div><div className="form-actions"><a className="button secondary" href={qr.qr_code} download={`subscription-${qr.trade_no}.svg`}>{copy.downloadImage}</a><button className="button primary" onClick={onClose}>{copy.close}</button></div></Modal>;
}

function RenewDialog({ api, order, locale, onClose, onRenewed }: { api: DistributorOrdersAPI; order: DistributorOrder; locale: DistributorLocale; onClose: () => void; onRenewed: (value: DistributorOrder) => void }) {
  const [plan, setPlan] = useState<PlanOffer | null>(null);
  const [period, setPeriod] = useState<PlanPeriod>(order.order.period);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const idempotencyKey = useRef(secureRandomUUID());
  const copy = distributorCopy[locale];
  const periodLabels = distributorPeriodLabels(locale);
  useEffect(() => {
    let active = true;
    void api.listPlanOffers().then((plans) => {
      if (!active) return;
      const found = plans.find((item) => item.id === order.subscription_entitlement.plan_id) ?? null;
      setPlan(found);
      const options = found === null ? [] : renewalPeriods(found);
      if (!options.some(([key]) => key === order.order.period)) setPeriod(options[0]?.[0] ?? "monthly");
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause, locale)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, locale, order.order.period, order.subscription_entitlement.plan_id]);
  const options = useMemo(() => plan === null ? [] : renewalPeriods(plan), [plan]);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (busy) return;
    setBusy(true); setError("");
    try { onRenewed(await api.renewDistributorOrder(order.order.trade_no, period, idempotencyKey.current)); }
    catch (cause) { setError(messageOf(cause, locale)); setBusy(false); }
  };
  return <Modal title={copy.renewTitle} onClose={onClose}><div className="modal-header"><h2>{copy.renewTitle}</h2><button className="icon-button" aria-label={distributorCloseLabel(locale, copy.renewTitle)} onClick={onClose}>×</button></div><p className="muted">{copy.renewHint}</p>{loading ? <div className="alert" role="status">{copy.loadingRenewal}</div> : <form className="form-stack" onSubmit={(event) => void submit(event)}><div className="detail-list"><div><span>{copy.originalOrder}</span><strong className="monospace">{order.subscription.trade_no}</strong></div><div><span>{copy.renewCurrentExpiry}</span><strong>{order.subscription_entitlement.expired_at === null ? copy.permanent : formatDate(order.subscription_entitlement.expired_at, locale)}</strong></div></div><label>{copy.renewPeriod}<select aria-label={copy.renewPeriod} value={period} onChange={(event) => setPeriod(event.target.value as PlanPeriod)}>{options.map(([key, cents]) => <option key={key} value={key}>{periodLabels[key] ?? key} · ¥{formatDistributorCents(cents)}</option>)}</select></label>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" disabled={busy} onClick={onClose}>{copy.cancel}</button><button className="button primary" type="submit" disabled={busy || options.length === 0}>{busy ? copy.renewing : copy.renewConfirm}</button></div></form>}</Modal>;
}

function renewalPeriods(plan: PlanOffer): Array<[PlanPeriod, number]> { return Object.entries(plan.prices).filter((entry): entry is [PlanPeriod, number] => entry[1] !== undefined && entry[1] > 0 && !["onetime", "reset_traffic"].includes(entry[0])); }
function toggleSet(current: Set<number>, id: number): Set<number> { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next; }
function Detail({ label, value }: { label: string; value: string }) { return <div><dt>{label}</dt><dd>{value}</dd></div>; }
function formatTraffic(value: number): string { if (value < 1024) return `${value} B`; const units = ["KiB", "MiB", "GiB", "TiB", "PiB"]; let amount = value; let index = -1; do { amount /= 1024; index++; } while (amount >= 1024 && index < units.length - 1); return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[index]}`; }
function formatLimit(value: number, unit: string, copy: DistributorCopy): string { return value === 0 ? copy.unlimited : `${value} ${unit}`; }
function formatDate(value: string, locale: DistributorLocale): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale, { hour12: false }); }
function messageOf(cause: unknown, locale: DistributorLocale): string { return cause instanceof Error ? cause.message : locale === "en-US" ? "Distributor order request failed" : "分销订单请求失败"; }
function downloadBlob(blob: Blob, filename: string) { const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url); }

export function distributorEntitlementForTest(value: DistributorEntitlement): DistributorEntitlement { return value; }
