import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, Plan, PlanInput, PlanPeriod, PlanPrices, ServerGroup } from "../../lib/api";

type PlansAPI = Pick<AdminAPI, "listPlans" | "createPlan" | "updatePlan" | "setPlanState" | "reorderPlans" | "deletePlan" | "listServerGroups">;

const periods: Array<{ key: PlanPeriod; label: string }> = [
  { key: "monthly", label: "月付" }, { key: "quarterly", label: "季付" }, { key: "half_yearly", label: "半年付" },
  { key: "yearly", label: "年付" }, { key: "two_yearly", label: "两年付" }, { key: "three_yearly", label: "三年付" },
  { key: "onetime", label: "流量包" }, { key: "reset_traffic", label: "重置包" }
];
const maxPlanPriceCents = 9_000_000_000_000_000n;
const planDescriptionTemplate = `## 套餐特点
• 高速稳定的全球网络接入
• 支持多设备同时在线
• 无限制的流量重置

## 使用说明
1. 支持设备：iOS、Android、Windows、macOS
2. 24/7 技术支持
3. 自动定期流量重置

## 注意事项
- 禁止滥用
- 遵守当地法律法规
- 支持随时更换套餐`;

export function PlanManagementPage({ api }: { api: PlansAPI }) {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Plan | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Plan | null>(null);
  const [sorting, setSorting] = useState(false);
  const [savingOrder, setSavingOrder] = useState(false);
  const [busyIDs, setBusyIDs] = useState<Set<number>>(() => new Set());

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [nextPlans, nextGroups] = await Promise.all([api.listPlans(), api.listServerGroups()]);
      setPlans(nextPlans);
      setGroups(nextGroups);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    void Promise.all([api.listPlans(), api.listServerGroups()]).then(([nextPlans, nextGroups]) => {
      if (!active) return;
      setPlans(nextPlans);
      setGroups(nextGroups);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const visiblePlans = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (query === "") return plans;
    return plans.filter((plan) => plan.name.toLocaleLowerCase().includes(query) || plan.tags.some((tag) => tag.toLocaleLowerCase().includes(query)));
  }, [plans, search]);
  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);

  const updateState = async (plan: Plan, field: "show" | "sell" | "renew", value: boolean) => {
    setBusyIDs((current) => new Set(current).add(plan.id));
    setError("");
    setPlans((current) => current.map((item) => item.id === plan.id ? { ...item, [field]: value } : item));
    try {
      const state = { show: plan.show, sell: plan.sell, renew: plan.renew, [field]: value };
      const updated = await api.setPlanState(plan.id, plan.revision, state.show, state.sell, state.renew);
      setPlans((current) => current.map((item) => item.id === updated.id ? updated : item));
    } catch (cause) {
      setPlans((current) => current.map((item) => item.id === plan.id ? plan : item));
      setError(messageOf(cause));
    } finally {
      setBusyIDs((current) => {
        const next = new Set(current);
        next.delete(plan.id);
        return next;
      });
    }
  };

  const move = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= plans.length) return;
    setPlans((current) => {
      const next = [...current];
	  const sourcePlan = next[index];
	  const targetPlan = next[target];
	  if (sourcePlan === undefined || targetPlan === undefined) return current;
	  next[index] = targetPlan;
	  next[target] = sourcePlan;
      return next;
    });
  };

  const saveOrder = async () => {
    setSavingOrder(true);
    setError("");
    try {
      setPlans(await api.reorderPlans(plans.map((plan) => plan.id)));
      setSorting(false);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSavingOrder(false);
    }
  };

  return <main className="page-shell resource-page plan-management-page">
    <header className="page-header"><div><p className="eyebrow">Catalog</p><h1>套餐管理</h1><p className="muted">管理套餐权益、周期价格、容量、销售与续费状态。</p></div><div className="row-actions">
      <button className="button secondary" onClick={() => setSorting((value) => !value)}>{sorting ? "取消排序" : "编辑排序"}</button>
      <button className="button primary" onClick={() => setEditing(null)}>添加套餐</button>
    </div></header>
    <label className="search-field">搜索套餐<input type="search" placeholder="搜索套餐..." value={search} onChange={(event) => setSearch(event.target.value)} /></label>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>刷新</button></div>}
    {sorting && plans.length > 0 && <div className="form-actions"><button className="button primary" disabled={savingOrder} onClick={() => void saveOrder()}>{savingOrder ? "正在保存排序…" : "保存排序"}</button></div>}
    {loading ? <div className="empty-card">正在加载套餐…</div> : plans.length === 0 ? <div className="empty-card">尚未创建套餐。</div> : visiblePlans.length === 0 ? <div className="empty-card">没有匹配的套餐。</div> :
      <section className="resource-table-wrap" aria-label="套餐列表"><table className="resource-table"><thead><tr><th>套餐</th><th>权益</th><th>统计</th><th>价格</th><th>容量</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>{visiblePlans.map((plan) => {
          const sourceIndex = plans.findIndex((item) => item.id === plan.id);
          return <tr key={plan.id}>
            <td data-label="套餐"><strong>{plan.name}</strong><small className="muted monospace">PID {plan.id} · Revision {plan.revision}</small>{plan.tags.length > 0 && <small>{plan.tags.join(" · ")}</small>}</td>
            <td data-label="权益">{plan.transfer_enable} GiB<small className="muted">速度 {limitText(plan.speed_limit)} · 设备 {limitText(plan.device_limit)}</small><small className="muted">权限组 {plan.group_id === null ? "不限制" : groupNames.get(plan.group_id) ?? `#${plan.group_id}`}</small></td>
            <td data-label="统计"><strong>总 {plan.users_count}</strong><small className="muted">有效 {plan.active_users_count} · 活跃率 {plan.users_count > 0 ? Math.round(plan.active_users_count / plan.users_count * 100) : 0}%</small></td>
            <td data-label="价格"><div className="plan-offer-prices">{periods.some(({ key }) => plan.prices[key] !== undefined) ? periods.map(({ key, label }) => plan.prices[key] === undefined ? null : <span key={key}>{label} ¥{formatCents(plan.prices[key] ?? 0)}</span>) : "未设置"}</div></td>
            <td data-label="容量">{plan.capacity_limit === null || plan.capacity_limit <= 0 ? "不限量" : `${plan.capacity_users_count}/${plan.capacity_limit}`}</td>
            <td data-label="状态"><div className="plan-state-list">
              <label><input type="checkbox" disabled={busyIDs.has(plan.id)} checked={plan.show} onChange={(event) => void updateState(plan, "show", event.target.checked)} />展示</label>
              <label><input type="checkbox" disabled={busyIDs.has(plan.id)} checked={plan.sell} onChange={(event) => void updateState(plan, "sell", event.target.checked)} />销售</label>
              <label><input type="checkbox" disabled={busyIDs.has(plan.id)} checked={plan.renew} onChange={(event) => void updateState(plan, "renew", event.target.checked)} />续费</label>
            </div></td>
            <td data-label="操作"><div className="row-actions">{sorting ? <>
              <button className="button secondary compact" disabled={sourceIndex === 0} aria-label={`上移套餐：${plan.name}`} onClick={() => move(sourceIndex, -1)}>上移</button>
              <button className="button secondary compact" disabled={sourceIndex === plans.length - 1} aria-label={`下移套餐：${plan.name}`} onClick={() => move(sourceIndex, 1)}>下移</button>
            </> : <>
              <button className="button secondary compact" aria-label={`编辑套餐：${plan.name}`} onClick={() => setEditing(plan)}>编辑</button>
              <button className="button ghost compact danger-text" aria-label={`删除套餐：${plan.name}`} onClick={() => setDeleting(plan)}>删除</button>
            </>}</div></td>
          </tr>;
        })}</tbody></table></section>}
    {editing !== undefined && <PlanEditor api={api} groups={groups} plan={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => {
      setPlans((current) => editing === null ? [...current, saved] : current.map((item) => item.id === saved.id ? saved : item));
      setEditing(undefined);
    }} />}
    {deleting !== null && <PlanDelete api={api} plan={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setPlans((current) => current.filter((item) => item.id !== deleting.id)); setDeleting(null); }} />}
  </main>;
}

type PlanDraft = Omit<PlanInput, "prices" | "tags"> & { tagsText: string; prices: Record<PlanPeriod, string>; forceUpdate: boolean };

function PlanEditor({ api, groups, plan, onClose, onSaved }: { api: PlansAPI; groups: ServerGroup[]; plan: Plan | null; onClose: () => void; onSaved: (plan: Plan) => void }) {
  const title = plan === null ? "添加套餐" : "编辑套餐";
  const [draft, setDraft] = useState<PlanDraft>(() => planDraft(plan));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const update = <K extends keyof PlanDraft,>(key: K, value: PlanDraft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const input: PlanInput = {
        group_id: draft.group_id, transfer_enable: draft.transfer_enable, name: draft.name, speed_limit: draft.speed_limit,
        content: draft.content, reset_traffic_method: draft.reset_traffic_method, capacity_limit: draft.capacity_limit,
        prices: pricesToCents(draft.prices), device_limit: draft.device_limit,
        tags: draft.tagsText.split(/[,，\n]/).map((tag) => tag.trim()).filter(Boolean)
      };
      onSaved(plan === null ? await api.createPlan(input) : await api.updatePlan(plan.id, plan.revision, input, draft.forceUpdate));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };
  return <Modal title={title} onClose={onClose}><ModalHeader title={title} onClose={onClose} /><form className="form-stack plan-editor" onSubmit={(event) => void submit(event)}>
    <label>套餐名称<input required maxLength={255} value={draft.name} onChange={(event) => update("name", event.target.value)} /></label>
    <label>标签<input value={draft.tagsText} placeholder="推荐, 稳定" onChange={(event) => update("tagsText", event.target.value)} /></label>
    <label>服务器分组<select value={draft.group_id ?? ""} onChange={(event) => update("group_id", event.target.value === "" ? null : Number(event.target.value))}><option value="">不限制</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
    <div className="registration-policy-grid">
      <label>流量（GiB）<input type="number" min={1} required value={draft.transfer_enable} onChange={(event) => update("transfer_enable", Number(event.target.value))} /></label>
      <OptionalNumber label="速度限制" value={draft.speed_limit} maximum={1_000_000_000} onChange={(value) => update("speed_limit", value)} />
      <OptionalNumber label="设备限制" value={draft.device_limit} maximum={1000} onChange={(value) => update("device_limit", value)} />
      <OptionalNumber label="容量限制" value={draft.capacity_limit} maximum={1_000_000_000} onChange={(value) => update("capacity_limit", value)} />
    </div>
    <label>流量重置方式<select value={draft.reset_traffic_method ?? ""} onChange={(event) => update("reset_traffic_method", event.target.value === "" ? null : Number(event.target.value))}>
      <option value="">跟随系统</option><option value={0}>每月 1 日</option><option value={1}>按到期日每月</option><option value={2}>永不</option><option value={3}>每年 1 月 1 日</option><option value={4}>按到期月日每年</option>
    </select></label>
    <fieldset className="settings-fieldset"><legend>周期价格</legend><div className="plan-price-grid">{periods.map((period) => <label key={period.key}>{period.label}<input type="number" min={0} step="0.01" inputMode="decimal" value={draft.prices[period.key]} onChange={(event) => update("prices", { ...draft.prices, [period.key]: event.target.value })} /></label>)}</div></fieldset>
    <div className="form-stack"><div className="section-heading"><strong>套餐描述</strong><div className="row-actions">
      <button className="button secondary compact" type="button" onClick={() => update("content", planDescriptionTemplate)}>使用模板</button>
      <button className="button secondary compact" type="button" onClick={() => setPreviewing((value) => !value)}>{previewing ? "隐藏预览" : "显示预览"}</button>
    </div></div><label className="sr-only" htmlFor="plan-description">套餐描述</label><textarea id="plan-description" value={draft.content} onChange={(event) => update("content", event.target.value)} />
      <small className="muted">支持安全的 Markdown；原始 HTML 不会执行。</small>
      {previewing && <div className="markdown-body plan-content-preview" aria-label="套餐描述预览"><SafeMarkdown>{draft.content}</SafeMarkdown></div>}
    </div>
    {plan !== null && <label className="switch-label"><input type="checkbox" checked={draft.forceUpdate} onChange={(event) => update("forceUpdate", event.target.checked)} />强制同步套餐权益到现有用户</label>}
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving} type="submit">{saving ? "正在保存…" : "保存"}</button></div>
  </form></Modal>;
}

function PlanDelete({ api, plan, onClose, onDeleted }: { api: PlansAPI; plan: Plan; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true); setError("");
    try { await api.deletePlan(plan.id); onDeleted(); } catch (cause) { setError(messageOf(cause)); setBusy(false); }
  };
  return <Modal title="删除套餐" onClose={onClose}><ModalHeader title="删除套餐" onClose={onClose} /><p>确定删除“{plan.name}”吗？仍被用户或业务记录引用时，服务端会拒绝删除。</p>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : "确认删除"}</button></div></Modal>;
}

function OptionalNumber({ label, value, maximum, onChange }: { label: string; value: number | null; maximum: number; onChange: (value: number | null) => void }) {
  return <label>{label}<input type="number" min={0} max={maximum} value={value ?? ""} placeholder="不限" onChange={(event) => onChange(event.target.value === "" ? null : Number(event.target.value))} /></label>;
}

function planDraft(plan: Plan | null): PlanDraft {
  const prices = Object.fromEntries(periods.map(({ key }) => {
	const cents = plan?.prices[key];
	return [key, cents === undefined ? "" : formatCents(cents)];
  })) as Record<PlanPeriod, string>;
  return { group_id: plan?.group_id ?? null, transfer_enable: plan?.transfer_enable ?? 1, name: plan?.name ?? "", speed_limit: plan?.speed_limit ?? null, content: plan?.content ?? "", reset_traffic_method: plan?.reset_traffic_method ?? null, capacity_limit: plan?.capacity_limit ?? null, prices, device_limit: plan?.device_limit ?? null, tagsText: plan?.tags.join(", ") ?? "", forceUpdate: false };
}

function pricesToCents(values: Record<PlanPeriod, string>): PlanPrices {
  const prices: PlanPrices = {};
  for (const { key } of periods) {
    const raw = values[key].trim();
    if (raw === "") continue;
    const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(raw);
    const label = periods.find((period) => period.key === key)?.label ?? key;
    if (match === null) throw new Error(`${label}价格无效`);
    const whole = match[1];
    if (whole === undefined) throw new Error(`${label}价格无效`);
    const cents = BigInt(whole) * 100n + BigInt((match[2] ?? "").padEnd(2, "0") || "0");
    if (cents > maxPlanPriceCents) throw new Error(`${label}价格超出范围`);
    prices[key] = Number(cents);
  }
  return prices;
}

function formatCents(cents: number): string {
  return `${Math.trunc(cents / 100)}.${String(cents % 100).padStart(2, "0")}`;
}

function limitText(value: number | null): string { return value === null || value === 0 ? "不限" : String(value); }

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) { return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>; }

function SafeMarkdown({ children }: { children: string }) {
  return <Markdown components={{
    a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; },
    img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; }
  }}>{children}</Markdown>;
}

function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "套餐请求失败"; }
