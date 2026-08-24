import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  vi.unstubAllGlobals();
  document.title = "";
  window.history.replaceState(null, "", "#/");
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
    await waitFor(() => expect(document.title).toBe("登录 | Tenant Board"));
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
    expect(document.title).toBe("登录 | Xboard-Go");
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

  it("opens the legacy password recovery fields from the login page", async () => {
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Recovery Board", app_description: null, app_url: null, tos_url: null, logo: null,
          is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
          captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
          recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "忘记密码" }));
    expect(screen.getByRole("heading", { name: "重置密码 Recovery Board" })).toBeVisible();
    expect(screen.getByLabelText("邮箱验证码")).toBeVisible();
    expect(screen.getByLabelText("密码", { exact: true })).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText("再次输入密码")).toBeVisible();
    expect(screen.getByRole("button", { name: "发送" })).toBeVisible();
    expect(screen.getByRole("button", { name: "重置密码" })).toBeVisible();
    expect(screen.getByRole("button", { name: "返回登入" })).toBeVisible();
  });

  it("requests a recovery code, resets the password, and returns to login", async () => {
    const requests: Array<{ path: string; body: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Recovery Board", app_description: null, app_url: null, tos_url: null, logo: null,
          is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
          captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
          recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      if (path.endsWith("/api/v1/auth/session")) {
        return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
      }
      if (path.endsWith("/api/v1/auth/password-reset/request") || path.endsWith("/api/v1/auth/password-reset/confirm")) {
        requests.push({ path, body: JSON.parse(typeof init?.body === "string" ? init.body : "{}") as unknown });
        return Promise.resolve(jsonResponse(path.endsWith("/request") ? 202 : 200, { status: "success", data: true }));
      }
      throw new Error(`unexpected fetch ${path}`);
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "忘记密码" }));
    await user.type(screen.getByLabelText("邮箱"), "reset@example.test");
    await user.click(screen.getByRole("button", { name: "发送" }));
    expect(await screen.findByRole("status")).toHaveTextContent("验证码已发送，请检查邮箱");
    expect(screen.getByRole("button", { name: "60 秒" })).toBeDisabled();
    await user.type(screen.getByLabelText("邮箱验证码"), "384729");
    await user.type(screen.getByLabelText("密码", { exact: true }), "new-password-456");
    await user.type(screen.getByLabelText("再次输入密码"), "new-password-456");
    await user.click(screen.getByRole("button", { name: "重置密码" }));
    expect(await screen.findByRole("status")).toHaveTextContent("重置密码成功,正在返回登录");
    expect(await screen.findByRole("heading", { name: "登录 Recovery Board" }, { timeout: 2_000 })).toBeVisible();
    expect(requests).toEqual([
      { path: "/api/v1/auth/password-reset/request", body: { email: "reset@example.test" } },
      { path: "/api/v1/auth/password-reset/confirm", body: { email: "reset@example.test", email_code: "384729", password: "new-password-456" } }
    ]);
  });

  it("registers with the legacy basic fields and enters the user portal", async () => {
    const requests: Array<{ path: string; body: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Registration Board", app_description: null, app_url: null, tos_url: null, logo: null,
          is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: ["example.test"], is_captcha: 0,
          captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
          recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      if (path.endsWith("/api/v1/auth/session")) {
        return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
      }
      if (path.endsWith("/api/v1/auth/register")) {
        const body = typeof init?.body === "string" ? init.body : "";
        requests.push({ path, body: JSON.parse(body) as unknown });
        return Promise.resolve(jsonResponse(200, { status: "success", data: { id: 42, email: "new@example.test", is_admin: false } }));
      }
      if (path.endsWith("/api/v1/notices?page=1")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: { items: [], total: 0, page: 1, page_size: 5 } }));
      }
      return Promise.resolve(jsonResponse(200, { status: "success", data: [] }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "注册账号" }));
    expect(screen.getByRole("heading", { name: "注册 Registration Board" })).toBeVisible();
    expect(screen.getByText("允许邮箱后缀：example.test", { exact: true })).toBeVisible();
    await user.type(screen.getByLabelText("邮箱"), "NEW@EXAMPLE.TEST");
    await user.type(screen.getByLabelText("密码", { exact: true }), "password-123");
    await user.type(screen.getByLabelText("再次输入密码"), "password-123");
    await user.click(screen.getByRole("button", { name: "注册" }));

    expect(await screen.findByText("new@example.test", { exact: true })).toBeVisible();
    expect(requests).toEqual([{ path: "/api/v1/auth/register", body: {
      email: "NEW@EXAMPLE.TEST", password: "password-123", password_confirmation: "password-123"
    } }]);
  });
});

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}
