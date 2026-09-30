import { createContext, useEffect, useRef, type FormEvent, type ReactNode } from "react";

export const AutoSaveSchedule = createContext<(() => void) | null>(null);

/** Legacy settings save one second after the last edit. Lock the form while
 * saving so a returned snapshot cannot overwrite a newer unsaved edit. */
export function AutoSaveForm({ children, saving, onSubmit, className }: {
  children: ReactNode;
  saving: boolean;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  className?: string;
}) {
  const form = useRef<HTMLFormElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const cancel = () => { clearTimeout(timer.current); timer.current = undefined; };
  useEffect(() => () => clearTimeout(timer.current), []);
  const schedule = () => {
    cancel();
    timer.current = setTimeout(() => {
      timer.current = undefined;
      if (form.current?.isConnected && form.current.checkValidity()) form.current.requestSubmit();
    }, 1000);
  };
  return <form ref={form} className={`auto-save-form ${className ?? ""}`} onChange={schedule} onClick={event => {
    if (event.target instanceof Element && event.target.closest("button[data-autosave]")) schedule();
  }} onSubmit={event => { cancel(); if (saving) { event.preventDefault(); return; } onSubmit(event); }}>
    <AutoSaveSchedule.Provider value={schedule}><fieldset className="auto-save-fields" disabled={saving}>{children}</fieldset></AutoSaveSchedule.Provider>
  </form>;
}
