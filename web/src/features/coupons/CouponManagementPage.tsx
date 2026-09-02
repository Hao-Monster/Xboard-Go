import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { Coupon, CouponInput, CouponPage, CouponQuery, CouponType, Plan, PlanPeriod } from "../../lib/api";
import { useCurrency } from "../../lib/currency";

export interface CouponManagementAPI {
  listCoupons: (query?: CouponQuery) => Promise<CouponPage>;
  listPlans: () => Promise<Plan[]>;
  createCoupon: (input: CouponInput) => Promise<Coupon>;
  updateCoupon: (id: number, input: CouponInput) => Promise<Coupon>;
  setCouponVisibility: (id: number, show: boolean) => Promise<Coupon>;
  deleteCoupon: (id: number) => Promise<void>;
  createCouponBatch: (input: CouponInput, count: number) => Promise<Blob>;
}

const periods: Array<[PlanPeriod, string]> = [
  ["monthly", "月付"], ["quarterly", "季付"], ["half_yearly", "半年付"], ["yearly", "年付"],
  ["two_yearly", "两年付"], ["three_yearly", "三年付"], ["onetime", "一次性"], ["reset_traffic", "流量重置包"]
];
const defaultCouponStartedAt = new Date().toISOString();
const defaultCouponEndedAt = new Date(new Date(defaultCouponStartedAt).getTime() + 365 * 86_400_000).toISOString();

export function CouponManagementPage({ api }: { api: CouponManagementAPI }) {
  const { format: formatMoney } = useCurrency();
  const [page, setPage] = useState<CouponPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [plans, setPlans] = useState<Plan[]>([]);
  const [query, setQuery] = useState("");
  const [type, setType] = useState<"" | CouponType>("");
  const [applied, setApplied] = useState<CouponQuery>({ page: 1, page_size: 20 });
  const [editing, setEditing] = useState<Coupon | "new" | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async (next: CouponQuery) => {
    setLoading(true);
    setError("");
    try {
      setPage(await api.listCoupons(next));
      setApplied(next);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    const initial = { page: 1, page_size: 20 } satisfies CouponQuery;
    void Promise.all([api.listCoupons(initial), api.listPlans()]).then(([coupons, nextPlans]) => {
      if (!active) return;
      setPage(coupons);
      setPlans(nextPlans);
      setApplied(initial);
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  const search = (event: FormEvent) => {
    event.preventDefault();
    const next: CouponQuery = { page: 1, page_size: 20 };
    if (query.trim() !== "") next.query = query.trim();
    if (type !== "") next.type = type;
    void load(next);
  };

  const toggle = async (coupon: Coupon) => {
    setError("");
    try {
      const updated = await api.setCouponVisibility(coupon.id, !coupon.show);
      setPage((current) => ({ ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) }));
    } catch (cause) {
      setError(messageOf(cause));
    }
  };

  const remove = async (coupon: Coupon) => {
    if (!window.confirm(`确认删除优惠券 ${coupon.code}？`)) return;
    setError("");
    try {
      await api.deleteCoupon(coupon.id);
      await load(applied);
    } catch (cause) {
      setError(messageOf(cause));
    }
  };

  return <main className="page-shell resource-page coupon-page">
    <header className="page-header"><div><p className="eyebrow">Finance</p><h1>优惠券管理</h1><p className="muted">创建、批量生成和限制优惠券适用的套餐与付款周期。</p></div><button className="button primary" onClick={() => setEditing("new")}>新增优惠券</button></header>
    <form className="ticket-filter-bar" onSubmit={search}><label className="search-field">搜索优惠券<input type="search" placeholder="搜索名称或券码" value={query} maxLength={200} onChange={(event) => setQuery(event.target.value)} /></label><label>类型<select value={type} onChange={(event) => setType(event.target.value === "" ? "" : Number(event.target.value) as CouponType)}><option value="">全部</option><option value="1">固定金额</option><option value="2">百分比</option></select></label><button className="button secondary" type="submit" disabled={loading}>搜索</button></form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载优惠券…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的优惠券。</div> : <section className="resource-table-wrap" aria-label="优惠券列表"><table className="resource-table"><thead><tr><th>ID / 状态</th><th>卷名称</th><th>类型</th><th>卷码</th><th>剩余次数</th><th>每用户次数</th><th>有效期</th><th>操作</th></tr></thead><tbody>{page.items.map((coupon) => <tr key={coupon.id}><td data-label="ID / 状态"><strong>#{coupon.id}</strong><small className="muted">{coupon.show ? "已启用" : "已禁用"}</small></td><td data-label="卷名称">{coupon.name}</td><td data-label="类型">{coupon.type === 1 ? formatMoney(coupon.value) : `${coupon.value}%`}</td><td data-label="卷码"><strong className="monospace">{coupon.code}</strong></td><td data-label="剩余次数">{coupon.limit_use ?? "不限"}</td><td data-label="每用户次数">{coupon.limit_use_with_user ?? "不限"}</td><td data-label="有效期"><small>{formatDate(coupon.started_at)}<br />至 {formatDate(coupon.ended_at)}</small></td><td data-label="操作"><div className="table-actions"><button className="button secondary compact" aria-label={`${coupon.show ? "禁用" : "启用"} ${coupon.code}`} onClick={() => void toggle(coupon)}>{coupon.show ? "禁用" : "启用"}</button><button className="button ghost compact" onClick={() => setEditing(coupon)}>编辑</button><button className="button danger compact" onClick={() => void remove(coupon)}>删除</button></div></td></tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>上一页</button><span>第 {page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>下一页</button></div>}
    </section>}
    {editing !== null && <CouponEditor api={api} plans={plans} coupon={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void load(applied); }} />}
  </main>;
}

function CouponEditor({ api, plans, coupon, onClose, onSaved }: { api: CouponManagementAPI; plans: Plan[]; coupon: Coupon | null; onClose: () => void; onSaved: () => void }) {
  const { code: currency } = useCurrency();
  const [name, setName] = useState(coupon?.name ?? "");
  const [code, setCode] = useState(coupon?.code ?? "");
  const [type, setType] = useState<CouponType>(coupon?.type ?? 1);
  const [value, setValue] = useState(coupon === null ? "0.00" : coupon.type === 1 ? formatCents(coupon.value) : String(coupon.value));
  const [show, setShow] = useState(coupon?.show ?? true);
  const [limitUse, setLimitUse] = useState(coupon?.limit_use === null || coupon === null ? "" : String(coupon.limit_use));
  const [limitUser, setLimitUser] = useState(coupon?.limit_use_with_user === null || coupon === null ? "" : String(coupon.limit_use_with_user));
  const [planIDs, setPlanIDs] = useState<number[]>(coupon?.limit_plan_ids ?? []);
  const [limitPeriods, setLimitPeriods] = useState<PlanPeriod[]>(coupon?.limit_period ?? []);
  const [startedAt, setStartedAt] = useState(localDateTime(coupon?.started_at ?? defaultCouponStartedAt));
  const [endedAt, setEndedAt] = useState(localDateTime(coupon?.ended_at ?? defaultCouponEndedAt));
  const [count, setCount] = useState(1);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const input: CouponInput = {
        code: code.trim(), name: name.trim(), type, value: type === 1 ? parseCents(value) : parseInteger(value, 1, 100), show,
        limit_use: limitUse.trim() === "" ? null : parseInteger(limitUse, 0, 1_000_000_000),
        limit_use_with_user: limitUser.trim() === "" ? null : parseInteger(limitUser, 1, 1_000_000_000),
        limit_plan_ids: planIDs, limit_period: limitPeriods,
        started_at: dateTimeSeconds(startedAt), ended_at: dateTimeSeconds(endedAt)
      };
      if (count > 1) {
        if (coupon !== null) throw new Error("编辑优惠券时不能批量生成");
        const blob = await api.createCouponBatch({ ...input, code: "" }, count);
        downloadBlob(blob, "coupons.csv");
      } else if (coupon === null) {
        await api.createCoupon(input);
      } else {
        await api.updateCoupon(coupon.id, input);
      }
      onSaved();
    } catch (cause) {
      setError(messageOf(cause));
      setSaving(false);
    }
  };

  const togglePlan = (id: number, checked: boolean) => setPlanIDs(checked ? [...planIDs, id] : planIDs.filter((value) => value !== id));
  const togglePeriod = (period: PlanPeriod, checked: boolean) => setLimitPeriods(checked ? [...limitPeriods, period] : limitPeriods.filter((value) => value !== period));

  return <Modal title={coupon === null ? "新增优惠券" : "编辑优惠券"} onClose={onClose}><div className="modal-header"><h2>{coupon === null ? "新增优惠券" : "编辑优惠券"}</h2><button className="icon-button" aria-label="关闭优惠券编辑" onClick={onClose}>×</button></div><form className="form-stack coupon-form" onSubmit={(event) => void submit(event)}>
    <div className="form-grid"><label>卷名称<input required maxLength={200} value={name} onChange={(event) => setName(event.target.value)} /></label><label>卷码<input required={count === 1} disabled={count > 1} maxLength={64} value={count > 1 ? "自动生成" : code} onChange={(event) => setCode(event.target.value)} /></label></div>
    {coupon === null && <label>批量数量<input type="number" min={1} max={500} value={count} onChange={(event) => setCount(Number(event.target.value))} /></label>}
    <div className="form-grid"><label>优惠类型<select value={type} onChange={(event) => setType(Number(event.target.value) as CouponType)}><option value="1">固定金额</option><option value="2">百分比</option></select></label><label>{type === 1 ? `优惠金额（${currency}）` : "优惠比例（%）"}<input inputMode="decimal" required value={value} onChange={(event) => setValue(event.target.value)} /></label></div>
    <div className="form-grid"><label>开始时间<input type="datetime-local" required value={startedAt} onChange={(event) => setStartedAt(event.target.value)} /></label><label>结束时间<input type="datetime-local" required value={endedAt} onChange={(event) => setEndedAt(event.target.value)} /></label></div>
    <div className="form-grid"><label>可用总次数<input type="number" min={0} placeholder="不限制" value={limitUse} onChange={(event) => setLimitUse(event.target.value)} /></label><label>每用户可用次数<input type="number" min={1} placeholder="不限制" value={limitUser} onChange={(event) => setLimitUser(event.target.value)} /></label></div>
    <label className="switch-label"><input type="checkbox" checked={show} onChange={(event) => setShow(event.target.checked)} />启用优惠券</label>
    <fieldset className="settings-fieldset"><legend>可用付款周期（不选表示不限）</legend><div className="checkbox-grid">{periods.map(([period, label]) => <label className="switch-label" key={period}><input type="checkbox" checked={limitPeriods.includes(period)} onChange={(event) => togglePeriod(period, event.target.checked)} />{label}</label>)}</div></fieldset>
    <fieldset className="settings-fieldset"><legend>可用订阅套餐（不选表示不限）</legend><div className="checkbox-grid">{plans.map((plan) => <label className="switch-label" key={plan.id}><input type="checkbox" checked={planIDs.includes(plan.id)} onChange={(event) => togglePlan(plan.id, event.target.checked)} />{plan.name}</label>)}</div></fieldset>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" disabled={saving} onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : count > 1 ? "生成并下载" : "保存优惠券"}</button></div>
  </form></Modal>;
}

function parseCents(value: string): number {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim());
  if (match === null || match[1] === undefined) throw new Error("优惠金额格式无效");
  const cents = BigInt(match[1]) * 100n + BigInt((match[2] ?? "").padEnd(2, "0") || "0");
  if (cents < 1n || cents > 9_000_000_000_000_000n) throw new Error("优惠金额超出范围");
  return Number(cents);
}

function parseInteger(value: string, minimum: number, maximum: number): number {
  if (!/^\d+$/.test(value.trim())) throw new Error("次数或比例格式无效");
  const result = Number(value);
  if (!Number.isSafeInteger(result) || result < minimum || result > maximum) throw new Error("次数或比例超出范围");
  return result;
}

function dateTimeSeconds(value: string): number {
  const milliseconds = new Date(value).getTime();
  if (!Number.isFinite(milliseconds) || milliseconds < 0) throw new Error("有效期格式无效");
  return Math.floor(milliseconds / 1000);
}

function localDateTime(value: string): string {
  const date = new Date(value);
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

function formatCents(value: number): string { return `${Math.trunc(value / 100)}.${String(value % 100).padStart(2, "0")}`; }
function formatDate(value: string): string { return new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)); }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "优惠券请求失败"; }
