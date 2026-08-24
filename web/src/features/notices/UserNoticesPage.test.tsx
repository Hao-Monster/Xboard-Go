import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { NoticePage, UserSession } from "../../lib/api";
import { UserNoticesPage } from "./UserNoticesPage";

const session: UserSession = { id: 12, email: "user@example.test", is_admin: false };

describe("UserNoticesPage", () => {
  it("renders only server-provided visible pages as safe markdown and paginates", async () => {
    const first: NoticePage = {
      items: [{
        id: 1, sort: 1, title: "Service update",
        content: "**Available now** <img src=x onerror=alert(1)> [unsafe](javascript:alert(2))", image_url: null,
        tags: ["news"], show: true, revision: 1,
        created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z"
      }],
      total: 6, page: 1, page_size: 5
    };
    const second: NoticePage = { items: [], total: 6, page: 2, page_size: 5 };
    const api = {
      listVisibleNotices: vi.fn().mockResolvedValueOnce(first).mockResolvedValueOnce(second),
      logout: vi.fn().mockResolvedValue(undefined)
    };
    const signedOut = vi.fn();
    const user = userEvent.setup();
    render(<UserNoticesPage api={api} session={session} onSignedOut={signedOut} />);

    expect(await screen.findByRole("heading", { name: "Service update" })).toBeVisible();
    expect(screen.getByText("Available now", { selector: "strong" })).toBeVisible();
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText(/<img src=x onerror=alert\(1\)>/)).toBeVisible();
    expect(screen.getByText("unsafe", { selector: "a" }).getAttribute("href")).not.toMatch(/^javascript:/i);

    await user.click(screen.getByRole("button", { name: "下一页" }));
    await waitFor(() => expect(api.listVisibleNotices).toHaveBeenLastCalledWith(2));
    expect(await screen.findByText("本页没有公告。")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "退出" }));
    await waitFor(() => expect(signedOut).toHaveBeenCalled());
  });
});
