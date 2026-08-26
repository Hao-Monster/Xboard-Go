import { useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent, type Dispatch, type SetStateAction } from "react";

import type { KnowledgeAttachment, KnowledgeAttachmentPage, KnowledgeAttachmentUpload } from "../../lib/api";
import { SafeKnowledgeMarkdown } from "./UserKnowledgePage";

export interface KnowledgeAttachmentAPI {
  initializeKnowledgeAttachment: (file: File, draftToken: string) => Promise<KnowledgeAttachmentUpload>;
  uploadKnowledgeAttachmentChunk: (uploadUUID: string, index: number, digest: string, chunk: Blob, signal?: AbortSignal) => Promise<{ received_chunks: number }>;
  getKnowledgeAttachmentUpload: (uploadUUID: string) => Promise<KnowledgeAttachmentUpload>;
  completeKnowledgeAttachmentUpload: (uploadUUID: string) => Promise<KnowledgeAttachment>;
  cancelKnowledgeAttachmentUpload: (uploadUUID: string, draftToken: string) => Promise<void>;
  listKnowledgeAttachments: (filter: { knowledgeID?: number; draftToken?: string; page?: number; perPage?: number }) => Promise<KnowledgeAttachmentPage>;
  dropKnowledgeAttachment: (uuid: string, draftToken: string) => Promise<void>;
  cloneKnowledgeAttachments: (sourceKnowledgeID: number, sourceUUIDs: string[], draftToken: string) => Promise<Array<{ source_uuid: string; attachment: KnowledgeAttachment }>>;
  generateKnowledgeAttachmentQRCode: (url: string) => Promise<{ svg: string }>;
}

type UploadStatus = "queued" | "uploading" | "complete" | "failed" | "cancelling";

interface UploadItem {
  id: string;
  file?: File;
  name: string;
  size: number;
  status: UploadStatus;
  progress: number;
  uploadUUID?: string;
  attachment?: KnowledgeAttachment;
  error?: string;
  controller?: AbortController;
  markdown?: string;
}

interface Props {
  api: KnowledgeAttachmentAPI;
  articleID?: number;
  draftToken: string;
  body: string;
  setBody: Dispatch<SetStateAction<string>>;
  onBlockingChange: (blocked: boolean) => void;
}

const markerPrefix = "xboard-knowledge-upload:";

export function KnowledgeAttachmentEditor({ api, articleID, draftToken, body, setBody, onBlockingChange }: Props) {
  const [items, setItems] = useState<UploadItem[]>([]);
  const [loadError, setLoadError] = useState("");
  const [sourceArticleID, setSourceArticleID] = useState("");
  const [cloning, setCloning] = useState(false);
  const [linkOpen, setLinkOpen] = useState(false);
  const [linkLabel, setLinkLabel] = useState("");
  const [linkURL, setLinkURL] = useState("");
  const [qrOpen, setQROpen] = useState(false);
  const [qrURL, setQRURL] = useState("");
  const [qrBusy, setQRBusy] = useState(false);
  const [previewing, setPreviewing] = useState(true);
  const mounted = useRef(true);
  const generation = useRef(0);
  const controllers = useRef(new Set<AbortController>());
  const textarea = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    mounted.current = true;
    const activeControllers = controllers.current;
    return () => {
      mounted.current = false;
      generation.current += 1;
      activeControllers.forEach((controller) => controller.abort());
      activeControllers.clear();
    };
  }, []);

  useEffect(() => {
    const currentGeneration = ++generation.current;
    void api.listKnowledgeAttachments(articleID === undefined ? { draftToken, perPage: 100 } : { knowledgeID: articleID, perPage: 100 })
      .then((page) => {
        if (!mounted.current || generation.current !== currentGeneration) return;
        setItems(page.items.map((attachment) => ({
          id: attachment.uuid, name: attachment.original_name, size: attachment.size,
          status: "complete", progress: 100, attachment, markdown: markdownFor(attachment)
        })));
      })
      .catch((cause: unknown) => { if (mounted.current && generation.current === currentGeneration) setLoadError(messageOf(cause)); });
  }, [api, articleID, draftToken]);

  const blocked = cloning || items.some((item) => item.status === "queued" || item.status === "uploading" || item.status === "failed" || item.status === "cancelling");
  useEffect(() => onBlockingChange(blocked), [blocked, onBlockingChange]);

  const addFiles = (files: File[]) => {
    if (files.length === 0) return;
    const queued = files.map<UploadItem>((file) => ({ id: randomID(), file, name: file.name, size: file.size, status: "queued", progress: 0 }));
    setItems((current) => [...current, ...queued]);
    setBody((current) => `${current}${current.trim() === "" ? "" : "\n\n"}${queued.map((item) => uploadMarker(item.id)).join("\n")}`);
    void runWithConcurrency(queued, 2, uploadOne);
  };

  const uploadOne = async (item: UploadItem) => {
    if (item.file === undefined || !mounted.current) return;
    const controller = new AbortController();
    controllers.current.add(controller);
    item.controller = controller;
    updateItem(item.id, { status: "uploading", error: "", controller });
    try {
      let session = item.uploadUUID === undefined
        ? await api.initializeKnowledgeAttachment(item.file, draftToken)
        : await api.getKnowledgeAttachmentUpload(item.uploadUUID);
      item.uploadUUID = session.upload_uuid;
      updateItem(item.id, { uploadUUID: session.upload_uuid });
      const uploaded = new Set(session.uploaded_chunks);
      for (let index = 0; index < session.total_chunks; index += 1) {
        if (uploaded.has(index)) continue;
        const chunk = item.file.slice(index * session.chunk_size, Math.min(item.size, (index + 1) * session.chunk_size));
        const digest = await sha256Hex(chunk);
        await retry(() => api.uploadKnowledgeAttachmentChunk(session.upload_uuid, index, digest, chunk, controller.signal), 3, controller.signal);
        session = { ...session, received_chunks: session.received_chunks + 1 };
        updateItem(item.id, { progress: Math.round(session.received_chunks / session.total_chunks * 100) });
      }
      const attachment = await api.completeKnowledgeAttachmentUpload(session.upload_uuid);
      const markdown = markdownFor(attachment);
      if (!mounted.current || controller.signal.aborted) return;
      setBody((current) => current.replace(uploadMarker(item.id), markdown));
      updateItem(item.id, { status: "complete", progress: 100, attachment, markdown, controller: undefined });
    } catch (cause) {
      if (!mounted.current || controller.signal.aborted) return;
      updateItem(item.id, { status: "failed", error: messageOf(cause), controller: undefined });
    } finally {
      controllers.current.delete(controller);
    }
  };

  const cancel = async (item: UploadItem) => {
    updateItem(item.id, { status: "cancelling" });
    item.controller?.abort();
    try {
      if (item.uploadUUID !== undefined && item.attachment === undefined) await api.cancelKnowledgeAttachmentUpload(item.uploadUUID, draftToken);
      if (item.attachment !== undefined && item.attachment.knowledge_id === null) await api.dropKnowledgeAttachment(item.attachment.uuid, draftToken);
      setBody((current) => current.replace(item.markdown ?? uploadMarker(item.id), ""));
      setItems((current) => current.filter((candidate) => candidate.id !== item.id));
    } catch (cause) {
      updateItem(item.id, { status: "failed", error: messageOf(cause) });
    }
  };

  const retryItem = (item: UploadItem) => { void uploadOne(item); };
  const updateItem = (id: string, values: Partial<UploadItem>) => {
    if (!mounted.current) return;
    setItems((current) => current.map((item) => item.id === id ? { ...item, ...values } : item));
  };
  const choose = (event: ChangeEvent<HTMLInputElement>) => { addFiles(Array.from(event.target.files ?? [])); event.target.value = ""; };
  const paste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const images = Array.from(event.clipboardData.items).filter((item) => item.kind === "file" && item.type.startsWith("image/"))
      .map((item) => item.getAsFile()).filter((file): file is File => file !== null);
    if (images.length > 0) { event.preventDefault(); addFiles(images); return; }
    const html = event.clipboardData.getData("text/html");
    if (html.trim() === "") return;
    event.preventDefault();
    insertAtSelection(sanitizedHTMLToMarkdown(html), event.currentTarget.selectionStart, event.currentTarget.selectionEnd);
  };

  const insertAtSelection = (value: string, start = textarea.current?.selectionStart ?? body.length, end = textarea.current?.selectionEnd ?? start) => {
    setBody((current) => `${current.slice(0, start)}${value}${current.slice(end)}`);
    window.requestAnimationFrame(() => { textarea.current?.focus(); textarea.current?.setSelectionRange(start + value.length, start + value.length); });
  };
  const wrapSelection = (before: string, after: string, fallback: string) => {
    const start = textarea.current?.selectionStart ?? body.length;
    const end = textarea.current?.selectionEnd ?? start;
    const selected = body.slice(start, end) || fallback;
    insertAtSelection(`${before}${selected}${after}`, start, end);
  };
  const setHeading = (level: string) => {
    const cursor = textarea.current?.selectionStart ?? body.length;
    const start = body.lastIndexOf("\n", Math.max(0, cursor - 1)) + 1;
    const lineEnd = body.indexOf("\n", cursor);
    const end = lineEnd === -1 ? body.length : lineEnd;
    const line = body.slice(start, end).replace(/^#{1,6}\s+/, "");
    insertAtSelection(`${level === "P" ? "" : `${"#".repeat(Number(level.slice(1)))} `}${line}`, start, end);
  };
  const insertLink = () => {
    const url = safeKnowledgeURL(linkURL);
    if (url === "") { setLoadError("请输入有效的 http://、https:// 或站内链接。"); return; }
    insertAtSelection(`[${escapeMarkdownInline(linkLabel.trim() || url)}](${url})`);
    setLinkLabel(""); setLinkURL(""); setLinkOpen(false); setLoadError("");
  };
  const insertQRCode = async () => {
    const url = safeHTTPURL(qrURL);
    if (url === "" || url.length > 2048) { setLoadError("请输入不超过 2048 个字符的 http:// 或 https:// 链接。"); return; }
    setQRBusy(true); setLoadError("");
    try {
      const result = await api.generateKnowledgeAttachmentQRCode(url);
      if (!/^\s*<svg[\s>]/i.test(result.svg)) throw new Error("服务器没有返回有效的二维码图像。");
      addFiles([await qrSVGToPNGFile(result.svg)]);
      setQRURL(""); setQROpen(false);
    } catch (cause) { setLoadError(messageOf(cause)); } finally { setQRBusy(false); }
  };

  const cloneFromArticle = async () => {
    const sourceID = Number(sourceArticleID);
    if (!Number.isSafeInteger(sourceID) || sourceID < 1 || sourceID === articleID) { setLoadError("请输入其他文章的有效编号。"); return; }
    setCloning(true); setLoadError("");
    try {
      const source = await api.listKnowledgeAttachments({ knowledgeID: sourceID, perPage: 100 });
      if (source.items.length === 0) { setLoadError("来源文章没有可复制的附件。"); return; }
      const cloned = await api.cloneKnowledgeAttachments(sourceID, source.items.map((item) => item.uuid), draftToken);
      const additions = cloned.map(({ attachment }) => markdownFor(attachment));
      setItems((current) => [...current, ...cloned.map(({ attachment }) => ({
        id: attachment.uuid, name: attachment.original_name, size: attachment.size, status: "complete" as const,
        progress: 100, attachment, markdown: markdownFor(attachment)
      }))]);
      setBody((current) => `${current}${current.trim() === "" ? "" : "\n\n"}${additions.join("\n\n")}`);
      setSourceArticleID("");
    } catch (cause) { setLoadError(messageOf(cause)); } finally { setCloning(false); }
  };

  const previewBody = items.reduce((current, item) => item.attachment === undefined
    ? current : current.replaceAll(item.attachment.placeholder, item.attachment.url), body);

  return <section className="knowledge-attachment-editor" aria-labelledby="knowledge-attachment-heading">
    <div className="editor-toolbar"><div><strong id="knowledge-attachment-heading">文章附件</strong><p className="muted small">支持大文件分片续传；粘贴图片会自动上传。</p></div>
      <label className="button secondary compact attachment-picker">添加附件<input aria-label="选择知识附件" type="file" multiple onChange={choose} /></label></div>
    <div className="attachment-clone-row"><label>从其他文章复制附件<input aria-label="来源知识编号" type="number" min={1} value={sourceArticleID} onChange={(event) => setSourceArticleID(event.target.value)} /></label>
      <button className="button ghost compact" type="button" disabled={cloning || sourceArticleID === ""} onClick={() => void cloneFromArticle()}>{cloning ? "正在复制…" : "复制全部附件"}</button></div>
    <div className="knowledge-rich-toolbar" role="toolbar" aria-label="知识正文编辑工具">
      <label className="sr-only" htmlFor="knowledge-heading-level">正文格式</label><select id="knowledge-heading-level" aria-label="正文格式" defaultValue="P" onChange={(event) => { setHeading(event.target.value); event.target.value = "P"; }}><option value="P">正文</option><option value="H1">标题 1</option><option value="H2">标题 2</option><option value="H3">标题 3</option></select>
      <button className="button ghost compact" type="button" onClick={() => wrapSelection("**", "**", "粗体文本")}>粗体</button>
      <button className="button ghost compact" type="button" onClick={() => wrapSelection("*", "*", "强调文本")}>斜体</button>
      <button className="button ghost compact" type="button" onClick={() => wrapSelection("`", "`", "代码")}>代码</button>
      <button className="button ghost compact" type="button" aria-expanded={linkOpen} onClick={() => { setLinkOpen((current) => !current); setQROpen(false); }}>插入链接</button>
      <button className="button ghost compact" type="button" aria-expanded={qrOpen} onClick={() => { setQROpen((current) => !current); setLinkOpen(false); }}>插入二维码</button>
      <button className="button ghost compact" type="button" aria-pressed={previewing} onClick={() => setPreviewing((current) => !current)}>{previewing ? "隐藏预览" : "显示预览"}</button>
    </div>
    {linkOpen && <div className="knowledge-rich-insert-row"><label>链接文字<input aria-label="链接文字" maxLength={256} value={linkLabel} onChange={(event) => setLinkLabel(event.target.value)} /></label><label>链接地址<input aria-label="链接地址" type="url" maxLength={2048} placeholder="https://example.com" value={linkURL} onChange={(event) => setLinkURL(event.target.value)} /></label><button className="button secondary compact" type="button" onClick={insertLink}>确认插入</button></div>}
    {qrOpen && <div className="knowledge-rich-insert-row"><label>二维码链接<input aria-label="二维码链接" type="url" maxLength={2048} placeholder="https://example.com" value={qrURL} onChange={(event) => setQRURL(event.target.value)} /></label><button className="button secondary compact" type="button" disabled={qrBusy} onClick={() => void insertQRCode()}>{qrBusy ? "正在生成并上传…" : "生成并上传二维码"}</button></div>}
    <div className={`knowledge-rich-editor-grid${previewing ? " has-preview" : ""}`}><textarea ref={textarea} aria-label="内容" name="body" required maxLength={1_048_576} value={body} onPaste={paste} onChange={(event) => setBody(event.target.value)} />
      {previewing && <div className="markdown-body knowledge-rich-preview" role="region" aria-label="知识正文预览"><SafeKnowledgeMarkdown body={previewBody} /></div>}</div>
    {loadError !== "" && <div className="alert error" role="alert">{loadError}</div>}
    {items.length > 0 && <ul className="knowledge-attachment-list">{items.map((item) => <li key={item.id}>
      <span><strong>{item.name}</strong><small className="muted">{formatBytes(item.size)} · {statusLabel(item.status)}</small></span>
      <progress max={100} value={item.progress} aria-label={`${item.name} 上传进度`} />
      <span className="row-actions">{item.status === "failed" && <button className="button secondary compact" type="button" onClick={() => retryItem(item)}>重试</button>}
        <button className="button ghost compact" type="button" disabled={item.status === "cancelling"} onClick={() => void cancel(item)}>{item.status === "complete" ? "移除" : "取消"}</button></span>
      {item.error !== undefined && item.error !== "" && <small className="error-text">{item.error}</small>}
    </li>)}</ul>}
  </section>;
}

export function createKnowledgeDraftToken(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
}

export async function sha256Hex(value: Blob): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", await value.arrayBuffer());
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function markdownFor(attachment: KnowledgeAttachment): string {
  const name = attachment.original_name.replace(/([\\[\]])/g, "\\$1").replace(/[\r\n]+/g, " ");
  if (attachment.disposition === "inline" && attachment.mime_type.startsWith("image/")) return `![${name}](${attachment.placeholder})`;
  if (attachment.disposition === "inline" && attachment.mime_type.startsWith("video/")) return `<video controls preload="metadata" src="${attachment.placeholder}"></video>`;
  return `[${name}](${attachment.placeholder})`;
}

const attachmentPattern = /^knowledge-attachment:\/\/[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const safeHTMLTags = new Set(["A", "B", "BLOCKQUOTE", "BR", "CODE", "DIV", "EM", "H1", "H2", "H3", "H4", "H5", "H6", "IMG", "LI", "OL", "P", "PRE", "SPAN", "STRONG", "UL", "VIDEO"]);
const blockedHTMLTags = new Set(["EMBED", "IFRAME", "OBJECT", "SCRIPT", "STYLE", "SVG"]);

export function sanitizedHTMLToMarkdown(html: string): string {
  const parsed = new DOMParser().parseFromString(html, "text/html");
  return normalizeMarkdown(Array.from(parsed.body.childNodes).map((node) => serializeHTMLNode(node, {})).join(""));
}

export function safeKnowledgeURL(value: string): string {
  const url = value.trim();
  if (/\s/.test(url)) return "";
  if (attachmentPattern.test(url) || /^\/(?!\/)[^\s]*$/.test(url)) return url;
  try { const parsed = new URL(url); return parsed.protocol === "http:" || parsed.protocol === "https:" ? url : ""; } catch { return ""; }
}

function safeHTTPURL(value: string): string {
  const url = safeKnowledgeURL(value);
  return /^https?:\/\//i.test(url) ? url : "";
}

function serializeHTMLNode(node: Node, context: { ordered?: boolean; index?: number; inPre?: boolean } = {}): string {
  if (node.nodeType === Node.TEXT_NODE) return context.inPre ? node.textContent ?? "" : escapeMarkdownInline(node.textContent ?? "");
  if (!(node instanceof Element)) return "";
  const tag = node.tagName.toUpperCase();
  if (blockedHTMLTags.has(tag)) return "";
  const children = () => Array.from(node.childNodes).map((child) => serializeHTMLNode(child, context)).join("");
  if (!safeHTMLTags.has(tag)) return children();
  if (/^H[1-6]$/.test(tag)) return `${"#".repeat(Number(tag.slice(1)))} ${normalizeMarkdown(children())}\n\n`;
  if (tag === "P" || tag === "DIV") return `${normalizeMarkdown(children())}\n\n`;
  if (tag === "BR") return "\n";
  if (tag === "STRONG" || tag === "B") return `**${children()}**`;
  if (tag === "EM") return `*${children()}*`;
  if (tag === "CODE" && context.inPre) return node.textContent ?? "";
  if (tag === "CODE") return `\`${(node.textContent ?? "").replaceAll("`", "\\`")}\``;
  if (tag === "PRE") return `\`\`\`\n${Array.from(node.childNodes).map((child) => serializeHTMLNode(child, { ...context, inPre: true })).join("").trimEnd()}\n\`\`\`\n\n`;
  if (tag === "BLOCKQUOTE") return `${normalizeMarkdown(children()).split("\n").map((line) => `> ${line}`).join("\n")}\n\n`;
  if (tag === "A") { const href = safeKnowledgeURL(node.getAttribute("href") ?? ""); const label = escapeMarkdownInline((node.textContent ?? "").replace(/\s+/g, " ").trim() || href); return href === "" ? label : `[${label}](${href})`; }
  if (tag === "IMG") { const source = safeKnowledgeURL(node.getAttribute("src") ?? ""); return source === "" ? "" : `![${escapeMarkdownInline(node.getAttribute("alt") || "图片")}](${source})`; }
  if (tag === "VIDEO") { const source = safeKnowledgeURL(node.getAttribute("src") ?? ""); return source === "" ? "" : `<video controls preload="metadata" src="${source}"></video>\n\n`; }
  if (tag === "UL" || tag === "OL") return `${Array.from(node.children).filter((child) => child.tagName === "LI").map((child, index) => serializeHTMLNode(child, { ...context, ordered: tag === "OL", index })).join("")}\n`;
  if (tag === "LI") { const marker = context.ordered ? `${(context.index ?? 0) + 1}. ` : "- "; return `${marker}${normalizeMarkdown(children()).replaceAll("\n", "\n  ")}\n`; }
  return children();
}

function normalizeMarkdown(value: string): string { return value.replaceAll("\u00a0", " ").replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim(); }
function escapeMarkdownInline(value: string): string { return value.replace(/([\\`*_[\]])/g, "\\$1"); }

export function qrSVGToPNGFile(svg: string): Promise<File> {
  return new Promise((resolve, reject) => {
    const source = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
    const image = new Image();
    image.onload = () => {
      const canvas = document.createElement("canvas"); canvas.width = 512; canvas.height = 512;
      const context = canvas.getContext("2d");
      if (context === null) { reject(new Error("当前浏览器无法生成二维码图片。")); return; }
      context.fillStyle = "#ffffff"; context.fillRect(0, 0, 512, 512); context.drawImage(image, 0, 0, 512, 512);
      canvas.toBlob((blob) => { if (blob === null) { reject(new Error("二维码图片生成失败。")); return; } const timestamp = new Date().toISOString().replace(/[-:TZ.]/g, "").slice(0, 14); resolve(new File([blob], `链接二维码-${timestamp}.png`, { type: "image/png" })); }, "image/png");
    };
    image.onerror = () => reject(new Error("二维码预览加载失败。"));
    image.src = source;
  });
}

async function retry<T>(operation: () => Promise<T>, attempts: number, signal: AbortSignal): Promise<T> {
  let lastError: unknown;
  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    if (signal.aborted) throw new DOMException("Upload cancelled", "AbortError");
    try { return await operation(); } catch (cause) {
      lastError = cause;
      if (attempt < attempts) await new Promise((resolve) => window.setTimeout(resolve, attempt * 150));
    }
  }
  throw lastError;
}

async function runWithConcurrency<T>(items: T[], concurrency: number, worker: (item: T) => Promise<void>): Promise<void> {
  let next = 0;
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, async () => {
    while (next < items.length) { const item = items[next++]; if (item !== undefined) await worker(item); }
  }));
}

function uploadMarker(id: string) { return `<!-- ${markerPrefix}${id} -->`; }
function randomID() { return `${Date.now().toString(36)}-${crypto.randomUUID()}`; }
function statusLabel(status: UploadStatus) { return ({ queued: "等待上传", uploading: "上传中", complete: "已就绪", failed: "上传失败", cancelling: "正在取消" })[status]; }
function formatBytes(value: number) { if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GB`; if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(2)} MB`; if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`; return `${value} B`; }
function messageOf(cause: unknown) { return cause instanceof Error ? cause.message : "附件请求失败"; }
