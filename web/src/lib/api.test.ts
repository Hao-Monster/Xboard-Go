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
