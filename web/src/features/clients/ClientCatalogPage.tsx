import { useEffect, useMemo, useState } from "react";

import { Modal } from "../../components/Overlay";
import type { ClientCatalogDownload, ClientCatalogEntry, ClientCatalogQR } from "../../lib/api";
import { distributorCloseLabel, distributorCopy, type DistributorLocale } from "../distributor/locale";

interface ClientCatalogAPI {
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
}

const platformLabels: Record<string, string> = { android: "Android", ios: "iPhone / iPad", windows: "Windows", macos: "macOS", linux: "Linux" };

export function ClientCatalogPage({ api, locale = "zh-CN" }: { api: ClientCatalogAPI; locale?: DistributorLocale }) {
  const [clients, setClients] = useState<ClientCatalogEntry[]>([]);
  const [filter, setFilter] = useState("all");
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [qrTarget, setQRTarget] = useState<{ client: ClientCatalogEntry; download: ClientCatalogDownload } | null>(null);
  const copy = distributorCopy[locale];
  const filters = [["all", copy.all], ...Object.entries(platformLabels)] as const;

  useEffect(() => {
    let live = true;
    void api.listClientCatalog().then((result) => { if (live) setClients(result); }).catch((cause: unknown) => { if (live) setError(message(cause, locale)); }).finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api, locale]);

  const visible = useMemo(() => clients.filter((client) => filter === "all" || client.downloads.some((download) => download.platform === filter)), [clients, filter]);
  const chooseFilter = (next: string) => {
    setFilter(next);
    if (next !== "all") setSelected(Object.fromEntries(clients.filter((client) => client.downloads.some((download) => download.platform === next)).map((client) => [client.id, next])));
  };

  return <main className="page-shell client-catalog-page">
    <header className="page-header"><div><p className="eyebrow">HWID Clients</p><h1>{copy.clientsTitle}</h1><p className="muted">{copy.clientsSubtitle}</p></div><span className="security-note">✓ {copy.secureLinks}</span></header>
    <nav className="client-filters" aria-label={copy.platformFilter}>{filters.map(([key, label]) => <button className={filter === key ? "active" : ""} aria-current={filter === key ? "page" : undefined} key={key} onClick={() => chooseFilter(key)}>{label}</button>)}</nav>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">{copy.loadingClients}</div> : visible.length === 0 ? <div className="empty-card">{copy.noClients}</div> : <section className="client-card-grid" aria-label={copy.clientList}>{visible.map((client) => {
      const downloads = filter === "all" ? client.downloads : client.downloads.filter((download) => download.platform === filter);
      const selectedPlatform = selected[client.id];
      const download = downloads.find((entry) => entry.platform === selectedPlatform) ?? downloads[0];
      return download === undefined ? null : <article className={`client-card ${client.featured ? "featured" : ""}`} aria-label={client.name} key={client.id}>
        <header><span className="client-logo" aria-hidden="true">{Array.from(client.name)[0] ?? "?"}</span><div><div className="client-badges"><span>{client.core}</span><span>✓ HWID</span>{client.featured && <strong>{copy.recommended}</strong>}</div><h2>{client.name}</h2></div></header>
        <p className="muted">{client.description}</p>
        <label>{copy.choosePlatform}<select value={download.platform} onChange={(event) => setSelected((current) => ({ ...current, [client.id]: event.target.value }))}>{downloads.map((entry) => <option value={entry.platform} key={entry.platform}>{platformLabels[entry.platform] ?? entry.platform}</option>)}</select></label>
        <div className="client-actions">
          <SecureLink href={download.download_url}>{copy.directDownload}</SecureLink>
          <button className="button primary compact" onClick={() => setQRTarget({ client, download })}>{copy.qrDownload}</button>
          {download.cloud_url !== null && <SecureLink href={download.cloud_url}>{copy.cloudDownload}</SecureLink>}
          {download.tutorial_url !== null && <SecureLink href={download.tutorial_url}>{copy.tutorial}</SecureLink>}
        </div>
      </article>;
    })}</section>}
    <p className="client-footnote">{copy.clientFootnote}</p>
    {qrTarget !== null && <ClientQRModal api={api} target={qrTarget} locale={locale} onClose={() => setQRTarget(null)} />}
  </main>;
}

function SecureLink({ href, children }: { href: string; children: string }) {
  return <a className="button secondary compact" href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">{children}</a>;
}

function ClientQRModal({ api, target, locale, onClose }: { api: ClientCatalogAPI; target: { client: ClientCatalogEntry; download: ClientCatalogDownload }; locale: DistributorLocale; onClose: () => void }) {
  const [result, setResult] = useState<ClientCatalogQR | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    void api.clientCatalogQR(target.client.id, target.download.platform).then((value) => { if (live) setResult(value); }).catch((cause: unknown) => { if (live) setError(message(cause, locale)); });
    return () => { live = false; };
  }, [api, locale, target]);
  const copy = distributorCopy[locale];
  const title = locale === "en-US" ? `Download ${target.client.name} by QR` : `扫码下载 ${target.client.name}`;
  return <Modal title={title} onClose={onClose}>
    <div className="modal-header"><div><p className="eyebrow">{platformLabels[target.download.platform] ?? target.download.platform}</p><h2>{title}</h2></div><button className="icon-button" aria-label={distributorCloseLabel(locale, title)} onClick={onClose}>×</button></div>
    <p className="muted">{copy.qrFallback}</p>
    {error !== "" ? <div className="alert error" role="alert">{error}</div> : result === null ? <div className="client-qr-loading">{copy.generatingQR}</div> : <div className="client-qr-result"><img src={result.qr_code} alt={locale === "en-US" ? `${target.client.name} download QR` : `${target.client.name} 下载二维码`} /><SecureLink href={result.download_url}>{copy.openDownload}</SecureLink></div>}
  </Modal>;
}

function message(cause: unknown, locale: DistributorLocale): string {
  return cause instanceof Error ? cause.message : locale === "en-US" ? "Client catalog request failed" : "客户端目录请求失败";
}
