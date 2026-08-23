import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, RoutingAction, RoutingRule, RoutingRuleInput } from "../../lib/api";

type RoutesAPI = Pick<AdminAPI, "listRoutingRules" | "createRoutingRule" | "updateRoutingRule" | "deleteRoutingRule">;

const actionLabels: Record<RoutingAction, string> = { block: "阻断", direct: "直连", dns: "DNS", proxy: "代理" };

export function RoutingRulesPage({ api }: { api: RoutesAPI }) {
  const [rules, setRules] = useState<RoutingRule[]>([]);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<RoutingRule | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<RoutingRule | null>(null);
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
  const visible = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return query === "" ? rules : rules.filter((rule) => rule.remarks.toLocaleLowerCase().includes(query) || rule.match.some((value) => value.toLocaleLowerCase().includes(query)));
  }, [rules, search]);

  return <main className="page-shell resource-page">
    <header className="page-header">
      <div><p className="eyebrow">Node routing</p><h1>路由规则</h1><p className="muted">规则按编号顺序下发到关联节点。被节点使用的规则不能删除。</p></div>
      <button className="button primary" onClick={() => setEditing(null)}>新增路由规则</button>
    </header>
    <div className="resource-toolbar"><label>搜索规则<input type="search" value={search} placeholder="备注或匹配内容" onChange={(event) => setSearch(event.target.value)} /></label></div>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>重试</button></div>}
    {loading ? <div className="empty-card">正在加载路由规则…</div> : visible.length === 0 ? <div className="empty-card">{rules.length === 0 ? "尚未创建路由规则。" : "没有符合搜索条件的路由规则。"}</div> : (
      <section className="resource-table-wrap" aria-label="路由规则列表"><table className="resource-table route-table">
        <thead><tr><th>备注</th><th>匹配规则</th><th>动作</th><th>动作值</th><th>操作</th></tr></thead>
        <tbody>{visible.map((rule) => <tr key={rule.id}>
          <td data-label="备注"><strong>{rule.remarks}</strong><small className="muted monospace">RID {rule.id}</small></td>
          <td data-label="匹配规则"><div className="match-list">{rule.match.slice(0, 3).map((value) => <code key={value}>{value}</code>)}{rule.match.length > 3 && <small className="muted">另有 {rule.match.length - 3} 条</small>}</div></td>
          <td data-label="动作"><span className={`route-action ${rule.action}`}>{actionLabels[rule.action]}</span></td>
          <td data-label="动作值"><span className="monospace">{rule.action_value || "—"}</span></td>
          <td data-label="操作"><div className="row-actions"><button className="button secondary compact" aria-label={`编辑路由规则：${rule.remarks}`} onClick={() => setEditing(rule)}>编辑</button><button className="button ghost compact danger-text" aria-label={`删除路由规则：${rule.remarks}`} onClick={() => setDeleting(rule)}>删除</button></div></td>
        </tr>)}</tbody>
      </table></section>
    )}
    {editing !== undefined && <RouteEditor api={api} rule={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => {
      setRules((current) => editing === null ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
      setEditing(undefined);
    }} />}
    {deleting !== null && <RouteDelete api={api} rule={deleting} onClose={() => setDeleting(null)} onDeleted={() => {
      setRules((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
    }} />}
  </main>;
}

function RouteEditor({ api, rule, onClose, onSaved }: { api: RoutesAPI; rule: RoutingRule | null; onClose: () => void; onSaved: (rule: RoutingRule) => void }) {
  const title = rule === null ? "新增路由规则" : "编辑路由规则";
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
    const input: RoutingRuleInput = { remarks, match: matches.split(/\r?\n/), action, action_value: needsValue ? actionValue : "" };
    try {
      onSaved(rule === null ? await api.createRoutingRule(input) : await api.updateRoutingRule(rule.id, input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };
  return <Modal title={title} onClose={onClose}>
    <ModalHeader title={title} onClose={onClose} />
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>备注<input value={remarks} maxLength={255} required onChange={(event) => setRemarks(event.target.value)} /></label>
      <label>匹配规则<textarea value={matches} maxLength={500_000} required placeholder={"每行一条，例如：\n*.example.com\n10.0.0.0/8\ngeosite:cn"} onChange={(event) => setMatches(event.target.value)} /></label>
      <label>动作<select value={action} onChange={(event) => setAction(event.target.value as RoutingAction)}><option value="block">阻断</option><option value="direct">直连</option><option value="dns">DNS</option><option value="proxy">代理</option></select></label>
      {needsValue && <label>{action === "dns" ? "DNS 出站标记" : "代理出站标记"}<input value={actionValue} maxLength={255} required onChange={(event) => setActionValue(event.target.value)} /></label>}
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving} type="submit">{saving ? "正在保存…" : "保存"}</button></div>
    </form>
  </Modal>;
}

function RouteDelete({ api, rule, onClose, onDeleted }: { api: RoutesAPI; rule: RoutingRule; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true);
    setError("");
    try { await api.deleteRoutingRule(rule.id); onDeleted(); } catch (cause) { setError(errorMessage(cause)); setBusy(false); }
  };
  return <Modal title="删除路由规则" onClose={onClose}><ModalHeader title="删除路由规则" onClose={onClose} /><p>确定删除“{rule.remarks}”吗？如果仍有节点引用，服务端会拒绝操作。</p>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : "确认删除"}</button></div></Modal>;
}

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>;
}

function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : "请求失败，请稍后重试"; }
