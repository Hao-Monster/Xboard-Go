import { useEffect, useMemo, useState } from "react";

import { Modal } from "../../components/Overlay";
import type { ClientCatalogDownload, ClientCatalogEntry, ClientCatalogQR } from "../../lib/api";

interface ClientCatalogAPI {
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
}

const filters = [
  ["all", "全部"], ["android", "Android"], ["ios", "iPhone / iPad"], ["windows", "Windows"], ["macos", "macOS"], ["linux", "Linux"]
] as const;
const platformLabels = Object.fromEntries(filters.slice(1)) as Record<string, string>;

export function ClientCatalogPage({ api }: { api: ClientCatalogAPI }) {
  const [clients, setClients] = useState<ClientCatalogEntry[]>([]);
  const [filter, setFilter] = useState("all");
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [qrTarget, setQRTarget] = useState<{ client: ClientCatalogEntry; download: ClientCatalogDownload } | null>(null);

  useEffect(() => {
    let live = true;
    void api.listClientCatalog().then((result) => { if (live) setClients(result); }).catch((cause: unknown) => { if (live) setError(message(cause)); }).finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const visible = useMemo(() => clients.filter((client) => filter === "all" || client.downloads.some((download) => download.platform === filter)), [clients, filter]);
  const chooseFilter = (next: string) => {
    setFilter(next);
    if (next !== "all") setSelected(Object.fromEntries(clients.filter((client) => client.downloads.some((download) => download.platform === next)).map((client) => [client.id, next])));
  };

  return <main className="page-shell client-catalog-page">
    <header className="page-header"><div><p className="eyebrow">HWID Clients</p><h1>客户端下载</h1><p className="muted">仅收录支持 HWID 设备识别的客户端；安装地址由服务端校验。</p></div><span className="security-note">✓ 安全中转链接</span></header>
    <nav className="client-filters" aria-label="平台筛选">{filters.map(([key, label]) => <button className={filter === key ? "active" : ""} aria-current={filter === key ? "page" : undefined} key={key} onClick={() => chooseFilter(key)}>{label}</button>)}</nav>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">正在加载客户端目录…</div> : visible.length === 0 ? <div className="empty-card">该平台暂无支持 HWID 的客户端。</div> : <section className="client-card-grid" aria-label="客户端目录">{visible.map((client) => {
      const downloads = filter === "all" ? client.downloads : client.downloads.filter((download) => download.platform === filter);
      const selectedPlatform = selected[client.id];
      const download = downloads.find((entry) => entry.platform === selectedPlatform) ?? downloads[0];
      return download === undefined ? null : <article className={`client-card ${client.featured ? "featured" : ""}`} aria-label={client.name} key={client.id}>
        <header><span className="client-logo" aria-hidden="true">{Array.from(client.name)[0] ?? "?"}</span><div><div className="client-badges"><span>{client.core}</span><span>✓ HWID</span>{client.featured && <strong>推荐</strong>}</div><h2>{client.name}</h2></div></header>
        <p className="muted">{client.description}</p>
        <label>选择下载平台<select value={download.platform} onChange={(event) => setSelected((current) => ({ ...current, [client.id]: event.target.value }))}>{downloads.map((entry) => <option value={entry.platform} key={entry.platform}>{platformLabels[entry.platform] ?? entry.platform}</option>)}</select></label>
        <div className="client-actions">
          <SecureLink href={download.download_url}>直接下载</SecureLink>
          <button className="button primary compact" onClick={() => setQRTarget({ client, download })}>扫码下载</button>
          {download.cloud_url !== null && <SecureLink href={download.cloud_url}>网盘下载</SecureLink>}
          {download.tutorial_url !== null && <SecureLink href={download.tutorial_url}>使用教程</SecureLink>}
        </div>
      </article>;
    })}</section>}
    <p className="client-footnote">安装包来自各客户端官方发布渠道；GitHub 客户端按受控规则匹配最新 Release。</p>
    {qrTarget !== null && <ClientQRModal api={api} target={qrTarget} onClose={() => setQRTarget(null)} />}
  </main>;
}

function SecureLink({ href, children }: { href: string; children: string }) {
  return <a className="button secondary compact" href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">{children}</a>;
}

function ClientQRModal({ api, target, onClose }: { api: ClientCatalogAPI; target: { client: ClientCatalogEntry; download: ClientCatalogDownload }; onClose: () => void }) {
  const [result, setResult] = useState<ClientCatalogQR | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    void api.clientCatalogQR(target.client.id, target.download.platform).then((value) => { if (live) setResult(value); }).catch((cause: unknown) => { if (live) setError(message(cause)); });
    return () => { live = false; };
  }, [api, target]);
  const title = `扫码下载 ${target.client.name}`;
  return <Modal title={title} onClose={onClose}>
    <div className="modal-header"><div><p className="eyebrow">{platformLabels[target.download.platform] ?? target.download.platform}</p><h2>{title}</h2></div><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>
    <p className="muted">未单独配置扫码链接时，二维码使用直接下载地址。</p>
    {error !== "" ? <div className="alert error" role="alert">{error}</div> : result === null ? <div className="client-qr-loading">正在生成下载二维码…</div> : <div className="client-qr-result"><img src={result.qr_code} alt={`${target.client.name} 下载二维码`} /><SecureLink href={result.download_url}>当前设备打开下载链接</SecureLink></div>}
  </Modal>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "客户端目录请求失败";
}
