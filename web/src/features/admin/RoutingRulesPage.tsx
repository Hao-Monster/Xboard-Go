import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, RoutingAction, RoutingRule, RoutingRuleInput } from "../../lib/api";
import "./RoutingRulesPage.css";

type RoutesAPI = Pick<AdminAPI, "listRoutingRules" | "createRoutingRule" | "updateRoutingRule" | "deleteRoutingRule">;

const actionLabels: Record<RoutingAction, string> = {
  block: "禁止访问",
  dns: "指定DNS服务器进行解析",
  direct: "直连",
  proxy: "转发",
};

const actionShortLabels: Record<RoutingAction, string> = {
  block: "禁止访问",
  dns: "DNS解析",
  direct: "直连",
  proxy: "转发",
};

type SortKey = "id" | "remarks" | "action";
type SortDir = "asc" | "desc";
const PAGE_SIZES = [10, 20, 50];

export function RoutingRulesPage({ api }: { api: RoutesAPI }) {
  const [rules, setRules] = useState<RoutingRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<RoutingRule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<RoutingRule | null>(null);
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("id");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setRules(await api.listRoutingRules());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let live = true;
    void api.listRoutingRules().then((result) => {
      if (live) setRules(result);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  const toggleSort = (key: SortKey) => {
    if (sortKey === key) setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    else { setSortKey(key); setSortDir("asc"); }
    setPage(1);
  };

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return rules;
    return rules.filter((r) =>
      r.remarks.toLowerCase().includes(q) ||
      r.match.some((v) => v.toLowerCase().includes(q)) ||
      actionLabels[r.action].toLowerCase().includes(q)
    );
  }, [rules, search]);

  const sorted = useMemo(() => {
    return [...filtered].sort((a, b) => {
      let cmp = 0;
      if (sortKey === "id") cmp = a.id - b.id;
      else if (sortKey === "remarks") cmp = a.remarks.localeCompare(b.remarks);
      else if (sortKey === "action") cmp = a.action.localeCompare(b.action);
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [filtered, sortKey, sortDir]);

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const paged = sorted.slice((currentPage - 1) * pageSize, currentPage * pageSize);
  const goPage = (p: number) => setPage(Math.max(1, Math.min(p, totalPages)));

  const sortIcon = (key: SortKey) => {
    if (sortKey !== key) return <span className="rr-sort-icon neutral" aria-hidden="true">↕</span>;
    return <span className="rr-sort-icon active" aria-hidden="true">{sortDir === "asc" ? "↑" : "↓"}</span>;
  };

  return (
    <main className="page-shell resource-page rr-page">
      {/* Header */}
      <header className="rr-header">
        <h1 className="rr-title">路由管理</h1>
        <p className="rr-subtitle">管理所有路由规则，包括添加、删除、编辑等操作。</p>
      </header>

      {/* Toolbar */}
      <div className="rr-toolbar">
        <button className="button secondary compact rr-add-btn" onClick={() => setEditing(null)}>
          <span className="rr-plus" aria-hidden="true">+</span>
          添加路由
        </button>
        <input
          className="rr-search"
          type="search"
          placeholder="搜索路由..."
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(1); }}
          aria-label="搜索路由"
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
        <div className="empty-card">正在加载路由规则…</div>
      ) : (
        <section className="resource-table-wrap rr-table-wrap" aria-label="路由规则列表">
          <table className="resource-table rr-table">
            <thead>
              <tr>
                <th className="rr-th-id">
                  <button className="rr-sort-btn" onClick={() => toggleSort("id")}>
                    组ID {sortIcon("id")}
                  </button>
                </th>
                <th className="rr-th-remarks">
                  <button className="rr-sort-btn" onClick={() => toggleSort("remarks")}>
                    备注 {sortIcon("remarks")}
                  </button>
                </th>
                <th className="rr-th-match">动作值</th>
                <th className="rr-th-action">
                  <button className="rr-sort-btn" onClick={() => toggleSort("action")}>
                    动作 {sortIcon("action")}
                  </button>
                </th>
                <th className="rr-th-ops">操作</th>
              </tr>
            </thead>
            <tbody>
              {paged.length === 0 ? (
                <tr className="rr-empty-row">
                  <td colSpan={5} className="rr-empty-cell">暂无数据</td>
                </tr>
              ) : paged.map((rule) => (
                <tr key={rule.id}>
                  <td data-label="组ID" className="rr-td-id">
                    <span className="rr-id-badge">{rule.id}</span>
                  </td>
                  <td data-label="备注" className="rr-td-remarks">
                    <strong>{rule.remarks}</strong>
                  </td>
                  <td data-label="动作值" className="rr-td-match">
                    <div className="rr-match-list">
                      {rule.match.slice(0, 2).map((v) => (
                        <code key={v} className="rr-match-tag">{v}</code>
                      ))}
                      {rule.match.length > 2 && (
                        <span className="rr-match-more">+{rule.match.length - 2} 条</span>
                      )}
                    </div>
                    {rule.action_value && (
                      <span className="rr-action-value-inline">{rule.action_value}</span>
                    )}
                  </td>
                  <td data-label="动作" className="rr-td-action">
                    <span className={`rr-action-badge rr-action-${rule.action}`}>
                      {actionShortLabels[rule.action]}
                    </span>
                  </td>
                  <td data-label="操作" className="rr-td-ops">
                    <div className="row-actions rr-row-actions">
                      <button
                        className="rr-icon-btn"
                        aria-label={`编辑路由规则：${rule.remarks}`}
                        title="编辑"
                        onClick={() => setEditing(rule)}
                      >
                        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                          <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                          <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                        </svg>
                      </button>
                      <button
                        className="rr-icon-btn rr-icon-btn-danger"
                        aria-label={`删除路由规则：${rule.remarks}`}
                        title="删除"
                        onClick={() => setDeleting(rule)}
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
        <div className="rr-footer">
          <span className="rr-footer-status">已选择 0 项，共 {sorted.length} 项</span>
          <div className="rr-pagination">
            <label className="rr-page-size-label">
              每页显示
              <select
                className="rr-page-size-select"
                value={pageSize}
                onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}
                aria-label="每页显示条数"
              >
                {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </label>
            <span className="rr-page-info">
              第
              <input
                className="rr-page-input"
                type="number"
                min={1}
                max={Math.max(1, totalPages)}
                value={currentPage}
                onChange={(e) => goPage(Number(e.target.value))}
                aria-label="页码"
              />
              页，共 {totalPages} 页
            </span>
            <div className="rr-page-btns">
              <button className="rr-page-btn" onClick={() => goPage(1)} disabled={currentPage === 1 || sorted.length === 0} aria-label="首页">«</button>
              <button className="rr-page-btn" onClick={() => goPage(currentPage - 1)} disabled={currentPage === 1 || sorted.length === 0} aria-label="上一页">‹</button>
              <button className="rr-page-btn" onClick={() => goPage(currentPage + 1)} disabled={currentPage === totalPages || sorted.length === 0} aria-label="下一页">›</button>
              <button className="rr-page-btn" onClick={() => goPage(totalPages)} disabled={currentPage === totalPages || sorted.length === 0} aria-label="末页">»</button>
            </div>
          </div>
        </div>
      )}

      {/* Modals */}
      {editing !== undefined && (
        <RouteEditor
          api={api}
          rule={editing}
          onClose={() => setEditing(undefined)}
          onSaved={(saved) => {
            setRules((cur) =>
              editing === null ? [saved, ...cur] : cur.map((r) => r.id === saved.id ? saved : r)
            );
            setEditing(undefined);
          }}
        />
      )}
      {deleting !== null && (
        <RouteDelete
          api={api}
          rule={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            setRules((cur) => cur.filter((r) => r.id !== deleting.id));
            setDeleting(null);
          }}
        />
      )}
    </main>
  );
}

function RouteEditor({
  api, rule, onClose, onSaved,
}: {
  api: RoutesAPI;
  rule: RoutingRule | null;
  onClose: () => void;
  onSaved: (rule: RoutingRule) => void;
}) {
  const isCreate = rule === null;
  const title = isCreate ? "创建路由" : "编辑路由";
  const [remarks, setRemarks] = useState(rule?.remarks ?? "");
  const [matches, setMatches] = useState(rule?.match.join("\n") ?? "");
  const [action, setAction] = useState<RoutingAction>(rule?.action ?? "block");
  const [actionValue, setActionValue] = useState(rule?.action_value ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const needsValue = action === "dns" || action === "proxy";

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    const input: RoutingRuleInput = {
      remarks,
      match: matches.split(/\r?\n/).filter((v) => v.trim() !== ""),
      action,
      action_value: needsValue ? actionValue : "",
    };
    try {
      onSaved(isCreate ? await api.createRoutingRule(input) : await api.updateRoutingRule(rule.id, input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title={title} onClose={onClose}>
      <div className="rr-modal-header">
        <div className="rr-modal-title-row">
          <h2 className="rr-modal-title">{title}</h2>
          <button className="rr-modal-close" aria-label={`关闭${title}`} onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/>
            </svg>
          </button>
        </div>
      </div>
      <form className="rr-modal-body" onSubmit={(e) => void submit(e)}>
        <div className="rr-form-field">
          <label className="rr-field-label" htmlFor="rr-remarks">
            备注 <span className="rr-required" aria-hidden="true">*</span>
          </label>
          <input
            id="rr-remarks"
            className="rr-field-input"
            value={remarks}
            maxLength={255}
            required
            placeholder="请输入备注"
            onChange={(e) => setRemarks(e.target.value)}
          />
        </div>
        <div className="rr-form-field">
          <label className="rr-field-label" htmlFor="rr-match">
            匹配规则 <span className="rr-required" aria-hidden="true">*</span>
          </label>
          <textarea
            id="rr-match"
            className="rr-field-textarea"
            value={matches}
            maxLength={500_000}
            required
            rows={6}
            placeholder={"example.com\n*.example.com"}
            onChange={(e) => setMatches(e.target.value)}
          />
        </div>
        <div className="rr-form-field">
          <label className="rr-field-label" htmlFor="rr-action">
            动作 <span className="rr-required" aria-hidden="true">*</span>
          </label>
          <select
            id="rr-action"
            className="rr-field-select"
            value={action}
            onChange={(e) => setAction(e.target.value as RoutingAction)}
          >
            <option value="block">禁止访问</option>
            <option value="dns">指定DNS服务器进行解析</option>
            <option value="direct">直连</option>
            <option value="proxy">转发</option>
          </select>
        </div>
        {needsValue && (
          <div className="rr-form-field">
            <label className="rr-field-label" htmlFor="rr-action-value">
              {action === "dns" ? "DNS 服务器地址" : "代理出站标记"} <span className="rr-required" aria-hidden="true">*</span>
            </label>
            <input
              id="rr-action-value"
              className="rr-field-input"
              value={actionValue}
              maxLength={255}
              required
              placeholder={action === "dns" ? "例如：8.8.8.8" : "例如：proxy-out"}
              onChange={(e) => setActionValue(e.target.value)}
            />
          </div>
        )}
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="rr-modal-footer">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" disabled={saving} type="submit">
            {saving ? "正在保存…" : "确认"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function RouteDelete({
  api, rule, onClose, onDeleted,
}: {
  api: RoutesAPI;
  rule: RoutingRule;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true);
    setError("");
    try {
      await api.deleteRoutingRule(rule.id);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  };
  return (
    <Modal title="删除路由" onClose={onClose}>
      <div className="rr-modal-header">
        <div className="rr-modal-title-row">
          <h2 className="rr-modal-title">删除路由</h2>
          <button className="rr-modal-close" aria-label="关闭删除路由" onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/>
            </svg>
          </button>
        </div>
      </div>
      <div className="rr-modal-body">
        <p className="rr-delete-text">
          确定删除路由 <strong>"{rule.remarks}"</strong> 吗？如果仍有节点引用，服务端会拒绝操作。
        </p>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="rr-modal-footer">
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
