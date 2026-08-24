import { useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { SMTPEncryption, TicketSettings, TicketSettingsInput } from "../../lib/api";

interface TicketSettingsAPI {
  getTicketSettings: () => Promise<TicketSettings>;
  updateTicketSettings: (input: TicketSettingsInput) => Promise<TicketSettings>;
}

export function TicketSettingsDialog({ api, onClose }: { api: TicketSettingsAPI; onClose: () => void }) {
  const [draft, setDraft] = useState<TicketSettingsInput | null>(null);
  const [passwordSet, setPasswordSet] = useState(false);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  useEffect(() => {
    let active = true;
    void api.getTicketSettings().then((value) => {
      if (!active) return;
      setDraft(toInput(value));
      setPasswordSet(value.smtp_password_set);
    }).catch((cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const update = <Key extends keyof TicketSettingsInput>(key: Key, value: TicketSettingsInput[Key]) => {
    setDraft((current) => current === null ? null : { ...current, [key]: value });
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (draft === null) return;
    setSaving(true);
    setError("");
    setSuccess("");
    const input: TicketSettingsInput = { ...draft };
    if (password !== "") input.smtp_password = password;
    else if (clearPassword) input.smtp_password = "";
    try {
      const saved = await api.updateTicketSettings(input);
      setDraft(toInput(saved));
      setPasswordSet(saved.smtp_password_set);
      setPassword("");
      setClearPassword(false);
      setSuccess("工单设置已保存。");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return <Modal title="工单设置" onClose={onClose}>
    <div className="modal-header"><div><h2>工单设置</h2><p className="muted small">配置回复规则与管理员回复邮件。</p></div><button className="icon-button" aria-label="关闭工单设置" onClick={onClose}>×</button></div>
    {loading ? <div className="ticket-loading">正在加载设置…</div> : draft === null ? <div className="alert error" role="alert">{error || "无法加载工单设置"}</div> : <form className="form-stack ticket-settings-form" onSubmit={(event) => void save(event)}>
      <fieldset className="settings-fieldset"><legend>工单规则</legend>
        <label>站点名称<input required maxLength={100} value={draft.app_name} onChange={(event) => update("app_name", event.target.value)} /></label>
        <label>站点地址<input type="url" maxLength={2048} placeholder="https://panel.example.com" value={draft.app_url} onChange={(event) => update("app_url", event.target.value)} /></label>
        <label className="switch-label"><input type="checkbox" checked={draft.ticket_must_wait_reply} onChange={(event) => update("ticket_must_wait_reply", event.target.checked)} />用户必须等待管理员回复</label>
        <p className="muted small">启用后，同一用户不能连续发送工单消息；管理员回复后可继续发送。</p>
      </fieldset>
      <fieldset className="settings-fieldset"><legend>回复邮件</legend>
        <label className="switch-label"><input type="checkbox" checked={draft.smtp_enabled} onChange={(event) => update("smtp_enabled", event.target.checked)} />启用工单回复邮件</label>
        <div className="ticket-settings-grid">
          <label>SMTP 主机<input required={draft.smtp_enabled} maxLength={253} value={draft.smtp_host} onChange={(event) => update("smtp_host", event.target.value)} /></label>
          <label>SMTP 端口<input required={draft.smtp_enabled} type="number" min={1} max={65535} value={draft.smtp_port} onChange={(event) => update("smtp_port", Number(event.target.value))} /></label>
          <label>传输加密<select value={draft.smtp_encryption} onChange={(event) => update("smtp_encryption", event.target.value as SMTPEncryption)}><option value="starttls">STARTTLS</option><option value="tls">TLS</option><option value="none">无（仅本地测试）</option></select></label>
          <label>发件地址<input required={draft.smtp_enabled} type="email" maxLength={320} placeholder="support@example.com" value={draft.smtp_from_address} onChange={(event) => update("smtp_from_address", event.target.value)} /></label>
        </div>
        <label>SMTP 用户名<input maxLength={320} autoComplete="username" value={draft.smtp_username} onChange={(event) => update("smtp_username", event.target.value)} /></label>
        <label>SMTP 密码<input type="password" maxLength={4096} autoComplete="new-password" placeholder={passwordSet && !clearPassword ? "已安全保存；留空则保持不变" : "留空表示不设置密码"} value={password} onChange={(event) => { setPassword(event.target.value); if (event.target.value !== "") setClearPassword(false); }} /></label>
        {passwordSet && <button className="button ghost compact settings-clear-secret" type="button" disabled={clearPassword} onClick={() => { setPassword(""); setClearPassword(true); }}>{clearPassword ? "保存后将清除密码" : "清除已保存密码"}</button>}
        <p className="muted small">密码仅以加密密文保存，读取设置时不会返回或再次展示。</p>
      </fieldset>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      {success !== "" && <div className="alert success" role="status">{success}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存设置"}</button></div>
    </form>}
  </Modal>;
}

function toInput(settings: TicketSettings): TicketSettingsInput {
  return {
    revision: settings.revision, app_name: settings.app_name, app_url: settings.app_url,
    ticket_must_wait_reply: settings.ticket_must_wait_reply, smtp_enabled: settings.smtp_enabled,
    smtp_host: settings.smtp_host, smtp_port: settings.smtp_port, smtp_username: settings.smtp_username,
    smtp_encryption: settings.smtp_encryption, smtp_from_address: settings.smtp_from_address
  };
}

function errorMessage(cause: unknown) {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
