export interface PurchaseSupportOrder {
  tradeNo: string;
  customerID: number;
  planName: string;
  period: string;
  amount: number;
}

function validateOrder(order: PurchaseSupportOrder): void {
  if (!Number.isSafeInteger(order.customerID) || order.customerID <= 0 ||
      !/^[A-Za-z0-9_-]{1,100}$/.test(order.tradeNo) ||
      !Number.isSafeInteger(order.amount) || order.amount <= 0 ||
      !order.planName.trim() || order.planName.length > 1000 || !order.period.trim()) {
    throw new Error("订单信息不完整，请重新打开订单详情。");
  }
}

export function purchaseOrderMessage(order: PurchaseSupportOrder): string {
  validateOrder(order);
  const line = (value: string) => Array.from(value, character => {
    const code = character.charCodeAt(0);
    return code < 32 || code === 127 ? " " : character;
  }).join("");
  const amount = `${Math.trunc(order.amount / 100)}.${String(order.amount % 100).padStart(2, "0")}`;
  return `你好，我想购买以下订单，请协助处理。\n订单号：${order.tradeNo}\n套餐：${line(order.planName)}\n周期：${line(order.period)}\n待付金额：¥${amount}\n以上为购买咨询信息，付款及开通结果以后台核验为准。`;
}

export async function purchaseOrderKey(order: Pick<PurchaseSupportOrder, "tradeNo" | "customerID">, origin: string): Promise<string> {
  if (!Number.isSafeInteger(order.customerID) || order.customerID <= 0 || !/^[A-Za-z0-9_-]{1,100}$/.test(order.tradeNo)) {
    throw new Error("订单身份不完整，请重新打开订单详情。");
  }
  // Version 8 UUID from an origin-scoped order identity. Mutable display fields
  // must not generate another delivery key after an ambiguous network result.
  const input = JSON.stringify(["xboard-purchase-v1", new URL(origin).origin, order.customerID, order.tradeNo]);
  const bytes = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(input))).slice(0, 16);
  bytes[6] = (bytes[6]! & 15) | 128;
  bytes[8] = (bytes[8]! & 63) | 128;
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

export const purchaseHandoffPrefix = "freedompost-handoff:";

export function sendPurchaseOrder(frame: Window, origin: string, requestKey: string, content: string, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const requestId = crypto.randomUUID();
    const timeoutMs = 25_000;
    let finished = false;
    const post = (value: object) => frame.postMessage(`${purchaseHandoffPrefix}${JSON.stringify(value)}`, origin);
    const finish = (error?: Error) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      window.removeEventListener("message", receive);
      signal.removeEventListener("abort", abort);
      if (error) {
        post({ version: 1, requestId, action: "cancel" });
        reject(error);
      } else resolve();
    };
    const abort = () => finish(new Error("客服已收起，可重新打开恢复原订单。"));
    const receive = (event: MessageEvent) => {
      if (event.source !== frame || event.origin !== origin || typeof event.data !== "string" || event.data.length > 8192 || !event.data.startsWith(purchaseHandoffPrefix)) return;
      let data: unknown;
      try { data = JSON.parse(event.data.slice(purchaseHandoffPrefix.length)); } catch { return; }
      if (!data || typeof data !== "object" || !("requestId" in data) || data.requestId !== requestId) return;
      const value = data as Record<string, unknown>;
      if (value.version !== 1 || value.action !== "purchase") return;
      if (value.ok !== true) {
        finish(new Error(value.code === "HANDOFF_CONFLICT" ? "此订单已绑定其他客服会话或信息发生变化，请回到原会话联系人工客服。" : "订单及二维码尚未确认送达，请重试原订单或联系人工客服。"));
        return;
      }
      if (value.requestKey !== requestKey || typeof value.bindingId !== "string" || !/^[a-f0-9]{64}$/.test(value.bindingId) ||
          typeof value.generation !== "string" || !value.generation ||
          !Number.isSafeInteger(value.messageId) || Number(value.messageId) <= 0 ||
          !Number.isSafeInteger(value.qrMessageId) || Number(value.qrMessageId) <= 0 || value.messageId === value.qrMessageId || value.rendered !== true) {
        finish(new Error("客服返回的订单回执无效，请重试原订单。"));
        return;
      }
      finish();
    };
    const timer = setTimeout(() => finish(new Error("客服响应超时，订单可能已送达；重试会恢复原订单，不重复发送。")), timeoutMs + 1000);
    window.addEventListener("message", receive);
    signal.addEventListener("abort", abort, { once: true });
    if (signal.aborted) { abort(); return; }
    post({ version: 1, action: "purchase", requestId, requestKey, content, timeoutMs });
  });
}
