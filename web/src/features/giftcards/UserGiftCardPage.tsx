import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import { purchaseSnapshotSummary } from "../orders/GiftCardPurchaseDetails";
import type { GiftCardPreview, GiftCardRedeemResult, GiftCardReward, GiftCardUsagePage } from "../../lib/api";
import "./UserGiftCardPage.css";

export interface UserGiftCardAPI {
  checkGiftCard: (code: string) => Promise<GiftCardPreview>;
  redeemGiftCard: (code: string) => Promise<GiftCardRedeemResult>;
  listMyGiftCardUsages: (page?: number, pageSize?: number) => Promise<GiftCardUsagePage>;
}

export function UserGiftCardPage({ api }: { api: UserGiftCardAPI }) {
  const [code, setCode] = useState("");
  const [checked, setChecked] = useState<{ code: string; preview: GiftCardPreview } | null>(null);
  const preview = checked?.preview ?? null;
  const [history, setHistory] = useState<GiftCardUsagePage>({ items: [], total: 0, page: 1, page_size: 15 });
  const [checking, setChecking] = useState(false); const [redeeming, setRedeeming] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");
  const [historyError, setHistoryError] = useState("");
  const [historyLoading, setHistoryLoading] = useState(true);
  const queryVersion = useRef(0);
  const historyVersion = useRef(0);
  const requestedPage = useRef(1);
  const redeemingRef = useRef(false);
  const fetchHistory = useCallback((page: number, version: number) => api.listMyGiftCardUsages(page, 15)
    .then(result => {
      if (version === historyVersion.current) setHistory(result);
    }).catch((cause: unknown) => {
      if (version === historyVersion.current) setHistoryError(cause instanceof Error ? cause.message : "兑换记录加载失败");
    }).finally(() => {
      if (version === historyVersion.current) setHistoryLoading(false);
    }), [api]);
  const loadHistory = (page = 1) => {
    requestedPage.current = page;
    setHistoryLoading(true);
    setHistoryError("");
    return fetchHistory(page, ++historyVersion.current);
  };
  useEffect(() => {
    const historyRequests = historyVersion;
    const queries = queryVersion;
    void fetchHistory(1, ++historyRequests.current);
    return () => { historyRequests.current++; queries.current++; };
  }, [fetchHistory]);
  const check = async (event: FormEvent) => {
    event.preventDefault();
    if (redeemingRef.current || checking || code.trim() === "") return;
    const queriedCode = code.trim().toUpperCase();
    const version = ++queryVersion.current;
    setChecking(true); setChecked(null); setError(""); setMessage("");
    try {
      const result = await api.checkGiftCard(queriedCode);
      if (version === queryVersion.current) setChecked({ code: queriedCode, preview: result });
    } catch (cause) {
      if (version === queryVersion.current) setError(cause instanceof Error ? cause.message : "查询失败");
    } finally {
      if (version === queryVersion.current) setChecking(false);
    }
  };
  const redeem = async () => {
    if (redeemingRef.current || checking || checked === null || !checked.preview.can_redeem || checked.code !== code.trim().toUpperCase()) return;
    // Bind confirmation to the queried code; a synchronous lock also covers rapid repeated clicks.
    redeemingRef.current = true;
    queryVersion.current++;
    setRedeeming(true); setError("");
    try {
      const result = await api.redeemGiftCard(checked.code);
      setMessage(`${result.message} ${rewardSummary(result.rewards)}`);
      setChecked(null); setCode("");
      await loadHistory(1);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "兑换失败"); }
    finally { redeemingRef.current = false; setRedeeming(false); }
  };
  return <main className="page-shell user-gift-card"><header className="page-header"><div><p className="eyebrow">Gift card</p><h1>礼品卡兑换</h1><p className="muted">输入兑换码，确认奖励和使用条件后再兑换。</p></div></header>
    <section className="site-settings-card"><h2>兑换礼品卡</h2><form className="coupon-check-row" onSubmit={(event) => void check(event)}><input aria-label="礼品卡兑换码" autoComplete="off" minLength={8} maxLength={32} pattern="[A-Za-z0-9]+" value={code} disabled={redeeming} placeholder="请输入兑换码" required onChange={(event) => { queryVersion.current++; setCode(event.target.value.toUpperCase()); setChecked(null); setChecking(false); setError(""); setMessage(""); }} /><button className="button secondary" disabled={checking || redeeming || code.trim() === ""}>{checking ? "查询中…" : "查询奖励"}</button></form>
      {preview !== null && <div className="gift-preview" role="status"><h3>{preview.template.name}</h3><p>{rewardSummary(preview.reward_preview)}</p>{preview.template.type === 4 && <><p className="muted">兑换后生成兑换码购买订单；外部支付金额未核验。请先取消待支付订单，或等待处理中订单完成后再兑换。</p>{preview.purchase_preview && <dl><dt>总流量额度</dt><dd>{trafficGB(preview.purchase_preview.transfer_before)} → {trafficGB(preview.purchase_preview.transfer_after)}</dd><dt>{preview.purchase_preview.renewal ? "已用流量（保留）" : "兑换后已用流量"}</dt><dd>{trafficGB(preview.purchase_preview.used_traffic)}</dd><dt>兑换后剩余流量</dt><dd>{trafficGB(Math.max(0, preview.purchase_preview.transfer_after - preview.purchase_preview.used_traffic))}</dd><dt>有效期</dt><dd>{expiry(preview.purchase_preview.expires_before)} → {expiry(preview.purchase_preview.expires_after)}</dd></dl>}</>}{preview.can_redeem ? <button className="button primary" disabled={redeeming} onClick={() => void redeem()}>{redeeming ? "正在兑换…" : "确认兑换"}</button> : <div className="alert error" role="alert">{preview.reason}</div>}</div>}
      {error !== "" && <div className="alert error" role="alert">{error}</div>}{message !== "" && <div className="alert success" role="status">{message}</div>}
    </section>
    <section className="site-settings-card" aria-labelledby="gift-history-heading"><h2 id="gift-history-heading">兑换记录</h2>
      {historyError !== "" && <div className="alert error" role="alert">{historyError}<button className="button ghost compact" disabled={historyLoading || redeeming} onClick={() => void loadHistory(requestedPage.current)}>重试加载记录</button></div>}
      {historyLoading && <p role="status">正在加载兑换记录…</p>}
      {history.items.length === 0 ? !historyLoading && historyError === "" && <div className="empty-card">暂无兑换记录</div> : <div className="resource-table-wrap"><table className="resource-table" aria-label="兑换记录" aria-busy={historyLoading}><thead><tr><th>模板</th><th>兑换码</th><th>奖励</th><th>兑换时间</th><th>关联订单</th></tr></thead><tbody>{history.items.map((item) => <tr key={item.id}><td data-label="模板">{item.template_name}</td><td data-label="兑换码"><code>{maskedCode(item.code ?? "")}</code></td><td data-label="奖励">{rewardSummary(item.rewards)}</td><td data-label="兑换时间">{new Date(item.used_at).toLocaleString("zh-CN", { hour12: false })}</td><td data-label="关联订单">{item.order_trade_no ?? "—"}</td></tr>)}</tbody></table></div>}
      {history.total > history.page_size && <nav className="pagination-footer" aria-label="兑换记录分页"><button className="button secondary compact" disabled={historyLoading || redeeming || history.page <= 1} onClick={() => void loadHistory(history.page - 1)}>上一页</button><span>第 {history.page} / {Math.ceil(history.total / history.page_size)} 页 · 共 {history.total} 条</span><button className="button secondary compact" disabled={historyLoading || redeeming || history.page * history.page_size >= history.total} onClick={() => void loadHistory(history.page + 1)}>下一页</button></nav>}
    </section>
  </main>;
}

function rewardSummary(value: GiftCardReward) { if (value.purchase_snapshot) return purchaseSnapshotSummary(value.purchase_snapshot); const parts: string[] = []; if ((value.balance ?? 0) > 0) parts.push(`余额 ¥${((value.balance ?? 0) / 100).toFixed(2)}`); if ((value.transfer_enable ?? 0) > 0) parts.push(`流量 ${(Math.round((value.transfer_enable ?? 0) / 1_073_741.824) / 1000).toString()} GB`); if ((value.expire_days ?? 0) > 0) parts.push(`有效期 ${value.expire_days} 天`); if (value.plan_id != null) parts.push(`套餐 #${value.plan_id}（${value.plan_validity_days ?? 0} 天）`); if ((value.device_limit ?? 0) > 0) parts.push(`设备 +${value.device_limit}`); if (value.reset_package) parts.push("重置流量"); return parts.join(" · ") || "奖励将在兑换后生效"; }
function maskedCode(value: string) { return value.length <= 8 ? value : `${value.slice(0, 8)}****`; }

function trafficGB(value: number) { return `${Math.round(value / 1_073_741.824) / 1000} GB`; }
function expiry(value: string | null) { return value === null ? "不限时 / 无到期时间" : new Date(value).toLocaleString("zh-CN", { hour12: false }); }
