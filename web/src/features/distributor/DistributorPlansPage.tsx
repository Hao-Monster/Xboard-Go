import { useEffect, useMemo, useState } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { DistributorOrder, DistributorQR, PlanOffer, PlanPeriod } from "../../lib/api";
import { useCurrency } from "../../lib/currency";
import { distributorCopy, distributorPeriodLabels, type DistributorCopy, type DistributorLocale } from "./locale";

export interface DistributorPlansAPI {
  listPlanOffers: () => Promise<PlanOffer[]>;
  createDistributorOrder: (planID: number, period: PlanPeriod) => Promise<DistributorOrder>;
  getDistributorOrderQR: (tradeNo: string) => Promise<DistributorQR>;
}

type PlanFilter = "all" | "high-traffic" | "unlimited-speed" | "unlimited-devices";

export function DistributorPlansPage({ api, locale = "zh-CN" }: { api: DistributorPlansAPI; locale?: DistributorLocale }) {
  const { format: formatMoney } = useCurrency();
  const [plans, setPlans] = useState<PlanOffer[]>([]);
  const [filter, setFilter] = useState<PlanFilter>("all");
  const [periods, setPeriods] = useState<Record<number, PlanPeriod>>({});
  const [loading, setLoading] = useState(true);
  const [buying, setBuying] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [delivery, setDelivery] = useState<{ plan: PlanOffer; period: PlanPeriod; qr: DistributorQR } | null>(null);
  const copy = distributorCopy[locale];
  const periodLabels = distributorPeriodLabels(locale);
  const filters = useMemo<Array<[PlanFilter, string]>>(() => [
    ["all", copy.allPlans], ["high-traffic", copy.highTraffic], ["unlimited-speed", copy.unlimitedSpeed], ["unlimited-devices", copy.unlimitedDevices]
  ], [copy]);

  useEffect(() => {
    let active = true;
    void api.listPlanOffers().then((items) => {
      if (!active) return;
      const available = items.filter((plan) => plan.can_purchase && availablePeriods(plan).length > 0);
      setPlans(available);
      setPeriods(Object.fromEntries(available.map((plan) => [plan.id, availablePeriods(plan)[0]?.[0] ?? "monthly"])));
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause, locale)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, locale]);

  const visibleFilters = useMemo(() => filters.filter(([key]) => key === "all" || plans.some((plan) => matches(plan, key))), [filters, plans]);
  const visible = useMemo(() => plans.filter((plan) => matches(plan, filter)), [filter, plans]);

  const purchase = async (plan: PlanOffer, period = periods[plan.id]) => {
    if (period === undefined || buying !== null) return;
    setBuying(plan.id); setError("");
    try {
      const created = await api.createDistributorOrder(plan.id, period);
      const qr = await api.getDistributorOrderQR(created.order.trade_no);
      setDelivery({ plan, period, qr });
    } catch (cause) {
      setError(messageOf(cause, locale));
    } finally {
      setBuying(null);
    }
  };

  return <main className="page-shell distributor-plan-page">
    <header className="page-header"><div><p className="eyebrow">Distributor catalog</p><h1>{copy.centerTitle}</h1><p className="muted">{copy.centerSubtitle}</p></div></header>
    <ol className="distributor-delivery-steps" aria-label={copy.deliveryTitle}><li><b>01</b>{copy.deliveryStepOne}</li><li><b>02</b>{copy.deliveryStepTwo}</li><li><b>03</b>{copy.deliveryStepThree}</li></ol>
    <nav className="client-filters" aria-label={copy.allPlans}>{visibleFilters.map(([key, label]) => <button type="button" className={filter === key ? "active" : ""} aria-current={filter === key ? "page" : undefined} key={key} onClick={() => setFilter(key)}>{label}</button>)}</nav>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">{copy.loadingPlans}</div> : visible.length === 0 ? <div className="empty-card">{copy.noPlans}</div> : <section className="plan-card-grid" aria-label={copy.allPlans}>{visible.map((plan, index) => {
      const options = availablePeriods(plan);
      const selected = periods[plan.id] ?? options[0]?.[0] ?? "monthly";
      const cents = plan.prices[selected] ?? 0;
      return <article className={`site-settings-card distributor-plan-card ${index === 0 ? "featured" : ""}`} key={plan.id}>
        <div className="distributor-plan-tags">{index === 0 && <span className="count-pill">{copy.featured}</span>}{plan.transfer_enable >= 100 && <span>{copy.highTraffic}</span>}{unlimited(plan.speed_limit) && <span>{copy.unlimitedSpeed}</span>}{unlimited(plan.device_limit) && <span>{copy.unlimitedDevices}</span>}</div>
        <div className="section-heading"><div><h2>{plan.name}</h2><p className="muted">{copy.independentDelivery}</p></div><strong className="distributor-plan-price">{formatMoney(cents, locale)}</strong></div>
        <dl className="distributor-plan-specs"><div><dt>{copy.traffic}</dt><dd>{plan.transfer_enable} GB</dd></div><div><dt>{copy.speed}</dt><dd>{limit(plan.speed_limit, "Mbps", copy.unlimited)}</dd></div><div><dt>{copy.devices}</dt><dd>{limit(plan.device_limit, locale === "zh-CN" ? "台" : "devices", copy.unlimited)}</dd></div><div><dt>{copy.resetMethod}</dt><dd>{resetLabel(plan.reset_traffic_method, copy)}</dd></div></dl>
        <fieldset className="distributor-period-options"><legend>{copy.period}</legend>{options.map(([period, price]) => <label key={period}><input type="radio" name={`period-${plan.id}`} checked={selected === period} onChange={() => setPeriods((current) => ({ ...current, [plan.id]: period }))} /><span>{periodLabels[period]}<strong>{formatMoney(price, locale)}</strong></span></label>)}</fieldset>
        {plan.content !== "" && <details><summary>{copy.planDetails}</summary><div className="markdown-body plan-content"><Markdown>{plan.content}</Markdown></div></details>}
        <button className="button primary distributor-buy-button" type="button" disabled={buying !== null || cents <= 0} onClick={() => void purchase(plan, selected)}>{buying === plan.id ? copy.ordering : copy.orderAction}</button>
      </article>;
    })}</section>}
    {delivery !== null && <DeliveryDialog delivery={delivery} busy={buying !== null} locale={locale} onBuyAgain={() => void purchase(delivery.plan, delivery.period)} onClose={() => setDelivery(null)} />}
  </main>;
}

function DeliveryDialog({ delivery, busy, locale, onBuyAgain, onClose }: { delivery: { plan: PlanOffer; period: PlanPeriod; qr: DistributorQR }; busy: boolean; locale: DistributorLocale; onBuyAgain: () => void; onClose: () => void }) {
  const [message, setMessage] = useState("");
  const devices = delivery.qr.hwid_devices ?? [];
  const copy = distributorCopy[locale];
  const periodLabels = distributorPeriodLabels(locale);
  const copyImage = async () => {
    try {
      const blob = await (await fetch(delivery.qr.qr_code)).blob();
      if (typeof ClipboardItem === "undefined") throw new Error("unsupported");
      await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
      setMessage(copy.copiedImage);
    } catch {
      setMessage(copy.copyImageUnsupported);
    }
  };
  return <Modal title={copy.deliveryTitle} onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Delivery</p><h2>{copy.done}</h2></div><button className="icon-button" aria-label={copy.closeDelivery} onClick={onClose}>×</button></div>
    <p className="muted">{copy.deliveryHint}</p>
    <img className="distributor-qr" src={delivery.qr.qr_code} alt={copy.customerQR} />
    <div className="detail-list"><div><span>{copy.orderNo}</span><strong className="monospace">{delivery.qr.trade_no}</strong></div><div><span>{copy.plan}</span><strong>{delivery.plan.name} · {periodLabels[delivery.period]}</strong></div><div><span>{copy.deviceBinding}</span><strong>{delivery.qr.hwid_enabled ? devices.length > 0 ? `${copy.boundDevices}: ${devices.length}` : copy.unboundDevice : copy.hwidDisabled}</strong></div></div>
    {message !== "" && <div className="alert" role="status">{message}</div>}
    <div className="form-actions"><button className="button secondary" type="button" onClick={() => void copyImage()}>{copy.copyImage}</button><a className="button secondary" href={delivery.qr.qr_code} download={`subscription-${delivery.qr.trade_no}.svg`}>{copy.downloadImage}</a><button className="button primary" type="button" disabled={busy} onClick={onBuyAgain}>{busy ? copy.ordering : copy.buyAgain}</button></div>
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
function limit(value: number | null, unit: string, unlimitedLabel: string): string { return unlimited(value) ? unlimitedLabel : `${value} ${unit}`; }
function resetLabel(value: number | null, copy: DistributorCopy): string { return value === null ? copy.followSystem : [copy.firstDayMonth, copy.monthlyReset, copy.neverReset, copy.firstDayYear, copy.yearlyReset][value] ?? copy.followSystem; }
function messageOf(cause: unknown, locale: DistributorLocale): string { return cause instanceof Error ? cause.message : locale === "en-US" ? "Distributor order failed" : "分销下单失败"; }
