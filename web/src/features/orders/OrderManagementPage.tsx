import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminOrderDetail, AdminOrderPage, AdminOrderQuery, AssignOrderInput, Order, OrderStatus, OrderType, Plan, PlanPeriod } from "../../lib/api";
import { formatCents, formatDate, orderStatusLabel, orderTypeLabel, periodLabel } from "./UserOrdersPage";

export interface OrderManagementAPI {
  listAdminOrders: (query?: AdminOrderQuery) => Promise<AdminOrderPage>;
  getAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
  assignOrder: (input: AssignOrderInput) => Promise<Order>;
  paidAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
  cancelAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
	updateAdminOrderCommissionStatus: (tradeNo: string, status: 0 | 1 | 3) => Promise<AdminOrderDetail>;
  listPlans: () => Promise<Plan[]>;
}

const periods: Array<[PlanPeriod, string]> = [["monthly", "月付"], ["quarterly", "季付"], ["half_yearly", "半年付"], ["yearly", "年付"], ["two_yearly", "两年付"], ["three_yearly", "三年付"], ["onetime", "一次性"], ["reset_traffic", "流量重置包"]];
const statusOptions: Array<[OrderStatus, string]> = [[0, "待支付"], [1, "开通中"], [2, "已取消"], [3, "已完成"], [4, "已折抵"]];
const typeOptions: Array<[OrderType, string]> = [[1, "新购"], [2, "续费"], [3, "升级"], [4, "流量重置"]];
const commissionOptions: Array<[0 | 1 | 2 | 3, string]> = [[0, "待确认"], [1, "发放中"], [2, "有效"], [3, "无效"]];

export function OrderManagementPage({ api }: { api: OrderManagementAPI }) {
  const [page, setPage] = useState<AdminOrderPage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [plans, setPlans] = useState<Plan[]>([]);
  const [search, setSearch] = useState("");
	const [statuses, setStatuses] = useState<OrderStatus[]>([]);
	const [types, setTypes] = useState<OrderType[]>([]);
	const [selectedPeriods, setSelectedPeriods] = useState<PlanPeriod[]>([]);
	const [commissionStatuses, setCommissionStatuses] = useState<Array<0 | 1 | 2 | 3>>([]);
  const [openFilter, setOpenFilter] = useState<string | null>(null);
  const [applied, setApplied] = useState<AdminOrderQuery>({ page: 1, page_size: 20 });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [assigning, setAssigning] = useState(false);
	const [selected, setSelected] = useState<AdminOrderDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = useCallback(async (query: AdminOrderQuery) => {
    setLoading(true);
    setError("");
    try {
      setPage(await api.listAdminOrders(query));
      setApplied(query);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    const query = { page: 1, page_size: 20 } satisfies AdminOrderQuery;
    void Promise.all([api.listAdminOrders(query), api.listPlans()]).then(([orders, nextPlans]) => {
      if (!active) return;
      setPage(orders);
      setPlans(nextPlans);
      setApplied(query);
    }).catch((cause: unknown) => { if (active) setError(messageOf(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api]);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setOpenFilter(null);
    const query: AdminOrderQuery = { page: 1, page_size: 20 };
    if (search.trim() !== "") query.query = search.trim();
		if (statuses.length > 0) query.statuses = statuses;
		if (types.length > 0) query.types = types;
		if (selectedPeriods.length > 0) query.periods = selectedPeriods;
		if (commissionStatuses.length > 0) query.commission_statuses = commissionStatuses;
    void load(query);
  };

  const open = async (tradeNo: string) => {
    setDetailLoading(true);
    setError("");
    try { setSelected(await api.getAdminOrder(tradeNo)); } catch (cause) { setError(messageOf(cause)); } finally { setDetailLoading(false); }
  };

  const refreshCurrent = () => load(applied);
	const sort = (field: NonNullable<AdminOrderQuery["sort_by"]>) => {
		const descending = applied.sort_by === field ? !applied.sort_desc : false;
		void load({ ...applied, page: 1, sort_by: field, sort_desc: descending });
	};
	const selectMobileSort = (field: AdminOrderQuery["sort_by"] | "") => {
		if (field === "") {
			const query = { ...applied, page: 1 };
			delete query.sort_by;
			delete query.sort_desc;
			void load(query);
			return;
		}
		void load({ ...applied, page: 1, sort_by: field, sort_desc: false });
	};
	const changeOpenFilter = (label: string, open: boolean) => setOpenFilter((current) => open ? label : current === label ? null : current);

  return <main className="page-shell resource-page order-page">
    <header className="page-header"><div><p className="eyebrow">Finance</p><h1>订单管理</h1><p className="muted">查询订单、为用户分配订阅，并对待支付订单执行人工开通或取消。</p></div><button className="button primary" onClick={() => setAssigning(true)}>添加订单</button></header>
    <form className="ticket-filter-bar order-filter-bar" onSubmit={submit}>
			<label className="search-field">搜索订单<input type="search" aria-label="搜索订单" placeholder="订单号或用户邮箱" value={search} maxLength={128} onChange={(event) => setSearch(event.target.value)} /></label>
			<MultiFilter label="订单状态" options={statusOptions} values={statuses} onChange={setStatuses} open={openFilter === "订单状态"} onOpenChange={(open) => changeOpenFilter("订单状态", open)} />
			<MultiFilter label="订单类型" options={typeOptions} values={types} onChange={setTypes} open={openFilter === "订单类型"} onOpenChange={(open) => changeOpenFilter("订单类型", open)} />
			<MultiFilter label="付款周期" options={periods} values={selectedPeriods} onChange={setSelectedPeriods} open={openFilter === "付款周期"} onOpenChange={(open) => changeOpenFilter("付款周期", open)} />
			<MultiFilter label="佣金状态" options={commissionOptions} values={commissionStatuses} onChange={setCommissionStatuses} open={openFilter === "佣金状态"} onOpenChange={(open) => changeOpenFilter("佣金状态", open)} />
			<div className="mobile-order-sort">
				<label>排序字段<select aria-label="订单排序字段" value={applied.sort_by ?? ""} onChange={(event) => selectMobileSort(event.target.value as AdminOrderQuery["sort_by"] | "")}>
					<option value="">默认（最新创建）</option><option value="total_amount">订单金额</option><option value="status">订单状态</option><option value="commission_balance">佣金金额</option><option value="commission_status">佣金状态</option><option value="created_at">创建时间</option>
				</select></label>
				<button className="button secondary compact" type="button" aria-label="切换订单排序方向" disabled={applied.sort_by === undefined} onClick={() => { if (applied.sort_by !== undefined) sort(applied.sort_by); }}>{applied.sort_desc === true ? "降序" : "升序"}</button>
			</div>
			<button className="button secondary" type="submit" disabled={loading}>查询订单</button>
		</form>
    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void refreshCurrent()}>重试</button></div>}
    {detailLoading && <div className="alert" role="status">正在读取订单详情…</div>}
    {loading && page.items.length === 0 ? <div className="empty-card">正在加载订单…</div> : page.items.length === 0 ? <div className="empty-card">没有符合条件的订单。</div> : <section className="resource-table-wrap"><table className="resource-table order-admin-table"><thead><tr><th>订单号</th><th>用户</th><th>套餐</th><th>类型 / 周期</th><SortableHeader label="订单金额" field="total_amount" applied={applied} onSort={sort} /><SortableHeader label="订单状态" field="status" applied={applied} onSort={sort} /><SortableHeader label="佣金金额" field="commission_balance" applied={applied} onSort={sort} /><SortableHeader label="佣金状态" field="commission_status" applied={applied} onSort={sort} /><SortableHeader label="创建时间" field="created_at" applied={applied} onSort={sort} /><th>操作</th></tr></thead><tbody>{page.items.map((order) => <tr key={order.id}><td data-label="订单号"><strong className="monospace">{order.trade_no}</strong></td><td data-label="用户">{order.user_email}</td><td data-label="套餐">{order.plan_name}</td><td data-label="类型 / 周期">{orderTypeLabel(order.type)}<small className="muted">{periodLabel(order.period)}</small></td><td data-label="订单金额">¥{formatCents(order.total_amount)}</td><td data-label="订单状态"><span className={`order-status status-${order.status}`}>{orderStatusLabel(order.status)}</span></td><td data-label="佣金金额">¥{formatCents(order.commission_balance)}</td><td data-label="佣金状态"><span className={`commission-status commission-${order.commission_status ?? "none"}`}>{commissionStatusLabel(order.commission_status)}</span></td><td data-label="创建时间">{formatDate(order.created_at)}</td><td data-label="操作"><button className="button secondary compact" aria-label={`查看订单：${order.trade_no}`} onClick={() => void open(order.trade_no)}>订单详情</button></td></tr>)}</tbody></table>
      {page.total > page.page_size && <div className="pagination-footer"><button className="button secondary compact" disabled={page.page <= 1 || loading} onClick={() => void load({ ...applied, page: page.page - 1 })}>上一页</button><span>第 {page.page} 页</span><button className="button secondary compact" disabled={page.page * page.page_size >= page.total || loading} onClick={() => void load({ ...applied, page: page.page + 1 })}>下一页</button></div>}
    </section>}
    {assigning && <AssignOrderDialog api={api} plans={plans} onClose={() => setAssigning(false)} onCreated={() => { setAssigning(false); void load({ page: 1, page_size: 20 }); }} />}
    {selected !== null && <AdminOrderDetailDialog api={api} order={selected} onClose={() => setSelected(null)} onUpdated={(order) => { setSelected(order); void refreshCurrent(); }} />}
  </main>;
}

function MultiFilter<T extends string | number>({ label, options, values, onChange, open, onOpenChange }: {
	label: string;
	options: Array<[T, string]>;
	values: T[];
	onChange: (values: T[]) => void;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const toggle = (value: T) => onChange(values.includes(value) ? values.filter((current) => current !== value) : [...values, value]);
	return <details className="order-multi-filter" open={open} onToggle={(event) => onOpenChange(event.currentTarget.open)}>
		<summary>{label}：{values.length === 0 ? "全部" : `已选 ${values.length} 项`}</summary>
		<fieldset aria-label={label}>
			<legend className="sr-only">{label}</legend>
			{options.map(([value, optionLabel]) => <label key={String(value)}>
				<input type="checkbox" aria-label={`${label}：${optionLabel}`} checked={values.includes(value)} onChange={() => toggle(value)} />
				<span>{optionLabel}</span>
			</label>)}
			{values.length > 0 && <button className="button ghost compact" type="button" onClick={() => onChange([])}>清除</button>}
		</fieldset>
	</details>;
}

function SortableHeader({ label, field, applied, onSort }: {
	label: string;
	field: NonNullable<AdminOrderQuery["sort_by"]>;
	applied: AdminOrderQuery;
	onSort: (field: NonNullable<AdminOrderQuery["sort_by"]>) => void;
}) {
	const active = applied.sort_by === field;
	return <th aria-sort={active ? applied.sort_desc ? "descending" : "ascending" : "none"}>
		<button className="table-sort-button" type="button" aria-label={`按${label}排序`} onClick={() => onSort(field)}>
			{label}<span aria-hidden="true">{active ? applied.sort_desc ? " ↓" : " ↑" : " ↕"}</span>
		</button>
	</th>;
}

function AssignOrderDialog({ api, plans, onClose, onCreated }: { api: OrderManagementAPI; plans: Plan[]; onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState("");
  const [planID, setPlanID] = useState(plans[0]?.id ?? 0);
  const [period, setPeriod] = useState<PlanPeriod>("monthly");
  const [amount, setAmount] = useState("0.00");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const selectedPlan = plans.find((plan) => plan.id === planID);
  const availablePeriods = periods.filter(([value]) => selectedPlan?.prices[value] !== undefined);
  const selectablePeriods = availablePeriods.length === 0 ? periods : availablePeriods;
  const effectivePeriod = selectablePeriods.some(([value]) => value === period) ? period : selectablePeriods[0]?.[0] ?? "monthly";
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api.assignOrder({ email: email.trim(), plan_id: planID, period: effectivePeriod, total_amount: parseCNY(amount) });
      onCreated();
    } catch (cause) {
      setError(messageOf(cause));
      setSaving(false);
    }
  };
  const selectPlan = (nextID: number) => {
    setPlanID(nextID);
    const next = plans.find((plan) => plan.id === nextID);
    const first = periods.find(([value]) => next?.prices[value] !== undefined);
    if (first !== undefined) setPeriod(first[0]);
  };
  return <Modal title="添加订单" onClose={onClose}><div className="modal-header"><h2>添加订单</h2><button className="icon-button" aria-label="关闭添加订单" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>用户邮箱<input type="email" required maxLength={254} value={email} onChange={(event) => setEmail(event.target.value)} /></label><label>订阅套餐<select required value={planID || ""} onChange={(event) => selectPlan(Number(event.target.value))}><option value="" disabled>请选择套餐</option>{plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name}</option>)}</select></label><label>付款周期<select value={effectivePeriod} onChange={(event) => setPeriod(event.target.value as PlanPeriod)}>{selectablePeriods.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><label>支付金额（CNY）<input inputMode="decimal" required value={amount} onChange={(event) => setAmount(event.target.value)} /></label><p className="small muted">管理员分配订单沿用旧 Xboard 规则：金额由管理员明确指定，不自动使用套餐标价或用户余额。</p>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving || planID === 0}>{saving ? "正在创建…" : "创建订单"}</button></div></form></Modal>;
}

function AdminOrderDetailDialog({ api, order, onClose, onUpdated }: { api: OrderManagementAPI; order: AdminOrderDetail; onClose: () => void; onUpdated: (order: AdminOrderDetail) => void }) {
  const [busy, setBusy] = useState<"paid" | "cancel" | "commission" | "">("");
  const [error, setError] = useState("");
  const act = async (action: "paid" | "cancel") => {
    if (busy !== "") return;
    setBusy(action); setError("");
    try { onUpdated(action === "paid" ? await api.paidAdminOrder(order.trade_no) : await api.cancelAdminOrder(order.trade_no)); }
    catch (cause) { setError(messageOf(cause)); }
    finally { setBusy(""); }
  };
	const updateCommission = async (status: 0 | 1 | 3) => {
		if (busy !== "") return;
		setBusy("commission"); setError("");
		try {
			onUpdated(await api.updateAdminOrderCommissionStatus(order.trade_no, status));
		} catch (cause) {
			setError(messageOf(cause));
		} finally {
			setBusy("");
		}
	};
	const commissionEditable = order.status === 3 && order.invite_user !== null && order.commission_balance > 0 && order.commission_status !== 2;
	return <Modal title="订单详情" onClose={onClose}>
		<div className="modal-header"><h2>订单详情</h2><button className="icon-button" aria-label="关闭订单详情" onClick={onClose}>×</button></div>
		<div className="detail-list order-detail-list">
			<div><span>订单号</span><strong className="monospace">{order.trade_no}</strong></div>
			<div><span>用户</span><strong>{order.user_email}</strong></div>
			<div><span>套餐</span><strong>{order.plan_name}</strong></div>
			<div><span>类型 / 周期</span><strong>{orderTypeLabel(order.type)} · {periodLabel(order.period)}</strong></div>
			<div><span>订单状态</span><strong>{orderStatusLabel(order.status)}</strong></div>
			<div><span>套餐原价</span><strong>¥{formatCents(order.original_amount)}</strong></div>
			<div><span>支付金额</span><strong>¥{formatCents(order.total_amount)}</strong></div>
			<div><span>支付手续费</span><strong>{order.handling_amount === null ? "—" : `¥${formatCents(order.handling_amount)}`}</strong></div>
			<div><span>余额支付</span><strong>¥{formatCents(order.balance_amount)}</strong></div>
			<div><span>优惠金额</span><strong>¥{formatCents(order.discount_amount)}</strong></div>
			<div><span>旧订阅折抵</span><strong>¥{formatCents(order.surplus_amount)}</strong></div>
			<div><span>折抵返还余额</span><strong>¥{formatCents(order.surplus_credit)}</strong></div>
			<div><span>支付回调号</span><strong className="monospace">{order.callback_no ?? "—"}</strong></div>
			<div><span>邀请人</span><strong>{order.invite_user?.email ?? "—"}</strong></div>
			<div><span>预计佣金</span><strong>¥{formatCents(order.commission_balance)}</strong></div>
			<div><span>实际佣金</span><strong>{order.actual_commission_balance === null ? "—" : `¥${formatCents(order.actual_commission_balance)}`}</strong></div>
			<div><span>佣金状态</span><strong>{commissionStatusLabel(order.commission_status)}</strong></div>
			<div><span>创建时间</span><strong>{formatDate(order.created_at)}</strong></div>
			<div><span>更新时间</span><strong>{formatDate(order.updated_at)}</strong></div>
			<div><span>订阅地址</span><strong>{order.subscribe_url === null ? "—" : <a href={order.subscribe_url} target="_blank" rel="noreferrer">打开订阅链接</a>}</strong></div>
		</div>
		<section className="commission-log-section" aria-label="佣金发放记录">
			<h3>佣金发放记录</h3>
			{order.commission_log.length === 0 ? <p className="muted">暂无佣金发放记录。</p> : <ul>{order.commission_log.map((item) => <li key={item.id}><span>{item.trade_no}</span><strong>¥{formatCents(item.get_amount)}</strong><time>{formatDate(item.created_at)}</time></li>)}</ul>}
		</section>
		{order.commission_status === 2 && <div className="alert success" role="status">佣金已发放，出于资金安全不可回退或重复发放。</div>}
		{commissionEditable && <div className="commission-actions" aria-label="修改佣金状态">
			<span className="muted">修改佣金状态</span>
			<button className="button secondary compact" disabled={busy !== "" || order.commission_status === 0} onClick={() => void updateCommission(0)}>设为待确认</button>
			<button className="button secondary compact" disabled={busy !== "" || order.commission_status === 1} onClick={() => void updateCommission(1)}>设为发放中</button>
			<button className="button secondary compact" disabled={busy !== "" || order.commission_status === 3} onClick={() => void updateCommission(3)}>设为无效</button>
		</div>}
		{error !== "" && <div className="alert error" role="alert">{error}</div>}
		<div className="form-actions"><button className="button ghost" onClick={onClose}>关闭</button>{order.status === 0 && <button className="button secondary" disabled={busy !== ""} onClick={() => void act("cancel")}>{busy === "cancel" ? "正在取消…" : "取消订单"}</button>}{order.status === 0 && <button className="button primary" disabled={busy !== ""} onClick={() => void act("paid")}>{busy === "paid" ? "正在开通…" : "标记已支付并开通"}</button>}</div>
	</Modal>;
}

function parseCNY(value: string): number {
  const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(value.trim());
  if (match === null || match[1] === undefined) throw new Error("支付金额格式无效");
  const cents = BigInt(match[1]) * 100n + BigInt((match[2] ?? "").padEnd(2, "0") || "0");
  if (cents > 9_000_000_000_000_000n) throw new Error("支付金额超出范围");
  return Number(cents);
}

function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "订单请求失败"; }

function commissionStatusLabel(status: number | null): string {
	return commissionOptions.find(([value]) => value === status)?.[1] ?? "—";
}
