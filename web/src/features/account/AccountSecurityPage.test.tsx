import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AccountSecurityPage } from "./AccountSecurityPage";
import type { AccountSecurityAPI, AccountSession } from "../../lib/api";

const sessions: AccountSession[] = [
  {
    id: 22,
    is_current: false,
    created_at: "2026-08-24T02:00:00Z",
    last_used_at: "2026-08-24T03:00:00Z",
    expires_at: "2026-08-24T14:00:00Z"
  },
  {
    id: 11,
    is_current: true,
    created_at: "2026-08-24T01:00:00Z",
    last_used_at: "2026-08-24T03:30:00Z",
    expires_at: "2026-08-24T13:00:00Z"
  }
];

describe("AccountSecurityPage", () => {
  it("shows a load failure and retries without a false empty state", async () => {
    const user = userEvent.setup();
    const api = makeAPI();
    api.listAccountSessions.mockRejectedValueOnce(new Error("活动会话加载失败"));
    render(<AccountSecurityPage api={api} onSignedOut={vi.fn()} />);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("活动会话加载失败");
    expect(screen.queryByText("没有活动会话。")).not.toBeInTheDocument();
    await user.click(within(alert).getByRole("button", { name: "重试" }));

    expect(await screen.findByText("当前会话")).toBeVisible();
    expect(api.listAccountSessions).toHaveBeenCalledTimes(2);
  });

  it("lists active sessions and revokes another session without signing out the current one", async () => {
    const user = userEvent.setup();
    const api = makeAPI();
    const onSignedOut = vi.fn();
    render(<AccountSecurityPage api={api} onSignedOut={onSignedOut} />);

    expect(await screen.findByText("当前会话")).toBeVisible();
    const other = screen.getByTestId("account-session-22");
    await user.click(within(other).getByRole("button", { name: "撤销会话" }));

    expect(api.revokeAccountSession).toHaveBeenCalledWith(22);
    expect(screen.queryByTestId("account-session-22")).not.toBeInTheDocument();
    expect(screen.getByTestId("account-session-11")).toBeVisible();
    expect(onSignedOut).not.toHaveBeenCalled();
  });

  it("keeps a session visible and offers retry when revocation fails", async () => {
    const user = userEvent.setup();
    const api = makeAPI();
    api.revokeAccountSession.mockRejectedValueOnce(new Error("会话撤销失败"));
    render(<AccountSecurityPage api={api} onSignedOut={vi.fn()} />);

    const other = await screen.findByTestId("account-session-22");
    await user.click(within(other).getByRole("button", { name: "撤销会话" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("会话撤销失败");
    expect(screen.getByTestId("account-session-22")).toBeVisible();
    expect(within(other).getByRole("button", { name: "撤销会话" })).toBeEnabled();
  });

  it("validates password confirmation, preserves input on failure, and signs out after success", async () => {
    const user = userEvent.setup();
    const api = makeAPI();
    const onSignedOut = vi.fn();
    render(<AccountSecurityPage api={api} onSignedOut={onSignedOut} />);
    await screen.findByText("当前会话");

    const oldPassword = screen.getByLabelText("当前密码");
    const newPassword = screen.getByLabelText("新密码");
    const confirmPassword = screen.getByLabelText("确认新密码");
    await user.type(oldPassword, "admin-password-123");
    await user.type(newPassword, "replacement-password-456");
    await user.type(confirmPassword, "different-password-789");
    await user.click(screen.getByRole("button", { name: "修改密码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("两次输入的新密码不一致");
    expect(api.changePassword).not.toHaveBeenCalled();

    await user.clear(confirmPassword);
    await user.type(confirmPassword, "replacement-password-456");
    api.changePassword.mockRejectedValueOnce(new Error("当前密码不正确"));
    await user.click(screen.getByRole("button", { name: "修改密码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("当前密码不正确");
    expect(oldPassword).toHaveValue("admin-password-123");
    expect(newPassword).toHaveValue("replacement-password-456");

    await user.click(screen.getByRole("button", { name: "修改密码" }));
    expect(api.changePassword).toHaveBeenLastCalledWith("admin-password-123", "replacement-password-456");
    expect(onSignedOut).toHaveBeenCalledOnce();
  });

  it("signs out when the current session is explicitly revoked", async () => {
    const user = userEvent.setup();
    const api = makeAPI();
    const onSignedOut = vi.fn();
    render(<AccountSecurityPage api={api} onSignedOut={onSignedOut} />);

    const current = await screen.findByTestId("account-session-11");
    await user.click(within(current).getByRole("button", { name: "退出当前会话" }));

    expect(api.revokeAccountSession).toHaveBeenCalledWith(11);
    expect(onSignedOut).toHaveBeenCalledOnce();
  });
});

function makeAPI() {
  return {
    listAccountSessions: vi.fn<() => Promise<AccountSession[]>>().mockResolvedValue(sessions.map((session) => ({ ...session }))),
    revokeAccountSession: vi.fn<(id: number) => Promise<void>>().mockResolvedValue(undefined),
    changePassword: vi.fn<(oldPassword: string, newPassword: string) => Promise<void>>().mockResolvedValue(undefined)
  } satisfies AccountSecurityAPI;
}
