import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { Modal } from "../../components/Overlay";
import type { CaptchaToken, GuestConfig } from "../../lib/api";

type CaptchaAction = "register" | "sendEmailVerify";

interface PendingChallenge {
  action: CaptchaAction;
  resolve: (token: CaptchaToken) => void;
  reject: (error: Error) => void;
}

interface GoogleRecaptchaAPI {
  ready: (callback: () => void) => void;
  execute: (siteKey: string, options: { action: string }) => Promise<string>;
  render: (container: HTMLElement, options: Record<string, unknown>) => number;
  reset: (widgetID?: number) => void;
}

interface TurnstileAPI {
  render: (container: HTMLElement, options: Record<string, unknown>) => string;
  reset: (widgetID?: string) => void;
  remove?: (widgetID: string) => void;
}

declare global {
  interface Window {
    grecaptcha?: GoogleRecaptchaAPI;
    turnstile?: TurnstileAPI;
  }
}

export function useCaptchaChallenge(config: GuestConfig): {
  requestCaptcha: (action: CaptchaAction) => Promise<CaptchaToken>;
  challenge: ReactNode;
} {
  const activeRef = useRef<PendingChallenge | null>(null);
  const [pending, setPending] = useState<PendingChallenge | null>(null);

  const finish = useCallback((token: CaptchaToken) => {
    const active = activeRef.current;
    activeRef.current = null;
    setPending(null);
    active?.resolve(token);
  }, []);

  const cancel = useCallback((message = "验证码未完成") => {
    const active = activeRef.current;
    activeRef.current = null;
    setPending(null);
    active?.reject(new Error(message));
  }, []);

  useEffect(() => () => {
    activeRef.current?.reject(new Error("验证码已取消"));
    activeRef.current = null;
  }, []);

  const requestCaptcha = useCallback(async (action: CaptchaAction): Promise<CaptchaToken> => {
    if (config.is_captcha !== 1) return {};
    if (activeRef.current !== null) throw new Error("请先完成当前验证码");
    if (config.captcha_type === "recaptcha-v3") {
      const siteKey = requiredSiteKey(config.recaptcha_v3_site_key);
      await loadProviderScript("recaptcha-v3", `https://www.google.com/recaptcha/api.js?render=${encodeURIComponent(siteKey)}`, () => window.grecaptcha !== undefined);
      const token = await withTimeout(new Promise<string>((resolve, reject) => {
        const api = window.grecaptcha;
        if (api === undefined) {
          reject(new Error("验证码加载失败，请重试"));
          return;
        }
        api.ready(() => void api.execute(siteKey, { action }).then(resolve, reject));
      }));
      if (token === "") throw new Error("验证码有误");
      return { recaptcha_v3_token: token };
    }
    requiredSiteKey(config.captcha_type === "turnstile" ? config.turnstile_site_key : config.recaptcha_site_key);
    return new Promise<CaptchaToken>((resolve, reject) => {
      const challenge = { action, resolve, reject };
      activeRef.current = challenge;
      setPending(challenge);
    });
  }, [config]);

  return {
    requestCaptcha,
    challenge: pending === null ? null : <InteractiveCaptchaModal config={config} action={pending.action} onToken={finish} onCancel={cancel} />
  };
}

function InteractiveCaptchaModal({ config, action, onToken, onCancel }: {
  config: GuestConfig;
  action: CaptchaAction;
  onToken: (token: CaptchaToken) => void;
  onCancel: (message?: string) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let active = true;
    let cleanup: () => void = () => undefined;
    const load = async () => {
      try {
        if (config.captcha_type === "turnstile") {
          const siteKey = requiredSiteKey(config.turnstile_site_key);
          await loadProviderScript("turnstile", "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit", () => window.turnstile !== undefined);
          if (!active || containerRef.current === null || window.turnstile === undefined) return;
          const api = window.turnstile;
          const widgetID = api.render(containerRef.current, {
            sitekey: siteKey,
            action,
            callback: (token: string) => onToken({ turnstile_token: token }),
            "expired-callback": () => setError("验证码已过期，请重新验证"),
            "error-callback": () => setError("验证码加载失败，请重试")
          });
          cleanup = () => {
            if (api.remove !== undefined) api.remove(widgetID);
            else api.reset(widgetID);
          };
          return;
        }
        const siteKey = requiredSiteKey(config.recaptcha_site_key);
        await loadProviderScript("recaptcha", "https://www.google.com/recaptcha/api.js?render=explicit", () => window.grecaptcha !== undefined);
        if (!active || containerRef.current === null || window.grecaptcha === undefined) return;
        const api = window.grecaptcha;
        const widgetID = api.render(containerRef.current, {
          sitekey: siteKey,
          callback: (token: string) => onToken({ recaptcha_data: token }),
          "expired-callback": () => setError("验证码已过期，请重新验证"),
          "error-callback": () => setError("验证码加载失败，请重试")
        });
        cleanup = () => api.reset(widgetID);
      } catch (cause) {
        if (active) setError(cause instanceof Error ? cause.message : "验证码加载失败，请重试");
      }
    };
    void load();
    return () => {
      active = false;
      cleanup();
    };
  }, [action, attempt, config.captcha_type, config.recaptcha_site_key, config.turnstile_site_key, onToken]);

  return <Modal title="人机验证" onClose={() => onCancel()}>
    <div className="modal-header"><div><p className="eyebrow">Security</p><h2>人机验证</h2></div><button className="icon-button" type="button" aria-label="关闭人机验证" onClick={() => onCancel()}>×</button></div>
    <p className="muted small">完成验证后将自动继续当前操作。</p>
    <div ref={containerRef} className="captcha-widget" aria-label="验证码组件" />
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions">{error !== "" && <button className="button secondary" type="button" onClick={() => { setError(""); setAttempt((value) => value + 1); }}>重试</button>}<button className="button ghost" type="button" onClick={() => onCancel()}>取消</button></div>
  </Modal>;
}

function requiredSiteKey(value: string | null): string {
  if (value === null || value.trim() === "") throw new Error("验证码配置不完整");
  return value;
}

const scriptPromises = new Map<string, Promise<void>>();

export function resetCaptchaProviderScripts() {
  scriptPromises.clear();
  document.querySelectorAll("script[data-captcha-provider]").forEach((script) => script.remove());
  delete window.grecaptcha;
  delete window.turnstile;
}

function loadProviderScript(provider: string, source: string, ready: () => boolean): Promise<void> {
  if (ready()) return Promise.resolve();
  const existing = scriptPromises.get(provider);
  if (existing !== undefined) return existing;
  const promise = withTimeout(new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = source;
    script.async = true;
    script.defer = true;
    script.dataset.captchaProvider = provider;
    script.onload = () => ready() ? resolve() : reject(new Error("验证码加载失败，请重试"));
    script.onerror = () => reject(new Error("验证码加载失败，请重试"));
    document.head.append(script);
  })).catch((error: unknown) => {
    scriptPromises.delete(provider);
    throw error;
  });
  scriptPromises.set(provider, promise);
  return promise;
}

function withTimeout<T>(promise: Promise<T>, milliseconds = 15_000): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error("验证码加载超时，请重试")), milliseconds);
    promise.then((value) => { window.clearTimeout(timer); resolve(value); }, (error: unknown) => {
      window.clearTimeout(timer);
      reject(error instanceof Error ? error : new Error("验证码加载失败，请重试"));
    });
  });
}
