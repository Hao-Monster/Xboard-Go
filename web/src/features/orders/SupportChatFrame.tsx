import { useEffect, useRef, useState } from "react";
import { purchaseOrderKey, purchaseOrderMessage, sendPurchaseOrder, type PurchaseSupportOrder } from "./purchaseSupport";

const prefix = "chatwoot-widget:";
function validSession(value: unknown): value is string { return typeof value === "string" && value.length <= 8192 && /^[A-Za-z0-9_.=-]+$/.test(value); }

export function SupportChatFrame({ url, customerID, order }: { url: string; customerID: number; order?: PurchaseSupportOrder }) {
  const frame = useRef<HTMLIFrameElement>(null);
  const key = `purchase-support:${customerID}:${url}`;
  const [source] = useState(() => {
    const target = new URL(url);
    try { const saved = sessionStorage.getItem(key); if (validSession(saved)) target.searchParams.set("cw_conversation", saved); } catch { /* Storage can be disabled by the browser. */ }
    return target.href;
  });
  const [resumeURL, setResumeURL] = useState(source);
  const [ready, setReady] = useState(false);
  const [slow, setSlow] = useState(false);
  const [delivery, setDelivery] = useState<"idle" | "sending" | "sent" | "failed">("idle");
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [connection, setConnection] = useState(0);
  const content = order ? purchaseOrderMessage(order) : "";
  const tradeNo = order?.tradeNo;
  useEffect(() => {
    const origin = new URL(url).origin;
    const receive = (event: MessageEvent) => {
      if (event.origin !== origin || event.source !== frame.current?.contentWindow || typeof event.data !== "string" || !event.data.startsWith(prefix) || event.data.length > 32768) return;
      try {
        const data: unknown = JSON.parse(event.data.slice(prefix.length));
        if (!data || typeof data !== "object" || !("event" in data)) return;
        let token: unknown;
        if (data.event === "loaded" && "config" in data && data.config && typeof data.config === "object" && "authToken" in data.config) {
          token = data.config.authToken;
          frame.current?.contentWindow?.postMessage(`${prefix}${JSON.stringify({ event: "config-set", locale: "zh_CN", position: "right", hideMessageBubble: true, showPopoutButton: false, darkMode: "light", directConversation: true })}`, origin);
          frame.current?.contentWindow?.postMessage(`${prefix}${JSON.stringify({ event: "toggle-open", isOpen: true })}`, origin);
          setReady(true);
          setConnection(value => value + 1);
        }
        if (data.event === "setAuthCookie" && "data" in data && data.data && typeof data.data === "object" && "widgetAuthToken" in data.data) token = data.data.widgetAuthToken;
        // Keep the widget's own session scoped to this panel user and browser tab.
        // Never treat a widget message as proof of payment or user identity.
        if (validSession(token)) { const resume = new URL(url); resume.searchParams.set("cw_conversation", token); setResumeURL(resume.href); try { sessionStorage.setItem(key, token); } catch { /* The open chat still works without persistence. */ } }
      } catch { /* Ignore malformed messages from the third-party frame. */ }
    };
    window.addEventListener("message", receive);
    const timer = setTimeout(() => setSlow(true), 15000);
    return () => { window.removeEventListener("message", receive); clearTimeout(timer); };
  }, [key, url]);
  useEffect(() => {
    if (!ready || !tradeNo || !content || !frame.current?.contentWindow) return;
    const controller = new AbortController();
    const target = frame.current.contentWindow;
    // Only the identity fields enter the key; content is immutable for this run.
    const identity = { tradeNo, customerID };
    void (async () => {
      setDelivery("sending");
      setError("");
      try {
        const requestKey = await purchaseOrderKey(identity, window.location.origin);
        if (controller.signal.aborted) return;
        await sendPurchaseOrder(target, new URL(url).origin, requestKey, content, controller.signal);
        if (!controller.signal.aborted) setDelivery("sent");
      } catch (cause) {
        if (!controller.signal.aborted) {
          setDelivery("failed");
          setError(cause instanceof Error ? cause.message : "客服暂不可用，请重试原订单。");
        }
      }
    })();
    return () => controller.abort();
  }, [ready, tradeNo, content, customerID, url, attempt, connection]);
  return <>
    {!ready && <p role="status">{slow ? "客服加载较慢，可尝试在新窗口打开。" : "正在加载客服…"}</p>}
    {order && delivery === "sending" && <p role="status">正在发送订单并等待微信二维码…</p>}
    {order && delivery === "sent" && <p role="status">订单已发送，微信二维码已显示在会话中。</p>}
    {order && delivery === "failed" && <div role="alert">{error}<button type="button" className="button secondary compact" onClick={() => setAttempt(value => value + 1)}>重试原订单</button></div>}
    <iframe ref={frame} title="在线客服聊天" src={source} referrerPolicy="no-referrer" sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-downloads" onError={() => setSlow(true)} />
    <a href={resumeURL} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">无法显示？在新窗口打开客服</a>
  </>;
}
