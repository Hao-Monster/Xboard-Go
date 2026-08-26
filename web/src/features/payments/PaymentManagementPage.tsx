import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type {
  AdminAPI, PaymentConfigField, PaymentMethod, PaymentMethodInput, PaymentProvider, PaymentProviderDefinition
} from "../../lib/api";

type PaymentsAPI = Pick<AdminAPI,
  "listPaymentProviders" | "listAdminPayments" | "createPayment" | "updatePayment" |
  "setPaymentEnabled" | "reorderPayments" | "deletePayment"
>;

export function PaymentManagementPage({ api }: { api: PaymentsAPI }) {
  const [definitions, setDefinitions] = useState<PaymentProviderDefinition[]>([]);
  const [methods, setMethods] = useState<PaymentMethod[]>([]);
  const [total, setTotal] = useState(0);
  const [query, setQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<PaymentMethod | null | undefined>(undefined);
  const [ordering, setOrdering] = useState<PaymentMethod[] | null>(null);
  const [busyID, setBusyID] = useState(0);

  const load = useCallback(async (search: string) => {
    setLoading(true);
    setError("");
    try {
      const [providerResult, paymentResult] = await Promise.all([
        api.listPaymentProviders(), api.listAdminPayments(1, 200, search.trim())
      ]);
      setDefinitions(providerResult);
      setMethods(paymentResult.items);
      setTotal(paymentResult.total);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    void Promise.all([api.listPaymentProviders(), api.listAdminPayments(1, 200, "")]).then(([providerResult, paymentResult]) => {
      if (!active) return;
      setDefinitions(providerResult);
      setMethods(paymentResult.items);
      setTotal(paymentResult.total);
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  const toggle = async (method: PaymentMethod) => {
    if (busyID !== 0) return;
    setBusyID(method.id);
    setError("");
    try {
      const updated = await api.setPaymentEnabled(method.id, !method.enable);
      setMethods((current) => current.map((item) => item.id === updated.id ? updated : item));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusyID(0);
    }
  };

  const remove = async (method: PaymentMethod) => {
    if (!window.confirm(`确认删除支付方式“${method.name}”？已被订单使用的支付方式只能禁用。`)) return;
    setBusyID(method.id);
    setError("");
    try {
      await api.deletePayment(method.id);
      setMethods((current) => current.filter((item) => item.id !== method.id));
      setTotal((current) => Math.max(0, current - 1));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusyID(0);
    }
  };

  return <main className="page-shell resource-page payment-page">
    <header className="page-header"><div><p className="eyebrow">Payments</p><h1>支付配置</h1><p className="muted">在这里可以配置支付方式，包括支付宝、数字货币和聚合支付。</p></div><div className="row-actions"><button className="button secondary" disabled={loading || query.trim() !== "" || methods.length < 2 || total !== methods.length} onClick={() => setOrdering([...methods])}>调整排序</button><button className="button primary" disabled={definitions.length === 0} onClick={() => setEditing(null)}>添加支付方式</button></div></header>
    <form className="system-filter-bar payment-filter-bar" onSubmit={(event) => { event.preventDefault(); void load(query); }}><label>搜索<input value={query} maxLength={255} placeholder="显示名称或支付接口" onChange={(event) => setQuery(event.target.value)} /></label><button className="button secondary" disabled={loading}>{loading ? "正在查询…" : "查询"}</button></form>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load(query)}>重试</button></div>}
    {loading && methods.length === 0 ? <div className="empty-card">正在加载支付方式…</div> : <section className="resource-table-wrap" aria-label="支付方式列表"><table className="resource-table"><thead><tr><th>ID</th><th>启用</th><th>显示名称</th><th>支付接口</th><th>手续费</th><th>通知地址</th><th>操作</th></tr></thead><tbody>{methods.length === 0 ? <tr><td colSpan={7} className="table-empty-cell">暂无数据。添加支付方式后，用户才能为付费订单结算。</td></tr> : methods.map((method) => <tr key={method.id}>
      <td data-label="ID">{method.id}</td><td data-label="启用"><button className={`button compact ${method.enable ? "primary" : "secondary"}`} disabled={busyID !== 0} aria-label={`${method.enable ? "禁用" : "启用"}：${method.name}`} onClick={() => void toggle(method)}>{method.enable ? "已启用" : "已禁用"}</button></td><td data-label="显示名称"><strong>{method.name}</strong></td><td data-label="支付接口"><span className="count-pill">{method.payment}</span></td><td data-label="手续费">{feeLabel(method)}</td><td data-label="通知地址"><code className="payment-notify-url">{method.notify_url}</code></td><td data-label="操作"><span className="row-actions"><button className="button secondary compact" onClick={() => setEditing(method)}>编辑</button><button className="button danger compact" disabled={busyID !== 0} onClick={() => void remove(method)}>删除</button></span></td>
    </tr>)}</tbody></table></section>}
    {editing !== undefined && <PaymentEditor api={api} definitions={definitions} payment={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => { setMethods((current) => editing === null ? [...current, saved] : current.map((item) => item.id === saved.id ? saved : item)); if (editing === null) setTotal((current) => current + 1); setEditing(undefined); }} />}
    {ordering !== null && <PaymentOrderEditor api={api} methods={ordering} onClose={() => setOrdering(null)} onSaved={(saved) => { setMethods(saved); setOrdering(null); }} />}
  </main>;
}

function PaymentEditor({ api, definitions, payment, onClose, onSaved }: {
  api: Pick<PaymentsAPI, "createPayment" | "updatePayment">;
  definitions: PaymentProviderDefinition[];
  payment: PaymentMethod | null;
  onClose: () => void;
  onSaved: (payment: PaymentMethod) => void;
}) {
  const [provider, setProvider] = useState<PaymentProvider>(payment?.payment ?? definitions[0]?.provider ?? "AlipayF2F");
  const definition = useMemo(() => definitions.find((item) => item.provider === provider), [definitions, provider]);
  const [name, setName] = useState(payment?.name ?? "");
  const [icon, setIcon] = useState(payment?.icon ?? "");
  const [notifyDomain, setNotifyDomain] = useState(payment?.notify_domain ?? "");
  const [fixedFee, setFixedFee] = useState(String(payment?.handling_fee_fixed ?? 0));
  const [percentage, setPercentage] = useState(formatBasisPoints(payment?.handling_fee_basis_points ?? 0));
  const [enabled, setEnabled] = useState(payment?.enable ?? false);
  const [config, setConfig] = useState<Record<string, string>>(payment?.config ?? {});
  const [clearFields, setClearFields] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const changeProvider = (next: PaymentProvider) => {
    setProvider(next);
    setConfig({});
    setClearFields([]);
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const input: PaymentMethodInput = {
        revision: payment?.revision, payment: provider, name: name.trim(), icon: icon.trim(), notify_domain: notifyDomain.trim(),
        handling_fee_fixed: parseNonNegativeInteger(fixedFee, "固定手续费"),
        handling_fee_basis_points: parsePercentageBasisPoints(percentage), enable: enabled, config,
        clear_config_fields: clearFields
      };
      onSaved(payment === null ? await api.createPayment(input) : await api.updatePayment(payment.id, input));
    } catch (cause) {
      setError(messageOf(cause));
      setSaving(false);
    }
  };

  return <Modal title={payment === null ? "添加支付方式" : "编辑支付方式"} onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Payment method</p><h2>{payment === null ? "添加支付方式" : "编辑支付方式"}</h2></div><button className="icon-button" aria-label="关闭支付方式编辑" onClick={onClose}>×</button></div><form className="form-stack payment-form" onSubmit={(event) => void submit(event)}>
    <label>显示名称<input aria-label="显示名称" name="name" required maxLength={255} value={name} placeholder="请输入支付名称" onChange={(event) => setName(event.target.value)} /><small>用于用户结算时显示。</small></label>
    <label>图标 URL<input aria-label="图标 URL" name="icon" type="url" value={icon} placeholder="https://example.com/icon.svg" onChange={(event) => setIcon(event.target.value)} /></label>
    <label>通知域名<input aria-label="通知域名" name="notify_domain" type="url" value={notifyDomain} placeholder="https://pay.example.com" onChange={(event) => setNotifyDomain(event.target.value)} /><small>留空时使用站点地址；仅允许 HTTPS。</small></label>
    <div className="form-grid"><label>百分比手续费（%）<input aria-label="百分比手续费（%）" name="handling_fee_percent" inputMode="decimal" required value={percentage} onChange={(event) => setPercentage(event.target.value)} /></label><label>固定手续费（分）<input aria-label="固定手续费（分）" name="handling_fee_fixed" inputMode="numeric" required value={fixedFee} onChange={(event) => setFixedFee(event.target.value)} /></label></div>
    <label>支付接口<select aria-label="支付接口" required value={provider} onChange={(event) => changeProvider(event.target.value as PaymentProvider)}>{definitions.map((item) => <option key={item.provider} value={item.provider}>{item.provider} · {item.label}</option>)}</select></label>
    <fieldset className="settings-fieldset"><legend>支付配置</legend>{definition?.fields.map((field) => <PaymentConfigInput key={field.key} field={field} value={config[field.key] ?? ""} configured={payment?.payment === provider && payment.configured_fields.includes(field.key)} clear={clearFields.includes(field.key)} onChange={(value) => setConfig((current) => ({ ...current, [field.key]: value }))} onClear={(clear) => setClearFields((current) => clear ? [...new Set([...current, field.key])] : current.filter((item) => item !== field.key))} />)}</fieldset>
    <label className="toggle-row"><input aria-label="保存后立即启用" type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>保存后立即启用</span></label>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "确认"}</button></div>
  </form></Modal>;
}

function PaymentConfigInput({ field, value, configured, clear, onChange, onClear }: {
  field: PaymentConfigField; value: string; configured: boolean; clear: boolean;
  onChange: (value: string) => void; onClear: (clear: boolean) => void;
}) {
  const required = field.required && (!field.secret || !configured || clear);
	const inputID = `payment-config-${field.key}`;
  const input = field.type === "textarea"
    ? <textarea id={inputID} aria-label={field.label} name={`config.${field.key}`} required={required} rows={4} value={value} placeholder={configured && !clear ? "已安全保存；留空保持不变" : ""} onChange={(event) => onChange(event.target.value)} />
    : <input id={inputID} aria-label={field.label} name={`config.${field.key}`} type={field.secret ? "password" : field.type} required={required} value={value} autoComplete="off" placeholder={configured && !clear ? "已安全保存；留空保持不变" : ""} onChange={(event) => onChange(event.target.value)} />;
  return <div className="payment-config-field"><label htmlFor={inputID}>{field.label}</label>{input}{field.description !== undefined && <small>{field.description}</small>}{field.secret && configured && !field.required && <label className="secret-clear"><input aria-label="清除已保存密钥" type="checkbox" checked={clear} onChange={(event) => onClear(event.target.checked)} />清除已保存密钥</label>}</div>;
}

function PaymentOrderEditor({ api, methods, onClose, onSaved }: { api: Pick<PaymentsAPI, "reorderPayments">; methods: PaymentMethod[]; onClose: () => void; onSaved: (methods: PaymentMethod[]) => void }) {
  const [ordered, setOrdered] = useState(methods);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const move = (index: number, offset: number) => setOrdered((current) => { const next = [...current]; const target = index + offset; [next[index], next[target]] = [next[target]!, next[index]!]; return next; });
  const save = async () => { setSaving(true); setError(""); try { await api.reorderPayments(ordered.map((item) => item.id)); onSaved(ordered.map((item, index) => ({ ...item, sort: index + 1 }))); } catch (cause) { setError(messageOf(cause)); setSaving(false); } };
  return <Modal title="调整支付方式排序" onClose={onClose}><div className="modal-header"><h2>调整支付方式排序</h2><button className="icon-button" aria-label="关闭支付排序" onClick={onClose}>×</button></div><ol className="notice-order-list">{ordered.map((method, index) => <li key={method.id}><span><strong>{method.name}</strong><small className="muted">{method.payment}</small></span><span className="row-actions"><button className="button ghost compact" aria-label={`上移：${method.name}`} disabled={index === 0} onClick={() => move(index, -1)}>↑</button><button className="button ghost compact" aria-label={`下移：${method.name}`} disabled={index === ordered.length - 1} onClick={() => move(index, 1)}>↓</button></span></li>)}</ol>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary" disabled={saving} onClick={() => void save()}>{saving ? "正在保存…" : "保存排序"}</button></div></Modal>;
}

function parseNonNegativeInteger(value: string, label: string): number {
  if (!/^(0|[1-9]\d*)$/.test(value)) throw new Error(`${label}必须是非负整数`);
  const result = Number(value);
  if (!Number.isSafeInteger(result)) throw new Error(`${label}超出范围`);
  return result;
}

function parsePercentageBasisPoints(value: string): number {
  const match = /^(\d{1,3})(?:\.(\d{1,2}))?$/.exec(value);
  if (match === null || match[1] === undefined) throw new Error("百分比手续费格式无效");
  const result = Number(match[1]) * 100 + Number((match[2] ?? "").padEnd(2, "0"));
  if (result > 10_000) throw new Error("百分比手续费必须在 0–100% 之间");
  return result;
}

function formatBasisPoints(value: number): string { return (value / 100).toFixed(2).replace(/\.00$/, "").replace(/(\.\d)0$/, "$1"); }
function feeLabel(method: PaymentMethod): string { const parts = []; if (method.handling_fee_basis_points > 0) parts.push(`${formatBasisPoints(method.handling_fee_basis_points)}%`); if (method.handling_fee_fixed > 0) parts.push(`${method.handling_fee_fixed} 分`); return parts.length === 0 ? "无" : parts.join(" + "); }
function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "支付配置请求失败"; }
