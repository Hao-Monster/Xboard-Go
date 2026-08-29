import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { CommissionSettings } from "../../lib/api";
import { CommissionSettingsPage } from "./CommissionSettingsPage";

const initial: CommissionSettings = {
  revision: 4,
  invite_commission: 20,
  commission_first_time_enable: true,
  commission_auto_check_enable: true,
  withdraw_close_enable: false,
  commission_distribution_enable: true,
  commission_distribution_l1: 50,
  commission_distribution_l2: 30,
  commission_distribution_l3: 20,
  updated_at: "2026-08-29T08:00:00Z"
};

describe("CommissionSettingsPage", () => {
  it("loads all eight settings, shows effective rates, and saves with the observed revision", async () => {
    const updated = {
      ...initial,
      revision: 5,
      invite_commission: 25,
      commission_first_time_enable: false,
      commission_auto_check_enable: false,
      withdraw_close_enable: true,
      commission_distribution_l1: 40,
      commission_distribution_l2: 35,
      commission_distribution_l3: 25
    };
    const api = {
      getCommissionSettings: vi.fn().mockResolvedValue(initial),
      updateCommissionSettings: vi.fn().mockResolvedValue(updated)
    };
    const user = userEvent.setup();
    render(<CommissionSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "邀请佣金设置" })).toBeVisible();
    expect(screen.getByText("Revision 4", { exact: true })).toBeVisible();
    expect(screen.getByLabelText("全局邀请佣金比例（%）")).toHaveValue(20);
    expect(screen.getByRole("checkbox", { name: "仅首次有效订单返佣" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "自动确认到期佣金" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "佣金直接计入账户余额" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "启用三级分佣" })).toBeChecked();
    expect(screen.getByText("当前用户侧有效比例：10% / 6% / 4%", { exact: true })).toBeVisible();

    await user.clear(screen.getByLabelText("全局邀请佣金比例（%）"));
    await user.type(screen.getByLabelText("全局邀请佣金比例（%）"), "25");
    await user.click(screen.getByRole("checkbox", { name: "仅首次有效订单返佣" }));
    await user.click(screen.getByRole("checkbox", { name: "自动确认到期佣金" }));
    await user.click(screen.getByRole("checkbox", { name: "佣金直接计入账户余额" }));
    for (const [label, value] of [["一级分佣比例（%）", "40"], ["二级分佣比例（%）", "35"], ["三级分佣比例（%）", "25"]] as const) {
      await user.clear(screen.getByLabelText(label));
      await user.type(screen.getByLabelText(label), value);
    }
    await user.click(screen.getByRole("button", { name: "保存佣金设置" }));
    await waitFor(() => expect(api.updateCommissionSettings).toHaveBeenCalledWith({
      revision: 4,
      invite_commission: 25,
      commission_first_time_enable: false,
      commission_auto_check_enable: false,
      withdraw_close_enable: true,
      commission_distribution_enable: true,
      commission_distribution_l1: 40,
      commission_distribution_l2: 35,
      commission_distribution_l3: 25
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("佣金设置已保存");
    expect(screen.getByText("Revision 5", { exact: true })).toBeVisible();
  });

  it("blocks unsafe totals locally, disables inactive levels, and supports conflict recovery", async () => {
    const inactive = { ...initial, commission_distribution_enable: false };
    const api = {
      getCommissionSettings: vi.fn().mockResolvedValue(inactive),
      updateCommissionSettings: vi.fn().mockRejectedValue(new Error("设置已被其他管理员修改，请刷新后重试"))
    };
    const user = userEvent.setup();
    render(<CommissionSettingsPage api={api} />);

    expect(await screen.findByLabelText("一级分佣比例（%）")).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "启用三级分佣" }));
    await user.clear(screen.getByLabelText("一级分佣比例（%）"));
    await user.type(screen.getByLabelText("一级分佣比例（%）"), "51");
    await user.click(screen.getByRole("button", { name: "保存佣金设置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("三级分佣比例合计不能超过 100%");
    expect(api.updateCommissionSettings).not.toHaveBeenCalled();

    await user.clear(screen.getByLabelText("一级分佣比例（%）"));
    await user.type(screen.getByLabelText("一级分佣比例（%）"), "50");
    await user.click(screen.getByRole("button", { name: "保存佣金设置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改");
    await user.click(screen.getByRole("button", { name: "刷新最新设置" }));
    await waitFor(() => expect(api.getCommissionSettings).toHaveBeenCalledTimes(2));
  });
});
