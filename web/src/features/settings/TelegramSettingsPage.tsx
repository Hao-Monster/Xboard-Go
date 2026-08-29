import { useEffect, useState, type FormEvent } from "react";

import type { TelegramProvisionResult, TelegramSettings, TelegramSettingsInput } from "../../lib/api";

interface TelegramSettingsAPI {
  getTelegramSettings: () => Promise<TelegramSettings>;
  updateTelegramSettings: (input: TelegramSettingsInput) => Promise<TelegramSettings>;
  provisionTelegramWebhook: (revision: number) => Promise<TelegramProvisionResult>;
}

interface TelegramDraft {
  telegram_bot_enable: boolean;
  telegram_bot_token: string;
  clear_telegram_bot_token: boolean;
  telegram_webhook_url: string;
  telegram_discuss_link: string;
}

export function TelegramSettingsPage({ api }: { api: TelegramSettingsAPI }) {
  const [current, setCurrent] = useState<TelegramSettings | null>(null);
  const [draft, setDraft] = useState<TelegramDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [provisioning, setProvisioning] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  const apply = (settings: TelegramSettings) => {
    setCurrent(settings);
    setDraft({
      telegram_bot_enable: settings.telegram_bot_enable,
      telegram_bot_token: "",
      clear_telegram_bot_token: false,
      telegram_webhook_url: settings.telegram_webhook_url,
      telegram_discuss_link: settings.telegram_discuss_link
    });
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      apply(await api.getTelegramSettings());
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void api.getTelegramSettings()
      .then((settings) => { if (live) apply(settings); })
      .catch((cause: unknown) => { if (live) setError(message(cause)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const update = <K extends keyof TelegramDraft,>(key: K, value: TelegramDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [key]: value });
    setError("");
    setSuccess("");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    const input: TelegramSettingsInput = {
      revision: current.revision,
      telegram_bot_enable: draft.clear_telegram_bot_token ? false : draft.telegram_bot_enable,
      telegram_webhook_url: draft.telegram_webhook_url,
      telegram_discuss_link: draft.telegram_discuss_link
    };
    if (draft.clear_telegram_bot_token) input.clear_telegram_bot_token = true;
    else if (draft.telegram_bot_token !== "") input.telegram_bot_token = draft.telegram_bot_token;
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      apply(await api.updateTelegramSettings(input));
      setSuccess("Telegram 设置已保存");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSaving(false);
    }
  };

  const provision = async () => {
    if (current === null) return;
    setProvisioning(true);
    setError("");
    setSuccess("");
    try {
      const result = await api.provisionTelegramWebhook(current.revision);
      apply(result.settings);
      setSuccess(`Webhook 已设置：${result.webhook_url}`);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setProvisioning(false);
    }
  };

  const absent = current === null || draft === null;
  const tokenAvailable = !absent && !draft.clear_telegram_bot_token && current.telegram_bot_token_set;
  const settingsSaved = current !== null && draft !== null && draft.telegram_bot_enable === current.telegram_bot_enable &&
    draft.telegram_bot_token === "" && !draft.clear_telegram_bot_token &&
    draft.telegram_webhook_url === current.telegram_webhook_url && draft.telegram_discuss_link === current.telegram_discuss_link;

  return <main className="page-shell telegram-settings-page">
    <header className="page-header"><div><p className="eyebrow">Telegram</p><h1>Telegram 设置</h1><p className="muted">配置机器人绑定入口、群组链接和经过官方 Secret Token 验证的 Webhook。</p></div></header>
    {loading && absent && <div className="empty-card">正在加载 Telegram 设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {absent && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载 Telegram 设置</button>}
    {!absent && <form className="mail-settings-form" onSubmit={(event) => void save(event)}>
      <section className="site-settings-card" aria-labelledby="telegram-bot-heading">
        <div className="section-heading"><div><h2 id="telegram-bot-heading">Bot 与绑定引导</h2><p className="muted">令牌只写入加密存储，管理接口和页面均不会读取明文或密文。</p></div><span className="count-pill">Revision {current.revision}</span></div>
        <div className="form-stack">
          <label className="switch-label"><input type="checkbox" checked={draft.telegram_bot_enable} disabled={draft.clear_telegram_bot_token} onChange={(event) => update("telegram_bot_enable", event.target.checked)} />启用 Telegram 绑定引导</label>
          <div className="mail-settings-grid">
            <label>机器人令牌<input type="password" maxLength={160} autoComplete="new-password" value={draft.telegram_bot_token} disabled={draft.clear_telegram_bot_token} placeholder={current.telegram_bot_token_set ? "已安全保存" : "123456789:BotToken"} onChange={(event) => update("telegram_bot_token", event.target.value)} /></label>
            <label>Webhook Base URL<input type="url" maxLength={2048} placeholder="https://panel.example.com" value={draft.telegram_webhook_url} onChange={(event) => update("telegram_webhook_url", event.target.value)} /></label>
            <label>群组链接<input type="url" maxLength={2048} placeholder="https://t.me/example_group" value={draft.telegram_discuss_link} onChange={(event) => update("telegram_discuss_link", event.target.value)} /></label>
          </div>
          <p className="small muted">{current.telegram_bot_token_set ? "令牌已安全配置，留空保存将保持不变。" : "尚未配置机器人令牌。"}</p>
          {current.telegram_bot_token_set && <button className="button secondary compact" type="button" disabled={saving || provisioning} onClick={() => {
            if (draft === null) return;
            setDraft({ ...draft, clear_telegram_bot_token: true, telegram_bot_enable: false, telegram_bot_token: "" });
            setError(""); setSuccess("");
          }}>清除机器人令牌</button>}
          <p className="small muted">Webhook Base URL 留空时使用系统设置中的站点网址；两者都必须为 HTTPS。群组链接仅接受 t.me 或 telegram.me。</p>
        </div>
      </section>
      <section className="site-settings-card" aria-labelledby="telegram-webhook-heading">
        <div className="section-heading"><div><h2 id="telegram-webhook-heading">Webhook</h2><p className="muted">设置前会验证 Bot 身份，服务端生成独立 Secret Token 并通过请求头校验每次回调。</p></div></div>
        {current.telegram_bot_username !== "" && <p>当前机器人：<strong>@{current.telegram_bot_username}</strong></p>}
        {current.telegram_webhook_configured_at !== null && <p className="small muted">最近完成设置：{new Date(current.telegram_webhook_configured_at).toLocaleString()}</p>}
        <button className="button secondary" type="button" disabled={saving || provisioning || !draft.telegram_bot_enable || !tokenAvailable || !settingsSaved} onClick={() => void provision()}>{provisioning ? "正在设置…" : "一键设置 Webhook"}</button>
      </section>
      {success !== "" && <div className="alert success" role="status">{success}</div>}
      <div className="form-actions"><button className="button primary" type="submit" disabled={saving || provisioning}>{saving ? "正在保存…" : "保存 Telegram 设置"}</button></div>
    </form>}
    {error !== "" && !absent && <div className="form-actions"><button className="button secondary" type="button" disabled={saving || provisioning} onClick={() => void load()}>刷新最新设置</button></div>}
  </main>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "Telegram 设置请求失败";
}
