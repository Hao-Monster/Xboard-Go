/// <reference types="node" />
import { webcrypto } from "node:crypto";
import { afterEach, expect, it, vi } from "vitest";
import { purchaseOrderKey, purchaseOrderMessage, purchaseHandoffPrefix, sendPurchaseOrder } from "./purchaseSupport";

const order = { tradeNo: "QA-20261015-001", customerID: 12, planName: "测试套餐", period: "月付", amount: 15001 };
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.useRealTimers(); document.body.replaceChildren(); });

it("uses the displayed order context without claiming payment or including credentials", () => {
  expect(purchaseOrderMessage(order)).toBe("你好，我想购买以下订单，请协助处理。\n订单号：QA-20261015-001\n套餐：测试套餐\n周期：月付\n待付金额：¥150.01\n以上为购买咨询信息，付款及开通结果以后台核验为准。");
});

it("keeps one stable key per panel/customer/order across retries but isolates new orders and panels", async () => {
  vi.stubGlobal("crypto", webcrypto);
  const key = await purchaseOrderKey(order, "https://panel.example.test");
  expect(key).toMatch(/^[a-f0-9]{8}-[a-f0-9]{4}-8[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/);
  const changedPrice = { ...order, amount: 20000 };
  expect(await purchaseOrderKey(changedPrice, "https://panel.example.test")).toBe(key);
  for (const different of [{ ...order, tradeNo: "QA-002" }, { ...order, customerID: 13 }]) {
    expect(await purchaseOrderKey(different, "https://panel.example.test")).not.toBe(key);
  }
  expect(await purchaseOrderKey(order, "https://other.example.test")).not.toBe(key);
});

function beginDelivery() {
  vi.stubGlobal("crypto", webcrypto);
  const iframe = document.createElement("iframe");
  document.body.appendChild(iframe);
  const frame = iframe.contentWindow!;
  const post = vi.spyOn(frame, "postMessage");
  const controller = new AbortController();
  const key = "12345678-1234-8123-8123-123456789abc";
  const promise = sendPurchaseOrder(frame, "https://chat.example.test", key, "QA order", controller.signal);
  const request = JSON.parse(String(post.mock.calls[0]?.[0]).slice(purchaseHandoffPrefix.length)) as { requestId: string };
  const receipt = { version: 1, action: "purchase", requestId: request.requestId, requestKey: key, ok: true,
    bindingId: "a".repeat(64), generation: "generation:1", messageId: 101, qrMessageId: 102, rendered: true };
  const reply = (value: object = receipt, origin = "https://chat.example.test", source: Window = frame) => {
    window.dispatchEvent(new MessageEvent("message", { origin, source, data: `${purchaseHandoffPrefix}${JSON.stringify(value)}` }));
  };
  return { promise, reply, receipt, controller, post };
}

it("only accepts the matching frame/origin/request and a rendered order plus QR receipt", async () => {
  const task = beginDelivery();
  const done = vi.fn(); void task.promise.then(done);
  task.reply(task.receipt, "https://evil.example.test");
  task.reply(task.receipt, "https://chat.example.test", window);
  task.reply({ ...task.receipt, requestId: "other" });
  await Promise.resolve(); expect(done).not.toHaveBeenCalled();
  task.reply(); await task.promise; expect(done).toHaveBeenCalledOnce();
});

it("does not report API success without the paired QR and visible rendering", async () => {
  const task = beginDelivery();
  const rejected = expect(task.promise).rejects.toThrow("回执无效");
  task.reply({ ...task.receipt, rendered: false });
  await rejected;
  expect(task.post).toHaveBeenLastCalledWith(expect.stringContaining('"action":"cancel"'), "https://chat.example.test");
});

it("cancels on close and does not reopen on a late success", async () => {
  const task = beginDelivery();
  const rejected = expect(task.promise).rejects.toThrow("已收起");
  task.controller.abort(); task.reply(); await rejected;
  expect(task.post.mock.calls).toHaveLength(2);
});

it("times out with an honest uncertain delivery state and no ordinary-message fallback", async () => {
  vi.useFakeTimers(); const task = beginDelivery();
  const rejected = expect(task.promise).rejects.toThrow("订单可能已送达");
  await vi.advanceTimersByTimeAsync(26_000); await rejected;
  expect(task.post.mock.calls).toHaveLength(2);
});

it("rejects missing identity and invalid money instead of sending ambiguous context", () => {
  for (const invalid of [{ ...order, customerID: 0 }, { ...order, tradeNo: "" }, { ...order, amount: -1 }, { ...order, amount: 1.5 }]) {
    expect(() => purchaseOrderMessage(invalid)).toThrow();
  }
});
