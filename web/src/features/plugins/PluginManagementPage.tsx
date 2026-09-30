import { translateAdmin } from "../../lib/adminLocale";
import { useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, TrustedPlugin, TrustedPluginConfig } from "../../lib/api";

type PluginAPI = Pick<AdminAPI, "listTrustedPlugins" | "updateTrustedPlugin">;
type PluginDestination = "payments" | "telegram";

const telegramTextFields = [
  ["start_welcome_title", "欢迎标题"],
  ["start_bot_description", "机器人说明"],
  ["start_bind_guide", "绑定引导"],
  ["start_unbind_guide", "解绑引导"],
  ["start_bind_commands", "绑定命令提示"],
  ["start_footer", "页脚提示"],
  ["help_text", "帮助文案"]
] as const;

export function PluginManagementPage({ api, onNavigate }: { api: PluginAPI; onNavigate: (destination: PluginDestination) => void }) {
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState<"feature" | "payment" | "all">("feature");
  const [status, setStatus] = useState("");
  const [plugins, setPlugins] = useState<TrustedPlugin[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyCode, setBusyCode] = useState("");
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<TrustedPlugin | null>(null);

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      setPlugins(await api.listTrustedPlugins());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.listTrustedPlugins().then((result) => {
      if (active) setPlugins(result);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const replace = (updated: TrustedPlugin) => setPlugins((current) => current.map((plugin) => plugin.code === updated.code ? updated : plugin));
  const toggle = async (plugin: TrustedPlugin) => {
    const action = plugin.enabled ? "禁用" : "启用";
    if (!window.confirm(`确认${action}插件“${plugin.name}”？${plugin.enabled ? "相关新业务入口会立即停止，但历史数据仍会保留。" : "相关业务入口会立即恢复。"}`)) return;
    setBusyCode(plugin.code);
    setError("");
    try {
      replace(await api.updateTrustedPlugin(plugin.code, { revision: plugin.revision, enabled: !plugin.enabled, config: plugin.config }));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusyCode("");
    }
  };

  const visiblePlugins = plugins.filter(plugin => (category === "all" || plugin.type === category) && (status === "" || plugin.enabled === (status === "enabled")) && `${plugin.name} ${plugin.code}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));

  return <main className="page-shell resource-page plugin-page">
    <header className="page-header"><div><h1>{translateAdmin("插件管理")}</h1><p className="muted">管理内置插件及其业务配置。</p></div></header>
    <div className="resource-toolbar"><input type="search" aria-label="搜索插件" placeholder={translateAdmin("搜索插件名称或描述...")} value={search} onChange={event => setSearch(event.target.value)} /><select aria-label="插件状态" value={status} onChange={event => setStatus(event.target.value)}><option value="">{translateAdmin("全部状态")}</option><option value="enabled">{translateAdmin("已启用")}</option><option value="disabled">{translateAdmin("已禁用")}</option></select></div>
    <div role="tablist" aria-label="插件分类" className="plugin-category-tabs">{([['feature', '功能'], ['payment', '支付方式'], ['all', '所有插件']] as const).map(([value, label]) => <button type="button" role="tab" aria-selected={category === value} key={value} onClick={() => setCategory(value)}>{label}</button>)}</div>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load()}>{translateAdmin("重试")}</button></div>}
    {loading && plugins.length === 0 ? <div className="empty-card">正在加载可信插件…</div> : <section className="plugin-cards" role="tabpanel" aria-label={category === "feature" ? "功能" : category === "payment" ? "支付方式" : translateAdmin("所有插件")}>
      {visiblePlugins.length === 0 && <div className="empty-card">暂无匹配插件</div>}
      {visiblePlugins.map(plugin => <article className="plugin-card" key={plugin.code}>
        <h3>{plugin.name}</h3><div className="row-actions"><span className="count-pill">{plugin.type === "payment" ? "支付方式" : "功能"}</span><span className="count-pill">{plugin.enabled ? translateAdmin("已启用") : translateAdmin("已禁用")}</span></div>
        <p className="muted"><code>{plugin.code}</code> · v{plugin.version}</p>
        <div className="row-actions">{plugin.code === "telegram" ? <><button className="button secondary compact" aria-label={`插件配置：${plugin.name}`} onClick={() => setEditing(plugin)}>{translateAdmin("配置")}</button><button className="button ghost compact" aria-label={`业务设置：${plugin.name}`} onClick={() => onNavigate("telegram")}>机器人设置</button></> : <button className="button secondary compact" aria-label={`支付配置：${plugin.name}`} onClick={() => onNavigate("payments")}>{translateAdmin("配置")}</button>}
          <button className={`button compact ${plugin.enabled ? "danger" : "primary"}`} aria-label={`${plugin.enabled ? translateAdmin("禁用") : translateAdmin("启用")}：${plugin.name}`} disabled={busyCode !== ""} onClick={() => void toggle(plugin)}>{busyCode === plugin.code ? "正在保存…" : plugin.enabled ? translateAdmin("禁用") : translateAdmin("启用")}</button>
        </div>
      </article>)}
    </section>}
    {editing !== null && <TelegramPluginEditor api={api} plugin={editing} onClose={() => setEditing(null)} onSaved={(updated) => { replace(updated); setEditing(null); }} />}
  </main>;
}

function TelegramPluginEditor({ api, plugin, onClose, onSaved }: { api: PluginAPI; plugin: TrustedPlugin; onClose: () => void; onSaved: (plugin: TrustedPlugin) => void }) {
  const [config, setConfig] = useState<TrustedPluginConfig>(plugin.config);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      onSaved(await api.updateTrustedPlugin(plugin.code, { revision: plugin.revision, enabled: plugin.enabled, config }));
    } catch (cause) {
      setError(messageOf(cause));
      setSaving(false);
    }
  };
  return <Modal title="Telegram 插件配置" onClose={onClose}><div className="modal-header"><div><h2>Telegram 插件配置</h2></div><button className="icon-button" aria-label="关闭 Telegram 插件配置" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}>
    <fieldset className="settings-fieldset"><legend>业务通知</legend><label className="toggle-row"><input aria-label="工单通知" type="checkbox" checked={config.enable_ticket_notify === true} onChange={(event) => setConfig((current) => ({ ...current, enable_ticket_notify: event.target.checked }))} /><span>向管理员发送工单通知</span></label><label className="toggle-row"><input aria-label="支付通知" type="checkbox" checked={config.enable_payment_notify === true} onChange={(event) => setConfig((current) => ({ ...current, enable_payment_notify: event.target.checked }))} /><span>向管理员发送支付通知</span></label></fieldset>
    <fieldset className="settings-fieldset"><legend>命令与引导文案</legend>{telegramTextFields.map(([key, label]) => <label key={key}>{label}<textarea aria-label={label} required maxLength={4096} rows={key === "help_text" ? 5 : 3} value={typeof config[key] === "string" ? config[key] : ""} onChange={(event) => setConfig((current) => ({ ...current, [key]: event.target.value }))} /></label>)}</fieldset>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存插件配置"}</button></div>
  </form></Modal>;
}

function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "插件管理请求失败"; }
