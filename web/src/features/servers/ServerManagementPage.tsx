import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Drawer, Modal } from "../../components/Overlay";
import type { ActivationSchedule, AdminAPI, DailyScheduleInput, LoadHistory, Machine, MachineEnrollment, Node } from "../../lib/api";

import "./ServerManagementPage.css";

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

function MachineDetailDrawer({ api, machine, observedAt, onClose, onChanged, onNavigateNodes }: { onNavigateNodes?: (id: number, create: boolean) => void; api: AdminAPI; machine: Machine; observedAt: number; onClose: () => void; onChanged: () => void }) {
	const [currentMachine, setCurrentMachine] = useState(machine);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [unassigned, setUnassigned] = useState<Node[]>([]);
  const [history, setHistory] = useState<LoadHistory[]>([]);
  const [range, setRange] = useState(1);
  const [selectedNodeID, setSelectedNodeID] = useState("");
  const [scheduleNode, setScheduleNode] = useState<Node | null>(null);
  const [enrollment, setEnrollment] = useState<MachineEnrollment | null>(null);
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busyNodeID, setBusyNodeID] = useState<number | null>(null);
  const [error, setError] = useState("");

  const loadDetail = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [linked, available, loadHistory] = await Promise.all([api.listMachineNodes(machine.id), api.listUnassignedNodes(), api.listLoadHistory(machine.id, range, 240)]);
      setNodes(linked);
      setUnassigned(available);
      setHistory(loadHistory);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api, machine.id, range]);

  useEffect(() => {
    let live = true;
    void Promise.all([api.listMachineNodes(machine.id), api.listUnassignedNodes(), api.listLoadHistory(machine.id, range, 240)]).then(([linked, available, loadHistory]) => {
      if (!live) return;
      setNodes(linked);
      setUnassigned(available);
      setHistory(loadHistory);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, machine.id, range]);

  const toggleNode = async (node: Node) => {
    setBusyNodeID(node.id);
    setError("");
    try {
      await api.setNodeEnabled(machine.id, node.id, node.revision, !node.enabled);
      setNodes((current) => current.map((item) => item.id === node.id ? { ...item, enabled: !item.enabled, revision: item.revision + 1 } : item));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusyNodeID(null);
    }
  };

  const assignSelected = async () => {
    const nodeID = Number(selectedNodeID);
    if (!Number.isInteger(nodeID) || nodeID < 1) return;
    setBusyNodeID(nodeID);
    try {
      const candidate = unassigned.find((node) => node.id === nodeID);
      if (candidate === undefined) return;
      await api.assignNode(machine.id, nodeID, candidate.revision);
      setSelectedNodeID("");
      await loadDetail();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusyNodeID(null);
    }
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

  const rotateEnrollment = async () => {
    setError("");
    try {
      const result = await api.createEnrollment(machine.id, true);
      setEnrollment({ ...currentMachine, ...result });
    } catch (cause) {
      setError(errorMessage(cause));
    }
  };

  return (
    <>
      <Drawer title="服务器详情" suspended={scheduleNode !== null || enrollment !== null || editing || confirmingDelete} onClose={onClose}>
        <div className="drawer-header">
          <div><p className="eyebrow">服务器详情</p><h2>{currentMachine.name}</h2></div>
          <button className="icon-button" aria-label="关闭服务器详情" onClick={onClose}>×</button>
        </div>
        <div className="drawer-body">
          <section className="detail-section">
            <div className="section-heading"><h3>服务器状态</h3><StatusBadge machine={currentMachine} observedAt={observedAt} /></div>
            <p className="muted">SID: {currentMachine.id} • 最后心跳：{relativeTime(currentMachine.last_seen_at, observedAt)} • 节点数：{nodes.length}</p>
            <div className="action-group wrap">
              <button className="button secondary" onClick={() => setEditing(true)}>编辑信息</button>
              <button className="button ghost danger-text" onClick={() => setConfirmingDelete(true)}>删除服务器</button>
            </div>
          </section>
          {onNavigateNodes && <div className="action-group"><button className="button secondary" onClick={() => onNavigateNodes(machine.id, true)}>新增节点到此服务器</button><button className="button secondary" onClick={() => onNavigateNodes(machine.id, false)}>前往节点管理</button></div>}
          <div className="action-group" aria-label="趋势时间范围">{[1,6,12,24].map(hours => <button className="button compact secondary" aria-pressed={range === hours} key={hours} onClick={() => setRange(hours)}>{hours}h</button>)}</div><LoadPanel machine={currentMachine} history={history} />
          <section className="detail-section">
            <h3>安装 xboard-node</h3>
            <p className="muted">生成一次性接入码及安装命令，在此服务器上安装 xboard-node。接入码只展示一次，请妥善保存。</p>
            <button className="button secondary" onClick={() => void rotateEnrollment()}>生成新的接入命令</button>
          </section>
          <section className="detail-section">
            <div className="section-heading"><h3>关联节点</h3><span className="muted">总计 {nodes.length} · 已启用 {nodes.filter(node => node.enabled).length}</span></div>
            {error !== "" && <div className="alert error" role="alert">{error}</div>}
            {loading ? <p className="muted">正在加载节点…</p> : nodes.length === 0 ? <p className="muted">暂无关联节点。</p> : (
              <div className="machine-table-scroll"><table className="machine-linked-table"><thead><tr><th>名称</th><th>类型</th><th>地址</th><th>已激活</th><th>操作</th></tr></thead><tbody>
                {nodes.map((node) => (
                  <tr key={node.id}>
                    <td>{onNavigateNodes ? <button className="button ghost compact" onClick={() => onNavigateNodes(machine.id, false)}>{node.name}</button> : <strong>{node.name}</strong>}</td>
                    <td>{node.type}</td><td className="monospace">{node.host}:{node.port}</td>
                    <td>
                      <label className="switch-label">
                        <input
                          type="checkbox"
                          checked={node.enabled}
                          disabled={busyNodeID === node.id}
                          aria-label={`启用节点：${node.name}`}
                          onChange={() => void toggleNode(node)}
                        />
                        <span>{node.enabled ? "已启用" : "已停用"}</span>
                      </label></td><td>
                      <button className="button compact secondary" aria-label={`定时设置：${node.name}`} onClick={() => setScheduleNode(node)}>定时设置</button>
                      <button className="button compact ghost danger-text" disabled={busyNodeID === node.id} onClick={() => void unassign(node)}>解除关联</button>
                    </td>
                  </tr>
                ))}
              </tbody></table></div>
            )}
          </section>
          <section className="detail-section">
            <h3>关联已有节点</h3>
            <div className="inline-form">
              <select aria-label="待关联节点" value={selectedNodeID} onChange={(event) => setSelectedNodeID(event.target.value)}>
                <option value="">选择未关联节点</option>
                {unassigned.map((node) => <option key={node.id} value={node.id}>{node.name} ({node.type})</option>)}
              </select>
              <button className="button primary" disabled={selectedNodeID === "" || busyNodeID !== null} onClick={() => void assignSelected()}>关联</button>
            </div>
          </section>
        </div>
      </Drawer>
      {scheduleNode !== null && <ScheduleModal api={api} node={scheduleNode} onClose={() => setScheduleNode(null)} onSaved={() => void loadDetail()} />}
      {enrollment !== null && <EnrollmentModal enrollment={enrollment} onClose={() => setEnrollment(null)} />}
      {editing && (
        <EditMachineModal
          api={api}
          machine={currentMachine}
          onClose={() => setEditing(false)}
          onUpdated={(updated) => {
            setCurrentMachine(updated);
            setEditing(false);
            onChanged();
          }}
        />
      )}
      {confirmingDelete && (
        <DeleteMachineModal
          api={api}
          machine={currentMachine}
          onClose={() => setConfirmingDelete(false)}
          onDeleted={() => {
            setConfirmingDelete(false);
            onClose();
            onChanged();
          }}
        />
      )}
    </>
  );
}

function LoadPanel({ machine, history }: { machine: Machine; history: LoadHistory[] }) {
  const [metric, setMetric] = useState("CPU");
  const latest = history.at(-1);
  const cpu = machine.load_status?.cpu ?? latest?.cpu;
  const memoryTotal = machine.load_status?.mem.total ?? latest?.mem_total ?? 0;
  const memoryUsed = machine.load_status?.mem.used ?? latest?.mem_used ?? 0;
  const diskTotal = machine.load_status?.disk?.total ?? latest?.disk_total ?? 0;
  const diskUsed = machine.load_status?.disk?.used ?? latest?.disk_used ?? 0;
  const networkIn = machine.load_status?.net?.in_speed ?? latest?.net_in_speed;
  const networkOut = machine.load_status?.net?.out_speed ?? latest?.net_out_speed;
  const memoryPercent = percent(memoryUsed, memoryTotal);
  const diskPercent = percent(diskUsed, diskTotal);

  return (
    <section className="detail-section">
      <div className="section-heading"><h3>负载趋势</h3></div><div className="action-group">{["CPU","MEM","DISK","↓ IN","↑ OUT"].map(value => <button key={value} aria-pressed={metric === value} className="button compact secondary" onClick={() => setMetric(value)}>{value}</button>)}</div>{history.length > 1 && <TrendChart label={`${metric}负载趋势`} maximum={metric.includes("IN") || metric.includes("OUT") ? undefined : 100} series={[{ color: "var(--theme-primary)", values: history.map(h => metric === "CPU" ? h.cpu : metric === "MEM" ? percent(h.mem_used,h.mem_total) : metric === "DISK" ? percent(h.disk_used,h.disk_total) : metric === "↓ IN" ? h.net_in_speed : h.net_out_speed) }]} />}<h3>负载</h3>
      {cpu === undefined ? <p className="muted">机器尚未上报负载。</p> : (
        <>
          <div className="load-metrics">
            <LoadMetric label="CPU" value={`${cpu.toFixed(1)}%`} high={cpu >= 80} />
            <LoadMetric label="内存" value={`${memoryPercent.toFixed(1)}% · ${(memoryUsed / 1024 ** 3).toFixed(2)} / ${(memoryTotal / 1024 ** 3).toFixed(2)} GB`} high={memoryPercent >= 90} />
            <LoadMetric label="磁盘" value={`${diskPercent.toFixed(1)}% · ${(diskUsed / 1024 ** 3).toFixed(2)} / ${(diskTotal / 1024 ** 3).toFixed(2)} GB`} />
            <LoadMetric label="入站 / 出站" value={`${formatRate(networkIn)} / ${formatRate(networkOut)}`} />
          </div>
        </>
      )}
    </section>
  );
}

function LoadMetric({ label, value, high = false }: { label: string; value: string; high?: boolean }) {
  return <div className={`load-metric${high ? " high" : ""}`}><span>{label}</span><strong>{value}</strong></div>;
}

function TrendChart({ label, series, maximum }: { label: string; series: Array<{ values: number[]; color: string }>; maximum?: number }) {
  const width = 600;
  const height = 112;
  const allValues = series.flatMap((item) => item.values);
  const ceiling = maximum ?? Math.max(1, ...allValues);
  return (
    <svg className="trend-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={label} preserveAspectRatio="none">
      <line x1="0" y1={height / 2} x2={width} y2={height / 2} className="chart-gridline" />
      {series.map((item) => (
        <polyline key={item.color} fill="none" stroke={item.color} strokeWidth="3" vectorEffect="non-scaling-stroke" points={chartPoints(item.values, width, height, ceiling)} />
      ))}
    </svg>
  );
}

function chartPoints(values: number[], width: number, height: number, maximum: number): string {
  const divisor = Math.max(values.length - 1, 1);
  return values.map((value, index) => `${index / divisor * width},${height - Math.min(Math.max(value, 0), maximum) / maximum * height}`).join(" ");
}

function percent(used: number, total: number): number {
  return total > 0 ? used / total * 100 : 0;
}

function formatRate(value: number | undefined): string {
  if (value === undefined) return "—";
  if (value >= 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MiB/s`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KiB/s`;
  return `${value.toFixed(0)} B/s`;
}

function EditMachineModal({ api, machine, onClose, onUpdated }: { api: AdminAPI; machine: Machine; onClose: () => void; onUpdated: (machine: Machine) => void }) {
  const [name, setName] = useState(machine.name);
  const [notes, setNotes] = useState(machine.notes);
  const [isActive, setIsActive] = useState(machine.is_active);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      onUpdated(await api.updateMachine(machine.id, { name, notes, is_active: isActive }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };
  return (
    <Modal title="编辑服务器" onClose={onClose}>
      <ModalHeader title="编辑服务器" onClose={onClose} /><p className="muted">修改服务器名称、备注或启用状态。</p>
      <form className="form-stack" onSubmit={(event) => void submit(event)}>
        <label>服务器名称<input value={name} maxLength={255} required onChange={(event) => setName(event.target.value)} /></label>
        <label>备注<textarea value={notes} maxLength={4000} onChange={(event) => setNotes(event.target.value)} /></label>
        <label className="switch-label"><input type="checkbox" checked={isActive} onChange={(event) => setIsActive(event.target.checked)} />启用服务器</label><p className="muted">禁用后 xboard-node 将不再使用此服务器。</p>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="form-actions">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在更新…" : "更新"}</button>
        </div>
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
