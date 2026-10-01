import "./TrafficResetModal.css";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Modal } from "../../components/Overlay";
import type { TrafficResetAPI, TrafficResetFilters, TrafficResetPage, TrafficResetStats } from "../../lib/api";

export function TrafficResetModal({api,onClose}: {api: TrafficResetAPI; onClose: () => void}) {
  const [filters,setFilters] = useState<TrafficResetFilters>({});
  const [applied,setApplied] = useState<TrafficResetFilters>({});
  const [page,setPage] = useState<TrafficResetPage | null>(null);
  const [stats,setStats] = useState<TrafficResetStats | null>(null);
  const [days,setDays] = useState(30);
  const [busy,setBusy] = useState(false); const [error,setError] = useState(""); const sequence = useRef(0);
  async function load(next: number, nextFilters = applied, nextDays = days) {
    const request = ++sequence.current; setBusy(true); setError("");
    try {
      const [data,counts] = await Promise.all([api.listGlobalTrafficResets(nextFilters,next),api.getTrafficResetStats(nextDays)]);
      if (request === sequence.current) {setPage(data);setStats(counts);setApplied(nextFilters);}
    } catch(cause) {if(request === sequence.current) setError(cause instanceof Error ? cause.message : "加载流量重置记录失败");}
    finally {if(request === sequence.current) setBusy(false);}
  }
  useEffect(() => {
    let active = true; const request = ++sequence.current;
    const invalidate = () => {sequence.current++;};
    void Promise.all([api.listGlobalTrafficResets({},1),api.getTrafficResetStats(30)]).then(([data,counts]) => {if(active && sequence.current === request){setPage(data);setStats(counts);}}).catch((cause: unknown) => {if(active && sequence.current === request) setError(cause instanceof Error ? cause.message : "加载失败");});
    return () => {active=false;invalidate();};
  },[api]);
  function submit(event: FormEvent) {event.preventDefault();void load(1,filters,days);}
  const field = (key: keyof TrafficResetFilters,value: string) => setFilters({...filters,[key]:value});
  return <Modal title="流量重置记录" onClose={onClose} className="traffic-reset-modal">
    <h2>流量重置记录</h2>
    <form onSubmit={submit} className="resource-form">
      <div className="form-grid">
        <label>用户 ID<input type="number" min="1" value={filters.user_id ?? ""} onChange={e => field("user_id",e.target.value)} /></label>
        <label>用户邮箱<input type="search" maxLength={320} value={filters.user_email ?? ""} onChange={e => field("user_email",e.target.value)} /></label>
        <label>重置类型<select value={filters.reset_type ?? ""} onChange={e => field("reset_type",e.target.value)}><option value="">全部类型</option>{Object.entries({manual:"手动重置",monthly:"按月重置",first_day_month:"每月1号重置",yearly:"按年重置",first_day_year:"每年1月1日重置",purchase:"购买重置"}).map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <label>触发来源<select value={filters.trigger_source ?? ""} onChange={e => field("trigger_source",e.target.value)}><option value="">全部来源</option>{Object.entries({manual:"手动触发",cron:"定时任务",auto:"自动触发",api:"API",user_access:"用户访问",order:"订单",gift_card:"礼品卡"}).map(([value,label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <label>开始日期<input type="date" value={filters.start_date ?? ""} onChange={e => field("start_date",e.target.value)} /></label>
        <label>结束日期<input type="date" min={filters.start_date} value={filters.end_date ?? ""} onChange={e => field("end_date",e.target.value)} /></label>
        <label>统计天数<input type="number" min={1} max={365} required value={days} onChange={e => setDays(Number(e.target.value))} /></label>
      </div>
      <button className="button primary" disabled={busy}>{busy ? "查询中…" : "查询记录"}</button>
    </form>
    {error && <div role="alert" className="alert error">{error}</div>}
    {stats && <p aria-label="全局统计">全局统计（按统计天数，不受列表筛选影响）：共 {stats.total_resets} 次 · 手动 {stats.manual_resets} · 定时 {stats.cron_resets} · 自动 {stats.auto_resets}</p>}
    {!page && !error && <p role="status">正在加载记录…</p>}
    <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>用户</th><th>重置类型</th><th>来源</th><th>重置前</th><th>重置后</th><th>时间</th><th>备注</th></tr></thead><tbody>
      {page?.data.map(row => <tr key={row.id}><td>{row.user_email}<small> #{row.user_id}</small></td><td>{row.reset_type_name}</td><td>{row.trigger_source_name}</td><td>{row.old_traffic.formatted}</td><td>{row.new_traffic.formatted}</td><td>{new Date(row.reset_time).toLocaleString("zh-CN")}</td><td>{row.metadata?.reason || "—"}</td></tr>)}
      {page?.data.length === 0 && <tr><td colSpan={7}>暂无匹配的重置记录。</td></tr>}
    </tbody></table></div>
    {page && <div className="pagination-footer"><button className="button secondary" disabled={busy || page.pagination.current_page <= 1} onClick={() => void load(page.pagination.current_page-1)}>上一页</button><span>第 {page.pagination.current_page} / {page.pagination.last_page} 页 · 共 {page.pagination.total} 条</span><button className="button secondary" disabled={busy || page.pagination.current_page >= page.pagination.last_page} onClick={() => void load(page.pagination.current_page+1)}>下一页</button></div>}
    <button className="button secondary" onClick={onClose}>关闭</button>
  </Modal>;
}
