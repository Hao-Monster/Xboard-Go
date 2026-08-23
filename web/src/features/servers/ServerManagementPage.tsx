import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Drawer, Modal } from "../../components/Overlay";
import type { ActivationSchedule, AdminAPI, DailyScheduleInput, LoadHistory, Machine, MachineEnrollment, Node } from "../../lib/api";

interface Props {
  api: AdminAPI;
}

export function ServerManagementPage({ api }: Props) {
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
  const [loadFilter, setLoadFilter] = useState("all");
  const [sortBy, setSortBy] = useState("id");

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
    const matchesLoad = loadFilter === "all" || isHighLoad(machine);
    return matchesQuery && matchesStatus && matchesLinked && matchesLoad;
  }).sort((left, right) => {
    if (sortBy === "name") return left.name.localeCompare(right.name, "zh-CN");
    if (sortBy === "load") return machineLoad(right) - machineLoad(left);
    return left.id - right.id;
  });
  const onlineCount = machines.filter((machine) => machineStatus(machine, observedAt) === "online").length;
  const inactiveCount = machines.filter((machine) => !machine.is_active).length;
  const highLoadCount = machines.filter(isHighLoad).length;
  const nodeCount = machines.reduce((total, machine) => total + machine.servers_count, 0);

  return (
    <main className="page-shell">
      <header className="page-header">
        <div>
          <p className="eyebrow">基础设施</p>
          <h1>服务器管理</h1>
          <p className="muted">管理节点机器、关联节点和每日激活计划。</p>
        </div>
        <button className="button primary" onClick={() => setCreating(true)}>新增服务器</button>
      </header>

      <section className="overview-grid" aria-label="服务器概览">
        <OverviewMetric label="服务器" value={machines.length} />
        <OverviewMetric label="承载节点" value={nodeCount} />
        <OverviewMetric label="在线" value={onlineCount} tone="good" />
        <OverviewMetric label="离线或失联" value={machines.length - onlineCount - inactiveCount} tone="warning" />
        <OverviewMetric label="高负载" value={highLoadCount} tone={highLoadCount > 0 ? "danger" : "neutral"} hint="CPU ≥ 80% 或内存 ≥ 90%" />
      </section>

      <section className="filter-bar" aria-label="服务器筛选">
        <label className="search-field">搜索<input type="search" placeholder="名称、备注或 ID" value={query} onChange={(event) => setQuery(event.target.value)} /></label>
        <label>状态<select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="all">全部</option><option value="online">在线</option><option value="offline">离线</option><option value="inactive">已停用</option></select></label>
        <label>承载节点<select value={linkedFilter} onChange={(event) => setLinkedFilter(event.target.value)}><option value="all">全部</option><option value="yes">有节点</option><option value="no">无节点</option></select></label>
        <label>负载<select value={loadFilter} onChange={(event) => setLoadFilter(event.target.value)}><option value="all">全部</option><option value="high">高负载</option></select></label>
        <label>排序<select value={sortBy} onChange={(event) => setSortBy(event.target.value)}><option value="id">ID</option><option value="name">名称</option><option value="load">负载</option></select></label>
        <button className="button ghost compact" onClick={() => { setQuery(""); setStatusFilter("all"); setLinkedFilter("all"); setLoadFilter("all"); setSortBy("id"); }}>重置</button>
      </section>

      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      {loading ? (
        <div className="empty-card" aria-live="polite">正在加载服务器…</div>
      ) : machines.length === 0 ? (
        <div className="empty-card">尚未添加服务器。</div>
      ) : filteredMachines.length === 0 ? (
        <div className="empty-card">没有符合当前筛选条件的服务器。</div>
      ) : (
        <section className="machine-grid" aria-label="服务器列表">
          {filteredMachines.map((machine) => (
            <article className="machine-card" key={machine.id}>
              <div className="card-heading">
                <div>
                  <h2>{machine.name}</h2>
                  <p className="muted monospace">#{machine.id}</p>
                </div>
                <StatusBadge machine={machine} observedAt={observedAt} />
              </div>
              <p className="card-notes">{machine.notes || "暂无备注"}</p>
              <dl className="metrics">
                <div><dt>关联节点</dt><dd>{machine.servers_count}</dd></div>
                <div><dt>最后在线</dt><dd>{formatLastSeen(machine.last_seen_at)}</dd></div>
              </dl>
              <button className="button secondary full" onClick={() => setDetailMachine(machine)}>服务器详情</button>
            </article>
          ))}
        </section>
      )}

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
          observedAt={observedAt}
          onClose={() => setDetailMachine(null)}
          onChanged={() => void refresh()}
        />
      )}
    </main>
  );
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
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    setError("");
    try {
      onCreated(await api.createMachine({ name, notes, is_active: true }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal title="新增服务器" onClose={onClose}>
      <ModalHeader title="新增服务器" onClose={onClose} />
      <form className="form-stack" onSubmit={(event) => void submit(event)}>
        <label>服务器名称<input value={name} maxLength={255} required onChange={(event) => setName(event.target.value)} /></label>
        <label>备注<textarea value={notes} maxLength={4000} onChange={(event) => setNotes(event.target.value)} /></label>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="form-actions">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" type="submit" disabled={submitting}>{submitting ? "正在创建…" : "创建服务器"}</button>
        </div>
      </form>
    </Modal>
  );
}

function MachineDetailDrawer({ api, machine, observedAt, onClose, onChanged }: { api: AdminAPI; machine: Machine; observedAt: number; onClose: () => void; onChanged: () => void }) {
	const [currentMachine, setCurrentMachine] = useState(machine);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [unassigned, setUnassigned] = useState<Node[]>([]);
  const [history, setHistory] = useState<LoadHistory[]>([]);
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
      const [linked, available, loadHistory] = await Promise.all([api.listMachineNodes(machine.id), api.listUnassignedNodes(), api.listLoadHistory(machine.id)]);
      setNodes(linked);
      setUnassigned(available);
      setHistory(loadHistory);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api, machine.id]);

  useEffect(() => {
    let live = true;
    void Promise.all([api.listMachineNodes(machine.id), api.listUnassignedNodes(), api.listLoadHistory(machine.id)]).then(([linked, available, loadHistory]) => {
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
  }, [api, machine.id]);

  const toggleNode = async (node: Node) => {
    setBusyNodeID(node.id);
    setError("");
    try {
      await api.setNodeEnabled(machine.id, node.id, !node.enabled);
      setNodes((current) => current.map((item) => item.id === node.id ? { ...item, enabled: !item.enabled } : item));
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
      await api.assignNode(machine.id, nodeID);
      setSelectedNodeID("");
      await loadDetail();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusyNodeID(null);
    }
  };

  const unassign = async (nodeID: number) => {
    setBusyNodeID(nodeID);
    try {
      await api.unassignNode(machine.id, nodeID);
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
            <div className="section-heading"><h3>机器信息</h3><StatusBadge machine={currentMachine} observedAt={observedAt} /></div>
            <p className="muted">{currentMachine.notes || "暂无备注"}</p>
            <div className="action-group wrap">
              <button className="button secondary" onClick={() => setEditing(true)}>编辑信息</button>
              <button className="button secondary" onClick={() => void rotateEnrollment()}>生成新的接入命令</button>
              <button className="button ghost danger-text" onClick={() => setConfirmingDelete(true)}>删除服务器</button>
            </div>
          </section>
          <LoadPanel machine={currentMachine} history={history} />
          <section className="detail-section">
            <div className="section-heading"><h3>关联节点</h3><span className="count-pill">{nodes.length}</span></div>
            {error !== "" && <div className="alert error" role="alert">{error}</div>}
            {loading ? <p className="muted">正在加载节点…</p> : nodes.length === 0 ? <p className="muted">暂无关联节点。</p> : (
              <div className="node-list">
                {nodes.map((node) => (
                  <article className="node-row" key={node.id}>
                    <div className="node-copy">
                      <strong>{node.name}</strong>
                      <span className="muted monospace">{node.type} · {node.host}:{node.port}</span>
                    </div>
                    <div className="node-actions">
                      <label className="switch-label">
                        <input
                          type="checkbox"
                          checked={node.enabled}
                          disabled={busyNodeID === node.id}
                          aria-label={`启用节点：${node.name}`}
                          onChange={() => void toggleNode(node)}
                        />
                        <span>{node.enabled ? "已启用" : "已停用"}</span>
                      </label>
                      <button className="button compact secondary" aria-label={`定时设置：${node.name}`} onClick={() => setScheduleNode(node)}>定时设置</button>
                      <button className="button compact ghost danger-text" disabled={busyNodeID === node.id} onClick={() => void unassign(node.id)}>解除关联</button>
                    </div>
                  </article>
                ))}
              </div>
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
      <div className="section-heading"><h3>负载与网络</h3><span className="muted small">最近 1 小时</span></div>
      {cpu === undefined ? <p className="muted">机器尚未上报负载。</p> : (
        <>
          <div className="load-metrics">
            <LoadMetric label="CPU" value={`${cpu.toFixed(1)}%`} high={cpu >= 80} />
            <LoadMetric label="内存" value={`${memoryPercent.toFixed(1)}%`} high={memoryPercent >= 90} />
            <LoadMetric label="磁盘" value={`${diskPercent.toFixed(1)}%`} />
            <LoadMetric label="入站 / 出站" value={`${formatRate(networkIn)} / ${formatRate(networkOut)}`} />
          </div>
          {history.length > 1 && (
            <div className="charts-grid">
              <TrendChart
                label="CPU（蓝）和内存（绿）趋势"
                series={[
                  { values: history.map((item) => item.cpu), color: "#9ab2ff" },
                  { values: history.map((item) => percent(item.mem_used, item.mem_total)), color: "#78e5cb" }
                ]}
                maximum={100}
              />
              <TrendChart
                label="网络入站（蓝）和出站（绿）趋势"
                series={[
                  { values: history.map((item) => item.net_in_speed), color: "#9ab2ff" },
                  { values: history.map((item) => item.net_out_speed), color: "#78e5cb" }
                ]}
              />
            </div>
          )}
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
      <ModalHeader title="编辑服务器" onClose={onClose} />
      <form className="form-stack" onSubmit={(event) => void submit(event)}>
        <label>服务器名称<input value={name} maxLength={255} required onChange={(event) => setName(event.target.value)} /></label>
        <label>备注<textarea value={notes} maxLength={4000} onChange={(event) => setNotes(event.target.value)} /></label>
        <label className="switch-label"><input type="checkbox" checked={isActive} onChange={(event) => setIsActive(event.target.checked)} />允许机器接入</label>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="form-actions">
          <button className="button ghost" type="button" onClick={onClose}>取消</button>
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存修改"}</button>
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

function formatLastSeen(value: string | null): string {
  if (value === null) return "从未上报";
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value));
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
