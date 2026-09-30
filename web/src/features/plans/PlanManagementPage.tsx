import { translateAdmin } from "../../lib/adminLocale";
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { SafeMarkdown } from "../../components/SafeMarkdown";

import { NodeGroupCreator } from "../nodes/NodeGroupCreator";
import { TagInput } from "../../components/TagInput";
import { MarkdownEditor } from "../../components/MarkdownEditor";
import { Modal } from "../../components/Overlay";
import type { AdminAPI, Plan, PlanInput, PlanPeriod, PlanPrices, ServerGroup } from "../../lib/api";
import { invalidateAdminData } from "../../lib/useAdminData";
import { adminDataCache } from "../../lib/dataPrefetchCache";
import "./PlanManagementPage.css";

type PlansAPI = Pick<AdminAPI, "listPlans" | "createPlan" | "updatePlan" | "setPlanState" | "reorderPlans" | "deletePlan" | "listServerGroups"> & Partial<Pick<AdminAPI, "createServerGroup">>;

const periods: Array<{ key: PlanPeriod; label: string }> = [
  { key: "monthly", label: "月付" }, { key: "quarterly", label: "季付" }, { key: "half_yearly", label: "半年付" },
  { key: "yearly", label: "年付" }, { key: "two_yearly", label: "两年付" }, { key: "three_yearly", label: "三年付" },
  { key: "onetime", label: "流量包" }, { key: "reset_traffic", label: "重置包" },
];

const PAGE_SIZES = [10, 20, 50];
const maxPlanPriceCents = 9_000_000_000_000_000n;
const planDescriptionTemplate = `## 套餐特点\n• 高速稳定的全球网络接入\n• 支持多设备同时在线\n• 无限制的流量重置\n\n## 使用说明\n1. 支持设备：iOS、Android、Windows、macOS\n2. 24/7 技术支持\n3. 自动定期流量重置\n\n## 注意事项\n- 禁止滥用\n- 遵守当地法律法规\n- 支持随时更换套餐`;

export function PlanManagementPage({ api }: { api: PlansAPI }) {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [groups, setGroups] = useState<ServerGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Plan | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Plan | null>(null);
  const [sorting, setSorting] = useState(false);
  const [savingOrder, setSavingOrder] = useState(false);
  const [busyIDs, setBusyIDs] = useState<Set<number>>(() => new Set());
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

  const refresh = useCallback(async () => {
    invalidateAdminData("plans");
    setLoading(true);
    setError("");
    try {
      const [nextPlans, nextGroups] = await Promise.all([api.listPlans(), api.listServerGroups()]);
      setPlans(nextPlans);
      setGroups(nextGroups);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    // Use prefetched data if available (from hover), otherwise fetch normally
    const source = adminDataCache.consume<[Plan[], ServerGroup[]]>("plans")
      ?? Promise.all([api.listPlans(), api.listServerGroups()]);
    void source.then(([nextPlans, nextGroups]) => {
      if (!active) return;
      setPlans(nextPlans);
      setGroups(nextGroups);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (query === "") return plans;
    return plans.filter((plan) =>
      plan.name.toLocaleLowerCase().includes(query) ||
      plan.tags.some((tag) => tag.toLocaleLowerCase().includes(query))
    );
  }, [plans, search]);

  const groupNames = useMemo(() => new Map(groups.map((group) => [group.id, group.name])), [groups]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const currentPage = Math.min(page, totalPages);
  const paged = sorting ? filtered : filtered.slice((currentPage - 1) * pageSize, currentPage * pageSize);
  const goPage = (p: number) => setPage(Math.max(1, Math.min(p, totalPages)));

  const updateState = async (plan: Plan, field: "show" | "sell" | "renew", value: boolean) => {
    setBusyIDs((cur) => new Set(cur).add(plan.id));
    setError("");
    setPlans((cur) => cur.map((item) => item.id === plan.id ? { ...item, [field]: value } : item));
    try {
      const state = { show: plan.show, sell: plan.sell, renew: plan.renew, [field]: value };
      const updated = await api.setPlanState(plan.id, plan.revision, state.show, state.sell, state.renew);
      setPlans((cur) => cur.map((item) => item.id === updated.id ? updated : item));
    } catch (cause) {
      setPlans((cur) => cur.map((item) => item.id === plan.id ? plan : item));
      setError(messageOf(cause));
    } finally {
      setBusyIDs((cur) => { const next = new Set(cur); next.delete(plan.id); return next; });
    }
  };

  const move = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= plans.length) return;
    setPlans((cur) => {
      const next = [...cur];
      const a = next[index]; const b = next[target];
      if (a === undefined || b === undefined) return cur;
      next[index] = b; next[target] = a;
      return next;
    });
  };

  const saveOrder = async () => {
    setSavingOrder(true);
    setError("");
    try {
      setPlans(await api.reorderPlans(plans.map((plan) => plan.id)));
      setSorting(false);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSavingOrder(false);
    }
  };

  return (
    <main className="page-shell resource-page pm-page">
      {/* Header */}
      <header className="pm-header">
        <h1 className="pm-title">{translateAdmin("订阅套餐")}</h1>
        <p className="pm-subtitle">{translateAdmin("在这里可以配置订阅计划，包括添加、删除、编辑等操作。")}</p>
      </header>

      {/* Toolbar */}
      <div className="pm-toolbar">
        <div className="pm-toolbar-left">
          <button className="button secondary compact pm-add-btn" onClick={() => setEditing(null)}>
            <span className="pm-plus" aria-hidden="true">+</span>{translateAdmin("添加套餐")}</button>
          <button
            className={`button secondary compact pm-sort-btn${sorting ? " pm-sort-active" : ""}`}
            onClick={() => { setSorting((v) => !v); setPage(1); }}
          >
            {sorting ? "退出排序" : translateAdmin("编辑排序")}
          </button>
          {sorting && (
            <button
              className="button primary compact"
              disabled={savingOrder}
              onClick={() => void saveOrder()}
            >
              {savingOrder ? "正在保存…" : translateAdmin("保存排序")}
            </button>
          )}
        </div>
        <input
          className="pm-search"
          type="search"
          placeholder={translateAdmin("搜索套餐...")}
          value={search}
          onChange={(e) => { setSearch(e.target.value); setPage(1); }}
          aria-label="搜索套餐"
        />
      </div>

      {/* Error */}
      {error !== "" && (
        <div className="alert error resource-alert" role="alert">
          {error}
          <button className="button ghost compact" onClick={() => void refresh()}>{translateAdmin("刷新")}</button>
        </div>
      )}

      {/* Table — always rendered */}
      {loading ? (
        <div className="empty-card">正在加载套餐…</div>
      ) : (
        <section className="resource-table-wrap pm-table-wrap" aria-label="套餐列表">
          <table className="resource-table pm-table">
            <thead>
              <tr>
                <th>ID</th><th>{translateAdmin("显示")}</th><th>{translateAdmin("新购")}</th><th>{translateAdmin("续费")}</th>
                <th className="pm-th-plan">{translateAdmin("名称")}</th><th className="pm-th-stat">{translateAdmin("统计")}</th>
                <th>{translateAdmin("权限组")}</th><th className="pm-th-price">{translateAdmin("价格")}</th><th className="pm-th-ops">{translateAdmin("操作")}</th>
              </tr>
            </thead>
            <tbody>
              {paged.length === 0 ? (
                <tr className="pm-empty-row">
                  <td colSpan={9} className="pm-empty-cell">{search ? "没有匹配的套餐。" : plans.length === 0 ? "尚未创建套餐。" : translateAdmin("暂无数据")}</td>
                </tr>
              ) : paged.map((plan) => {
                const sourceIndex = plans.findIndex((item) => item.id === plan.id);
                const busy = busyIDs.has(plan.id);
                return (
                  <tr key={plan.id} className={busy ? "pm-row-busy" : ""}>
                    <td data-label="ID">{plan.id}</td>
                    {([['show', '显示'], ['sell', '新购'], ['renew', '续费']] as const).map(([field, label]) => <td key={field} data-label={label}>
                      <label className="pm-switch-label" title={label}>
                        <input type="checkbox" role="switch" aria-label={label} className="pm-switch-input" disabled={busy} checked={plan[field]} onChange={event => void updateState(plan, field, event.target.checked)} />
                        <span className="pm-switch-track" aria-hidden="true" />
                      </label>
                    </td>)}
                    <td data-label="名称" className="pm-td-plan"><strong className="pm-plan-name">{plan.name}</strong></td>
                    <td data-label="统计" className="pm-td-stat"><span title="总用户数">{plan.users_count}</span><span title="有效用户数">{plan.active_users_count}</span></td>
                    <td data-label="权限组">{plan.group_id === null ? '' : groupNames.get(plan.group_id) ?? `#${plan.group_id}`}</td>
                    {/* 价格 */}
                    <td data-label="价格" className="pm-td-price">
                      {periods.some(({ key }) => plan.prices[key] !== undefined) ? (
                        <div className="pm-price-list">
                          {periods.map(({ key, label }) => plan.prices[key] === undefined ? null : (
                            <span key={key} className="pm-price-item">
                              <span className="pm-price-label">{label}</span>
                              <span className="pm-price-val">¥{formatCents(plan.prices[key] ?? 0)}</span>
                            </span>
                          ))}
                        </div>
                      ) : <span className="muted">未设置</span>}
                    </td>
                    {/* 操作 */}
                    <td data-label="操作" className="pm-td-ops">
                      <div className="row-actions pm-row-actions">
                        {sorting ? (
                          <>
                            <button
                              className="pm-icon-btn"
                              disabled={sourceIndex === 0}
                              aria-label={`上移套餐：${plan.name}`}
                              title="上移"
                              onClick={() => move(sourceIndex, -1)}
                            >
                              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M18 15l-6-6-6 6"/>
                              </svg>
                            </button>
                            <button
                              className="pm-icon-btn"
                              disabled={sourceIndex === plans.length - 1}
                              aria-label={`下移套餐：${plan.name}`}
                              title="下移"
                              onClick={() => move(sourceIndex, 1)}
                            >
                              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M6 9l6 6 6-6"/>
                              </svg>
                            </button>
                          </>
                        ) : (
                          <>
                            <button
                              className="pm-icon-btn"
                              aria-label={`编辑套餐：${plan.name}`}
                              title={translateAdmin("编辑")}
                              onClick={() => setEditing(plan)}
                            >
                              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                                <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                              </svg>
                            </button>
                            <button
                              className="pm-icon-btn pm-icon-btn-danger"
                              aria-label={`删除套餐：${plan.name}`}
                              title={translateAdmin("删除")}
                              onClick={() => setDeleting(plan)}
                            >
                              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                                <polyline points="3 6 5 6 21 6"/>
                                <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                                <path d="M10 11v6M14 11v6"/>
                                <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
                              </svg>
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      )}

      {/* Footer / Pagination — always visible */}
      {!loading && (
        <div className="pm-footer">
          <span className="pm-footer-status">已选择 0 项，共 {filtered.length} 项</span>
          {!sorting && (
            <div className="pm-pagination">
              <label className="pm-page-size-label">{translateAdmin("每页显示")}<select
                  className="pm-page-size-select"
                  value={pageSize}
                  onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}
                  aria-label="每页显示条数"
                >
                  {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
                </select>
              </label>
              <span className="pm-page-info">{translateAdmin("第")}<input
                  className="pm-page-input"
                  type="number"
                  min={1}
                  max={Math.max(1, totalPages)}
                  value={currentPage}
                  onChange={(e) => goPage(Number(e.target.value))}
                  aria-label="页码"
                />
                页，共 {totalPages} 页
              </span>
              <div className="pm-page-btns">
                <button className="pm-page-btn" onClick={() => goPage(1)} disabled={currentPage === 1 || filtered.length === 0} aria-label="首页">«</button>
                <button className="pm-page-btn" onClick={() => goPage(currentPage - 1)} disabled={currentPage === 1 || filtered.length === 0} aria-label={translateAdmin("上一页")}>‹</button>
                <button className="pm-page-btn" onClick={() => goPage(currentPage + 1)} disabled={currentPage === totalPages || filtered.length === 0} aria-label={translateAdmin("下一页")}>›</button>
                <button className="pm-page-btn" onClick={() => goPage(totalPages)} disabled={currentPage === totalPages || filtered.length === 0} aria-label="末页">»</button>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Modals */}
      {editing !== undefined && (
        <PlanEditor
          api={api}
          groups={groups}
          plan={editing}
          onClose={() => setEditing(undefined)}
          onSaved={(saved) => {
            setPlans((cur) => editing === null ? [...cur, saved] : cur.map((item) => item.id === saved.id ? saved : item));
            setEditing(undefined);
          }}
        />
      )}
      {deleting !== null && (
        <PlanDelete
          api={api}
          plan={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            setPlans((cur) => cur.filter((item) => item.id !== deleting.id));
            setDeleting(null);
          }}
        />
      )}
    </main>
  );
}

type PlanDraft = Omit<PlanInput, "prices" | "tags" | "transfer_enable"> & { transfer_enable: number | ""; tagsText: string; prices: Record<PlanPeriod, string>; forceUpdate: boolean };

function PlanEditor({ api, groups, plan, onClose, onSaved }: {
  api: PlansAPI; groups: ServerGroup[]; plan: Plan | null; onClose: () => void; onSaved: (plan: Plan) => void;
}) {
  const isCreate = plan === null;
  const title = isCreate ? "添加套餐" : "编辑套餐";
  const [draft, setDraft] = useState<PlanDraft>(() => planDraft(plan));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [previewing, setPreviewing] = useState(false);
  const [groupCreating, setGroupCreating] = useState(false);
  const [basePrice, setBasePrice] = useState("");
  const [createdGroups, setCreatedGroups] = useState<ServerGroup[]>([]);
  const update = <K extends keyof PlanDraft,>(key: K, value: PlanDraft[K]) => setDraft((cur) => ({ ...cur, [key]: value }));

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const input: PlanInput = {
        group_id: draft.group_id, transfer_enable: Number(draft.transfer_enable), name: draft.name,
        speed_limit: draft.speed_limit, content: draft.content,
        reset_traffic_method: draft.reset_traffic_method, capacity_limit: draft.capacity_limit,
        prices: pricesToCents(draft.prices), device_limit: draft.device_limit,
        distributor_hwid_limit: draft.distributor_hwid_limit,
        tags: draft.tagsText.split(/[,，\n]/).map((tag) => tag.trim()).filter(Boolean),
      };
      onSaved(isCreate ? await api.createPlan(input) : await api.updatePlan(plan.id, plan.revision, input, draft.forceUpdate));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal title={title} className="pm-editor-modal" onClose={onClose} suspended={groupCreating}>
      <div className="pm-modal-header">
        <div className="pm-modal-title-row">
          <h2 className="pm-modal-title">{title}</h2>
          <button className="pm-modal-close" aria-label={`关闭${title}`} onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none"><path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/></svg>
          </button>
        </div>
      </div>
      <form className="pm-modal-body" onSubmit={(e) => void submit(e)}>
        <div className="pm-form-scroll"><div className="pm-details-grid">
        {/* 基本信息 */}
        <div className="pm-form-section">
          <div className="pm-form-grid pm-form-grid-2">
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-name">{translateAdmin("套餐名称")}</label>
              <input id="pm-name" className="pm-field-input" autoFocus required maxLength={255}
                value={draft.name} placeholder={translateAdmin("请输入套餐名称")}
                onChange={(e) => update("name", e.target.value)} />
            </div>
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-tags">{translateAdmin("标签")}</label>
              <TagInput id="pm-tags" label={translateAdmin("标签")} value={draft.tagsText.split(/[,，\n]/).map(tag => tag.trim()).filter(Boolean)} onChange={tags => update("tagsText", tags.join(", "))} />
            </div>
          </div>
          <div className="pm-form-field">
            <div className="pm-group-label"><label className="pm-field-label" htmlFor="pm-group">{translateAdmin("服务器分组")}</label>{api.createServerGroup && <NodeGroupCreator buttonLabel={translateAdmin("添加分组")} create={name => api.createServerGroup!(name)} onOpenChange={setGroupCreating} onCreated={group => { setCreatedGroups(current => [...current, group]); update("group_id", group.id); }} />}</div>
            <select id="pm-group" className="pm-field-select"
              value={draft.group_id ?? ""}
              onChange={(e) => update("group_id", e.target.value === "" ? null : Number(e.target.value))}>
              <option value="">{translateAdmin("请选择服务器分组")}</option>
              {[...groups, ...createdGroups].map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </div>
        </div>

        {/* 权益配置 */}
        <div className="pm-form-section">
          <div className="pm-form-grid pm-form-grid-4">
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-transfer">流量（GB）</label>
              <input placeholder={translateAdmin("请输入流量限制")} id="pm-transfer" className="pm-field-input" type="number" min={1} required
                value={draft.transfer_enable}
                onChange={(e) => update("transfer_enable", e.target.value === "" ? "" : Number(e.target.value))} />
            </div>
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-speed">{translateAdmin("速度限制")}</label>
              <input id="pm-speed" className="pm-field-input" type="number" min={0} max={1_000_000_000}
                value={draft.speed_limit ?? ""} placeholder={translateAdmin("请输入速度限制")}
                onChange={(e) => update("speed_limit", e.target.value === "" ? null : Number(e.target.value))} />
            </div>
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-device">{translateAdmin("设备限制")}</label>
              <input id="pm-device" className="pm-field-input" type="number" min={0} max={1000}
                value={draft.device_limit ?? ""} placeholder={translateAdmin("请输入设备限制")}
                onChange={(e) => update("device_limit", e.target.value === "" ? null : Number(e.target.value))} />
            </div>
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-hwid">分销 HWID 设备上限</label>
              <input id="pm-hwid" className="pm-field-input" type="number" min={1} max={100} required
                value={draft.distributor_hwid_limit ?? 1}
                onChange={(e) => update("distributor_hwid_limit", Number(e.target.value))} />
            </div>
            <div className="pm-form-field">
              <label className="pm-field-label" htmlFor="pm-capacity">{translateAdmin("容量限制")}</label>
              <input id="pm-capacity" className="pm-field-input" type="number" min={0} max={1_000_000_000}
                value={draft.capacity_limit ?? ""} placeholder={translateAdmin("请输入容量限制")}
                onChange={(e) => update("capacity_limit", e.target.value === "" ? null : Number(e.target.value))} />
            </div>
          </div>
          <div className="pm-form-field">
            <label className="pm-field-label" htmlFor="pm-reset-method">{translateAdmin("流量重置方式")}</label>
            <select id="pm-reset-method" className="pm-field-select"
              value={draft.reset_traffic_method ?? ""}
              onChange={(e) => update("reset_traffic_method", e.target.value === "" ? null : Number(e.target.value))}>
              <option value="">{translateAdmin("跟随系统")}</option>
              <option value={0}>每月 1 日</option>
              <option value={1}>按到期日每月</option>
              <option value={2}>永不重置</option>
              <option value={3}>每年 1 月 1 日</option>
              <option value={4}>按到期月日每年</option>
            </select>
          </div>
        </div>

        </div>
        {/* 周期价格 */}
        <div className="pm-form-section">
          <div className="pm-section-title-row"><div className="pm-section-title">{translateAdmin("价格设置")}</div><label className="pm-base-price"><span aria-hidden="true">¥</span><input aria-label={translateAdmin("基础价格")} placeholder={translateAdmin("基础价格")} type="number" min={0} step="0.01" value={basePrice} onChange={event => {
            const value = event.target.value; setBasePrice(value);
            const amount = Number(value);
            if (value.trim() === "" || !Number.isFinite(amount) || amount < 0) return;
            const factors: Record<PlanPeriod, [number, number]> = { monthly: [1, 1], quarterly: [3, 0.95], half_yearly: [6, 0.9], yearly: [12, 0.85], two_yearly: [24, 0.8], three_yearly: [36, 0.75], onetime: [1, 1], reset_traffic: [1, 1] };
            update("prices", Object.fromEntries(periods.map(({ key }) => [key, (amount * factors[key][0] * factors[key][1]).toFixed(2)])) as Record<PlanPeriod, string>);
          }} /></label></div>
          <div className="pm-form-grid pm-form-grid-4">
            {periods.map((period) => (
              <div key={period.key} className={`pm-form-field pm-price-${period.key}`}>
                <label className="pm-field-label" htmlFor={`pm-price-${period.key}`}>{period.label}</label>
                <input
                  id={`pm-price-${period.key}`}
                  className="pm-field-input"
                  type="number" min={0} step="0.01" inputMode="decimal"
                  value={draft.prices[period.key]}
                  placeholder=""
                  onChange={(e) => update("prices", { ...draft.prices, [period.key]: e.target.value })}
                />
                {period.key === "onetime" && <small>{translateAdmin("一次性流量包，无时间限制")}</small>}
                {period.key === "reset_traffic" && <small>{translateAdmin("重置流量包，可多次使用")}</small>}
              </div>
            ))}
          </div>
        </div>

        {/* 套餐说明 */}
        <div className="pm-form-section">
          <div className="pm-section-title-row">
            <span className="pm-section-title">{translateAdmin("套餐说明")}</span>
            <div className="pm-section-actions">
              <button className="pm-text-btn" type="button"
                onClick={() => update("content", planDescriptionTemplate)}>{translateAdmin("使用模板")}</button>
              <button className="pm-text-btn" type="button"
                onClick={() => setPreviewing((v) => !v)}>
                {previewing ? translateAdmin("隐藏预览") : translateAdmin("显示预览")}
              </button>
            </div>
          </div>
          <label className="sr-only" htmlFor="pm-content">{translateAdmin("套餐说明")}</label>
          <MarkdownEditor id="pm-content" label={translateAdmin("套餐说明")} value={draft.content} onChange={value => update("content", value)} />
          {previewing && (
            <div className="pm-preview markdown-body" aria-label="套餐说明预览">
              <SafeMarkdown>{draft.content}</SafeMarkdown>
            </div>
          )}
        </div>

        {/* 强制同步（编辑时显示） */}
        {!isCreate && (
          <div className="pm-form-section">
            <label className="pm-switch-label pm-force-sync">
              <input type="checkbox" className="pm-switch-input"
                checked={draft.forceUpdate}
                onChange={(e) => update("forceUpdate", e.target.checked)} />
              <span className="pm-switch-track" aria-hidden="true" />
              <span className="pm-switch-text">强制同步套餐权益到现有用户</span>
            </label>
          </div>
        )}

        {error !== "" && <div className="alert error" role="alert">{error}</div>}

        </div>
        <div className="pm-modal-footer">
          <button className="button ghost" type="button" onClick={onClose}>{translateAdmin("取消")}</button>
          <button className="button primary" disabled={saving} type="submit">
            {saving ? "正在提交…" : translateAdmin("提交")}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function PlanDelete({ api, plan, onClose, onDeleted }: {
  api: PlansAPI; plan: Plan; onClose: () => void; onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true); setError("");
    try { await api.deletePlan(plan.id); onDeleted(); }
    catch (cause) { setError(messageOf(cause)); setBusy(false); }
  };
  return (
    <Modal title="删除套餐" onClose={onClose}>
      <div className="pm-modal-header">
        <div className="pm-modal-title-row">
          <h2 className="pm-modal-title">删除套餐</h2>
          <button className="pm-modal-close" aria-label="关闭删除套餐" onClick={onClose}>
            <svg viewBox="0 0 15 15" fill="none"><path d="M11.7816 4.03157C12.0062 3.80702 12.0062 3.44295 11.7816 3.2184C11.5571 2.99385 11.193 2.99385 10.9685 3.2184L7.50005 6.68682L4.03164 3.2184C3.80708 2.99385 3.44301 2.99385 3.21846 3.2184C2.99391 3.44295 2.99391 3.80702 3.21846 4.03157L6.68688 7.49999L3.21846 10.9684C2.99391 11.193 2.99391 11.557 3.21846 11.7816C3.44301 12.0061 3.80708 12.0061 4.03164 11.7816L7.50005 8.31316L10.9685 11.7816C11.193 12.0061 11.5571 12.0061 11.7816 11.7816C12.0062 11.557 12.0062 11.193 11.7816 10.9684L8.31322 7.49999L11.7816 4.03157Z" fill="currentColor" fillRule="evenodd" clipRule="evenodd"/></svg>
          </button>
        </div>
      </div>
      <div className="pm-modal-body pm-delete-body">
        <p className="pm-delete-text">确定删除套餐 <strong>"{plan.name}"</strong> 吗？仍被用户或业务记录引用时，服务端会拒绝删除。</p>
        {error !== "" && <div className="alert error" role="alert">{error}</div>}
        <div className="pm-modal-footer">
          <button className="button ghost" onClick={onClose}>{translateAdmin("取消")}</button>
          <button className="button primary destructive" disabled={busy} onClick={() => void remove()}>
            {busy ? "正在删除…" : translateAdmin("确认删除")}
          </button>
        </div>
      </div>
    </Modal>
  );
}

function planDraft(plan: Plan | null): PlanDraft {
  const prices = Object.fromEntries(periods.map(({ key }) => {
    const cents = plan?.prices[key];
    return [key, cents === undefined ? "" : formatCents(cents)];
  })) as Record<PlanPeriod, string>;
  return {
    group_id: plan?.group_id ?? null, transfer_enable: plan?.transfer_enable ?? "",
    name: plan?.name ?? "", speed_limit: plan?.speed_limit ?? null,
    content: plan?.content ?? "", reset_traffic_method: plan?.reset_traffic_method ?? null,
    capacity_limit: plan?.capacity_limit ?? null, prices,
    distributor_hwid_limit: plan?.distributor_hwid_limit ?? 1,
    device_limit: plan?.device_limit ?? null, tagsText: plan?.tags.join(", ") ?? "",
    forceUpdate: false,
  };
}

function pricesToCents(values: Record<PlanPeriod, string>): PlanPrices {
  const prices: PlanPrices = {};
  for (const { key } of periods) {
    const raw = values[key].trim();
    if (raw === "") continue;
    const match = /^(\d+)(?:\.(\d{1,2}))?$/.exec(raw);
    const label = periods.find((p) => p.key === key)?.label ?? key;
    if (match === null) throw new Error(`${label}价格无效`);
    const whole = match[1];
    if (whole === undefined) throw new Error(`${label}价格无效`);
    const cents = BigInt(whole) * 100n + BigInt((match[2] ?? "").padEnd(2, "0") || "0");
    if (cents > maxPlanPriceCents) throw new Error(`${label}价格超出范围`);
    prices[key] = Number(cents);
  }
  return prices;
}

function formatCents(cents: number): string {
  return `${Math.trunc(cents / 100)}.${String(cents % 100).padStart(2, "0")}`;
}


function messageOf(cause: unknown): string { return cause instanceof Error ? cause.message : "套餐请求失败"; }
