import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type {
  AdminAPI, AdminNode, AdminNodeQuery, AdminNodeRevision, AdminNodeStateInput, AdminNodeUpdateInput, Machine, ServerGroup
} from "../../lib/api";

type NodeManagementAPI = Pick<AdminAPI,
  "listAdminNodes" | "listMachines" | "listServerGroups" | "updateAdminNode" | "copyAdminNode" |
  "reorderAdminNodes" | "updateAdminNodeStates" | "resetAdminNodeTraffic" | "deleteAdminNodes"
>;

interface Props {
  api: NodeManagementAPI;
}

const pageSize = 500;
const protocols = [
  ["shadowsocks", "Shadowsocks"], ["vmess", "VMess"], ["trojan", "Trojan"], ["hysteria", "Hysteria"],
  ["vless", "VLess"], ["tuic", "TUIC"], ["socks", "SOCKS"], ["naive", "Naive"], ["http", "HTTP"],
  ["mieru", "Mieru"], ["anytls", "AnyTLS"]
] as const;

export function NodeManagementPage({ api }: Props) {
  const [nodes, setNodes] = useState<AdminNode[]>([]);
  const [machines, setMachines] = useState<Machine[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<number[]>([]);
  const [editing, setEditing] = useState<AdminNode | null>(null);
  const [confirming, setConfirming] = useState<{ kind: "reset" | "delete"; targets: AdminNodeRevision[] } | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [queryInput, setQueryInput] = useState("");
  const [typeInput, setTypeInput] = useState("");
  const [showInput, setShowInput] = useState("");
  const [enabledInput, setEnabledInput] = useState("");
  const [machineInput, setMachineInput] = useState("");
  const [bulkMachine, setBulkMachine] = useState("");
  const [filters, setFilters] = useState<Omit<AdminNodeQuery, "page" | "page_size">>({});

  useEffect(() => {
    let live = true;
    void Promise.all([api.listMachines(), api.listServerGroups()]).then(([nextMachines, nextGroups]) => {
      if (!live) return;
      setMachines(nextMachines);
      setGroups(nextGroups);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    });
    return () => { live = false; };
  }, [api]);

  useEffect(() => {
    let live = true;
    void api.listAdminNodes({ page, page_size: pageSize, ...filters }).then((result) => {
      if (!live) return;
      setNodes(result.items);
      setTotal(result.total);
      setSelected([]);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, filters, page, reloadToken]);

  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);
  const selectedTargets = useMemo(() => nodes.filter((node) => selected.includes(node.id)).map(nodeRevision), [nodes, selected]);
  const pageCount = Math.max(1, Math.ceil(total / pageSize));

  const refresh = useCallback(() => {
    setLoading(true);
    setError("");
    setReloadToken((current) => current + 1);
  }, []);
  const applyFilters = (event: FormEvent) => {
    event.preventDefault();
    const next: Omit<AdminNodeQuery, "page" | "page_size"> = {};
    if (queryInput.trim() !== "") next.q = queryInput.trim();
    if (typeInput !== "") next.type = typeInput;
    if (showInput !== "") next.show = showInput === "shown";
    if (enabledInput !== "") next.enabled = enabledInput === "enabled";
    if (machineInput === "unassigned") next.unassigned = true;
    else if (machineInput !== "") next.machine_id = Number(machineInput);
    setLoading(true); setError(""); setPage(1);
    setFilters(next);
  };
  const resetFilters = () => {
    setQueryInput(""); setTypeInput(""); setShowInput(""); setEnabledInput(""); setMachineInput("");
    setLoading(true); setError(""); setPage(1); setFilters({});
  };

  const run = async (operation: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await operation();
      refresh();
      return true;
    } catch (cause) {
      setError(errorMessage(cause));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const updateState = (input: Omit<AdminNodeStateInput, "targets">) => {
    if (selectedTargets.length === 0) return;
    void run(() => api.updateAdminNodeStates({ targets: selectedTargets, ...input }));
  };
  const applyMachine = () => {
    if (bulkMachine === "" || selectedTargets.length === 0) return;
    updateState({ machine_id: bulkMachine === "unassigned" ? null : Number(bulkMachine) });
  };
  const moveNode = (node: AdminNode, offset: -1 | 1) => {
    const index = nodes.findIndex((item) => item.id === node.id);
    const target = index + offset;
    if (index < 0 || target < 0 || target >= nodes.length) return;
    const ordered = [...nodes];
    [ordered[index], ordered[target]] = [ordered[target]!, ordered[index]!];
    void run(() => api.reorderAdminNodes(ordered.map(nodeRevision)));
  };

  return <main className="page-shell node-management-page">
    <header className="page-header"><div><p className="eyebrow">基础设施</p><h1>节点管理</h1><p className="muted">管理节点显隐、运行状态、部署归属、顺序和累计流量。</p></div></header>
    <section className="overview-grid node-overview" aria-label="节点概览">
      <Overview label="节点" value={total} />
      <Overview label="当前页显示" value={nodes.filter((node) => node.show).length} />
      <Overview label="当前页启用" value={nodes.filter((node) => node.enabled).length} />
      <Overview label="当前页在线" value={nodes.reduce((sum, node) => sum + node.online_count, 0)} />
    </section>
    <form className="filter-bar node-filter-bar" aria-label="节点筛选" onSubmit={applyFilters}>
      <label className="search-field">搜索<input aria-label="搜索节点" type="search" placeholder="名称、地址或 ID" value={queryInput} onChange={(event) => setQueryInput(event.target.value)} /></label>
      <label>协议<select aria-label="协议筛选" value={typeInput} onChange={(event) => setTypeInput(event.target.value)}><option value="">全部</option>{protocols.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label>显隐<select aria-label="显隐筛选" value={showInput} onChange={(event) => setShowInput(event.target.value)}><option value="">全部</option><option value="shown">显示</option><option value="hidden">隐藏</option></select></label>
      <label>启停<select aria-label="启停筛选" value={enabledInput} onChange={(event) => setEnabledInput(event.target.value)}><option value="">全部</option><option value="enabled">启用</option><option value="disabled">停用</option></select></label>
      <label>部署<select aria-label="部署筛选" value={machineInput} onChange={(event) => setMachineInput(event.target.value)}><option value="">全部</option><option value="unassigned">独立部署</option>{machines.map((machine) => <option key={machine.id} value={machine.id}>{machine.name}</option>)}</select></label>
      <div className="row-actions"><button className="button primary compact" type="submit">查询节点</button><button className="button ghost compact" type="button" onClick={resetFilters}>重置</button></div>
    </form>
    <section className="node-bulk-toolbar" aria-label="节点批量操作">
      <span>已选择 {selectedTargets.length} 个节点</span>
      <div className="node-bulk-actions">
        <button className="button compact secondary" disabled={busy || selectedTargets.length === 0} onClick={() => updateState({ show: true })}>批量显示</button>
        <button className="button compact secondary" disabled={busy || selectedTargets.length === 0} onClick={() => updateState({ show: false })}>批量隐藏</button>
        <button className="button compact secondary" disabled={busy || selectedTargets.length === 0} onClick={() => updateState({ enabled: true })}>批量启用</button>
        <button className="button compact secondary" disabled={busy || selectedTargets.length === 0} onClick={() => updateState({ enabled: false })}>批量停用</button>
        <select aria-label="批量绑定服务器" value={bulkMachine} onChange={(event) => setBulkMachine(event.target.value)}><option value="">选择服务器</option><option value="unassigned">解除绑定</option>{machines.map((machine) => <option key={machine.id} value={machine.id}>{machine.name}</option>)}</select>
        <button className="button compact secondary" disabled={busy || selectedTargets.length === 0 || bulkMachine === ""} onClick={applyMachine}>应用绑定</button>
        <button className="button compact ghost" disabled={busy || selectedTargets.length === 0} onClick={() => setConfirming({ kind: "reset", targets: selectedTargets })}>批量重置流量</button>
        <button className="button compact ghost danger-text" disabled={busy || selectedTargets.length === 0} onClick={() => setConfirming({ kind: "delete", targets: selectedTargets })}>批量删除</button>
      </div>
    </section>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    {loading ? <div className="empty-card" aria-live="polite">正在加载节点…</div> : <section className="resource-table-wrap node-table-wrap">
      <table className="resource-table node-table" aria-label="节点列表"><thead><tr><th>节点ID</th><th>显隐</th><th>节点</th><th>部署方式</th><th>地址</th><th>在线人数</th><th>倍率</th><th>权限组</th><th>流量使用</th><th>操作</th></tr></thead>
        <tbody>{nodes.length === 0 ? <tr className="empty-table-row"><td colSpan={10}>没有符合条件的节点。</td></tr> : nodes.map((node, index) => <tr key={node.id}>
          <td data-label="节点ID"><label className="node-select"><input type="checkbox" aria-label={`选择节点：${node.name}`} checked={selected.includes(node.id)} disabled={busy} onChange={(event) => setSelected((current) => event.target.checked ? [...current, node.id] : current.filter((id) => id !== node.id))} /><strong>#{node.id}</strong></label></td>
          <td data-label="显隐"><span className={`status-badge ${node.show ? "enabled" : "blocked"}`}>{node.show ? "显示" : "隐藏"}</span></td>
          <td data-label="节点"><strong>{node.name}</strong><small className="muted">{protocolLabel(node.type)} · {node.enabled ? "已启用" : "已停用"}</small></td>
          <td data-label="部署方式">{node.machine_name ?? "独立部署"}</td>
          <td data-label="地址"><code>{node.host}:{node.port}</code></td>
          <td data-label="在线人数">{node.online_count}</td>
          <td data-label="倍率">{formatRateMultiplier(node.rate)}</td>
          <td data-label="权限组"><span className="node-groups">{node.group_ids.length === 0 ? "全部" : node.group_ids.map((id) => groupNames.get(id) ?? `#${id}`).join("、")}</span></td>
          <td data-label="流量使用">{formatBytes(node.traffic_upload + node.traffic_download)}<small className="muted">↑ {formatBytes(node.traffic_upload)} · ↓ {formatBytes(node.traffic_download)}</small></td>
          <td data-label="操作"><div className="row-actions node-row-actions">
            <button className="button compact secondary" disabled={busy} aria-label={`编辑节点：${node.name}`} onClick={() => setEditing(node)}>编辑</button>
            <button className="button compact secondary" disabled={busy} aria-label={`复制节点：${node.name}`} onClick={() => void run(() => api.copyAdminNode(node.id, node.revision))}>复制</button>
            <button className="button compact ghost" disabled={busy || index === 0} aria-label={`上移节点：${node.name}`} onClick={() => moveNode(node, -1)}>↑</button>
            <button className="button compact ghost" disabled={busy || index === nodes.length - 1} aria-label={`下移节点：${node.name}`} onClick={() => moveNode(node, 1)}>↓</button>
            <button className="button compact ghost" disabled={busy} aria-label={`重置流量：${node.name}`} onClick={() => setConfirming({ kind: "reset", targets: [nodeRevision(node)] })}>重置</button>
            <button className="button compact ghost danger-text" disabled={busy} aria-label={`删除节点：${node.name}`} onClick={() => setConfirming({ kind: "delete", targets: [nodeRevision(node)] })}>删除</button>
          </div></td>
        </tr>)}</tbody>
      </table>
      <footer className="pagination-footer"><button className="button compact ghost" disabled={page <= 1 || loading} onClick={() => { setLoading(true); setError(""); setPage((current) => current - 1); }}>上一页</button><span>第 {page} / {pageCount} 页 · 共 {total} 个节点</span><button className="button compact ghost" disabled={page >= pageCount || loading} onClick={() => { setLoading(true); setError(""); setPage((current) => current + 1); }}>下一页</button></footer>
    </section>}
    {editing !== null && <EditNodeModal api={api} node={editing} machines={machines} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); refresh(); }} />}
    {confirming !== null && <ConfirmNodeMutation api={api} kind={confirming.kind} targets={confirming.targets} onClose={() => setConfirming(null)} onDone={() => { setConfirming(null); refresh(); }} />}
  </main>;
}

function Overview({ label, value }: { label: string; value: number }) {
  return <article className="overview-metric"><span>{label}</span><strong>{value}</strong></article>;
}

function EditNodeModal({ api, node, machines, onClose, onSaved }: { api: NodeManagementAPI; node: AdminNode; machines: Machine[]; onClose: () => void; onSaved: () => void }) {
  const [input, setInput] = useState<AdminNodeUpdateInput>({
    revision: node.revision, name: node.name, host: node.host, port: node.port, show: node.show,
    enabled: node.enabled, sort: node.sort, machine_id: node.machine_id
  });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setSaving(true); setError("");
    try { await api.updateAdminNode(node.id, input); onSaved(); }
    catch (cause) { setError(errorMessage(cause)); setSaving(false); }
  };
  return <Modal title="编辑节点" onClose={onClose}><div className="modal-header"><div><p className="eyebrow">{protocolLabel(node.type)}</p><h2>编辑节点</h2></div><button className="icon-button" aria-label="关闭编辑节点" onClick={onClose}>×</button></div>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>节点名称<input required maxLength={255} value={input.name} onChange={(event) => setInput({ ...input, name: event.target.value })} /></label>
      <label>节点地址<input required maxLength={255} value={input.host} onChange={(event) => setInput({ ...input, host: event.target.value })} /></label>
      <label>连接端口<input required inputMode="numeric" pattern="[0-9]{1,5}(-[0-9]{1,5})?" value={input.port} onChange={(event) => setInput({ ...input, port: event.target.value })} /></label>
      <label>排序<input required type="number" min="0" max="1000000000" value={input.sort} onChange={(event) => setInput({ ...input, sort: Number(event.target.value) })} /></label>
      <label>绑定服务器<select value={input.machine_id ?? ""} onChange={(event) => setInput({ ...input, machine_id: event.target.value === "" ? null : Number(event.target.value) })}><option value="">独立部署</option>{machines.map((machine) => <option key={machine.id} value={machine.id}>{machine.name}</option>)}</select></label>
      <label className="switch-label"><input type="checkbox" checked={input.show} onChange={(event) => setInput({ ...input, show: event.target.checked })} />用户端显示</label>
      <label className="switch-label"><input type="checkbox" checked={input.enabled} onChange={(event) => setInput({ ...input, enabled: event.target.checked })} />启用运行</label>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存修改"}</button></div>
    </form>
  </Modal>;
}

function ConfirmNodeMutation({ api, kind, targets, onClose, onDone }: { api: NodeManagementAPI; kind: "reset" | "delete"; targets: AdminNodeRevision[]; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const title = kind === "delete" ? "删除节点" : "重置节点流量";
  const submit = async () => {
    setBusy(true); setError("");
    try {
      if (kind === "delete") await api.deleteAdminNodes(targets);
      else await api.resetAdminNodeTraffic(targets);
      onDone();
    } catch (cause) { setError(errorMessage(cause)); setBusy(false); }
  };
  return <Modal title={title} role="alertdialog" onClose={onClose}><div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>
    <p>{kind === "delete" ? `确定删除选中的 ${targets.length} 个节点吗？该操作不会删除历史审计记录。` : `确定将选中的 ${targets.length} 个节点当前累计流量归零吗？历史统计会保留。`}</p>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" disabled={busy} onClick={onClose}>取消</button><button className={`button primary${kind === "delete" ? " destructive" : ""}`} disabled={busy} onClick={() => void submit()}>{busy ? "正在处理…" : kind === "delete" ? "确认删除" : "确认重置"}</button></div>
  </Modal>;
}

function nodeRevision(node: AdminNode): AdminNodeRevision { return { id: node.id, revision: node.revision }; }
function protocolLabel(type: string): string { return protocols.find(([value]) => value === type)?.[1] ?? type; }
function formatRateMultiplier(value: number): string { return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(2).replace(/0+$/, "")}×`; }
function formatBytes(value: number): string {
  if (value >= 1024 ** 4) return `${(value / 1024 ** 4).toFixed(2)} TiB`;
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GiB`;
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(2)} MiB`;
  if (value >= 1024) return `${(value / 1024).toFixed(2)} KiB`;
  return `${value} B`;
}
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : "请求失败"; }
