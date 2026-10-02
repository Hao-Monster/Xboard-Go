import { useEffect, useState, type ReactNode } from "react";

import { PortalIcon } from "../user/PortalIcon";
import { Modal } from "../../components/Overlay";
import type { SubscriptionQR, UserSubscription } from "../../lib/api";

interface UserSubscriptionAPI {
  getSubscription: () => Promise<UserSubscription>;
  getSubscriptionQR: () => Promise<SubscriptionQR>;
  resetSubscriptionSecurity: () => Promise<UserSubscription>;
}

// Existing subscriptions remain purchases after expiry or exhaustion. Imported
// or manually granted traffic may exist without a catalog plan.
export function hasSubscription(subscription: Pick<UserSubscription, "plan_id" | "transfer_enable">): boolean {
  return subscription.plan_id !== null || subscription.transfer_enable > 0;
}

export function UserSubscriptionPage({ api, onOpenTutorial, onOpenPlans, onOpenTickets, announcement }: { api: UserSubscriptionAPI; onOpenTutorial?: () => void; onOpenPlans?: () => void; onOpenTickets?: () => void; announcement?: ReactNode }) {
  const [subscription, setSubscription] = useState<UserSubscription | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetting, setResetting] = useState(false);

  const load = async () => {
    setLoading(true);
    setError("");
    try {
      setSubscription(await api.getSubscription());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let live = true;
    void api.getSubscription().then((value) => { if (live) setSubscription(value); })
      .catch((cause: unknown) => { if (live) setError(messageOf(cause)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const reset = async () => {
    setResetting(true);
    setError("");
    setMessage("");
    try {
      setSubscription(await api.resetSubscriptionSecurity());
      setResetOpen(false);
      setMessage("订阅信息已重置");
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setResetting(false);
    }
  };

  if (loading && subscription === null) return <main className="page-shell"><div className="empty-card">正在加载订阅信息…</div></main>;
  if (subscription === null) return <main className="page-shell"><header className="page-header"><div><h1>我的订阅</h1></div></header>{error !== "" && <div className="alert error" role="alert">{error}</div>}<button className="button secondary" onClick={() => void load()}>重新加载订阅信息</button></main>;

  const used = Math.max(0, subscription.u + subscription.d);
  const percent = subscription.transfer_enable <= 0 ? 0 : Math.min(100, Math.round(used / subscription.transfer_enable * 100));
  return <main className="page-shell user-subscription-page">
    {announcement}
    <header className="page-header"><div><p className="eyebrow">Dashboard</p><h1>我的订阅</h1><p className="muted">查看套餐、流量与订阅地址，并一键导入客户端。</p></div><span className={`status-badge ${subscription.subscription_valid ? "enabled" : "blocked"}`}>{subscription.subscription_valid ? "订阅可用" : "订阅不可用"}</span></header>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {message !== "" && <div className="alert success resource-alert" role="status">{message}</div>}
    {!hasSubscription(subscription) && onOpenPlans ? <section className="portal-empty-subscription"><h2>当前订阅</h2><button onClick={onOpenPlans}><PortalIcon name="plus" /><span>购买订阅</span></button></section> : <section className="subscription-overview-card" aria-label="订阅概览">
      <div className="subscription-plan-heading"><div><p className="eyebrow">当前套餐</p><h2>{subscription.plan?.name ?? "暂无订阅套餐"}</h2></div><strong>{percent}%</strong></div>
      <p className="muted">{expiryText(subscription.expired_at)}{subscription.next_reset_at !== null ? `，已用流量将在 ${formatDate(subscription.next_reset_at)} 重置` : ""}</p>
      <progress max={100} value={percent} aria-label="流量使用进度" aria-valuenow={percent} aria-valuetext={`${percent}%`} />
      <p className="subscription-traffic-copy">已用 {formatBytes(used)} / 总计 {formatBytes(subscription.transfer_enable)}</p>
      <div className="subscription-detail-grid">
        <div><span>设备限制</span><strong>{subscription.device_limit > 0 ? `${subscription.device_limit} 台` : "不限"}</strong></div>
        <div><span>速度限制</span><strong>{subscription.speed_limit > 0 ? `${subscription.speed_limit} Mbps` : "不限"}</strong></div>
        <div><span>下次重置</span><strong>{subscription.reset_day === null ? "不重置" : `${subscription.reset_day} 天`}</strong></div>
      </div>
    </section>}
    {hasSubscription(subscription) && <section className="subscription-action-card" aria-labelledby="subscription-address-heading">
      <div className="section-heading"><div><h2 id="subscription-address-heading">订阅地址</h2><p className="muted">订阅地址属于私密凭证，请勿公开分享。</p></div></div>
      <label>订阅地址<input className="monospace" readOnly value={subscription.subscribe_url} /></label>
      <div className="subscription-actions">
        <button className="button primary" type="button" onClick={() => setImportOpen(true)}><PortalIcon name="rss" />一键订阅</button>
        <CopyButton value={subscription.subscribe_url} />
        <button className="button secondary danger-text" type="button" onClick={() => setResetOpen(true)}><PortalIcon name="refresh" />重置订阅信息</button>
      </div>
    </section>}
    {(onOpenTutorial || onOpenPlans || onOpenTickets) && <section className="portal-shortcuts" aria-label="捷径"><h2>捷径</h2>
      {onOpenTutorial && <button onClick={onOpenTutorial}><span>查看教程<small>学习如何使用订阅服务</small></span><PortalIcon name="book" /></button>}
      {hasSubscription(subscription) && <button onClick={() => setImportOpen(true)}><span>导入客户端<small>快速将节点导入对应客户端进行使用</small></span><PortalIcon name="rss" /></button>}
      {onOpenPlans && <button onClick={onOpenPlans}><span>购买订阅<small>查看可购买或可续费的套餐</small></span><PortalIcon name="bag" /></button>}
      {onOpenTickets && <button onClick={onOpenTickets}><span>遇到问题<small>遇到问题可以通过工单与我们沟通</small></span><PortalIcon name="ticket" /></button>}
    </section>}
    {importOpen && hasSubscription(subscription) && <SubscriptionImportModal api={api} subscription={subscription} onOpenTutorial={onOpenTutorial} onClose={() => setImportOpen(false)} />}
    {resetOpen && <Modal title="重置订阅信息" onClose={() => { if (!resetting) setResetOpen(false); }}>
      <div className="modal-header"><div><p className="eyebrow">Security</p><h2>重置订阅信息</h2></div><button className="icon-button" aria-label="关闭重置订阅信息" disabled={resetting} onClick={() => setResetOpen(false)}>×</button></div>
      <div className="alert warning">重置会同时更换 UUID 和订阅令牌，旧订阅地址会立即失效，所有设备都需要重新导入。</div>
      <div className="form-actions"><button className="button secondary" disabled={resetting} onClick={() => setResetOpen(false)}>取消</button><button className="button destructive" disabled={resetting} onClick={() => void reset()}>{resetting ? "正在重置…" : "确认重置"}</button></div>
    </Modal>}
  </main>;
}

function SubscriptionImportModal({ api, subscription, onOpenTutorial, onClose }: { api: UserSubscriptionAPI; subscription: UserSubscription; onOpenTutorial?: () => void; onClose: () => void }) {
  const [qr, setQR] = useState<SubscriptionQR | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    void api.getSubscriptionQR().then((value) => { if (live) setQR(value); }).catch((cause: unknown) => { if (live) setError(messageOf(cause)); });
    return () => { live = false; };
  }, [api]);
  return <Modal title="一键订阅" onClose={onClose}>
    <div className="modal-header"><div><p className="eyebrow">Import</p><h2>一键订阅</h2></div><button className="icon-button" aria-label="关闭一键订阅" onClick={onClose}>×</button></div>
    <p className="muted">扫描二维码订阅，或选择当前设备上的客户端。</p>
    {error !== "" ? <div className="alert error" role="alert">{error}</div> : qr === null ? <div className="client-qr-loading">正在生成订阅二维码…</div> : <div className="client-qr-result"><img src={qr.qr_code} alt="订阅二维码" /></div>}
    <div className="subscription-import-actions">
      <CopyButton value={subscription.subscribe_url} />
      <a className="button secondary" href={`clash://install-config?url=${encodeURIComponent(subscription.subscribe_url)}`}>导入到 Clash</a>
      <a className="button secondary" href={`hiddify://import/${encodeURIComponent(subscription.subscribe_url)}`}>导入到 Hiddify</a>
      {onOpenTutorial !== undefined && <button className="button ghost" type="button" onClick={() => { onClose(); onOpenTutorial(); }}>不会使用，查看使用教程</button>}
    </div>
  </Modal>;
}

function CopyButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState(false);
  const copy = async () => {
    setCopied(false);
    setError(false);
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setError(true);
    }
  };
  return <><button className="button secondary" type="button" onClick={() => void copy()}><PortalIcon name="copy" />复制订阅地址</button>{copied && <span className="small copy-status" role="status">订阅地址已复制</span>}{error && <span className="small danger-text" role="alert">复制失败，请手动复制</span>}</>;
}

function formatBytes(value: number): string {
  if (value <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024)));
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "日期未知" : new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function expiryText(value: string | null): string {
  if (value === null) return "永久有效";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "到期时间未知" : `于 ${new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium" }).format(date)} 到期`;
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "订阅信息请求失败";
}
