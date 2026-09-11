import { useEffect, useMemo, useState } from "react";
import type { AdminAPI, AdminOrder, AdminUser, SystemStatus, Ticket } from "../../lib/api";

type Props = { api: AdminAPI };

const bytes = (value: number) => {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1);
  return `${(value / 1024 ** i).toFixed(i > 1 ? 2 : 0)} ${units[i]}`;
};

const money = (value: number) => `¥${value.toFixed(2)}`;

function IconRefresh() {
  return (
    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" /><path d="M3 3v5h5" />
      <path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16" /><path d="M16 21h5v-5" />
    </svg>
  );
}

function MetricIcon({ name }: { name: string }) {
  switch (name) {
    case "dollar":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><line x1="12" x2="12" y1="2" y2="22"/><path d="M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/></svg>;
    case "ticket":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2Z"/><path d="M13 5v2"/><path d="M13 17v2"/><path d="M13 11v2"/></svg>;
    case "commission":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="8" r="6"/><path d="M15.477 12.89 17 22l-5-3-5 3 1.523-9.11"/></svg>;
    case "user-plus":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><line x1="19" x2="19" y1="8" y2="14"/><line x1="22" x2="16" y1="11" y2="11"/></svg>;
    case "users":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>;
    case "upload":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m18 15-6-6-6 6"/><path d="M12 9v12"/></svg>;
    case "download":
      return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m6 9 6 6 6-6"/><path d="M12 3v12"/></svg>;
    default:
      return null;
  }
}

export function AdminDashboardPage({ api }: Props) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [orders, setOrders] = useState<AdminOrder[]>([]);
  const [nodes, setNodes] = useState<{ name: string; traffic_upload: number; traffic_download: number; online_count: number }[]>([]);
  const [tickets, setTickets] = useState<Ticket[]>([]);
  const [status, setStatus] = useState<SystemStatus | null>(null);
  const [updated, setUpdated] = useState(new Date());
  const [error, setError] = useState("");
  const [chartRange, setChartRange] = useState<"30" | "7">("30");
  const [chartMode, setChartMode] = useState<"amount" | "count">("amount");
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  const load = async () => {
    setError("");
    try {
      const [u, o, n, t, s] = await Promise.all([
        api.listAdminUsers({ page: 1, page_size: 50, sort_by: "created_at", sort_desc: true }),
        api.listAdminOrders({ page: 1, page_size: 50, sort_by: "created_at", sort_desc: true }),
        api.listAdminNodes({ page: 1, page_size: 50 }),
        api.listAdminTickets({ page: 1, page_size: 50 }),
        api.getSystemStatus()
      ]);
      setUsers(u.items);
      setOrders(o.items);
      setNodes(n.items.map((item) => ({ name: item.name, traffic_upload: item.traffic_upload, traffic_download: item.traffic_download, online_count: item.online_count })));
      setTickets(t.items);
      setStatus(s);
      setUpdated(new Date());
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "仪表盘数据加载失败");
    }
  };

  useEffect(() => { void load(); }, []);

  const now = Date.now();
  const day = 86_400_000;
  const month = now - 30 * day;
  const prevMonth = now - 60 * day;
  const paid = orders.filter((o) => o.status === 3 || o.status === 1);
  const monthOrders = paid.filter((o) => new Date(o.paid_at ?? o.created_at).getTime() >= month);
  const prevOrders = paid.filter((o) => {
    const time = new Date(o.paid_at ?? o.created_at).getTime();
    return time >= prevMonth && time < month;
  });
  const total = monthOrders.reduce((sum, o) => sum + o.total_amount, 0);
  const previous = prevOrders.reduce((sum, o) => sum + o.total_amount, 0);
  const upload = users.reduce((sum, u) => sum + u.traffic_upload, 0);
  const download = users.reduce((sum, u) => sum + u.traffic_download, 0);
  const active = users.filter((u) => u.online_count > 0).length;
  const pendingTickets = tickets.filter((t) => t.status !== 1).length;
  const pendingCommission = tickets.filter((t) => t.withdrawal?.status === "pending").length;
  const nodeRank = [...nodes].sort((a, b) => b.traffic_upload + b.traffic_download - a.traffic_upload - a.traffic_download).slice(0, 10);
  const userRank = useMemo(() => [...users].sort((a, b) => b.traffic_used - a.traffic_used).slice(0, 10), [users]);

  // Chart data aggregation
  const chartDays = chartRange === "30" ? 30 : 7;
  const chartData = useMemo(() => {
    const points: { date: string; label: string; amount: number; count: number }[] = [];
    const dateMap = new Map<string, { amount: number; count: number }>();
    for (let i = chartDays - 1; i >= 0; i--) {
      const d = new Date(now - i * day);
      const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
      const label = `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
      dateMap.set(key, { amount: 0, count: 0 });
      points.push({ date: key, label, amount: 0, count: 0 });
    }
    for (const order of paid) {
      const d = new Date(order.paid_at ?? order.created_at);
      const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
      const slot = dateMap.get(key);
      if (slot) {
        slot.amount += order.total_amount;
        slot.count += 1;
      }
    }
    for (const p of points) {
      const val = dateMap.get(p.date);
      if (val) {
        p.amount = val.amount;
        p.count = val.count;
      }
    }
    return points;
  }, [paid, chartDays, now]);

  const chartTotals = useMemo(() => {
    const totAmount = chartData.reduce((s, p) => s + p.amount, 0);
    const totCount = chartData.reduce((s, p) => s + p.count, 0);
    return { amount: totAmount, count: totCount };
  }, [chartData]);

  // Compute SVG coordinates
  const svgWidth = 800;
  const svgHeight = 220;
  const paddingX = 40;
  const paddingY = 25;
  const graphWidth = svgWidth - paddingX * 2;
  const graphHeight = svgHeight - paddingY * 2;

  const maxVal = Math.max(...chartData.map((d) => (chartMode === "amount" ? d.amount : d.count)), 1);

  const coords = useMemo(() => {
    return chartData.map((d, idx) => {
      const val = chartMode === "amount" ? d.amount : d.count;
      const x = paddingX + (idx / Math.max(chartData.length - 1, 1)) * graphWidth;
      const y = paddingY + graphHeight - (val / maxVal) * graphHeight;
      return { x, y, data: d };
    });
  }, [chartData, chartMode, maxVal, graphWidth, graphHeight]);

  const splinePath = useMemo(() => {
    if (coords.length === 0) return "";
    let path = `M ${coords[0]!.x} ${coords[0]!.y}`;
    for (let i = 0; i < coords.length - 1; i++) {
      const current = coords[i]!;
      const next = coords[i + 1]!;
      const cx = (current.x + next.x) / 2;
      path += ` C ${cx} ${current.y}, ${cx} ${next.y}, ${next.x} ${next.y}`;
    }
    return path;
  }, [coords]);

  const areaPath = useMemo(() => {
    if (coords.length === 0) return "";
    const bottomY = paddingY + graphHeight;
    const first = coords[0]!;
    const last = coords[coords.length - 1]!;
    return `${splinePath} L ${last.x} ${bottomY} L ${first.x} ${bottomY} Z`;
  }, [splinePath, coords, graphHeight]);

  return (
    <main className="page-shell admin-dashboard">
      <header className="page-header">
        <div>
          <p className="eyebrow">Dashboard</p>
          <h1>仪表盘</h1>
          <p className="muted">业务、节点、用户和队列的运行概览。</p>
        </div>
        <button className="button secondary dashboard-refresh-btn" onClick={() => void load()}>
          <IconRefresh />
          <span>立即刷新</span>
        </button>
      </header>

      {error && <div className="alert error" role="alert">{error}</div>}

      {/* 实时状态卡片 */}
      <section className="dashboard-realtime-card">
        <div className="realtime-header">
          <div>
            <h2>实时状态</h2>
            <p className="muted">基于节点最近上报的全局在线情况</p>
          </div>
          <div className="realtime-header-right">
            <span className="live-status-pill">
              <span className="live-pulse-dot" />
              实时 · {updated.toLocaleTimeString("zh-CN", { hour12: false })}
            </span>
          </div>
        </div>
        <div className="dashboard-realtime-grid">
          <div className="realtime-counter-card">
            <div className="counter-head">
              <span>在线设备</span>
              <span className="counter-icon-dot" />
            </div>
            <strong className="counter-value">
              {nodes.reduce((sum, n) => sum + n.online_count, 0)}
            </strong>
            <small className="muted">按节点心跳汇总</small>
          </div>
          <div className="realtime-counter-card">
            <div className="counter-head">
              <span>在线用户</span>
              <span className="counter-icon-dot" />
            </div>
            <strong className="counter-value">{active}</strong>
            <small className="muted">当前有在线设备</small>
          </div>
          <div className="realtime-counter-card">
            <div className="counter-head">
              <span>在线节点</span>
              <span className="counter-icon-dot" />
            </div>
            <strong className="counter-value">
              {nodes.filter((n) => n.online_count > 0).length}
            </strong>
            <small className="muted">共 {nodes.length} 个节点</small>
          </div>
        </div>
      </section>

      {/* 8 个核心运营卡片 */}
      <section className="dashboard-metrics-grid">
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>今日收入</span>
            <MetricIcon name="dollar" />
          </div>
          <strong className="card-val">
            {money(monthOrders.filter((o) => new Date(o.paid_at ?? o.created_at).toDateString() === new Date().toDateString()).reduce((s, o) => s + o.total_amount, 0))}
          </strong>
          <small className="muted">已支付订单</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>月收入</span>
            <MetricIcon name="dollar" />
          </div>
          <strong className="card-val">{money(total)}</strong>
          <small className="muted">较上月 {previous ? `${((total - previous) / previous * 100).toFixed(1)}%` : "—"}</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>待处理工单</span>
            <MetricIcon name="ticket" />
          </div>
          <strong className={`card-val ${pendingTickets > 0 ? "warning-val" : ""}`}>
            {pendingTickets}
          </strong>
          <small className="muted">{pendingTickets ? "需要处理" : "无待处理工单"}</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>待处理佣金</span>
            <MetricIcon name="commission" />
          </div>
          <strong className={`card-val ${pendingCommission > 0 ? "warning-val" : ""}`}>
            {pendingCommission}
          </strong>
          <small className="muted">{pendingCommission ? "需要审核" : "无待处理佣金"}</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>月新增用户</span>
            <MetricIcon name="user-plus" />
          </div>
          <strong className="card-val">
            {users.filter((u) => new Date(u.created_at).getTime() >= month).length}
          </strong>
          <small className="muted">最近 30 天</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>总用户</span>
            <MetricIcon name="users" />
          </div>
          <strong className="card-val">{users.length}</strong>
          <small className="muted">活跃用户 {active}</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>月上传</span>
            <MetricIcon name="upload" />
          </div>
          <strong className="card-val">{bytes(upload)}</strong>
          <small className="muted">今日累计 {bytes(upload)}</small>
        </div>
        <div className="legacy-metric-card">
          <div className="card-top">
            <span>月下载</span>
            <MetricIcon name="download" />
          </div>
          <strong className="card-val">{bytes(download)}</strong>
          <small className="muted">今日累计 {bytes(download)}</small>
        </div>
      </section>

      {/* 收入概览交互图表 */}
      <section className="dashboard-chart-card">
        <div className="chart-header">
          <div>
            <h2>收入概览</h2>
          </div>
          <div className="chart-controls">
            <span className="chart-total-tag">
              {chartMode === "amount" ? `总金额: ${money(chartTotals.amount)}` : `总订单数: ${chartTotals.count}`}
            </span>
            <div className="segmented-control">
              <button
                type="button"
                className={`segment-btn ${chartMode === "amount" ? "active" : ""}`}
                onClick={() => setChartMode("amount")}
              >
                金额
              </button>
              <button
                type="button"
                className={`segment-btn ${chartMode === "count" ? "active" : ""}`}
                onClick={() => setChartMode("count")}
              >
                数量
              </button>
            </div>
            <select
              className="chart-range-select"
              value={chartRange}
              onChange={(e) => setChartRange(e.target.value as "30" | "7")}
            >
              <option value="30">最近30天</option>
              <option value="7">最近7天</option>
            </select>
          </div>
        </div>

        <div className="chart-body">
          <svg className="spline-chart-svg" viewBox={`0 0 ${svgWidth} ${svgHeight}`} preserveAspectRatio="none">
            <defs>
              <linearGradient id="chartGradient" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--theme-primary)" stopOpacity="0.45" />
                <stop offset="100%" stopColor="var(--theme-primary)" stopOpacity="0.02" />
              </linearGradient>
            </defs>
            {/* Gridlines */}
            <line x1={paddingX} y1={paddingY} x2={svgWidth - paddingX} y2={paddingY} stroke="var(--theme-border)" strokeDasharray="3 3" opacity="0.4" />
            <line x1={paddingX} y1={paddingY + graphHeight / 2} x2={svgWidth - paddingX} y2={paddingY + graphHeight / 2} stroke="var(--theme-border)" strokeDasharray="3 3" opacity="0.4" />
            <line x1={paddingX} y1={paddingY + graphHeight} x2={svgWidth - paddingX} y2={paddingY + graphHeight} stroke="var(--theme-border)" opacity="0.6" />

            {/* Area & Line */}
            {areaPath && <path d={areaPath} fill="url(#chartGradient)" />}
            {splinePath && <path d={splinePath} fill="none" stroke="var(--theme-primary)" strokeWidth="2.5" strokeLinecap="round" />}

            {/* Hover Points */}
            {coords.map((c, i) => (
              <circle
                key={c.data.date}
                cx={c.x}
                cy={c.y}
                r={hoverIndex === i ? 5 : 3}
                fill={hoverIndex === i ? "var(--theme-primary)" : "var(--theme-surface)"}
                stroke="var(--theme-primary)"
                strokeWidth="2"
                onMouseEnter={() => setHoverIndex(i)}
                onMouseLeave={() => setHoverIndex(null)}
                style={{ cursor: "pointer", transition: "r 0.15s ease" }}
              />
            ))}
          </svg>

          {/* X Axis Labels */}
          <div className="chart-x-axis">
            {coords.filter((_, idx) => idx % Math.ceil(coords.length / 6) === 0 || idx === coords.length - 1).map((c) => (
              <span key={c.data.date} style={{ left: `${(c.x / svgWidth) * 100}%` }}>
                {c.data.label}
              </span>
            ))}
          </div>

          {/* Hover Tooltip */}
          {hoverIndex !== null && coords[hoverIndex] && (
            <div
              className="chart-tooltip"
              style={{
                left: `${(coords[hoverIndex]!.x / svgWidth) * 100}%`,
                top: `${(coords[hoverIndex]!.y / svgHeight) * 100}%`
              }}
            >
              <strong>{coords[hoverIndex]!.data.date}</strong>
              <span>
                {chartMode === "amount" ? money(coords[hoverIndex]!.data.amount) : `${coords[hoverIndex]!.data.count} 笔`}
              </span>
            </div>
          )}
        </div>
      </section>

      {/* 排行榜 */}
      <div className="dashboard-columns">
        <Rank title="节点流量排行" items={nodeRank.map((n) => ({ label: n.name, value: bytes(n.traffic_upload + n.traffic_download) }))} />
        <Rank title="用户流量排行" items={userRank.map((u) => ({ label: u.email, value: bytes(u.traffic_used) }))} />
      </div>

      {/* 队列状态 */}
      <section className="dashboard-queue">
        <h2>队列状态</h2>
        <div className="dashboard-queue-grid">
          <Metric label="调度器" value={status?.scheduler.healthy ? "正常" : "异常"} hint={status ? `运行 ${Math.floor(status.uptime_seconds / 60)} 分钟` : "加载中"} tone={status?.scheduler.healthy ? "good" : "danger"} />
          <Metric label="邮件队列" value={String(status?.mail_queue.pending ?? 0)} hint={`失败 ${status?.mail_queue.failed ?? 0}`} />
          <Metric label="Telegram 队列" value={String(status?.telegram_queue.pending ?? 0)} hint={`失败 ${status?.telegram_queue.failed ?? 0}`} />
          <Metric label="近期任务" value={String((status?.mail_queue.sent ?? 0) + (status?.telegram_queue.sent ?? 0))} hint="已发送任务" />
        </div>
      </section>
    </main>
  );
}

function Metric({ label, value, hint, tone = "" }: { label: string; value: string; hint?: string; tone?: string }) {
  return (
    <article className={`overview-metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {hint && <small>{hint}</small>}
    </article>
  );
}

function Rank({ title, items }: { title: string; items: { label: string; value: string }[] }) {
  const max = Math.max(...items.map((i) => Number.parseFloat(i.value) || 1), 1);
  return (
    <section className="dashboard-rank">
      <h2>{title}</h2>
      {items.length === 0 ? (
        <p className="muted">暂无数据</p>
      ) : (
        items.map((item) => (
          <div className="dashboard-rank-row" key={item.label}>
            <span title={item.label}>{item.label}</span>
            <b>{item.value}</b>
            <i style={{ width: `${Math.max(8, (Number.parseFloat(item.value) || 0) / max * 100)}%` }} />
          </div>
        ))
      )}
    </section>
  );
}
