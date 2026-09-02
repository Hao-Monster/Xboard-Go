import { useCallback, useEffect, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type {
  GiftCardCode, GiftCardCodeInput, GiftCardCodePage, GiftCardCodeStatus, GiftCardConditions, GiftCardLimits, GiftCardReward, GiftCardSpecialConfig,
  GiftCardStatistics, GiftCardTemplate, GiftCardTemplateInput, GiftCardTemplatePage, GiftCardType,
  GiftCardUsagePage, Plan
} from "../../lib/api";
import { useCurrency } from "../../lib/currency";

export interface GiftCardManagementAPI {
  listGiftCardTemplates: (page?: number, pageSize?: number, type?: GiftCardType, status?: boolean) => Promise<GiftCardTemplatePage>;
  createGiftCardTemplate: (input: GiftCardTemplateInput) => Promise<GiftCardTemplate>;
  updateGiftCardTemplate: (id: number, input: GiftCardTemplateInput) => Promise<GiftCardTemplate>;
  deleteGiftCardTemplate: (id: number) => Promise<void>;
  generateGiftCardCodes: (templateID: number, count: number, prefix: string, expiresAt: number | null, maxUsage: number) => Promise<GiftCardCode[]>;
  generateGiftCardCodesCSV: (templateID: number, count: number, prefix: string, expiresAt: number | null, maxUsage: number) => Promise<Blob>;
  listGiftCardCodes: (page?: number, pageSize?: number, search?: string, templateID?: number, status?: GiftCardCodeStatus, batchNo?: string) => Promise<GiftCardCodePage>;
  updateGiftCardCode: (id: number, input: GiftCardCodeInput) => Promise<GiftCardCode>;
  exportGiftCardCodes: (batchNo?: string) => Promise<Blob>;
  toggleGiftCardCode: (id: number) => Promise<GiftCardCode>;
  deleteGiftCardCode: (id: number) => Promise<void>;
  listGiftCardUsages: (page?: number, pageSize?: number, userID?: number, templateID?: number, codeID?: number) => Promise<GiftCardUsagePage>;
  getGiftCardStatistics: () => Promise<GiftCardStatistics>;
  listPlans: () => Promise<Plan[]>;
}

type Tab = "templates" | "codes" | "usages" | "statistics";
const typeNames: Record<GiftCardType, string> = { 1: "通用礼品卡", 2: "套餐礼品卡", 3: "盲盒礼品卡" };
const codeStatusNames = ["可用", "已用完", "已禁用", "已过期"];

export function GiftCardManagementPage({ api }: { api: GiftCardManagementAPI }) {
  const [tab, setTab] = useState<Tab>("templates");
  const [templates, setTemplates] = useState<GiftCardTemplatePage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [codes, setCodes] = useState<GiftCardCodePage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [usages, setUsages] = useState<GiftCardUsagePage>({ items: [], total: 0, page: 1, page_size: 20 });
  const [statistics, setStatistics] = useState<GiftCardStatistics | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [templateOptions, setTemplateOptions] = useState<GiftCardTemplate[]>([]);
  const [templateOptionsLoading, setTemplateOptionsLoading] = useState(false);
  const [editing, setEditing] = useState<GiftCardTemplate | null | undefined>(undefined);
  const [editingCode, setEditingCode] = useState<GiftCardCode | undefined>(undefined);
  const [generating, setGenerating] = useState(false);
  const [templateType, setTemplateType] = useState(""); const [templateStatus, setTemplateStatus] = useState("");
  const [codeSearch, setCodeSearch] = useState(""); const [codeTemplate, setCodeTemplate] = useState(""); const [codeStatus, setCodeStatus] = useState(""); const [codeBatch, setCodeBatch] = useState("");
  const [usageUser, setUsageUser] = useState(""); const [usageTemplate, setUsageTemplate] = useState(""); const [usageCode, setUsageCode] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true); setError("");
    try {
      if (tab === "templates") setTemplates(await api.listGiftCardTemplates(templates.page, 20, optionalGiftType(templateType), optionalBoolean(templateStatus)));
      if (tab === "codes") setCodes(await api.listGiftCardCodes(codes.page, 20, codeSearch, optionalNumber(codeTemplate), optionalCodeStatus(codeStatus), codeBatch));
      if (tab === "usages") setUsages(await api.listGiftCardUsages(usages.page, 20, optionalNumber(usageUser), optionalNumber(usageTemplate), optionalNumber(usageCode)));
      if (tab === "statistics") setStatistics(await api.getGiftCardStatistics());
    } catch (cause) { setError(cause instanceof Error ? cause.message : "礼品卡数据加载失败"); }
    finally { setLoading(false); }
  }, [api, codeBatch, codeSearch, codeStatus, codeTemplate, codes.page, tab, templateStatus, templateType, templates.page, usageCode, usageTemplate, usageUser, usages.page]);

  useEffect(() => {
    let active = true;
    const request = tab === "templates" ? api.listGiftCardTemplates(templates.page, 20, optionalGiftType(templateType), optionalBoolean(templateStatus)) : tab === "codes" ? api.listGiftCardCodes(codes.page, 20, codeSearch, optionalNumber(codeTemplate), optionalCodeStatus(codeStatus), codeBatch) : tab === "usages" ? api.listGiftCardUsages(usages.page, 20, optionalNumber(usageUser), optionalNumber(usageTemplate), optionalNumber(usageCode)) : api.getGiftCardStatistics();
    void request.then((value) => {
      if (!active) return;
      setError("");
      if (tab === "templates") setTemplates(value as GiftCardTemplatePage);
      if (tab === "codes") setCodes(value as GiftCardCodePage);
      if (tab === "usages") setUsages(value as GiftCardUsagePage);
      if (tab === "statistics") setStatistics(value as GiftCardStatistics);
    }).catch((cause: unknown) => { if (active) setError(cause instanceof Error ? cause.message : "礼品卡数据加载失败"); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, codeBatch, codeSearch, codeStatus, codeTemplate, codes.page, tab, templateStatus, templateType, templates.page, usageCode, usageTemplate, usageUser, usages.page]);
  useEffect(() => { void api.listPlans().then(setPlans).catch((cause: unknown) => setError(cause instanceof Error ? cause.message : "套餐数据加载失败")); }, [api]);
  useEffect(() => {
    if (tab !== "codes") return;
    let active = true;
    void loadGiftCardTemplateOptions(api).then((items) => { if (active) setTemplateOptions(items); })
      .catch((cause: unknown) => { if (active) setError(cause instanceof Error ? cause.message : "礼品卡模板选项加载失败"); })
      .finally(() => { if (active) setTemplateOptionsLoading(false); });
    return () => { active = false; };
  }, [api, tab]);

  const removeTemplate = async (item: GiftCardTemplate) => {
    if (!window.confirm(`确认删除模板“${item.name}”？`)) return;
    try { await api.deleteGiftCardTemplate(item.id); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : "删除失败"); }
  };
  const toggleCode = async (item: GiftCardCode) => {
    try { const updated = await api.toggleGiftCardCode(item.id); setCodes((current) => ({ ...current, items: current.items.map((code) => code.id === updated.id ? updated : code) })); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "状态更新失败"); }
  };
  const removeCode = async (item: GiftCardCode) => {
    if (!window.confirm(`确认删除兑换码 ${item.code}？`)) return;
    try { await api.deleteGiftCardCode(item.id); await load(); } catch (cause) { setError(cause instanceof Error ? cause.message : "删除失败"); }
  };
  const exportCodes = async (batch = codeBatch) => {
    if (batch.trim() === "") { setError("请先填写批次号，或使用列表中的“导出批次”"); return; }
    try { downloadBlob(await api.exportGiftCardCodes(batch), batch === "" ? "gift-card-codes.csv" : `gift-card-${batch}.csv`); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "导出失败"); }
  };
  const switchTab = (next: Tab) => {
    if (next === tab) return;
    setLoading(true);
    if (next === "codes") setTemplateOptionsLoading(true);
    setTab(next);
  };

  return <main className="content gift-card-management">
    <header className="page-heading"><div><p className="eyebrow">Finance</p><h1>礼品卡管理</h1><p className="muted">模板、兑换码、使用记录与统计采用与 Xboard 一致的业务口径。</p></div>
      <div className="row-actions">{tab === "templates" && <button className="button primary" onClick={() => setEditing(null)}>添加模板</button>}{tab === "codes" && <><button className="button primary" disabled={templateOptionsLoading} onClick={() => setGenerating(true)}>{templateOptionsLoading ? "正在加载模板…" : "生成兑换码"}</button><button className="button secondary" disabled={codeBatch.trim() === ""} onClick={() => void exportCodes()}>导出筛选批次</button></>}<button className="button secondary" onClick={() => void load()}>刷新</button></div>
    </header>
    <nav className="tab-list" aria-label="礼品卡功能"><button aria-current={tab === "templates" ? "page" : undefined} onClick={() => switchTab("templates")}>模板管理</button><button aria-current={tab === "codes" ? "page" : undefined} onClick={() => switchTab("codes")}>兑换码管理</button><button aria-current={tab === "usages" ? "page" : undefined} onClick={() => switchTab("usages")}>使用记录</button><button aria-current={tab === "statistics" ? "page" : undefined} onClick={() => switchTab("statistics")}>统计数据</button></nav>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    {tab === "templates" && <div className="filter-bar"><label>模板类型<select value={templateType} onChange={(event) => { setTemplates((current) => ({ ...current, page: 1 })); setTemplateType(event.target.value); }}><option value="">全部</option>{Object.entries(typeNames).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label><label>模板状态<select value={templateStatus} onChange={(event) => { setTemplates((current) => ({ ...current, page: 1 })); setTemplateStatus(event.target.value); }}><option value="">全部</option><option value="true">启用</option><option value="false">禁用</option></select></label></div>}
    {tab === "codes" && <div className="filter-bar"><label>搜索兑换码<input value={codeSearch} onChange={(event) => { setCodes((current) => ({ ...current, page: 1 })); setCodeSearch(event.target.value); }} /></label><label>模板<select value={codeTemplate} onChange={(event) => { setCodes((current) => ({ ...current, page: 1 })); setCodeTemplate(event.target.value); }}><option value="">全部</option>{templateOptions.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label><label>兑换码状态<select value={codeStatus} onChange={(event) => { setCodes((current) => ({ ...current, page: 1 })); setCodeStatus(event.target.value); }}><option value="">全部</option>{codeStatusNames.map((label, value) => <option value={value} key={label}>{label}</option>)}</select></label><label>批次号<input value={codeBatch} onChange={(event) => { setCodes((current) => ({ ...current, page: 1 })); setCodeBatch(event.target.value); }} /></label></div>}
    {tab === "usages" && <div className="filter-bar"><label>用户 ID<input type="number" min={1} value={usageUser} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageUser(event.target.value); }} /></label><label>模板 ID<input type="number" min={1} value={usageTemplate} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageTemplate(event.target.value); }} /></label><label>兑换码 ID<input type="number" min={1} value={usageCode} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageCode(event.target.value); }} /></label></div>}
    {loading ? <div className="empty-state">正在加载…</div> : <>
      {tab === "templates" && <><TemplateTable page={templates} onEdit={setEditing} onDelete={(item) => void removeTemplate(item)} /><Pagination page={templates.page} total={templates.total} pageSize={templates.page_size} onPage={(page) => { setLoading(true); setTemplates((current) => ({ ...current, page })); }} /></>}
      {tab === "codes" && <><CodeTable page={codes} onEdit={setEditingCode} onExport={(item) => void exportCodes(item.batch_no)} onToggle={(item) => void toggleCode(item)} onDelete={(item) => void removeCode(item)} /><Pagination page={codes.page} total={codes.total} pageSize={codes.page_size} onPage={(page) => { setLoading(true); setCodes((current) => ({ ...current, page })); }} /></>}
      {tab === "usages" && <><UsageTable page={usages} /><Pagination page={usages.page} total={usages.total} pageSize={usages.page_size} onPage={(page) => { setLoading(true); setUsages((current) => ({ ...current, page })); }} /></>}
      {tab === "statistics" && statistics !== null && <Statistics value={statistics} />}
    </>}
    {editing !== undefined && <TemplateEditor api={api} template={editing} plans={plans} onClose={() => setEditing(undefined)} onSaved={() => { setEditing(undefined); void load(); }} />}
    {editingCode !== undefined && <CodeEditor api={api} code={editingCode} onClose={() => setEditingCode(undefined)} onSaved={() => { setEditingCode(undefined); void load(); }} />}
    {generating && <CodeGenerator api={api} templates={templateOptions} onClose={() => setGenerating(false)} onSaved={() => { setGenerating(false); setTab("codes"); void load(); }} />}
  </main>;
}

function TemplateTable({ page, onEdit, onDelete }: { page: GiftCardTemplatePage; onEdit: (item: GiftCardTemplate) => void; onDelete: (item: GiftCardTemplate) => void }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "状态", "名称", "类型", "奖励内容", "排序", "创建时间", "操作"].map((label) => <th key={label}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}>
    <td data-label="ID">#{item.id}</td><td data-label="状态"><span className={`status-pill ${item.status ? "active" : "inactive"}`}>{item.status ? "启用" : "禁用"}</span></td><td data-label="名称"><strong>{item.name}</strong><small className="muted">{item.description}</small></td><td data-label="类型">{typeNames[item.type]}</td><td data-label="奖励内容"><RewardSummary value={item.rewards} /></td><td data-label="排序">{item.sort}</td><td data-label="创建时间">{formatDate(item.created_at)}</td><td data-label="操作"><span className="row-actions"><button className="button secondary compact" onClick={() => onEdit(item)}>编辑</button><button className="button danger compact" onClick={() => onDelete(item)}>删除</button></span></td>
  </tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={8}>暂无礼品卡模板</EmptyTableRow>}</tbody></table></div>;
}

function CodeTable({ page, onEdit, onExport, onToggle, onDelete }: { page: GiftCardCodePage; onEdit: (item: GiftCardCode) => void; onExport: (item: GiftCardCode) => void; onToggle: (item: GiftCardCode) => void; onDelete: (item: GiftCardCode) => void }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "兑换码", "模板名称", "状态", "过期时间", "已用次数", "可用次数", "创建时间", "操作"].map((label) => <th key={label}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}>
    <td data-label="ID">#{item.id}</td><td data-label="兑换码"><code>{item.code}</code></td><td data-label="模板名称">{item.template_name ?? `#${item.template_id}`}</td><td data-label="状态">{codeStatusNames[item.status]}</td><td data-label="过期时间">{item.expires_at === null ? "长期有效" : formatDate(item.expires_at)}</td><td data-label="已用次数">{item.usage_count}</td><td data-label="可用次数">{item.max_usage}</td><td data-label="创建时间">{formatDate(item.created_at)}</td><td data-label="操作"><span className="row-actions"><button className="button secondary compact" onClick={() => onEdit(item)}>编辑</button><button className="button secondary compact" onClick={() => onExport(item)}>导出批次</button><button className="button secondary compact" disabled={item.status === 1 || item.status === 3} onClick={() => onToggle(item)}>{item.status === 2 ? "启用" : "禁用"}</button><button className="button danger compact" disabled={item.usage_count > 0} onClick={() => onDelete(item)}>删除</button></span></td>
  </tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={9}>暂无兑换码</EmptyTableRow>}</tbody></table></div>;
}

function UsageTable({ page }: { page: GiftCardUsagePage }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "兑换码", "用户邮箱", "模板名称", "奖励", "使用时间"].map((label) => <th key={label}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}><td data-label="ID">#{item.id}</td><td data-label="兑换码"><code>{item.code}</code></td><td data-label="用户邮箱">{item.user_email}</td><td data-label="模板名称">{item.template_name}</td><td data-label="奖励"><RewardSummary value={item.rewards} /></td><td data-label="使用时间">{formatDate(item.used_at)}</td></tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={6}>暂无使用记录</EmptyTableRow>}</tbody></table></div>;
}

function EmptyTableRow({ colSpan, children }: { colSpan: number; children: string }) {
  return <tr className="empty-table-row"><td colSpan={colSpan}><div className="empty-state">{children}</div></td></tr>;
}

function Statistics({ value }: { value: GiftCardStatistics }) {
  return <><div className="stats-grid"><article><span>模板总数</span><strong>{value.template_total}</strong></article><article><span>活跃模板数</span><strong>{value.active_templates}</strong></article><article><span>兑换码总数</span><strong>{value.code_total}</strong></article><article><span>已使用兑换码</span><strong>{value.used_codes}</strong></article></div><section className="panel"><h2>最近 30 天使用量</h2>{value.daily_usages.length === 0 ? <p className="muted">暂无使用数据</p> : <ul>{value.daily_usages.map((item) => <li key={item.date}>{item.date}：{item.count}</li>)}</ul>}</section></>;
}

function TemplateEditor({ api, template, plans, onClose, onSaved }: { api: GiftCardManagementAPI; template: GiftCardTemplate | null; plans: Plan[]; onClose: () => void; onSaved: () => void }) {
  const { code: currency } = useCurrency();
  const [name, setName] = useState(template?.name ?? ""); const [description, setDescription] = useState(template?.description ?? "");
  const [type, setType] = useState<GiftCardType>(template?.type ?? 1); const [status, setStatus] = useState(template?.status ?? true); const [sort, setSort] = useState(String(template?.sort ?? 0));
  const [balance, setBalance] = useState(centsToYuan(template?.rewards.balance ?? 0)); const [traffic, setTraffic] = useState(bytesToGiB(template?.rewards.transfer_enable ?? 0)); const [expireDays, setExpireDays] = useState(String(template?.rewards.expire_days ?? 0)); const [devices, setDevices] = useState(String(template?.rewards.device_limit ?? 0)); const [reset, setReset] = useState(template?.rewards.reset_package ?? false);
  const [planID, setPlanID] = useState(String(template?.rewards.plan_id ?? plans[0]?.id ?? "")); const [validity, setValidity] = useState(String(template?.rewards.plan_validity_days ?? 30));
  const [conditions, setConditions] = useState<GiftCardConditions>(template?.conditions ?? {}); const [limits, setLimits] = useState<GiftCardLimits>(template?.limits ?? { max_use_per_user: 1 }); const [special, setSpecial] = useState<GiftCardSpecialConfig>(template?.special_config ?? { festival_multiplier_basis_points: 10_000 });
  const [startedAt, setStartedAt] = useState(localDateTimeInput(template?.special_config.started_at)); const [endedAt, setEndedAt] = useState(localDateTimeInput(template?.special_config.ended_at));
  const [icon, setIcon] = useState(template?.icon ?? ""); const [background, setBackground] = useState(template?.background_image ?? ""); const [theme, setTheme] = useState(template?.theme ?? "#1890ff");
  const [random, setRandom] = useState(template?.rewards.random_rewards ?? []); const [error, setError] = useState(""); const [saving, setSaving] = useState(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); setSaving(true); setError(""); try {
    const rewards: GiftCardReward = type === 1 ? { balance: yuanToCents(balance), transfer_enable: gibToBytes(traffic), expire_days: numberValue(expireDays), device_limit: numberValue(devices), reset_package: reset } : type === 2 ? { plan_id: numberValue(planID), plan_validity_days: numberValue(validity) } : { random_rewards: random };
    if ((startedAt === "") !== (endedAt === "")) throw new Error("活动开始和结束时间必须同时填写");
    const specialConfig: GiftCardSpecialConfig = { ...special, started_at: startedAt === "" ? null : new Date(startedAt).toISOString(), ended_at: endedAt === "" ? null : new Date(endedAt).toISOString() };
    const input: GiftCardTemplateInput = { name: name.trim(), description: description.trim(), type, status, conditions, rewards, limits, special_config: specialConfig, icon: icon.trim(), background_image: background.trim(), theme: theme.trim(), sort: numberValue(sort), revision: template?.revision };
    if (template === null) await api.createGiftCardTemplate(input); else await api.updateGiftCardTemplate(template.id, input); onSaved();
  } catch (cause) { setError(cause instanceof Error ? cause.message : "保存失败"); } finally { setSaving(false); } };
  return <Modal title={template === null ? "添加礼品卡模板" : "编辑礼品卡模板"} className="gift-template-modal" onClose={onClose}><div className="modal-header"><h2>{template === null ? "添加礼品卡模板" : "编辑礼品卡模板"}</h2><button className="icon-button" aria-label="关闭模板编辑" onClick={onClose}>×</button></div><form className="form-stack gift-template-form" onSubmit={(event) => void submit(event)}>
    <div className="form-grid"><label>模板名称<input value={name} maxLength={255} required onChange={(event) => setName(event.target.value)} /></label><label>礼品卡类型<select value={type} onChange={(event) => setType(Number(event.target.value) as GiftCardType)}><option value={1}>通用礼品卡</option><option value={2}>套餐礼品卡</option><option value={3}>盲盒礼品卡</option></select></label><label>排序<input type="number" min={0} value={sort} onChange={(event) => setSort(event.target.value)} /></label><label className="switch-label"><input type="checkbox" checked={status} onChange={(event) => setStatus(event.target.checked)} />启用模板</label></div>
    <label>模板描述<textarea value={description} maxLength={4096} onChange={(event) => setDescription(event.target.value)} /></label>
    <fieldset><legend>奖励内容</legend>{type === 1 && <div className="form-grid"><label>{`余额（${currency}）`}<input inputMode="decimal" value={balance} onChange={(event) => setBalance(event.target.value)} /></label><label>流量（GB）<input inputMode="decimal" value={traffic} onChange={(event) => setTraffic(event.target.value)} /></label><label>有效期（天）<input type="number" min={0} value={expireDays} onChange={(event) => setExpireDays(event.target.value)} /></label><label>设备数<input type="number" min={0} value={devices} onChange={(event) => setDevices(event.target.value)} /></label><label className="switch-label"><input type="checkbox" checked={reset} onChange={(event) => setReset(event.target.checked)} />重置已用流量</label></div>}{type === 2 && <div className="form-grid"><label>套餐<select value={planID} required onChange={(event) => setPlanID(event.target.value)}><option value="">请选择套餐</option>{plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name}</option>)}</select></label><label>套餐有效期（天）<input type="number" min={0} value={validity} onChange={(event) => setValidity(event.target.value)} /></label></div>}{type === 3 && <RandomRewardEditor values={random} onChange={setRandom} />}</fieldset>
    <fieldset><legend>使用条件</legend><div className="form-grid"><label>新用户最大注册天数<input type="number" min={0} value={conditions.new_user_max_days ?? ""} onChange={(event) => setConditions({ ...conditions, new_user_max_days: event.target.value === "" ? null : numberValue(event.target.value) })} /></label><PlanIDs label="允许套餐 ID" value={conditions.allowed_plans ?? []} onChange={(value) => setConditions({ ...conditions, allowed_plans: value })} /><PlanIDs label="禁止套餐 ID" value={conditions.disallowed_plans ?? []} onChange={(value) => setConditions({ ...conditions, disallowed_plans: value })} /><label className="switch-label"><input type="checkbox" checked={conditions.new_user_only ?? false} onChange={(event) => setConditions({ ...conditions, new_user_only: event.target.checked })} />仅新用户</label><label className="switch-label"><input type="checkbox" checked={conditions.paid_user_only ?? false} onChange={(event) => setConditions({ ...conditions, paid_user_only: event.target.checked })} />仅付费用户</label><label className="switch-label"><input type="checkbox" checked={conditions.require_invite ?? false} onChange={(event) => setConditions({ ...conditions, require_invite: event.target.checked })} />必须有邀请人</label></div></fieldset>
    <fieldset><legend>使用限制与活动</legend><div className="form-grid"><NumberField label="每用户最多使用次数" value={limits.max_use_per_user ?? 1} onChange={(value) => setLimits({ ...limits, max_use_per_user: value })} /><NumberField label="冷却时间（小时）" value={limits.cooldown_hours ?? 0} onChange={(value) => setLimits({ ...limits, cooldown_hours: value })} /><label>邀请奖励比例<input inputMode="decimal" value={(limits.invite_reward_basis_points ?? 0) / 10_000} onChange={(event) => setLimits({ ...limits, invite_reward_basis_points: Math.round(Number(event.target.value) * 10_000) })} /></label><label>节日奖励倍率<input inputMode="decimal" value={(special.festival_multiplier_basis_points ?? 10_000) / 10_000} onChange={(event) => setSpecial({ ...special, festival_multiplier_basis_points: Math.round(Number(event.target.value) * 10_000) })} /></label><label>活动开始时间<input type="datetime-local" value={startedAt} onChange={(event) => setStartedAt(event.target.value)} /></label><label>活动结束时间<input type="datetime-local" value={endedAt} onChange={(event) => setEndedAt(event.target.value)} /></label></div></fieldset>
    <fieldset><legend>外观</legend><div className="form-grid"><label>图标<input maxLength={255} value={icon} onChange={(event) => setIcon(event.target.value)} /></label><label>背景图片<input type="url" maxLength={255} value={background} onChange={(event) => setBackground(event.target.value)} /></label><label>主题色<input type="color" value={theme} onChange={(event) => setTheme(event.target.value)} /></label></div></fieldset>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button type="button" className="button ghost" onClick={onClose}>取消</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存模板"}</button></div>
  </form></Modal>;
}

function RandomRewardEditor({ values, onChange }: { values: NonNullable<GiftCardReward["random_rewards"]>; onChange: (values: NonNullable<GiftCardReward["random_rewards"]>) => void }) {
  const { code: currency } = useCurrency();
  const add = () => onChange([...values, { weight: 1, rewards: { balance: 0, transfer_enable: 0, expire_days: 0 } }]);
  return <div className="form-stack"><button type="button" className="button secondary" onClick={add}>添加随机奖励</button>{values.map((item, index) => <div className="form-grid random-reward" key={index}><NumberField label="权重" value={item.weight} onChange={(value) => onChange(values.map((current, position) => position === index ? { ...current, weight: value } : current))} /><label>{`余额（${currency}）`}<input value={centsToYuan(item.rewards.balance ?? 0)} onChange={(event) => onChange(values.map((current, position) => position === index ? { ...current, rewards: { ...current.rewards, balance: yuanToCents(event.target.value) } } : current))} /></label><label>流量（GB）<input value={bytesToGiB(item.rewards.transfer_enable ?? 0)} onChange={(event) => onChange(values.map((current, position) => position === index ? { ...current, rewards: { ...current.rewards, transfer_enable: gibToBytes(event.target.value) } } : current))} /></label><NumberField label="有效期（天）" value={item.rewards.expire_days ?? 0} onChange={(value) => onChange(values.map((current, position) => position === index ? { ...current, rewards: { ...current.rewards, expire_days: value } } : current))} /><button type="button" className="button danger compact" onClick={() => onChange(values.filter((_, position) => position !== index))}>删除奖励</button></div>)}</div>;
}

function CodeGenerator({ api, templates, onClose, onSaved }: { api: GiftCardManagementAPI; templates: GiftCardTemplate[]; onClose: () => void; onSaved: () => void }) {
  const [templateID, setTemplateID] = useState(String(templates.find((item) => item.status)?.id ?? "")); const [count, setCount] = useState("1"); const [prefix, setPrefix] = useState("GC"); const [expiresHours, setExpiresHours] = useState(""); const [maxUsage, setMaxUsage] = useState("1"); const [downloadCSV, setDownloadCSV] = useState(false); const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => { event.preventDefault(); setSaving(true); setError(""); try { const expiry = expiresHours === "" ? null : Math.floor(Date.now() / 1000) + numberValue(expiresHours) * 3600; const parameters = [numberValue(templateID), numberValue(count), prefix.trim().toUpperCase(), expiry, numberValue(maxUsage)] as const; if (downloadCSV) downloadBlob(await api.generateGiftCardCodesCSV(...parameters), "gift-cards.csv"); else await api.generateGiftCardCodes(...parameters); onSaved(); } catch (cause) { setError(cause instanceof Error ? cause.message : "生成失败"); } finally { setSaving(false); } };
  return <Modal title="生成兑换码" onClose={onClose}><div className="modal-header"><h2>生成兑换码</h2><button className="icon-button" aria-label="关闭兑换码生成" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>礼品卡模板<select value={templateID} required onChange={(event) => setTemplateID(event.target.value)}><option value="">请选择模板</option>{templates.filter((item) => item.status).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><div className="form-grid"><label>生成数量<input type="number" min={1} max={10000} value={count} onChange={(event) => setCount(event.target.value)} /></label><label>兑换码前缀<input pattern="[A-Z0-9]*" maxLength={10} value={prefix} onChange={(event) => setPrefix(event.target.value.toUpperCase())} /></label><label>有效期（小时）<input type="number" min={1} value={expiresHours} placeholder="留空表示长期有效" onChange={(event) => setExpiresHours(event.target.value)} /></label><label>最大使用次数<input type="number" min={1} max={1000} value={maxUsage} onChange={(event) => setMaxUsage(event.target.value)} /></label><label className="switch-label"><input type="checkbox" checked={downloadCSV} onChange={(event) => setDownloadCSV(event.target.checked)} />导出CSV</label></div>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button type="button" className="button ghost" onClick={onClose}>取消</button><button className="button primary" disabled={saving}>{saving ? "正在生成…" : "生成兑换码"}</button></div></form></Modal>;
}

function CodeEditor({ api, code, onClose, onSaved }: { api: GiftCardManagementAPI; code: GiftCardCode; onClose: () => void; onSaved: () => void }) {
  const [value, setValue] = useState(code.code); const [status, setStatus] = useState<GiftCardCodeStatus>(code.status);
  const [expiresAt, setExpiresAt] = useState(localDateTimeInput(code.expires_at)); const [maxUsage, setMaxUsage] = useState(String(code.max_usage));
  const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => { event.preventDefault(); setSaving(true); setError(""); try {
    await api.updateGiftCardCode(code.id, { code: value.trim().toUpperCase(), status, expires_at: expiresAt === "" ? null : Math.floor(new Date(expiresAt).getTime() / 1000), max_usage: numberValue(maxUsage) }); onSaved();
  } catch (cause) { setError(cause instanceof Error ? cause.message : "保存失败"); setSaving(false); } };
  return <Modal title="编辑兑换码" onClose={onClose}><div className="modal-header"><h2>编辑兑换码</h2><button className="icon-button" aria-label="关闭兑换码编辑" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>兑换码<input required minLength={8} maxLength={32} pattern="[A-Z0-9]+" value={value} onChange={(event) => setValue(event.target.value.toUpperCase())} /></label><div className="form-grid"><label>兑换码状态<select value={status} onChange={(event) => setStatus(Number(event.target.value) as GiftCardCodeStatus)}>{codeStatusNames.map((label, itemStatus) => <option value={itemStatus} key={label}>{label}</option>)}</select></label><label>过期时间<input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} /></label><label>最大使用次数<input type="number" min={Math.max(1, code.usage_count)} max={1000} value={maxUsage} onChange={(event) => setMaxUsage(event.target.value)} /></label></div>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存兑换码"}</button></div></form></Modal>;
}

function Pagination({ page, total, pageSize, onPage }: { page: number; total: number; pageSize: number; onPage: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  if (pages <= 1) return null;
  return <div className="pagination-footer"><button className="button secondary compact" disabled={page <= 1} onClick={() => onPage(page - 1)}>上一页</button><span>第 {page} / {pages} 页，共 {total} 条</span><button className="button secondary compact" disabled={page >= pages} onClick={() => onPage(page + 1)}>下一页</button></div>;
}

function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) { return <label>{label}<input type="number" min={0} value={value} onChange={(event) => onChange(numberValue(event.target.value))} /></label>; }
function PlanIDs({ label, value, onChange }: { label: string; value: number[]; onChange: (value: number[]) => void }) { return <label>{label}<input value={value.join(",")} placeholder="例如 1,2" onChange={(event) => onChange(event.target.value.split(",").map((item) => Number(item.trim())).filter((item) => Number.isInteger(item) && item > 0))} /></label>; }
function RewardSummary({ value }: { value: GiftCardReward }) { const { format: formatMoney } = useCurrency(); const parts: string[] = []; if ((value.balance ?? 0) > 0) parts.push(`余额 ${formatMoney(value.balance ?? 0)}`); if ((value.transfer_enable ?? 0) > 0) parts.push(`流量 ${bytesToGiB(value.transfer_enable ?? 0)} GB`); if (value.plan_id != null) parts.push(`套餐 #${value.plan_id}`); if ((value.expire_days ?? 0) > 0) parts.push(`${value.expire_days} 天`); if ((value.random_rewards?.length ?? 0) > 0) parts.push(`${value.random_rewards?.length} 项随机奖励`); return <>{parts.join(" · ") || "流量重置"}</>; }
function numberValue(value: string) { const parsed = Number(value); return Number.isSafeInteger(parsed) && parsed >= 0 ? parsed : 0; }
function yuanToCents(value: string) { if (!/^\d+(?:\.\d{1,2})?$/.test(value.trim())) throw new Error("金额最多保留两位小数"); return Math.round(Number(value) * 100); }
function gibToBytes(value: string) { if (!/^\d+(?:\.\d{1,6})?$/.test(value.trim())) throw new Error("流量格式无效"); const result = Math.round(Number(value) * 1_073_741_824); if (!Number.isSafeInteger(result)) throw new Error("流量超出安全范围"); return result; }
function centsToYuan(value: number) { return (value / 100).toFixed(2); }
function bytesToGiB(value: number) { return String(Math.round(value / 1_073_741.824) / 1000); }
function formatDate(value: string) { return new Date(value).toLocaleString("zh-CN", { hour12: false }); }
function localDateTimeInput(value: string | null | undefined) { if (value == null || value === "") return ""; const date = new Date(value); return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16); }
function optionalNumber(value: string) { const result = Number(value); return value === "" || !Number.isSafeInteger(result) || result < 1 ? undefined : result; }
function optionalGiftType(value: string) { const result = optionalNumber(value); return result === undefined ? undefined : result as GiftCardType; }
function optionalCodeStatus(value: string) { if (value === "") return undefined; const result = Number(value); return Number.isInteger(result) && result >= 0 && result <= 3 ? result as GiftCardCodeStatus : undefined; }
function optionalBoolean(value: string) { return value === "" ? undefined : value === "true"; }
async function loadGiftCardTemplateOptions(api: GiftCardManagementAPI) {
  const pageSize = 200; const maximum = 1_000;
  const first = await api.listGiftCardTemplates(1, pageSize);
  if (first.total > maximum) throw new Error(`礼品卡模板超过 ${maximum} 条，请先清理旧模板`);
  const pageCount = Math.ceil(first.total / pageSize);
  if (pageCount <= 1) return first.items;
  const remaining = await Promise.all(Array.from({ length: pageCount - 1 }, (_, index) => api.listGiftCardTemplates(index + 2, pageSize)));
  return [first, ...remaining].flatMap((page) => page.items);
}
function downloadBlob(blob: Blob, filename: string) { const url = URL.createObjectURL(blob); const anchor = document.createElement("a"); anchor.href = url; anchor.download = filename; anchor.click(); URL.revokeObjectURL(url); }
