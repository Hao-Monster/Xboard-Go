import { useEffect, useState, type FormEvent } from "react";

import type {
  SubscriptionPolicySettings,
  SubscriptionPolicySettingsInput,
  SubscriptionSettings,
  SubscriptionSettingsInput,
  SubscriptionTemplateName
} from "../../lib/api";

interface SubscriptionSettingsAPI {
  getSubscriptionSettings: () => Promise<SubscriptionSettings>;
  updateSubscriptionSettings: (input: SubscriptionSettingsInput) => Promise<SubscriptionSettings>;
  getSubscriptionPolicySettings: () => Promise<SubscriptionPolicySettings>;
  updateSubscriptionPolicySettings: (input: SubscriptionPolicySettingsInput) => Promise<SubscriptionPolicySettings>;
}
type SubscriptionDraft = Omit<SubscriptionSettingsInput, "revision">;
type SubscriptionPolicyDraft = Omit<SubscriptionPolicySettingsInput, "revision">;

const templateLabels: Record<SubscriptionTemplateName, string> = {
  singbox: "Sing-box",
  clash: "Clash",
  clashmeta: "Clash Meta",
  stash: "Stash",
  surge: "Surge",
  surfboard: "Surfboard"
};
const templateNames = Object.keys(templateLabels) as SubscriptionTemplateName[];

const resetOptions = [
  [0, "每月 1 日"], [1, "按月重置"], [2, "不重置"], [3, "每年 1 月 1 日"], [4, "按年重置"]
] as const;
const eventOptions = [[0, "不执行任何动作"], [1, "重置用户流量"]] as const;

export function SubscriptionSettingsPage({ api }: { api: SubscriptionSettingsAPI }) {
  const [current, setCurrent] = useState<SubscriptionSettings | null>(null);
  const [draft, setDraft] = useState<SubscriptionDraft | null>(null);
  const [policyCurrent, setPolicyCurrent] = useState<SubscriptionPolicySettings | null>(null);
  const [policyDraft, setPolicyDraft] = useState<SubscriptionPolicyDraft | null>(null);
  const [activeTemplate, setActiveTemplate] = useState<SubscriptionTemplateName>("singbox");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [policySaving, setPolicySaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [policySaved, setPolicySaved] = useState(false);

  const apply = (settings: SubscriptionSettings) => {
    setCurrent(settings);
    setDraft({ path: settings.path, show_info: settings.show_info, show_protocol: settings.show_protocol, templates: { ...settings.templates } });
  };

  const applyPolicy = (settings: SubscriptionPolicySettings) => {
    setPolicyCurrent(settings);
    setPolicyDraft({
      plan_change_enable: settings.plan_change_enable,
      reset_traffic_method: settings.reset_traffic_method,
      surplus_enable: settings.surplus_enable,
      new_order_event_id: settings.new_order_event_id,
      renew_order_event_id: settings.renew_order_event_id,
      change_order_event_id: settings.change_order_event_id,
      default_remind_expire: settings.default_remind_expire,
      default_remind_traffic: settings.default_remind_traffic
    });
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    setPolicySaved(false);
    try {
      const [settings, policy] = await Promise.all([api.getSubscriptionSettings(), api.getSubscriptionPolicySettings()]);
      apply(settings);
      applyPolicy(policy);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void Promise.all([api.getSubscriptionSettings(), api.getSubscriptionPolicySettings()])
      .then(([settings, policy]) => {
        if (!live) return;
        apply(settings);
        applyPolicy(policy);
      })
      .catch((cause: unknown) => { if (live) setError(message(cause)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const update = <K extends keyof SubscriptionDraft,>(name: K, value: SubscriptionDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [name]: value });
    setSaved(false);
  };

  const updatePolicy = <K extends keyof SubscriptionPolicyDraft,>(name: K, value: SubscriptionPolicyDraft[K]) => {
    if (policyDraft === null) return;
    setPolicyDraft({ ...policyDraft, [name]: value });
    setPolicySaved(false);
  };

  const updateTemplate = (content: string) => {
    if (draft === null) return;
    setDraft({ ...draft, templates: { ...draft.templates, [activeTemplate]: content } });
    setSaved(false);
  };

  const savePolicy = async (event: FormEvent) => {
    event.preventDefault();
    if (policyCurrent === null || policyDraft === null) return;
    setPolicySaving(true);
    setError("");
    setPolicySaved(false);
    try {
      applyPolicy(await api.updateSubscriptionPolicySettings({ revision: policyCurrent.revision, ...policyDraft }));
      setPolicySaved(true);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setPolicySaving(false);
    }
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      apply(await api.updateSubscriptionSettings({ revision: current.revision, ...draft }));
      setSaved(true);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSaving(false);
    }
  };

  const absent = draft === null || current === null || policyDraft === null || policyCurrent === null;
  return <main className="page-shell subscription-settings-page">
    <header className="page-header"><div><p className="eyebrow">Subscription</p><h1>订阅设置</h1><p className="muted">配置套餐变更策略、订单事件、订阅输出和六类客户端模板。</p></div></header>
    {loading && absent && <div className="empty-card">正在加载订阅设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {absent && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载订阅设置</button>}
    {policyDraft !== null && policyCurrent !== null && <form className="subscription-settings-layout" onSubmit={(event) => void savePolicy(event)}>
      <section className="site-settings-card subscription-policy" aria-labelledby="subscription-policy-heading">
        <div className="section-heading"><div><h2 id="subscription-policy-heading">订阅策略</h2><p className="muted">订单开关和事件在保存后立即用于后续订单。</p></div><span className="count-pill">策略 Revision {policyCurrent.revision}</span></div>
        <div className="form-stack">
          <label className="switch-label"><input type="checkbox" checked={policyDraft.plan_change_enable} onChange={(event) => updatePolicy("plan_change_enable", event.target.checked)} />允许用户更改订阅</label>
          <p className="small muted">关闭后，已有套餐的用户不能跨套餐变更。</p>
          <label>月流量重置方式<select aria-label="月流量重置方式" value={policyDraft.reset_traffic_method} onChange={(event) => updatePolicy("reset_traffic_method", Number(event.target.value))}>{resetOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
          <p className="small muted">套餐未单独设置时使用此全局重置方式。</p>
          <label className="switch-label"><input type="checkbox" checked={policyDraft.surplus_enable} onChange={(event) => updatePolicy("surplus_enable", event.target.checked)} />开启折抵方案</label>
          <p className="small muted">变更套餐时按旧版口径折抵剩余价值。</p>
          <label>当订阅新购时触发事件<select aria-label="当订阅新购时触发事件" value={policyDraft.new_order_event_id} onChange={(event) => updatePolicy("new_order_event_id", Number(event.target.value))}>{eventOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
          <label>当订阅续费时触发事件<select aria-label="当订阅续费时触发事件" value={policyDraft.renew_order_event_id} onChange={(event) => updatePolicy("renew_order_event_id", Number(event.target.value))}>{eventOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
          <label>当订阅变更时触发事件<select aria-label="当订阅变更时触发事件" value={policyDraft.change_order_event_id} onChange={(event) => updatePolicy("change_order_event_id", Number(event.target.value))}>{eventOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        </div>
      </section>
      {policySaved && <div className="alert success" role="status">订阅策略已保存</div>}
      <div className="form-actions"><button className="button primary" type="submit" disabled={policySaving}>{policySaving ? "正在保存…" : "保存订阅策略"}</button></div>
    </form>}
    {draft !== null && current !== null && <form className="subscription-settings-layout" onSubmit={(event) => void save(event)}>
      <section className="site-settings-card subscription-options" aria-labelledby="subscription-options-heading">
        <div className="section-heading"><div><h2 id="subscription-options-heading">订阅输出</h2><p className="muted">修改路径后，旧路径下的所有订阅地址立即失效。</p></div><span className="count-pill">Revision {current.revision}</span></div>
        <div className="form-stack">
          <label>订阅路径<input required name="path" pattern="[A-Za-z0-9_-]{1,64}" maxLength={64} placeholder="s" value={draft.path} onChange={(event) => update("path", event.target.value)} /></label>
          <p className="small muted">当前格式：/{draft.path || "{path}"}/xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx；仅允许字母、数字、下划线和连字符。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.show_info} onChange={(event) => update("show_info", event.target.checked)} />在订阅中展示订阅信息</label>
          <label className="switch-label"><input type="checkbox" checked={draft.show_protocol} onChange={(event) => update("show_protocol", event.target.checked)} />在线路名称中显示协议名称</label>
        </div>
      </section>
      <section className="site-settings-card subscription-template-editor" aria-labelledby="subscription-template-heading">
        <div className="section-heading"><div><h2 id="subscription-template-heading">订阅模板</h2><p className="muted">六个模板与路径、开关在同一事务中保存。</p></div></div>
        <nav className="subscription-template-tabs" aria-label="订阅模板类型">
          {templateNames.map((name) => <button type="button" className={activeTemplate === name ? "active" : ""} aria-pressed={activeTemplate === name} key={name} onClick={() => setActiveTemplate(name)}>{templateLabels[name]}</button>)}
        </nav>
        <label className="subscription-template-field">{templateLabels[activeTemplate]} 订阅模板
          <textarea className="monospace" aria-label={`${templateLabels[activeTemplate]} 订阅模板`} spellCheck={false} value={draft.templates[activeTemplate]} onChange={(event) => updateTemplate(event.target.value)} />
        </label>
        <p className="small muted">{new TextEncoder().encode(draft.templates[activeTemplate]).length.toLocaleString()} / 1,048,576 bytes；JSON/YAML 模板保存时由服务端校验结构。</p>
      </section>
      {saved && <div className="alert success" role="status">订阅设置已保存</div>}
      <div className="form-actions"><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存订阅设置"}</button></div>
    </form>}
    {error !== "" && !absent && <div className="form-actions"><button className="button secondary" type="button" disabled={saving || policySaving} onClick={() => void load()}>刷新最新设置</button></div>}
  </main>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "订阅设置请求失败";
}
