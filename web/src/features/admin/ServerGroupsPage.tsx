import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, ServerGroup } from "../../lib/api";

type GroupsAPI = Pick<AdminAPI, "listServerGroups" | "createServerGroup" | "updateServerGroup" | "deleteServerGroup">;

export function ServerGroupsPage({ api }: { api: GroupsAPI }) {
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<ServerGroup | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<ServerGroup | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setGroups(await api.listServerGroups());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let live = true;
    void api.listServerGroups().then((result) => {
      if (live) setGroups(result);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  return (
    <main className="page-shell resource-page">
      <header className="page-header">
        <div>
          <p className="eyebrow">Access control</p>
          <h1>权限组</h1>
          <p className="muted">管理用户与节点之间的访问边界。被引用的权限组不能删除。</p>
        </div>
        <button className="button primary" onClick={() => setEditing(null)}>新增权限组</button>
      </header>

      {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>重试</button></div>}
      {loading ? <div className="empty-card">正在加载权限组…</div> : groups.length === 0 ? (
        <div className="empty-card">尚未创建权限组。</div>
      ) : (
        <section className="resource-table-wrap" aria-label="权限组列表">
          <table className="resource-table">
            <thead><tr><th>名称</th><th>用户数</th><th>节点数</th><th>操作</th></tr></thead>
            <tbody>{groups.map((group) => (
              <tr key={group.id}>
                <td data-label="名称"><strong>{group.name}</strong><small className="muted monospace">GID {group.id}</small></td>
                <td data-label="用户数"><span className="count-pill">{group.users_count}</span></td>
                <td data-label="节点数"><span className="count-pill">{group.server_count}</span></td>
                <td data-label="操作"><div className="row-actions">
                  <button className="button secondary compact" aria-label={`编辑权限组：${group.name}`} onClick={() => setEditing(group)}>编辑</button>
                  <button className="button ghost compact danger-text" aria-label={`删除权限组：${group.name}`} onClick={() => setDeleting(group)}>删除</button>
                </div></td>
              </tr>
            ))}</tbody>
          </table>
        </section>
      )}

      {editing !== undefined && <GroupEditor api={api} group={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => {
        setGroups((current) => editing === null ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
        setEditing(undefined);
      }} />}
      {deleting !== null && <GroupDelete api={api} group={deleting} onClose={() => setDeleting(null)} onDeleted={() => {
        setGroups((current) => current.filter((item) => item.id !== deleting.id));
        setDeleting(null);
      }} />}
    </main>
  );
}

function GroupEditor({ api, group, onClose, onSaved }: { api: GroupsAPI; group: ServerGroup | null; onClose: () => void; onSaved: (group: ServerGroup) => void }) {
  const title = group === null ? "新增权限组" : "编辑权限组";
  const [name, setName] = useState(group?.name ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      onSaved(group === null ? await api.createServerGroup(name) : await api.updateServerGroup(group.id, name));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };
  return <Modal title={title} onClose={onClose}>
    <ModalHeader title={title} onClose={onClose} />
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>权限组名称<input value={name} maxLength={255} required onChange={(event) => setName(event.target.value)} /></label>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving} type="submit">{saving ? "正在保存…" : "保存"}</button></div>
    </form>
  </Modal>;
}

function GroupDelete({ api, group, onClose, onDeleted }: { api: GroupsAPI; group: ServerGroup; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true);
    setError("");
    try {
      await api.deleteServerGroup(group.id);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  };
  return <Modal title="删除权限组" onClose={onClose}>
    <ModalHeader title="删除权限组" onClose={onClose} />
    <p>确定删除“{group.name}”吗？如果仍有用户或节点引用，服务端会拒绝操作。</p>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : "确认删除"}</button></div>
  </Modal>;
}

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>;
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
