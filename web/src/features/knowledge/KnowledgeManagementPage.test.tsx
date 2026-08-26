import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { KnowledgeArticle } from "../../lib/api";
import { KnowledgeManagementPage } from "./KnowledgeManagementPage";

const published: KnowledgeArticle = {
  id: 11, language: "zh-CN", category: "入门", title: "连接指南", body: undefined,
  sort: 1, show: true, revision: 1, created_at: "2026-08-24T08:00:00Z", updated_at: "2026-08-24T08:00:00Z",
  share_url: "https://panel.example.test/guide/11/article"
};
const draft: KnowledgeArticle = { ...published, id: 12, title: "安装草稿", sort: 2, show: false, share_url: "https://panel.example.test/guide/12/article" };

describe("KnowledgeManagementPage", () => {
  it("matches the observed Xboard management fields and performs the full article lifecycle", async () => {
    const created = { ...published, id: 13, title: "新手教程", revision: 1 };
    const detailed = { ...published, body: "旧正文" };
    const api = {
      listKnowledgeAdmin: vi.fn().mockResolvedValue([published, draft]),
      getKnowledgeAdmin: vi.fn().mockResolvedValue(detailed),
      listKnowledgeCategories: vi.fn().mockResolvedValue(["入门"]),
      createKnowledge: vi.fn().mockResolvedValue(created),
      updateKnowledge: vi.fn().mockResolvedValue({ ...detailed, title: "连接指南 2", revision: 2 }),
      setKnowledgeVisibility: vi.fn().mockResolvedValue({ ...draft, show: true, revision: 2 }),
      reorderKnowledge: vi.fn().mockImplementation((ids: number[]) => Promise.resolve(ids.map((id, index) => ({ ...(id === 11 ? published : draft), sort: index + 1 })))),
      deleteKnowledge: vi.fn().mockResolvedValue(undefined)
    };
    const user = userEvent.setup();
    render(<KnowledgeManagementPage api={api} />);

    expect(await screen.findByRole("heading", { name: "知识库管理" })).toBeVisible();
    expect(screen.getByText("连接指南")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "添加知识" }));
    const addDialog = screen.getByRole("dialog", { name: "添加知识" });
    await user.type(within(addDialog).getByLabelText("标题"), "新手教程");
    await user.type(within(addDialog).getByLabelText("分类"), "入门");
    await user.selectOptions(within(addDialog).getByLabelText("语言"), "zh-CN");
    await user.type(within(addDialog).getByLabelText("内容"), "公开正文");
    await user.click(within(addDialog).getByRole("button", { name: "插入订阅专属区块" }));
    await user.click(within(addDialog).getByLabelText("显示"));
    await user.click(within(addDialog).getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.createKnowledge).toHaveBeenCalledWith(expect.objectContaining({
      language: "zh-CN", category: "入门", title: "新手教程", show: true,
      body: expect.stringContaining("<!--access start-->"), draft_token: expect.stringMatching(/^[0-9a-f]{64}$/)
    })));

    await user.click(screen.getByRole("button", { name: "显示知识：安装草稿" }));
    await waitFor(() => expect(api.setKnowledgeVisibility).toHaveBeenCalledWith(12, 1, true));

    await user.click(screen.getByRole("button", { name: "编辑知识：连接指南" }));
    await waitFor(() => expect(api.getKnowledgeAdmin).toHaveBeenCalledWith(11));
    const editDialog = await screen.findByRole("dialog", { name: "编辑知识" });
    const title = within(editDialog).getByLabelText("标题");
    await user.clear(title);
    await user.type(title, "连接指南 2");
    await user.click(within(editDialog).getByRole("button", { name: "提交" }));
    await waitFor(() => expect(api.updateKnowledge).toHaveBeenCalledWith(11, 1, expect.objectContaining({ title: "连接指南 2", body: "旧正文" })));

    await user.click(screen.getByRole("button", { name: "编辑排序" }));
    const orderDialog = screen.getByRole("dialog", { name: "编辑知识排序" });
    await user.click(within(orderDialog).getByRole("button", { name: "下移：连接指南 2" }));
    await user.click(within(orderDialog).getByRole("button", { name: "保存排序" }));
    await waitFor(() => expect(api.reorderKnowledge).toHaveBeenCalled());
  });

  it("does not hide mutation conflicts and allows the administrator to reload", async () => {
    const api = {
      listKnowledgeAdmin: vi.fn().mockResolvedValue([draft]), getKnowledgeAdmin: vi.fn(),
      listKnowledgeCategories: vi.fn().mockResolvedValue([]), createKnowledge: vi.fn(), updateKnowledge: vi.fn(),
      setKnowledgeVisibility: vi.fn().mockRejectedValue(new Error("知识文章已被其他操作修改，请刷新后重试")),
      reorderKnowledge: vi.fn(), deleteKnowledge: vi.fn()
    };
    const user = userEvent.setup();
    render(<KnowledgeManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: "显示知识：安装草稿" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("知识文章已被其他操作修改");
    await user.click(screen.getByRole("button", { name: "刷新" }));
    await waitFor(() => expect(api.listKnowledgeAdmin).toHaveBeenCalledTimes(2));
  });
});
