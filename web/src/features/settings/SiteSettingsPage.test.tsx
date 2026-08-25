import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { APIError, type SiteSettings } from "../../lib/api";
import { SiteSettingsPage } from "./SiteSettingsPage";

const initial: SiteSettings = {
  revision: 4,
  app_name: "Xboard-Go",
  app_description: "Existing description",
  app_url: "https://old.example.test",
  tos_url: "https://old.example.test/terms",
  logo: "https://old.example.test/logo.png",
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
  updated_at: "2026-08-24T12:00:00Z"
};

describe("SiteSettingsPage", () => {
  it("loads all legacy identity fields and publishes the saved identity", async () => {
    const updated: SiteSettings = {
      ...initial, revision: 5, app_name: "Example Board", app_description: "Fast control plane",
      app_url: "https://panel.example.test/", tos_url: "https://panel.example.test/terms/",
      logo: "https://images.example.test/brand.svg", stop_register: true,
      email_verify: true,
      email_whitelist_enable: true, email_whitelist_suffix: ["allowed.test", "gmail.com"],
      email_gmail_limit_enable: true, register_limit_by_ip_enable: true,
      register_limit_count: 2, register_limit_expire: 30,
      password_limit_enable: true, password_limit_count: 2, password_limit_expire: 30,
      invite_force: true, invite_gen_limit: 7, invite_never_expire: true,
      login_with_mail_link_enable: true
    };
    const api = {
      getSiteSettings: vi.fn().mockResolvedValue(initial),
      updateSiteSettings: vi.fn().mockResolvedValue(updated)
    };
    const onIdentityChanged = vi.fn();
    const user = userEvent.setup();
    render(<SiteSettingsPage api={api} onIdentityChanged={onIdentityChanged} />);

    expect(await screen.findByRole("heading", { name: "系统设置" })).toBeVisible();
    expect(screen.getByLabelText("站点名称")).toHaveValue("Xboard-Go");
    expect(screen.getByLabelText("站点描述")).toHaveValue("Existing description");
    expect(screen.getByLabelText("站点网址")).toHaveValue("https://old.example.test");
    expect(screen.getByLabelText("用户条款(TOS)URL")).toHaveValue("https://old.example.test/terms");
    expect(screen.getByLabelText("LOGO")).toHaveValue("https://old.example.test/logo.png");
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

    await user.clear(screen.getByLabelText("站点名称"));
    await user.type(screen.getByLabelText("站点名称"), updated.app_name);
    await user.clear(screen.getByLabelText("站点描述"));
    await user.type(screen.getByLabelText("站点描述"), updated.app_description);
    await user.clear(screen.getByLabelText("站点网址"));
    await user.type(screen.getByLabelText("站点网址"), updated.app_url);
    await user.clear(screen.getByLabelText("用户条款(TOS)URL"));
    await user.type(screen.getByLabelText("用户条款(TOS)URL"), updated.tos_url);
    await user.clear(screen.getByLabelText("LOGO"));
    await user.type(screen.getByLabelText("LOGO"), updated.logo);
    await user.click(screen.getByRole("checkbox", { name: "停止新用户注册" }));
    await user.click(screen.getByRole("checkbox", { name: "邮箱验证" }));
    await user.click(screen.getByRole("checkbox", { name: "邮箱后缀白名单" }));
    fireEvent.change(screen.getByLabelText("邮箱后缀"), { target: { value: "allowed.test\ngmail.com" } });
    await user.click(screen.getByRole("checkbox", { name: "禁止使用Gmail多别名" }));
    await user.click(screen.getByRole("checkbox", { name: "IP注册限制" }));
    await user.clear(screen.getByLabelText("注册次数"));
    await user.type(screen.getByLabelText("注册次数"), "2");
    await user.clear(screen.getByLabelText("限制时长（分钟）"));
    await user.type(screen.getByLabelText("限制时长（分钟）"), "30");
    await user.click(screen.getByRole("checkbox", { name: "强制邀请码" }));
    await user.clear(screen.getByLabelText("邀请码生成上限"));
    await user.type(screen.getByLabelText("邀请码生成上限"), "7");
    await user.click(screen.getByRole("checkbox", { name: "邀请码永不过期" }));
    await user.click(screen.getByRole("checkbox", { name: "邮件链接登录" }));
    await user.clear(screen.getByLabelText("密码错误次数"));
    await user.type(screen.getByLabelText("密码错误次数"), "2");
    await user.clear(screen.getByLabelText("登录锁定时长（分钟）"));
    await user.type(screen.getByLabelText("登录锁定时长（分钟）"), "30");
    await user.click(screen.getByRole("button", { name: "保存站点设置" }));

    await waitFor(() => expect(api.updateSiteSettings).toHaveBeenCalledWith({
      revision: 4,
      app_name: updated.app_name,
      app_description: updated.app_description,
      app_url: updated.app_url,
      tos_url: updated.tos_url,
      logo: updated.logo,
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
      login_with_mail_link_enable: true
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("站点设置已保存");
    expect(screen.getByLabelText("站点网址")).toHaveValue(updated.app_url);
    expect(screen.getByLabelText("LOGO")).toHaveValue(updated.logo);
    expect(onIdentityChanged).toHaveBeenCalledWith(updated);
    await user.type(screen.getByLabelText("站点名称"), " draft");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("supports retry after an initial load failure and preserves the form on conflict", async () => {
    const api = {
      getSiteSettings: vi.fn()
        .mockRejectedValueOnce(new Error("设置服务暂时不可用"))
        .mockResolvedValue(initial),
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
