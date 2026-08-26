import { useCallback, useEffect, useState } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminDistributorOrderDetail, AdminUser, DistributorEntitlement, DistributorHWIDDevice, DistributorHWIDSettings, DistributorOrderPage, DistributorOrderQuery, DistributorSettlementStatus, DistributorSettlementSummary } from "../../lib/api";
import { formatDistributorCents } from "./DistributorPlansPage";

export interface AdminDistributorAPI {
  listAdminDistributorOptions: () => Promise<AdminUser[]>;
  listAdminDistributorOrders: (query?: DistributorOrderQuery) => Promise<DistributorOrderPage>;
  getAdminDistributorOrder: (orderID: number) => Promise<AdminDistributorOrderDetail>;
  updateAdminDistributorRemark: (orderID: number, remark: string | null) => Promise<{ order_id: number; remark: string | null }>;
  updateAdminDistributorEntitlement: (orderID: number, input: Omit<DistributorEntitlement, "plan_id" | "plan_name" | "used_traffic" | "remaining_traffic">) => Promise<DistributorEntitlement>;
  updateAdminDistributorHWID: (orderID: number, enabled: boolean, limit: number) => Promise<DistributorHWIDSettings>;
  listAdminDistributorHWIDDevices: (orderID: number, search?: string) => Promise<DistributorHWIDDevice[]>;
  deleteAdminDistributorHWIDDevice: (orderID: number, deviceID: number) => Promise<void>;
  previewAdminDistributorSettlement: (userID: number) => Promise<DistributorSettlementSummary>;
  settleAdminDistributorOrders: (userID: number) => Promise<DistributorSettlementSummary>;
  exportAdminDistributorOrders: (query?: DistributorOrderQuery) => Promise<Blob>;
}

export function AdminDistributorPage({ api }: { api: AdminDistributorAPI }) {
  const [distributors, setDistributors] = useState<AdminUser[]>([]);
  const [page, setPage] = useState<DistributorOrderPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [distributorID, setDistributorID] = useState("");
  const [search, setSearch] = useState("");
  const [appliedSearch, setAppliedSearch] = useState("");
  const [settlement, setSettlement] = useState<"" | DistributorSettlementStatus>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [exporting, setExporting] = useState(false);
  const [selectedOrderID, setSelectedOrderID] = useState<number | null>(null);
  const [settlingUserID, setSettlingUserID] = useState<number | null>(null);

  const query = useCallback((pageNumber = 1): DistributorOrderQuery => ({ page: pageNumber, page_size: 20,
    ...(distributorID === "" ? {} : { distributor_user_id: Number(distributorID) }),
    ...(appliedSearch === "" ? {} : { search: appliedSearch }),
    ...(settlement === "" ? {} : { settlement_status: settlement })
  }), [appliedSearch, distributorID, settlement]);
  const load = useCallback(async (pageNumber = 1) => {
    setLoading(true); setError("");
    try { setPage(await api.listAdminDistributorOrders(query(pageNumber))); }
    catch (cause) { setError(messageOf(cause)); }
    finally { setLoading(false); }
  }, [api, query]);

  useEffect(() => {
    let active = true;
    void api.listAdminDistributorOptions().then((items) => { if (active) setDistributors(items); }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); });
    return () => { active = false; };
  }, [api]);
  useEffect(() => {
    let active = true;
    void api.listAdminDistributorOrders(query(1)).then((next) => {
      if (active) setPage(next);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api, query]);

  const exportOrders = async () => {
    setExporting(true); setError("");
    try { downloadBlob(await api.exportAdminDistributorOrders(query(page.page)), "分销订单.xlsx"); }
    catch (cause) { setError(messageOf(cause)); }
    finally { setExporting(false); }
  };

  return <main className="page-shell admin-distributor-page">
    <header className="page-header"><div><p className="eyebrow">Distributor operations</p><h1>分销管理</h1><p className="muted">管理分销订单、客户订阅权益、HWID 设备与线下结算。</p></div><button className="button primary" disabled={distributorID === ""} onClick={() => setSettlingUserID(Number(distributorID))}>结算所选分销商</button></header>
    <form className="resource-toolbar distributor-order-toolbar" onSubmit={(event) => { event.preventDefault(); setAppliedSearch(search.trim()); }}>
      <label>分销商<select aria-label="分销商" value={distributorID} onChange={(event) => setDistributorID(event.target.value)}><option value="">全部分销商</option>{distributors.map((item) => <option key={item.id} value={item.id}>{item.distributor_name ?? item.email} · {item.email}{item.banned ? "（已封禁）" : ""}</option>)}</select></label>
      <label className="search-field">搜索<input type="search" maxLength={512} value={search} placeholder="订单、客户名或订阅凭据" onChange={(event) => setSearch(event.target.value)} /></label><button className="button secondary" type="submit">搜索</button>
      <label>结算状态<select aria-label="结算状态" value={settlement} onChange={(event) => setSettlement(event.target.value === "" ? "" : Number(event.target.value) as DistributorSettlementStatus)}><option value="">全部</option><option value="0">未结算</option><option value="1">已结算</option></select></label>
      <button className="button secondary" type="button" disabled={exporting} onClick={() => void exportOrders()}>{exporting ? "正在导出…" : "导出 Excel"}</button>
    </form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载分销订单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的分销订单。</div> : <section className="resource-table-wrap" aria-label="管理员分销订单列表"><table className="resource-table"><thead><tr><th>订单</th><th>分销商</th><th>客户 / 套餐</th><th>金额</th><th>结算</th><th>连接</th><th>操作</th></tr></thead><tbody>{page.items.map((item) => <tr key={item.order.id}><td data-label="订单"><strong className="monospace">{item.order.trade_no}</strong><small>{item.is_subscription_origin ? "新购" : `续费 · 原单 ${item.subscription.trade_no}`}</small><small className="muted">{formatDate(item.order.created_at)}</small></td><td data-label="分销商"><strong>{item.distributor_name || item.distributor_email}</strong><small>{item.distributor_email}</small></td><td data-label="客户 / 套餐"><strong>{item.subscription.customer_name ?? "未填写客户名"}</strong><small>{item.plan_name}</small></td><td data-label="金额">¥{formatDistributorCents(item.order.total_amount)}</td><td data-label="结算"><span className={`status-badge ${item.settlement_status === 1 ? "enabled" : "warning"}`}>{item.settlement_status === 1 ? "已结算" : "未结算"}</span></td><td data-label="连接">{item.subscription.connected_at === null ? "尚未连接" : item.subscription.connected_node_name ?? "已连接"}</td><td data-label="操作"><button className="button primary compact" aria-label={`分销订单详情：${item.order.trade_no}`} onClick={() => setSelectedOrderID(item.order.id)}>详情与设置</button></td></tr>)}</tbody></table></section>}
    {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary" disabled={loading || page.page <= 1} onClick={() => void load(page.page - 1)}>上一页</button><span>第 {page.page} 页 · 共 {page.total} 条</span><button className="button secondary" disabled={loading || page.page * page.page_size >= page.total} onClick={() => void load(page.page + 1)}>下一页</button></div>}
    {selectedOrderID !== null && <DistributorDetailDialog api={api} orderID={selectedOrderID} onClose={() => setSelectedOrderID(null)} onUpdated={() => void load(page.page)} />}
    {settlingUserID !== null && <SettlementDialog api={api} distributor={distributors.find((item) => item.id === settlingUserID)} userID={settlingUserID} onClose={() => setSettlingUserID(null)} onSettled={() => { setSettlingUserID(null); void load(1); }} />}
  </main>;
}

function DistributorDetailDialog({ api, orderID, onClose, onUpdated }: { api: AdminDistributorAPI; orderID: number; onClose: () => void; onUpdated: () => void }) {
  const [detail, setDetail] = useState<AdminDistributorOrderDetail | null>(null);
  const [devices, setDevices] = useState<DistributorHWIDDevice[]>([]);
  const [deviceSearch, setDeviceSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [remark, setRemark] = useState("");
  const [transferEnable, setTransferEnable] = useState("0");
  const [expiredAt, setExpiredAt] = useState("");
  const [speedLimit, setSpeedLimit] = useState("0");
  const [deviceLimit, setDeviceLimit] = useState("0");
  const [hwidEnabled, setHWIDEnabled] = useState(true);
  const [hwidLimit, setHWIDLimit] = useState("1");

  const populate = (value: AdminDistributorOrderDetail) => {
    setDetail(value); setRemark(value.order.subscription.remark ?? "");
    setTransferEnable(String(value.order.subscription_entitlement.transfer_enable)); setExpiredAt(toLocalDateTime(value.order.subscription_entitlement.expired_at));
    setSpeedLimit(String(value.order.subscription_entitlement.speed_limit)); setDeviceLimit(String(value.order.subscription_entitlement.device_limit));
    setHWIDEnabled(value.hwid.enabled); setHWIDLimit(String(value.hwid.limit));
  };
  const loadDevices = useCallback(async (search = "") => { setDevices(await api.listAdminDistributorHWIDDevices(orderID, search)); }, [api, orderID]);
  useEffect(() => {
    let active = true;
    void Promise.all([api.getAdminDistributorOrder(orderID), api.listAdminDistributorHWIDDevices(orderID)]).then(([value, rows]) => { if (active) { populate(value); setDevices(rows); } }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, orderID]);
  const act = async (name: string, operation: () => Promise<void>) => { if (busy !== "") return; setBusy(name); setError(""); setMessage(""); try { await operation(); setMessage("设置已保存"); onUpdated(); } catch (cause) { setError(messageOf(cause)); } finally { setBusy(""); } };
  const saveRemark = () => act("remark", async () => { const result = await api.updateAdminDistributorRemark(orderID, remark.trim() === "" ? null : remark.trim()); setRemark(result.remark ?? ""); if (detail !== null) setDetail({ ...detail, order: { ...detail.order, subscription: { ...detail.order.subscription, remark: result.remark } } }); });
  const saveEntitlement = () => act("entitlement", async () => { const entitlement = await api.updateAdminDistributorEntitlement(orderID, { transfer_enable: Number(transferEnable), expired_at: expiredAt === "" ? null : new Date(expiredAt).toISOString(), speed_limit: Number(speedLimit), device_limit: Number(deviceLimit) }); if (detail !== null) setDetail({ ...detail, order: { ...detail.order, subscription_entitlement: entitlement } }); });
  const saveHWID = () => act("hwid", async () => { const hwid = await api.updateAdminDistributorHWID(orderID, hwidEnabled, Number(hwidLimit)); if (detail !== null) setDetail({ ...detail, hwid }); });
  const deleteDevice = (device: DistributorHWIDDevice) => act(`device-${device.id}`, async () => { await api.deleteAdminDistributorHWIDDevice(orderID, device.id); await loadDevices(deviceSearch.trim()); });

  return <Modal title="分销订单详情" className="wide-modal" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Subscription administration</p><h2>分销订单详情</h2></div><button className="icon-button" aria-label="关闭分销订单详情" onClick={onClose}>×</button></div>
    {loading ? <div className="alert" role="status">正在加载详情…</div> : detail !== null && <div className="distributor-admin-detail">
      <section><h3>订单与订阅</h3><div className="detail-list"><Detail label="订单号" value={detail.order.order.trade_no} mono /><Detail label="原始订单" value={detail.order.subscription.trade_no} mono /><Detail label="分销商" value={`${detail.order.distributor_name} · ${detail.order.distributor_email}`} /><Detail label="客户名称" value={detail.order.subscription.customer_name ?? "-"} /><Detail label="套餐" value={detail.order.plan_name} /><Detail label="订阅地址" value={detail.subscribe_url} mono /></div><button className="button secondary compact" onClick={() => void navigator.clipboard.writeText(detail.subscribe_url).then(() => setMessage("订阅地址已复制")).catch(() => setError("复制订阅地址失败"))}>复制订阅地址</button></section>
      <form className="form-stack" onSubmit={(event) => { event.preventDefault(); void saveRemark(); }}><h3>备注</h3><label>内部备注<textarea maxLength={500} value={remark} onChange={(event) => setRemark(event.target.value)} /></label><button className="button secondary" disabled={busy !== ""}>{busy === "remark" ? "正在保存…" : "保存备注"}</button></form>
      <form className="form-stack" onSubmit={(event) => { event.preventDefault(); void saveEntitlement(); }}><h3>客户订阅权益</h3><p className="muted small">已用流量只读：{formatTraffic(detail.order.subscription_entitlement.used_traffic)}</p><label>总流量（字节）<input type="number" min="0" step="1" required value={transferEnable} onChange={(event) => setTransferEnable(event.target.value)} /></label><label>到期时间（留空长期有效）<input type="datetime-local" value={expiredAt} onChange={(event) => setExpiredAt(event.target.value)} /></label><div className="time-grid"><label>限速（Mbps）<input type="number" min="0" max="1000000000" required value={speedLimit} onChange={(event) => setSpeedLimit(event.target.value)} /></label><label>设备限制<input type="number" min="0" max="1000" required value={deviceLimit} onChange={(event) => setDeviceLimit(event.target.value)} /></label></div><button className="button secondary" disabled={busy !== ""}>{busy === "entitlement" ? "正在保存…" : "保存权益"}</button></form>
      <form className="form-stack" onSubmit={(event) => { event.preventDefault(); void saveHWID(); }}><h3>HWID 设置</h3><label className="switch-label"><input type="checkbox" checked={hwidEnabled} onChange={(event) => setHWIDEnabled(event.target.checked)} />启用设备绑定</label><label>HWID 上限<input type="number" min="1" max="100" required value={hwidLimit} onChange={(event) => setHWIDLimit(event.target.value)} /></label><p className="muted small">当前已登记 {detail.hwid.registered_count} 台</p><button className="button secondary" disabled={busy !== ""}>{busy === "hwid" ? "正在保存…" : "保存 HWID 设置"}</button></form>
      <section><h3>HWID 设备</h3><form className="resource-toolbar" onSubmit={(event) => { event.preventDefault(); void act("device-search", () => loadDevices(deviceSearch.trim())); }}><label>搜索 HWID<input type="search" maxLength={64} value={deviceSearch} onChange={(event) => setDeviceSearch(event.target.value)} /></label><button className="button secondary" disabled={busy !== ""}>搜索设备</button></form>{devices.length === 0 ? <div className="empty-card compact-empty">暂无登记设备</div> : <div className="resource-table-wrap compact-table"><table className="resource-table"><thead><tr><th>HWID</th><th>设备</th><th>IP / 最近出现</th><th>操作</th></tr></thead><tbody>{devices.map((device) => <tr key={device.id}><td data-label="HWID"><code>{device.hwid}</code></td><td data-label="设备">{[device.device_model, device.device_os, device.os_version].filter(Boolean).join(" · ") || "未知设备"}</td><td data-label="IP / 最近出现">{device.ip_address ?? "-"}<small>{formatDate(device.last_seen_at)}</small></td><td data-label="操作"><button className="button danger compact" type="button" disabled={busy !== ""} onClick={() => void deleteDevice(device)}>{busy === `device-${device.id}` ? "正在删除…" : "删除"}</button></td></tr>)}</tbody></table></div>}</section>
    </div>}
    {error !== "" && <div className="alert error" role="alert">{error}</div>}{message !== "" && <div className="alert success" role="status">{message}</div>}<div className="form-actions"><button className="button primary" onClick={onClose}>关闭</button></div>
  </Modal>;
}

function SettlementDialog({ api, distributor, userID, onClose, onSettled }: { api: AdminDistributorAPI; distributor?: AdminUser; userID: number; onClose: () => void; onSettled: (summary: DistributorSettlementSummary) => void }) {
  const [summary, setSummary] = useState<DistributorSettlementSummary | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { let active = true; void api.previewAdminDistributorSettlement(userID).then((value) => { if (active) setSummary(value); }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); }); return () => { active = false; }; }, [api, userID]);
  const settle = async () => { setBusy(true); setError(""); try { onSettled(await api.settleAdminDistributorOrders(userID)); } catch (cause) { setError(messageOf(cause)); setBusy(false); } };
  return <Modal title="分销订单结算" onClose={onClose}><div className="modal-header"><h2>分销订单结算</h2><button className="icon-button" aria-label="关闭分销订单结算" onClick={onClose}>×</button></div><p>将一次性结算 <strong>{distributor?.distributor_name ?? distributor?.email ?? `#${userID}`}</strong> 当前全部已完成且未结算的分销订单。</p>{summary === null ? <div className="alert" role="status">正在计算结算金额…</div> : <div className="detail-list"><Detail label="订单数" value={String(summary.count)} /><Detail label="结算总额" value={`¥${formatDistributorCents(summary.total_amount)}`} /></div>}{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" disabled={busy} onClick={onClose}>取消</button><button className="button primary" disabled={busy || summary === null || summary.count === 0} onClick={() => void settle()}>{busy ? "正在结算…" : "确认结算"}</button></div></Modal>;
}

function Detail({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><span>{label}</span><strong className={mono ? "monospace" : ""}>{value}</strong></div>; }
function toLocalDateTime(value: string | null): string { if (value === null) return ""; const date = new Date(value); return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16); }
function formatDate(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { hour12: false }); }
function formatTraffic(value: number): string { if (value < 1024) return `${value} B`; const units = ["KiB", "MiB", "GiB", "TiB", "PiB"]; let amount = value; let index = -1; do { amount /= 1024; index++; } while (amount >= 1024 && index < units.length - 1); return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[index]}`; }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "分销管理请求失败"; }
function downloadBlob(blob: Blob, filename: string) { const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = filename; document.body.append(link); link.click(); link.remove(); URL.revokeObjectURL(url); }
