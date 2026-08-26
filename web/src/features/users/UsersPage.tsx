import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Modal } from "../../components/Overlay";
import {
  APIError,
  type AdminAPI,
  type AdminUser,
  type AdminUserCreateInput,
  type AdminUserQuery,
  type AdminUserUpdateInput,
  type ServerGroup
} from "../../lib/api";

type UsersAPI = Pick<AdminAPI,
  "listAdminUsers" | "getAdminUser" | "createAdminUser" | "updateAdminUser" | "resetAdminUserPassword" | "listServerGroups"
>;

const userTimestampFormatter = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" });

export function UsersPage({ api, currentUserID }: { api: UsersAPI; currentUserID: number }) {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [emailPrefix, setEmailPrefix] = useState("");
  const [status, setStatus] = useState("all");
  const [groupID, setGroupID] = useState("");
  const [appliedQuery, setAppliedQuery] = useState<AdminUserQuery>({});
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState("");
	const [groupError, setGroupError] = useState("");
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<AdminUser | null>(null);
  const [resetting, setResetting] = useState<AdminUser | null>(null);
	const requestVersion = useRef(0);

  useEffect(() => {
    let active = true;
		const version = ++requestVersion.current;
		void Promise.allSettled([api.listAdminUsers({ limit: 50 }), api.listServerGroups()]).then(([pageResult, groupsResult]) => {
			if (!active || version !== requestVersion.current) return;
			if (pageResult.status === "fulfilled") {
				setUsers(pageResult.value.items);
				setNextCursor(pageResult.value.next_cursor ?? "");
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
    }).finally(() => {
			if (active && version === requestVersion.current) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const runQuery = async (query: AdminUserQuery) => {
		const version = ++requestVersion.current;
		setLoadingMore(false);
    setLoading(true);
    setError("");
    try {
      const page = await api.listAdminUsers({ limit: 50, ...query });
			if (version !== requestVersion.current) return;
			setUsers(page.items);
      setNextCursor(page.next_cursor ?? "");
      setAppliedQuery(query);
    } catch (cause) {
			if (version === requestVersion.current) setError(errorMessage(cause));
    } finally {
			if (version === requestVersion.current) setLoading(false);
    }
  };

  const submitFilters = (event: FormEvent) => {
    event.preventDefault();
    const query: AdminUserQuery = {};
    if (emailPrefix.trim() !== "") query.email_prefix = emailPrefix.trim();
    if (status === "active") query.banned = false;
    if (status === "banned") query.banned = true;
    if (groupID !== "") query.group_id = Number(groupID);
    void runQuery(query);
  };

  const loadMore = async () => {
    if (nextCursor === "" || loadingMore) return;
    setLoadingMore(true);
		const version = requestVersion.current;
    setError("");
    try {
      const page = await api.listAdminUsers({ limit: 50, ...appliedQuery, cursor: nextCursor });
			if (version !== requestVersion.current) return;
			setUsers((current) => {
        const known = new Set(current.map((item) => item.id));
        return [...current, ...page.items.filter((item) => !known.has(item.id))];
      });
      setNextCursor(page.next_cursor ?? "");
    } catch (cause) {
			if (version === requestVersion.current) setError(errorMessage(cause));
    } finally {
			if (version === requestVersion.current) setLoadingMore(false);
    }
  };

  const replaceUser = (updated: AdminUser) => {
		setUsers((current) => {
			const remaining = current.filter((item) => item.id !== updated.id);
			return matchesQuery(updated, appliedQuery) ? sortUsers([...remaining, updated], appliedQuery) : remaining;
		});
    setEditing((current) => current?.id === updated.id ? updated : current);
    setResetting((current) => current?.id === updated.id ? updated : current);
  };

	const retryGroups = async () => {
		setGroupError("");
		try {
			setGroups(await api.listServerGroups());
		} catch (cause) {
			setGroupError(`权限组加载失败：${errorMessage(cause)}`);
		}
	};

  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);

  return <main className="page-shell">
    <header className="page-header">
      <div><p className="eyebrow">Identity and access</p><h1>用户管理</h1><p className="muted">以游标分页管理用户、访问状态和可并存的管理员、员工与分销商角色。</p></div>
      <button className="button primary" onClick={() => setCreating(true)}>新增用户</button>
    </header>

    <form className="user-filter-bar" onSubmit={submitFilters}>
      <label className="search-field">邮箱前缀<input type="search" role="searchbox" aria-label="邮箱前缀" value={emailPrefix} onChange={(event) => setEmailPrefix(event.target.value)} placeholder="例如 user@" /></label>
      <label>用户状态<select aria-label="用户状态" value={status} onChange={(event) => setStatus(event.target.value)}><option value="all">全部</option><option value="active">正常</option><option value="banned">已封禁</option></select></label>
      <label>权限组筛选<select aria-label="权限组筛选" value={groupID} onChange={(event) => setGroupID(event.target.value)}><option value="">全部权限组</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
      <button className="button secondary" type="submit" disabled={loading}>查询用户</button>
    </form>

    {error !== "" && <div className="alert error resource-alert" role="alert"><span>{error}</span><button className="button ghost compact" onClick={() => void runQuery(appliedQuery)}>重试</button></div>}
		{groupError !== "" && <div className="alert warning resource-alert" role="alert"><span>{groupError}</span><button className="button ghost compact" onClick={() => void retryGroups()}>重试权限组</button></div>}
    {loading && users.length === 0 ? <div className="empty-card">正在加载用户…</div> : users.length === 0 ? <div className="empty-card">没有符合条件的用户。</div> :
      <div className="resource-table-wrap">
        <table className="resource-table user-table">
          <thead><tr><th>用户</th><th>状态</th><th>权限组</th><th>流量</th><th>限制</th><th>操作</th></tr></thead>
          <tbody>{users.map((account) => <tr key={account.id}>
            <td data-label="用户"><strong>{account.email}</strong><small className="muted">#{account.id}{roleSummary(account)}</small>{account.is_distributor && account.distributor_name && <small>{account.distributor_name}</small>}</td>
            <td data-label="状态"><span className={`status-badge ${account.banned ? "blocked" : "enabled"}`}>{account.banned ? "已封禁" : "正常"}</span><small className="muted">在线 {account.online_count}</small><small className="muted">最后登录 {formatTimestamp(account.last_login_at)}</small></td>
            <td data-label="权限组">{account.group_id === null ? "未分组" : groupNames.get(account.group_id) ?? `#${account.group_id}`}</td>
            <td data-label="流量"><span>{formatBytes(account.traffic_upload + account.traffic_download)} / {formatBytes(account.transfer_enable)}</span></td>
            <td data-label="限制"><span>{account.speed_limit === 0 ? "不限速" : `${account.speed_limit} Mbps`} · {account.device_limit === 0 ? "不限设备" : `${account.device_limit} 台`}</span></td>
            <td data-label="操作"><div className="row-actions"><button className="button ghost compact" aria-label={`编辑用户：${account.email}`} onClick={() => setEditing(account)}>编辑</button><button className="button ghost compact" aria-label={`重置密码：${account.email}`} onClick={() => setResetting(account)}>重置密码</button></div></td>
          </tr>)}</tbody>
        </table>
        {nextCursor !== "" && <div className="pagination-footer"><button className="button secondary" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? "正在加载…" : "加载更多用户"}</button></div>}
      </div>}

		{creating && <UserEditor api={api} groups={groups} onClose={() => setCreating(false)} onSaved={(created) => { if (matchesQuery(created, appliedQuery)) setUsers((current) => sortUsers([created, ...current], appliedQuery)); setCreating(false); }} />}
    {editing !== null && <UserEditor api={api} groups={groups} account={editing} currentUserID={currentUserID} onClose={() => setEditing(null)} onSaved={(updated) => { replaceUser(updated); setEditing(null); }} />}
    {resetting !== null && <PasswordReset api={api} account={resetting} onClose={() => setResetting(null)} onSaved={(updated) => { replaceUser(updated); setResetting(null); }} />}
  </main>;
}

function UserEditor({ api, groups, account, currentUserID, onClose, onSaved }: {
  api: UsersAPI; groups: ServerGroup[]; account?: AdminUser; currentUserID?: number; onClose: () => void; onSaved: (user: AdminUser) => void;
}) {
  const editing = account !== undefined;
  const [current, setCurrent] = useState(account);
  const [email, setEmail] = useState(account?.email ?? "");
  const [password, setPassword] = useState("");
  const [groupID, setGroupID] = useState(account?.group_id === null || account === undefined ? "" : String(account.group_id));
  const [transferEnable, setTransferEnable] = useState(String(account?.transfer_enable ?? 0));
  const [expiredAt, setExpiredAt] = useState(toLocalDateTime(account?.expired_at ?? null));
  const [speedLimit, setSpeedLimit] = useState(String(account?.speed_limit ?? 0));
  const [deviceLimit, setDeviceLimit] = useState(String(account?.device_limit ?? 0));
  const [banned, setBanned] = useState(account?.banned ?? false);
  const [isAdmin, setIsAdmin] = useState(account?.is_admin ?? false);
  const [isStaff, setIsStaff] = useState(account?.is_staff ?? false);
  const [isDistributor, setIsDistributor] = useState(account?.is_distributor ?? false);
  const [distributorName, setDistributorName] = useState(account?.distributor_name ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);

  const resetFrom = (value: AdminUser) => {
    setCurrent(value); setEmail(value.email); setGroupID(value.group_id === null ? "" : String(value.group_id));
    setTransferEnable(String(value.transfer_enable)); setExpiredAt(toLocalDateTime(value.expired_at));
    setSpeedLimit(String(value.speed_limit)); setDeviceLimit(String(value.device_limit)); setBanned(value.banned);
    setIsAdmin(value.is_admin); setIsStaff(value.is_staff ?? false); setIsDistributor(value.is_distributor ?? false);
    setDistributorName(value.distributor_name ?? "");
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (isDistributor && distributorName.trim() === "") {
      setError("启用分销商角色时必须填写分销商名称");
      return;
    }
    setBusy(true); setError(""); setConflict(false);
    const common = {
      email: email.trim(), group_id: groupID === "" ? null : Number(groupID), transfer_enable: Number(transferEnable),
      expired_at: expiredAt === "" ? null : new Date(expiredAt).toISOString(), speed_limit: Number(speedLimit),
      device_limit: Number(deviceLimit), banned, is_admin: isAdmin, is_staff: isStaff,
      is_distributor: isDistributor, distributor_name: isDistributor ? distributorName.trim() : null
    };
    try {
      const saved = editing && current !== undefined
        ? await api.updateAdminUser(current.id, { ...common, revision: current.revision } satisfies AdminUserUpdateInput)
        : await api.createAdminUser({ ...common, password } satisfies AdminUserCreateInput);
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
      {!editing && <label>初始密码<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} value={password} required onChange={(event) => setPassword(event.target.value)} /></label>}
      <label>权限组<select value={groupID} onChange={(event) => setGroupID(event.target.value)}><option value="">未分组</option>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
      <label>流量额度（字节）<input type="number" min="0" max={Number.MAX_SAFE_INTEGER} step="1" value={transferEnable} required onChange={(event) => setTransferEnable(event.target.value)} /></label>
      <label>到期时间（留空表示不限期）<input type="datetime-local" value={expiredAt} onChange={(event) => setExpiredAt(event.target.value)} /></label>
      <div className="time-grid"><label>限速（Mbps，0 为不限速）<input type="number" min="0" step="1" value={speedLimit} required onChange={(event) => setSpeedLimit(event.target.value)} /></label><label>设备数（0 为不限设备）<input type="number" min="0" max="1000" step="1" value={deviceLimit} required onChange={(event) => setDeviceLimit(event.target.value)} /></label></div>
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

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}

function matchesQuery(account: AdminUser, query: AdminUserQuery): boolean {
	return (query.email_prefix === undefined || account.email.toLowerCase().startsWith(query.email_prefix.toLowerCase())) &&
		(query.banned === undefined || account.banned === query.banned) &&
		(query.group_id === undefined || account.group_id === query.group_id);
}

function sortUsers(users: AdminUser[], query: AdminUserQuery): AdminUser[] {
	return [...users].sort(query.email_prefix === undefined
		? (left, right) => right.id - left.id
		: (left, right) => left.email.localeCompare(right.email) || left.id - right.id);
}

function roleSummary(account: AdminUser): string {
  const roles = [account.is_admin ? "管理员" : "", account.is_staff ? "员工" : "", account.is_distributor ? "分销商" : ""].filter(Boolean);
  return roles.length === 0 ? "" : ` · ${roles.join(" · ")}`;
}
