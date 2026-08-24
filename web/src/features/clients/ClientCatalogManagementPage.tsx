import { useEffect, useMemo, useState } from "react";

import type { AdminAPI, AdminClientCatalog, ClientCatalogActionLinks, ClientCatalogOverrideInput } from "../../lib/api";

type ClientCatalogAdminAPI = Pick<AdminAPI, "listClientCatalogAdmin" | "saveClientCatalog">;

const platformLabels: Record<string, string> = {
  android: "Android", ios: "iPhone / iPad", windows: "Windows", macos: "macOS", linux: "Linux"
};
const fields: Array<{ key: keyof ClientCatalogActionLinks; label: string; help: string }> = [
  { key: "direct", label: "直接下载", help: "留空时使用官方商店或 GitHub 最新安装包。" },
  { key: "qr", label: "扫码下载", help: "留空时二维码复用直接下载地址。" },
  { key: "cloud", label: "网盘下载", help: "留空时用户页面隐藏此按钮。" },
  { key: "tutorial", label: "使用教程", help: "支持 HTTPS 外链或以 / 开头的站内地址。" }
];

export function ClientCatalogManagementPage({ api }: { api: ClientCatalogAdminAPI }) {
  const [catalog, setCatalog] = useState<AdminClientCatalog | null>(null);
  const [selected, setSelected] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let live = true;
    void api.listClientCatalogAdmin().then((result) => {
      if (!live) return;
      setCatalog(result);
      setSelected((current) => result.clients.some((client) => client.id === current) ? current : result.clients[0]?.id ?? "");
    }).catch((cause: unknown) => {
      if (live) setError(message(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, reload]);

  const active = useMemo(() => catalog?.clients.find((client) => client.id === selected) ?? catalog?.clients[0] ?? null, [catalog, selected]);
  const update = (clientID: string, platform: string, action: keyof ClientCatalogActionLinks, value: string) => {
    setSuccess("");
    setCatalog((current) => current === null ? null : ({ ...current, clients: current.clients.map((client) => client.id !== clientID ? client : ({
      ...client,
      platforms: client.platforms.map((entry) => entry.platform !== platform ? entry : ({ ...entry, links: { ...entry.links, [action]: value } }))
    })) }));
  };
  const save = async () => {
    if (catalog === null) return;
    setSaving(true);
    setError("");
    setSuccess("");
    try {
      const saved = await api.saveClientCatalog(catalog.revision, linksPayload(catalog));
      setCatalog(saved);
      setSuccess("客户端按钮配置已保存。");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setSaving(false);
    }
  };
  const retry = () => {
    setLoading(true);
    setError("");
    setReload((current) => current + 1);
  };

  return <main className="page-shell client-admin-page">
    <header className="page-header">
      <div><p className="eyebrow">Client Catalog</p><h1>客户端管理</h1><p className="muted">按客户端和系统配置直接、扫码、网盘与教程按钮；所有跳转由服务端验证。</p></div>
      <button className="button primary" disabled={catalog === null || saving} onClick={() => void save()}>{saving ? "正在保存…" : "保存全部配置"}</button>
    </header>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={retry}>重新加载</button></div>}
    {success !== "" && <div className="alert success resource-alert" role="status">{success}</div>}
    {loading ? <div className="empty-card">正在加载客户端配置…</div> : catalog === null || active === null ? <div className="empty-card">没有可配置的客户端。</div> : <div className="client-admin-layout">
      <nav className="client-tabs" aria-label="客户端列表">{catalog.clients.map((client) => <button type="button" className={client.id === active.id ? "active" : ""} aria-current={client.id === active.id ? "page" : undefined} key={client.id} onClick={() => setSelected(client.id)}><strong>{client.name}</strong><small>{client.platforms.length} 个系统</small></button>)}</nav>
      <section className="client-config-panel">
        <header className="section-heading"><div><h2>{active.name}</h2><span className="count-pill">{active.core}</span></div><p className="muted small">空白字段使用默认行为或隐藏对应按钮。</p></header>
        <div className="client-platform-stack">{active.platforms.map((entry) => <section className="client-platform-config" aria-label={`${active.name} ${platformLabels[entry.platform] ?? entry.platform}`} key={entry.platform}>
          <header><h3>{platformLabels[entry.platform] ?? entry.platform}</h3><code>{entry.platform}</code></header>
          <div className="client-link-grid">{fields.map((field) => <label key={field.key}><span>{field.label}</span><input aria-label={field.label} type={field.key === "tutorial" ? "text" : "url"} maxLength={2048} autoComplete="off" spellCheck={false} value={entry.links[field.key]} placeholder={field.key === "tutorial" ? "https://… 或 /guide/…" : "https://…"} onChange={(event) => update(active.id, entry.platform, field.key, event.target.value)} /><small>{field.help}</small></label>)}</div>
        </section>)}</div>
      </section>
    </div>}
  </main>;
}

function linksPayload(catalog: AdminClientCatalog): ClientCatalogOverrideInput {
  return Object.fromEntries(catalog.clients.map((client) => [client.id, Object.fromEntries(client.platforms.map((entry) => [entry.platform, { ...entry.links }]))]));
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "客户端配置请求失败";
}
