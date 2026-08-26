import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { KnowledgeArticle } from "../../lib/api";
import { UserKnowledgePage } from "./UserKnowledgePage";

const summary: KnowledgeArticle = {
  id: 7, language: "zh-CN", category: "使用指南", title: "Windows 客户端", body: "摘要",
  sort: 1, show: true, revision: 1, created_at: "2026-08-24T08:00:00Z", updated_at: "2026-08-24T08:00:00Z",
  share_url: "https://panel.example.test/guide/7/windows"
};

describe("UserKnowledgePage", () => {
  it("filters by language/keyword, groups categories, and reads safe subscription-aware content", async () => {
    const videoURL = `/knowledge-attachments/550e8400-e29b-41d4-a716-446655440000?expires=1787796000&disposition=inline&signature=${"a".repeat(64)}`;
    const detail = {
      ...summary,
			body: `# 安装\n\n<div class="v2board-no-access">您必须拥有有效的订阅才可以查看该区域的内容</div>\n\n<script>alert(1)</script>\n\n[危险链接](javascript:alert(2))\n\n<video controls preload="metadata" src="${videoURL}"></video>\n\n<video controls preload="metadata" src="https://evil.example/video.mp4"></video>`
    };
    const api = {
      listKnowledge: vi.fn().mockResolvedValue([summary]),
      getKnowledge: vi.fn().mockResolvedValue(detail)
    };
    const user = userEvent.setup();
    render(<UserKnowledgePage api={api} />);

    expect(await screen.findByRole("heading", { name: "知识库" })).toBeVisible();
    expect(screen.getByRole("heading", { name: "使用指南" })).toBeVisible();
    await user.type(screen.getByLabelText("搜索知识"), "Windows");
    await user.click(screen.getByRole("button", { name: "搜索" }));
    await waitFor(() => expect(api.listKnowledge).toHaveBeenLastCalledWith("zh-CN", "Windows"));

    await user.click(screen.getByRole("button", { name: "阅读：Windows 客户端" }));
    const dialog = await screen.findByRole("dialog", { name: "Windows 客户端" });
    expect(within(dialog).getByRole("heading", { name: "安装" })).toBeVisible();
    expect(within(dialog).getByText("您必须拥有有效的订阅才可以查看该区域的内容")).toBeVisible();
    expect(dialog.querySelector("script")).toBeNull();
    const unsafeLink = within(dialog).getByText("危险链接").closest("a");
    expect(unsafeLink).not.toBeNull();
    expect(unsafeLink?.getAttribute("href")).not.toMatch(/^javascript:/i);
		expect(dialog.querySelectorAll("video")).toHaveLength(1);
		expect(dialog.querySelector("video")).toHaveAttribute("src", videoURL);
    const share = within(dialog).getByRole("link", { name: "公开分享" });
    expect(share).toHaveAttribute("href", summary.share_url);
    expect(share).toHaveAttribute("rel", "noopener noreferrer");
  });
});
