import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Coupon, Plan } from "../../lib/api";
import { CouponManagementPage } from "./CouponManagementPage";

const coupon: Coupon = {
  id: 9, code: "SAVE500", name: "新用户优惠", type: 1, value: 500, show: true,
  limit_use: 3, limit_use_with_user: 1, limit_plan_ids: [], limit_period: [],
  started_at: "2026-08-25T00:00:00Z", ended_at: "2026-09-25T00:00:00Z",
  created_at: "2026-08-25T00:00:00Z", updated_at: "2026-08-25T00:00:00Z"
};
const plan = { id: 1, name: "标准套餐", prices: { monthly: 1_000 } } as Plan;

describe("CouponManagementPage", () => {
  it("lists, filters, creates, and toggles coupons with old-Xboard business fields", async () => {
    const api = {
      listCoupons: vi.fn().mockResolvedValue({ items: [coupon], total: 1, page: 1, page_size: 20 }),
      listPlans: vi.fn().mockResolvedValue([plan]),
      createCoupon: vi.fn().mockResolvedValue(coupon), updateCoupon: vi.fn(),
      setCouponVisibility: vi.fn().mockResolvedValue({ ...coupon, show: false }),
      deleteCoupon: vi.fn(), createCouponBatch: vi.fn()
    };
    const user = userEvent.setup();
    render(<CouponManagementPage api={api} />);

    expect(await screen.findByText("SAVE500")).toBeVisible();
    await user.type(screen.getByPlaceholderText("搜索名称或券码"), "SAVE");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listCoupons).toHaveBeenLastCalledWith(expect.objectContaining({ query: "SAVE", page: 1 })));

    await user.click(screen.getByRole("button", { name: "新增优惠券" }));
    await user.type(screen.getByLabelText("卷名称"), "新用户优惠");
    await user.type(screen.getByLabelText("卷码"), "SAVE500");
    await user.clear(screen.getByLabelText("优惠金额（CNY）"));
    await user.type(screen.getByLabelText("优惠金额（CNY）"), "5");
    await user.click(screen.getByRole("button", { name: "保存优惠券" }));
    await waitFor(() => expect(api.createCoupon).toHaveBeenCalledWith(expect.objectContaining({
      code: "SAVE500", name: "新用户优惠", type: 1, value: 500, show: true
    })));

    await user.click(screen.getByRole("button", { name: "禁用 SAVE500" }));
    await waitFor(() => expect(api.setCouponVisibility).toHaveBeenCalledWith(coupon.id, false));
  });

  it("edits, paginates, and deletes coupons through observable administrator flows", async () => {
    const updated = { ...coupon, name: "续费优惠" };
    const api = {
      listCoupons: vi.fn().mockResolvedValue({ items: [coupon], total: 21, page: 1, page_size: 20 }),
      listPlans: vi.fn().mockResolvedValue([plan]),
      createCoupon: vi.fn(), updateCoupon: vi.fn().mockResolvedValue(updated),
      setCouponVisibility: vi.fn(), deleteCoupon: vi.fn().mockResolvedValue(undefined), createCouponBatch: vi.fn()
    };
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    render(<CouponManagementPage api={api} />);

    expect(await screen.findByText("SAVE500")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "编辑" }));
    await user.clear(screen.getByLabelText("卷名称"));
    await user.type(screen.getByLabelText("卷名称"), updated.name);
    await user.click(screen.getByRole("button", { name: "保存优惠券" }));
    await waitFor(() => expect(api.updateCoupon).toHaveBeenCalledWith(coupon.id, expect.objectContaining({
      code: coupon.code, name: updated.name, type: coupon.type, value: coupon.value
    })));

    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.listCoupons).toHaveBeenLastCalledWith(expect.objectContaining({ page: 2, page_size: 20 })));

    await user.click(screen.getByRole("button", { name: "删除" }));
    await waitFor(() => expect(api.deleteCoupon).toHaveBeenCalledWith(coupon.id));
    expect(confirm).toHaveBeenCalledWith(`确认删除优惠券 ${coupon.code}？`);
    confirm.mockRestore();
  });
});
