import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { PaymentMethod, PaymentProviderDefinition } from "../../lib/api";
import { PaymentManagementPage } from "./PaymentManagementPage";

const definitions: PaymentProviderDefinition[] = [{
  provider: "EPay", label: "易支付", fields: [
    { key: "url", label: "支付网关地址", type: "url", required: true, secret: false },
    { key: "pid", label: "商户ID", type: "text", required: true, secret: false },
    { key: "key", label: "通信密钥", type: "password", required: true, secret: true },
    { key: "type", label: "支付类型", type: "text", required: false, secret: false }
  ]
}];

const method: PaymentMethod = {
  id: 7, uuid: "Payment1", payment: "EPay", name: "易支付主通道", icon: "", notify_domain: "",
  handling_fee_fixed: 123, handling_fee_basis_points: 250, enable: true, sort: 1, revision: 2,
  created_at: "2026-08-26T00:00:00Z", updated_at: "2026-08-26T00:00:00Z",
  config: { url: "https://epay.example.test", pid: "1001", type: "alipay" }, configured_fields: ["key"],
  notify_url: "https://panel.example.test/api/v1/guest/payment/notify/EPay/Payment1"
};

describe("PaymentManagementPage", () => {
  it("renders the locked provider surface and creates an integer-fee encrypted configuration request", async () => {
    const created = { ...method, id: 8, uuid: "Payment2", name: "新易支付", revision: 1 };
    const api = createAPI([], created);
    const user = userEvent.setup();
    render(<PaymentManagementPage api={api} />);

    expect(await screen.findByRole("heading", { name: "支付配置" })).toBeVisible();
		expect(screen.getByRole("columnheader", { name: "ID" })).toBeVisible();
		expect(screen.getByRole("columnheader", { name: "启用" })).toBeVisible();
		expect(screen.getByText("暂无数据。添加支付方式后，用户才能为付费订单结算。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加支付方式" }));
    const dialog = screen.getByRole("dialog", { name: "添加支付方式" });
    await user.type(within(dialog).getByLabelText("显示名称"), "新易支付");
    await user.type(within(dialog).getByLabelText("支付网关地址"), "https://epay.example.test");
    await user.type(within(dialog).getByLabelText("商户ID"), "1001");
    await user.type(within(dialog).getByLabelText("通信密钥"), "secret-one");
    await user.type(within(dialog).getByLabelText("支付类型"), "alipay");
    await user.clear(within(dialog).getByLabelText("百分比手续费（%）"));
    await user.type(within(dialog).getByLabelText("百分比手续费（%）"), "2.5");
    await user.clear(within(dialog).getByLabelText("固定手续费（分）"));
    await user.type(within(dialog).getByLabelText("固定手续费（分）"), "123");
    await user.click(within(dialog).getByLabelText("保存后立即启用"));
    await user.click(within(dialog).getByRole("button", { name: "确认" }));

    await waitFor(() => expect(api.createPayment).toHaveBeenCalledWith(expect.objectContaining({
      payment: "EPay", name: "新易支付", handling_fee_fixed: 123, handling_fee_basis_points: 250, enable: true,
      config: { url: "https://epay.example.test", pid: "1001", key: "secret-one", type: "alipay" }
    })));
    expect(await screen.findByText("新易支付")).toBeVisible();
  });

  it("does not expose a stored secret, preserves it when blank, and replaces it only when explicitly entered", async () => {
    const updated = { ...method, revision: 3 };
    const api = createAPI([method], updated);
    const user = userEvent.setup();
    render(<PaymentManagementPage api={api} />);

    expect(await screen.findByText(method.name)).toBeVisible();
    expect(screen.queryByDisplayValue("secret-one")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑" }));
    let dialog = screen.getByRole("dialog", { name: "编辑支付方式" });
    expect(within(dialog).getByLabelText("通信密钥")).toHaveAttribute("placeholder", "已安全保存；留空保持不变");
    await user.click(within(dialog).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.updatePayment).toHaveBeenLastCalledWith(method.id, expect.objectContaining({ config: method.config, clear_config_fields: [] })));

    await user.click(screen.getByRole("button", { name: "编辑" }));
    dialog = screen.getByRole("dialog", { name: "编辑支付方式" });
		await user.type(within(dialog).getByLabelText("通信密钥"), "replacement-secret");
    await user.click(within(dialog).getByRole("button", { name: "确认" }));
    await waitFor(() => expect(api.updatePayment).toHaveBeenLastCalledWith(method.id, expect.objectContaining({ config: { ...method.config, key: "replacement-secret" }, clear_config_fields: [] })));
  });

  it("toggles visibility and sends the full ordered identifier set", async () => {
    const second = { ...method, id: 8, uuid: "Payment2", name: "备用通道", sort: 2 };
    const api = createAPI([method, second], method);
    api.setPaymentEnabled.mockResolvedValue({ ...method, enable: false });
    const user = userEvent.setup();
    render(<PaymentManagementPage api={api} />);

    await user.click(await screen.findByRole("button", { name: `禁用：${method.name}` }));
    await waitFor(() => expect(api.setPaymentEnabled).toHaveBeenCalledWith(method.id, false));
    await user.click(screen.getByRole("button", { name: "调整排序" }));
    const dialog = screen.getByRole("dialog", { name: "调整支付方式排序" });
    await user.click(within(dialog).getByRole("button", { name: `下移：${method.name}` }));
    await user.click(within(dialog).getByRole("button", { name: "保存排序" }));
    await waitFor(() => expect(api.reorderPayments).toHaveBeenCalledWith([second.id, method.id]));
  });
});

function createAPI(methods: PaymentMethod[], saved: PaymentMethod) {
  return {
    listPaymentProviders: vi.fn().mockResolvedValue(definitions),
    listAdminPayments: vi.fn().mockResolvedValue({ items: methods, total: methods.length, page: 1, page_size: 200 }),
    createPayment: vi.fn().mockResolvedValue(saved), updatePayment: vi.fn().mockResolvedValue(saved),
    setPaymentEnabled: vi.fn(), reorderPayments: vi.fn().mockResolvedValue(undefined), deletePayment: vi.fn()
  };
}
