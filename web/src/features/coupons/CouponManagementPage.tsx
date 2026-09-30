import { translateAdmin } from "../../lib/adminLocale";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import { DateRangeInput } from "../../components/DateRangeInput";
import { NodeFilter } from "../nodes/NodeFilter";
import { Modal } from "../../components/Overlay";
import type { Coupon, CouponInput, CouponPage, CouponQuery, CouponType, Plan, PlanPeriod } from "../../lib/api";

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

export function CouponManagementPage({ api }: { api: CouponManagementAPI }) {
  const [page, setPage] = useState<CouponPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [plans, setPlans] = useState<Plan[]>([]);
  const [query, setQuery] = useState("");
  const [type, setType] = useState<"" | CouponType>("");
  const [applied, setApplied] = useState<CouponQuery>({ page: 1, page_size: 20 });
  const [editing, setEditing] = useState<Coupon | "new" | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const loadSequence = useRef(0);

  const load = useCallback(async (next: CouponQuery) => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    setError("");
    try {
      const result = await api.listCoupons(next);
      if (sequence !== loadSequence.current) return;
      setPage(result);
      setApplied(next);
    } catch (cause) {
      if (sequence === loadSequence.current) setError(messageOf(cause));
    } finally {
      if (sequence === loadSequence.current) setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    const requests = loadSequence;
    const sequence = ++requests.current;
    let active = true;
    void Promise.all([api.listPlans(), api.listCoupons({ page: 1, page_size: 20 })]).then(([nextPlans, result]) => {
      if (!active) return;
      setPlans(nextPlans);
      if (sequence === requests.current) { setPage(result); setApplied({ page: 1, page_size: 20 }); }
    }).catch((cause: unknown) => { if (active && sequence === requests.current) setError(messageOf(cause)); })
      .finally(() => { if (active && sequence === requests.current) setLoading(false); });
    return () => { active = false; ++requests.current; };
  }, [api]);

  const search = (nextQuery: string, nextType: "" | CouponType) => {
    setQuery(nextQuery); setType(nextType);
    const next: CouponQuery = { page: 1, page_size: 20 };
    if (nextQuery.trim() !== "") next.query = nextQuery.trim();
    if (nextType !== "") next.type = nextType;
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
    <header className="page-header"><div><h1>{translateAdmin("优惠券管理")}</h1><p className="muted">{translateAdmin("在这里可以查看优惠券，包括增加、查看、删除等操作。")}</p></div></header>
    <div className="resource-toolbar"><button className="button secondary compact" onClick={() => setEditing("new")}>{translateAdmin("添加优惠券")}</button><input type="search" aria-label="搜索优惠券" placeholder={translateAdmin("搜索优惠券...")} value={query} maxLength={200} onChange={event => search(event.target.value, type)} /><NodeFilter label={translateAdmin("类型")} options={[{value:"1",label:"按金额优惠"},{value:"2",label:"按比例优惠"}]} value={type === "" ? [] : [String(type)]} onChange={values => search(query, values.length ? Number(values.at(-1)) as CouponType : "")} /></div>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载优惠券…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的优惠券。</div> : <section className="resource-table-wrap" aria-label="优惠券列表"><table className="resource-table"><thead><tr><th>ID / 状态</th><th>{translateAdmin("卷名称")}</th><th>{translateAdmin("类型")}</th><th>{translateAdmin("卷码")}</th><th>{translateAdmin("剩余次数")}</th><th>每用户次数</th><th>{translateAdmin("有效期")}</th><th>{translateAdmin("操作")}</th></tr></thead><tbody>{page.items.map((coupon) => <tr key={coupon.id}><td data-label="ID / 状态"><strong>#{coupon.id}</strong><small className="muted">{coupon.show ? translateAdmin("已启用") : translateAdmin("已禁用")}</small></td><td data-label="卷名称">{coupon.name}</td><td data-label="类型">{coupon.type === 1 ? `¥${formatCents(coupon.value)}` : `${coupon.value}%`}</td><td data-label="卷码"><strong className="monospace">{coupon.code}</strong></td><td data-label="剩余次数">{coupon.limit_use ?? "不限"}</td><td data-label="每用户次数">{coupon.limit_use_with_user ?? "不限"}</td><td data-label="有效期"><small>{formatDate(coupon.started_at)}<br />{translateAdmin("至")}{formatDate(coupon.ended_at)}</small></td><td data-label="操作"><div className="table-actions"><button className="button secondary compact" aria-label={`${coupon.show ? translateAdmin("禁用") : translateAdmin("启用")} ${coupon.code}`} onClick={() => void toggle(coupon)}>{coupon.show ? translateAdmin("禁用") : translateAdmin("启用")}</button><button className="button ghost compact" onClick={() => setEditing(coupon)}>{translateAdmin("编辑")}</button><button className="button danger compact" onClick={() => void remove(coupon)}>{translateAdmin("删除")}</button></div></td></tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>{translateAdmin("上一页")}</button><span>{translateAdmin("第")}{page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>{translateAdmin("下一页")}</button></div>}
    </section>}
    {editing !== null && <CouponEditor api={api} plans={plans} coupon={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void load(applied); }} />}
  </main>;
}

function CouponEditor({ api, plans, coupon, onClose, onSaved }: { api: CouponManagementAPI; plans: Plan[]; coupon: Coupon | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(coupon?.name ?? "");
  const [code, setCode] = useState(coupon?.code ?? "");
  const [type, setType] = useState<CouponType>(coupon?.type ?? 1);
  const [value, setValue] = useState(coupon === null ? "0.00" : coupon.type === 1 ? formatCents(coupon.value) : String(coupon.value));
  const show = coupon?.show ?? true;
  const [limitUse, setLimitUse] = useState(coupon?.limit_use === null || coupon === null ? "" : String(coupon.limit_use));
  const [limitUser, setLimitUser] = useState(coupon?.limit_use_with_user === null || coupon === null ? "" : String(coupon.limit_use_with_user));
  const [planIDs, setPlanIDs] = useState<number[]>(coupon?.limit_plan_ids ?? []);
  const [limitPeriods, setLimitPeriods] = useState<PlanPeriod[]>(coupon?.limit_period ?? []);
  const [openedAt] = useState(() => new Date());
  const [startedAt, setStartedAt] = useState(() => localDateTime(coupon?.started_at ?? openedAt.toISOString()));
  const [endedAt, setEndedAt] = useState(() => {
    const end = new Date(openedAt);
    end.setDate(end.getDate() + 7);
    return localDateTime(coupon?.ended_at ?? end.toISOString());
  });
  const [countText, setCountText] = useState("");
  const count = countText === "" ? 1 : Number(countText);
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


  return <Modal title={coupon === null ? translateAdmin("添加优惠券") : translateAdmin("编辑优惠券")} onClose={onClose}><div className="modal-header"><h2>{coupon === null ? translateAdmin("添加优惠券") : translateAdmin("编辑优惠券")}</h2><button className="icon-button" aria-label="关闭优惠券编辑" onClick={onClose}>×</button></div><form className="form-stack coupon-form" onSubmit={(event) => void submit(event)}>
    <div className="form-grid"><label>{translateAdmin("优惠券名称")}<input autoFocus placeholder={translateAdmin("请输入优惠券名称")} required maxLength={200} value={name} onChange={(event) => setName(event.target.value)} /></label><label>{translateAdmin("自定义优惠码")}<input placeholder={translateAdmin("自定义优惠码，留空则自动生成")} disabled={count > 1} maxLength={64} value={count > 1 ? "自动生成" : code} onChange={(event) => setCode(event.target.value)} /></label></div>
    {coupon === null && <label>{translateAdmin("批量生成数量")}<input type="number" min={1} max={500} placeholder="留空则只生成单个优惠码" value={countText} onChange={(event) => setCountText(event.target.value)} /></label>}
    <div className="form-grid"><label>优惠类型<select value={type} onChange={(event) => setType(Number(event.target.value) as CouponType)}><option value="1">固定金额</option><option value="2">百分比</option></select></label><label>{type === 1 ? "优惠金额（元）" : "优惠比例（%）"}<input inputMode="decimal" required value={value} onChange={(event) => setValue(event.target.value)} /></label></div>
    <DateRangeInput start={startedAt} end={endedAt} onStartChange={setStartedAt} onEndChange={setEndedAt} />
    <div className="form-grid"><label>{translateAdmin("最大使用次数")}<input type="number" min={0} placeholder="不限制" value={limitUse} onChange={(event) => setLimitUse(event.target.value)} /></label><label>{translateAdmin("每个用户可使用次数")}<input type="number" min={1} placeholder="不限制" value={limitUser} onChange={(event) => setLimitUser(event.target.value)} /></label></div>
    <div className="field-block"><span>{translateAdmin("指定周期")}</span><NodeFilter label={translateAdmin("指定周期")} options={periods.map(([value,label]) => ({value,label}))} value={limitPeriods} onChange={values => setLimitPeriods(values as PlanPeriod[])} /><small className="muted">{translateAdmin("选择可以使用优惠券的订阅周期，留空表示不限制使用周期")}</small></div>
    <div className="field-block"><span>{translateAdmin("指定订阅")}</span><NodeFilter label={translateAdmin("指定订阅")} options={plans.map(plan => ({value:String(plan.id),label:plan.name}))} value={planIDs.map(String)} onChange={values => setPlanIDs(values.map(Number))} /><small className="muted">{translateAdmin("选择可以使用优惠券的订阅计划，留空表示不限制计划")}</small></div>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" disabled={saving} onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : count > 1 ? "生成并下载" : translateAdmin("确认")}</button></div>
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
