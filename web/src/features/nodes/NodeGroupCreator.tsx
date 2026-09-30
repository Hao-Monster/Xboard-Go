import { translateAdmin } from "../../lib/adminLocale";
import { useState } from "react";
import type { ServerGroup } from "../../lib/api";
import { Modal } from "../../components/Overlay";

/** Suspend the parent dialog while preserving its unsaved node draft. */
export function NodeGroupCreator({ create, onCreated, onOpenChange, buttonLabel = "添加权限组" }: {
  buttonLabel?: string;
  create: (name: string) => Promise<ServerGroup>;
  onCreated: (group: ServerGroup) => void;
  onOpenChange?: (open: boolean) => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const changeOpen = (next: boolean) => { setOpen(next); onOpenChange?.(next); };
  const save = async () => {
    if (busy || !name.trim()) return;
    setBusy(true); setError("");
    try {
      const group = await create(name.trim());
      onCreated(group); changeOpen(false); setName("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "权限组创建失败"); }
    finally { setBusy(false); }
  };
  return <div className="node-group-creator">
    <button type="button" className="button ghost compact" aria-expanded={open} onClick={() => { setError(""); changeOpen(true); }} disabled={busy}>{buttonLabel}</button>
    {open && <Modal title={translateAdmin("创建权限组")} onClose={() => { if (!busy) changeOpen(false); }}><div className="form-stack">
      <h2>{translateAdmin("创建权限组")}</h2>
      <p className="muted">{translateAdmin("创建新的权限组，可以为不同的用户分配不同的权限。")}</p>
      <label>权限组名称<input autoFocus value={name} maxLength={255} disabled={busy} onChange={event => setName(event.target.value)} onKeyDown={event => {
        if (event.key === "Enter") { event.preventDefault(); void save(); }
      }} /></label>
      {error && <div role="alert" className="alert error">{error}</div>}
      <div className="form-actions"><button type="button" className="button secondary compact" disabled={busy} onClick={() => changeOpen(false)}>{translateAdmin("取消")}</button>
        <button type="button" className="button primary compact" disabled={busy || !name.trim()} onClick={() => void save()}>{busy ? "正在添加…" : translateAdmin("创建权限组")}</button></div>
    </div></Modal>}
  </div>;
}
