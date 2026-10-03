import type { GiftCardPurchaseSnapshot, Order } from "../../lib/api";

const periods = { monthly: "1 个月", quarterly: "3 个月", half_yearly: "6 个月", yearly: "1 年", two_yearly: "2 年", three_yearly: "3 年", onetime: "按流量（不限时）" };
export function purchaseSnapshotSummary(snapshot: GiftCardPurchaseSnapshot): string {
  return `${snapshot.plan_name} · ${Math.round(snapshot.transfer_enable / 1_073_741.824) / 1000} GB · ${periods[snapshot.period]}`;
}
export function GiftCardPurchaseDetails({ order }: { order: Order }) {
  if (order.source !== "gift_card_purchase") return null;
  return <section aria-label="兑换码购买信息"><h3>兑换码购买</h3><p>通过兑换码发放套餐权益，站内未收款；外部支付金额未核验。</p>{order.purchase_snapshot && <p>{purchaseSnapshotSummary(order.purchase_snapshot)}</p>}<p>兑换批次：{order.gift_card_batch_no || "—"}</p><p className="muted">退款需联系购买渠道人工核实，面板不自动回收已兑换权益。</p></section>;
}
