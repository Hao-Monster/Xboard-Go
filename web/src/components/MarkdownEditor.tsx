import { useRef, useState } from "react";
import { SafeMarkdown } from "./SafeMarkdown";
import "./MarkdownEditor.css";

const actions = [
  { label: "粗体", before: "**", after: "**", sample: "粗体文字" },
  { label: "斜体", before: "*", after: "*", sample: "斜体文字" },
  { label: "下划线", before: "<u>", after: "</u>", sample: "文字" },
  { label: "删除线", before: "~~", after: "~~", sample: "文字" },
  { label: "引用", before: "> ", after: "", sample: "引用文字" },
  { label: "无序列表", before: "- ", after: "", sample: "列表项" },
  { label: "有序列表", before: "1. ", after: "", sample: "列表项" },
  { label: "换行", before: "  \n", after: "", sample: "" },
  { label: "代码", before: "`", after: "`", sample: "代码" },
  { label: "代码块", before: "\n```\n", after: "\n```\n", sample: "代码" },
  { label: "链接", before: "[", after: "](https://)", sample: "链接文字" },
  { label: "图片", before: "![", after: "](https://)", sample: "图片说明" },
  { label: "表格", before: "\n", after: "\n", sample: "| 标题 | 标题 |\n| --- | --- |\n| 内容 | 内容 |" },
  { label: "分隔线", before: "\n", after: "\n", sample: "---" },
];

/** Edits Markdown source only; rendering continues through the existing safe renderer. */
export function MarkdownEditor({ id, label, value, onChange, required, maxLength, rows = 8 }: {
  id?: string; label: string; value: string; onChange: (value: string) => void;
  required?: boolean; maxLength?: number; rows?: number;
}) {
  const textarea = useRef<HTMLTextAreaElement>(null);
  const [headingsOpen, setHeadingsOpen] = useState(false);
  const [preview, setPreview] = useState(false);
  const [past, setPast] = useState<string[]>([]);
  const [future, setFuture] = useState<string[]>([]);
  const commit = (next: string) => {
    if (next === value || (maxLength !== undefined && next.length > maxLength)) return;
    setPast(current => [...current.slice(-99), value]);
    setFuture([]);
    onChange(next);
  };
  const insert = (action: typeof actions[number]) => {
    const field = textarea.current;
    if (!field) return;
    const start = field.selectionStart;
    const end = field.selectionEnd;
    const selected = value.slice(start, end) || action.sample;
    const next = value.slice(0, start) + action.before + selected + action.after + value.slice(end);
    commit(next);
    requestAnimationFrame(() => {
      field.focus();
      field.setSelectionRange(start + action.before.length, start + action.before.length + selected.length);
    });
  };
  const undo = () => {
    const previous = past.at(-1);
    if (previous === undefined) return;
    setPast(past.slice(0, -1)); setFuture([value, ...future]); onChange(previous);
  };
  const redo = () => {
    const next = future[0];
    if (next === undefined) return;
    setFuture(future.slice(1)); setPast([...past, value]); onChange(next);
  };
  return <div className="markdown-source-editor">
    <div className="markdown-source-toolbar" role="group" aria-label={`${label}格式工具`}>
      <div className="markdown-heading-menu"><button type="button" title="标题" aria-label={`${label}：标题`} aria-expanded={headingsOpen} onClick={() => setHeadingsOpen(!headingsOpen)}>H⌄</button>
        {headingsOpen && <div role="group" aria-label="标题级别">{[1, 2, 3, 4, 5, 6].map(level => <button type="button" key={level} onMouseDown={event => event.preventDefault()} onClick={() => { insert({ label: "标题", before: "#".repeat(level) + " ", after: "", sample: "标题" }); setHeadingsOpen(false); }}>H{level}</button>)}</div>}
      </div>
      {actions.map(action => <button type="button" key={action.label} title={action.label} aria-label={`${label}：${action.label}`} onMouseDown={event => event.preventDefault()} onClick={() => insert(action)}>{({粗体: "B", 斜体: "I", 下划线: "U̲", 删除线: "S̶", 引用: "❞", 无序列表: "☷", 有序列表: "≡", 换行: "↵", 代码: "‹›", 代码块: "{ }", 链接: "↗", 图片: "▧", 表格: "▦", 分隔线: "—"} as Record<string, string>)[action.label]}</button>)}
      <button type="button" title="清空" aria-label={`${label}：清空`} onClick={() => commit("")}>⌫</button>
      <button type="button" disabled={!past.length} onClick={undo}>撤销</button>
      <button type="button" disabled={!future.length} onClick={redo}>重做</button>
      <button type="button" title="显示编辑器与预览" aria-label={`${label}：显示编辑器与预览`} aria-pressed={preview} onClick={() => setPreview(!preview)}>◫</button>
    </div>
    <div className={preview ? "markdown-editor-panes" : ""}><textarea id={id} aria-label={label} ref={textarea} rows={rows} value={value} required={required} maxLength={maxLength} onChange={event => commit(event.target.value)} />
    {preview && <div className="markdown-body" role="region" aria-label={`${label}预览`}><SafeMarkdown>{value}</SafeMarkdown></div>}</div>
  </div>;
}
