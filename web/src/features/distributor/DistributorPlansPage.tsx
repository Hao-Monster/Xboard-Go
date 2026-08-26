import { useEffect, useMemo, useState } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { DistributorOrder, DistributorQR, PlanOffer, PlanPeriod } from "../../lib/api";

export interface DistributorPlansAPI {
  listPlanOffers: () => Promise<PlanOffer[]>;
  createDistributorOrder: (planID: number, period: PlanPeriod) => Promise<DistributorOrder>;
  getDistributorOrderQR: (tradeNo: string) => Promise<DistributorQR>;
}

type PlanFilter = "all" | "high-traffic" | "unlimited-speed" | "unlimited-devices";

const filters: Array<[PlanFilter, string]> = [
  ["all", "全部套餐"], ["high-traffic", "大流量"], ["unlimited-speed", "不限速"], ["unlimited-devices", "不限设备"]
];

const periodLabels: Record<PlanPeriod, string> = {
  monthly: "月付", quarterly: "季付", half_yearly: "半年付", yearly: "年付", two_yearly: "两年付",
  three_yearly: "三年付", onetime: "一次性", reset_traffic: "流量重置包"
};

export function DistributorPlansPage({ api }: { api: DistributorPlansAPI }) {
  const [plans, setPlans] = useState<PlanOffer[]>([]);
  const [filter, setFilter] = useState<PlanFilter>("all");
  const [periods, setPeriods] = useState<Record<number, PlanPeriod>>({});
  const [loading, setLoading] = useState(true);
  const [buying, setBuying] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [delivery, setDelivery] = useState<{ plan: PlanOffer; period: PlanPeriod; qr: DistributorQR } | null>(null);

  useEffect(() => {
    let active = true;
    void api.listPlanOffers().then((items) => {
      if (!active) return;
      const available = items.filter((plan) => plan.can_purchase && availablePeriods(plan).length > 0);
      setPlans(available);
      setPeriods(Object.fromEntries(available.map((plan) => [plan.id, availablePeriods(plan)[0]?.[0] ?? "monthly"])));
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  const visibleFilters = useMemo(() => filters.filter(([key]) => key === "all" || plans.some((plan) => matches(plan, key))), [plans]);
  const visible = useMemo(() => plans.filter((plan) => matches(plan, filter)), [filter, plans]);

  const purchase = async (plan: PlanOffer, period = periods[plan.id]) => {
    if (period === undefined || buying !== null) return;
    setBuying(plan.id); setError("");
    try {
      const created = await api.createDistributorOrder(plan.id, period);
      const qr = await api.getDistributorOrderQR(created.order.trade_no);
      setDelivery({ plan, period, qr });
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBuying(null);
    }
  };

  return <main className="page-shell distributor-plan-page">
    <header className="page-header"><div><p className="eyebrow">Distributor catalog</p><h1>购买订阅</h1><p className="muted">选择套餐并下单，客户扫描二维码即可添加独立订阅。</p></div></header>
    <ol className="distributor-delivery-steps" aria-label="订阅交付步骤"><li><b>01</b>选择套餐并下单</li><li><b>02</b>客户扫描二维码</li><li><b>03</b>确认节点可用</li></ol>
    <nav className="client-filters" aria-label="套餐筛选">{visibleFilters.map(([key, label]) => <button type="button" className={filter === key ? "active" : ""} aria-current={filter === key ? "page" : undefined} key={key} onClick={() => setFilter(key)}>{label}</button>)}</nav>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">正在加载套餐…</div> : visible.length === 0 ? <div className="empty-card">当前没有符合条件的可售套餐。</div> : <section className="plan-card-grid" aria-label="分销套餐列表">{visible.map((plan, index) => {
      const options = availablePeriods(plan);
      const selected = periods[plan.id] ?? options[0]?.[0] ?? "monthly";
      const cents = plan.prices[selected] ?? 0;
      return <article className={`site-settings-card distributor-plan-card ${index === 0 ? "featured" : ""}`} key={plan.id}>
        <div className="distributor-plan-tags">{index === 0 && <span className="count-pill">精选套餐</span>}{plan.transfer_enable >= 100 && <span>大流量</span>}{unlimited(plan.speed_limit) && <span>不限速</span>}{unlimited(plan.device_limit) && <span>不限设备</span>}</div>
        <div className="section-heading"><div><h2>{plan.name}</h2><p className="muted">独立订阅交付，客户扫码即可添加。</p></div><strong className="distributor-plan-price">¥{formatCents(cents)}</strong></div>
        <dl className="distributor-plan-specs"><div><dt>套餐流量</dt><dd>{plan.transfer_enable} GB</dd></div><div><dt>速度限制</dt><dd>{limit(plan.speed_limit, "Mbps")}</dd></div><div><dt>同时在线</dt><dd>{limit(plan.device_limit, "台")}</dd></div><div><dt>流量重置</dt><dd>{resetLabel(plan.reset_traffic_method)}</dd></div></dl>
        <fieldset className="distributor-period-options"><legend>周期</legend>{options.map(([period, price]) => <label key={period}><input type="radio" name={`period-${plan.id}`} checked={selected === period} onChange={() => setPeriods((current) => ({ ...current, [plan.id]: period }))} /><span>{periodLabels[period]}<strong>¥{formatCents(price)}</strong></span></label>)}</fieldset>
        {plan.content !== "" && <details><summary>套餐详情</summary><div className="markdown-body plan-content"><Markdown>{plan.content}</Markdown></div></details>}
        <button className="button primary distributor-buy-button" type="button" disabled={buying !== null || cents <= 0} onClick={() => void purchase(plan, selected)}>{buying === plan.id ? "正在下单…" : "已确认，直接下单"}</button>
      </article>;
    })}</section>}
    {delivery !== null && <DeliveryDialog delivery={delivery} busy={buying !== null} onBuyAgain={() => void purchase(delivery.plan, delivery.period)} onClose={() => setDelivery(null)} />}
  </main>;
}

function DeliveryDialog({ delivery, busy, onBuyAgain, onClose }: { delivery: { plan: PlanOffer; period: PlanPeriod; qr: DistributorQR }; busy: boolean; onBuyAgain: () => void; onClose: () => void }) {
  const [message, setMessage] = useState("");
  const devices = delivery.qr.hwid_devices ?? [];
  const copy = async () => {
    try {
      const blob = await (await fetch(delivery.qr.qr_code)).blob();
      if (typeof ClipboardItem === "undefined") throw new Error("unsupported");
      await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
      setMessage("二维码图片已复制");
    } catch {
      setMessage("当前浏览器无法复制图片，请下载后发送");
    }
  };
  return <Modal title="订阅交付" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Delivery</p><h2>已添加成功</h2></div><button className="icon-button" aria-label="关闭订阅交付" onClick={onClose}>×</button></div>
    <p className="muted">请让客户扫描二维码添加订阅。关闭弹窗不会关闭或撤销订阅。</p>
    <img className="distributor-qr" src={delivery.qr.qr_code} alt="客户订阅二维码" />
    <div className="detail-list"><div><span>订单号</span><strong className="monospace">{delivery.qr.trade_no}</strong></div><div><span>套餐</span><strong>{delivery.plan.name} · {periodLabels[delivery.period]}</strong></div><div><span>设备绑定</span><strong>{delivery.qr.hwid_enabled ? devices.length > 0 ? `已绑定 ${devices.length} 台` : "尚未绑定设备" : "未启用设备绑定"}</strong></div></div>
    {message !== "" && <div className="alert" role="status">{message}</div>}
    <div className="form-actions"><button className="button secondary" type="button" onClick={() => void copy()}>复制图片</button><a className="button secondary" href={delivery.qr.qr_code} download={`subscription-${delivery.qr.trade_no}.svg`}>下载图片</a><button className="button primary" type="button" disabled={busy} onClick={onBuyAgain}>{busy ? "正在下单…" : "再次购买该套餐"}</button></div>
  </Modal>;
}

function availablePeriods(plan: PlanOffer): Array<[PlanPeriod, number]> {
  return Object.entries(plan.prices).filter((entry): entry is [PlanPeriod, number] => entry[1] !== undefined && entry[1] > 0 && entry[0] !== "reset_traffic");
}
function matches(plan: PlanOffer, filter: PlanFilter): boolean {
  if (filter === "high-traffic") return plan.transfer_enable >= 100;
  if (filter === "unlimited-speed") return unlimited(plan.speed_limit);
  if (filter === "unlimited-devices") return unlimited(plan.device_limit);
  return true;
}
function unlimited(value: number | null): boolean { return value === null || value === 0; }
function limit(value: number | null, unit: string): string { return unlimited(value) ? "不限" : `${value} ${unit}`; }
function resetLabel(value: number | null): string { return value === null ? "跟随系统" : ["每月 1 日", "按月重置", "不重置", "每年 1 月 1 日", "按年重置"][value] ?? "跟随系统"; }
export function formatDistributorCents(value: number): string { return formatCents(value); }
function formatCents(value: number): string { return `${Math.trunc(value / 100)}.${String(Math.abs(value % 100)).padStart(2, "0")}`; }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "分销下单失败"; }
