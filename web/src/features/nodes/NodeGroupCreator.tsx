import { useState } from "react";
import type { ServerGroup } from "../../lib/api";

/** Inline editor keeps the node draft intact while creating a permission group. */
export function NodeGroupCreator({ create, onCreated }: {
  create: (name: string) => Promise<ServerGroup>;
  onCreated: (group: ServerGroup) => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const save = async () => {
    if (busy || !name.trim()) return;
    setBusy(true); setError("");
    try {
      const group = await create(name.trim());
      onCreated(group); setOpen(false); setName("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "权限组创建失败"); }
    finally { setBusy(false); }
  };
  return <div className="node-group-creator">
    <button type="button" className="button ghost compact" aria-expanded={open} onClick={() => setOpen(!open)} disabled={busy}>添加权限组</button>
    {open && <div className="form-stack">
      <label>权限组名称<input value={name} maxLength={255} disabled={busy} onChange={event => setName(event.target.value)} onKeyDown={event => {
        if (event.key === "Enter") { event.preventDefault(); void save(); }
      }} /></label>
      {error && <div role="alert" className="alert error">{error}</div>}
      <div className="row-actions"><button type="button" className="button secondary compact" disabled={busy} onClick={() => setOpen(false)}>取消添加</button>
        <button type="button" className="button primary compact" disabled={busy || !name.trim()} onClick={() => void save()}>{busy ? "正在添加…" : "确认添加"}</button></div>
    </div>}
  </div>;
}
