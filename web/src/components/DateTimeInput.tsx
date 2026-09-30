import { useState } from "react";
import "./DateRangeInput.css";

export function DateTimeInput({ label, value, onChange, placeholder = "请选择用户到期日期，留空为长期有效", clearLabel = "长期有效" }: { label: string; value: string; onChange: (value: string) => void; placeholder?: string; clearLabel?: string }) {
  const [open, setOpen] = useState(false);
  const [month, setMonth] = useState(() => { const day = value ? new Date(value) : new Date(); return new Date(day.getFullYear(), day.getMonth(), 1); });
  const days = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate();
  return <div className="date-range-input" onKeyDown={event => { if (open && event.key === "Escape") { event.stopPropagation(); setOpen(false); } }}>
    <span>{label}</span><button type="button" className="button secondary date-range-trigger" aria-label={label} aria-expanded={open} onClick={() => setOpen(!open)}>{value ? value.replace("T", " ") : placeholder}</button>
    {open && <div className="date-range-calendar" role="group" aria-label={`${label}日历`}>
      <div className="date-range-navigation"><button type="button" aria-label="上个月" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth()-1, 1))}>‹</button><strong>{month.getFullYear()}年{month.getMonth()+1}月</strong><button type="button" aria-label="下个月" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth()+1, 1))}>›</button></div>
      <div className="date-range-days">{["日", "一", "二", "三", "四", "五", "六"].map(day => <span key={day}>{day}</span>)}
        {Array.from({length:month.getDay()}, (_, i) => <span key={`blank-${i}`} />)}
        {Array.from({length:days}, (_, i) => { const date = `${month.getFullYear()}-${String(month.getMonth()+1).padStart(2,"0")}-${String(i+1).padStart(2,"0")}`; return <button type="button" key={date} aria-label={date} aria-pressed={value.slice(0,10) === date} onClick={() => { onChange(`${date}T${value.slice(11) || "00:00"}`); setOpen(false); }}>{i+1}</button>; })}
      </div><label>精确日期时间<input type="datetime-local" value={value} onChange={event => onChange(event.target.value)} /></label>
      <button type="button" className="button ghost compact" onClick={() => { onChange(""); setOpen(false); }}>{clearLabel}</button>
    </div>}
  </div>;
}
