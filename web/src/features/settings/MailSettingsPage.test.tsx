import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { APIError, type MailSettings } from "../../lib/api";
import { MailSettingsPage } from "./MailSettingsPage";

const initial: MailSettings = {
  revision: 4,
  smtp_enabled: true,
  smtp_host: "smtp.old.example.test",
  smtp_port: 587,
  smtp_username: "old-mailer",
  smtp_password_set: true,
  smtp_encryption: "starttls",
  smtp_from_address: "support@old.example.test",
  remind_mail_enable: false,
  updated_at: "2026-08-29T03:30:00Z"
};

describe("MailSettingsPage", () => {
  it("renders every legacy mail setting on first load, saves safely, and sends a test mail", async () => {
    const updated: MailSettings = {
      ...initial,
      revision: 5,
      smtp_host: "smtp.example.test",
      smtp_port: 465,
      smtp_username: "mailer",
      smtp_encryption: "tls",
      smtp_from_address: "support@example.test",
      remind_mail_enable: true
    };
    const api = {
      getMailSettings: vi.fn().mockResolvedValue(initial),
      updateMailSettings: vi.fn().mockResolvedValue(updated),
      testMailSettings: vi.fn().mockResolvedValue({ recipient: "admin@example.test" })
    };
    const user = userEvent.setup();
    render(<MailSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "邮件设置" })).toBeVisible();
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue(initial.smtp_host);
    expect(screen.getByLabelText("SMTP 端口")).toHaveValue(initial.smtp_port);
    expect(screen.getByLabelText("SMTP 用户名")).toHaveValue(initial.smtp_username);
    expect(screen.getByLabelText("SMTP 密码")).toHaveValue("");
    expect(screen.getByLabelText("SMTP 密码")).toHaveAttribute("placeholder", "已安全保存；留空保持不变");
    expect(screen.getByLabelText("加密方式")).toHaveValue("starttls");
    expect(screen.getByLabelText("发件人地址")).toHaveValue(initial.smtp_from_address);
    expect(screen.getByRole("checkbox", { name: "启用 SMTP 邮件服务" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "启用订阅到期和流量提醒" })).not.toBeChecked();

    changeValue("SMTP 主机", updated.smtp_host);
    changeValue("SMTP 端口", String(updated.smtp_port));
    changeValue("SMTP 用户名", updated.smtp_username);
    changeValue("SMTP 密码", "new-smtp-password");
    await user.selectOptions(screen.getByLabelText("加密方式"), "tls");
    changeValue("发件人地址", updated.smtp_from_address);
    await user.click(screen.getByRole("checkbox", { name: "启用订阅到期和流量提醒" }));
    await user.click(screen.getByRole("button", { name: "保存邮件设置" }));

    await waitFor(() => expect(api.updateMailSettings).toHaveBeenCalledWith({
      revision: 4,
      smtp_enabled: true,
      smtp_host: updated.smtp_host,
      smtp_port: 465,
      smtp_username: updated.smtp_username,
      smtp_password: "new-smtp-password",
      smtp_encryption: "tls",
      smtp_from_address: updated.smtp_from_address,
      remind_mail_enable: true
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("邮件设置已保存");
    expect(screen.getByLabelText("SMTP 密码")).toHaveValue("");

    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    expect(await screen.findByRole("status")).toHaveTextContent("测试邮件已发送至 admin@example.test");
    expect(api.testMailSettings).toHaveBeenCalledOnce();
  });

  it("supports explicit password clearing and preserves the draft after a revision conflict", async () => {
    const api = {
      getMailSettings: vi.fn().mockResolvedValue(initial),
      updateMailSettings: vi.fn().mockRejectedValue(new APIError(409, "settings_conflict", "设置已被其他管理员修改，请刷新后重试")),
      testMailSettings: vi.fn()
    };
    const user = userEvent.setup();
    render(<MailSettingsPage api={api} />);

    expect(await screen.findByLabelText("SMTP 主机")).toHaveValue(initial.smtp_host);
    changeValue("SMTP 主机", "draft.example.test");
    await user.click(screen.getByRole("checkbox", { name: "清除已保存的 SMTP 密码" }));
    await user.click(screen.getByRole("button", { name: "保存邮件设置" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改，请刷新后重试");
    expect(screen.getByLabelText("SMTP 主机")).toHaveValue("draft.example.test");
    expect(api.updateMailSettings).toHaveBeenCalledWith(expect.objectContaining({ smtp_password: "" }));
    expect(screen.getByRole("button", { name: "刷新最新设置" })).toBeVisible();
  });
});

function changeValue(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}
