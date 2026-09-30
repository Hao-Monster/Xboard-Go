import { useState } from "react";

export function TagInput({ id, label, value, onChange }: { id?: string; label: string; value: string[]; onChange: (value: string[]) => void }) {
  const [draft, setDraft] = useState("");
  const add = (text = draft) => {
    const tags = text.split(/[,，\n]/).map(tag => tag.trim()).filter(Boolean);
    if (tags.length) onChange([...new Set([...value, ...tags])]);
    setDraft("");
  };
  return <div className="tag-input-control">
    {value.map(tag => <span className="tag-input-chip" key={tag}>{tag}<button type="button" aria-label={`移除标签 ${tag}`} onClick={() => onChange(value.filter(item => item !== tag))}>×</button></span>)}
    <input id={id} aria-label={label} value={draft} placeholder="输入后回车添加标签" onChange={event => setDraft(event.target.value)} onBlur={() => add()} onKeyDown={event => {
      if (event.key === "Enter" && !event.nativeEvent.isComposing) { event.preventDefault(); add(); }
    }} />
  </div>;
}
