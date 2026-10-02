import { useId, type ReactNode } from "react";
import { translateAdmin } from "../../lib/adminLocale";

const sectionIcons: Record<string, ReactNode> = {
  "基础配置": <><circle cx="12" cy="12" r="10"></circle><path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"></path><path d="M2 12h20"></path></>,
  "奖励内容": <><rect x="3" y="8" width="18" height="4" rx="1"></rect><path d="M12 8v13"></path><path d="M19 12v7a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-7"></path><path d="M7.5 8a2.5 2.5 0 0 1 0-5A4.8 8 0 0 1 12 8a4.8 8 0 0 1 4.5-5 2.5 2.5 0 0 1 0 5"></path></>,
  "使用条件": <><circle cx="12" cy="12" r="10"></circle><circle cx="12" cy="12" r="6"></circle><circle cx="12" cy="12" r="2"></circle></>,
  "使用限制": <><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"></path><path d="m9 12 2 2 4-4"></path></>,
  "特殊配置": <><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></>,
  "显示效果": <><circle cx="13.5" cy="6.5" r=".5" fill="currentColor"></circle><circle cx="17.5" cy="10.5" r=".5" fill="currentColor"></circle><circle cx="8.5" cy="7.5" r=".5" fill="currentColor"></circle><circle cx="6.5" cy="12.5" r=".5" fill="currentColor"></circle><path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"></path></>,
};

export function GiftSection({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return <section className="gift-section" role="group" aria-labelledby={id}><h3 id={id}><svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{sectionIcons[title]}</svg>{translateAdmin(title)}</h3>{children}</section>;
}

export function GiftField({ label, value, onChange, placeholder, unit, type = "text", required, maxLength, min, max, wide = false }: { label: string; value: string | number; onChange: (value: string) => void; placeholder?: string; unit?: string; type?: "text" | "number" | "url"; required?: boolean; maxLength?: number; min?: number; max?: number; wide?: boolean }) {
  const id = useId();
  return <div className={`gift-field${wide ? " gift-wide" : ""}`}><label htmlFor={id}>{translateAdmin(label)}</label><div className="gift-input-wrap"><input id={id} type={type} inputMode={unit ? "decimal" : undefined} min={min} max={max} required={required} maxLength={maxLength} placeholder={placeholder ? translateAdmin(placeholder) : undefined} value={value} onChange={event => onChange(event.target.value)} />{unit && <span className="gift-unit" aria-hidden="true">{unit}</span>}</div></div>;
}

export function GiftSwitch({ label, description, checked, onChange, compact = false, wide = false }: { label: string; description?: string; checked: boolean; onChange: (value: boolean) => void; compact?: boolean; wide?: boolean }) {
  const id = useId();
  return <div className={`gift-switch${compact ? " gift-switch-compact" : ""}${wide ? " gift-wide" : ""}`}><div><label htmlFor={id}>{translateAdmin(label)}</label>{description && <p id={`${id}-description`}>{translateAdmin(description)}</p>}</div><input id={id} type="checkbox" role="switch" checked={checked} aria-describedby={description ? `${id}-description` : undefined} onChange={event => onChange(event.target.checked)} /></div>;
}
