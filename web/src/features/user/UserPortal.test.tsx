import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { UserSession } from "../../lib/api";
import { UserPortal } from "./UserPortal";

const session: UserSession = { id: 12, email: "user@example.test", is_admin: false };

describe("UserPortal", () => {
  it("navigates between notices and clients and signs out through one shared shell", async () => {
    const api = {
      listVisibleNotices: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 }),
      listClientCatalog: vi.fn().mockResolvedValue([]),
      clientCatalogQR: vi.fn(),
      logout: vi.fn().mockResolvedValue(undefined)
    };
    const onSignedOut = vi.fn();
    const user = userEvent.setup();
    render(<UserPortal api={api} session={session} onSignedOut={onSignedOut} />);

    expect(await screen.findByRole("heading", { name: "公告" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "客户端下载" }));
    expect(await screen.findByRole("heading", { name: "客户端下载" })).toBeVisible();
    expect(api.listClientCatalog).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "退出" }));
    await waitFor(() => expect(onSignedOut).toHaveBeenCalledTimes(1));
  });

  it("keeps the session visible when logout fails", async () => {
    const api = {
      listVisibleNotices: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 5 }),
      listClientCatalog: vi.fn().mockResolvedValue([]),
      clientCatalogQR: vi.fn(),
      logout: vi.fn().mockRejectedValue(new Error("会话注销失败"))
    };
    const onSignedOut = vi.fn();
    const user = userEvent.setup();
    render(<UserPortal api={api} session={session} onSignedOut={onSignedOut} />);

    await user.click(screen.getByRole("button", { name: "退出" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("会话注销失败");
    expect(onSignedOut).not.toHaveBeenCalled();
  });
});
