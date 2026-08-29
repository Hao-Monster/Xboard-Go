import { useEffect, useState, type FormEvent } from "react";

import type { ClientAppSettings, ClientAppSettingsInput } from "../../lib/api";

interface ClientAppSettingsAPI {
  getClientAppSettings: () => Promise<ClientAppSettings>;
  updateClientAppSettings: (input: ClientAppSettingsInput) => Promise<ClientAppSettings>;
}

type ClientAppDraft = Omit<ClientAppSettingsInput, "revision">;

interface PlatformFields {
  name: string;
  description: string;
  version: keyof Pick<ClientAppDraft, "windows_version" | "macos_version" | "android_version">;
  downloadURL: keyof Pick<ClientAppDraft, "windows_download_url" | "macos_download_url" | "android_download_url">;
  placeholder: string;
}

const platforms: PlatformFields[] = [
  { name: "Windows", description: "Windows 客户端版本与安装包地址", version: "windows_version", downloadURL: "windows_download_url", placeholder: "https://download.example.com/client.exe" },
  { name: "macOS", description: "macOS 客户端版本与磁盘映像地址", version: "macos_version", downloadURL: "macos_download_url", placeholder: "https://download.example.com/client.dmg" },
  { name: "Android", description: "Android 客户端版本与 APK 地址", version: "android_version", downloadURL: "android_download_url", placeholder: "https://download.example.com/client.apk" }
];

export function ClientAppSettingsPage({ api, onDirtyChange = () => undefined }: {
  api: ClientAppSettingsAPI;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  const [current, setCurrent] = useState<ClientAppSettings | null>(null);
  const [draft, setDraft] = useState<ClientAppDraft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  const apply = (settings: ClientAppSettings) => {
    setCurrent(settings);
    setDraft(toDraft(settings));
  };
  const dirty = current !== null && draft !== null && platforms.some(({ version, downloadURL }) =>
    current[version] !== draft[version] || current[downloadURL] !== draft[downloadURL]);

  useEffect(() => { onDirtyChange(dirty); }, [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);
  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const load = async () => {
    setLoading(true); setError(""); setSuccess("");
    try {
      apply(await api.getClientAppSettings());
    } catch (cause) {
      setError(message(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void api.getClientAppSettings().then((settings) => {
      if (live) apply(settings);
    }).catch((cause: unknown) => {
      if (live) setError(message(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  const update = (key: keyof ClientAppDraft, value: string) => {
    if (draft === null) return;
    setDraft({ ...draft, [key]: value });
    setError(""); setSuccess("");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null || saving || !dirty) return;
    setSaving(true); setError(""); setSuccess("");
    try {
      apply(await api.updateClientAppSettings({ revision: current.revision, ...draft }));
      setSuccess("客户端版本设置已保存");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSaving(false);
    }
  };

  const absent = current === null || draft === null;
  return <main className="page-shell client-app-settings-page">
    <header className="page-header"><div><p className="eyebrow">Client applications</p><h1>客户端版本</h1><p className="muted">配置与旧版 Xboard 一致的 Windows、macOS 和 Android 客户端版本及下载地址。</p></div></header>
    {loading && absent && <div className="empty-card">正在加载客户端版本设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {absent && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载客户端版本设置</button>}
    {!absent && <form className="client-app-settings-form" onSubmit={(event) => void save(event)}>
      <div className="section-heading"><div><h2>应用发布信息</h2><p className="muted">留空表示暂不提供该平台的版本信息；下载地址仅接受绝对 HTTPS URL。</p></div><span className="count-pill">Revision {current.revision}</span></div>
      <div className="client-app-platform-grid">
        {platforms.map((platform) => <section className="site-settings-card client-app-platform-card" key={platform.name} aria-labelledby={`client-app-${platform.name}-heading`}>
          <div className="section-heading"><div><h2 id={`client-app-${platform.name}-heading`}>{platform.name}</h2><p className="muted">{platform.description}</p></div></div>
          <div className="form-stack">
            <label>{platform.name} 版本<input maxLength={128} autoComplete="off" disabled={saving} value={draft[platform.version]} onChange={(event) => update(platform.version, event.target.value)} /></label>
            <label>{platform.name} 下载地址<input type="url" inputMode="url" pattern="https://.*" maxLength={2048} autoComplete="url" disabled={saving} placeholder={platform.placeholder} value={draft[platform.downloadURL]} onChange={(event) => update(platform.downloadURL, event.target.value)} /></label>
          </div>
        </section>)}
      </div>
      {success !== "" && <div className="alert success" role="status">{success}</div>}
      <div className="form-actions"><button className="button primary" type="submit" disabled={saving || !dirty}>{saving ? "正在保存…" : "保存客户端版本"}</button></div>
    </form>}
  </main>;
}

function toDraft(settings: ClientAppSettings): ClientAppDraft {
  return {
    windows_version: settings.windows_version, windows_download_url: settings.windows_download_url,
    macos_version: settings.macos_version, macos_download_url: settings.macos_download_url,
    android_version: settings.android_version, android_download_url: settings.android_download_url
  };
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "客户端版本设置请求失败";
}
