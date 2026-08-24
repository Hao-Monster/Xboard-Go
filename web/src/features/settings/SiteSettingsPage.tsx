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

  const updateDraft = (field: keyof SiteDraft, value: string) => {
    if (draft === null) return;
    setDraft({ ...draft, [field]: value });
    setSaved(false);
  };

  return <main className="page-shell site-settings-page">
    <header className="page-header"><div><p className="eyebrow">Configuration</p><h1>系统设置</h1><p className="muted">配置站点身份和公开品牌信息；其余旧版配置将按独立业务切片补齐。</p></div></header>
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
        </div>
        <p className="small muted">网址可留空；非空时必须是完整的 HTTP 或 HTTPS 地址。站点描述最多 500 个字符。</p>
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
    tos_url: settings.tos_url
  };
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "站点设置请求失败";
}
