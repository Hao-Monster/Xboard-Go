import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import { Drawer, Modal } from "../../components/Overlay";
import type { ActivationSchedule, AdminAPI, DailyScheduleInput, LoadHistory, Machine, MachineEnrollment, Node } from "../../lib/api";

import "./ServerManagementPage.css";
import { MachineTokenSection } from "./MachineTokenSection";

interface Props {
  api: AdminAPI;
  onNavigateNodes?: (id: number, create: boolean) => void;
}

export function ServerManagementPage({ api, onNavigateNodes }: Props) {
  const [machines, setMachines] = useState<Machine[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [detailMachine, setDetailMachine] = useState<Machine | null>(null);
  const [creating, setCreating] = useState(false);
  const [createdEnrollment, setCreatedEnrollment] = useState<MachineEnrollment | null>(null);
  const [observedAt, setObservedAt] = useState(() => Date.now());
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const [linkedFilter, setLinkedFilter] = useState("all");

  const [sortBy, setSortBy] = useState("id");
  const [descending, setDescending] = useState(false);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [editTarget, setEditTarget] = useState<Machine | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Machine | null>(null);
  const sort = (key: string) => { setDescending(sortBy === key ? !descending : false); setSortBy(key); setPage(1); };

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setMachines(await api.listMachines());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let live = true;
    void api.listMachines().then((result) => {
      if (live) setMachines(result);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  useEffect(() => {
    const timer = window.setInterval(() => setObservedAt(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const normalizedQuery = query.trim().toLocaleLowerCase();
  const filteredMachines = machines.filter((machine) => {
    const matchesQuery = normalizedQuery === "" || machine.name.toLocaleLowerCase().includes(normalizedQuery) ||
      machine.notes.toLocaleLowerCase().includes(normalizedQuery) || String(machine.id).includes(normalizedQuery);
    const status = machineStatus(machine, observedAt);
    const matchesStatus = statusFilter === "all" || statusFilter === status;
    const matchesLinked = linkedFilter === "all" || (linkedFilter === "yes" ? machine.servers_count > 0 : machine.servers_count === 0);

    return matchesQuery && matchesStatus && matchesLinked;
  }).sort((left, right) => {
    const direction = descending ? -1 : 1;
    if (sortBy === "name") return direction * left.name.localeCompare(right.name, "zh-CN");
    if (sortBy === "status") return direction * machineStatus(left, observedAt).localeCompare(machineStatus(right, observedAt));
    if (sortBy === "heartbeat") return direction * (Date.parse(left.last_seen_at ?? "") || 0) - direction * (Date.parse(right.last_seen_at ?? "") || 0);
    if (sortBy === "load") return machineLoad(right) - machineLoad(left);
    return left.id - right.id;
  });
  const onlineCount = machines.filter((machine) => machineStatus(machine, observedAt) === "online").length;

  const highLoadCount = machines.filter(isHighLoad).length;
  const nodeCount = machines.reduce((total, machine) => total + machine.servers_count, 0);

  const pageCount = Math.max(1, Math.ceil(filteredMachines.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  return (
    <main className="page-shell machine-page">
      <header className="page-header"><div><h1>服务器管理</h1><p className="muted">用于查看服务器健康、负载与承载节点，并从运维视角快捷发起节点操作。</p></div></header>
      <section className="machine-overview" aria-label="服务器概览">
        <OverviewMetric label="服务器总数" value={machines.length} />
        <OverviewMetric label="在线服务器" value={onlineCount} tone="good" />
        <OverviewMetric label="离线/失联" value={machines.length - onlineCount} tone="warning" />
        <OverviewMetric label="高负载" value={highLoadCount} />
        <OverviewMetric label="节点数" value={nodeCount} />
      </section>
      <section className="machine-toolbar" aria-label="服务器筛选">
        <button className="button secondary compact" onClick={() => setCreating(true)}>＋ 添加服务器</button>
        <input type="search" aria-label="搜索" placeholder="搜索服务器名称、备注或 SID..." value={query} onChange={e => { setQuery(e.target.value); setPage(1); }} />
        <select aria-label="状态" value={statusFilter} onChange={e => { setStatusFilter(e.target.value); setPage(1); }}><option value="all">⊕ 状态</option><option value="online">在线</option><option value="offline">离线</option><option value="inactive">已停用</option></select>
        <select aria-label="节点" value={linkedFilter} onChange={e => { setLinkedFilter(e.target.value); setPage(1); }}><option value="all">⊕ 节点</option><option value="yes">有节点</option><option value="no">无节点</option></select>
        <div className="machine-summary"><span>在线：{onlineCount}/{machines.length}</span><span>高负载：{highLoadCount}</span></div>
      </section>
      <p className="muted machine-hint">适合集中查看服务器在线情况、承载节点数量与资源压力。</p>
      {error && <div className="alert error" role="alert">{error}</div>}
      <div className="machine-table-scroll"><table className="machine-table" aria-label="服务器列表">
        <thead><tr><th><button onClick={() => sort("name")}>服务器名称 ↕</button></th><th><button onClick={() => sort("status")}>状态 ↕</button></th><th>负载</th><th>节点数</th><th><button onClick={() => sort("heartbeat")}>最后心跳 ↕</button></th><th>操作</th></tr></thead>
        <tbody>{loading ? <tr><td colSpan={6}>正在加载服务器…</td></tr> : filteredMachines.length === 0 ? <tr><td colSpan={6}>暂无服务器</td></tr> : filteredMachines.slice((currentPage - 1) * pageSize, currentPage * pageSize).map(machine => <tr className="machine-row" key={machine.id}>
          <td><div className="machine-name"><MachineIcon kind="server" /><strong>{machine.name}</strong><span className="badge">SID: {machine.id}</span></div><div className="machine-subline"><StatusBadge machine={machine} observedAt={observedAt} /> • 最后心跳：{relativeTime(machine.last_seen_at, observedAt)} • 节点数：{machine.servers_count}</div></td>
          <td><StatusBadge machine={machine} observedAt={observedAt} /></td>
          <td><MachineLoad machine={machine} /></td>
          <td><strong>{machine.servers_count}</strong> <span className="muted">已承载节点</span><div><button className="button compact secondary" onClick={() => setDetailMachine(machine)}>服务器详情</button></div></td>
          <td>{relativeTime(machine.last_seen_at, observedAt)}<small className="muted">负载上报: {relativeTime(machine.load_status ? new Date(machine.load_status.updated_at * 1000).toISOString() : null, observedAt)}</small></td>
          <td><div className="action-group"><button className="icon-button" aria-label={`打开服务器详情：${machine.name}`} onClick={() => setDetailMachine(machine)}><MachineIcon kind="detail" /></button><button className="icon-button" aria-label={`编辑服务器：${machine.name}`} onClick={() => setEditTarget(machine)}><MachineIcon kind="edit" /></button><button className="icon-button danger-text" aria-label={`删除服务器：${machine.name}`} onClick={() => setDeleteTarget(machine)}><MachineIcon kind="delete" /></button></div></td>
        </tr>)}</tbody>
      </table></div>
      <footer className="machine-pagination"><span>已选择 0 项，共 {filteredMachines.length} 项</span><div>每页显示 <select aria-label="每页显示" value={pageSize} onChange={e => { setPageSize(Number(e.target.value)); setPage(1); }}>{[10,20,30,40,50].map(n => <option key={n}>{n}</option>)}</select> 第 <input aria-label="页码" type="number" min={1} max={pageCount} value={currentPage} onChange={e => setPage(Math.min(pageCount, Math.max(1, Number(e.target.value) || 1)))} /> 页，共 {pageCount} 页
        <button aria-label="跳转到第一页" disabled={currentPage === 1} onClick={() => setPage(1)}>«</button><button aria-label="上一页" disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}>‹</button><button aria-label="下一页" disabled={currentPage === pageCount} onClick={() => setPage(currentPage + 1)}>›</button><button aria-label="跳转到最后一页" disabled={currentPage === pageCount} onClick={() => setPage(pageCount)}>»</button></div></footer>
      {editTarget && <EditMachineModal api={api} machine={editTarget} onClose={() => setEditTarget(null)} onUpdated={() => { setEditTarget(null); void refresh(); }} />}
      {deleteTarget && <DeleteMachineModal api={api} machine={deleteTarget} onClose={() => setDeleteTarget(null)} onDeleted={() => { setDeleteTarget(null); void refresh(); }} />}
      {creating && (
        <CreateMachineModal
          api={api}
          onClose={() => setCreating(false)}
          onCreated={(result) => {
            setCreating(false);
            setCreatedEnrollment(result);
            void refresh();
          }}
        />
      )}
      {createdEnrollment !== null && (
        <EnrollmentModal enrollment={createdEnrollment} onClose={() => setCreatedEnrollment(null)} />
      )}
      {detailMachine !== null && (
        <MachineDetailDrawer
          api={api}
          machine={detailMachine}
          onNavigateNodes={onNavigateNodes}
          observedAt={observedAt}
          onClose={() => setDetailMachine(null)}
          onChanged={() => void refresh()}
        />
      )}
    </main>
  );
}

function MachineIcon({ kind }: { kind: "server" | "detail" | "edit" | "delete" }) {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">{kind === "delete" ? <><path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7" /></> : kind === "edit" ? <><path d="m15 4 5 5M4 20l5-1L21 7a2 2 0 0 0-5-5L4 14v6Z" /></> : <><rect x="3" y="3" width="18" height="7" rx="2" /><rect x="3" y="14" width="18" height="7" rx="2" /><path d="M7 6.5h.01M7 17.5h.01M11 6.5h6M11 17.5h6" /></>}</svg>;
}

function OverviewMetric({ label, value, tone = "neutral", hint }: { label: string; value: number; tone?: "neutral" | "good" | "warning" | "danger"; hint?: string }) {
  return <article className={`overview-metric ${tone}`}><span>{label}</span><strong>{value}</strong>{hint !== undefined && <small>{hint}</small>}</article>;
}

function StatusBadge({ machine, observedAt }: { machine: Machine; observedAt: number }) {
	const status = machineStatus(machine, observedAt);
	const label = status === "online" ? "在线" : status === "inactive" ? "已停用" : "离线";
	return <span className={`badge ${status}`}>{label}</span>;
}

function machineStatus(machine: Machine, observedAt: number): "online" | "offline" | "inactive" {
  if (!machine.is_active) return "inactive";
  return machine.last_seen_at !== null && observedAt - new Date(machine.last_seen_at).getTime() <= 5 * 60 * 1000 ? "online" : "offline";
}

function machineLoad(machine: Machine): number {
  const memory = machine.load_status?.mem;
  const memoryPercent = memory !== undefined && memory.total > 0 ? memory.used / memory.total * 100 : 0;
  return Math.max(machine.load_status?.cpu ?? 0, memoryPercent);
}

function isHighLoad(machine: Machine): boolean {
  const memory = machine.load_status?.mem;
  const memoryPercent = memory !== undefined && memory.total > 0 ? memory.used / memory.total * 100 : 0;
  return (machine.load_status?.cpu ?? 0) >= 80 || memoryPercent >= 90;
}

function CreateMachineModal({ api, onClose, onCreated }: { api: AdminAPI; onClose: () => void; onCreated: (result: MachineEnrollment) => void }) {
  const [name, setName] = useState("");
  const [notes, setNotes] = useState("");
  const [active, setActive] = useState(true);
  const [nameTouched, setNameTouched] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const invalidName = nameTouched && name.trim() === "";

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting || name.trim() === "") return;
    setSubmitting(true);
    setError("");
    try {
      onCreated(await api.createMachine({ name, notes, is_active: active }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal title="新建服务器" className="machine-create-modal" onClose={onClose}>
      <header className="machine-create-header">
        <h2>新建服务器</h2>
        <p>当你希望一台服务器承载多个节点时，再创建服务器记录。</p>
        <button type="button" className="machine-create-close" aria-label="关闭新建服务器" onClick={onClose}>×</button>
      </header>
      <form onSubmit={(event) => void submit(event)}>
        <div className="machine-create-fields">
          <div className="machine-create-field">
            <label htmlFor="machine-create-name">服务器名称</label>
            <input autoFocus id="machine-create-name" value={name} placeholder="例如 HK-01" maxLength={255} required aria-invalid={invalidName} aria-describedby={invalidName ? "machine-create-name-error" : undefined} onBlur={() => setNameTouched(true)} onChange={(event) => setName(event.target.value)} />
            {invalidName && <p id="machine-create-name-error" className="machine-create-error">请输入服务器名称</p>}
          </div>
          <div className="machine-create-field">
            <label htmlFor="machine-create-notes">备注</label>
            <textarea id="machine-create-notes" value={notes} placeholder="关于此服务器的可选备注" maxLength={4000} onChange={(event) => setNotes(event.target.value)} />
          </div>
          <div className="machine-create-enabled">
            <div><label id="machine-create-enabled-label" htmlFor="machine-create-enabled">启用服务器</label><p id="machine-create-enabled-description">禁用后 xboard-node 将不再使用此服务器。</p></div>
            <button id="machine-create-enabled" type="button" role="switch" aria-checked={active} aria-labelledby="machine-create-enabled-label" aria-describedby="machine-create-enabled-description" className="machine-create-switch" onClick={() => setActive(value => !value)}><span /></button>
          </div>
          {error !== "" && <div className="alert error" role="alert">{error}</div>}
        </div>
        <footer className="machine-create-footer">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" type="submit" disabled={submitting || !name.trim()}>{submitting ? "正在提交…" : "提交"}</button>
        </footer>
      </form>
    </Modal>
  );
}

function MachineDetailDrawer({
  api,
  machine,
  observedAt,
  onClose,
  onChanged,
  onNavigateNodes
}: {
  onNavigateNodes?: (id: number, create: boolean) => void;
  api: AdminAPI;
  machine: Machine;
  observedAt: number;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [currentMachine, setCurrentMachine] = useState(machine);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [unassigned, setUnassigned] = useState<Node[]>([]);
  const [history, setHistory] = useState<LoadHistory[]>([]);
  const [range, setRange] = useState(6);
  const [scheduleNode, setScheduleNode] = useState<Node | null>(null);
  const [assignModalOpen, setAssignModalOpen] = useState(false);
  const [busyNodeID, setBusyNodeID] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [tokenDialogOpen, setTokenDialogOpen] = useState(false);

  const [enrollment, setEnrollment] = useState<Pick<MachineEnrollment, "token" | "token_type" | "expires_at" | "install_command"> | null>(null);
  const [enrollmentLoading, setEnrollmentLoading] = useState(true);
  const initialEnrollment = useRef<ReturnType<AdminAPI["createEnrollment"]> | null>(null);
  const [enrollmentError, setEnrollmentError] = useState("");

  const loadDetail = useCallback(async () => {
    try {
      const [linked, available, loadHistory] = await Promise.all([
        api.listMachineNodes(machine.id),
        api.listUnassignedNodes(),
        api.listLoadHistory(machine.id, range, 240)
      ]);
      setNodes(linked);
      setUnassigned(available);
      setHistory(loadHistory);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }, [api, machine.id, range]);

  useEffect(() => {
    let live = true;
    let pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const [linked, available, loadHistory, machines] = await Promise.all([
          api.listMachineNodes(machine.id), api.listUnassignedNodes(),
          api.listLoadHistory(machine.id, range, 240), api.listMachines()
        ]);
        if (!live) return;
        setNodes(linked); setUnassigned(available); setHistory(loadHistory);
        const latest = machines.find(item => item.id === machine.id);
        if (latest) setCurrentMachine(latest);
        setError("");
      } catch (cause) { if (live) setError(errorMessage(cause)); }
      finally { pending = false; }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 30000);
    return () => { live = false; window.clearInterval(timer); };
  }, [api, machine.id, range]);

  const fetchEnrollment = useCallback(async () => {
    setEnrollmentLoading(true);
    setEnrollment(null);
    setEnrollmentError("");
    try {
      const result = await api.createEnrollment(machine.id, false);
      setEnrollment(result);
    } catch (cause) {
      setEnrollmentError(errorMessage(cause));
    } finally {
      setEnrollmentLoading(false);
    }
  }, [api, machine.id]);

  useEffect(() => {
    let live = true;
    initialEnrollment.current ??= api.createEnrollment(machine.id, false);
    void initialEnrollment.current.then((result) => { if (live) setEnrollment(result); })
      .catch((cause: unknown) => { if (live) setEnrollmentError(errorMessage(cause)); })
      .finally(() => { if (live) setEnrollmentLoading(false); });
    return () => { live = false; };
  }, [api, machine.id]);

  const toggleNode = async (node: Node) => {
    setBusyNodeID(node.id);
    try {
      await api.setNodeEnabled(machine.id, node.id, node.revision, !node.enabled);
      setNodes((current) =>
        current.map((item) =>
          item.id === node.id ? { ...item, enabled: !item.enabled, revision: item.revision + 1 } : item
        )
      );
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusyNodeID(null);
    }
  };

  const assignNode = async (nodeID: number) => {
    const candidate = unassigned.find((node) => node.id === nodeID);
    if (candidate === undefined) return;
    await api.assignNode(machine.id, nodeID, candidate.revision);
    await loadDetail();
    onChanged();
  };

  const unassign = async (node: Node) => {
    setBusyNodeID(node.id);
    try {
      await api.unassignNode(machine.id, node.id, node.revision);
      await loadDetail();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusyNodeID(null);
    }
  };

  return (
    <>
      <Drawer
        title="服务器详情"
        className="machine-detail-drawer"
        suspended={scheduleNode !== null || assignModalOpen || tokenDialogOpen}
        onClose={onClose}
      >
        <div className="machine-detail-header">
          <div className="machine-detail-title-group">
            <span className="machine-detail-icon"><MachineIcon kind="server" /></span>
            <h2>{currentMachine.name}</h2>
          </div>
          <button
            type="button"
            className="machine-detail-close-btn"
            aria-label="关闭服务器详情"
            onClick={onClose}
          >
            <CloseIcon />
          </button>
        </div>

        <div className="machine-detail-body">
          {error && <div className="alert error" role="alert">{error}</div>}
          {/* Summary Card */}
          <section className="detail-card machine-summary-card">
            <div className="machine-summary-left">
              <div className="machine-summary-badges">
                <span className="machine-sid-badge">SID:{currentMachine.id}</span>
                <StatusBadge machine={currentMachine} observedAt={observedAt} />
                <span className="machine-summary-cpu">
                  <span className="summary-cpu-dot" /> CPU {Math.round(currentMachine.load_status?.cpu ?? history.at(-1)?.cpu ?? 0)}%
                </span>
              </div>
              <div className="machine-summary-meta">
                最后心跳: {relativeTime(currentMachine.last_seen_at, observedAt)}
                <span className="meta-separator">•</span>
                节点数: {nodes.length}
              </div>
            </div>
            <div className="machine-summary-right">
              {onNavigateNodes && (
                <>
                  <button
                    type="button"
                    className="button primary compact machine-action-add-node"
                    onClick={() => onNavigateNodes(machine.id, true)}
                  >
                    <span>新增节点到此服务器</span>
                    <ArrowRightIcon />
                  </button>
                  <button
                    type="button"
                    className="button secondary compact machine-action-goto-nodes"
                    onClick={() => onNavigateNodes(machine.id, false)}
                  >
                    <span>前往节点管理</span>
                    <ExternalLinkIcon />
                  </button>
                </>
              )}
            </div>
          </section>

          {/* Trend & Load Side by Side */}
          <div className="machine-panels-row">
            <DetailTrendPanel
              history={history}
              range={range}
              onRangeChange={setRange}
            />
            <DetailLoadPanel
              machine={currentMachine}
              history={history}
            />
          </div>

          <MachineTokenSection api={api} machineID={machine.id} onReset={fetchEnrollment} onDialogChange={setTokenDialogOpen} />
          {/* Inline Install Section */}
          <DetailInstallSection
            enrollment={enrollment}
            loading={enrollmentLoading}
            error={enrollmentError}
            onRetry={() => void fetchEnrollment()}
          />

          {/* Linked Nodes Section */}
          <DetailLinkedNodesSection
            nodes={nodes}
            busyNodeID={busyNodeID}
            onNavigateNodes={onNavigateNodes}
            machineID={machine.id}
            onToggleNode={toggleNode}
            onOpenSchedule={setScheduleNode}
            onUnassign={unassign}
            onOpenAssign={() => setAssignModalOpen(true)}
          />
        </div>
      </Drawer>

      {scheduleNode !== null && (
        <ScheduleModal
          api={api}
          node={scheduleNode}
          onClose={() => setScheduleNode(null)}
          onSaved={() => void loadDetail()}
        />
      )}

      {assignModalOpen && (
        <AssignNodeModal
          unassigned={unassigned}
          onClose={() => setAssignModalOpen(false)}
          onAssign={assignNode}
        />
      )}
    </>
  );
}

function DetailTrendPanel({
  history,
  range,
  onRangeChange
}: {
  history: LoadHistory[];
  range: number;
  onRangeChange: (hours: number) => void;
}) {
  const [hovered, setHovered] = useState<number | null>(null);
  const [activeSeries, setActiveSeries] = useState({
    cpu: true,
    mem: true,
    disk: true,
    in: true,
    out: true
  });

  const toggle = (key: keyof typeof activeSeries) => {
    setActiveSeries((prev) => ({ ...prev, [key]: !prev[key] }));
  };

  const width = 600;
  const height = 140;
  const n = history.length;
  const divisor = Math.max(n - 1, 1);

  const maxNetSpeed = Math.max(
    1,
    ...history.map((h) => Math.max(h.net_in_speed || 0, h.net_out_speed || 0))
  );

  const cpuPoints = history
    .map(
      (h, i) =>
        `${((i / divisor) * width).toFixed(1)},${(
          height -
          (Math.min(Math.max(h.cpu, 0), 100) / 100) * height
        ).toFixed(1)}`
    )
    .join(" ");

  const memPoints = history
    .map(
      (h, i) =>
        `${((i / divisor) * width).toFixed(1)},${(
          height -
          (Math.min(Math.max(percent(h.mem_used, h.mem_total), 0), 100) / 100) *
            height
        ).toFixed(1)}`
    )
    .join(" ");

  const diskPoints = history
    .map(
      (h, i) =>
        `${((i / divisor) * width).toFixed(1)},${(
          height -
          (Math.min(Math.max(percent(h.disk_used, h.disk_total), 0), 100) /
            100) *
            height
        ).toFixed(1)}`
    )
    .join(" ");

  const inPoints = history
    .map(
      (h, i) =>
        `${((i / divisor) * width).toFixed(1)},${(
          height -
          (Math.min(Math.max(h.net_in_speed, 0), maxNetSpeed) / maxNetSpeed) *
            height
        ).toFixed(1)}`
    )
    .join(" ");

  const outPoints = history
    .map(
      (h, i) =>
        `${((i / divisor) * width).toFixed(1)},${(
          height -
          (Math.min(Math.max(h.net_out_speed, 0), maxNetSpeed) / maxNetSpeed) *
            height
        ).toFixed(1)}`
    )
    .join(" ");

  // Pick tick indices for time display from actual history
  const tickIndices: number[] = [];
  if (n > 0) {
    const tickCount = Math.min(n, 7);
    if (tickCount <= 2) {
      for (let i = 0; i < n; i++) tickIndices.push(i);
    } else {
      for (let i = 0; i < tickCount; i++) {
        tickIndices.push(Math.round((i / (tickCount - 1)) * (n - 1)));
      }
    }
  }

  return (
    <section className="detail-card detail-trend-card">
      <div className="detail-trend-header">
        <div className="detail-panel-title">
          <ActivityIcon />
          <h3>负载趋势</h3>
        </div>
        <div className="trend-range-pills" aria-label="趋势时间范围">
          {[1, 6, 12, 24].map((hours) => (
            <button
              key={hours}
              type="button"
              className={`trend-range-pill ${range === hours ? "active" : ""}`}
              aria-pressed={range === hours}
              onClick={() => onRangeChange(hours)}
            >
              {hours}h
            </button>
          ))}
        </div>
      </div>

      <div className="trend-series-toggles">
        <button
          type="button"
          className={`trend-series-btn ${activeSeries.cpu ? "active" : "muted"}`}
          aria-pressed={activeSeries.cpu}
          onClick={() => toggle("cpu")}
        >
          <span className="series-dot cpu" /> CPU
        </button>
        <button
          type="button"
          className={`trend-series-btn ${activeSeries.mem ? "active" : "muted"}`}
          aria-pressed={activeSeries.mem}
          onClick={() => toggle("mem")}
        >
          <span className="series-dot mem" /> MEM
        </button>
        <button
          type="button"
          className={`trend-series-btn ${activeSeries.disk ? "active" : "muted"}`}
          aria-pressed={activeSeries.disk}
          onClick={() => toggle("disk")}
        >
          <span className="series-dot disk" /> DISK
        </button>
        <span className="series-divider">|</span>
        <button
          type="button"
          className={`trend-series-btn ${activeSeries.in ? "active" : "muted"}`}
          aria-pressed={activeSeries.in}
          onClick={() => toggle("in")}
        >
          <span className="series-dot in" /> ↓ IN
        </button>
        <button
          type="button"
          className={`trend-series-btn ${activeSeries.out ? "active" : "muted"}`}
          aria-pressed={activeSeries.out}
          onClick={() => toggle("out")}
        >
          <span className="series-dot out" /> ↑ OUT
        </button>
      </div>

      <div className="trend-chart-container">
        {/* Left axis (percent) */}
        <div className="trend-axis left">
          <span>100%</span>
          <span>75%</span>
          <span>50%</span>
          <span>25%</span>
          <span>0%</span>
        </div>

        {/* SVG chart */}
        <div className="trend-chart-svg-wrap">
          {n === 0 ? (
            <div className="trend-empty muted">暂无负载历史数据</div>
          ) : (
            <svg
              className="multiseries-trend-chart"
              viewBox={`0 0 ${width} ${height}`}
              preserveAspectRatio="none"
              role="img"
              aria-label="多指标负载趋势图"
              tabIndex={0}
              onMouseMove={event => {
                const bounds = event.currentTarget.getBoundingClientRect();
                setHovered(Math.max(0, Math.min(n - 1, Math.round((event.clientX - bounds.left) / bounds.width * (n - 1)))));
              }}
              onMouseLeave={() => setHovered(null)}
              onFocus={() => setHovered(n - 1)}
              onBlur={() => setHovered(null)}
              onKeyDown={event => {
                if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
                  event.preventDefault();
                  setHovered(index => Math.max(0, Math.min(n - 1, (index ?? n - 1) + (event.key === "ArrowLeft" ? -1 : 1))));
                }
              }}
            >
              <defs>
                <linearGradient id="trend-grad-in" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#34d399" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#34d399" stopOpacity="0.0" />
                </linearGradient>
                <linearGradient id="trend-grad-out" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor="#60a5fa" stopOpacity="0.35" />
                  <stop offset="100%" stopColor="#60a5fa" stopOpacity="0.0" />
                </linearGradient>
              </defs>

              {/* Gridlines */}
              {[0, 0.25, 0.5, 0.75, 1].map((p) => (
                <line
                  key={p}
                  x1="0"
                  y1={height * (1 - p)}
                  x2={width}
                  y2={height * (1 - p)}
                  className="trend-gridline"
                />
              ))}

              {/* Series lines and areas */}
              {activeSeries.in && maxNetSpeed > 0 && n > 1 && (
                <>
                  <polygon
                    points={`0,${height} ${inPoints} ${width},${height}`}
                    fill="url(#trend-grad-in)"
                  />
                  <polyline
                    fill="none"
                    stroke="#34d399"
                    strokeWidth="1.5"
                    vectorEffect="non-scaling-stroke"
                    points={inPoints}
                  />
                </>
              )}

              {activeSeries.out && maxNetSpeed > 0 && n > 1 && (
                <>
                  <polygon
                    points={`0,${height} ${outPoints} ${width},${height}`}
                    fill="url(#trend-grad-out)"
                  />
                  <polyline
                    fill="none"
                    stroke="#60a5fa"
                    strokeWidth="1.5"
                    vectorEffect="non-scaling-stroke"
                    points={outPoints}
                  />
                </>
              )}

              {activeSeries.cpu && n > 1 && (
                <polyline
                  fill="none"
                  stroke="#38bdf8"
                  strokeWidth="1.8"
                  vectorEffect="non-scaling-stroke"
                  points={cpuPoints}
                />
              )}

              {activeSeries.mem && n > 1 && (
                <polyline
                  fill="none"
                  stroke="#fbbf24"
                  strokeWidth="1.8"
                  vectorEffect="non-scaling-stroke"
                  points={memPoints}
                />
              )}

              {activeSeries.disk && n > 1 && (
                <polyline
                  fill="none"
                  stroke="#f43f5e"
                  strokeWidth="1.8"
                  vectorEffect="non-scaling-stroke"
                  points={diskPoints}
                />
              )}
            </svg>
          )}
        </div>

        {/* Right axis (network rate) */}
        <div className="trend-axis right">
          <span>{formatRate(maxNetSpeed * 1.0)}</span>
          <span>{formatRate(maxNetSpeed * 0.75)}</span>
          <span>{formatRate(maxNetSpeed * 0.5)}</span>
          <span>{formatRate(maxNetSpeed * 0.25)}</span>
          <span>0 B/s</span>
        </div>
        {hovered !== null && history[hovered] && <div className="trend-tooltip" role="status">
          <strong>{new Date(history[hovered].recorded_at).toLocaleString()}</strong>
          <span>CPU {history[hovered].cpu.toFixed(1)}% · MEM {percent(history[hovered].mem_used, history[hovered].mem_total).toFixed(1)}% · DISK {percent(history[hovered].disk_used, history[hovered].disk_total).toFixed(1)}%</span>
          <span>↓ {formatRate(history[hovered].net_in_speed)} · ↑ {formatRate(history[hovered].net_out_speed)}</span>
        </div>}
      </div>

      {/* Time ticks from actual history */}
      {n > 0 && (
        <div className="trend-time-ticks">
          {tickIndices.map((idx, i) => (
            <span
              key={idx}
              className="trend-tick-label"
              style={{
                left: `${(idx / divisor) * 100}%`,
                transform:
                  i === 0
                    ? "none"
                    : i === tickIndices.length - 1
                    ? "translateX(-100%)"
                    : "translateX(-50%)"
              }}
            >
              {history[idx] && formatTickTime(history[idx].recorded_at)}
            </span>
          ))}
        </div>
      )}
    </section>
  );
}

function DetailLoadPanel({ machine, history }: { machine: Machine; history: LoadHistory[] }) {
  const latest = history.at(-1);
  const cpu = machine.load_status?.cpu ?? latest?.cpu ?? 0;
  const memoryTotal = machine.load_status?.mem?.total ?? latest?.mem_total ?? 0;
  const memoryUsed = machine.load_status?.mem?.used ?? latest?.mem_used ?? 0;
  const diskTotal = machine.load_status?.disk?.total ?? latest?.disk_total ?? 0;
  const diskUsed = machine.load_status?.disk?.used ?? latest?.disk_used ?? 0;
  const networkIn = machine.load_status?.net?.in_speed ?? latest?.net_in_speed ?? 0;
  const networkOut = machine.load_status?.net?.out_speed ?? latest?.net_out_speed ?? 0;
  const memoryPercent = percent(memoryUsed, memoryTotal);
  const diskPercent = percent(diskUsed, diskTotal);

  return (
    <section className="detail-card detail-load-card">
      <div className="detail-trend-header">
        <div className="detail-panel-title">
          <BarChartIcon />
          <span>负载</span>
        </div>
      </div>
      <div className="detail-load-body">
        <div className="load-bar-item">
          <div className="load-bar-header">
            <span className="load-bar-label"><CpuIcon /> CPU</span>
            <span className="load-bar-value">{cpu.toFixed(1)}%</span>
          </div>
          <div className="load-bar-track">
            <div className="load-bar-fill" style={{ width: `${Math.min(cpu, 100)}%` }} />
          </div>
        </div>
        <div className="load-bar-item">
          <div className="load-bar-header">
            <span className="load-bar-label"><MemoryIcon /> 内存</span>
            <span className="load-bar-value">
              {(memoryUsed / 1024 ** 3).toFixed(2)} GB / {(memoryTotal / 1024 ** 3).toFixed(2)} GB
            </span>
          </div>
          <div className="load-bar-track">
            <div className="load-bar-fill" style={{ width: `${Math.min(memoryPercent, 100)}%` }} />
          </div>
        </div>
        <div className="load-bar-item">
          <div className="load-bar-header">
            <span className="load-bar-label"><DiskIcon /> 磁盘</span>
            <span className="load-bar-value">
              {(diskUsed / 1024 ** 3).toFixed(2)} GB / {(diskTotal / 1024 ** 3).toFixed(2)} GB
            </span>
          </div>
          <div className="load-bar-track">
            <div className="load-bar-fill" style={{ width: `${Math.min(diskPercent, 100)}%` }} />
          </div>
        </div>
        <div className="load-bar-item">
          <div className="load-bar-header">
            <span className="load-bar-label"><NetworkRateIcon /> 网络速率</span>
            <span className="load-bar-value">
              ↓{formatRate(networkIn)} ↑{formatRate(networkOut)}
            </span>
          </div>
        </div>
      </div>
    </section>
  );
}

function DetailInstallSection({
  enrollment,
  loading,
  error,
  onRetry
}: {
  enrollment: Pick<MachineEnrollment, "token" | "token_type" | "expires_at" | "install_command"> | null;
  loading: boolean;
  error: string;
  onRetry: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [clock, setClock] = useState(() => Date.now());
  const [copyError, setCopyError] = useState("");
  useEffect(() => { const timer = window.setInterval(() => setClock(Date.now()), 1000); return () => window.clearInterval(timer); }, []);
  const isExpired = enrollment?.expires_at ? new Date(enrollment.expires_at).getTime() <= clock : false;

  const copy = async () => {
    if (!enrollment?.install_command) return;
    try { await navigator.clipboard.writeText(enrollment.install_command); setCopied(true); setCopyError(""); }
    catch { setCopyError("复制失败，请手动复制安装命令。"); }
  };

  return (
    <section className="detail-card detail-install-card">
      <div className="detail-install-header">
        <h3 className="detail-install-title">&gt;_ 安装 xboard-node</h3>
        <p className="detail-install-desc">在目标服务器上执行此命令，即可用 machine mode 安装 xboard-node 并接入当前服务器记录。</p>
      </div>
      <div className="detail-install-content">
        {loading ? (
          <div className="detail-install-status muted">正在生成安装命令…</div>
        ) : error !== "" ? (
          <div className="detail-install-status error">
            <span>生成安装命令失败: {error}</span>
            <button type="button" className="button secondary compact" onClick={onRetry}>重试</button>
          </div>
        ) : isExpired ? (
          <div className="detail-install-status warning">
            <span>接入命令已过期</span>
            <button type="button" className="button secondary compact" onClick={onRetry}>重新生成</button>
          </div>
        ) : enrollment ? (
          <pre className="detail-install-command"><code>{enrollment.install_command}</code></pre>
        ) : (
          <div className="detail-install-status muted">暂无安装命令</div>
        )}
      </div>
      {copyError && <p role="alert">{copyError}</p>}
      <div className="detail-install-footer">
        <span className="detail-install-hint">需要 root 或 sudo 权限，且目标服务器需为支持 systemd 的 Linux。</span>
        <button
          type="button"
          className="button secondary compact detail-copy-button"
          disabled={!enrollment || isExpired || loading || error !== ""}
          onClick={() => void copy()}
        >
          <CopyIcon />
          <span>{copied ? "已复制" : "复制安装命令"}</span>
        </button>
      </div>
    </section>
  );
}

function DetailLinkedNodesSection({
  nodes,
  busyNodeID,
  onNavigateNodes,
  machineID,
  onToggleNode,
  onOpenSchedule,
  onUnassign,
  onOpenAssign
}: {
  nodes: Node[];
  busyNodeID: number | null;
  onNavigateNodes?: (id: number, create: boolean) => void;
  machineID: number;
  onToggleNode: (node: Node) => Promise<void>;
  onOpenSchedule: (node: Node) => void;
  onUnassign: (node: Node) => Promise<void>;
  onOpenAssign: () => void;
}) {
  const activeCount = nodes.filter((n) => n.enabled).length;

  return (
    <section className="detail-card detail-nodes-card">
      <div className="detail-nodes-header">
        <div className="detail-nodes-title-group">
          <h3>关联节点</h3>
          <span className="badge-pill">{nodes.length} 个节点</span>
          <span className="badge-pill">{activeCount} 个已激活</span>
        </div>
        <div className="detail-nodes-actions">
          <button type="button" className="button secondary compact" onClick={onOpenAssign}>
            <LinkIcon />
            <span>关联已有节点</span>
          </button>
          {onNavigateNodes && (
            <button
              type="button"
              className="button ghost compact"
              onClick={() => onNavigateNodes(machineID, false)}
            >
              <span>前往节点管理</span>
              <ArrowRightIcon />
            </button>
          )}
        </div>
      </div>
      {nodes.length === 0 ? (
        <p className="muted" style={{ margin: "12px 0 0", fontSize: "12px" }}>暂无关联节点。</p>
      ) : (
        <div className="detail-linked-table-scroll">
          <table className="machine-linked-table">
            <thead>
              <tr>
                <th>名称</th>
                <th>类型</th>
                <th>地址</th>
                <th>已激活</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((node) => (
                <tr key={node.id}>
                  <td>
                    {onNavigateNodes ? (
                      <button
                        type="button"
                        className="linked-node-link"
                        onClick={() => onNavigateNodes(machineID, false)}
                      >
                        <strong>{node.name}</strong>
                        <ExternalLinkIcon />
                      </button>
                    ) : (
                      <strong>{node.name}</strong>
                    )}
                  </td>
                  <td>
                    <span className="linked-node-type-badge">{node.type}</span>
                  </td>
                  <td className="monospace">{node.host}:{node.port}</td>
                  <td>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={node.enabled}
                      disabled={busyNodeID === node.id}
                      aria-label={`启用节点：${node.name}`}
                      className="machine-create-switch"
                      onClick={() => void onToggleNode(node)}
                    >
                      <span />
                    </button>
                  </td>
                  <td>
                    <button
                      type="button"
                      className="button compact secondary"
                      aria-label={`定时设置：${node.name}`}
                      onClick={() => onOpenSchedule(node)}
                    >
                      定时设置
                    </button>
                    <button
                      type="button"
                      className="button compact ghost danger-text"
                      disabled={busyNodeID === node.id}
                      onClick={() => void onUnassign(node)}
                    >
                      解除关联
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function AssignNodeModal({ unassigned, onClose, onAssign }: {
  unassigned: Node[]; onClose: () => void; onAssign: (nodeID: number) => Promise<void>;
}) {
  const [selected, setSelected] = useState<number[]>([]);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!selected.length || submitting) return;
    setSubmitting(true); setError("");
    try {
      for (const id of selected) {
        await onAssign(id);
        setSelected(current => current.filter(value => value !== id));
      }
      onClose();
    } catch (cause) { setError(errorMessage(cause)); }
    finally { setSubmitting(false); }
  };
  return <Modal title="关联已有节点" className="machine-create-modal" onClose={() => { if (!submitting) onClose(); }}>
    <header className="machine-create-header"><h2>关联已有节点</h2><p>选择要关联到当前服务器的节点</p><button type="button" className="machine-create-close" disabled={submitting} aria-label="关闭关联节点" onClick={onClose}>×</button></header>
    <form onSubmit={event => void submit(event)}>
      <div className="machine-create-fields">
        {unassigned.length === 0 ? <p className="muted">没有未绑定的节点</p> : <>
          <input aria-label="搜索待关联节点" placeholder="搜索节点" value={search} onChange={event => setSearch(event.target.value)} />
          <div className="assign-node-options">{unassigned.filter(node => node.name.toLowerCase().includes(search.toLowerCase())).map(node =>
            <label className="switch-label" key={node.id}><input type="checkbox" disabled={submitting} checked={selected.includes(node.id)} onChange={event => setSelected(current => event.target.checked ? [...current,node.id] : current.filter(id => id !== node.id))} />{node.name} ({node.type})</label>
          )}</div>
        </>}
        <p className="muted">已选 {selected.length} 个</p>
        {error && <div className="alert error" role="alert">{error}</div>}
      </div>
      <footer className="machine-create-footer"><button className="button ghost" type="button" disabled={submitting} onClick={onClose}>取消</button><button className="button primary" disabled={submitting || !selected.length}>{submitting ? "正在关联…" : `关联 ${selected.length} 个节点`}</button></footer>
    </form>
  </Modal>;
}

function percent(used: number, total: number): number {
  return total > 0 ? (used / total) * 100 : 0;
}

function formatRate(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return "—";
  if (value >= 1024 * 1024 * 1024) return `${(value / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB/s`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB/s`;
  return `${value.toFixed(0)} B/s`;
}

function formatTickTime(value: string | null | undefined): string {
  if (!value) return "";
  try {
    const d = new Date(value);
    if (isNaN(d.getTime())) return "";
    const hours = String(d.getHours()).padStart(2, "0");
    const minutes = String(d.getMinutes()).padStart(2, "0");
    return `${hours}:${minutes}`;
  } catch {
    return "";
  }
}

function CloseIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  );
}

function ArrowRightIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M5 12h14M12 5l7 7-7 7" />
    </svg>
  );
}

function ExternalLinkIcon() {
  return (
    <svg aria-hidden="true" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6M15 3h6v6M10 14 21 3" />
    </svg>
  );
}

function ActivityIcon() {
  return (
    <svg aria-hidden="true" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
    </svg>
  );
}

function BarChartIcon() {
  return (
    <svg aria-hidden="true" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <line x1="12" y1="20" x2="12" y2="10" />
      <line x1="18" y1="20" x2="18" y2="4" />
      <line x1="6" y1="20" x2="6" y2="16" />
    </svg>
  );
}

function CpuIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="4" y="4" width="16" height="16" rx="2" />
      <rect x="9" y="9" width="6" height="6" />
      <path d="M15 2v2M9 2v2M20 15h2M20 9h2M9 20v2M15 20v2M2 9h2M2 15h2" />
    </svg>
  );
}

function MemoryIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M6 19v2M10 19v2M14 19v2M18 19v2M6 3v2M10 3v2M14 3v2M18 3v2M2 7h20v10H2z" />
    </svg>
  );
}

function DiskIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="M7 16h.01M17 16h.01" />
    </svg>
  );
}

function NetworkRateIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M7 16V4M3 8l4-4 4 4M17 8v12M13 16l4 4 4-4" />
    </svg>
  );
}

function CopyIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
      <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
    </svg>
  );
}

function LinkIcon() {
  return (
    <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
      <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
    </svg>
  );
}

function EditMachineModal({ api, machine, onClose, onUpdated }: { api: AdminAPI; machine: Machine; onClose: () => void; onUpdated: (machine: Machine) => void }) {
  const [name, setName] = useState(machine.name);
  const [notes, setNotes] = useState(machine.notes);
  const [isActive, setIsActive] = useState(machine.is_active);
  const [nameTouched, setNameTouched] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const invalidName = nameTouched && name.trim() === "";

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving || name.trim() === "") return;
    setSaving(true);
    setError("");
    try {
      onUpdated(await api.updateMachine(machine.id, { name: name.trim(), notes, is_active: isActive }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title="编辑服务器" className="machine-create-modal" onClose={onClose}>
      <header className="machine-create-header">
        <h2>编辑服务器</h2>
        <p>修改服务器名称、备注或启用状态。</p>
        <button type="button" className="machine-create-close" aria-label="关闭编辑服务器" onClick={onClose}>×</button>
      </header>
      <form onSubmit={(event) => void submit(event)}>
        <div className="machine-create-fields">
          <div className="machine-create-field">
            <label htmlFor="machine-edit-name">服务器名称</label>
            <input
              autoFocus
              id="machine-edit-name"
              value={name}
              placeholder="例如 HK-01"
              maxLength={255}
              required
              aria-invalid={invalidName}
              aria-describedby={invalidName ? "machine-edit-name-error" : undefined}
              onBlur={() => setNameTouched(true)}
              onChange={(event) => setName(event.target.value)}
            />
            {invalidName && <p id="machine-edit-name-error" className="machine-create-error">请输入服务器名称</p>}
          </div>
          <div className="machine-create-field">
            <label htmlFor="machine-edit-notes">备注</label>
            <textarea
              id="machine-edit-notes"
              value={notes}
              placeholder="关于此服务器的可选备注"
              maxLength={4000}
              onChange={(event) => setNotes(event.target.value)}
            />
          </div>
          <div className="machine-create-enabled">
            <div>
              <label id="machine-edit-enabled-label" htmlFor="machine-edit-enabled">启用服务器</label>
              <p id="machine-edit-enabled-description">禁用后 xboard-node 将不再使用此服务器。</p>
            </div>
            <button
              id="machine-edit-enabled"
              type="button"
              role="switch"
              aria-checked={isActive}
              aria-labelledby="machine-edit-enabled-label"
              aria-describedby="machine-edit-enabled-description"
              className="machine-create-switch"
              onClick={() => setIsActive((value) => !value)}
            >
              <span />
            </button>
          </div>
          {error !== "" && <div className="alert error" role="alert">{error}</div>}
        </div>
        <footer className="machine-create-footer">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" type="submit" disabled={saving || !name.trim()}>
            {saving ? "正在更新…" : "更新"}
          </button>
        </footer>
      </form>
    </Modal>
  );
}

function DeleteMachineModal({ api, machine, onClose, onDeleted }: { api: AdminAPI; machine: Machine; onClose: () => void; onDeleted: () => void }) {
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setDeleting(true);
    setError("");
    try {
      await api.deleteMachine(machine.id);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setDeleting(false);
    }
  };
  return (
    <Modal title="删除服务器" onClose={onClose}>
      <ModalHeader title="删除服务器" onClose={onClose} />
      <p>确定删除“{machine.name}”吗？关联节点会解除关联，节点本身不会被删除。</p>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions">
        <button className="button ghost" onClick={onClose}>取消</button>
        <button className="button primary destructive" disabled={deleting} onClick={() => void remove()}>{deleting ? "正在删除…" : "确认删除"}</button>
      </div>
    </Modal>
  );
}

function ScheduleModal({ api, node, onClose, onSaved }: { api: AdminAPI; node: Node; onClose: () => void; onSaved: () => void }) {
  const [enableTime, setEnableTime] = useState("19:00");
  const [disableTime, setDisableTime] = useState("01:00");
  const [existing, setExisting] = useState<ActivationSchedule | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    void api.getActivationSchedule(node.id).then((result) => {
      if (!live) return;
      setExisting(result);
      if (result.enable_time !== "") setEnableTime(result.enable_time);
      else if (result.enable_at != null) setEnableTime(singaporeClock(result.enable_at));
      if (result.disable_time !== "") setDisableTime(result.disable_time);
      else if (result.disable_at != null) setDisableTime(singaporeClock(result.disable_at));
    }).catch((cause: unknown) => {
      if (live && errorStatus(cause) !== 404) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, node.id]);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (enableTime === disableTime) {
      setError("启用时间和停用时间不能相同");
      return;
    }
    setSaving(true);
    setError("");
    const input: DailyScheduleInput = {
      schedule_type: "daily",
      timezone: "Asia/Singapore",
      enable_time: enableTime,
      disable_time: disableTime
    };
    try {
      await api.saveActivationSchedule(node.id, input);
      onSaved();
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    setSaving(true);
    setError("");
    try {
      await api.deleteActivationSchedule(node.id);
      onSaved();
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title="激活计划设置" onClose={onClose}>
      <ModalHeader title="激活计划设置" subtitle={node.name} onClose={onClose} />
      <form className="form-stack" onSubmit={(event) => void save(event)}>
        {existing?.schedule_type === "once" && <div className="alert warning">旧版单次计划将在保存后替换为每日计划。</div>}
        <p className="muted">时区固定为 Asia/Singapore。跨午夜时段会自动识别。</p>
        <div className="time-grid">
          <label>启用时间<input type="time" value={enableTime} required disabled={loading || saving} onChange={(event) => setEnableTime(event.target.value)} /></label>
          <label>停用时间<input type="time" value={disableTime} required disabled={loading || saving} onChange={(event) => setDisableTime(event.target.value)} /></label>
        </div>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="form-actions split">
          <div>{existing !== null && <button className="button ghost danger-text" type="button" disabled={saving} onClick={() => void remove()}>删除计划</button>}</div>
          <div className="action-group">
            <button className="button ghost" type="button" onClick={onClose}>取消</button>
            <button className="button primary" type="submit" disabled={loading || saving}>{saving ? "正在保存…" : "保存计划"}</button>
          </div>
        </div>
      </form>
    </Modal>
  );
}

function EnrollmentModal({ enrollment, onClose }: { enrollment: MachineEnrollment; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    await navigator.clipboard.writeText(enrollment.install_command);
    setCopied(true);
  };
  return (
    <Modal title="服务器接入命令" onClose={onClose}>
      <ModalHeader title="服务器接入命令" subtitle="此接入码只展示一次，并将在 15 分钟后过期。" onClose={onClose} />
      <pre className="command-box"><code>{enrollment.install_command}</code></pre>
      <div className="form-actions"><button className="button primary" onClick={() => void copy()}>{copied ? "已复制" : "复制命令"}</button></div>
    </Modal>
  );
}

function ModalHeader({ title, subtitle, onClose }: { title: string; subtitle?: string; onClose: () => void }) {
  return (
    <div className="modal-header">
      <div><h2>{title}</h2>{subtitle !== undefined && <p className="muted">{subtitle}</p>}</div>
      <button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button>
    </div>
  );
}

function singaporeClock(value: string): string {
  return new Intl.DateTimeFormat("en-GB", {
    timeZone: "Asia/Singapore",
    hour: "2-digit",
    minute: "2-digit",
    hourCycle: "h23"
  }).format(new Date(value));
}

function errorStatus(cause: unknown): number | undefined {
  return typeof cause === "object" && cause !== null && "status" in cause && typeof cause.status === "number" ? cause.status : undefined;
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}

function relativeTime(value: string | null, now: number): string {
  if (!value) return "从未上报";
  const elapsed = Math.max(0, Math.floor((now - Date.parse(value)) / 1000));
  if (!Number.isFinite(elapsed)) return "—";
  return elapsed < 60 ? `${elapsed}s` : elapsed < 3600 ? `${Math.floor(elapsed / 60)}m` : elapsed < 86400 ? `${Math.floor(elapsed / 3600)}h` : `${Math.floor(elapsed / 86400)}d`;
}
function MachineLoad({ machine }: { machine: Machine }) {
  const load = machine.load_status;
  if (!load) return <span className="muted">暂无负载数据</span>;
  return <div className="machine-load">{[["CPU", load.cpu], ["MEM", percent(load.mem.used, load.mem.total)]].map(([label, value]) => <div key={label}><div>{label}<strong>{Number(value).toFixed(0)}%</strong></div><progress max={100} value={Number(value)} /></div>)}<small>DISK {load.disk ? `${(load.disk.used / 1024 ** 3).toFixed(2)} GB / ${(load.disk.total / 1024 ** 3).toFixed(2)} GB` : "—"}</small></div>;
}
