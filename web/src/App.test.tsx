import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  vi.unstubAllGlobals();
  document.title = "";
});

describe("App public identity bootstrap", () => {
  it("renders configured branding and a safe TOS link before login", async () => {
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Tenant Board", app_description: "Tenant description", app_url: "https://tenant.example.test",
          tos_url: "https://tenant.example.test/terms", logo: "https://images.example.test/tenant.svg", is_email_verify: 0, is_invite_force: 0,
          email_whitelist_suffix: 0, is_captcha: 0, captcha_type: "recaptcha", recaptcha_site_key: null,
          recaptcha_v3_site_key: null, recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
    }));
    render(<App />);

    expect(await screen.findByRole("heading", { name: "登录 Tenant Board" })).toBeVisible();
    expect(screen.getByText("Tenant description", { exact: true })).toBeVisible();
    expect(screen.getByRole("img", { name: "Tenant Board LOGO" })).toHaveAttribute("src", "https://images.example.test/tenant.svg");
    expect(screen.getByRole("img", { name: "Tenant Board LOGO" })).toHaveAttribute("referrerpolicy", "no-referrer");
    expect(screen.getByRole("link", { name: "用户条款" })).toHaveAttribute("rel", "noreferrer noopener");
    await waitFor(() => expect(document.title).toBe("Tenant Board 控制面板"));
    expect(document.querySelector('meta[name="description"]')).toHaveAttribute("content", "Tenant description");
  });

  it("keeps login available with the safe default when public configuration hangs", async () => {
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) return new Promise<Response>(() => undefined);
      return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
    }));
    render(<App />);

    expect(await screen.findByRole("heading", { name: "登录 Xboard-Go" })).toBeVisible();
    expect(screen.queryByRole("link", { name: "用户条款" })).not.toBeInTheDocument();
    expect(document.title).toBe("Xboard-Go 控制面板");
  });

  it("falls back to the local brand mark when a configured logo cannot load", async () => {
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Fallback Board", app_description: null, app_url: null, tos_url: null,
          logo: "https://images.example.test/broken.svg", is_email_verify: 0, is_invite_force: 0,
          email_whitelist_suffix: 0, is_captcha: 0, captcha_type: "recaptcha", recaptcha_site_key: null,
          recaptcha_v3_site_key: null, recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
    }));
    render(<App />);

    const logo = await screen.findByRole("img", { name: "Fallback Board LOGO" });
    fireEvent.error(logo);
    await waitFor(() => expect(screen.queryByRole("img", { name: "Fallback Board LOGO" })).not.toBeInTheDocument());
    expect(screen.getByText("F", { exact: true })).toBeVisible();
  });
});

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
