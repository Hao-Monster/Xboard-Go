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
