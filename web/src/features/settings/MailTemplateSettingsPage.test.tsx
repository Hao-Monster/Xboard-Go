import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { MailTemplate } from "../../lib/api";
import { MailTemplateSettingsPage } from "./MailTemplateSettingsPage";

const templates: MailTemplate[] = [
  { name: "verify", label: "邮箱验证码", subject: "{{name}} - 邮箱验证码", content: "<p>{{code}}</p>", required_variables: ["code"], optional_variables: ["name", "url"], customized: false, revision: 1, updated_at: "1970-01-01T00:00:00Z" },
  { name: "notify", label: "站点通知", subject: "{{name}} - 站点通知", content: "<p>{{content}}</p>", required_variables: ["content"], optional_variables: ["name", "url"], customized: false, revision: 1, updated_at: "1970-01-01T00:00:00Z" }
];
const verifyTemplate = templates[0]!;

afterEach(() => vi.unstubAllGlobals());

describe("MailTemplateSettingsPage", () => {
  it("loads all templates, preserves unsaved edits, previews, saves, resets and sends tests", async () => {
    const updated = { ...verifyTemplate, subject: "{{name}} - 新验证码", customized: true, revision: 2 };
    const api = {
      listMailTemplates: vi.fn().mockResolvedValue(templates),
      getMailTemplate: vi.fn((name: string) => Promise.resolve(templates.find((item) => item.name === name)!)),
      updateMailTemplate: vi.fn().mockResolvedValue(updated),
      resetMailTemplate: vi.fn().mockResolvedValue({ ...verifyTemplate, revision: 3 }),
      previewMailTemplate: vi.fn().mockResolvedValue({ subject: "Xboard-Go - 新验证码", html: "<p>123456</p>", text: "123456" }),
      testMailTemplate: vi.fn().mockResolvedValue({ recipient: "admin@example.test" })
    };
    const user = userEvent.setup();
    const confirm = vi.fn().mockReturnValueOnce(false).mockReturnValue(true);
    vi.stubGlobal("confirm", confirm);
    render(<MailTemplateSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "邮箱验证码" })).toBeVisible();
    expect(screen.getByRole("button", { name: /站点通知/ })).toBeVisible();
    expect(screen.queryByRole("button", { name: "恢复默认" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("邮件主题"), { target: { value: updated.subject } });
    expect(screen.getByRole("button", { name: "发送测试邮件" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: /站点通知/ }));
    expect(confirm).toHaveBeenCalledWith("当前模板有未保存的修改，确认切换并放弃这些修改吗？");
    expect(screen.getByRole("heading", { name: "邮箱验证码" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "预览" }));
    const preview = await screen.findByRole("region", { name: "邮件模板预览" });
    expect(within(preview).getByTitle("邮件 HTML 预览")).toHaveAttribute("sandbox", "");
    expect(api.previewMailTemplate).toHaveBeenCalledWith("verify", updated.subject, verifyTemplate.content);
    await user.click(screen.getByRole("button", { name: "保存模板" }));
    await waitFor(() => expect(api.updateMailTemplate).toHaveBeenCalledWith("verify", 1, updated.subject, verifyTemplate.content));
    expect(await screen.findByRole("status")).toHaveTextContent("邮件模板已保存");
    expect(screen.getByRole("button", { name: "恢复默认" })).toBeVisible();

    fireEvent.change(screen.getByLabelText("测试收件人"), { target: { value: "admin@example.test" } });
    await user.click(screen.getByRole("button", { name: "发送测试邮件" }));
    expect(api.testMailTemplate).toHaveBeenCalledWith("verify", "admin@example.test");
    expect(await screen.findByRole("status")).toHaveTextContent("测试邮件已发送至 admin@example.test");

    await user.click(screen.getByRole("button", { name: "恢复默认" }));
    await waitFor(() => expect(api.resetMailTemplate).toHaveBeenCalledWith("verify", 2));
    expect(await screen.findByRole("status")).toHaveTextContent("已恢复默认模板");
  });

  it("inserts only allowlisted variables at the editor selection", async () => {
    const api = {
      listMailTemplates: vi.fn().mockResolvedValue(templates), getMailTemplate: vi.fn().mockResolvedValue(verifyTemplate),
      updateMailTemplate: vi.fn(), resetMailTemplate: vi.fn(), previewMailTemplate: vi.fn(), testMailTemplate: vi.fn()
    };
    const user = userEvent.setup();
    render(<MailTemplateSettingsPage api={api} />);
    const editor = await screen.findByLabelText<HTMLTextAreaElement>("HTML 内容");
    editor.setSelectionRange(3, 3);
    await user.click(screen.getByRole("button", { name: "{{name}}" }));
    expect(editor.value).toBe("<p>{{name}}{{code}}</p>");
  });

  it("recovers the editor after an initial catalog request fails", async () => {
    const api = {
      listMailTemplates: vi.fn().mockRejectedValueOnce(new Error("暂时不可用")).mockResolvedValue(templates),
      getMailTemplate: vi.fn().mockResolvedValue(verifyTemplate),
      updateMailTemplate: vi.fn(), resetMailTemplate: vi.fn(), previewMailTemplate: vi.fn(), testMailTemplate: vi.fn()
    };
    const user = userEvent.setup();
    render(<MailTemplateSettingsPage api={api} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("暂时不可用");
    await user.click(screen.getByRole("button", { name: "重新加载" }));
    expect(await screen.findByRole("heading", { name: "邮箱验证码" })).toBeVisible();
    expect(api.listMailTemplates).toHaveBeenCalledTimes(2);
    expect(api.getMailTemplate).toHaveBeenCalledWith("verify");
  });
});
