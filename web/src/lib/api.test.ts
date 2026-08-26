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
