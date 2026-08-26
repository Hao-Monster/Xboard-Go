import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { CouponQuote, Order, PlanOffer } from "../../lib/api";
import { PlanCatalogPage } from "./PlanCatalogPage";

const plan: PlanOffer = {
  id: 7, group_id: 2, transfer_enable: 100, name: "标准套餐", speed_limit: null, content: "",
  reset_traffic_method: null, capacity_limit: null, prices: { monthly: 100_000 }, device_limit: null,
  tags: [], show: true, sell: true, renew: true, revision: 1, created_at: "2026-08-26T00:00:00Z",
  updated_at: "2026-08-26T00:00:00Z", sort: 1, capacity_remaining: null, can_purchase: true, can_renew: false
};
const quote: CouponQuote = {
  coupon: {
    id: 3, code: "FIXED123", name: "固定优惠", type: 1, value: 1_234, show: true,
    limit_use: 5, limit_use_with_user: 1, limit_plan_ids: [7], limit_period: ["monthly"],
    started_at: "2026-08-25T00:00:00Z", ended_at: "2026-09-25T00:00:00Z",
    created_at: "2026-08-25T00:00:00Z", updated_at: "2026-08-25T00:00:00Z"
  },
  original_amount: 100_000, coupon_discount_amount: 1_234, total_after_coupon: 98_766
};
const order = { id: 11, trade_no: "202608260001" } as Order;

describe("PlanCatalogPage coupons", () => {
  it("verifies the coupon, shows the exact discount, and submits only the verified code", async () => {
    const api = {
      listPlanOffers: vi.fn().mockResolvedValue([plan]),
      checkCoupon: vi.fn().mockResolvedValue(quote),
      createOrder: vi.fn().mockResolvedValue(order)
    };
    const user = userEvent.setup();
    render(<PlanCatalogPage api={api} couponEnabled onOrderCreated={vi.fn()} />);

    await user.click(await screen.findByRole("button", { name: "立即订阅" }));
    await user.type(screen.getByPlaceholderText("有优惠券？"), "FIXED123");
    await user.click(screen.getByRole("button", { name: "验证" }));

    await waitFor(() => expect(api.checkCoupon).toHaveBeenCalledWith("FIXED123", plan.id, "monthly"));
    expect(screen.getByText("-¥12.34")).toBeVisible();
    expect(screen.getByText("¥987.66")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "下单" }));
    await waitFor(() => expect(api.createOrder).toHaveBeenCalledWith(plan.id, "monthly", "FIXED123"));
  });

  it("does not expose coupon controls when the global setting is disabled", async () => {
    const api = { listPlanOffers: vi.fn().mockResolvedValue([plan]), checkCoupon: vi.fn(), createOrder: vi.fn() };
    const user = userEvent.setup();
    render(<PlanCatalogPage api={api} couponEnabled={false} />);
    await user.click(await screen.findByRole("button", { name: "立即订阅" }));
    expect(screen.queryByPlaceholderText("有优惠券？")).not.toBeInTheDocument();
  });

  it("discards an in-flight quote when the selected period changes", async () => {
    let resolveQuote: ((value: CouponQuote) => void) | undefined;
    const api = {
      listPlanOffers: vi.fn().mockResolvedValue([{ ...plan, prices: { monthly: 100_000, yearly: 900_000 } }]),
      checkCoupon: vi.fn().mockImplementation(() => new Promise<CouponQuote>((resolve) => { resolveQuote = resolve; })),
      createOrder: vi.fn()
    };
    const user = userEvent.setup();
    render(<PlanCatalogPage api={api} couponEnabled />);

    await user.click(await screen.findByRole("button", { name: "立即订阅" }));
    await user.type(screen.getByPlaceholderText("有优惠券？"), "FIXED123");
    await user.click(screen.getByRole("button", { name: "验证" }));
    await user.selectOptions(screen.getByLabelText("付款周期"), "yearly");
    resolveQuote?.(quote);

    await waitFor(() => expect(screen.getByRole("button", { name: "验证" })).toBeEnabled());
    expect(screen.queryByText("-¥12.34")).not.toBeInTheDocument();
    expect(api.createOrder).not.toHaveBeenCalled();
  });
});
