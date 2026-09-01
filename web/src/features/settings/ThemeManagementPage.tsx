import { useEffect, useMemo, useRef, useState, type ChangeEvent, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import { themeAssetURL, type ThemeCatalog, type ThemeConfig, type ThemeConfigInput, type ThemeItem } from "../../lib/api";

interface ThemeManagementAPI {
  listThemes: () => Promise<ThemeCatalog>;
  updateThemeLayout: (revision: number, sidebarStyle: "light" | "dark", headerStyle: "light" | "dark") => Promise<ThemeCatalog>;
  uploadTheme: (file: File) => Promise<ThemeItem>;
  getTheme: (name: string) => Promise<ThemeItem>;
  updateThemeConfig: (name: string, input: ThemeConfigInput) => Promise<ThemeItem>;
  activateTheme: (name: string, revision: number) => Promise<ThemeCatalog>;
  deleteTheme: (name: string) => Promise<void>;
}

export function ThemeManagementPage({ api, onDirtyChange = () => undefined, onThemeChanged = () => undefined }: {
  api: ThemeManagementAPI;
  onDirtyChange?: (dirty: boolean) => void;
  onThemeChanged?: () => void;
}) {
  const [catalog, setCatalog] = useState<ThemeCatalog | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [preview, setPreview] = useState<ThemeItem | null>(null);
  const [previewIndex, setPreviewIndex] = useState(0);
  const [editing, setEditing] = useState<ThemeItem | null>(null);
  const [draft, setDraft] = useState<ThemeConfig | null>(null);
  const uploadRef = useRef<HTMLInputElement>(null);
  const settingsOpenerRef = useRef<HTMLButtonElement>(null);

  const dirty = editing !== null && draft !== null && JSON.stringify(editing.config) !== JSON.stringify(draft);
  const saving = busy.startsWith("save:");
  useEffect(() => { onDirtyChange(dirty); }, [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);
  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault(); event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const load = async () => {
    setLoading(true); setError("");
    try {
      setCatalog(await api.listThemes());
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    let live = true;
    void api.listThemes().then((next) => {
      if (live) setCatalog(next);
    }).catch((cause: unknown) => {
      if (live) setError(message(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  const upload = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file === undefined || busy !== "") return;
    if (!file.name.toLowerCase().endsWith(".zip")) {
      setError("请选择 ZIP 主题包"); return;
    }
    setBusy("upload"); setError(""); setSuccess("");
    try {
      const installed = await api.uploadTheme(file);
      setSuccess(`${installed.name} ${installed.version} 已上传`);
      await load();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const openSettings = async (item: ThemeItem) => {
    if (busy !== "") return;
    setBusy(`settings:${item.name}`); setError(""); setSuccess("");
    try {
      const current = await api.getTheme(item.name);
      setEditing(current); setDraft({ ...current.config });
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const closeSettings = () => {
    if (saving) return;
    if (dirty && !window.confirm("主题设置有未保存的修改，确认关闭并放弃这些修改吗？")) return;
    setEditing(null); setDraft(null);
  };

  const saveSettings = async (event: FormEvent) => {
    event.preventDefault();
    if (editing === null || draft === null || !dirty || busy !== "") return;
    setBusy(`save:${editing.name}`); setError(""); setSuccess("");
    try {
      const updated = await api.updateThemeConfig(editing.name, { revision: editing.revision, ...draft });
      setEditing(updated); setDraft({ ...updated.config });
      setCatalog((current) => current === null ? current : ({ ...current, themes: current.themes.map((item) => item.name === updated.name ? updated : item) }));
      setSuccess("主题设置已保存");
      onThemeChanged();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const activate = async (item: ThemeItem) => {
    if (catalog === null || busy !== "" || item.is_active) return;
    setBusy(`activate:${item.name}`); setError(""); setSuccess("");
    try {
      setCatalog(await api.activateTheme(item.name, catalog.revision));
      setSuccess(`${item.name} 已设为当前主题`);
      onThemeChanged();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const remove = async (item: ThemeItem) => {
    if (!item.can_delete || busy !== "" || !window.confirm("确定要删除该主题吗？删除后无法恢复。")) return;
    setBusy(`delete:${item.name}`); setError(""); setSuccess("");
    try {
      await api.deleteTheme(item.name);
      setCatalog((current) => current === null ? current : ({ ...current, themes: current.themes.filter((theme) => theme.name !== item.name) }));
      setSuccess(`${item.name} 已删除`);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const showPreview = (item: ThemeItem) => { setPreview(item); setPreviewIndex(0); };
  const updateLayout = async (sidebarStyle: "light" | "dark", headerStyle: "light" | "dark") => {
    if (catalog === null || busy !== "") return;
    setBusy("layout"); setError(""); setSuccess("");
    try {
      setCatalog(await api.updateThemeLayout(catalog.revision, sidebarStyle, headerStyle));
      setSuccess("导航样式已保存");
      onThemeChanged();
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };
  const themes = catalog?.themes ?? [];
  return <main className="page-shell theme-management-page">
    <header className="page-header"><div><p className="eyebrow">Appearance</p><h1>主题配置</h1><p className="muted">上传安全的声明式主题，预览后配置并激活；主题不会执行第三方脚本或模板代码。</p></div>
      <div className="theme-upload-action">
        <input ref={uploadRef} className="visually-hidden" id="theme-upload" aria-label="上传主题包" type="file" accept=".zip,application/zip" disabled={busy !== ""} onChange={(event) => void upload(event)} />
        <button className="button primary" type="button" disabled={busy !== ""} onClick={() => uploadRef.current?.click()}>{busy === "upload" ? "正在验证主题…" : "上传主题"}</button>
      </div>
    </header>
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {success !== "" && <div className="alert success theme-status" role="status">{success}</div>}
    {loading && catalog === null && <div className="empty-card">正在加载主题…</div>}
    {!loading && catalog === null && <button className="button secondary" type="button" onClick={() => void load()}>重新加载主题</button>}
    {catalog !== null && <>
      <section className="site-settings-card" aria-labelledby="theme-layout-heading">
        <div className="section-heading"><div><h2 id="theme-layout-heading">导航样式</h2><p className="muted">侧栏明暗基于当前主题色计算，顶栏保持独立的明暗配置。</p></div></div>
        <div className="commission-settings-grid">
          <label>侧栏样式<select value={catalog.sidebar_style} disabled={busy !== ""} onChange={(event) => void updateLayout(event.target.value as "light" | "dark", catalog.header_style)}><option value="light">浅色</option><option value="dark">深色</option></select></label>
          <label>顶栏样式<select value={catalog.header_style} disabled={busy !== ""} onChange={(event) => void updateLayout(catalog.sidebar_style, event.target.value as "light" | "dark")}><option value="light">浅色</option><option value="dark">深色</option></select></label>
        </div>
      </section>
      <div className="section-heading theme-catalog-heading"><div><h2>主题目录</h2><p className="muted">当前主题：{catalog.active_theme}</p></div><span className="count-pill">{themes.length} 个主题 · Revision {catalog.revision}</span></div>
      <div className="theme-grid">
        {themes.map((item) => <article className={`theme-card${item.is_active ? " active" : ""}`} key={item.name}>
          <ThemeSwatch item={item} />
          <div className="theme-card-body">
            <div className="section-heading"><div><h2>{item.name}</h2><p className="muted">{item.description || "未提供主题说明"}</p></div><span className="count-pill">v{item.version}</span></div>
            <div className="theme-badges"><span className={`status-badge ${item.is_active ? "enabled" : "warning"}`}>{item.is_active ? "当前主题" : "未激活"}</span>{item.is_system && <span className="badge inactive">系统主题</span>}</div>
            <div className="theme-actions">
              {item.images.length > 0 && <button className="button ghost compact" type="button" aria-label={`预览 ${item.name}`} onClick={() => showPreview(item)}>预览</button>}
              <button className="button secondary compact" type="button" aria-label={`设置 ${item.name}`} disabled={busy !== ""} onClick={(event) => { settingsOpenerRef.current = event.currentTarget; void openSettings(item); }}>主题设置</button>
              <button className="button primary compact" type="button" aria-label={item.is_active ? `当前主题 ${item.name}` : `激活 ${item.name}`} disabled={item.is_active || busy !== ""} onClick={() => void activate(item)}>{item.is_active ? "当前主题" : "激活主题"}</button>
              {item.can_delete && <button className="button destructive compact" type="button" aria-label={`删除 ${item.name}`} disabled={busy !== ""} onClick={() => void remove(item)}>删除</button>}
            </div>
          </div>
        </article>)}
      </div>
    </>}

    {preview !== null && <Modal title={`${preview.name} 主题预览`} className="theme-preview-modal" onClose={() => setPreview(null)}>
        <div className="modal-header"><div><p className="eyebrow">Theme preview</p><h2>{preview.name} 主题预览</h2></div><button className="icon-button" type="button" aria-label="关闭预览" onClick={() => setPreview(null)}>×</button></div>
        <img className="theme-preview-image" src={themeAssetURL(preview, preview.images[previewIndex] ?? "")} alt={`${preview.name} 预览 ${previewIndex + 1}`} />
        {preview.images.length > 1 && <div className="theme-preview-controls"><button className="button secondary compact" type="button" onClick={() => setPreviewIndex((previewIndex + preview.images.length - 1) % preview.images.length)}>上一张</button><span>{previewIndex + 1} / {preview.images.length}</span><button className="button secondary compact" type="button" onClick={() => setPreviewIndex((previewIndex + 1) % preview.images.length)}>下一张</button></div>}
    </Modal>}

    {editing !== null && draft !== null && <Modal title={`${editing.name} 主题设置`} className="theme-settings-modal" restoreFocusRef={settingsOpenerRef} onClose={closeSettings}>
        <div className="modal-header"><div><p className="eyebrow">Theme settings</p><h2>{editing.name} 主题设置</h2><p className="muted">Revision {editing.revision}</p></div><button className="icon-button" type="button" aria-label="关闭主题设置" disabled={saving} onClick={closeSettings}>×</button></div>
        <form className="form-stack" onSubmit={(event) => void saveSettings(event)}>
          <label>主题色<select value={draft.theme_color} disabled={busy !== ""} onChange={(event) => setDraft({ ...draft, theme_color: event.target.value })}>{Object.keys(editing.palettes).sort().map((name) => <option key={name} value={name}>{name}</option>)}</select></label>
          <label>背景<select value={draft.background_url} disabled={busy !== ""} onChange={(event) => setDraft({ ...draft, background_url: event.target.value })}><option value="">无背景图片</option>{editing.backgrounds.map((background) => <option key={background} value={background}>{background}</option>)}</select></label>
          <label>字号<select value={draft.font_scale} disabled={busy !== ""} onChange={(event) => setDraft({ ...draft, font_scale: event.target.value as ThemeConfig["font_scale"] })}><option value="small">小</option><option value="normal">标准</option><option value="large">大</option></select></label>
          <label>圆角<select value={draft.radius} disabled={busy !== ""} onChange={(event) => setDraft({ ...draft, radius: event.target.value as ThemeConfig["radius"] })}><option value="compact">紧凑</option><option value="rounded">圆角</option><option value="pill">胶囊</option></select></label>
          <div className="theme-config-preview" style={{ background: editing.palettes[draft.theme_color]?.background, color: editing.palettes[draft.theme_color]?.text, borderColor: editing.palettes[draft.theme_color]?.border }}><span style={{ color: editing.palettes[draft.theme_color]?.muted }}>实时预览</span><strong style={{ color: editing.palettes[draft.theme_color]?.primary }}>主题强调色</strong></div>
          <div className="form-actions"><button className="button secondary" type="button" disabled={busy !== ""} onClick={closeSettings}>取消</button><button className="button primary" type="submit" disabled={!dirty || busy !== ""}>{busy.startsWith("save:") ? "正在保存…" : "保存主题设置"}</button></div>
        </form>
    </Modal>}
  </main>;
}

function ThemeSwatch({ item }: { item: ThemeItem }) {
  const palette = useMemo(() => item.palettes[item.config.theme_color] ?? Object.values(item.palettes)[0], [item]);
  return <div className="theme-swatch" aria-hidden="true" style={{ background: palette?.background, borderColor: palette?.border }}><span style={{ background: palette?.surface }} /><strong style={{ background: palette?.primary }} /></div>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "主题请求失败";
}
