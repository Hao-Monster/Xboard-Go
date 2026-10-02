import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PortalAnnouncement } from "./PortalAnnouncement";

describe("PortalAnnouncement", () => {
  it("opens the actual published notice without rendering title HTML", async () => {
    const onOpen = vi.fn();
    const api = { listVisibleNotices: vi.fn().mockResolvedValue({ items: [{ title: "<b>服务公告</b>", updated_at: "2026-10-02T00:00:00Z", image_url: null }] }) };
    render(<PortalAnnouncement api={api} onOpen={onOpen} />);
    await userEvent.click(await screen.findByRole("button", { name: "查看公告：<b>服务公告</b>" }));
    expect(onOpen).toHaveBeenCalledOnce();
    expect(screen.getByText("<b>服务公告</b>")).toBeVisible();
  });
  it("reports a failed request and retains a path to the notices page", async () => {
    const onOpen = vi.fn();
    render(<PortalAnnouncement api={{ listVisibleNotices: vi.fn().mockRejectedValue(new Error("网络不可用")) }} onOpen={onOpen} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("网络不可用");
    await userEvent.click(screen.getByRole("button", { name: "查看公告" }));
    expect(onOpen).toHaveBeenCalledOnce();
  });
});
