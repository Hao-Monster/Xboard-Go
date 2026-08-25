import { useEffect, useState, type FormEvent } from "react";

import type { SubscriptionSettings, SubscriptionSettingsInput, SubscriptionTemplateName } from "../../lib/api";

interface SubscriptionSettingsAPI {
  getSubscriptionSettings: () => Promise<SubscriptionSettings>;
  updateSubscriptionSettings: (input: SubscriptionSettingsInput) => Promise<SubscriptionSettings>;
}
type SubscriptionDraft = Omit<SubscriptionSettingsInput, "revision">;

const templateLabels: Record<SubscriptionTemplateName, string> = {
  singbox: "Sing-box",
  clash: "Clash",
  clashmeta: "Clash Meta",
  stash: "Stash",
  surge: "Surge",
  surfboard: "Surfboard"
};
const templateNames = Object.keys(templateLabels) as SubscriptionTemplateName[];

export function SubscriptionSettingsPage({ api }: { api: SubscriptionSettingsAPI }) {
  const [current, setCurrent] = useState<SubscriptionSettings | null>(null);
  const [draft, setDraft] = useState<SubscriptionDraft | null>(null);
  const [activeTemplate, setActiveTemplate] = useState<SubscriptionTemplateName>("singbox");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const apply = (settings: SubscriptionSettings) => {
    setCurrent(settings);
    setDraft({
      path: settings.path,
      show_info: settings.show_info,
      show_protocol: settings.show_protocol,
      templates: { ...settings.templates }
    });
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      apply(await api.getSubscriptionSettings());
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void api.getSubscriptionSettings().then((settings) => { if (live) apply(settings); })
      .catch((cause: unknown) => { if (live) setError(message(cause)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const update = <K extends keyof SubscriptionDraft,>(name: K, value: SubscriptionDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [name]: value });
    setSaved(false);
  };

  const updateTemplate = (content: string) => {
    if (draft === null) return;
    setDraft({ ...draft, templates: { ...draft.templates, [activeTemplate]: content } });
    setSaved(false);
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

  return <main className="page-shell subscription-settings-page">
    <header className="page-header"><div><p className="eyebrow">Subscription</p><h1>订阅设置</h1><p className="muted">配置订阅路径、线路展示和六类客户端模板。</p></div></header>
    {loading && draft === null && <div className="empty-card">正在加载订阅设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {draft === null && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载订阅设置</button>}
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
      <div className="form-actions">
        {error !== "" && <button className="button secondary" type="button" disabled={saving} onClick={() => void load()}>刷新最新设置</button>}
        <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存订阅设置"}</button>
      </div>
    </form>}
  </main>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "订阅设置请求失败";
}
