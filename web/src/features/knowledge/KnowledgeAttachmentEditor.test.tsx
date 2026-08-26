import { useState } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { KnowledgeAttachment, KnowledgeAttachmentPage } from "../../lib/api";
import { KnowledgeAttachmentEditor, safeKnowledgeURL, sanitizedHTMLToMarkdown, type KnowledgeAttachmentAPI } from "./KnowledgeAttachmentEditor";

const readyAttachment: KnowledgeAttachment = {
  uuid: "550e8400-e29b-41d4-a716-446655440000", knowledge_id: null, original_name: "guide.txt",
  mime_type: "text/plain; charset=utf-8", extension: "txt", size: 8, sha256: "a".repeat(64),
  status: "ready", disposition: "attachment", url: "https://panel.example.test/knowledge-attachments/signed",
  placeholder: "knowledge-attachment://550e8400-e29b-41d4-a716-446655440000",
  created_at: 1787788800
};

describe("KnowledgeAttachmentEditor", () => {
  it("uploads in verified chunks, blocks a failed save state, and resumes on retry", async () => {
    const user = userEvent.setup();
    const api = attachmentAPI();
    vi.mocked(api.uploadKnowledgeAttachmentChunk)
      .mockRejectedValueOnce(new Error("temporary failure"))
      .mockRejectedValueOnce(new Error("temporary failure"))
      .mockRejectedValueOnce(new Error("temporary failure"));
    const blocking = vi.fn();
    render(<Harness api={api} onBlockingChange={blocking} />);
    await user.upload(screen.getByLabelText("选择知识附件"), new File(["abcdefgh"], "guide.txt", { type: "text/plain" }));
    expect(await screen.findByRole("button", { name: "重试" })).toBeInTheDocument();
    expect(blocking).toHaveBeenLastCalledWith(true);

    await user.click(screen.getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.getByTestId("knowledge-body")).toHaveTextContent(`[guide.txt](${readyAttachment.placeholder})`));
    expect(api.uploadKnowledgeAttachmentChunk).toHaveBeenCalledTimes(5);
    for (const call of vi.mocked(api.uploadKnowledgeAttachmentChunk).mock.calls) expect(call[2]).toMatch(/^[0-9a-f]{64}$/);
    await waitFor(() => expect(blocking).toHaveBeenLastCalledWith(false));
  });

  it("ignores a late attachment list response after switching articles", async () => {
    let resolveFirst: ((page: KnowledgeAttachmentPage) => void) | undefined;
    const first = new Promise<KnowledgeAttachmentPage>((resolve) => { resolveFirst = resolve; });
    const secondAttachment = { ...readyAttachment, uuid: "550e8400-e29b-41d4-a716-446655440001", original_name: "second.txt" };
    const api = attachmentAPI();
    vi.mocked(api.listKnowledgeAttachments).mockReturnValueOnce(first).mockResolvedValueOnce(pageOf(secondAttachment));
    const { rerender } = render(<Harness api={api} articleID={1} />);
    rerender(<Harness api={api} articleID={2} />);
    expect(await screen.findByText("second.txt")).toBeInTheDocument();
    resolveFirst?.(pageOf(readyAttachment));
    await waitFor(() => expect(screen.queryByText("guide.txt")).not.toBeInTheDocument());
  });

  it("uploads pasted images and inserts an inline image placeholder", async () => {
    const api = attachmentAPI();
    const image = new File(["image-bytes"], "clipboard.png", { type: "image/png" });
    const inline = { ...readyAttachment, original_name: image.name, mime_type: image.type, disposition: "inline" as const };
    vi.mocked(api.initializeKnowledgeAttachment).mockResolvedValueOnce({
      upload_uuid: inline.uuid, original_name: image.name, declared_size: image.size, chunk_size: image.size,
      total_chunks: 1, received_chunks: 0, uploaded_chunks: [], status: "initialized", expires_at: 1787875200
    });
    vi.mocked(api.completeKnowledgeAttachmentUpload).mockResolvedValueOnce(inline);
    render(<Harness api={api} />);
    const accepted = fireEvent.paste(screen.getByLabelText("内容"), { clipboardData: { items: [{ kind: "file", type: image.type, getAsFile: () => image }] } });
    expect(accepted).toBe(false);
    await waitFor(() => expect(screen.getByTestId("knowledge-body")).toHaveTextContent(`![clipboard.png](${inline.placeholder})`));
    expect(api.initializeKnowledgeAttachment).toHaveBeenCalledWith(image, "b".repeat(64));
  });

  it("clones all attachments from another article into an independent draft", async () => {
    const user = userEvent.setup();
    const api = attachmentAPI();
    const blocking = vi.fn();
    const source = { ...readyAttachment, knowledge_id: 9 };
    const clone = { ...readyAttachment, uuid: "550e8400-e29b-41d4-a716-446655440009", placeholder: "knowledge-attachment://550e8400-e29b-41d4-a716-446655440009" };
    let resolveClone: ((items: Array<{ source_uuid: string; attachment: KnowledgeAttachment }>) => void) | undefined;
    const pendingClone = new Promise<Array<{ source_uuid: string; attachment: KnowledgeAttachment }>>((resolve) => { resolveClone = resolve; });
    vi.mocked(api.listKnowledgeAttachments).mockResolvedValueOnce(pageOf()).mockResolvedValueOnce(pageOf(source));
    vi.mocked(api.cloneKnowledgeAttachments).mockReturnValueOnce(pendingClone);
    render(<Harness api={api} onBlockingChange={blocking} />);
    await user.type(screen.getByLabelText("来源知识编号"), "9");
    await user.click(screen.getByRole("button", { name: "复制全部附件" }));
    await waitFor(() => expect(blocking).toHaveBeenLastCalledWith(true));
    resolveClone?.([{ source_uuid: source.uuid, attachment: clone }]);
    await waitFor(() => expect(screen.getByTestId("knowledge-body")).toHaveTextContent(`[guide.txt](${clone.placeholder})`));
    await waitFor(() => expect(blocking).toHaveBeenLastCalledWith(false));
    expect(api.cloneKnowledgeAttachments).toHaveBeenCalledWith(9, [source.uuid], "b".repeat(64));
  });

  it("converts allowed pasted HTML to Markdown and drops executable content", () => {
    const markdown = sanitizedHTMLToMarkdown(`<h2>安装</h2><p>打开 <strong>客户端</strong><script>alert(1)</script></p><ol><li>第一步</li><li>第二步</li></ol><a href="javascript:alert(2)">危险链接</a><a href="https://example.test/guide">安全链接</a><img src="data:text/html,boom" onerror="alert(3)"><svg><script>alert(4)</script></svg>`);
    expect(markdown).toContain("## 安装");
    expect(markdown).toContain("**客户端**");
    expect(markdown).toContain("1. 第一步");
    expect(markdown).toContain("2. 第二步");
    expect(markdown).toContain("危险链接");
    expect(markdown).toContain("[安全链接](https://example.test/guide)");
    expect(markdown).not.toMatch(/javascript|data:text|alert|<svg/i);
    expect(safeKnowledgeURL("https://example.test/a b")).toBe("");
    expect(safeKnowledgeURL("/guide/1")).toBe("/guide/1");
  });

  it("applies rich Markdown tools and sanitizes HTML clipboard input", async () => {
    const user = userEvent.setup();
    const api = attachmentAPI();
    render(<Harness api={api} />);
    const editor = screen.getByLabelText<HTMLTextAreaElement>("内容");
    await user.type(editor, "客户端");
    editor.setSelectionRange(0, 3);
    await user.click(screen.getByRole("button", { name: "粗体" }));
    await waitFor(() => expect(screen.getByTestId("knowledge-body")).toHaveTextContent("**客户端**"));
    fireEvent.paste(editor, { clipboardData: { items: [], getData: (type: string) => type === "text/html" ? `<p>安全 <a href="javascript:alert(1)">文字</a></p>` : "" } });
    await waitFor(() => expect(screen.getByTestId("knowledge-body")).toHaveTextContent("安全 文字"));
    expect(screen.getByTestId("knowledge-body")).not.toHaveTextContent("javascript");
    expect(screen.getByRole("region", { name: "知识正文预览" })).toBeInTheDocument();
  });

  it("rejects unsafe QR links before calling the server", async () => {
    const user = userEvent.setup();
    const api = attachmentAPI();
    render(<Harness api={api} />);
    await user.click(screen.getByRole("button", { name: "插入二维码" }));
    await user.type(screen.getByLabelText("二维码链接"), "javascript:alert(1)");
    await user.click(screen.getByRole("button", { name: "生成并上传二维码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("http:// 或 https://");
    expect(api.generateKnowledgeAttachmentQRCode).not.toHaveBeenCalled();
  });
});

function Harness({ api, articleID, onBlockingChange = vi.fn() }: { api: KnowledgeAttachmentAPI; articleID?: number; onBlockingChange?: (blocked: boolean) => void }) {
  const [body, setBody] = useState("");
  return <><KnowledgeAttachmentEditor api={api} articleID={articleID} draftToken={"b".repeat(64)} body={body} setBody={setBody} onBlockingChange={onBlockingChange} />
    <output data-testid="knowledge-body">{body}</output></>;
}

function attachmentAPI(): KnowledgeAttachmentAPI {
  return {
    initializeKnowledgeAttachment: vi.fn().mockResolvedValue({
      upload_uuid: readyAttachment.uuid, original_name: "guide.txt", declared_size: 8, chunk_size: 4,
      total_chunks: 2, received_chunks: 0, uploaded_chunks: [], status: "initialized", expires_at: 1787875200
    }),
    uploadKnowledgeAttachmentChunk: vi.fn().mockResolvedValue({ received_chunks: 1 }),
    getKnowledgeAttachmentUpload: vi.fn().mockResolvedValue({
      upload_uuid: readyAttachment.uuid, original_name: "guide.txt", declared_size: 8, chunk_size: 4,
      total_chunks: 2, received_chunks: 0, uploaded_chunks: [], status: "uploading", expires_at: 1787875200
    }),
    completeKnowledgeAttachmentUpload: vi.fn().mockResolvedValue(readyAttachment),
    cancelKnowledgeAttachmentUpload: vi.fn().mockResolvedValue(undefined),
    listKnowledgeAttachments: vi.fn().mockResolvedValue(pageOf()),
    dropKnowledgeAttachment: vi.fn().mockResolvedValue(undefined),
    cloneKnowledgeAttachments: vi.fn().mockResolvedValue([]),
    generateKnowledgeAttachmentQRCode: vi.fn().mockResolvedValue({ svg: "<svg></svg>" })
  };
}

function pageOf(...items: KnowledgeAttachment[]): KnowledgeAttachmentPage { return { items, total: items.length, page: 1, per_page: 100 }; }
