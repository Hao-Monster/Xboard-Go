import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useState } from "react";

import type { CaptchaToken, GuestConfig } from "../../lib/api";
import { resetCaptchaProviderScripts, useCaptchaChallenge } from "./CaptchaChallenge";

afterEach(() => {
  resetCaptchaProviderScripts();
});

describe("CAPTCHA challenge", () => {
  it("opens an interactive reCAPTCHA v2 modal and returns the legacy token field", async () => {
    let complete: ((token: string) => void) | undefined;
    const reset = vi.fn();
    window.grecaptcha = {
      ready: (callback) => callback(),
      execute: vi.fn(),
      render: (_container, options) => {
        complete = options.callback as (token: string) => void;
        return 17;
      },
      reset
    };
    const user = userEvent.setup();
    render(<Harness config={captchaConfig({ captcha_type: "recaptcha", recaptcha_site_key: "v2-site" })} />);

    await user.click(screen.getByRole("button", { name: "注册动作" }));
    expect(await screen.findByRole("dialog", { name: "人机验证" })).toBeVisible();
    expect(screen.getByLabelText("验证码组件")).toBeVisible();
    act(() => complete?.("v2-browser-token"));
    expect(await screen.findByTestId("captcha-result")).toHaveTextContent(`{"recaptcha_data":"v2-browser-token"}`);
    expect(reset).toHaveBeenCalledWith(17);
  });

  it("executes reCAPTCHA v3 only on the requested action", async () => {
    const execute = vi.fn().mockResolvedValue("v3-browser-token");
    window.grecaptcha = { ready: (callback) => callback(), execute, render: vi.fn(), reset: vi.fn() };
    const user = userEvent.setup();
    render(<Harness config={captchaConfig({ captcha_type: "recaptcha-v3", recaptcha_v3_site_key: "v3-site" })} />);

    await user.click(screen.getByRole("button", { name: "邮箱动作" }));
    expect(await screen.findByTestId("captcha-result")).toHaveTextContent(`{"recaptcha_v3_token":"v3-browser-token"}`);
    expect(execute).toHaveBeenCalledWith("v3-site", { action: "sendEmailVerify" });
    expect(screen.queryByRole("dialog", { name: "人机验证" })).not.toBeInTheDocument();
  });

  it("binds Turnstile to the action and supports keyboard-accessible cancellation", async () => {
    let options: Record<string, unknown> | undefined;
    const remove = vi.fn();
    window.turnstile = {
      render: (_container, received) => { options = received; return "widget-1"; },
      reset: vi.fn(),
      remove
    };
    const user = userEvent.setup();
    render(<Harness config={captchaConfig({ captcha_type: "turnstile", turnstile_site_key: "turnstile-site" })} />);

    await user.click(screen.getByRole("button", { name: "注册动作" }));
    expect(await screen.findByRole("dialog", { name: "人机验证" })).toBeVisible();
    expect(options?.action).toBe("register");
    act(() => (options?.callback as (token: string) => void)("turnstile-browser-token"));
    expect(await screen.findByTestId("captcha-result")).toHaveTextContent(`{"turnstile_token":"turnstile-browser-token"}`);
    expect(remove).toHaveBeenCalledWith("widget-1");

    await user.click(screen.getByRole("button", { name: "注册动作" }));
    await user.keyboard("{Escape}");
    expect(await screen.findByRole("alert")).toHaveTextContent("验证码未完成");
  });

  it("does not load or execute a provider while CAPTCHA is disabled", async () => {
    const user = userEvent.setup();
    render(<Harness config={captchaConfig({ is_captcha: 0 })} />);
    await user.click(screen.getByRole("button", { name: "注册动作" }));
    expect(await screen.findByTestId("captcha-result")).toHaveTextContent("{}");
    expect(document.querySelector("script[data-captcha-provider]")).toBeNull();
  });
});

function Harness({ config }: { config: GuestConfig }) {
  const { requestCaptcha, challenge } = useCaptchaChallenge(config);
  const [result, setResult] = useState<CaptchaToken | null>(null);
  const [error, setError] = useState("");
  const run = (action: "register" | "sendEmailVerify") => {
    setError("");
    void requestCaptcha(action).then(setResult).catch((cause: unknown) => setError(cause instanceof Error ? cause.message : "验证失败"));
  };
  return <><button onClick={() => run("register")}>注册动作</button><button onClick={() => run("sendEmailVerify")}>邮箱动作</button>
    {result !== null && <output data-testid="captcha-result">{JSON.stringify(result)}</output>}
    {error !== "" && <div role="alert">{error}</div>}{challenge}</>;
}

function captchaConfig(overrides: Partial<GuestConfig>): GuestConfig {
  return {
    app_name: "Xboard-Go", app_description: null, app_url: null, tos_url: null, logo: null,
    is_email_verify: 0, is_invite_force: 0, enable_coupon_system: 1, email_whitelist_suffix: 0,
    is_captcha: 1, captcha_type: "recaptcha", recaptcha_site_key: "v2-site",
    recaptcha_v3_site_key: "v3-site", recaptcha_v3_score_threshold: 0.5,
    turnstile_site_key: "turnstile-site", is_recaptcha: 1,
    ...overrides
  };
}
