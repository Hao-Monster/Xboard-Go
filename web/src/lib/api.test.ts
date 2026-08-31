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

describe("APIClient Telegram settings contracts", () => {
  it("keeps reads non-mutating and sends revisioned settings and provisioning with CSRF", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=telegram-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({
        path, method: init?.method ?? "GET",
        body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined,
        csrf: headers.get("X-CSRF-Token")
      });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const input = {
      revision: 7, telegram_bot_enable: true,
      telegram_bot_token: "123456789:abcdefghijklmnopqrstuvwxyzABCDE",
      telegram_webhook_url: "https://panel.example.test", telegram_discuss_link: "https://t.me/xboard_group"
    };

    await api.getTelegramSettings();
    await api.updateTelegramSettings(input);
    await api.provisionTelegramWebhook(8);

    expect(requests).toEqual([
      { path: "/api/v1/admin/telegram-settings", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/telegram-settings", method: "PUT", body: input, csrf: "telegram-csrf" },
      { path: "/api/v1/admin/telegram-settings/webhook", method: "POST", body: { revision: 8 }, csrf: "telegram-csrf" }
    ]);
  });
});

describe("APIClient client app settings contracts", () => {
  it("keeps reads non-mutating and sends the complete revisioned update with CSRF", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=client-app-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({ path, method: init?.method ?? "GET", body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined, csrf: headers.get("X-CSRF-Token") });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const input = {
      revision: 3,
      windows_version: "4.8.1", windows_download_url: "https://download.example.test/windows.exe",
      macos_version: "4.8.2", macos_download_url: "https://download.example.test/macos.dmg",
      android_version: "4.8.3", android_download_url: "https://download.example.test/android.apk"
    };

    await api.getClientAppSettings();
    await api.updateClientAppSettings(input);

    expect(requests).toEqual([
      { path: "/api/v1/admin/client-app-settings", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/client-app-settings", method: "PUT", body: input, csrf: "client-app-csrf" }
    ]);
  });
});

describe("APIClient mail template contracts", () => {
  it("keeps catalog reads non-mutating and sends every editor action with CSRF", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=template-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({ path, method: init?.method ?? "GET", body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined, csrf: headers.get("X-CSRF-Token") });
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();

    await api.listMailTemplates();
    await api.getMailTemplate("mailLogin");
    await api.updateMailTemplate("mailLogin", 2, "{{name}} - 登录", "<p>{{link}}</p>");
    await api.previewMailTemplate("mailLogin", "{{name}} - 预览", "<p>{{link}}</p>");
    await api.testMailTemplate("mailLogin", "admin@example.test");
    await api.resetMailTemplate("mailLogin", 3);

    expect(requests).toEqual([
      { path: "/api/v1/admin/mail-templates", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/mail-templates/mailLogin", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/mail-templates/mailLogin", method: "PUT", body: { revision: 2, subject: "{{name}} - 登录", content: "<p>{{link}}</p>" }, csrf: "template-csrf" },
      { path: "/api/v1/admin/mail-templates/mailLogin/preview", method: "POST", body: { subject: "{{name}} - 预览", content: "<p>{{link}}</p>" }, csrf: "template-csrf" },
      { path: "/api/v1/admin/mail-templates/mailLogin/test", method: "POST", body: { email: "admin@example.test" }, csrf: "template-csrf" },
      { path: "/api/v1/admin/mail-templates/mailLogin/reset", method: "POST", body: { revision: 3 }, csrf: "template-csrf" }
    ]);
  });
});

describe("APIClient administrator node contracts", () => {
  it("encodes bounded filters and sends revision-protected mutations with CSRF", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null }> = [];
    document.cookie = "xboard_csrf=node-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({
        path, method: init?.method ?? "GET",
        body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined,
        csrf: headers.get("X-CSRF-Token")
      });
      if (path.endsWith("/bulk-delete")) return Promise.resolve(new Response(null, { status: 204 }));
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: { items: [], total: 0, page: 2, page_size: 500, node_ids: [41] } }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const targets = [{ id: 41, revision: 3 }];
		const definition = {
			type: "vless", external_code: null, parent_id: null, name: "SG VLESS", rate: 1,
			tags: [], host: "sg.test", port: "443", server_port: 443, listen_address: "0.0.0.0",
			protocol_settings: { tls: 0, network: "tcp", network_settings: {} }, show: true, enabled: true,
			sort: 10, machine_id: null, group_ids: [], route_ids: [], rate_time_enabled: false,
			rate_time_ranges: [], custom_outbounds: [], custom_routes: [], certificate_config: {}, transfer_enable: 0
		};

    await api.listAdminNodes({ page: 2, page_size: 500, q: " SG edge ", type: "vless", show: false, enabled: true, machine_id: 7 });
		await api.getAdminNodeDefinition(41);
		await api.createAdminNodeDefinition(definition);
		await api.replaceAdminNodeDefinition(41, { ...definition, revision: 3 });
    await api.updateAdminNode(41, { revision: 3, name: "SG", host: "sg.test", port: "443", show: true, enabled: true, sort: 10, machine_id: null });
    await api.copyAdminNode(41, 3);
    await api.reorderAdminNodes(targets);
    await api.updateAdminNodeStates({ targets, enabled: false, machine_id: null });
    await api.resetAdminNodeTraffic(targets);
    await api.deleteAdminNodes(targets);

    expect(requests).toEqual([
      { path: "/api/v1/admin/nodes?page=2&page_size=500&q=SG+edge&type=vless&show=false&enabled=true&machine_id=7", method: "GET", body: undefined, csrf: null },
			{ path: "/api/v1/admin/nodes/41", method: "GET", body: undefined, csrf: null },
			{ path: "/api/v1/admin/nodes", method: "POST", body: definition, csrf: "node-csrf" },
			{ path: "/api/v1/admin/nodes/41", method: "PUT", body: { ...definition, revision: 3 }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/41", method: "PATCH", body: { revision: 3, name: "SG", host: "sg.test", port: "443", show: true, enabled: true, sort: 10, machine_id: null }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/41/copy", method: "POST", body: { revision: 3 }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/order", method: "PUT", body: { targets }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/bulk-state", method: "POST", body: { targets, enabled: false, machine_id: null }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/bulk-reset-traffic", method: "POST", body: { targets }, csrf: "node-csrf" },
      { path: "/api/v1/admin/nodes/bulk-delete", method: "POST", body: { targets }, csrf: "node-csrf" }
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
		await api.listAdminOrders({ statuses: [3, 4], types: [1, 2], periods: ["monthly", "yearly"], commission_statuses: [0, 3], sort_by: "commission_balance", sort_desc: true });
    await api.assignOrder({ email: "buyer@example.test", plan_id: 7, period: "yearly", total_amount: 12_345 });
    await api.paidAdminOrder("trade/with slash");
		await api.updateAdminOrderCommissionStatus("trade/with slash", 3);

    expect(requests).toEqual([
      { path: "/api/v1/orders?limit=25&status=0", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/orders", method: "POST", body: { plan_id: 7, period: "monthly" }, csrf: "order-csrf" },
      { path: "/api/v1/orders/trade%2Fwith%20slash/checkout", method: "POST", body: {}, csrf: "order-csrf" },
      { path: "/api/v1/orders/trade%2Fwith%20slash/cancel", method: "POST", body: {}, csrf: "order-csrf" },
      { path: "/api/v1/admin/orders?page=2&page_size=50&status=3&type=2&period=yearly&query=buyer%40example.test", method: "GET", body: undefined, csrf: null },
			{ path: "/api/v1/admin/orders?status=3&status=4&type=1&type=2&period=monthly&period=yearly&commission_status=0&commission_status=3&sort_by=commission_balance&sort_desc=true", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/orders", method: "POST", body: { email: "buyer@example.test", plan_id: 7, period: "yearly", total_amount: 12_345 }, csrf: "order-csrf" },
			{ path: "/api/v1/admin/orders/trade%2Fwith%20slash/paid", method: "POST", body: {}, csrf: "order-csrf" },
			{ path: "/api/v1/admin/orders/trade%2Fwith%20slash/commission", method: "PATCH", body: { commission_status: 3 }, csrf: "order-csrf" }
    ]);
  });
});

describe("APIClient administrator user contracts", () => {
  it("encodes bounded pagination, allowlisted sort, quick filters, and structured advanced filters", async () => {
    let requested = "";
    let method = "";
    let body: unknown;
    let csrf = "";
    document.cookie = "xboard_csrf=user-query-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      requested = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      method = init?.method ?? "GET";
      body = typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined;
      csrf = new Headers(init?.headers).get("X-CSRF-Token") ?? "";
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: { items: [], total: 0, page: 2, page_size: 50 } }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    await api.listAdminUsers({
      page: 2, page_size: 50, sort_by: "balance", sort_desc: true, email_prefix: "vip", banned: false, group_id: 7,
      filters: [
        { field: "remarks", operator: "contains", value: "重点" },
        { field: "id", operator: "in", value: [41, 42] }
      ]
    });

    expect(requested).toBe("/api/v1/admin/users/query");
    expect(method).toBe("POST");
    expect(csrf).toBe("user-query-csrf");
    expect(body).toEqual({
      page: 2, page_size: 50, sort_by: "balance", sort_desc: true, email_prefix: "vip", banned: false, group_id: 7,
      filters: [
        { field: "remarks", operator: "contains", value: "重点" },
        { field: "id", operator: "in", value: [41, 42] }
      ]
    });
  });

	it("uses server-scoped user operation routes and preserves the reset idempotency key", async () => {
		const requests: Array<{ path: string; method: string; body?: unknown; idempotencyKey: string | null }> = [];
		document.cookie = "xboard_csrf=user-operation-csrf; path=/";
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
			const headers = new Headers(init?.headers);
			requests.push({
				path, method: init?.method ?? "GET",
				body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined,
				idempotencyKey: headers.get("Idempotency-Key")
			});
			return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
		}));
		const api = new APIClient();

		await api.getAdminUserSubscriptionURL(41);
		await api.resetAdminUserSubscriptionSecurity(41, 7);
		await api.listAdminUserOrders(41, 2, 20);
		await api.assignAdminUserOrder(41, { plan_id: 3, period: "monthly", total_amount: 2500 });
		await api.listAdminUserInvitations(41, 1, 20);
		await api.listAdminUserTraffic(41, 1, 20);
		await api.listAdminUserTrafficResets(41, 1, 20);
		await api.resetAdminUserTraffic(41, "customer request", "u4-browser-reset-0001");

		expect(requests).toEqual([
			{ path: "/api/v1/admin/users/41/subscription-url", method: "GET", body: undefined, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/subscription-security/reset", method: "POST", body: { revision: 7 }, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/orders?page=2&page_size=20", method: "GET", body: undefined, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/orders", method: "POST", body: { plan_id: 3, period: "monthly", total_amount: 2500 }, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/invitations?page=1&page_size=20", method: "GET", body: undefined, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/traffic?page=1&page_size=20", method: "GET", body: undefined, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/traffic-resets?page=1&page_size=20", method: "GET", body: undefined, idempotencyKey: null },
			{ path: "/api/v1/admin/users/41/traffic-reset", method: "POST", body: { reason: "customer request" }, idempotencyKey: "u4-browser-reset-0001" }
		]);
	});

  it("uses bounded administrator bulk-job routes, encoded IDs, CSRF, and authenticated CSV download", async () => {
    const requests: Array<{ path: string; method: string; body?: unknown; csrf: string | null; accept: string | null }> = [];
    document.cookie = "xboard_csrf=user-bulk-csrf; path=/";
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      const headers = new Headers(init?.headers);
      requests.push({
        path,
        method: init?.method ?? "GET",
        body: typeof init?.body === "string" ? JSON.parse(init.body) as unknown : undefined,
        csrf: headers.get("X-CSRF-Token"),
        accept: headers.get("Accept")
      });
      if (path.endsWith("/download")) {
        return Promise.resolve(new Response("csv-bytes", { status: 200, headers: { "Content-Type": "text/csv" } }));
      }
      return Promise.resolve(new Response(JSON.stringify({ status: "success", data: {} }), { status: 200, headers: { "Content-Type": "application/json" } }));
    }));
    const api = new APIClient();
    const filtered = {
      scope: "filtered" as const,
      email_prefix: "vip",
      banned: false,
      group_id: 7,
      filters: [{ field: "remarks", operator: "contains" as const, value: "重点" }]
    };

    await api.createAdminUserBulkMail({ scope: "selected", user_ids: [41, 42] }, "系统通知", "您好 {{user.email}}");
    await api.createAdminUserBulkCSV(filtered);
    await api.banAdminUsers({ scope: "all" }, "bulk-ban-0001");
    await api.listAdminUserBulkJobs(2, 50);
    await api.getAdminUserBulkJob("job/with slash");
    await api.cancelAdminUserBulkJob("job/with slash");
    const blob = await api.downloadAdminUserBulkCSV("job/with slash");

    expect(await blob.text()).toBe("csv-bytes");
    expect(requests).toEqual([
      { path: "/api/v1/admin/users/bulk/mail", method: "POST", body: { scope: "selected", user_ids: [41, 42], subject: "系统通知", content: "您好 {{user.email}}" }, csrf: "user-bulk-csrf", accept: "application/json" },
      { path: "/api/v1/admin/users/bulk/csv", method: "POST", body: filtered, csrf: "user-bulk-csrf", accept: "application/json" },
      { path: "/api/v1/admin/users/bulk/ban", method: "POST", body: { scope: "all", idempotency_key: "bulk-ban-0001" }, csrf: "user-bulk-csrf", accept: "application/json" },
      { path: "/api/v1/admin/user-bulk-jobs?page=2&page_size=50", method: "GET", body: undefined, csrf: null, accept: "application/json" },
      { path: "/api/v1/admin/user-bulk-jobs/job%2Fwith%20slash", method: "GET", body: undefined, csrf: null, accept: "application/json" },
      { path: "/api/v1/admin/user-bulk-jobs/job%2Fwith%20slash/cancel", method: "POST", body: {}, csrf: "user-bulk-csrf", accept: "application/json" },
      { path: "/api/v1/admin/user-bulk-jobs/job%2Fwith%20slash/download", method: "GET", body: undefined, csrf: null, accept: "text/csv" }
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

    await api.listTrustedPlugins();
    await api.updateTrustedPlugin("epay", { revision: 1, enabled: false, config: {} });
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
      { path: "/api/v1/admin/plugins", method: "GET", body: undefined, csrf: null },
      { path: "/api/v1/admin/plugins/epay", method: "PATCH", body: { revision: 1, enabled: false, config: {} }, csrf: "payment-csrf" },
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
