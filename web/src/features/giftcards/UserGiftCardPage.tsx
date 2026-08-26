import { useCallback, useEffect, useState, type FormEvent } from "react";

import type { GiftCardPreview, GiftCardRedeemResult, GiftCardReward, GiftCardUsagePage } from "../../lib/api";

export interface UserGiftCardAPI {
  checkGiftCard: (code: string) => Promise<GiftCardPreview>;
  redeemGiftCard: (code: string) => Promise<GiftCardRedeemResult>;
  listMyGiftCardUsages: (page?: number, pageSize?: number) => Promise<GiftCardUsagePage>;
}

export function UserGiftCardPage({ api }: { api: UserGiftCardAPI }) {
  const [code, setCode] = useState(""); const [preview, setPreview] = useState<GiftCardPreview | null>(null);
  const [history, setHistory] = useState<GiftCardUsagePage>({ items: [], total: 0, page: 1, page_size: 15 });
  const [checking, setChecking] = useState(false); const [redeeming, setRedeeming] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");
  const [historyError, setHistoryError] = useState("");
  const loadHistory = useCallback(async () => {
    try { const result = await api.listMyGiftCardUsages(1, 15); setHistory(result); setHistoryError(""); }
    catch (cause) { setHistoryError(cause instanceof Error ? cause.message : "兑换记录加载失败"); }
  }, [api]);
  useEffect(() => {
    let active = true;
    void api.listMyGiftCardUsages(1, 15).then((result) => { if (active) setHistory(result); })
      .catch((cause: unknown) => { if (active) setHistoryError(cause instanceof Error ? cause.message : "兑换记录加载失败"); });
    return () => { active = false; };
  }, [api]);
  const check = async (event: FormEvent) => { event.preventDefault(); setChecking(true); setError(""); setMessage(""); try { setPreview(await api.checkGiftCard(code.trim().toUpperCase())); } catch (cause) { setPreview(null); setError(cause instanceof Error ? cause.message : "查询失败"); } finally { setChecking(false); } };
  const redeem = async () => { if (preview === null || !preview.can_redeem) return; setRedeeming(true); setError(""); setHistoryError(""); try { const result = await api.redeemGiftCard(code.trim().toUpperCase()); setMessage(`${result.message} ${rewardSummary(result.rewards)}`); setPreview(null); setCode(""); await loadHistory(); } catch (cause) { setError(cause instanceof Error ? cause.message : "兑换失败"); } finally { setRedeeming(false); } };
  return <main className="content user-gift-card"><header className="page-heading"><div><p className="eyebrow">Gift card</p><h1>礼品卡兑换</h1><p className="muted">输入兑换码，确认奖励和使用条件后再兑换。</p></div></header>
    <section className="panel"><h2>兑换礼品卡</h2><form className="coupon-check-row" onSubmit={(event) => void check(event)}><input aria-label="礼品卡兑换码" autoComplete="off" minLength={8} maxLength={32} pattern="[A-Za-z0-9]+" value={code} placeholder="请输入兑换码" required onChange={(event) => { setCode(event.target.value.toUpperCase()); setPreview(null); setMessage(""); }} /><button className="button secondary" disabled={checking || code.trim() === ""}>{checking ? "查询中…" : "查询奖励"}</button></form>
      {preview !== null && <div className="gift-preview" role="status"><h3>{preview.template.name}</h3><p>{rewardSummary(preview.reward_preview)}</p>{preview.can_redeem ? <button className="button primary" disabled={redeeming} onClick={() => void redeem()}>{redeeming ? "正在兑换…" : "确认兑换"}</button> : <div className="alert error" role="alert">{preview.reason}</div>}</div>}
      {error !== "" && <div className="alert error" role="alert">{error}</div>}{message !== "" && <div className="alert success" role="status">{message}</div>}
    </section>
    <section className="panel"><h2>兑换记录</h2>{historyError !== "" ? <div className="alert error" role="alert">{historyError}</div> : history.items.length === 0 ? <div className="empty-state">暂无兑换记录</div> : <div className="table-wrap"><table><thead><tr><th>模板</th><th>兑换码</th><th>奖励</th><th>兑换时间</th></tr></thead><tbody>{history.items.map((item) => <tr key={item.id}><td>{item.template_name}</td><td><code>{maskedCode(item.code ?? "")}</code></td><td>{rewardSummary(item.rewards)}</td><td>{new Date(item.used_at).toLocaleString("zh-CN", { hour12: false })}</td></tr>)}</tbody></table></div>}</section>
  </main>;
}

function rewardSummary(value: GiftCardReward) { const parts: string[] = []; if ((value.balance ?? 0) > 0) parts.push(`余额 ¥${((value.balance ?? 0) / 100).toFixed(2)}`); if ((value.transfer_enable ?? 0) > 0) parts.push(`流量 ${(Math.round((value.transfer_enable ?? 0) / 1_073_741.824) / 1000).toString()} GB`); if ((value.expire_days ?? 0) > 0) parts.push(`有效期 ${value.expire_days} 天`); if (value.plan_id != null) parts.push(`套餐 #${value.plan_id}（${value.plan_validity_days ?? 0} 天）`); if ((value.device_limit ?? 0) > 0) parts.push(`设备 +${value.device_limit}`); if (value.reset_package) parts.push("重置流量"); return parts.join(" · ") || "奖励将在兑换后生效"; }
function maskedCode(value: string) { return value.length <= 8 ? value : `${value.slice(0, 8)}****`; }
