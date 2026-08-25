import { useEffect, useState } from "react";
import Markdown from "react-markdown";

import type { PlanOffer } from "../../lib/api";

type PlanCatalogAPI = { listPlanOffers: () => Promise<PlanOffer[]> };

const labels: Record<string, string> = { monthly: "月付", quarterly: "季付", half_yearly: "半年付", yearly: "年付", two_yearly: "两年付", three_yearly: "三年付", onetime: "流量包", reset_traffic: "重置包" };

export function PlanCatalogPage({ api }: { api: PlanCatalogAPI }) {
  const [plans, setPlans] = useState<PlanOffer[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
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
    {loading ? <div className="empty-card">正在加载套餐…</div> : plans.length === 0 ? <div className="empty-card">当前没有可用套餐。</div> : <section className="plan-card-grid" aria-label="订阅套餐列表">{plans.map((plan) => <article className="site-settings-card" key={plan.id}><div className="section-heading"><div><h2>{plan.name}</h2><p className="muted">{plan.transfer_enable} GiB · 速度 {limit(plan.speed_limit)} · 设备 {limit(plan.device_limit)}</p></div>{plan.capacity_remaining === null ? <span className="count-pill">不限量</span> : <span className="count-pill">剩余 {plan.capacity_remaining}</span>}</div>{plan.tags.length > 0 && <p>{plan.tags.join(" · ")}</p>}<div className="plan-offer-prices">{Object.entries(plan.prices).map(([period, cents]) => <span key={period}>{labels[period] ?? period} ¥{formatCents(cents ?? 0)}</span>)}</div>{plan.content !== "" && <div className="markdown-body plan-content"><Markdown components={{
      a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; },
      img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; }
    }}>{plan.content}</Markdown></div>}<p className="small muted">{plan.can_renew ? "当前套餐可续费" : plan.can_purchase ? "可购买" : "暂不可购买"}</p></article>)}</section>}
  </main>;
}

function limit(value: number | null): string { return value === null || value === 0 ? "不限" : String(value); }

function formatCents(cents: number): string { return `${Math.trunc(cents / 100)}.${String(cents % 100).padStart(2, "0")}`; }
