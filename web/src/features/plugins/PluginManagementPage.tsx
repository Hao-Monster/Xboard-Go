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

  return <main className="page-shell resource-page plugin-page">
    <header className="page-header"><div><p className="eyebrow">Trusted core extensions</p><h1>插件管理</h1><p className="muted">仅管理随 Xboard-Go 编译、经过审查的 7 个内置能力；不执行上传的 PHP、ZIP 或任意第三方代码。</p></div></header>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void load()}>重试</button></div>}
    {loading && plugins.length === 0 ? <div className="empty-card">正在加载可信插件…</div> : <section className="resource-table-wrap" aria-label="可信插件列表"><table className="resource-table"><thead><tr><th>插件</th><th>类型</th><th>版本</th><th>状态</th><th>配置</th><th>操作</th></tr></thead><tbody>{plugins.map((plugin) => <tr key={plugin.code}>
      <td data-label="插件"><strong>{plugin.name}</strong><small className="muted">{plugin.code}</small></td>
      <td data-label="类型">{plugin.type === "payment" ? "支付" : "功能"}</td>
      <td data-label="版本"><code>{plugin.version}</code></td>
      <td data-label="状态"><span className="count-pill">{plugin.enabled ? "已启用" : "已禁用"}</span></td>
      <td data-label="配置"><span className="row-actions">{plugin.code === "telegram" ? <><button className="button secondary compact" aria-label={`插件配置：${plugin.name}`} onClick={() => setEditing(plugin)}>插件文案</button><button className="button ghost compact" aria-label={`业务设置：${plugin.name}`} onClick={() => onNavigate("telegram")}>机器人设置</button></> : <button className="button secondary compact" aria-label={`支付配置：${plugin.name}`} onClick={() => onNavigate("payments")}>支付配置</button>}</span></td>
      <td data-label="操作"><button className={`button compact ${plugin.enabled ? "danger" : "primary"}`} aria-label={`${plugin.enabled ? "禁用" : "启用"}：${plugin.name}`} disabled={busyCode !== ""} onClick={() => void toggle(plugin)}>{busyCode === plugin.code ? "正在保存…" : plugin.enabled ? "禁用" : "启用"}</button></td>
    </tr>)}</tbody></table></section>}
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
  return <Modal title="Telegram 插件配置" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">Telegram Bot 1.0.1</p><h2>Telegram 插件配置</h2></div><button className="icon-button" aria-label="关闭 Telegram 插件配置" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}>
    <fieldset className="settings-fieldset"><legend>业务通知</legend><label className="toggle-row"><input aria-label="工单通知" type="checkbox" checked={config.enable_ticket_notify === true} onChange={(event) => setConfig((current) => ({ ...current, enable_ticket_notify: event.target.checked }))} /><span>向管理员发送工单通知</span></label><label className="toggle-row"><input aria-label="支付通知" type="checkbox" checked={config.enable_payment_notify === true} onChange={(event) => setConfig((current) => ({ ...current, enable_payment_notify: event.target.checked }))} /><span>向管理员发送支付通知</span></label></fieldset>
    <fieldset className="settings-fieldset"><legend>命令与引导文案</legend>{telegramTextFields.map(([key, label]) => <label key={key}>{label}<textarea aria-label={label} required maxLength={4096} rows={key === "help_text" ? 5 : 3} value={typeof config[key] === "string" ? config[key] : ""} onChange={(event) => setConfig((current) => ({ ...current, [key]: event.target.value }))} /></label>)}</fieldset>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存插件配置"}</button></div>
  </form></Modal>;
}

function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "插件管理请求失败"; }
