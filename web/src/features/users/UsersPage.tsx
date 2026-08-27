import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Modal } from "../../components/Overlay";
import {
  APIError,
  type AdminAPI,
  type AdminUserFilter,
  type AdminUserFilterOperator,
  type AdminUser,
  type AdminUserCreateInput,
  type AdminUserQuery,
  type AdminUserSort,
  type AdminUserUpdateInput,
  type Plan,
  type ServerGroup
} from "../../lib/api";

type UsersAPI = Pick<AdminAPI,
  "listAdminUsers" | "getAdminUser" | "createAdminUser" | "updateAdminUser" | "resetAdminUserPassword" | "listServerGroups" | "listPlans"
>;

const userTimestampFormatter = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" });
const defaultUserQuery: AdminUserQuery = { page: 1, page_size: 20, sort_by: "id", sort_desc: true };

interface AdvancedFilterDraft {
  id: number;
  field: string;
  operator: AdminUserFilterOperator;
  value: string;
}

export function UsersPage({ api, currentUserID }: { api: UsersAPI; currentUserID: number }) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [total, setTotal] = useState(0);
  const [emailPrefix, setEmailPrefix] = useState("");
  const [status, setStatus] = useState("all");
  const [groupID, setGroupID] = useState("");
  const [appliedQuery, setAppliedQuery] = useState<AdminUserQuery>(defaultUserQuery);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [advancedFilters, setAdvancedFilters] = useState<AdvancedFilterDraft[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [groupError, setGroupError] = useState("");
  const [planError, setPlanError] = useState("");
  const [creating, setCreating] = useState(false);
  const [viewing, setViewing] = useState<AdminUser | null>(null);
  const [editing, setEditing] = useState<AdminUser | null>(null);
  const [resetting, setResetting] = useState<AdminUser | null>(null);
  const requestVersion = useRef(0);
  const nextFilterID = useRef(1);

  useEffect(() => {
    let active = true;
    const version = ++requestVersion.current;
    void Promise.allSettled([api.listAdminUsers(defaultUserQuery), api.listServerGroups(), api.listPlans()]).then(([pageResult, groupsResult, plansResult]) => {
      if (!active || version !== requestVersion.current) return;
      if (pageResult.status === "fulfilled") {
        setUsers(pageResult.value.items);
        setTotal(pageResult.value.total ?? pageResult.value.items.length);
        setError("");
      } else {
        setError(errorMessage(pageResult.reason));
      }
      if (groupsResult.status === "fulfilled") {
        setGroups(groupsResult.value);
        setGroupError("");
      } else {
        setGroupError(`权限组加载失败：${errorMessage(groupsResult.reason)}`);
      }
      if (plansResult.status === "fulfilled") {
        setPlans(plansResult.value);
        setPlanError("");
      } else {
        setPlanError(`套餐加载失败：${errorMessage(plansResult.reason)}`);
      }
    }).finally(() => {
      if (active && version === requestVersion.current) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const runQuery = async (query: AdminUserQuery) => {
    const normalized = { ...defaultUserQuery, ...query };
    const version = ++requestVersion.current;
    setLoading(true);
    setError("");
    try {
      const page = await api.listAdminUsers(normalized);
      if (version !== requestVersion.current) return;
      setUsers(page.items);
      setTotal(page.total ?? page.items.length);
      setAppliedQuery({ ...normalized, page: page.page ?? normalized.page, page_size: page.page_size ?? normalized.page_size });
    } catch (cause) {
      if (version === requestVersion.current) setError(errorMessage(cause));
    } finally {
      if (version === requestVersion.current) setLoading(false);
    }
  };

  const submitFilters = (event: FormEvent) => {
    event.preventDefault();
    const query: AdminUserQuery = {
      page: 1, page_size: appliedQuery.page_size ?? 20, sort_by: appliedQuery.sort_by ?? "id",
      sort_desc: appliedQuery.sort_desc ?? true, filters: wireAdvancedFilters(advancedFilters)
    };
    if (emailPrefix.trim() !== "") query.email_prefix = emailPrefix.trim();
    if (status === "active") query.banned = false;
    if (status === "banned") query.banned = true;
    if (groupID !== "") query.group_id = Number(groupID);
    void runQuery(query);
  };

  const sortBy = (field: AdminUserSort) => {
    const sameField = appliedQuery.sort_by === field;
    void runQuery({ ...appliedQuery, page: 1, sort_by: field, sort_desc: sameField ? !appliedQuery.sort_desc : false });
  };

  const retryGroups = async () => {
    setGroupError("");
    try {
      setGroups(await api.listServerGroups());
    } catch (cause) {
      setGroupError(`权限组加载失败：${errorMessage(cause)}`);
    }
  };

  const retryPlans = async () => {
    setPlanError("");
    try {
      setPlans(await api.listPlans());
    } catch (cause) {
      setPlanError(`套餐加载失败：${errorMessage(cause)}`);
    }
  };

  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);
  const page = appliedQuery.page ?? 1;
  const pageSize = appliedQuery.page_size ?? 20;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));

  return <main className="page-shell">
    <header className="page-header">
      <div><p className="eyebrow">Identity and access</p><h1>用户管理</h1><p className="muted">按 Xboard 业务字段查询和管理用户；敏感订阅凭据不会进入列表响应。</p></div>
      <button className="button primary" onClick={() => setCreating(true)}>新增用户</button>
    </header>

    <form className="user-filter-bar" onSubmit={submitFilters}>
      <label className="search-field">邮箱前缀<input type="search" role="searchbox" aria-label="邮箱前缀" value={emailPrefix} onChange={(event) => setEmailPrefix(event.target.value)} placeholder="例如 user@" /></label>
      <label>用户状态<select aria-label="用户状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="all">全部</option><option value="active">正常</option><option value="banned">已封禁</option></select></label>
      <label>权限组筛选<select aria-label="权限组筛选" value={groupID} onChange={(event) => setGroupID(event.target.value)}><option value="">全部权限组</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
      <button className="button ghost" type="button" aria-expanded={advancedOpen} onClick={() => setAdvancedOpen((value) => !value)}>高级筛选</button>
      <button className="button secondary" type="submit" disabled={loading}>查询用户</button>
      {advancedOpen && <fieldset className="user-advanced-filters"><legend>高级筛选（全部条件同时满足）</legend>
        {advancedFilters.map((filter, index) => <div className="user-filter-rule" key={filter.id}>
          <label>筛选字段 {index + 1}<select aria-label={`筛选字段 ${index + 1}`} value={filter.field} onChange={(event) => setAdvancedFilters((current) => current.map((item) => item.id === filter.id ? { ...item, field: event.target.value, operator: defaultFilterOperator(event.target.value), value: "" } : item))}>{advancedFilterFields.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
          <label>筛选操作符 {index + 1}<select aria-label={`筛选操作符 ${index + 1}`} value={filter.operator} onChange={(event) => setAdvancedFilters((current) => current.map((item) => item.id === filter.id ? { ...item, operator: event.target.value as AdminUserFilterOperator } : item))}>{advancedFilterOperators.filter(([value]) => allowedFilterOperators(filter.field).includes(value)).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
          <label>筛选值 {index + 1}{filter.field === "plan_id" && filter.operator !== "is_null" && filter.operator !== "not_null" && filter.operator !== "in" ? <select aria-label={`筛选值 ${index + 1}`} value={filter.value} onChange={(event) => setAdvancedFilters((current) => current.map((item) => item.id === filter.id ? { ...item, value: event.target.value } : item))}><option value="">选择套餐</option>{plans.map((plan) => <option value={plan.id} key={plan.id}>{plan.name}</option>)}</select> : <input aria-label={`筛选值 ${index + 1}`} value={filter.value} disabled={filter.operator === "is_null" || filter.operator === "not_null"} placeholder={filter.operator === "in" ? "多个值用英文逗号分隔" : "输入筛选值"} onChange={(event) => setAdvancedFilters((current) => current.map((item) => item.id === filter.id ? { ...item, value: event.target.value } : item))} />}</label>
          <button className="button ghost compact" type="button" aria-label={`移除筛选条件 ${index + 1}`} onClick={() => setAdvancedFilters((current) => current.filter((item) => item.id !== filter.id))}>移除</button>
        </div>)}
        <button className="button ghost compact" type="button" disabled={advancedFilters.length >= 10} onClick={() => setAdvancedFilters((current) => [...current, { id: nextFilterID.current++, field: "email", operator: "contains", value: "" }])}>添加筛选条件</button>
      </fieldset>}
    </form>

    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void runQuery(appliedQuery)}>重试</button></div>}
		{groupError !== "" && <div className="alert warning resource-alert" role="alert"><span>{groupError}</span><button className="button ghost compact" onClick={() => void retryGroups()}>重试权限组</button></div>}
    {planError !== "" && <div className="alert warning resource-alert" role="alert"><span>{planError}</span><button className="button ghost compact" onClick={() => void retryPlans()}>重试套餐</button></div>}
    {loading && users.length === 0 ? <div className="empty-card">正在加载用户…</div> : users.length === 0 ? <div className="empty-card">没有符合条件的用户。</div> :
      <div className="resource-table-wrap user-table-wrap">
        <table className="resource-table user-table" aria-label="用户列表">
          <thead><tr>
            <SortableHeader label="ID" field="id" query={appliedQuery} onSort={sortBy} />
            <th scope="col">邮箱</th>
            <SortableHeader label="在线设备" field="online_count" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="状态" field="banned" query={appliedQuery} onSort={sortBy} />
            <th scope="col">订阅</th><th scope="col">权限组</th>
            <SortableHeader label="已用流量" field="traffic_used" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="总流量" field="transfer_enable" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="到期时间" field="expired_at" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="余额" field="balance" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="佣金" field="commission_balance" query={appliedQuery} onSort={sortBy} />
            <SortableHeader label="注册时间" field="created_at" query={appliedQuery} onSort={sortBy} />
            <th scope="col">操作</th>
          </tr></thead>
          <tbody>{users.map((account) => <tr key={account.id}>
            <td data-label="ID">#{account.id}</td>
            <td data-label="邮箱"><strong>{account.email}</strong><small className="muted">{roleSummary(account) || "普通用户"}</small>{account.is_distributor && account.distributor_name && <small>{account.distributor_name}</small>}</td>
            <td data-label="在线设备">{account.online_count} / {account.device_limit === 0 ? "∞" : account.device_limit}<small className="muted">最后登录 {formatTimestamp(account.last_login_at)}</small></td>
            <td data-label="状态"><span className={`status-badge ${account.banned ? "blocked" : "enabled"}`}>{account.banned ? "已封禁" : "正常"}</span></td>
            <td data-label="订阅">{account.plan_name ?? "无订阅"}</td>
            <td data-label="权限组">{account.group_name ?? (account.group_id === null ? "未分组" : groupNames.get(account.group_id) ?? `#${account.group_id}`)}</td>
            <td data-label="已用流量">{formatBytes(account.traffic_used ?? account.traffic_upload + account.traffic_download)}</td>
            <td data-label="总流量">{formatBytes(account.transfer_enable)}</td>
            <td data-label="到期时间">{account.expired_at === null ? "长期有效" : formatTimestamp(account.expired_at)}</td>
            <td data-label="余额">{formatMoney(account.balance)}</td>
            <td data-label="佣金">{formatMoney(account.commission_balance)}<small className="muted">{commissionLabel(account)}</small></td>
            <td data-label="注册时间">{formatTimestamp(account.created_at)}</td>
            <td data-label="操作"><div className="row-actions"><button className="button ghost compact" aria-label={`查看详情：${account.email}`} onClick={() => setViewing(account)}>详情</button><button className="button ghost compact" aria-label={`编辑用户：${account.email}`} onClick={() => setEditing(account)}>编辑</button><button className="button ghost compact" aria-label={`重置密码：${account.email}`} onClick={() => setResetting(account)}>重置密码</button></div></td>
          </tr>)}</tbody>
        </table>
        <div className="pagination-footer user-pagination"><span>共 {total} 名用户，第 {page} / {pageCount} 页</span><label>每页<select aria-label="每页用户数" value={pageSize} disabled={loading} onChange={(event) => void runQuery({ ...appliedQuery, page: 1, page_size: Number(event.target.value) })}><option value="10">10</option><option value="20">20</option><option value="50">50</option><option value="100">100</option><option value="200">200</option></select></label><div className="row-actions"><button className="button ghost compact" disabled={loading || page <= 1} onClick={() => void runQuery({ ...appliedQuery, page: 1 })}>首页</button><button className="button ghost compact" disabled={loading || page <= 1} onClick={() => void runQuery({ ...appliedQuery, page: page - 1 })}>上一页</button><button className="button ghost compact" disabled={loading || page >= pageCount} onClick={() => void runQuery({ ...appliedQuery, page: page + 1 })}>下一页</button><button className="button ghost compact" disabled={loading || page >= pageCount} onClick={() => void runQuery({ ...appliedQuery, page: pageCount })}>末页</button></div></div>
      </div>}

    {creating && <UserEditor api={api} groups={groups} plans={plans} onClose={() => setCreating(false)} onSaved={() => { setCreating(false); void runQuery(appliedQuery); }} />}
    {viewing !== null && <UserDetail account={viewing} onClose={() => setViewing(null)} />}
    {editing !== null && <UserEditor api={api} groups={groups} plans={plans} account={editing} currentUserID={currentUserID} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); void runQuery(appliedQuery); }} />}
    {resetting !== null && <PasswordReset api={api} account={resetting} onClose={() => setResetting(null)} onSaved={() => { setResetting(null); void runQuery(appliedQuery); }} />}
  </main>;
}

function SortableHeader({ label, field, query, onSort }: { label: string; field: AdminUserSort; query: AdminUserQuery; onSort: (field: AdminUserSort) => void }) {
  const active = query.sort_by === field;
  return <th scope="col" aria-label={label} aria-sort={active ? query.sort_desc ? "descending" : "ascending" : "none"}><button type="button" className="table-sort" aria-label={`按${label}排序`} onClick={() => onSort(field)}>{label}{active ? query.sort_desc ? " ↓" : " ↑" : ""}</button></th>;
}

function UserDetail({ account, onClose }: { account: AdminUser; onClose: () => void }) {
  return <Modal title="用户详情" onClose={onClose}>
    <ModalHeader title="用户详情" onClose={onClose} />
    <div className="user-detail-grid">
      <DetailField label="ID" value={`#${account.id}`} /><DetailField label="邮箱" value={account.email} />
      <DetailField label="角色" value={roleSummary(account) || "普通用户"} /><DetailField label="状态" value={account.banned ? "已封禁" : "正常"} />
      <DetailField label="套餐" value={account.plan_name ?? "无订阅"} /><DetailField label="权限组" value={account.group_name ?? "未分组"} />
      <DetailField label="邀请人" value={account.invite_user_email ?? "无"} /><DetailField label="Telegram" value={account.telegram_id === null ? "未绑定" : String(account.telegram_id)} />
      <DetailField label="备注" value={account.remarks ?? "无"} wide />
      <DetailField label="已用流量" value={`${formatBytes(account.traffic_used)}（上行 ${formatBytes(account.traffic_upload)} / 下行 ${formatBytes(account.traffic_download)}）`} />
      <DetailField label="总流量" value={formatBytes(account.transfer_enable)} /><DetailField label="在线设备" value={String(account.online_count)} />
      <DetailField label="速度 / 设备限制" value={`${account.speed_limit === 0 ? "不限速" : `${account.speed_limit} Mbps`} / ${account.device_limit === 0 ? "不限设备" : `${account.device_limit} 台`}`} />
      <DetailField label="上次 / 下次重置" value={`${formatTimestamp(account.last_reset_at)} / ${formatTimestamp(account.next_reset_at)}（${account.reset_count} 次）`} />
      <DetailField label="余额" value={formatMoney(account.balance)} /><DetailField label="佣金" value={`${formatMoney(account.commission_balance)} · ${commissionLabel(account)}`} />
      <DetailField label="专享折扣" value={account.discount === null ? "系统默认" : `${account.discount}%`} /><DetailField label="到期时间" value={account.expired_at === null ? "长期有效" : formatTimestamp(account.expired_at)} />
      <DetailField label="提醒" value={`${account.remind_expire ? "到期提醒开启" : "到期提醒关闭"} · ${account.remind_traffic ? "流量提醒开启" : "流量提醒关闭"}`} />
      <DetailField label="最后登录 / 在线" value={`${formatTimestamp(account.last_login_at)} / ${formatTimestamp(account.last_online_at)}`} />
      <DetailField label="注册 / 更新" value={`${formatTimestamp(account.created_at)} / ${formatTimestamp(account.updated_at)}`} wide />
    </div>
    <div className="form-actions"><button className="button primary" type="button" onClick={onClose}>关闭</button></div>
  </Modal>;
}

function DetailField({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return <div className={wide ? "user-detail-field wide" : "user-detail-field"}><span className="muted small">{label}</span><strong>{value}</strong></div>;
}

function UserEditor({ api, groups, plans, account, currentUserID, onClose, onSaved }: {
  api: UsersAPI; groups: ServerGroup[]; plans: Plan[]; account?: AdminUser; currentUserID?: number; onClose: () => void; onSaved: (user: AdminUser) => void;
}) {
  const editing = account !== undefined;
  const [current, setCurrent] = useState(account);
  const [email, setEmail] = useState(account?.email ?? "");
  const [password, setPassword] = useState("");
	const [planID, setPlanID] = useState(account?.plan_id === null || account === undefined ? "" : String(account.plan_id));
	const [inviteUserEmail, setInviteUserEmail] = useState(account?.invite_user_email ?? "");
  const [groupID, setGroupID] = useState(account?.group_id === null || account === undefined ? "" : String(account.group_id));
	const [transferEnable, setTransferEnable] = useState(account === undefined ? "0" : scaledIntegerToDecimal(account.transfer_enable, gibBytes));
	const [trafficUpload, setTrafficUpload] = useState(account === undefined ? "0" : scaledIntegerToDecimal(account.traffic_upload, gibBytes));
	const [trafficDownload, setTrafficDownload] = useState(account === undefined ? "0" : scaledIntegerToDecimal(account.traffic_download, gibBytes));
  const [expiredAt, setExpiredAt] = useState(toLocalDateTime(account?.expired_at ?? null));
  const [speedLimit, setSpeedLimit] = useState(String(account?.speed_limit ?? 0));
  const [deviceLimit, setDeviceLimit] = useState(String(account?.device_limit ?? 0));
	const [balance, setBalance] = useState(centsToDecimal(account?.balance ?? 0));
	const [commissionType, setCommissionType] = useState(String(account?.commission_type ?? 0));
	const [commissionRate, setCommissionRate] = useState(account?.commission_rate === null || account === undefined ? "" : String(account.commission_rate));
	const [commissionBalance, setCommissionBalance] = useState(centsToDecimal(account?.commission_balance ?? 0));
	const [discount, setDiscount] = useState(account?.discount === null || account === undefined ? "" : String(account.discount));
	const [telegramID, setTelegramID] = useState(account?.telegram_id === null || account === undefined ? "" : String(account.telegram_id));
	const [remindExpire, setRemindExpire] = useState(account?.remind_expire ?? false);
	const [remindTraffic, setRemindTraffic] = useState(account?.remind_traffic ?? false);
	const [remarks, setRemarks] = useState(account?.remarks ?? "");
  const [banned, setBanned] = useState(account?.banned ?? false);
  const [isAdmin, setIsAdmin] = useState(account?.is_admin ?? false);
  const [isStaff, setIsStaff] = useState(account?.is_staff ?? false);
  const [isDistributor, setIsDistributor] = useState(account?.is_distributor ?? false);
  const [distributorName, setDistributorName] = useState(account?.distributor_name ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);

  const resetFrom = (value: AdminUser) => {
		setCurrent(value); setEmail(value.email); setPassword("");
		setPlanID(value.plan_id === null ? "" : String(value.plan_id)); setInviteUserEmail(value.invite_user_email ?? "");
		setGroupID(value.group_id === null ? "" : String(value.group_id));
		setTransferEnable(scaledIntegerToDecimal(value.transfer_enable, gibBytes));
		setTrafficUpload(scaledIntegerToDecimal(value.traffic_upload, gibBytes));
		setTrafficDownload(scaledIntegerToDecimal(value.traffic_download, gibBytes)); setExpiredAt(toLocalDateTime(value.expired_at));
    setSpeedLimit(String(value.speed_limit)); setDeviceLimit(String(value.device_limit)); setBanned(value.banned);
		setBalance(centsToDecimal(value.balance)); setCommissionType(String(value.commission_type));
		setCommissionRate(value.commission_rate === null ? "" : String(value.commission_rate));
		setCommissionBalance(centsToDecimal(value.commission_balance)); setDiscount(value.discount === null ? "" : String(value.discount));
		setTelegramID(value.telegram_id === null ? "" : String(value.telegram_id));
		setRemindExpire(value.remind_expire); setRemindTraffic(value.remind_traffic); setRemarks(value.remarks ?? "");
    setIsAdmin(value.is_admin); setIsStaff(value.is_staff ?? false); setIsDistributor(value.is_distributor ?? false);
    setDistributorName(value.distributor_name ?? "");
  };

	const selectPlan = (value: string) => {
		setPlanID(value);
		if (value === "") return;
		const selected = plans.find((plan) => plan.id === Number(value));
		if (selected === undefined) return;
		setGroupID(selected.group_id === null ? "" : String(selected.group_id));
		setTransferEnable(String(selected.transfer_enable));
		setSpeedLimit(String(selected.speed_limit ?? 0));
		setDeviceLimit(String(selected.device_limit ?? 0));
	};

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (isDistributor && distributorName.trim() === "") {
      setError("启用分销商角色时必须填写分销商名称");
      return;
    }
		setBusy(true); setError(""); setConflict(false);
    try {
			const common = {
				email: email.trim(), group_id: groupID === "" ? null : safePositiveInteger(groupID, "权限组"),
				transfer_enable: editing ? decimalToScaledInteger(transferEnable, gibBytes, "流量额度") : safeNonnegativeInteger(transferEnable, "流量额度"),
				expired_at: expiredAt === "" ? null : new Date(expiredAt).toISOString(),
				speed_limit: safeNonnegativeInteger(speedLimit, "限速"), device_limit: safeNonnegativeInteger(deviceLimit, "设备数"),
				banned, is_admin: isAdmin, is_staff: isStaff,
				is_distributor: isDistributor, distributor_name: isDistributor ? distributorName.trim() : null
			};
			let saved: AdminUser;
			if (editing && current !== undefined) {
				const update: AdminUserUpdateInput = {
					...common, revision: current.revision, plan_id: planID === "" ? null : safePositiveInteger(planID, "套餐"),
					invite_user_email: inviteUserEmail.trim() === "" ? null : inviteUserEmail.trim(),
					traffic_upload: decimalToScaledInteger(trafficUpload, gibBytes, "已用上行流量"),
					traffic_download: decimalToScaledInteger(trafficDownload, gibBytes, "已用下行流量"),
					balance: decimalMoneyToCents(balance, "余额"), commission_type: safeRangeInteger(commissionType, "佣金类型", 0, 2),
					commission_rate: nullableRangeInteger(commissionRate, "佣金比例", 0, 100),
					commission_balance: decimalMoneyToCents(commissionBalance, "佣金余额"),
					discount: nullableRangeInteger(discount, "专享折扣", 0, 100), telegram_id: nullablePositiveInteger(telegramID, "Telegram ID"),
					remind_expire: remindExpire, remind_traffic: remindTraffic, remarks: remarks.trim() === "" ? null : remarks.trim()
				};
				if (password !== "") update.password = password;
				saved = await api.updateAdminUser(current.id, update);
			} else {
				saved = await api.createAdminUser({ ...common, password } satisfies AdminUserCreateInput);
			}
      onSaved(saved);
    } catch (cause) {
      setError(errorMessage(cause));
      setConflict(cause instanceof APIError && cause.code === "user_revision_conflict");
      setBusy(false);
    }
  };

  const reload = async () => {
    if (current === undefined) return;
    setBusy(true); setError("");
    try {
      resetFrom(await api.getAdminUser(current.id));
      setConflict(false);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  };

  const title = editing ? "编辑用户" : "新增用户";
  return <Modal title={title} onClose={onClose}>
    <ModalHeader title={title} onClose={onClose} />
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>邮箱<input type="email" value={email} maxLength={320} required onChange={(event) => setEmail(event.target.value)} /></label>
			<label>{editing ? "新密码（留空不修改）" : "初始密码"}<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} value={password} required={!editing} onChange={(event) => setPassword(event.target.value)} /></label>
			{editing && <label>邀请人邮箱（留空表示无）<input type="email" maxLength={320} value={inviteUserEmail} onChange={(event) => setInviteUserEmail(event.target.value)} /></label>}
			{editing && <label>套餐<select value={planID} onChange={(event) => selectPlan(event.target.value)}><option value="">无订阅</option>{plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name}</option>)}</select></label>}
      <label>权限组<select value={groupID} onChange={(event) => setGroupID(event.target.value)}><option value="">未分组</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
			<label>{editing ? "流量额度（GiB）" : "流量额度（字节）"}<input type="number" min="0" max={editing ? undefined : Number.MAX_SAFE_INTEGER} step={editing ? "any" : "1"} value={transferEnable} required onChange={(event) => setTransferEnable(event.target.value)} /></label>
      <label>到期时间（留空表示不限期）<input type="datetime-local" value={expiredAt} onChange={(event) => setExpiredAt(event.target.value)} /></label>
      <div className="time-grid"><label>限速（Mbps，0 为不限速）<input type="number" min="0" step="1" value={speedLimit} required onChange={(event) => setSpeedLimit(event.target.value)} /></label><label>设备数（0 为不限设备）<input type="number" min="0" max="1000" step="1" value={deviceLimit} required onChange={(event) => setDeviceLimit(event.target.value)} /></label></div>
			{editing && <fieldset className="settings-fieldset"><legend>流量使用</legend><div className="time-grid">
				<label>已用上行流量（GiB）<input type="number" min="0" step="any" value={trafficUpload} required onChange={(event) => setTrafficUpload(event.target.value)} /></label>
				<label>已用下行流量（GiB）<input type="number" min="0" step="any" value={trafficDownload} required onChange={(event) => setTrafficDownload(event.target.value)} /></label>
			</div></fieldset>}
			{editing && <fieldset className="settings-fieldset"><legend>财务与折扣</legend>
				<div className="time-grid"><label>余额（元）<input type="text" inputMode="decimal" value={balance} required onChange={(event) => setBalance(event.target.value)} /></label><label>佣金余额（元）<input type="text" inputMode="decimal" value={commissionBalance} required onChange={(event) => setCommissionBalance(event.target.value)} /></label></div>
				<div className="time-grid"><label>佣金类型<select value={commissionType} onChange={(event) => setCommissionType(event.target.value)}><option value="0">系统默认</option><option value="1">循环佣金</option><option value="2">首次佣金</option></select></label><label>佣金比例（留空使用系统默认）<input type="number" min="0" max="100" step="1" value={commissionRate} onChange={(event) => setCommissionRate(event.target.value)} /></label></div>
				<label>专享折扣（留空使用系统默认）<input type="number" min="0" max="100" step="1" value={discount} onChange={(event) => setDiscount(event.target.value)} /></label>
			</fieldset>}
			{editing && <fieldset className="settings-fieldset"><legend>联系与提醒</legend>
				<label>Telegram ID（留空表示未绑定）<input type="number" min="1" step="1" value={telegramID} onChange={(event) => setTelegramID(event.target.value)} /></label>
				<div className="role-switch-grid"><label className="switch-label"><input type="checkbox" checked={remindExpire} onChange={(event) => setRemindExpire(event.target.checked)} />到期提醒</label><label className="switch-label"><input type="checkbox" checked={remindTraffic} onChange={(event) => setRemindTraffic(event.target.checked)} />流量提醒</label></div>
				<label>备注<textarea maxLength={4096} rows={4} value={remarks} onChange={(event) => setRemarks(event.target.value)} /></label>
			</fieldset>}
      <fieldset className="settings-fieldset"><legend>账号角色（可并存）</legend><div className="role-switch-grid">
        <label className="switch-label"><input type="checkbox" checked={isAdmin} disabled={editing && current?.id === currentUserID} onChange={(event) => setIsAdmin(event.target.checked)} />管理员</label>
        <label className="switch-label"><input type="checkbox" checked={isStaff} onChange={(event) => setIsStaff(event.target.checked)} />员工</label>
        <label className="switch-label"><input type="checkbox" checked={isDistributor} onChange={(event) => { setIsDistributor(event.target.checked); if (!event.target.checked) setDistributorName(""); }} />分销商</label>
      </div>{isDistributor && <label>分销商名称<input aria-label="分销商名称" value={distributorName} minLength={1} maxLength={100} aria-invalid={error.includes("分销商名称")} onChange={(event) => setDistributorName(event.target.value)} /></label>}</fieldset>
      <label className="switch-label"><input type="checkbox" checked={banned} disabled={editing && current?.id === currentUserID} onChange={(event) => setBanned(event.target.checked)} />封禁用户</label>
      {editing && current?.id === currentUserID && <p className="muted small">为防止当前管理员锁定自己，此账号不能在本页封禁或移除管理员角色。</p>}
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions">{conflict && <button className="button secondary" type="button" disabled={busy} onClick={() => void reload()}>加载最新状态</button>}<button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={busy}>{busy ? "正在保存…" : editing ? "保存" : "创建"}</button></div>
    </form>
  </Modal>;
}

function PasswordReset({ api, account, onClose, onSaved }: { api: UsersAPI; account: AdminUser; onClose: () => void; onSaved: (user: AdminUser) => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try {
      onSaved(await api.resetAdminUserPassword(account.id, account.revision, password));
    } catch (cause) {
      setError(errorMessage(cause)); setBusy(false);
    }
  };
  return <Modal title="重置用户密码" onClose={onClose}>
    <ModalHeader title="重置用户密码" onClose={onClose} />
    <p className="muted">重置 {account.email} 的密码后，该用户所有现有会话会立即失效。</p>
    <form className="form-stack" onSubmit={(event) => void submit(event)}><label>新密码<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} value={password} required onChange={(event) => setPassword(event.target.value)} /></label>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={busy}>{busy ? "正在重置…" : "确认重置"}</button></div></form>
  </Modal>;
}

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>;
}

const gibBytes = 1_073_741_824n;
const maximumSafeInteger = BigInt(Number.MAX_SAFE_INTEGER);
const maximumMoneyCents = 9_000_000_000_000_000n;

function scaledIntegerToDecimal(value: number, scale: bigint): string {
	if (!Number.isSafeInteger(value) || value < 0) throw new Error("服务端返回了超出安全范围的数值");
	const integer = BigInt(value);
	const whole = integer / scale;
	let remainder = integer % scale;
	if (remainder === 0n) return whole.toString();
	let fraction = "";
	while (remainder !== 0n) {
		remainder *= 10n;
		fraction += (remainder / scale).toString();
		remainder %= scale;
	}
	return `${whole}.${fraction}`;
}

function decimalToScaledInteger(value: string, scale: bigint, label: string): number {
	const normalized = value.trim();
	const matched = /^(\d+)(?:\.(\d+))?$/.exec(normalized);
	if (matched === null || (matched[2]?.length ?? 0) > 30) throw new Error(`${label}格式无效`);
	const whole = matched[1];
	if (whole === undefined) throw new Error(`${label}格式无效`);
	const fraction = matched[2] ?? "";
	const denominator = 10n ** BigInt(fraction.length);
	const numerator = BigInt(whole) * denominator + BigInt(fraction === "" ? "0" : fraction);
	const scaled = numerator * scale;
	if (scaled % denominator !== 0n) throw new Error(`${label}无法精确换算为字节`);
	const result = scaled / denominator;
	if (result > maximumSafeInteger) throw new Error(`${label}超出安全范围`);
	return Number(result);
}

function safeNonnegativeInteger(value: string, label: string): number {
	const normalized = value.trim();
	if (!/^\d+$/.test(normalized)) throw new Error(`${label}必须是非负整数`);
	const result = BigInt(normalized);
	if (result > maximumSafeInteger) throw new Error(`${label}超出安全范围`);
	return Number(result);
}

function safePositiveInteger(value: string, label: string): number {
	const result = safeNonnegativeInteger(value, label);
	if (result < 1) throw new Error(`${label}必须是正整数`);
	return result;
}

function safeRangeInteger(value: string, label: string, minimum: number, maximum: number): number {
	const result = safeNonnegativeInteger(value, label);
	if (result < minimum || result > maximum) throw new Error(`${label}必须在 ${minimum} 到 ${maximum} 之间`);
	return result;
}

function nullableRangeInteger(value: string, label: string, minimum: number, maximum: number): number | null {
	return value.trim() === "" ? null : safeRangeInteger(value, label, minimum, maximum);
}

function nullablePositiveInteger(value: string, label: string): number | null {
	return value.trim() === "" ? null : safePositiveInteger(value, label);
}

function centsToDecimal(value: number): string {
	if (!Number.isSafeInteger(value) || value < 0) throw new Error("服务端返回了超出安全范围的金额");
	const cents = BigInt(value);
	return `${cents / 100n}.${(cents % 100n).toString().padStart(2, "0")}`;
}

function decimalMoneyToCents(value: string, label: string): number {
	const normalized = value.trim();
	const matched = /^(\d+)(?:\.(\d{1,2}))?$/.exec(normalized);
	if (matched === null) throw new Error(`${label}格式无效，最多保留两位小数`);
	const whole = matched[1];
	if (whole === undefined) throw new Error(`${label}格式无效，最多保留两位小数`);
	const fraction = (matched[2] ?? "").padEnd(2, "0");
	const cents = BigInt(whole) * 100n + BigInt(fraction === "" ? "0" : fraction);
	if (cents > maximumMoneyCents) throw new Error(`${label}超出安全范围`);
	return Number(cents);
}

function toLocalDateTime(value: string | null): string {
  if (value === null) return "";
  const date = new Date(value);
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
  let amount = value;
  let unit = -1;
  do { amount /= 1024; unit++; } while (amount >= 1024 && unit < units.length - 1);
  return `${amount.toFixed(amount >= 10 ? 1 : 2)} ${units[unit]}`;
}

function formatTimestamp(value: string | null): string {
  return value === null ? "从未" : userTimestampFormatter.format(new Date(value));
}

function formatMoney(cents: number): string {
  return `¥${(cents / 100).toFixed(2)}`;
}

function commissionLabel(account: AdminUser): string {
  const type = ["系统默认", "循环佣金", "首次佣金"][account.commission_type] ?? `类型 ${account.commission_type}`;
  return account.commission_rate === null ? type : `${type} ${account.commission_rate}%`;
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}

function roleSummary(account: AdminUser): string {
  const roles = [account.is_admin ? "管理员" : "", account.is_staff ? "员工" : "", account.is_distributor ? "分销商" : ""].filter(Boolean);
  return roles.join(" · ");
}

const advancedFilterFields = [
	["email", "邮箱"], ["id", "用户 ID"], ["plan_id", "套餐"], ["group_id", "权限组"],
	["transfer_enable", "总流量"], ["traffic_used", "已用流量"], ["online_count", "在线设备"],
	["expired_at", "到期时间（Unix 秒）"], ["banned", "封禁状态"], ["remarks", "备注"],
	["uuid", "UUID（仅精确匹配）"], ["subscription_token", "Token（仅精确匹配）"],
	["invite_user_email", "邀请人邮箱"], ["invite_user_id", "邀请人 ID"], ["is_admin", "管理员"],
	["is_staff", "员工"], ["is_distributor", "分销商"], ["balance", "余额（分）"],
	["commission_balance", "佣金（分）"], ["created_at", "注册时间（Unix 秒）"],
] as const;

const advancedFilterOperators: ReadonlyArray<readonly [AdminUserFilterOperator, string]> = [
	["contains", "包含"], ["eq", "等于"], ["neq", "不等于"], ["gt", "大于"], ["gte", "大于等于"],
	["lt", "小于"], ["lte", "小于等于"], ["in", "属于集合"], ["is_null", "为空"], ["not_null", "不为空"],
];

function wireAdvancedFilters(filters: AdvancedFilterDraft[]): AdminUserFilter[] | undefined {
	const wired = filters.flatMap<AdminUserFilter>((filter) => {
		if (filter.operator === "is_null" || filter.operator === "not_null") {
			return [{ field: filter.field, operator: filter.operator }];
		}
		const value = filter.value.trim();
		if (value === "") return [];
		if (filter.operator === "in") {
			const values = value.split(",").map((item) => item.trim()).filter(Boolean);
			return values.length === 0 ? [] : [{ field: filter.field, operator: filter.operator, value: values }];
		}
		return [{ field: filter.field, operator: filter.operator, value }];
	});
	return wired.length === 0 ? undefined : wired;
}

function isSecretFilterField(field: string): boolean {
	return field === "uuid" || field === "subscription_token";
}

const stringFilterFields = new Set(["email", "remarks", "invite_user_email"]);
const booleanFilterFields = new Set(["banned", "is_admin", "is_staff", "is_distributor"]);
const categoricalFilterFields = new Set(["plan_id", "group_id"]);
const nullableFilterFields = new Set(["plan_id", "group_id", "expired_at", "remarks", "invite_user_id", "invite_user_email"]);

function defaultFilterOperator(field: string): AdminUserFilterOperator {
  return stringFilterFields.has(field) ? "contains" : "eq";
}

function allowedFilterOperators(field: string): AdminUserFilterOperator[] {
  if (isSecretFilterField(field)) return ["eq", "in"];
  const operators: AdminUserFilterOperator[] = stringFilterFields.has(field)
    ? ["contains", "eq", "neq", "in"]
    : booleanFilterFields.has(field)
      ? ["eq", "neq", "in"]
      : categoricalFilterFields.has(field)
        ? ["eq", "neq", "in"]
        : ["eq", "neq", "gt", "gte", "lt", "lte", "in"];
  return nullableFilterFields.has(field) ? [...operators, "is_null", "not_null"] : operators;
}
