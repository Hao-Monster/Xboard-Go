import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { APIError, type Plan, type SiteSettings } from "../../lib/api";
import { SiteSettingsPage } from "./SiteSettingsPage";

const initial: SiteSettings = {
  revision: 4,
  app_name: "Xboard-Go",
  app_description: "Existing description",
  app_url: "https://old.example.test",
  safe_mode_enable: false,
  secure_path: "admin-path",
  force_https: false,
  subscribe_url: "",
  tos_url: "https://old.example.test/terms",
  logo: "https://old.example.test/logo.png",
  currency: "CNY",
  currency_symbol: "¥",
  stop_register: false,
  email_verify: false,
  email_whitelist_enable: false,
  email_whitelist_suffix: ["gmail.com", "qq.com"],
  email_gmail_limit_enable: false,
  register_limit_by_ip_enable: false,
  register_limit_count: 3,
  register_limit_expire: 60,
  password_limit_enable: true,
  password_limit_count: 5,
  password_limit_expire: 60,
  invite_force: false,
  invite_gen_limit: 5,
  invite_never_expire: false,
  login_with_mail_link_enable: false,
  try_out_plan_id: 0,
  try_out_hour: 1,
  traffic_reset_method: 1,
  coupon_enabled: true,
  captcha_enable: false,
  captcha_type: "recaptcha",
  recaptcha_site_key: "",
  recaptcha_secret_configured: false,
  recaptcha_v3_site_key: "",
  recaptcha_v3_score_threshold: 0.5,
  recaptcha_v3_secret_configured: false,
  turnstile_site_key: "",
  turnstile_secret_configured: false,
  updated_at: "2026-08-24T12:00:00Z"
};

describe("SiteSettingsPage", () => {
  it("loads all legacy identity fields and publishes the saved identity", async () => {
    const plans = [{ id: 17, name: "试用套餐" }] as Plan[];
    const updated: SiteSettings = {
      ...initial, revision: 5, app_name: "Example Board", app_description: "Fast control plane",
      app_url: "https://panel.example.test/", tos_url: "https://panel.example.test/terms/",
      safe_mode_enable: true, secure_path: "secure-admin-01",
      force_https: true, subscribe_url: "https://subscribe-a.example.test,https://subscribe-b.example.test/root",
      logo: "https://images.example.test/brand.svg", stop_register: true,
      currency: "USD", currency_symbol: "$",
      email_verify: true,
      email_whitelist_enable: true, email_whitelist_suffix: ["allowed.test", "gmail.com"],
      email_gmail_limit_enable: true, register_limit_by_ip_enable: true,
      register_limit_count: 2, register_limit_expire: 30,
      password_limit_enable: true, password_limit_count: 2, password_limit_expire: 30,
      invite_force: true, invite_gen_limit: 7, invite_never_expire: true,
      login_with_mail_link_enable: true,
      try_out_plan_id: 17,
      try_out_hour: 48,
      traffic_reset_method: 1,
      coupon_enabled: false,
      captcha_enable: true, captcha_type: "turnstile", turnstile_site_key: "turnstile-site",
      turnstile_secret_configured: true
    };
    const api = {
      getSiteSettings: vi.fn().mockResolvedValue(initial),
      listPlans: vi.fn().mockResolvedValue(plans),
      updateSiteSettings: vi.fn().mockResolvedValue(updated)
    };
    const onIdentityChanged = vi.fn();
    const user = userEvent.setup();
    render(<SiteSettingsPage api={api} onIdentityChanged={onIdentityChanged} />);

    expect(await screen.findByRole("heading", { name: "系统设置" })).toBeVisible();
    expect(screen.getByLabelText("站点名称")).toHaveValue("Xboard-Go");
    expect(screen.getByLabelText("站点描述")).toHaveValue("Existing description");
    expect(screen.getByLabelText("站点网址")).toHaveValue("https://old.example.test");
    expect(screen.getByRole("checkbox", { name: "安全模式（仅允许站点网址的域名访问前端）" })).not.toBeChecked();
    expect(screen.getByLabelText("管理员安全路径")).toHaveValue("admin-path");
    expect(screen.getByRole("checkbox", { name: "强制使用 HTTPS 生成公开地址" })).not.toBeChecked();
    expect(screen.getByLabelText("订阅公开地址")).toHaveValue("");
    expect(screen.getByLabelText("用户条款(TOS)URL")).toHaveValue("https://old.example.test/terms");
    expect(screen.getByLabelText("LOGO")).toHaveValue("https://old.example.test/logo.png");
    expect(screen.getByLabelText("货币代码")).toHaveValue("CNY");
    expect(screen.getByLabelText("货币符号")).toHaveValue("¥");
    expect(screen.getByRole("checkbox", { name: "停止新用户注册" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "邮箱验证" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "邮箱后缀白名单" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "禁止使用Gmail多别名" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "IP注册限制" })).not.toBeChecked();
    expect(screen.queryByLabelText("邮箱后缀")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("注册次数")).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "强制邀请码" })).not.toBeChecked();
    expect(screen.getByLabelText("邀请码生成上限")).toHaveValue(5);
    expect(screen.getByRole("checkbox", { name: "邀请码永不过期" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "邮件链接登录" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "密码错误次数限制" })).toBeChecked();
    expect(screen.getByLabelText("密码错误次数")).toHaveValue(5);
    expect(screen.getByLabelText("登录锁定时长（分钟）")).toHaveValue(60);
    expect(screen.getByRole("checkbox", { name: "验证码" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "启用优惠券" })).toBeChecked();
    expect(screen.getByLabelText("注册试用")).toHaveValue("0");
    expect(screen.queryByLabelText("注册试用时长")).not.toBeInTheDocument();

    changeValue("站点名称", updated.app_name);
    changeValue("站点描述", updated.app_description);
    changeValue("站点网址", updated.app_url);
    await user.click(screen.getByRole("checkbox", { name: "安全模式（仅允许站点网址的域名访问前端）" }));
    changeValue("管理员安全路径", "secure-admin-01");
    await user.click(screen.getByRole("checkbox", { name: "强制使用 HTTPS 生成公开地址" }));
    fireEvent.change(screen.getByLabelText("订阅公开地址"), { target: { value: " https://subscribe-a.example.test/\nhttps://subscribe-b.example.test/root " } });
    changeValue("用户条款(TOS)URL", updated.tos_url);
    changeValue("LOGO", updated.logo);
    changeValue("货币代码", updated.currency.toLowerCase());
    expect(screen.getByLabelText("货币代码")).toHaveValue("USD");
    changeValue("货币符号", updated.currency_symbol);
    await user.click(screen.getByRole("checkbox", { name: "停止新用户注册" }));
    await user.click(screen.getByRole("checkbox", { name: "启用优惠券" }));
    await user.click(screen.getByRole("checkbox", { name: "验证码" }));
    await user.selectOptions(screen.getByLabelText("验证码类型"), "turnstile");
    changeValue("Turnstile 站点密钥", "turnstile-site");
    changeValue("Turnstile 服务端密钥", "private-turnstile-secret");
    await user.click(screen.getByRole("checkbox", { name: "邮箱验证" }));
    await user.click(screen.getByRole("checkbox", { name: "邮箱后缀白名单" }));
    fireEvent.change(screen.getByLabelText("邮箱后缀"), { target: { value: "allowed.test\ngmail.com" } });
    await user.click(screen.getByRole("checkbox", { name: "禁止使用Gmail多别名" }));
    await user.click(screen.getByRole("checkbox", { name: "IP注册限制" }));
    changeValue("注册次数", "2");
    changeValue("限制时长（分钟）", "30");
    await user.click(screen.getByRole("checkbox", { name: "强制邀请码" }));
    changeValue("邀请码生成上限", "7");
    await user.click(screen.getByRole("checkbox", { name: "邀请码永不过期" }));
    await user.click(screen.getByRole("checkbox", { name: "邮件链接登录" }));
    await user.selectOptions(screen.getByLabelText("注册试用"), "17");
    changeValue("注册试用时长", "48");
    changeValue("密码错误次数", "2");
    changeValue("登录锁定时长（分钟）", "30");
    await user.click(screen.getByRole("button", { name: "保存站点设置" }));

    await waitFor(() => expect(api.updateSiteSettings).toHaveBeenCalledWith({
      revision: 4,
      app_name: updated.app_name,
      app_description: updated.app_description,
      app_url: updated.app_url,
      safe_mode_enable: true,
      secure_path: "secure-admin-01",
      force_https: true,
      subscribe_url: " https://subscribe-a.example.test/\nhttps://subscribe-b.example.test/root ",
      tos_url: updated.tos_url,
      logo: updated.logo,
      currency: updated.currency,
      currency_symbol: updated.currency_symbol,
      stop_register: true,
      email_verify: true,
      email_whitelist_enable: true,
      email_whitelist_suffix: ["allowed.test", "gmail.com"],
      email_gmail_limit_enable: true,
      register_limit_by_ip_enable: true,
      register_limit_count: 2,
      register_limit_expire: 30,
      password_limit_enable: true,
      password_limit_count: 2,
      password_limit_expire: 30,
      invite_force: true,
      invite_gen_limit: 7,
      invite_never_expire: true,
      login_with_mail_link_enable: true,
      try_out_plan_id: 17,
      try_out_hour: 48,
      traffic_reset_method: 1,
      coupon_enabled: false,
      captcha_enable: true,
      captcha_type: "turnstile",
      recaptcha_site_key: "",
      recaptcha_secret: "",
      clear_recaptcha_secret: false,
      recaptcha_v3_site_key: "",
      recaptcha_v3_score_threshold: 0.5,
      recaptcha_v3_secret: "",
      clear_recaptcha_v3_secret: false,
      turnstile_site_key: "turnstile-site",
      turnstile_secret: "private-turnstile-secret",
      clear_turnstile_secret: false
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("站点设置已保存");
    expect(screen.getByLabelText("站点网址")).toHaveValue(updated.app_url);
    expect(screen.getByLabelText("LOGO")).toHaveValue(updated.logo);
    expect(onIdentityChanged).toHaveBeenCalledWith(updated);
    changeValue("站点名称", `${updated.app_name} draft`);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("supports retry after an initial load failure and preserves the form on conflict", async () => {
    const api = {
      getSiteSettings: vi.fn()
        .mockRejectedValueOnce(new Error("设置服务暂时不可用"))
        .mockResolvedValue(initial),
      listPlans: vi.fn().mockResolvedValue([]),
      updateSiteSettings: vi.fn().mockRejectedValue(new APIError(409, "settings_conflict", "设置已被其他管理员修改，请刷新后重试"))
    };
    const user = userEvent.setup();
    render(<SiteSettingsPage api={api} onIdentityChanged={vi.fn()} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("设置服务暂时不可用");
    await user.click(screen.getByRole("button", { name: "重新加载站点设置" }));
    expect(await screen.findByLabelText("站点名称")).toHaveValue("Xboard-Go");
    await user.clear(screen.getByLabelText("站点名称"));
    await user.type(screen.getByLabelText("站点名称"), "Conflict Board");
    await user.click(screen.getByRole("button", { name: "保存站点设置" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改，请刷新后重试");
    expect(screen.getByLabelText("站点名称")).toHaveValue("Conflict Board");
    expect(screen.getByRole("button", { name: "刷新最新设置" })).toBeVisible();
  });
});

function changeValue(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
