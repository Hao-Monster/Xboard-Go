import { useEffect, useState, type FormEvent } from "react";

import type { MailSettings, MailSettingsInput, MailSettingsTestResult, SMTPEncryption } from "../../lib/api";

interface MailSettingsAPI {
  getMailSettings: () => Promise<MailSettings>;
  updateMailSettings: (input: MailSettingsInput) => Promise<MailSettings>;
  testMailSettings: () => Promise<MailSettingsTestResult>;
}

interface MailSettingsDraft {
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_password: string;
  clear_password: boolean;
  smtp_encryption: SMTPEncryption;
  smtp_from_address: string;
  remind_mail_enable: boolean;
}

export function MailSettingsPage({ api }: { api: MailSettingsAPI }) {
  const [current, setCurrent] = useState<MailSettings | null>(null);
  const [draft, setDraft] = useState<MailSettingsDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  const apply = (settings: MailSettings) => {
    setCurrent(settings);
    setDraft({
      smtp_enabled: settings.smtp_enabled,
      smtp_host: settings.smtp_host,
      smtp_port: settings.smtp_port,
      smtp_username: settings.smtp_username,
      smtp_password: "",
      clear_password: false,
      smtp_encryption: settings.smtp_encryption,
      smtp_from_address: settings.smtp_from_address,
      remind_mail_enable: settings.remind_mail_enable
    });
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      apply(await api.getMailSettings());
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void api.getMailSettings()
      .then((settings) => { if (live) apply(settings); })
      .catch((cause: unknown) => { if (live) setError(message(cause)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const update = <K extends keyof MailSettingsDraft,>(key: K, value: MailSettingsDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [key]: value });
    setError("");
    setSuccess("");
  };

  const setSMTPEnabled = (enabled: boolean) => {
    if (draft === null) return;
    setDraft({ ...draft, smtp_enabled: enabled, remind_mail_enable: enabled && draft.remind_mail_enable });
    setError("");
    setSuccess("");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    const input: MailSettingsInput = {
      revision: current.revision,
      smtp_enabled: draft.smtp_enabled,
      smtp_host: draft.smtp_host,
      smtp_port: draft.smtp_port,
      smtp_username: draft.smtp_username,
      smtp_encryption: draft.smtp_encryption,
      smtp_from_address: draft.smtp_from_address,
      remind_mail_enable: draft.remind_mail_enable
    };
    if (draft.smtp_password !== "") input.smtp_password = draft.smtp_password;
    else if (draft.clear_password) input.smtp_password = "";
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      apply(await api.updateMailSettings(input));
      setSuccess("邮件设置已保存");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSaving(false);
    }
  };

  const sendTest = async () => {
    setTesting(true);
    setError("");
    setSuccess("");
    try {
      const result = await api.testMailSettings();
      setSuccess(`测试邮件已发送至 ${result.recipient}`);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setTesting(false);
    }
  };

  const absent = current === null || draft === null;
  return <main className="page-shell mail-settings-page">
    <header className="page-header"><div><p className="eyebrow">Email</p><h1>邮件设置</h1><p className="muted">配置全局 SMTP、测试真实投递，并控制订阅到期和流量提醒。</p></div></header>
    {loading && absent && <div className="empty-card">正在加载邮件设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {absent && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载邮件设置</button>}
    {!absent && <form className="mail-settings-form" onSubmit={(event) => void save(event)}>
      <section className="site-settings-card" aria-labelledby="smtp-settings-heading">
        <div className="section-heading"><div><h2 id="smtp-settings-heading">SMTP 服务</h2><p className="muted">密码只写入加密存储，读取时不会返回明文或密文。</p></div><span className="count-pill">Revision {current.revision}</span></div>
        <div className="form-stack">
          <label className="switch-label"><input type="checkbox" checked={draft.smtp_enabled} onChange={(event) => setSMTPEnabled(event.target.checked)} />启用 SMTP 邮件服务</label>
          <div className="mail-settings-grid">
            <label>SMTP 主机<input required={draft.smtp_enabled} maxLength={253} value={draft.smtp_host} onChange={(event) => update("smtp_host", event.target.value)} /></label>
            <label>SMTP 端口<input required={draft.smtp_enabled} type="number" min={1} max={65535} value={draft.smtp_port} onChange={(event) => update("smtp_port", Number(event.target.value))} /></label>
            <label>SMTP 用户名<input maxLength={320} autoComplete="username" value={draft.smtp_username} onChange={(event) => update("smtp_username", event.target.value)} /></label>
            <label>SMTP 密码<input type="password" maxLength={4096} autoComplete="new-password" value={draft.smtp_password} disabled={draft.clear_password} placeholder={current.smtp_password_set ? "已安全保存；留空保持不变" : "未设置"} onChange={(event) => {
              if (draft === null) return;
              setDraft({ ...draft, smtp_password: event.target.value, clear_password: false });
              setError(""); setSuccess("");
            }} /></label>
            <label>加密方式<select aria-label="加密方式" value={draft.smtp_encryption} onChange={(event) => update("smtp_encryption", event.target.value as SMTPEncryption)}>
              <option value="none">无加密</option><option value="tls">SSL/TLS</option><option value="starttls">STARTTLS</option>
            </select></label>
            <label>发件人地址<input required={draft.smtp_enabled} type="email" maxLength={320} value={draft.smtp_from_address} onChange={(event) => update("smtp_from_address", event.target.value)} /></label>
          </div>
          {current.smtp_password_set && <label className="switch-label"><input type="checkbox" checked={draft.clear_password} onChange={(event) => {
            if (draft === null) return;
            setDraft({ ...draft, clear_password: event.target.checked, smtp_password: "" });
            setError(""); setSuccess("");
          }} />清除已保存的 SMTP 密码</label>}
          <p className="small muted">无加密 SMTP 默认被服务端拒绝，仅可在显式开启的隔离测试环境连接 Mailpit。</p>
        </div>
      </section>
      <section className="site-settings-card" aria-labelledby="mail-reminder-heading">
        <div className="section-heading"><div><h2 id="mail-reminder-heading">邮件提醒</h2><p className="muted">每天 11:30（UTC+8）为未来 24 小时内到期或流量达到 80% 的用户安排提醒。</p></div></div>
        <label className="switch-label"><input type="checkbox" disabled={!draft.smtp_enabled} checked={draft.remind_mail_enable} onChange={(event) => update("remind_mail_enable", event.target.checked)} />启用订阅到期和流量提醒</label>
      </section>
      {success !== "" && <div className="alert success" role="status">{success}</div>}
      <div className="form-actions split"><button className="button secondary" type="button" disabled={saving || testing} onClick={() => void sendTest()}>{testing ? "正在发送…" : "发送测试邮件"}</button><button className="button primary" type="submit" disabled={saving || testing}>{saving ? "正在保存…" : "保存邮件设置"}</button></div>
    </form>}
    {error !== "" && !absent && <div className="form-actions"><button className="button secondary" type="button" disabled={saving || testing} onClick={() => void load()}>刷新最新设置</button></div>}
  </main>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "邮件设置请求失败";
}
