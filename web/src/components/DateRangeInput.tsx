import { useEffect, useRef, useState } from "react";
import "./DateRangeInput.css";

const datePart = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;

export function DateRangeInput({ start, end, onStartChange, onEndChange }: {
  start: string; end: string; onStartChange: (value: string) => void; onEndChange: (value: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const [position, setPosition] = useState({left: 8, top: 8, width: 530});
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); event.stopImmediatePropagation(); setOpen(false); trigger.current?.focus(); } };
    const closeOnResize = () => setOpen(false);
    document.addEventListener("pointerdown", outside);
    window.addEventListener("keydown", escape, true);
    window.addEventListener("resize", closeOnResize);
    return () => { document.removeEventListener("pointerdown", outside); window.removeEventListener("keydown", escape, true); window.removeEventListener("resize", closeOnResize); };
  }, [open]);
  const [choosingEnd, setChoosingEnd] = useState(false);
  const [month, setMonth] = useState(() => { const date = new Date(start); return new Date(date.getFullYear(), date.getMonth(), 1); });
  const choose = (date: string) => {
    if (!choosingEnd || date < start.slice(0, 10)) {
      onStartChange(`${date}T${start.slice(11) || "00:00"}`);
      if (date > end.slice(0, 10)) onEndChange(`${date}T${end.slice(11) || "23:59"}`);
      setChoosingEnd(true);
    } else {
      onEndChange(`${date}T${end.slice(11) || "23:59"}`); setChoosingEnd(false);
    }
  };
  return <div className="date-range-input" ref={root}>
    <span>优惠券有效期</span>
    <button ref={trigger} type="button" className="button secondary date-range-trigger" aria-label="选择优惠券有效期" aria-expanded={open} onClick={() => {
      const rect = trigger.current!.getBoundingClientRect();
      const width = Math.min(530, window.innerWidth - 16);
      setPosition({left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)), top: Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - 480)), width});
      setOpen(!open);
    }}>{start.replace("T", " ")} - {end.replace("T", " ")}</button>
    {open && <div className="date-range-calendar" style={position} role="group" aria-label="优惠券有效期日历">
      <div className="date-range-navigation"><button type="button" aria-label="上个月" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() - 1, 1))}>‹</button><span>{choosingEnd ? "选择结束日期" : "选择开始日期"}</span><button type="button" aria-label="下个月" onClick={() => setMonth(new Date(month.getFullYear(), month.getMonth() + 1, 1))}>›</button></div>
      <div className="date-range-months">{[0, 1].map(offset => {
        const first = new Date(month.getFullYear(), month.getMonth() + offset, 1);
        const days = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate();
        return <section key={offset} aria-label={`${first.getFullYear()}年${first.getMonth() + 1}月`}><strong>{first.getFullYear()}年{first.getMonth() + 1}月</strong><div className="date-range-days">
          {["日", "一", "二", "三", "四", "五", "六"].map(day => <span key={day}>{day}</span>)}
          {Array.from({length:first.getDay()}, (_, index) => <span key={`blank-${index}`} />)}
          {Array.from({length:days}, (_, index) => { const date = datePart(new Date(first.getFullYear(), first.getMonth(), index + 1)); return <button type="button" key={date} aria-label={date} aria-pressed={date === start.slice(0, 10) || date === end.slice(0, 10)} className={date >= start.slice(0, 10) && date <= end.slice(0, 10) ? "in-range" : ""} onClick={() => choose(date)}>{index + 1}</button>; })}
        </div></section>;
      })}</div>
      <div className="form-grid"><label>开始时间<input type="datetime-local" required value={start} onChange={event => onStartChange(event.target.value)} /></label><label>结束时间<input type="datetime-local" required value={end} onChange={event => onEndChange(event.target.value)} /></label></div>
      <button type="button" className="button secondary compact" onClick={() => setOpen(false)}>完成日期选择</button>
    </div>}
  </div>;
}
