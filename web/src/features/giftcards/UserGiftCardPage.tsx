import { useCallback, useEffect, useState, type FormEvent } from "react";

import type { GiftCardPreview, GiftCardRedeemResult, GiftCardReward, GiftCardUsagePage } from "../../lib/api";
import "./UserGiftCardPage.css";

export interface UserGiftCardAPI {
  checkGiftCard: (code: string) => Promise<GiftCardPreview>;
  redeemGiftCard: (code: string) => Promise<GiftCardRedeemResult>;
  listMyGiftCardUsages: (page?: number, pageSize?: number) => Promise<GiftCardUsagePage>;
}

function IconGift() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="20 12 20 22 4 22 4 12" />
      <rect x="2" y="7" width="20" height="5" />
      <line x1="12" y1="22" x2="12" y2="7" />
      <path d="M12 7H7.5a2.5 2.5 0 0 1 0-5C11 2 12 7 12 7z" />
      <path d="M12 7h4.5a2.5 2.5 0 0 0 0-5C13 2 12 7 12 7z" />
    </svg>
  );
}

function IconHistory() {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </svg>
  );
}

function IconKey() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4" />
    </svg>
  );
}

function IconInfo() {
  return (
    <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="16" x2="12" y2="12" />
      <line x1="12" y1="8" x2="12.01" y2="8" />
    </svg>
  );
}

function IconEmptyBox() {
  return (
    <svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
      <path d="m3.3 7 8.7 5 8.7-5" />
      <path d="M12 22V12" />
    </svg>
  );
}

export function UserGiftCardPage({ api }: { api: UserGiftCardAPI }) {
  const [code, setCode] = useState("");
  const [preview, setPreview] = useState<GiftCardPreview | null>(null);
  const [history, setHistory] = useState<GiftCardUsagePage>({ items: [], total: 0, page: 1, page_size: 15 });
  const [checking, setChecking] = useState(false);
  const [redeeming, setRedeeming] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [historyError, setHistoryError] = useState("");

  const loadHistory = useCallback(async () => {
    try {
      const result = await api.listMyGiftCardUsages(1, 15);
      setHistory(result);
      setHistoryError("");
    } catch (cause) {
      setHistoryError(cause instanceof Error ? cause.message : "兑换记录加载失败");
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    void api.listMyGiftCardUsages(1, 15)
      .then((result) => {
        if (active) setHistory(result);
      })
      .catch((cause: unknown) => {
        if (active) setHistoryError(cause instanceof Error ? cause.message : "兑换记录加载失败");
      });
    return () => {
      active = false;
    };
  }, [api]);

  const check = async (event: FormEvent) => {
    event.preventDefault();
    setChecking(true);
    setError("");
    setMessage("");
    try {
      setPreview(await api.checkGiftCard(code.trim().toUpperCase()));
    } catch (cause) {
      setPreview(null);
      setError(cause instanceof Error ? cause.message : "查询失败");
    } finally {
      setChecking(false);
    }
  };

  const redeem = async () => {
    if (preview === null || !preview.can_redeem) return;
    setRedeeming(true);
    setError("");
    setHistoryError("");
    try {
      const result = await api.redeemGiftCard(code.trim().toUpperCase());
      setMessage(`${result.message} ${rewardSummary(result.rewards)}`);
      setPreview(null);
      setCode("");
      await loadHistory();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "兑换失败");
    } finally {
      setRedeeming(false);
    }
  };

  return (
    <main className="page-shell user-gift-card-page">
      <header className="page-header">
        <div>
          <span className="giftcard-eyebrow">
            <IconGift />
            Gift Card
          </span>
          <h1>礼品卡兑换</h1>
          <p className="muted">输入兑换码，确认奖励和使用条件后再兑换。</p>
        </div>
      </header>

      {/* Redeem Card */}
      <section className="giftcard-card" aria-labelledby="redeem-card-title">
        <div className="giftcard-card-header">
          <div className="giftcard-card-title-wrap">
            <span className="giftcard-card-icon" aria-hidden="true">
              <IconGift />
            </span>
            <div>
              <h2 id="redeem-card-title" className="giftcard-card-title">兑换礼品卡</h2>
              <p className="giftcard-card-subtitle">输入您获得的 8-32 位礼品卡卡密进行查询与兑换</p>
            </div>
          </div>
        </div>

        <form className="giftcard-redeem-form" onSubmit={(event) => void check(event)}>
          <div className="giftcard-input-group">
            <div className="giftcard-input-wrapper">
              <span className="giftcard-input-prefix-icon" aria-hidden="true">
                <IconKey />
              </span>
              <input
                className="giftcard-code-input"
                aria-label="礼品卡兑换码"
                autoComplete="off"
                minLength={8}
                maxLength={32}
                pattern="[A-Za-z0-9]+"
                value={code}
                placeholder="请输入礼品卡兑换码（如 GC1234ABCD）"
                required
                onChange={(event) => {
                  setCode(event.target.value.toUpperCase());
                  setPreview(null);
                  setMessage("");
                }}
              />
            </div>
            <button
              type="submit"
              className="button primary giftcard-check-btn"
              disabled={checking || code.trim() === ""}
            >
              {checking ? "查询中…" : "查询奖励"}
            </button>
          </div>
        </form>

        <div className="giftcard-tip-banner">
          <IconInfo />
          <span>使用提示：礼品卡仅限兑换一次，确认兑换后奖励将即刻生效并充入您的账号。</span>
        </div>

        {/* Live Preview Card */}
        {preview !== null && (
          <div className="gift-preview" role="status">
            <div className="gift-preview-header">
              <h3 className="gift-preview-title">
                {preview.template.name}
                {preview.can_redeem && <span className="gift-preview-status-badge">可兑换</span>}
              </h3>
            </div>
            <div className="gift-reward-chips">
              {rewardChips(preview.reward_preview).map((chip, idx) => (
                <span key={idx} className={`gift-reward-chip ${idx === 0 ? "highlight" : ""}`}>
                  {chip}
                </span>
              ))}
            </div>
            {preview.can_redeem ? (
              <div className="gift-preview-actions">
                <button
                  type="button"
                  className="button primary"
                  disabled={redeeming}
                  onClick={() => void redeem()}
                >
                  {redeeming ? "正在兑换…" : "确认兑换"}
                </button>
              </div>
            ) : (
              <div className="alert error" role="alert">
                {preview.reason}
              </div>
            )}
          </div>
        )}

        {error !== "" && <div className="alert error" role="alert" style={{ marginTop: 16 }}>{error}</div>}
        {message !== "" && <div className="alert success" role="status" style={{ marginTop: 16 }}>{message}</div>}
      </section>

      {/* History Card */}
      <section className="giftcard-card" aria-labelledby="history-card-title">
        <div className="giftcard-card-header">
          <div className="giftcard-card-title-wrap">
            <span className="giftcard-card-icon" aria-hidden="true">
              <IconHistory />
            </span>
            <div>
              <h2 id="history-card-title" className="giftcard-card-title">兑换记录</h2>
              <p className="giftcard-card-subtitle">查看历史已成功兑换的礼品卡明细</p>
            </div>
          </div>
          {history.total > 0 && (
            <span className="giftcard-history-count">{history.total} 条记录</span>
          )}
        </div>

        {historyError !== "" ? (
          <div className="alert error" role="alert">{historyError}</div>
        ) : history.items.length === 0 ? (
          <div className="giftcard-empty-state">
            <div className="giftcard-empty-icon" aria-hidden="true">
              <IconEmptyBox />
            </div>
            <div className="giftcard-empty-title">暂无兑换记录</div>
            <p className="giftcard-empty-hint">您兑换过的礼品卡和生效的奖励明细将展示在这里</p>
          </div>
        ) : (
          <div className="giftcard-table-wrapper">
            <table className="giftcard-history-table">
              <thead>
                <tr>
                  <th>模板</th>
                  <th>兑换码</th>
                  <th>奖励</th>
                  <th>兑换时间</th>
                </tr>
              </thead>
              <tbody>
                {history.items.map((item) => (
                  <tr key={item.id}>
                    <td><strong>{item.template_name}</strong></td>
                    <td>
                      <code className="giftcard-history-code">{maskedCode(item.code ?? "")}</code>
                    </td>
                    <td>
                      <span className="giftcard-history-reward-pill">
                        {rewardSummary(item.rewards)}
                      </span>
                    </td>
                    <td>
                      <span className="giftcard-history-time">
                        {new Date(item.used_at).toLocaleString("zh-CN", { hour12: false })}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </main>
  );
}

function rewardChips(value: GiftCardReward): string[] {
  const chips: string[] = [];
  if ((value.balance ?? 0) > 0) chips.push(`余额 ¥${((value.balance ?? 0) / 100).toFixed(2)}`);
  if ((value.transfer_enable ?? 0) > 0) chips.push(`流量 ${(Math.round((value.transfer_enable ?? 0) / 1_073_741.824) / 1000).toString()} GB`);
  if ((value.expire_days ?? 0) > 0) chips.push(`有效期 ${value.expire_days} 天`);
  if (value.plan_id != null) chips.push(`套餐 #${value.plan_id}（${value.plan_validity_days ?? 0} 天）`);
  if ((value.device_limit ?? 0) > 0) chips.push(`设备 +${value.device_limit}`);
  if (value.reset_package) chips.push("重置流量");
  return chips.length > 0 ? chips : ["奖励将在兑换后生效"];
}

function rewardSummary(value: GiftCardReward) {
  return rewardChips(value).join(" · ");
}

function maskedCode(value: string) {
  return value.length <= 8 ? value : `${value.slice(0, 8)}****`;
}
