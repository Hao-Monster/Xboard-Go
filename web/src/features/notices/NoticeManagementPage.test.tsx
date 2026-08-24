import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { Notice } from "../../lib/api";
import { NoticeManagementPage } from "./NoticeManagementPage";

const visible: Notice = {
  id: 7, sort: 1, title: "Service update", content: "**Available now**", image_url: null,
  tags: ["news"], show: true, revision: 1,
  created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z"
};

const draft: Notice = {
  ...visible, id: 8, sort: 2, title: "Draft maintenance", content: "draft", tags: ["ops"], show: false
};

describe("NoticeManagementPage", () => {
  it("creates, toggles, reorders, edits, filters, and deletes notices", async () => {
    const created: Notice = { ...visible, id: 9, title: "New notice", revision: 1 };
    const toggled: Notice = { ...draft, show: true, revision: 2 };
    const updated: Notice = { ...visible, title: "Service update revised", revision: 2 };
    const api = {
      listNotices: vi.fn().mockResolvedValue([visible, draft]),
      createNotice: vi.fn().mockResolvedValue(created),
      updateNotice: vi.fn().mockResolvedValue(updated),
      setNoticeVisibility: vi.fn().mockResolvedValue(toggled),
      reorderNotices: vi.fn().mockImplementation((ids: number[]) => {
        const byID: Record<number, Notice> = { 7: updated, 8: toggled, 9: created };
        return Promise.resolve(ids.map((id, index) => ({ ...byID[id], sort: index + 1 })));
      }),
      deleteNotice: vi.fn().mockResolvedValue(undefined)
    };
    const user = userEvent.setup();
    render(<NoticeManagementPage api={api} />);

    expect(await screen.findByText("Service update")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "添加公告" }));
    let dialog = screen.getByRole("dialog", { name: "添加公告" });
    await user.type(within(dialog).getByLabelText("标题"), "New notice");
    await user.type(within(dialog).getByLabelText("公告内容"), "new body");
    await user.type(within(dialog).getByLabelText("节点标签"), "release, service");
    await user.click(within(dialog).getByRole("checkbox", { name: "显示给用户" }));
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.createNotice).toHaveBeenCalledWith({
      title: "New notice", content: "new body", image_url: "", tags: ["release", "service"], show: true
    }));
    expect(await screen.findByText("New notice")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "显示公告：Draft maintenance" }));
    await waitFor(() => expect(api.setNoticeVisibility).toHaveBeenCalledWith(8, 1, true));
    expect(screen.getByRole("button", { name: "隐藏公告：Draft maintenance" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "编辑公告：Service update" }));
    dialog = screen.getByRole("dialog", { name: "编辑公告" });
    const title = within(dialog).getByLabelText("标题");
    await user.clear(title);
    await user.type(title, "Service update revised");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateNotice).toHaveBeenCalledWith(7, 1, {
      title: "Service update revised", content: "**Available now**", image_url: "", tags: ["news"], show: true
    }));

    await user.click(screen.getByRole("button", { name: "编辑排序" }));
    dialog = screen.getByRole("dialog", { name: "编辑公告排序" });
    await user.click(within(dialog).getByRole("button", { name: "下移：Service update revised" }));
    await user.click(within(dialog).getByRole("button", { name: "保存排序" }));
    await waitFor(() => expect(api.reorderNotices).toHaveBeenCalled());

    const search = screen.getByRole("searchbox", { name: "搜索公告标题" });
    fireEvent.change(search, { target: { value: "Draft" } });
    expect(search).toHaveValue("Draft");
    await waitFor(() => expect(screen.queryByText("Service update revised")).not.toBeInTheDocument());
    await user.clear(search);

    await user.click(screen.getByRole("button", { name: "删除公告：Service update revised" }));
    dialog = screen.getByRole("dialog", { name: "删除公告" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(api.deleteNotice).toHaveBeenCalledWith(7, 2));
  });

  it("keeps a failed reorder open so a conflict cannot look successful", async () => {
    const api = {
      listNotices: vi.fn().mockResolvedValue([visible, draft]),
      createNotice: vi.fn(), updateNotice: vi.fn(), setNoticeVisibility: vi.fn(), deleteNotice: vi.fn(),
      reorderNotices: vi.fn().mockRejectedValue(new Error("公告已被其他操作修改，请刷新后重试"))
    };
    const user = userEvent.setup();
    render(<NoticeManagementPage api={api} />);
    expect(await screen.findByText("Service update")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "编辑排序" }));
    const dialog = screen.getByRole("dialog", { name: "编辑公告排序" });
    await user.click(within(dialog).getByRole("button", { name: "保存排序" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("公告已被其他操作修改");
    expect(dialog).toBeVisible();
  });
});
