import "./PlanAudienceModal.css";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Modal } from "../../components/Overlay";
import type { PlanAudienceAPI, PlanAudienceUser, PlanVisibility } from "../../lib/api";

export function PlanAudienceModal({ id, api, onClose, onSaved }: { id: number; api: PlanAudienceAPI; onClose: () => void; onSaved: () => void }) {
  const [plan, setPlan] = useState<PlanVisibility | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    let active = true;
    void api.getPlanVisibility(id).then(({plan}) => { if (active) setPlan(plan); }).catch((cause: unknown) => { if (active) setError(message(cause)); });
    return () => { active = false; };
  }, [api, id]);
  async function save(event: FormEvent) {
    event.preventDefault(); if (!plan || saving) return;
    setSaving(true); setError("");
    try {
      await api.savePlanVisibility({plan_id: id, customer_visibility: plan.customer_visibility, distributor_visibility: plan.distributor_visibility, customer_user_ids: plan.customer_users.map(user => user.id), distributor_user_ids: plan.distributor_users.map(user => user.id)});
      onSaved();
    } catch (cause) { setError(message(cause)); } finally { setSaving(false); }
  }
  return <Modal className="plan-audience-modal" title="套餐购买权限" onClose={() => { if (!saving) onClose(); }}>
    <form onSubmit={event => void save(event)} className="resource-form">
      <h2>套餐购买权限{plan ? ` · ${plan.name}` : ""}</h2>
      {error && <div role="alert" className="alert error">{error}</div>}
      {!plan && !error && <p role="status">正在加载权限…</p>}
      {plan && <fieldset disabled={saving}>
        <label>客户购买范围<select value={plan.customer_visibility} onChange={event => setPlan({...plan, customer_visibility: event.target.value === "selected" ? "selected" : "all"})}><option value="all">全部客户</option><option value="selected">指定客户</option></select></label>
        {plan.customer_visibility === "selected" && <Recipients key="customer" audience="customer" api={api} selected={plan.customer_users} onChange={users => setPlan({...plan,customer_users:users})} />}
        <label>分销商购买范围<select value={plan.distributor_visibility} onChange={event => setPlan({...plan, distributor_visibility: event.target.value === "selected" ? "selected" : event.target.value === "all" ? "all" : "none"})}><option value="none">禁止分销</option><option value="all">全部分销商</option><option value="selected">指定分销商</option></select></label>
        {plan.distributor_visibility === "selected" && <Recipients key="distributor" audience="distributor" api={api} selected={plan.distributor_users} onChange={users => setPlan({...plan,distributor_users:users})} />}
        <p className="muted">名单为空时，没有人获得指定购买权限。已有订阅的续费权限保持不变。</p>
      </fieldset>}
      <div className="form-actions"><button type="button" className="button secondary" disabled={saving} onClick={onClose}>取消</button><button className="button primary" disabled={!plan || saving}>{saving ? "正在保存…" : "保存购买权限"}</button></div>
    </form>
  </Modal>;
}

function Recipients({audience, api, selected, onChange}: {audience: "customer" | "distributor"; api: PlanAudienceAPI; selected: PlanAudienceUser[]; onChange: (users: PlanAudienceUser[]) => void}) {
  const [query, setQuery] = useState(""); const [results, setResults] = useState<PlanAudienceUser[]>([]);
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false); const sequence = useRef(0);
  useEffect(() => () => { sequence.current++; }, []);
  async function search() {
    const request = ++sequence.current; setBusy(true); setError(""); setResults([]);
    try { const users = await api.searchPlanAudienceUsers(audience, query.trim()); if (request === sequence.current) setResults(users); }
    catch (cause) { if (request === sequence.current) setError(message(cause)); }
    finally { if (request === sequence.current) setBusy(false); }
  }
  return <section aria-label={audience === "customer" ? "指定客户名单" : "指定分销商名单"}>
    <label>{audience === "customer" ? "搜索客户" : "搜索分销商"}<input type="search" onKeyDown={event => {if(event.key === "Enter") {event.preventDefault(); if(!busy && query.trim().length >= 2) void search();}}} maxLength={255} value={query} onChange={event => { sequence.current++; setBusy(false); setQuery(event.target.value); setResults([]); }} placeholder="输入至少两个字符" /></label>
    <button type="button" className="button secondary compact" disabled={busy || query.trim().length < 2} onClick={() => void search()}>{busy ? "搜索中…" : "搜索名单"}</button>
    {error && <p role="alert">{error}</p>}
    <ul>{results.map(user => <li key={user.id}>{user.email} {user.distributor_name} {user.banned && "（已封禁）"}<button type="button" disabled={selected.length >= 5000 || selected.some(item => item.id === user.id)} onClick={() => onChange([...selected,user])}>添加</button></li>)}</ul>
    <p>已选择 {selected.length} 人（最多 5000 人）</p>
    <ul>{selected.map(user => <li key={user.id}>{user.email}<button type="button" aria-label={`移除 ${user.email}`} onClick={() => onChange(selected.filter(item => item.id !== user.id))}>移除</button></li>)}</ul>
  </section>;
}
function message(cause: unknown) { return cause instanceof Error ? cause.message : "请求失败，请重试。"; }
