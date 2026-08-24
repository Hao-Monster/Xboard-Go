import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { InvitationPage } from "./InvitationPage";

describe("InvitationPage", () => {
  it("loads the current user's invitation summary and generates a code", async () => {
    const initial = { codes: [], invited_count: 2 };
    const generated = { code: "Abcd1234", pv: 0, created_at: "2026-08-25T04:00:00Z" };
    const api = {
      getInvitations: vi.fn()
        .mockResolvedValueOnce(initial)
        .mockResolvedValueOnce({ codes: [generated], invited_count: 2 }),
      createInvitation: vi.fn().mockResolvedValue(generated)
    };
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<InvitationPage api={api} />);

    expect(await screen.findByRole("heading", { name: "我的邀请" })).toBeVisible();
    expect(await screen.findByText("2", { exact: true })).toBeVisible();
    expect(screen.getByText("邀请码管理", { exact: true })).toBeVisible();
    expect(screen.getByText("暂无可用邀请码", { exact: true })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "生成邀请码" }));
    await waitFor(() => expect(api.createInvitation).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("Abcd1234", { exact: true })).toBeVisible();
    expect(screen.getByText("0", { exact: true })).toBeVisible();
    expect(screen.getByRole("status")).toHaveTextContent("邀请码已生成");
    expect(api.getInvitations).toHaveBeenCalledTimes(2);
    await user.click(screen.getByRole("button", { name: "复制链接" }));
    expect(writeText).toHaveBeenCalledWith(`${window.location.origin}/#/register?code=Abcd1234`);
    expect(screen.getByRole("status")).toHaveTextContent("邀请链接已复制");
  });

  it("keeps existing data visible when generation reaches the limit", async () => {
    const api = {
      getInvitations: vi.fn().mockResolvedValue({
        codes: [{ code: "Keep1234", pv: 7, created_at: "2026-08-25T04:00:00Z" }], invited_count: 1
      }),
      createInvitation: vi.fn().mockRejectedValue(new Error("已达到创建数量上限"))
    };
    const user = userEvent.setup();
    render(<InvitationPage api={api} />);

    expect(await screen.findByText("Keep1234", { exact: true })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "生成邀请码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("已达到创建数量上限");
    expect(screen.getByText("Keep1234", { exact: true })).toBeVisible();
    expect(api.getInvitations).toHaveBeenCalledTimes(1);
  });
});
