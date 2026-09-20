import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { Modal } from "../../components/Overlay";
import type {
  AdminAPI, AdminNode, AdminNodeQuery, AdminNodeRevision, AdminNodeStateInput, Machine, RoutingRule, ServerGroup
} from "../../lib/api";
import { NodeDefinitionModal } from "./NodeDefinitionModal";
import "./node-management.css";
import { NodeFilter } from "./NodeFilter";

type NodeSortBy = "id" | "online_count";
type NodeSortOrder = "asc" | "desc";
type NodeListQuery = Omit<AdminNodeQuery, "page" | "page_size"> & {
  group_id?: number;
  sort_by?: NodeSortBy;
  sort_order?: NodeSortOrder;
};

type ListNode = AdminNode & {
  parent_id?: number | null;
  server_port?: number;
  transfer_enable?: number;
  external_code?: string;
  tags?: string[];
};

type NodeManagementAPI = Pick<AdminAPI,
  "listAdminNodes" | "listAdminNodeParentOptions" | "listMachines" | "listServerGroups" | "listRoutingRules" | "getAdminNodeDefinition" |
  "createAdminNodeDefinition" | "replaceAdminNodeDefinition" | "copyAdminNode" | "reorderAdminNodes" |
  "updateAdminNodeStates" | "resetAdminNodeTraffic" | "deleteAdminNodes" | "createServerGroup" | "generateNodeECH"
>;

interface Props {
  api: NodeManagementAPI;
  initialMachineID?: number;
  initiallyCreating?: boolean;
}

const pageSizeOptions = [10, 20, 50, 100, 500] as const;
const defaultPageSize = 500;
const searchDebounceMs = 300;
const machineOnlineWindowMs = 5 * 60 * 1000;
const protocols = [
  ["shadowsocks", "Shadowsocks"], ["vmess", "VMess"], ["trojan", "Trojan"], ["hysteria", "Hysteria"],
  ["vless", "VLess"], ["tuic", "TUIC"], ["socks", "SOCKS"], ["naive", "Naive"], ["http", "HTTP"],
  ["mieru", "Mieru"], ["anytls", "AnyTLS"]
] as const;

type MenuState =
  | null
  | { kind: "bulk" }
  | { kind: "bulk-bind" }
  | { kind: "row"; id: number }
  | { kind: "row-bind"; id: number };

export function NodeManagementPage({ api, initialMachineID, initiallyCreating = false }: Props) {
  const [nodes, setNodes] = useState<ListNode[]>([]);
  const [machines, setMachines] = useState<Machine[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [routes, setRoutes] = useState<RoutingRule[]>([]);
  const [total, setTotal] = useState(0);
  const [page, updatePage] = useState(1);
  const [pageSize, setPageSize] = useState(defaultPageSize);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [catalogError, setCatalogError] = useState("");
  const [selected, setSelected] = useState<number[]>([]);
  const [editing, setEditing] = useState<AdminNode | "create" | null>(initiallyCreating ? "create" : null);
  const [confirming, setConfirming] = useState<{ kind: "reset" | "delete"; targets: AdminNodeRevision[] } | null>(null);
  const [reloadToken, setReloadToken] = useState(0);
  const [queryInput, setQueryInput] = useState("");
  const [filters, setFilters] = useState<NodeListQuery>(initialMachineID === undefined ? {} : { machine_id: initialMachineID });
  const [menu, setMenu] = useState<MenuState>(null);
  const [sorting, setSorting] = useState(false);
  const [draft, setDraft] = useState<ListNode[] | null>(null);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [dropIndex, setDropIndex] = useState<number | null>(null);
  const [copiedID, setCopiedID] = useState<number | null>(null);
  const [observedAt, setObservedAt] = useState(() => Date.now());
  const [pageInput, setPageInput] = useState("1");
  const setPage = (value: number | ((page: number) => number)) => {
    const next = typeof value === "function" ? value(page) : value;
    if (next !== page) setLoading(true);
    updatePage(next); setPageInput(String(next)); setSorting(false); setDraft(null); setMenu(null);
  };
  const requestVersion = useRef(0);
  const pendingMutations = useRef(0);
  const copiedTimer = useRef<number>(0);

  useEffect(() => {
    let live = true;
    void Promise.allSettled([api.listMachines(), api.listServerGroups(), api.listRoutingRules()]).then(([machineResult, groupResult, routeResult]) => {
      if (!live) return;
      const failures: string[] = [];
      if (machineResult.status === "fulfilled") setMachines(machineResult.value);
      else failures.push(`服务器列表加载失败：${errorMessage(machineResult.reason)}`);
      if (groupResult.status === "fulfilled") setGroups(groupResult.value);
      else failures.push(`权限组加载失败：${errorMessage(groupResult.reason)}`);
      if (routeResult.status === "fulfilled") setRoutes(routeResult.value);
      else failures.push(`路由规则加载失败：${errorMessage(routeResult.reason)}`);
      setCatalogError(failures.join(" "));
    });
    return () => { live = false; };
  }, [api, reloadToken]);

  useEffect(() => {
    const nextQuery = queryInput.trim();
    if ((filters.q ?? "") === nextQuery) return;
    const timer = window.setTimeout(() => {
      updatePage(1); setPageInput("1"); setLoading(true); setError(""); setSorting(false); setDraft(null); setMenu(null);
      setFilters(current => ({...current,q:nextQuery || undefined}));
    }, searchDebounceMs);
    return () => window.clearTimeout(timer);
  }, [queryInput, filters.q]);

  useEffect(() => {
    let live = true;
    const version = ++requestVersion.current;
    const query = { page, page_size: pageSize, ...filters };
    void api.listAdminNodes(query).then((result) => {
      if (!live || version !== requestVersion.current) return;
      if (result.items.length === 0 && result.total > 0 && page > 1) {
        updatePage(Math.max(1, Math.ceil(result.total / pageSize)));
        setPageInput(String(Math.max(1, Math.ceil(result.total / pageSize))));
        return;
      }
      setNodes(result.items);
      setTotal(result.total);
      setSelected([]);
      setMenu(null);
    }).catch((cause: unknown) => {
      if (live && version === requestVersion.current) setError(errorMessage(cause));
    }).finally(() => {
      if (live && version === requestVersion.current) {
        setLoading(false);
        if (pendingMutations.current === 0) setBusy(false);
      }
    });
    return () => { live = false; };
  }, [api, filters, page, pageSize, reloadToken]);


  useEffect(() => {
    const timer = window.setInterval(() => setObservedAt(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);


  useEffect(() => () => { window.clearTimeout(copiedTimer.current); }, []);

  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);
  const machineByID = useMemo(() => new Map(machines.map((machine) => [machine.id, machine])), [machines]);
  const rows = sorting && draft !== null ? draft : nodes;
  const selectedTargets = useMemo(
    () => rows.filter((node) => selected.includes(node.id)).map(nodeRevision),
    [rows, selected]
  );
  const pageCount = Math.max(1, Math.ceil(Math.max(total, 0) / pageSize));
  const pending = loading || queryInput.trim() !== (filters.q ?? "");
  const canReorder = filters.sort_by === undefined && nodes.length > 1 && !busy && !pending;
  const allSelected = rows.length > 0 && rows.every((node) => selected.includes(node.id));
  const someSelected = rows.some((node) => selected.includes(node.id));

  const refresh = useCallback(() => {
    setError("");
    setLoading(true);
    setReloadToken((current) => current + 1);
  }, []);
  const closeMenu = useCallback(() => setMenu(null), []);
  const patchFilters = (patch: (current: NodeListQuery) => NodeListQuery) => {
    setSorting(false);
    setDraft(null);
    setPage(1);
    setLoading(true); setError("");
    setFilters(patch);
  };

  const applyPage = (result: { items: AdminNode[]; total: number }, preserveSelection: boolean) => {
    if (result.items.length === 0 && result.total > 0 && page > 1) {
      updatePage(Math.max(1, Math.ceil(result.total / pageSize)));
        setPageInput(String(Math.max(1, Math.ceil(result.total / pageSize))));
      return;
    }
    setNodes(result.items);
    setTotal(result.total);
    if (preserveSelection) {
      const visible = new Set(result.items.map((item) => item.id));
      setSelected((current) => current.filter((id) => visible.has(id)));
    } else {
      setSelected([]);
    }
  };
  const run = async (operation: () => Promise<unknown>, preserveSelection = true) => {
    pendingMutations.current += 1;
    setBusy(true);
    setError("");
    setMenu(null);
    try {
      await operation();
      const version = ++requestVersion.current;
      const result = await api.listAdminNodes({ page, page_size: pageSize, ...filters });
      if (version === requestVersion.current) applyPage(result, preserveSelection);
      return true;
    } catch (cause) {
      setError(errorMessage(cause));
      return false;
    } finally {
      pendingMutations.current = Math.max(0, pendingMutations.current - 1);
      if (pendingMutations.current === 0) setBusy(false);
      setLoading(false);
    }
  };
  const updateState = (targets: AdminNodeRevision[], input: Omit<AdminNodeStateInput, "targets">) => {
    if (targets.length === 0) return;
    void run(() => api.updateAdminNodeStates({ targets, ...input }));
  };
  const startSort = () => {
    if (!canReorder) return;
    setDraft([...nodes]);
    setSorting(true);
    setMenu(null);
  };
  const cancelSort = () => {
    setSorting(false);
    setDraft(null);
    setDragIndex(null);
    setDropIndex(null);
  };
  const moveDraft = (index: number, target: number) => {
    if (draft === null || target < 0 || target >= draft.length || target === index) return;
    setDraft((current) => {
      if (current === null) return current;
      const next = [...current];
      const [moved] = next.splice(index, 1);
      if (moved === undefined) return current;
      next.splice(target, 0, moved);
      return next;
    });
  };
  const saveSort = async () => {
    if (draft === null) return;
    if (!canReorder && !sorting) return;
    if (filters.sort_by !== undefined) {
      setError("请先取消列排序再调整顺序。");
      return;
    }
    const unchanged = draft.length === nodes.length && draft.every((node, index) => node.id === nodes[index]?.id);
    if (unchanged) {
      cancelSort();
      return;
    }
    const saved = await run(() => api.reorderAdminNodes(draft.map(nodeRevision)), false);
    if (saved) cancelSort();
  };

  const toggleSelected = (id: number, checked: boolean) => {
    setSelected((current) => checked ? (current.includes(id) ? current : [...current, id]) : current.filter((item) => item !== id));
  };
  const toggleAll = (checked: boolean) => {
    setSelected(checked ? rows.map((node) => node.id) : []);
  };
  const toggleSort = (field: NodeSortBy) => {
    patchFilters((current) => {
      const next = { ...current };
      if (current.sort_by !== field) {
        next.sort_by = field;
        next.sort_order = "asc";
        return next;
      }
      if (current.sort_order !== "desc") {
        next.sort_order = "desc";
        return next;
      }
      delete next.sort_by;
      delete next.sort_order;
      return next;
    });
  };
  const copyAddress = async (node: ListNode) => {
    const address = `${node.host}:${node.port}`;
    try {
      if (navigator.clipboard?.writeText !== undefined) await navigator.clipboard.writeText(address);
      else {
        const field = document.createElement("textarea");
        field.value = address;
        field.setAttribute("readonly", "");
        field.style.position = "fixed";
        field.style.left = "-9999px";
        document.body.append(field);
        field.select();
        const copied = document.execCommand("copy");
        field.remove();
        if (!copied) throw new Error("复制失败，请手动复制地址。");
      }
      setCopiedID(node.id);
      window.clearTimeout(copiedTimer.current);
      copiedTimer.current = window.setTimeout(() => setCopiedID((current) => current === node.id ? null : current), 1600);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  };
  const commitPageInput = () => {
    const next = Math.min(pageCount, Math.max(1, Math.trunc(Number(pageInput)) || 1));
    setPageInput(String(next));
    if (next !== page) setPage(next);
  };

  const sortDisabledReason = filters.sort_by !== undefined ? "请先取消列排序" : nodes.length < 2 ? "至少需要两个节点" : "";

  return <main className="page-shell node-management-page">
    <header className="page-header">
      <div>
        <h1>节点管理</h1>
        <p className="muted">管理所有节点，包括添加、删除、编辑等操作。</p>
      </div>
    </header>

    <div className="node-toolbar" aria-label="节点筛选">
      <button className="button primary compact" type="button" disabled={busy || pending || sorting} onClick={() => setEditing("create")}>添加节点</button>
      <div className="node-toolbar-search">
        <input aria-label="搜索节点" type="search" placeholder="搜索节点..." value={queryInput} disabled={sorting || busy} onChange={(event) => setQueryInput(event.target.value)} />
      </div>
      <NodeFilter label="类型" options={protocols.map(([value,label])=>({value,label}))} value={filters.types ?? []} disabled={sorting || busy} onChange={types=>patchFilters(current=>({...current,types:types.length ? types : undefined}))}/>
      <NodeFilter label="服务器" options={[{value:"unassigned",label:"独立部署"},...machines.map(machine=>({value:String(machine.id),label:machine.name}))]} value={[...(filters.machine_ids ?? (filters.machine_id ? [filters.machine_id] : [])).map(String),...(filters.unassigned ? ["unassigned"]:[])]} disabled={sorting || busy} onChange={values=>patchFilters(current=>({...current,machine_id:undefined,machine_ids:values.filter(value=>value!=="unassigned").map(Number),unassigned:values.includes("unassigned") || undefined}))}/>
      <NodeFilter label="权限组" options={groups.map(group=>({value:String(group.id),label:group.name}))} value={(filters.group_ids ?? []).map(String)} disabled={sorting || busy} onChange={values=>patchFilters(current=>({...current,group_ids:values.length ? values.map(Number):undefined}))}/>
      <ActionMenu
        open={menu?.kind === "bulk" || menu?.kind === "bulk-bind"}
        label="操作"
        ariaLabel="批量操作"
        disabled={busy || pending || selectedTargets.length === 0}
        onToggle={() => setMenu((current) => current?.kind === "bulk" || current?.kind === "bulk-bind" ? null : { kind: "bulk" })}
        onClose={closeMenu}
      >
        {menu?.kind === "bulk-bind" ? <>
          <div className="node-menu-heading">绑定服务器</div>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { machine_id: null }); }}>解除绑定</button>
          {machines.map((machine) => <button type="button" role="menuitem" key={machine.id} disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { machine_id: machine.id }); }}>{machine.name}</button>)}
          <div className="node-menu-separator" />
          <button type="button" role="menuitem" onClick={() => setMenu({ kind: "bulk" })}>返回</button>
        </> : <>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { show: true }); }}>显示节点</button>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { show: false }); }}>隐藏节点</button>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { enabled: true }); }}>启用节点</button>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState(selectedTargets, { enabled: false }); }}>禁用节点</button>
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); setConfirming({ kind: "reset", targets: selectedTargets }); }}>重置流量</button>
          <button type="button" role="menuitem" className="danger-text" disabled={busy || pending} onClick={() => { closeMenu(); setConfirming({ kind: "delete", targets: selectedTargets }); }}>删除</button>
          <div className="node-menu-separator" />
          <button type="button" role="menuitem" disabled={busy || pending} onClick={() => setMenu({ kind: "bulk-bind" })}>服务器绑定</button>
        </>}
      </ActionMenu>
      <div className="node-toolbar-spacer" />
      {sorting ? <>
        <button className="button primary compact" type="button" disabled={busy || pending} onClick={() => void saveSort()}>{busy ? "正在保存排序…" : "保存排序"}</button>
        <button className="button ghost compact" type="button" disabled={busy || pending} onClick={cancelSort}>取消</button>
      </> : <button
        className="button secondary compact"
        type="button"
        disabled={!canReorder}
        title={sortDisabledReason}
        onClick={startSort}
      >编辑排序</button>}
    </div>

    {sorting && <p className="node-sort-hint">拖拽或使用上移/下移调整顺序，确认后一次性保存。</p>}
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" type="button" onClick={() => { setError(""); refresh(); }}>重试</button></div>}
    {catalogError !== "" && <div className="alert warning resource-alert" role="alert">{catalogError}<button className="button ghost compact" type="button" onClick={() => { setCatalogError(""); refresh(); }}>重试</button></div>}

    {loading && nodes.length === 0 ? <div className="empty-card" aria-live="polite">正在加载节点…</div> : <section className="node-table-wrap">
      <table className="node-table" aria-label="节点列表" aria-busy={loading}>
        <thead>
          <tr>
            <th scope="col">
              <input
                className="node-check"
                type="checkbox"
                aria-label="选择全部"
                checked={allSelected}
                ref={(element) => { if (element !== null) element.indeterminate = someSelected && !allSelected; }}
                disabled={busy || pending || rows.length === 0}
                onChange={(event) => toggleAll(event.target.checked)}
              />
            </th>
            <SortableHeader label="节点ID" field="id" current={filters.sort_by} order={filters.sort_order} disabled={sorting || busy} onSort={toggleSort} />
            <th scope="col">显隐</th>
            <th scope="col">节点</th>
            <th scope="col">部署方式</th>
            <th scope="col">地址</th>
            <SortableHeader label="在线人数" field="online_count" current={filters.sort_by} order={filters.sort_order} disabled={sorting || busy} onSort={toggleSort} />
            <th scope="col">倍率</th>
            <th scope="col">权限组</th>
            <th scope="col">流量使用</th>
            <th scope="col">操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? <tr className="empty-table-row"><td colSpan={11}>{loading ? "正在加载节点…" : "没有符合条件的节点。"}</td></tr> : rows.map((node, index) => {
            const parentID = nodeParentID(node);
            const serverPort = nodeServerPort(node);
            const quota = nodeTransferEnable(node);
            const machine = node.machine_id === null ? undefined : machineByID.get(node.machine_id);
            const status = machine === undefined ? null : machineStatus(machine, observedAt);
            const used = node.traffic_upload + node.traffic_download;
            return <tr
              key={node.id}
              className={`admin-node-table-row${node.enabled ? "" : " is-disabled"}${dragIndex === index ? " is-dragging" : ""}${dropIndex === index ? " is-drop-target" : ""}`}
              draggable={sorting && !busy}
              onDragStart={(event) => {
                if (!sorting) return;
                event.dataTransfer.effectAllowed = "move";
                event.dataTransfer.setData("text/plain", String(index));
                setDragIndex(index);
              }}
              onDragOver={(event) => {
                if (!sorting || dragIndex === null) return;
                event.preventDefault();
                setDropIndex(index);
              }}
              onDrop={(event) => {
                event.preventDefault();
                if (dragIndex === null) return;
                moveDraft(dragIndex, index);
                setDragIndex(null);
                setDropIndex(null);
              }}
              onDragEnd={() => { setDragIndex(null); setDropIndex(null); }}
            >
              <td data-label="选择">
                <input
                  className="node-check"
                  type="checkbox"
                  aria-label={`选择节点：${node.name}`}
                  checked={selected.includes(node.id)}
                  disabled={busy || pending}
                  onChange={(event) => toggleSelected(node.id, event.target.checked)}
                />
              </td>
              <td data-label="节点ID">
                <span className="node-id-cell">
                  {sorting && <button type="button" className="node-drag-handle" aria-label={`拖拽节点：${node.name}`} disabled={busy || pending}>⠿</button>}
                  {parentID !== undefined && parentID !== null && <span className="node-parent-arrow" title={`父节点 #${parentID}`} aria-label={`父节点 #${parentID}`}>{node.id} → </span>}
                  <strong>{parentID ?? node.id}</strong>
                </span>
              </td>
              <td data-label="显隐">
                <button
                  type="button"
                  className="node-switch"
                  role="switch"
                  aria-checked={node.show}
                  aria-label={`${node.show ? "隐藏" : "显示"}节点：${node.name}`}
                  disabled={busy || pending}
                  onClick={() => updateState([nodeRevision(node)], { show: !node.show })}
                ><span /></button>
              </td>
              <td data-label="节点">
                <button type="button" className="node-name-button" aria-label={`编辑节点：${node.name}`} disabled={busy || pending} onClick={() => setEditing(node)}>{node.name}</button>
                <div className="node-name-meta">
                  <span className="node-protocol-badge"><ProtocolIcon type={node.type} />{protocolLabel(node.type)}</span>
                  {!node.enabled && <span className="node-tag">已停用</span>}
                  {nodeExternalCode(node) !== undefined && nodeExternalCode(node) !== "" && <span className="node-tag">{nodeExternalCode(node)}</span>}
                  {nodeTags(node).map((tag) => <span className="node-tag" key={tag}>{tag}</span>)}
                </div>
              </td>
              <td data-label="部署方式">
                <div className="node-deploy">
                  <span className="node-deploy-name">
                    {node.machine_id === null ? "独立部署" : machine?.name ?? node.machine_name ?? `#${node.machine_id}`}
                    {status !== null && <span className={`node-status ${status}`}>{status === "online" ? "在线" : status === "inactive" ? "已停用" : "离线"}</span>}
                  </span>
                </div>
              </td>
              <td data-label="地址">
                <div className="node-address">
                  <span className="node-address-row">
                    <code>{node.host}:{node.port}</code>
                    <button type="button" className="node-icon-button" aria-label={`复制地址：${node.name}`} disabled={busy || pending} onClick={() => void copyAddress(node)}>
                      <CopyIcon />
                    </button>
                    {copiedID === node.id && <span className="node-copy-ok">已复制</span>}
                  </span>
                  {serverPort !== undefined && <small className="muted">内部端口 {serverPort}</small>}
                </div>
              </td>
              <td data-label="在线人数">{node.online_count}</td>
              <td data-label="倍率">{formatRateMultiplier(node.rate)}</td>
              <td data-label="权限组">
                <span className="node-groups">
                  {node.group_ids.length === 0
                    ? <span className="muted">全部</span>
                    : node.group_ids.map((id) => <span className="node-group-badge" key={id}>{groupNames.get(id) ?? `#${id}`}</span>)}
                </span>
              </td>
              <td data-label="流量使用">
                <div className="node-traffic">
                  <span>{quota !== undefined && quota > 0 ? `${formatBytes(used)} / ${formatBytes(quota)}` : formatBytes(used)}</span>
                  <small className="muted">↑ {formatBytes(node.traffic_upload)} · ↓ {formatBytes(node.traffic_download)}</small>
                </div>
              </td>
              <td data-label="操作">
                {sorting ? <div className="row-actions node-row-actions">
                  <button className="button compact ghost" type="button" disabled={busy || index === 0} aria-label={`上移节点：${node.name}`} onClick={() => moveDraft(index, index - 1)}>↑</button>
                  <button className="button compact ghost" type="button" disabled={busy || index === rows.length - 1} aria-label={`下移节点：${node.name}`} onClick={() => moveDraft(index, index + 1)}>↓</button>
                </div> : <ActionMenu
                  open={(menu?.kind === "row" && menu.id === node.id) || (menu?.kind === "row-bind" && menu.id === node.id)}
                  label="⋯"
                  ariaLabel={`节点操作：${node.name}`}
                  compact
                  disabled={busy || pending}
                  onToggle={() => setMenu((current) => (current?.kind === "row" && current.id === node.id) || (current?.kind === "row-bind" && current.id === node.id) ? null : { kind: "row", id: node.id })}
                  onClose={closeMenu}
                >
                  {menu?.kind === "row-bind" && menu.id === node.id ? <>
                    <div className="node-menu-heading">绑定服务器</div>
                    <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState([nodeRevision(node)], { machine_id: null }); }}>解除绑定</button>
                    {machines.map((item) => <button type="button" role="menuitem" key={item.id} disabled={busy || pending} onClick={() => { closeMenu(); updateState([nodeRevision(node)], { machine_id: item.id }); }}>{item.name}</button>)}
                    <div className="node-menu-separator" />
                    <button type="button" role="menuitem" onClick={() => setMenu({ kind: "row", id: node.id })}>返回</button>
                  </> : <>
                    <button type="button" role="menuitem" aria-label={`编辑节点：${node.name}`} disabled={busy || pending} onClick={() => { closeMenu(); setEditing(node); }}>编辑</button>
                    <button type="button" role="menuitem" aria-label={`复制节点：${node.name}`} disabled={busy || pending} onClick={() => { closeMenu(); void run(() => api.copyAdminNode(node.id, node.revision), false); }}>复制</button>
                    <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState([nodeRevision(node)], { show: !node.show }); }}>{node.show ? "隐藏" : "显示"}</button>
                    <button type="button" role="menuitem" disabled={busy || pending} onClick={() => { closeMenu(); updateState([nodeRevision(node)], { enabled: !node.enabled }); }}>{node.enabled ? "停用" : "启用"}</button>
                    <button type="button" role="menuitem" disabled={busy || pending} onClick={() => setMenu({ kind: "row-bind", id: node.id })}>服务器绑定</button>
                    <button type="button" role="menuitem" aria-label={`重置流量：${node.name}`} disabled={busy || pending} onClick={() => { closeMenu(); setConfirming({ kind: "reset", targets: [nodeRevision(node)] }); }}>重置流量</button>
                    <button type="button" role="menuitem" className="danger-text" aria-label={`删除节点：${node.name}`} disabled={busy || pending} onClick={() => { closeMenu(); setConfirming({ kind: "delete", targets: [nodeRevision(node)] }); }}>删除</button>
                  </>}
                </ActionMenu>}
              </td>
            </tr>;
          })}
        </tbody>
      </table>
      <footer className="node-pagination">
        <span>已选择 {selected.length} 项，共 {total} 项</span>
        <div className="node-pagination-controls">
          <label>每页显示
            <select aria-label="每页显示" value={pageSize} disabled={pending || busy || sorting} onChange={(event) => { setLoading(true); setPage(1); setPageSize(Number(event.target.value)); }}>
              {pageSizeOptions.map((size) => <option key={size} value={size}>{size}</option>)}
            </select>
          </label>
          <button type="button" aria-label="跳转到第一页" disabled={page <= 1 || pending || busy || sorting} onClick={() => setPage(1)}>«</button>
          <button type="button" aria-label="上一页" disabled={page <= 1 || pending || busy || sorting} onClick={() => setPage((current) => Math.max(1, current - 1))}>‹</button>
          <input
            aria-label="页码"
            type="number"
            min={1}
            max={pageCount}
            value={pageInput}
            disabled={pending || busy || sorting}
            onChange={(event) => setPageInput(event.target.value)}
            onBlur={commitPageInput}
            onKeyDown={(event) => { if (event.key === "Enter") commitPageInput(); }}
          />
          <button type="button" aria-label="下一页" disabled={page >= pageCount || pending || busy || sorting} onClick={() => setPage((current) => Math.min(pageCount, current + 1))}>›</button>
          <button type="button" aria-label="跳转到最后一页" disabled={page >= pageCount || pending || busy || sorting} onClick={() => setPage(pageCount)}>»</button>
        </div>
      </footer>
    </section>}

    {editing !== null && <NodeDefinitionModal initialMachineID={initialMachineID} api={api} node={editing === "create" ? null : editing} machines={machines} groups={groups} routes={routes} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void run(() => Promise.resolve(), false); }} />}
    {confirming !== null && <ConfirmNodeMutation api={api} kind={confirming.kind} targets={confirming.targets} onClose={() => setConfirming(null)} onDone={() => { setConfirming(null); void run(() => Promise.resolve(), false); }} />}
  </main>;
}

function SortableHeader({ label, field, current, order, disabled, onSort }: {
  label: string;
  field: NodeSortBy;
  current?: NodeSortBy;
  order?: NodeSortOrder;
  disabled?: boolean;
  onSort: (field: NodeSortBy) => void;
}) {
  const active = current === field;
  return <th scope="col" aria-sort={active ? (order === "desc" ? "descending" : "ascending") : "none"}>
    <button type="button" className="table-sort-button" disabled={disabled} onClick={() => onSort(field)}>
      {label}{active ? (order === "desc" ? " ↓" : " ↑") : ""}
    </button>
  </th>;
}

function ActionMenu({ open, label, ariaLabel, disabled, compact = false, onToggle, onClose, children }: {
  open: boolean;
  label: string;
  ariaLabel: string;
  disabled?: boolean;
  compact?: boolean;
  onToggle: () => void;
  onClose: () => void;
  children: ReactNode;
}) {
  const rootRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      onClose();
    };
    const onPointer = (event: PointerEvent) => {
      if (rootRef.current !== null && !rootRef.current.contains(event.target as Node)) onClose();
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [open, onClose]);
  return <div className="node-menu-wrap" ref={rootRef}>
    <button
      className={`button ${compact ? "ghost" : "secondary"} compact`}
      type="button"
      aria-haspopup="menu"
      aria-expanded={open}
      aria-label={ariaLabel}
      disabled={disabled}
      onClick={onToggle}
    >{label}</button>
    {open && <div className="node-menu" role="menu" aria-label={ariaLabel}>{children}</div>}
  </div>;
}

function ConfirmNodeMutation({ api, kind, targets, onClose, onDone }: {
  api: NodeManagementAPI;
  kind: "reset" | "delete";
  targets: AdminNodeRevision[];
  onClose: () => void;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const handleClose = () => { if (!busy) onClose(); };
  const title = kind === "delete" ? "删除节点" : "重置节点流量";
  const submit = async () => {
    setBusy(true);
    setError("");
    try {
      if (kind === "delete") await api.deleteAdminNodes(targets);
      else await api.resetAdminNodeTraffic(targets);
      onDone();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  };
  return <Modal title={title} role="alertdialog" onClose={handleClose}>
    <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={handleClose}>×</button></div>
    <p>{kind === "delete" ? `确定删除选中的 ${targets.length} 个节点吗？该操作不会删除历史审计记录。` : `确定将选中的 ${targets.length} 个节点当前累计流量归零吗？历史统计会保留。`}</p>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions">
      <button className="button ghost" type="button" disabled={busy} onClick={handleClose}>取消</button>
      <button className={`button primary${kind === "delete" ? " destructive" : ""}`} type="button" disabled={busy} onClick={() => void submit()}>
        {busy ? "正在处理…" : kind === "delete" ? "确认删除" : "确认重置"}
      </button>
    </div>
  </Modal>;
}

function ProtocolIcon({ type }: { type: string }) {
  return <svg aria-hidden="true" width="10" height="10" viewBox="0 0 12 12" fill="currentColor">
    {type === "shadowsocks" ? <circle cx="6" cy="6" r="5" /> :
      type === "vmess" ? <rect x="1" y="1" width="10" height="10" rx="2" /> :
        type === "trojan" ? <polygon points="6,1 11,10 1,10" /> :
          type === "hysteria" ? <polygon points="6,1 11,6 6,11 1,6" /> :
            type === "vless" ? <circle cx="6" cy="6" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.6" /> :
              <rect x="2" y="2" width="8" height="8" rx="1" />}
  </svg>;
}

function CopyIcon() {
  return <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <rect x="9" y="9" width="11" height="11" rx="2" />
    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
  </svg>;
}

function machineStatus(machine: Machine, observedAt: number): "online" | "offline" | "inactive" {
  if (!machine.is_active) return "inactive";
  return machine.last_seen_at !== null && observedAt - new Date(machine.last_seen_at).getTime() <= machineOnlineWindowMs ? "online" : "offline";
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
function nodeParentID(node: ListNode): number | null | undefined {
  return "parent_id" in node ? node.parent_id : undefined;
}
function nodeServerPort(node: ListNode): number | undefined {
  return typeof node.server_port === "number" && Number.isFinite(node.server_port) ? node.server_port : undefined;
}
function nodeTransferEnable(node: ListNode): number | undefined {
  return typeof node.transfer_enable === "number" && Number.isFinite(node.transfer_enable) ? node.transfer_enable : undefined;
}
function nodeExternalCode(node: ListNode): string | undefined {
  return typeof node.external_code === "string" ? node.external_code : undefined;
}
function nodeTags(node: ListNode): string[] {
  return Array.isArray(node.tags) ? node.tags.filter((tag) => tag.trim() !== "") : [];
}
