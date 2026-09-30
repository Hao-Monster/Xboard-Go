import { useEffect, useId, useRef, useState } from "react";

export function MonthInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const [year, setYear] = useState(() => value ? Number(value.slice(0, 4)) : new Date().getFullYear());
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  const choose = (next: string) => { onChange(next); setOpen(false); trigger.current?.focus(); };
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false); };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); setOpen(false); trigger.current?.focus(); }
    };
    document.addEventListener("pointerdown", outside);
    window.addEventListener("keydown", escape, true);
    return () => { document.removeEventListener("pointerdown", outside); window.removeEventListener("keydown", escape, true); };
  }, [open]);
  return <div className="month-input" ref={root}>
    <span className="month-input-label">{label}</span>
    <button ref={trigger} className="button secondary" type="button" aria-label={label} aria-expanded={open} aria-controls={open ? id : undefined} aria-haspopup="dialog" onClick={() => { setYear(value ? Number(value.slice(0, 4)) : new Date().getFullYear()); setOpen(current => !current); }}>{value ? `${value.slice(0, 4)}年${Number(value.slice(5))}月` : "请选择月份"}<span aria-hidden="true">▣</span></button>
    {open && <div id={id} className="month-picker" role="dialog" aria-label={`${label}选择`}>
      <div className="month-picker-header"><button type="button" aria-label="上一年" disabled={year <= 1} onClick={() => setYear(current => current - 1)}>‹</button><strong>{year}年</strong><button type="button" aria-label="下一年" disabled={year >= 9999} onClick={() => setYear(current => current + 1)}>›</button></div>
      <div className="month-picker-grid">{Array.from({ length: 12 }, (_, index) => {
        const month = `${String(year).padStart(4, "0")}-${String(index + 1).padStart(2, "0")}`;
        return <button type="button" key={month} aria-pressed={value === month} onClick={() => choose(month)}>{index + 1}月</button>;
      })}</div>
      <div className="month-picker-footer"><button type="button" onClick={() => choose("")}>清除</button><button type="button" onClick={() => { const now = new Date(); choose(`${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`); }}>本月</button></div>
    </div>}
  </div>;
}
