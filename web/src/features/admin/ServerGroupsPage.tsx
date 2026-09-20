import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, ServerGroup } from "../../lib/api";
import "./ServerGroupsPage.css";

type GroupsAPI = Pick<AdminAPI, "listServerGroups" | "createServerGroup" | "updateServerGroup" | "deleteServerGroup">;

type SortKey = "id" | "name" | "users_count" | "server_count";
type SortDir = "asc" | "desc";

const PAGE_SIZES = [10, 20, 50];

export function ServerGroupsPage({ api }: { api: GroupsAPI }) {
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<ServerGroup | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<ServerGroup | null>(null);
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("id");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

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

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
    setPage(1);
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    return groups.filter((g) => !q || g.name.toLowerCase().includes(q));
  }, [groups, search]);

  const sorted = useMemo(() => {
    return [...filtered].sort((a, b) => {
      let cmp = 0;
      if (sortKey === "id") cmp = a.id - b.id;
      else if (sortKey === "name") cmp = a.name.localeCompare(b.name);
      else if (sortKey === "users_count") cmp = a.users_count - b.users_count;
      else if (sortKey === "server_count") cmp = a.server_count - b.server_count;
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [filtered, sortKey, sortDir]);

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const paged = sorted.slice((currentPage - 1) * pageSize, currentPage * pageSize);

  const sortIcon = (key: SortKey) => {
    if (sortKey !== key) return <span className="sg-sort-icon neutral" aria-hidden="true">↕</span>;
    return <span className="sg-sort-icon active" aria-hidden="true">{sortDir === "asc" ? "↑" : "↓"}</span>;
  };

  const goPage = (p: number) => setPage(Math.max(1, Math.min(p, totalPages)));

  return (
    <main className="page-shell resource-page sg-page">
      {/* Header */}
      <header className="sg-header">
        <div className="sg-header-text">
          <h1 className="sg-title">权限组管理</h1>
          <p className="sg-subtitle">管理所有权限组，包括添加、删除、编辑等操作。</p>
        </div>
      </header>

      {/* Toolbar */}
      <div className="sg-toolbar">
        <button className="button secondary compact sg-add-btn" onClick={() => setEditing(null)}>
          <span className="sg-plus" aria-hidden="true">+</span>
          添加权限组
        </button>
        <input
          className="sg-search"
          type="search"
          placeholder="搜索权限组..."
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(1); }}
          aria-label="搜索权限组"
        />
      </div>

      {/* Error */}
      {error !== "" && (
        <div className="alert error resource-alert" role="alert">
          {error}
          <button className="button ghost compact" onClick={() => void refresh()}>重试</button>
        </div>
      )}

      {/* Table */}
      {loading ? (
        <div className="empty-card">正在加载权限组…</div>
      ) : (
        <section className="resource-table-wrap sg-table-wrap" aria-label="权限组列表">
          <table className="resource-table sg-table">
            <thead>
              <tr>
                <th className="sg-th-id">
                  <button className="sg-sort-btn" onClick={() => toggleSort("id")}>
                    组ID {sortIcon("id")}
                  </button>
                </th>
                <th className="sg-th-name">
                  <button className="sg-sort-btn" onClick={() => toggleSort("name")}>
                    组名称 {sortIcon("name")}
                  </button>
                </th>
                <th className="sg-th-count">
                  <button className="sg-sort-btn" onClick={() => toggleSort("users_count")}>
                    用户数量 {sortIcon("users_count")}
                  </button>
                </th>
                <th className="sg-th-count">
                  <button className="sg-sort-btn" onClick={() => toggleSort("server_count")}>
                    节点数量 {sortIcon("server_count")}
                  </button>
                </th>
                <th className="sg-th-actions">操作</th>
              </tr>
            </thead>
            <tbody>
              {paged.length === 0 ? (
                <tr className="sg-empty-row">
                  <td colSpan={5} className="sg-empty-cell">{search ? "没有匹配的权限组。" : "暂无数据"}</td>
                </tr>
              ) : paged.map((group) => (
                <tr key={group.id}>
                  <td data-label="组ID" className="sg-td-id">
                    <span className="sg-id-badge">{group.id}</span>
                  </td>
                  <td data-label="组名称" className="sg-td-name">
                    <strong>{group.name}</strong>
                  </td>
                  <td data-label="用户数量" className="sg-td-count">
                    <span className="sg-count-cell">
                      <svg className="sg-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>
                      </svg>
                      {group.users_count}
                    </span>
                  </td>
                  <td data-label="节点数量" className="sg-td-count">
                    <span className="sg-count-cell">
                      <svg className="sg-icon" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                        <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>
                      </svg>
                      {group.server_count}
                    </span>
                  </td>
                  <td data-label="操作" className="sg-td-actions">
                    <div className="row-actions sg-row-actions">
                      <button
                        className="sg-icon-btn"
                        aria-label={`编辑权限组：${group.name}`}
                        title="编辑权限组"
                        onClick={() => setEditing(group)}
                      >
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                        </svg>
                      </button>
                      <button
                        className="sg-icon-btn sg-icon-btn-danger"
                        aria-label={`删除权限组：${group.name}`}
                        title="删除权限组"
                        onClick={() => setDeleting(group)}
                      >
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <polyline points="3 6 5 6 21 6"/>
                          <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                          <path d="M10 11v6M14 11v6"/>
                          <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
                        </svg>
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {/* Footer / Pagination — always visible when loaded */}
      {!loading && (
        <div className="sg-footer">
          <span className="sg-footer-status">已选择 0 项，共 {sorted.length} 项</span>
          <div className="sg-pagination">
            <label className="sg-page-size-label">
              每页显示
              <select
                className="sg-page-size-select"
                value={pageSize}
                onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}
                aria-label="每页显示条数"
              >
                {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </label>
            <span className="sg-page-info">
              第
              <input
                className="sg-page-input"
                type="number"
                min={1}
                max={Math.max(1, totalPages)}
                value={currentPage}
                onChange={(e) => goPage(Number(e.target.value))}
                aria-label="页码"
              />
              页，共 {totalPages} 页
            </span>
            <div className="sg-page-btns">
              <button className="sg-page-btn" onClick={() => goPage(1)} disabled={currentPage === 1 || sorted.length === 0} aria-label="首页">«</button>
              <button className="sg-page-btn" onClick={() => goPage(currentPage - 1)} disabled={currentPage === 1 || sorted.length === 0} aria-label="上一页">‹</button>
              <button className="sg-page-btn" onClick={() => goPage(currentPage + 1)} disabled={currentPage === totalPages || sorted.length === 0} aria-label="下一页">›</button>
              <button className="sg-page-btn" onClick={() => goPage(totalPages)} disabled={currentPage === totalPages || sorted.length === 0} aria-label="末页">»</button>
            </div>
          </div>
        </div>
      )}

      {/* Modals */}
      {editing !== undefined && (
        <GroupEditor
          api={api}
          group={editing}
          onClose={() => setEditing(undefined)}
          onSaved={(saved) => {
            setGroups((current) =>
              editing === null
                ? [saved, ...current]
                : current.map((item) => (item.id === saved.id ? saved : item))
            );
            setEditing(undefined);
          }}
        />
      )}
      {deleting !== null && (
        <GroupDelete
          api={api}
          group={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            setGroups((current) => current.filter((item) => item.id !== deleting.id));
            setDeleting(null);
          }}
        />
      )}
    </main>
  );
}

function GroupEditor({
  api,
  group,
  onClose,
  onSaved,
}: {
  api: GroupsAPI;
  group: ServerGroup | null;
  onClose: () => void;
  onSaved: (group: ServerGroup) => void;
}) {
  const isCreate = group === null;
  const title = isCreate ? "创建权限组" : "编辑权限组";
  const desc = isCreate
    ? "创建新的权限组，可以为不同的用户分配不同的权限。"
    : "修改权限组信息，更新后会立即生效。";
  const [name, setName] = useState(group?.name ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      onSaved(
        isCreate
          ? await api.createServerGroup(name)
          : await api.updateServerGroup(group.id, name)
      );
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title={title} onClose={onClose}>
      <div className="sg-modal-header">
        <div className="sg-modal-title-row">
          <h2 className="sg-modal-title">{title}</h2>
          <button className="sg-modal-close" aria-label={`关闭${title}`} onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/>
            </svg>
          </button>
        </div>
        <p className="sg-modal-desc">{desc}</p>
      </div>
      <form className="sg-modal-body" onSubmit={(event) => void submit(event)}>
        <div className="sg-form-field">
          <label className="sg-field-label" htmlFor="sg-group-name">组名称</label>
          <input
            id="sg-group-name"
            className="sg-field-input"
            value={name}
            maxLength={255}
            required
            placeholder="请输入权限组名称"
            onChange={(event) => setName(event.target.value)}
          />
          <p className="sg-field-hint">权限组名称用于标识不同的用户组，建议使用有意义的名称。</p>
        </div>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="sg-modal-footer">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" disabled={saving} type="submit">
            {saving ? "正在保存…" : isCreate ? "创建权限组" : "更新"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function GroupDelete({
  api,
  group,
  onClose,
  onDeleted,
}: {
  api: GroupsAPI;
  group: ServerGroup;
  onClose: () => void;
  onDeleted: () => void;
}) {
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
  return (
    <Modal title="删除权限组" onClose={onClose}>
      <div className="sg-modal-header">
        <div className="sg-modal-title-row">
          <h2 className="sg-modal-title">删除权限组</h2>
          <button className="sg-modal-close" aria-label="关闭删除权限组" onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/>
            </svg>
          </button>
        </div>
        <p className="sg-modal-desc">此操作不可撤销。如果仍有用户或节点引用此权限组，服务端会拒绝操作。</p>
      </div>
      <div className="sg-modal-body">
        <p className="sg-delete-confirm-text">确定删除权限组 <strong>"{group.name}"</strong> 吗？</p>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="sg-modal-footer">
          <button className="button ghost" onClick={onClose}>取消</button>
          <button className="button primary destructive" disabled={busy} onClick={() => void remove()}>
            {busy ? "正在删除…" : "确认删除"}
          </button>
        </div>
      </div>
    </Modal>
  );
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
