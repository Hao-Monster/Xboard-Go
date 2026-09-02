import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { CouponQuote, Order, PlanOffer, PlanPeriod } from "../../lib/api";
import { useCurrency } from "../../lib/currency";

type PlanCatalogAPI = {
  listPlanOffers: () => Promise<PlanOffer[]>;
  checkCoupon: (code: string, planID: number, period: PlanPeriod) => Promise<CouponQuote>;
  createOrder: (planID: number, period: PlanPeriod, couponCode?: string) => Promise<Order>;
};

const labels: Record<string, string> = { monthly: "月付", quarterly: "季付", half_yearly: "半年付", yearly: "年付", two_yearly: "两年付", three_yearly: "三年付", onetime: "流量包", reset_traffic: "重置包" };

export function PlanCatalogPage({ api, couponEnabled, onOrderCreated }: { api: PlanCatalogAPI; couponEnabled: boolean; onOrderCreated?: (order: Order) => void }) {
  const [plans, setPlans] = useState<PlanOffer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [purchasing, setPurchasing] = useState<PlanOffer | null>(null);
  const [createdTradeNo, setCreatedTradeNo] = useState("");
  const { format: formatMoney } = useCurrency();
  const load = async () => {
    setLoading(true); setError("");
    try { setPlans(await api.listPlanOffers()); } catch (cause) { setError(cause instanceof Error ? cause.message : "套餐加载失败"); } finally { setLoading(false); }
  };
  useEffect(() => {
	let active = true;
	void api.listPlanOffers().then((result) => {
	  if (active) setPlans(result);
	}).catch((cause: unknown) => {
	  if (active) setError(cause instanceof Error ? cause.message : "套餐加载失败");
	}).finally(() => {
	  if (active) setLoading(false);
	});
	return () => { active = false; };
  }, [api]);
  return <main className="page-shell resource-page"><header className="page-header"><div><p className="eyebrow">Plans</p><h1>订阅套餐</h1><p className="muted">查看当前可购买或可续费的套餐。</p></div></header>
    {error !== "" && <div className="alert error" role="alert">{error}<button className="button ghost compact" onClick={() => void load()}>重试</button></div>}
    {createdTradeNo !== "" && <div className="alert success" role="status">订单 {createdTradeNo} 已创建，请到“我的订单”继续处理。</div>}
    {loading ? <div className="empty-card">正在加载套餐…</div> : plans.length === 0 ? <div className="empty-card">当前没有可用套餐。</div> : <section className="plan-card-grid" aria-label="订阅套餐列表">{plans.map((plan) => <article className="site-settings-card" key={plan.id}><div className="section-heading"><div><h2>{plan.name}</h2><p className="muted">{plan.transfer_enable} GiB · 速度 {limit(plan.speed_limit)} · 设备 {limit(plan.device_limit)}</p></div>{plan.capacity_remaining === null ? <span className="count-pill">不限量</span> : <span className="count-pill">剩余 {plan.capacity_remaining}</span>}</div>{plan.tags.length > 0 && <p>{plan.tags.join(" · ")}</p>}<div className="plan-offer-prices">{Object.entries(plan.prices).map(([period, cents]) => <span key={period}>{labels[period] ?? period} {formatMoney(cents ?? 0)}</span>)}</div>{plan.content !== "" && <div className="markdown-body plan-content"><Markdown components={{
      a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; },
      img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; }
    }}>{plan.content}</Markdown></div>}<p className="small muted">{plan.can_renew ? "当前套餐可续费" : plan.can_purchase ? "可购买" : "暂不可购买"}</p><button className="button primary" disabled={(!plan.can_purchase && !plan.can_renew) || Object.keys(plan.prices).length === 0} onClick={() => setPurchasing(plan)}>立即订阅</button></article>)}</section>}
    {purchasing !== null && <PurchaseDialog api={api} plan={purchasing} couponEnabled={couponEnabled} onClose={() => setPurchasing(null)} onCreated={(order) => {
      setPurchasing(null);
      setCreatedTradeNo(order.trade_no);
      onOrderCreated?.(order);
    }} />}
  </main>;
}

function PurchaseDialog({ api, plan, couponEnabled, onClose, onCreated }: { api: PlanCatalogAPI; plan: PlanOffer; couponEnabled: boolean; onClose: () => void; onCreated: (order: Order) => void }) {
  const periods = useMemo(() => Object.entries(plan.prices).filter((entry): entry is [PlanPeriod, number] => entry[1] !== undefined), [plan.prices]);
  const [period, setPeriod] = useState<PlanPeriod>(() => periods[0]?.[0] ?? "monthly");
  const [saving, setSaving] = useState(false);
  const [couponCode, setCouponCode] = useState("");
  const [couponQuote, setCouponQuote] = useState<CouponQuote | null>(null);
  const [checkingCoupon, setCheckingCoupon] = useState(false);
  const [error, setError] = useState("");
  const couponCheckVersion = useRef(0);
  const { format: formatMoney } = useCurrency();
  const verifyCoupon = async () => {
    if (couponCode.trim() === "" || checkingCoupon) return;
    const version = ++couponCheckVersion.current;
    setCheckingCoupon(true);
    setError("");
    setCouponQuote(null);
    try {
      const quote = await api.checkCoupon(couponCode.trim(), plan.id, period);
      if (version === couponCheckVersion.current) setCouponQuote(quote);
    } catch (cause) {
      if (version === couponCheckVersion.current) setError(cause instanceof Error ? cause.message : "优惠券验证失败");
    } finally {
      if (version === couponCheckVersion.current) setCheckingCoupon(false);
    }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving) return;
    setSaving(true);
    setError("");
    try {
      onCreated(await api.createOrder(plan.id, period, couponQuote?.coupon.code));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "订单创建失败");
      setSaving(false);
    }
  };
  const amount = plan.prices[period] ?? 0;
  return <Modal title="配置订阅" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Checkout</p><h2>配置订阅</h2></div><button className="icon-button" aria-label="关闭配置订阅" onClick={onClose}>×</button></div>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <div className="detail-list"><div><span>套餐</span><strong>{plan.name}</strong></div><div><span>流量</span><strong>{plan.transfer_enable} GiB</strong></div></div>
      <label>付款周期<select value={period} onChange={(event) => { couponCheckVersion.current++; setPeriod(event.target.value as PlanPeriod); setCouponQuote(null); setCheckingCoupon(false); }}>{periods.map(([key, cents]) => <option key={key} value={key}>{labels[key] ?? key} · {formatMoney(cents)}</option>)}</select></label>
      <div className="order-total"><span>套餐标价</span><strong>{formatMoney(amount)}</strong></div>
      {couponEnabled && <div className="coupon-check-row"><input aria-label="优惠券" placeholder="有优惠券？" value={couponCode} onChange={(event) => { couponCheckVersion.current++; setCouponCode(event.target.value); setCouponQuote(null); setCheckingCoupon(false); }} /><button className="button secondary" type="button" disabled={checkingCoupon || couponCode.trim() === ""} onClick={() => void verifyCoupon()}>{checkingCoupon ? "验证中…" : "验证"}</button></div>}
      {couponQuote !== null && <div className="detail-list coupon-quote"><div><span>优惠券</span><strong>{couponQuote.coupon.name}</strong></div><div><span>优惠</span><strong>-{formatMoney(couponQuote.coupon_discount_amount)}</strong></div><div><span>券后金额</span><strong>{formatMoney(couponQuote.total_after_coupon)}</strong></div></div>}
      <p className="small muted">余额、会员折扣和套餐折抵由服务端在下单时计算，订单金额以订单详情为准。</p>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" disabled={saving} onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving || periods.length === 0}>{saving ? "正在下单…" : "下单"}</button></div>
    </form>
  </Modal>;
}

function limit(value: number | null): string { return value === null || value === 0 ? "不限" : String(value); }
