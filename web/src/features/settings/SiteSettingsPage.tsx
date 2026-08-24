import { useEffect, useState, type FormEvent } from "react";

import type { AdminAPI, SiteSettings, SiteSettingsInput } from "../../lib/api";

type SiteSettingsAPI = Pick<AdminAPI, "getSiteSettings" | "updateSiteSettings">;
type SiteDraft = Omit<SiteSettingsInput, "revision">;

export function SiteSettingsPage({ api, onIdentityChanged }: {
  api: SiteSettingsAPI;
  onIdentityChanged: (settings: SiteSettings) => void;
}) {
  const [current, setCurrent] = useState<SiteSettings | null>(null);
  const [draft, setDraft] = useState<SiteDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const applySettings = (settings: SiteSettings) => {
    setCurrent(settings);
    setDraft(toDraft(settings));
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      applySettings(await api.getSiteSettings());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.getSiteSettings().then((settings) => {
      if (active) applySettings(settings);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const updated = await api.updateSiteSettings({ revision: current.revision, ...draft });
      applySettings(updated);
      onIdentityChanged(updated);
      setSaved(true);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };

  const updateDraft = <K extends keyof SiteDraft,>(field: K, value: SiteDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [field]: value });
    setSaved(false);
  };

  return <main className="page-shell site-settings-page">
    <header className="page-header"><div><p className="eyebrow">Configuration</p><h1>系统设置</h1><p className="muted">配置站点身份、公开品牌信息和注册安全策略。</p></div></header>
    {loading && draft === null && <div className="empty-card">正在加载站点设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {draft === null && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载站点设置</button>}
    {draft !== null && current !== null && <section className="site-settings-card" aria-labelledby="site-settings-heading">
      <div className="section-heading"><div><h2 id="site-settings-heading">站点设置</h2><p className="muted">字段与旧 Xboard 站点设置保持同一业务含义。</p></div><span className="count-pill">Revision {current.revision}</span></div>
      <form className="form-stack site-settings-form" onSubmit={(event) => void save(event)}>
        <label>站点名称<input required value={draft.app_name} onChange={(event) => updateDraft("app_name", event.target.value)} /></label>
        <label>站点描述<textarea value={draft.app_description} onChange={(event) => updateDraft("app_description", event.target.value)} /></label>
        <div className="site-settings-url-grid">
          <label>站点网址<input type="url" placeholder="https://panel.example.com" value={draft.app_url} onChange={(event) => updateDraft("app_url", event.target.value)} /></label>
          <label>用户条款(TOS)URL<input type="url" placeholder="https://panel.example.com/terms" value={draft.tos_url} onChange={(event) => updateDraft("tos_url", event.target.value)} /></label>
          <label>LOGO<input type="url" placeholder="请输入LOGO URL，末尾不要/" value={draft.logo} onChange={(event) => updateDraft("logo", event.target.value)} /></label>
        </div>
        <label className="switch-label"><input type="checkbox" checked={draft.stop_register} onChange={(event) => updateDraft("stop_register", event.target.checked)} />停止新用户注册</label>
        <p className="small muted">网址可留空；非空时必须是完整的 HTTP 或 HTTPS 地址。LOGO 用于显示需要品牌标识的地方。站点描述最多 500 个字符。</p>
        <fieldset className="settings-fieldset">
          <legend>注册安全策略</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.email_whitelist_enable} onChange={(event) => updateDraft("email_whitelist_enable", event.target.checked)} />邮箱后缀白名单</label>
          {draft.email_whitelist_enable && <label>邮箱后缀<textarea required aria-describedby="email-whitelist-help" value={draft.email_whitelist_suffix.join("\n")} onChange={(event) => updateDraft("email_whitelist_suffix", suffixesFromText(event.target.value))} /></label>}
          {draft.email_whitelist_enable && <p className="small muted" id="email-whitelist-help">每行一个完整域名，不支持通配符；域名匹配不区分大小写。</p>}
          <label className="switch-label"><input type="checkbox" checked={draft.email_gmail_limit_enable} onChange={(event) => updateDraft("email_gmail_limit_enable", event.target.checked)} />禁止使用Gmail多别名</label>
          <p className="small muted">启用后拒绝 Gmail 与 Googlemail 地址中包含点号或加号的别名，不影响其他邮箱域名。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.register_limit_by_ip_enable} onChange={(event) => updateDraft("register_limit_by_ip_enable", event.target.checked)} />IP注册限制</label>
          {draft.register_limit_by_ip_enable && <div className="registration-policy-grid">
            <label>注册次数<input type="number" required min={1} max={100} value={draft.register_limit_count} onChange={(event) => updateDraft("register_limit_count", Number(event.target.value))} /></label>
            <label>限制时长（分钟）<input type="number" required min={1} max={10080} value={draft.register_limit_expire} onChange={(event) => updateDraft("register_limit_expire", Number(event.target.value))} /></label>
          </div>}
          <p className="small muted">只统计成功注册；达到次数后，在滑动窗口结束前拒绝同一来源 IP 的新注册。</p>
        </fieldset>
        {saved && <div className="alert success" role="status">站点设置已保存</div>}
        <div className="form-actions">
          {error !== "" && draft !== null && <button className="button secondary" type="button" disabled={saving} onClick={() => void load()}>刷新最新设置</button>}
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存站点设置"}</button>
        </div>
      </form>
    </section>}
  </main>;
}

function toDraft(settings: SiteSettings): SiteDraft {
  return {
    app_name: settings.app_name,
    app_description: settings.app_description,
    app_url: settings.app_url,
    tos_url: settings.tos_url,
    logo: settings.logo,
    stop_register: settings.stop_register,
    email_whitelist_enable: settings.email_whitelist_enable,
    email_whitelist_suffix: settings.email_whitelist_suffix,
    email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable,
    register_limit_count: settings.register_limit_count,
    register_limit_expire: settings.register_limit_expire
  };
}

function suffixesFromText(value: string): string[] {
  return value.split(/\r?\n/).map((suffix) => suffix.trim()).filter((suffix) => suffix !== "");
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "站点设置请求失败";
}
