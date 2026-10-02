import { useEffect, useState } from "react";
import type { PurchaseChannels } from "../../lib/api";
import "./PurchaseAlternatives.css";
import { SupportChatFrame } from "./SupportChatFrame";

export function safePurchaseURL(value: string): string | null {
  try { const url = new URL(value); return url.protocol === "https:" && !url.username && !url.password && !url.hash ? url.href : null; }
  catch { return null; }
}

export function chatwootWidgetURL(channels: PurchaseChannels): string | null {
  const origin = safePurchaseURL(channels.chatwoot_base_url);
  if (!origin || !/^[A-Za-z0-9_-]{1,128}$/.test(channels.chatwoot_website_token)) return null;
  const base = new URL(origin);
  if (base.pathname !== "/" || base.search) return null;
  const url = new URL("/widget", base.origin);
  url.searchParams.set("website_token", channels.chatwoot_website_token);
  url.searchParams.set("locale", "zh_CN");
  return url.href;
}

export function PurchaseAlternatives({ load, onRedeem, customerID = 0 }: { load: () => Promise<PurchaseChannels>; onRedeem?: () => void; customerID?: number }) {
  const [channels, setChannels] = useState<PurchaseChannels | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [chat, setChat] = useState(false);
  useEffect(() => {
    let active = true;
    void load().then(value => { if (active) setChannels(value); })
      .catch((cause: unknown) => { if (active) setError(cause instanceof Error ? cause.message : "购买渠道加载失败"); });
    return () => { active = false; };
  }, [load, attempt]);
  const storeURL = channels ? safePurchaseURL(channels.card_store_url) : null;
  const widgetURL = channels ? chatwootWidgetURL(channels) : null;
  return <section className="purchase-alternatives" aria-label="其他购买方式">
    <h3>使用兑换码购买</h3>
    {!channels && !error && <p role="status">正在加载购买渠道…</p>}
    {error && <div className="alert error" role="alert">{error}<button type="button" className="button ghost compact" onClick={() => { setError(""); setAttempt(value => value + 1); }}>重试购买渠道</button></div>}
    {channels && <>
      <p role="status">{storeURL && widgetURL ? "当前暂未提供在线支付。你可以前往发卡网购买兑换码，或联系在线客服购买，取得兑换码后回来兑换开通。" : storeURL ? "当前暂未提供在线支付。你可以前往发卡网购买兑换码，取得兑换码后回来兑换开通。" : widgetURL ? "当前暂未提供在线支付。你可以联系在线客服购买兑换码，取得兑换码后回来兑换开通。" : "当前暂未开放购买渠道。如已持有兑换码，可以直接前往兑换。"}</p>
      <div className="purchase-channel-cards">
        {storeURL && <article><h4>发卡网购买</h4><p>选择与本订单套餐、周期对应的商品，付款后领取兑换码。</p><a className="button primary" href={storeURL} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">前往发卡网购买</a></article>}
        {widgetURL && <article><h4>在线客服购买</h4><p>告知客服套餐和周期。客服核实到账后发送兑换码。</p><button type="button" className="button secondary" disabled={chat} onClick={() => setChat(true)}>打开客服购买</button></article>}
      </div>
      <p className="muted">外部购买以发卡网或客服确认为准。本订单不会自动标为已付款；如改用兑换码，请关闭此待支付订单，避免重复付款，订单中已抵扣的余额按原规则退回。</p>
    </>}
    {onRedeem && <button type="button" className="button secondary" onClick={onRedeem}>已有兑换码，去兑换</button>}
    {chat && widgetURL && <section className="purchase-support" aria-label="在线客服购买">
      <div className="purchase-support-heading"><h4>在线客服</h4><button type="button" className="button ghost compact" onClick={() => setChat(false)}>收起客服</button></div>
      <SupportChatFrame key={`${customerID}:${widgetURL}`} url={widgetURL} customerID={customerID} />
    </section>}
  </section>;
}
