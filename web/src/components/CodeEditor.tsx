import { useContext, useRef } from "react";
import { AutoSaveSchedule } from "./AutoSaveForm";

export function CodeEditor({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  const gutter = useRef<HTMLPreElement>(null);
  const scheduleSave = useContext(AutoSaveSchedule);
  return <div className="code-source-editor">
    <pre ref={gutter} className="code-source-gutter" aria-hidden="true">{value.split("\n").map((_, index) => index + 1).join("\n")}</pre>
    <textarea aria-label={label} value={value} spellCheck={false} wrap="off" onChange={event => onChange(event.target.value)}
      onScroll={event => { if (gutter.current) gutter.current.scrollTop = event.currentTarget.scrollTop; }}
      onKeyDown={event => {
        // Tab stays available for focus navigation; use the editor convention
        // Ctrl+] / Ctrl+[ to indent and outdent the selected lines.
        if (!(event.ctrlKey || event.metaKey) || !["[", "]"].includes(event.key)) return;
        event.preventDefault();
        const field = event.currentTarget;
        const start = value.lastIndexOf("\n", field.selectionStart - 1) + 1;
        const endOfLine = value.indexOf("\n", field.selectionEnd);
        const end = endOfLine < 0 ? value.length : endOfLine;
        const block = value.slice(start, end).split("\n").map(line => event.key === "]" ? `  ${line}` : line.replace(/^ {1,2}/, "")).join("\n");
        onChange(value.slice(0, start) + block + value.slice(end));
        scheduleSave?.();
        requestAnimationFrame(() => field.setSelectionRange(start, start + block.length));
      }} />
  </div>;
}
