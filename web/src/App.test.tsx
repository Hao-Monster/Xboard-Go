import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

afterEach(() => {
  vi.unstubAllGlobals();
  document.title = "";
  window.history.replaceState(null, "", "#/");
});

describe("App public identity bootstrap", () => {
	it("routes a distributor session to the dedicated allowlisted portal", async () => {
		const requested: string[] = [];
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			requested.push(path);
			if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				app_name: "Distributor Board", app_description: null, app_url: null, tos_url: null, logo: null,
				is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
				captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
				recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
			} }));
			if (path.endsWith("/api/v1/auth/session")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				id: 90, email: "seller@example.test", is_admin: false, is_staff: false, is_distributor: true, distributor_name: "星河分销"
			} }));
			if (path.endsWith("/api/v1/plans")) return Promise.resolve(jsonResponse(200, { status: "success", data: [] }));
			throw new Error(`unexpected fetch ${path}`);
		}));

		render(<App />);
		expect(await screen.findByRole("heading", { name: "分销订阅中心" }, { timeout: 5_000 })).toBeVisible();
		expect(screen.getByText("星河分销")).toBeVisible();
		expect(screen.queryByRole("button", { name: "我的订阅" })).not.toBeInTheDocument();
		expect(requested.some((path) => path.includes("/api/v1/notices"))).toBe(false);
	});

	it("keeps the administrator shell for an admin, staff, and distributor hybrid session", async () => {
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				app_name: "Hybrid Board", app_description: null, app_url: null, tos_url: null, logo: null,
				is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
				captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
				recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
			} }));
			if (path.endsWith("/api/v1/auth/session")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				id: 91, email: "hybrid@example.test", is_admin: true, is_staff: true, is_distributor: true, distributor_name: "混合角色"
			} }));
			return Promise.resolve(jsonResponse(503, { status: "fail", error: { code: "test_unavailable", message: "测试未提供运行状态" } }));
		}));

		render(<App />);
		const navigation = await screen.findByRole("navigation", { name: "管理端导航" });
		expect(navigation).toBeVisible();
		expect(navigation).toHaveClass("admin-sidebar");
		expect(navigation.parentElement).toHaveClass("admin-layout");
		expect(document.querySelector(".topbar .admin-nav")).not.toBeInTheDocument();
		expect(screen.getByText("hybrid@example.test", { exact: true })).toBeVisible();
		expect(screen.getByRole("button", { name: "分销管理" })).toBeVisible();
		expect(screen.getByRole("button", { name: "邮件设置" })).toBeVisible();
		expect(screen.queryByRole("heading", { name: "分销订阅中心" })).not.toBeInTheDocument();
	});

  it("warns before leaving client app settings with unsaved changes", async () => {
    const confirm = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
    vi.stubGlobal("confirm", confirm);
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
        app_name: "Client Board", app_description: null, app_url: null, tos_url: null, logo: null,
        is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0, is_captcha: 0,
        captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
        recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
      } }));
      if (path.endsWith("/api/v1/auth/session")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
        id: 92, email: "client-admin@example.test", is_admin: true, is_staff: false, is_distributor: false
      } }));
      if (path.endsWith("/api/v1/admin/client-app-settings")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
        revision: 1,
        windows_version: "4.8.1", windows_download_url: "https://download.example.test/windows.exe",
        macos_version: "4.8.2", macos_download_url: "https://download.example.test/macos.dmg",
        android_version: "4.8.3", android_download_url: "https://download.example.test/android.apk",
        updated_at: "2026-08-30T11:00:00Z"
      } }));
      return Promise.resolve(jsonResponse(503, { status: "fail", error: { code: "test_unavailable", message: "测试未提供运行状态" } }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "客户端版本" }));
    const version = await screen.findByLabelText("Windows 版本");
    await user.clear(version);
    await user.type(version, "5.0.0");
    await user.click(screen.getByRole("button", { name: "系统设置" }));
    expect(confirm).toHaveBeenNthCalledWith(1, "客户端版本有未保存的修改，确认离开并放弃这些修改吗？");
    expect(screen.getByRole("heading", { name: "客户端版本" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "系统设置" }));
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(await screen.findByRole("heading", { name: "系统设置" })).toBeVisible();
  });

	it("does not let a stale session bootstrap overwrite a newer login-link exchange", async () => {
		const token = "11111111111111111111111111111111";
		let sessionRequested = false;
		let resolveSession!: (response: Response) => void;
		const pendingSession = new Promise<Response>((resolve) => {
			resolveSession = resolve;
		});
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				app_name: "Race Board", app_description: null, app_url: null, tos_url: null, logo: null,
				is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
				captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
				recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
			} }));
			if (path.endsWith("/api/v1/auth/session")) {
				sessionRequested = true;
				return pendingSession;
			}
			if (path.endsWith("/api/v1/auth/login-link/exchange")) return Promise.resolve(jsonResponse(200, {
				status: "success", data: { id: 72, email: "race@example.test", is_admin: false, redirect: "dashboard" }
			}));
			if (path.endsWith("/api/v1/notices?page=1")) return Promise.resolve(jsonResponse(200, {
				status: "success", data: { items: [], total: 0, page: 1, page_size: 5 }
			}));
			throw new Error(`unexpected fetch ${path}`);
		}));

		render(<App />);
		await waitFor(() => expect(sessionRequested).toBe(true));
		window.history.replaceState(null, "", `#/login?verify=${token}&redirect=dashboard`);
		fireEvent(window, new HashChangeEvent("hashchange"));
		expect(await screen.findByText("race@example.test", { exact: true }, { timeout: 3_000 })).toBeVisible();

		await act(async () => {
			resolveSession(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
			await pendingSession;
		});
		expect(screen.getByText("race@example.test", { exact: true })).toBeVisible();
	});

	it("exchanges a one-time login link before session bootstrap and honors its user landing page", async () => {
		const token = "01234567".repeat(4);
		const requests: Array<{ path: string; method: string; body: unknown }> = [];
		window.history.replaceState(null, "", `#/login?verify=${token}&redirect=invite`);
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				app_name: "Link Board", app_description: null, app_url: null, tos_url: null, logo: null,
				is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
				captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
				recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
			} }));
			if (path.endsWith("/api/v1/auth/login-link/exchange")) {
				requests.push({ path, method: init?.method ?? "GET", body: JSON.parse(typeof init?.body === "string" ? init.body : "{}") as unknown });
				return Promise.resolve(jsonResponse(200, { status: "success", data: { id: 71, email: "linked@example.test", is_admin: false, redirect: "invite" } }));
			}
			if (path.endsWith("/api/v1/invitations")) return Promise.resolve(jsonResponse(200, { status: "success", data: { codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0, commission_rate: 10, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0 } }));
			if (path.includes("/api/v1/invitations/commissions")) return Promise.resolve(jsonResponse(200, { status: "success", data: { items: [], total: 0, page: 1, page_size: 50 } }));
			throw new Error(`unexpected fetch ${path}`);
		}));

		render(<App />);

		expect(await screen.findByText("linked@example.test", { exact: true })).toBeVisible();
		expect(screen.getByRole("button", { name: "我的邀请" })).toHaveAttribute("aria-current", "page");
		expect(window.location.hash).toBe("#/invite");
		expect(requests).toEqual([{ path: "/api/v1/auth/login-link/exchange", method: "POST", body: { token } }]);
	});

	it("scrubs an invalid login token and reports the exchange failure", async () => {
		const token = "fedcba98".repeat(4);
		window.history.replaceState(null, "", `#/login?verify=${token}&redirect=dashboard`);
		vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
			const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
			if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
				app_name: "Link Board", app_description: null, app_url: null, tos_url: null, logo: null,
				is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
				captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
				recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
			} }));
			if (path.endsWith("/api/v1/auth/login-link/exchange")) return Promise.resolve(jsonResponse(400, {
				status: "fail", error: { code: "login_link_invalid", message: "登录链接无效或已过期" }
			}));
			throw new Error(`unexpected fetch ${path}`);
		}));

		render(<App />);

		expect(await screen.findByRole("alert")).toHaveTextContent("登录链接无效或已过期");
		expect(window.location.hash).toBe("#/login");
	});

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
      if (path.endsWith("/api/v1/subscription")) return Promise.resolve(subscriptionResponse("new@example.test"));
      return Promise.resolve(jsonResponse(200, { status: "success", data: [] }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "注册账号" }));
    expect(screen.getByRole("heading", { name: "注册 Registration Board" })).toBeVisible();
    expect(screen.getByText("允许邮箱后缀：example.test", { exact: true })).toBeVisible();
    expect(screen.getByLabelText("邀请码")).toHaveAttribute("placeholder", "邀请码,（选填）");
    expect(screen.getByLabelText("邀请码")).not.toBeRequired();
    await user.type(screen.getByLabelText("邮箱"), "NEW@EXAMPLE.TEST");
    await user.type(screen.getByLabelText("密码", { exact: true }), "password-123");
    await user.type(screen.getByLabelText("再次输入密码"), "password-123");
    await user.click(screen.getByRole("button", { name: "注册" }));

    expect(await screen.findByText("new@example.test", { exact: true })).toBeVisible();
    expect(requests).toEqual([{ path: "/api/v1/auth/register", body: {
      email: "NEW@EXAMPLE.TEST", password: "password-123", password_confirmation: "password-123"
    } }]);
  });

  it("requires and submits the legacy invitation field when forced", async () => {
    const requests: unknown[] = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Invitation Board", app_description: null, app_url: null, tos_url: null, logo: null,
          is_email_verify: 0, is_invite_force: 1, email_whitelist_suffix: 0, is_captcha: 0,
          captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
          recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      if (path.endsWith("/api/v1/auth/session")) return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
      if (path.endsWith("/api/v1/auth/register")) {
        requests.push(JSON.parse(typeof init?.body === "string" ? init.body : "{}") as unknown);
        return Promise.resolve(jsonResponse(200, { status: "success", data: { id: 88, email: "invited@example.test", is_admin: false } }));
      }
      if (path.endsWith("/api/v1/notices?page=1")) return Promise.resolve(jsonResponse(200, { status: "success", data: { items: [], total: 0, page: 1, page_size: 5 } }));
      if (path.endsWith("/api/v1/subscription")) return Promise.resolve(subscriptionResponse("invited@example.test"));
      return Promise.resolve(jsonResponse(200, { status: "success", data: [] }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "注册账号" }));
    expect(screen.getByLabelText("邀请码")).toHaveAttribute("placeholder", "邀请码,（必填）");
    expect(screen.getByLabelText("邀请码")).toBeRequired();
    await user.type(screen.getByLabelText("邮箱"), "invited@example.test");
    await user.type(screen.getByLabelText("邀请码"), "Abcd1234");
    await user.type(screen.getByLabelText("密码", { exact: true }), "password-123");
    await user.type(screen.getByLabelText("再次输入密码"), "password-123");
    await user.click(screen.getByRole("button", { name: "注册" }));

    expect(await screen.findByText("invited@example.test", { exact: true })).toBeVisible();
    expect(requests).toEqual([{
      email: "invited@example.test", password: "password-123", password_confirmation: "password-123", invite_code: "Abcd1234"
    }]);
  });

  it("prefills and locks an invitation from the legacy registration link", async () => {
    window.history.replaceState(null, "", "#/register?code=Link1234");
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) return Promise.resolve(jsonResponse(200, { status: "success", data: {
        app_name: "Linked Board", app_description: null, app_url: null, tos_url: null, logo: null,
        is_email_verify: 0, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
        captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
        recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
      } }));
      return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
    }));
    render(<App />);

    expect(await screen.findByRole("heading", { name: "注册 Linked Board" })).toBeVisible();
    expect(screen.getByLabelText("邀请码")).toHaveValue("Link1234");
    expect(screen.getByLabelText("邀请码")).toBeDisabled();
  });

  it("shows, sends, and submits the legacy registration email code when enabled", async () => {
    const requests: Array<{ path: string; body: unknown }> = [];
    vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (path.endsWith("/api/v1/guest/comm/config")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: {
          app_name: "Verified Registration", app_description: null, app_url: null, tos_url: null, logo: null,
          is_email_verify: 1, is_invite_force: 0, email_whitelist_suffix: 0, is_captcha: 0,
          captcha_type: "recaptcha", recaptcha_site_key: null, recaptcha_v3_site_key: null,
          recaptcha_v3_score_threshold: 0.5, turnstile_site_key: null, is_recaptcha: 0
        } }));
      }
      if (path.endsWith("/api/v1/auth/session")) {
        return Promise.resolve(jsonResponse(401, { status: "fail", error: { code: "unauthenticated", message: "请先登录" } }));
      }
      if (path.endsWith("/api/v1/auth/registration-email/request")) {
        const body = JSON.parse(typeof init?.body === "string" ? init.body : "{}") as unknown;
        requests.push({ path, body });
        return Promise.resolve(jsonResponse(202, { status: "success", data: true }));
      }
      if (path.endsWith("/api/v1/auth/register")) {
        const body = JSON.parse(typeof init?.body === "string" ? init.body : "{}") as unknown;
        requests.push({ path, body });
        return Promise.resolve(jsonResponse(200, { status: "success", data: { id: 51, email: "verified@example.test", is_admin: false } }));
      }
      if (path.endsWith("/api/v1/notices?page=1")) {
        return Promise.resolve(jsonResponse(200, { status: "success", data: { items: [], total: 0, page: 1, page_size: 5 } }));
      }
      if (path.endsWith("/api/v1/subscription")) return Promise.resolve(subscriptionResponse("verified@example.test"));
      return Promise.resolve(jsonResponse(200, { status: "success", data: [] }));
    }));
    const user = userEvent.setup();
    render(<App />);

    await user.click(await screen.findByRole("button", { name: "注册账号" }));
    await user.type(screen.getByLabelText("邮箱"), "verified@example.test");
    expect(screen.getByLabelText("邮箱验证码")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "发送" }));
    expect(await screen.findByRole("status")).toHaveTextContent("验证码已发送，请检查邮箱");
    expect(screen.getByRole("button", { name: "60 秒" })).toBeDisabled();
    await user.type(screen.getByLabelText("邮箱验证码"), "482731");
    await user.type(screen.getByLabelText("密码", { exact: true }), "password-123");
    await user.type(screen.getByLabelText("再次输入密码"), "password-123");
    await user.click(screen.getByRole("button", { name: "注册" }));

    expect(await screen.findByText("verified@example.test", { exact: true })).toBeVisible();
    expect(requests).toEqual([
      { path: "/api/v1/auth/registration-email/request", body: { email: "verified@example.test" } },
      { path: "/api/v1/auth/register", body: {
        email: "verified@example.test", email_code: "482731", password: "password-123", password_confirmation: "password-123"
      } }
    ]);
  });
});

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function subscriptionResponse(email: string): Response {
  return jsonResponse(200, { status: "success", data: {
    plan_id: null, token: "1".repeat(32), expired_at: null, u: 0, d: 0, transfer_enable: 0, email,
    uuid: "11111111-1111-4111-8111-111111111111", device_limit: 0, speed_limit: 0, next_reset_at: null,
    plan: null, subscribe_url: `https://panel.example.test/s/${"1".repeat(32)}`, reset_day: null, subscription_valid: false
  } });
}
