import { afterEach, describe, expect, it, vi } from "vitest";

import { APIClient } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("APIClient CAPTCHA contracts", () => {
  it("sends the legacy provider token fields only on protected actions", async () => {
    const requests: Array<{ path: string; body: Record<string, unknown> }> = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.pathname : new URL(input.url).pathname;
      requests.push({ path, body: JSON.parse(typeof init?.body === "string" ? init.body : "{}") as Record<string, unknown> });
      const data = path.endsWith("/register") ? { id: 7, email: "new@example.test", is_admin: false } : true;
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();

    await api.register("new@example.test", "password-123", "password-123", "123456", "INVITE", { recaptcha_data: "v2-token" });
    await api.requestRegistrationEmailVerification("new@example.test", { recaptcha_v3_token: "v3-token" });
    await api.requestPasswordReset("new@example.test", { turnstile_token: "turnstile-token" });

    expect(requests).toEqual([
      { path: "/api/v1/auth/register", body: { email: "new@example.test", password: "password-123", password_confirmation: "password-123", email_code: "123456", invite_code: "INVITE", recaptcha_data: "v2-token" } },
      { path: "/api/v1/auth/registration-email/request", body: { email: "new@example.test", recaptcha_v3_token: "v3-token" } },
      { path: "/api/v1/auth/password-reset/request", body: { email: "new@example.test", turnstile_token: "turnstile-token" } }
    ]);
  });
});

describe("APIClient order contracts", () => {
  it("uses authoritative order routes, encoded trade numbers, integer cents, and CSRF on mutations", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=order-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({
        path: raw,
        method: init?.method ?? "GET",
        body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined,
        csrf: headers.get("X-CSRF-Token")
      });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();

    await api.listOrders(0, 25);
    await api.createOrder(7, "monthly");
    await api.checkoutOrder("trade/with slash");
    await api.cancelOrder("trade/with slash");
    await api.listAdminOrders({ page: 2, page_size: 50, status: 3, type: 2, period: "yearly", query: "buyer@example.test" });
    await api.assignOrder({ email: "buyer@example.test", plan_id: 7, period: "yearly", total_amount: 12_345 });
    await api.paidAdminOrder("trade/with slash");

    expect(requests).toEqual([
      { path: "/api/v1/orders?limit=25&status=0", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/orders", method: "POST", body: { plan_id: 7, period: "monthly" }, csrf: "order-csrf" },
      { path: "/api/v1/orders/trade%2Fwith%20slash/checkout", method: "POST", body: {}, csrf: "order-csrf" },
      { path: "/api/v1/orders/trade%2Fwith%20slash/cancel", method: "POST", body: {}, csrf: "order-csrf" },
      { path: "/api/v1/admin/orders?page=2&page_size=50&status=3&type=2&period=yearly&query=buyer%40example.test", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/orders", method: "POST", body: { email: "buyer@example.test", plan_id: 7, period: "yearly", total_amount: 12_345 }, csrf: "order-csrf" },
      { path: "/api/v1/admin/orders/trade%2Fwith%20slash/paid", method: "POST", body: {}, csrf: "order-csrf" }
    ]);
  });
});

describe("APIClient payment contracts", () => {
  it("uses session-protected reads and CSRF-protected administrator and checkout mutations", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=payment-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({ path: raw, method: init?.method ?? "GET", body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined, csrf: headers.get("X-CSRF-Token") });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const input = {
      payment: "EPay" as const, name: "易支付", icon: "", notify_domain: "https://pay.example.test",
      handling_fee_fixed: 123, handling_fee_basis_points: 250, enable: true,
      config: { url: "https://epay.example.test", pid: "1001", key: "secret", type: "alipay" }
    };

    await api.listPaymentProviders();
    await api.listAdminPayments(2, 20, "易支付");
    await api.createPayment(input);
    await api.updatePayment(7, { ...input, revision: 2 });
    await api.setPaymentEnabled(7, false);
    await api.reorderPayments([8, 7]);
    await api.deletePayment(7);
    await api.listPaymentMethods();
    await api.checkoutOrder("trade/payment", 7);

    expect(requests).toEqual([
      { path: "/api/v1/admin/payment-providers", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/payments?page=2&page_size=20&query=%E6%98%93%E6%94%AF%E4%BB%98", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/payments", method: "POST", body: input, csrf: "payment-csrf" },
      { path: "/api/v1/admin/payments/7", method: "PUT", body: { ...input, revision: 2 }, csrf: "payment-csrf" },
      { path: "/api/v1/admin/payments/7/enabled", method: "PATCH", body: { enable: false }, csrf: "payment-csrf" },
      { path: "/api/v1/admin/payments/order", method: "PUT", body: { ids: [8, 7] }, csrf: "payment-csrf" },
      { path: "/api/v1/admin/payments/7", method: "DELETE", body: undefined, csrf: "payment-csrf" },
      { path: "/api/v1/payments", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/orders/trade%2Fpayment/checkout", method: "POST", body: { payment_id: 7 }, csrf: "payment-csrf" }
    ]);
  });
});

describe("APIClient gift card contracts", () => {
  it("uses the modern administrator and user routes with CSRF on every mutation", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=gift-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({ path: raw, method: init?.method ?? "GET", body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined, csrf: headers.get("X-CSRF-Token") });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const input = { name: "card", description: "", type: 1 as const, status: true, conditions: {}, rewards: { balance: 500 }, limits: { max_use_per_user: 1 }, special_config: {}, icon: "", background_image: "", theme: "", sort: 0 };
    await api.listGiftCardTemplates(2, 20, 1, true); await api.createGiftCardTemplate(input); await api.updateGiftCardTemplate(7, { ...input, revision: 2 });
    await api.generateGiftCardCodes(7, 3, "VIP", null, 1); await api.generateGiftCardCodesCSV(7, 2, "CSV", null, 1); await api.listGiftCardCodes(2, 20, "VIP", 7, 0, "batch_1"); await api.updateGiftCardCode(9, { expires_at: null }); await api.exportGiftCardCodes("batch_1"); await api.toggleGiftCardCode(9); await api.deleteGiftCardCode(9);
    await api.listGiftCardUsages(2, 20, 3, 7, 9); await api.getGiftCardStatistics(); await api.checkGiftCard("VIPABCDEFGH"); await api.redeemGiftCard("VIPABCDEFGH"); await api.listMyGiftCardUsages();
    expect(requests).toEqual([
      { path: "/api/v1/admin/gift-card/templates?page=2&page_size=20&type=1&status=true", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/gift-card/templates", method: "POST", body: input, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/templates/7", method: "PUT", body: { ...input, revision: 2 }, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/codes/generate", method: "POST", body: { template_id: 7, count: 3, prefix: "VIP", expires_at: null, max_usage: 1 }, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/codes/generate", method: "POST", body: { template_id: 7, count: 2, prefix: "CSV", expires_at: null, max_usage: 1, download_csv: true }, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/codes?page=2&page_size=20&query=VIP&template_id=7&status=0&batch_no=batch_1", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/gift-card/codes/9", method: "PATCH", body: { expires_at: null }, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/codes/export?batch_no=batch_1", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/gift-card/codes/9/toggle", method: "POST", body: {}, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/codes/9", method: "DELETE", body: undefined, csrf: "gift-csrf" },
      { path: "/api/v1/admin/gift-card/usages?page=2&page_size=20&user_id=3&template_id=7&code_id=9", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/gift-card/statistics", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/user/gift-card/check", method: "POST", body: { code: "VIPABCDEFGH" }, csrf: "gift-csrf" },
      { path: "/api/v1/user/gift-card/redeem", method: "POST", body: { code: "VIPABCDEFGH" }, csrf: "gift-csrf" },
      { path: "/api/v1/user/gift-card/history?page=1&page_size=15", method: "GET", body: undefined, csrf: null }
    ]);
  });
});
