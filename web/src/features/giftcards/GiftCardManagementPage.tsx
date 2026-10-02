import { translateAdmin } from "../../lib/adminLocale";
import { useCallback, useEffect, useState, type FormEvent } from "react";

import { GiftField, GiftSection, GiftSwitch } from "./GiftTemplateFields";
import "./GiftTemplateEditor.css";
import { NodeFilter } from "../nodes/NodeFilter";
import { DateTimeInput } from "../../components/DateTimeInput";
import { Modal } from "../../components/Overlay";
import type {
  GiftCardCode, GiftCardCodeInput, GiftCardCodePage, GiftCardCodeStatus, GiftCardConditions, GiftCardLimits, GiftCardReward, GiftCardSpecialConfig,
  GiftCardStatistics, GiftCardTemplate, GiftCardTemplateInput, GiftCardTemplatePage, GiftCardType,
  GiftCardUsagePage, Plan
} from "../../lib/api";

export interface GiftCardManagementAPI {
  listGiftCardTemplates: (page?: number, pageSize?: number, type?: GiftCardType, status?: boolean, search?: string) => Promise<GiftCardTemplatePage>;
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
  const [hiddenColumns, setHiddenColumns] = useState<Record<string, string[]>>({});
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
  const [templateSearch, setTemplateSearch] = useState("");
  const [templateType, setTemplateType] = useState(""); const [templateStatus, setTemplateStatus] = useState("");
  const [codeSearch, setCodeSearch] = useState(""); const [codeStatus, setCodeStatus] = useState("");
  const codeTemplate = ""; const codeBatch = "";
  const [exportingBatch, setExportingBatch] = useState<string | null>(null);
  const [usageUser, setUsageUser] = useState(""); const [usageTemplate, setUsageTemplate] = useState(""); const [usageCode, setUsageCode] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true); setError("");
    try {
      if (tab === "templates") setTemplates(await api.listGiftCardTemplates(templates.page, 20, optionalGiftType(templateType), optionalBoolean(templateStatus), templateSearch));
      if (tab === "codes") setCodes(await api.listGiftCardCodes(codes.page, 20, codeSearch, optionalNumber(codeTemplate), optionalCodeStatus(codeStatus), codeBatch));
      if (tab === "usages") setUsages(await api.listGiftCardUsages(usages.page, 20, optionalNumber(usageUser), optionalNumber(usageTemplate), optionalNumber(usageCode)));
      if (tab === "statistics") setStatistics(await api.getGiftCardStatistics());
    } catch (cause) { setError(cause instanceof Error ? cause.message : "礼品卡数据加载失败"); }
    finally { setLoading(false); }
  }, [api, codeBatch, codeSearch, codeStatus, codeTemplate, codes.page, tab, templateSearch, templateStatus, templateType, templates.page, usageCode, usageTemplate, usageUser, usages.page]);

  useEffect(() => {
    let active = true;
    const request = tab === "templates" ? api.listGiftCardTemplates(templates.page, 20, optionalGiftType(templateType), optionalBoolean(templateStatus), templateSearch) : tab === "codes" ? api.listGiftCardCodes(codes.page, 20, codeSearch, optionalNumber(codeTemplate), optionalCodeStatus(codeStatus), codeBatch) : tab === "usages" ? api.listGiftCardUsages(usages.page, 20, optionalNumber(usageUser), optionalNumber(usageTemplate), optionalNumber(usageCode)) : api.getGiftCardStatistics();
    void request.then((value) => {
      if (!active) return;
      setError("");
      if (tab === "templates") setTemplates(value as GiftCardTemplatePage);
      if (tab === "codes") setCodes(value as GiftCardCodePage);
      if (tab === "usages") setUsages(value as GiftCardUsagePage);
      if (tab === "statistics") setStatistics(value as GiftCardStatistics);
    }).catch((cause: unknown) => { if (active) setError(cause instanceof Error ? cause.message : "礼品卡数据加载失败"); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [api, codeBatch, codeSearch, codeStatus, codeTemplate, codes.page, tab, templateSearch, templateStatus, templateType, templates.page, usageCode, usageTemplate, usageUser, usages.page]);
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

  return <main className="page-shell gift-card-management">
    <header className="page-header"><div><h1>{translateAdmin("礼品卡管理")}</h1><p className="muted">{translateAdmin("在这里可以管理礼品卡模板、兑换码和使用记录等功能。")}</p></div>

    </header>
    <nav className="tab-list" role="tablist" aria-label="礼品卡功能"><button role="tab" aria-selected={tab === "templates"} onClick={() => switchTab("templates")}>{translateAdmin("模板管理")}</button><button role="tab" aria-selected={tab === "codes"} onClick={() => switchTab("codes")}>{translateAdmin("兑换码管理")}</button><button role="tab" aria-selected={tab === "usages"} onClick={() => switchTab("usages")}>{translateAdmin("使用记录")}</button><button role="tab" aria-selected={tab === "statistics"} onClick={() => switchTab("statistics")}>{translateAdmin("统计数据")}</button></nav>
    <section className="gift-tab-heading"><h2>{tab === "templates" ? translateAdmin("模板管理") : tab === "codes" ? translateAdmin("兑换码管理") : tab === "usages" ? translateAdmin("使用记录") : translateAdmin("统计数据")}</h2><p className="muted">{tab === "templates" ? translateAdmin("管理礼品卡模板，包括创建、编辑和删除模板。") : tab === "codes" ? translateAdmin("管理礼品卡兑换码，包括生成、查看和导出兑换码。") : tab === "usages" ? "查看礼品卡使用记录。" : "查看礼品卡统计数据。"}</p></section>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="gift-toolbar">
    {tab === "templates" && <div className="filter-bar"><input aria-label="搜索礼品卡" placeholder={translateAdmin("搜索礼品卡...")} value={templateSearch} onChange={event => { setTemplates(current => ({ ...current, page: 1 })); setTemplateSearch(event.target.value); }} /><NodeFilter label="模板类型" options={Object.entries(typeNames).map(([value,label]) => ({value,label:translateAdmin(label)}))} value={templateType === "" ? [] : [templateType]} onChange={values => { setTemplates(current => ({...current,page:1})); setTemplateType(values.at(-1) ?? ""); }} /><NodeFilter label="模板状态" options={[{value:"true",label:translateAdmin("启用")},{value:"false",label:translateAdmin("禁用")}]} value={templateStatus === "" ? [] : [templateStatus]} onChange={values => { setTemplates(current => ({...current,page:1})); setTemplateStatus(values.at(-1) ?? ""); }} /><button className="button primary" onClick={() => setEditing(null)}>{translateAdmin("添加模板")}</button></div>}
    {tab === "codes" && <div className="filter-bar"><input aria-label="搜索兑换码" placeholder={translateAdmin("搜索礼品卡...")} value={codeSearch} onChange={event => { setCodes(current => ({ ...current, page: 1 })); setCodeSearch(event.target.value); }} /><NodeFilter label={translateAdmin("状态")} options={codeStatusNames.map((label, value) => ({ value: String(value), label: translateAdmin(label) }))} value={codeStatus === "" ? [] : [codeStatus]} onChange={values => { setCodes(current => ({ ...current, page: 1 })); setCodeStatus(values.at(-1) ?? ""); }} /><button className="button primary" disabled={templateOptionsLoading} onClick={() => setGenerating(true)}>{translateAdmin("生成兑换码")}</button><button className="button secondary" onClick={() => setExportingBatch("")}>{translateAdmin("导出")}</button></div>}
    {tab === "usages" && <div className="filter-bar"><label>用户 ID<input type="number" min={1} value={usageUser} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageUser(event.target.value); }} /></label><label>模板 ID<input type="number" min={1} value={usageTemplate} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageTemplate(event.target.value); }} /></label><label>兑换码 ID<input type="number" min={1} value={usageCode} onChange={(event) => { setUsages((current) => ({ ...current, page: 1 })); setUsageCode(event.target.value); }} /></label></div>}
    {tab !== "statistics" && <details className="gift-column-chooser"><summary className="button secondary">{translateAdmin("显示列")}</summary><div className="panel">{(tab === "templates" ? ["ID", "状态", "名称", "类型", "奖励内容", "排序", "创建时间"] : tab === "codes" ? ["ID", "兑换码", "模板名称", "状态", "过期时间", "已用次数", "可用次数", "创建时间"] : ["ID", "兑换码", "用户邮箱", "模板名称", "奖励", "使用时间"]).map(column => <label className="switch-label" key={column}><input type="checkbox" checked={!(hiddenColumns[tab] ?? []).includes(column)} onChange={event => setHiddenColumns(current => ({ ...current, [tab]: event.target.checked ? (current[tab] ?? []).filter(item => item !== column) : [...(current[tab] ?? []), column] }))} />{column}</label>)}</div></details>}
    </div>
    {loading ? <div className="empty-state">正在加载…</div> : <>
      {tab === "templates" && <><TemplateTable hidden={hiddenColumns[tab] ?? []} page={templates} onEdit={setEditing} onDelete={(item) => void removeTemplate(item)} /><Pagination page={templates.page} total={templates.total} pageSize={templates.page_size} onPage={(page) => { setLoading(true); setTemplates((current) => ({ ...current, page })); }} /></>}
      {tab === "codes" && <><CodeTable hidden={hiddenColumns[tab] ?? []} page={codes} onEdit={setEditingCode} onExport={(item) => void exportCodes(item.batch_no)} onToggle={(item) => void toggleCode(item)} onDelete={(item) => void removeCode(item)} /><Pagination page={codes.page} total={codes.total} pageSize={codes.page_size} onPage={(page) => { setLoading(true); setCodes((current) => ({ ...current, page })); }} /></>}
      {tab === "usages" && <><UsageTable hidden={hiddenColumns[tab] ?? []} page={usages} /><Pagination page={usages.page} total={usages.total} pageSize={usages.page_size} onPage={(page) => { setLoading(true); setUsages((current) => ({ ...current, page })); }} /></>}
      {tab === "statistics" && statistics !== null && <Statistics value={statistics} />}
    </>}
    {editing !== undefined && <TemplateEditor api={api} template={editing} plans={plans} onClose={() => setEditing(undefined)} onSaved={() => { setEditing(undefined); void load(); }} />}
    {editingCode !== undefined && <CodeEditor api={api} code={editingCode} onClose={() => setEditingCode(undefined)} onSaved={() => { setEditingCode(undefined); void load(); }} />}
    {exportingBatch !== null && <BatchExportDialog api={api} batch={exportingBatch} onClose={() => setExportingBatch(null)} />}
    {generating && <CodeGenerator api={api} templates={templateOptions} onClose={() => setGenerating(false)} onSaved={() => { setGenerating(false); setTab("codes"); void load(); }} />}
  </main>;
}

function BatchExportDialog({ api, batch, onClose }: { api: GiftCardManagementAPI; batch: string; onClose: () => void }) {
  const [value, setValue] = useState(batch); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => {
    event.preventDefault(); setBusy(true); setError("");
    try { downloadBlob(await api.exportGiftCardCodes(value.trim()), `gift-card-${value.trim()}.csv`); onClose(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "导出失败"); }
    finally { setBusy(false); }
  };
  return <Modal title={translateAdmin("导出")} onClose={onClose}><div className="modal-header"><h2>{translateAdmin("导出")}</h2><button type="button" className="icon-button" aria-label="关闭导出" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={event => void submit(event)}><label>批次号<input required autoFocus value={value} onChange={event => setValue(event.target.value)} /></label><p className="muted">导出指定批次的全部兑换码。</p>{error && <div role="alert" className="alert error">{error}</div>}<div className="form-actions"><button type="button" className="button ghost" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" disabled={busy || !value.trim()}>{busy ? "正在导出…" : translateAdmin("导出")}</button></div></form></Modal>;
}

function TemplateTable({ hidden, page, onEdit, onDelete }: { hidden: string[]; page: GiftCardTemplatePage; onEdit: (item: GiftCardTemplate) => void; onDelete: (item: GiftCardTemplate) => void }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "状态", "名称", "类型", "奖励内容", "排序", "创建时间", "操作"].map((label) => <th key={label} hidden={hidden.includes(label)}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}>
    <td hidden={hidden.includes("ID")} data-label="ID">#{item.id}</td><td hidden={hidden.includes("状态")} data-label="状态"><span className={`status-pill ${item.status ? "active" : "inactive"}`}>{item.status ? translateAdmin("启用") : translateAdmin("禁用")}</span></td><td hidden={hidden.includes("名称")} data-label="名称"><strong>{item.name}</strong><small className="muted">{item.description}</small></td><td hidden={hidden.includes("类型")} data-label="类型">{typeNames[item.type]}</td><td hidden={hidden.includes("奖励内容")} data-label="奖励内容">{rewardSummary(item.rewards)}</td><td hidden={hidden.includes("排序")} data-label="排序">{item.sort}</td><td hidden={hidden.includes("创建时间")} data-label="创建时间">{formatDate(item.created_at)}</td><td hidden={hidden.includes("操作")} data-label="操作"><span className="row-actions"><button className="button secondary compact" onClick={() => onEdit(item)}>{translateAdmin("编辑")}</button><button className="button danger compact" onClick={() => onDelete(item)}>{translateAdmin("删除")}</button></span></td>
  </tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={8}>暂无礼品卡模板</EmptyTableRow>}</tbody></table></div>;
}

function CodeTable({ hidden, page, onEdit, onExport, onToggle, onDelete }: { hidden: string[]; page: GiftCardCodePage; onEdit: (item: GiftCardCode) => void; onExport: (item: GiftCardCode) => void; onToggle: (item: GiftCardCode) => void; onDelete: (item: GiftCardCode) => void }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "兑换码", "模板名称", "状态", "过期时间", "已用次数", "可用次数", "创建时间", "操作"].map((label) => <th key={label} hidden={hidden.includes(label)}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}>
    <td hidden={hidden.includes("ID")} data-label="ID">#{item.id}</td><td hidden={hidden.includes("兑换码")} data-label="兑换码"><code>{item.code}</code></td><td hidden={hidden.includes("模板名称")} data-label="模板名称">{item.template_name ?? `#${item.template_id}`}</td><td hidden={hidden.includes("状态")} data-label="状态">{codeStatusNames[item.status]}</td><td hidden={hidden.includes("过期时间")} data-label="过期时间">{item.expires_at === null ? translateAdmin("长期有效") : formatDate(item.expires_at)}</td><td hidden={hidden.includes("已用次数")} data-label="已用次数">{item.usage_count}</td><td hidden={hidden.includes("可用次数")} data-label="可用次数">{item.max_usage}</td><td hidden={hidden.includes("创建时间")} data-label="创建时间">{formatDate(item.created_at)}</td><td hidden={hidden.includes("操作")} data-label="操作"><span className="row-actions"><button className="button secondary compact" onClick={() => onEdit(item)}>{translateAdmin("编辑")}</button><button className="button secondary compact" onClick={() => onExport(item)}>导出批次</button><button className="button secondary compact" disabled={item.status === 1 || item.status === 3} onClick={() => onToggle(item)}>{item.status === 2 ? translateAdmin("启用") : translateAdmin("禁用")}</button><button className="button danger compact" disabled={item.usage_count > 0} onClick={() => onDelete(item)}>{translateAdmin("删除")}</button></span></td>
  </tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={9}>暂无兑换码</EmptyTableRow>}</tbody></table></div>;
}

function UsageTable({ hidden, page }: { hidden: string[]; page: GiftCardUsagePage }) {
  return <div className="resource-table-wrap gift-card-table"><table className="resource-table"><thead><tr>{["ID", "兑换码", "用户邮箱", "模板名称", "奖励", "使用时间"].map((label) => <th key={label} hidden={hidden.includes(label)}>{label}</th>)}</tr></thead><tbody>{page.items.map((item) => <tr key={item.id}><td hidden={hidden.includes("ID")} data-label="ID">#{item.id}</td><td hidden={hidden.includes("兑换码")} data-label="兑换码"><code>{item.code}</code></td><td hidden={hidden.includes("用户邮箱")} data-label="用户邮箱">{item.user_email}</td><td hidden={hidden.includes("模板名称")} data-label="模板名称">{item.template_name}</td><td hidden={hidden.includes("奖励")} data-label="奖励">{rewardSummary(item.rewards)}</td><td hidden={hidden.includes("使用时间")} data-label="使用时间">{formatDate(item.used_at)}</td></tr>)}{page.items.length === 0 && <EmptyTableRow colSpan={6}>暂无使用记录</EmptyTableRow>}</tbody></table></div>;
}

function EmptyTableRow({ colSpan, children }: { colSpan: number; children: string }) {
  return <tr className="empty-table-row"><td colSpan={colSpan}><div className="empty-state">{children}</div></td></tr>;
}

function Statistics({ value }: { value: GiftCardStatistics }) {
  return <><div className="stats-grid"><article><span>{translateAdmin("模板总数")}</span><strong>{value.template_total}</strong></article><article><span>{translateAdmin("活跃模板数")}</span><strong>{value.active_templates}</strong></article><article><span>{translateAdmin("兑换码总数")}</span><strong>{value.code_total}</strong></article><article><span>{translateAdmin("已使用兑换码")}</span><strong>{value.used_codes}</strong></article></div><section className="panel"><h2>最近 30 天使用量</h2>{value.daily_usages.length === 0 ? <p className="muted">暂无使用数据</p> : <ul>{value.daily_usages.map((item) => <li key={item.date}>{item.date}：{item.count}</li>)}</ul>}</section></>;
}

function TemplateEditor({ api, template, plans, onClose, onSaved }: { api: GiftCardManagementAPI; template: GiftCardTemplate | null; plans: Plan[]; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(template?.name ?? ""); const [description, setDescription] = useState(template?.description ?? "");
  const [type, setType] = useState<GiftCardType>(template?.type ?? 1); const [status, setStatus] = useState(template?.status ?? true); const [sort, setSort] = useState(String(template?.sort ?? 0));
  const [balance, setBalance] = useState(template ? centsToYuan(template.rewards.balance ?? 0) : ""); const [traffic, setTraffic] = useState(template ? bytesToGiB(template.rewards.transfer_enable ?? 0) : ""); const [expireDays, setExpireDays] = useState(String(template?.rewards.expire_days ?? "")); const [devices, setDevices] = useState(String(template?.rewards.device_limit ?? "")); const [reset, setReset] = useState(template?.rewards.reset_package ?? false);
  const [planID, setPlanID] = useState(String(template?.rewards.plan_id ?? "")); const [validity, setValidity] = useState(String(template?.rewards.plan_validity_days ?? ""));
  const [conditions, setConditions] = useState<GiftCardConditions>(template?.conditions ?? {}); const [limits, setLimits] = useState<GiftCardLimits>(template?.limits ?? {}); const [special] = useState<GiftCardSpecialConfig>(template?.special_config ?? {});
  const [inviteRate, setInviteRate] = useState(template?.limits.invite_reward_basis_points == null ? "" : String(template.limits.invite_reward_basis_points / 10_000));
  const [multiplier, setMultiplier] = useState(template?.special_config.festival_multiplier_basis_points == null ? "" : String(template.special_config.festival_multiplier_basis_points / 10_000));
  const [startedAt, setStartedAt] = useState(localDateTimeInput(template?.special_config.started_at)); const [endedAt, setEndedAt] = useState(localDateTimeInput(template?.special_config.ended_at));
  const [icon, setIcon] = useState(template?.icon ?? ""); const [background, setBackground] = useState(template?.background_image ?? ""); const theme = template?.theme ?? "#1890ff";
  const [random, setRandom] = useState<RandomRewardDraft[]>((template?.rewards.random_rewards ?? []).map(item => ({ original: item.rewards, weight: String(item.weight), balance: centsToYuan(item.rewards.balance ?? 0), traffic: bytesToGiB(item.rewards.transfer_enable ?? 0), days: String(item.rewards.expire_days ?? 0) }))); const [error, setError] = useState(""); const [saving, setSaving] = useState(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (saving) return; setSaving(true); setError(""); try {
    const rewards: GiftCardReward = type === 1 ? { balance: yuanToCents(balance || "0"), transfer_enable: gibToBytes(traffic || "0"), expire_days: numberValue(expireDays), device_limit: numberValue(devices), reset_package: reset } : type === 2 ? { plan_id: numberValue(planID), plan_validity_days: numberValue(validity) } : { random_rewards: random.map(item => ({ weight: numberValue(item.weight), rewards: { ...item.original, balance: yuanToCents(item.balance || "0"), transfer_enable: gibToBytes(item.traffic || "0"), expire_days: numberValue(item.days) } })) };
    if ((startedAt === "") !== (endedAt === "")) throw new Error("活动开始和结束时间必须同时填写");
    const specialConfig: GiftCardSpecialConfig = { ...special, festival_multiplier_basis_points: ratioBasisPoints(multiplier, "节日奖励乘数"), started_at: startedAt === "" ? null : new Date(startedAt).toISOString(), ended_at: endedAt === "" ? null : new Date(endedAt).toISOString() };
    const input: GiftCardTemplateInput = { name: name.trim(), description: description.trim(), type, status, conditions, rewards, limits: { ...limits, invite_reward_basis_points: ratioBasisPoints(inviteRate, "邀请人奖励比例", 1) }, special_config: specialConfig, icon: icon.trim(), background_image: background.trim(), theme: theme.trim(), sort: numberValue(sort), revision: template?.revision };
    if (template === null) await api.createGiftCardTemplate(input); else await api.updateGiftCardTemplate(template.id, input); onSaved();
  } catch (cause) { setError(cause instanceof Error ? cause.message : "保存失败"); } finally { setSaving(false); } };
  return <Modal title={template === null ? translateAdmin("添加模板") : translateAdmin("编辑模板")} className="gift-template-modal" onClose={() => { if (!saving) onClose(); }}>
    <div className="modal-header"><h2>{template === null ? translateAdmin("添加模板") : translateAdmin("编辑模板")}</h2><button className="gift-close" aria-label="关闭模板编辑" disabled={saving} onClick={onClose}><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18" /></svg></button></div>
    <form className="gift-template-form" onSubmit={(event) => void submit(event)}><div className="gift-template-scroll">
      <GiftSection title="基础配置"><div className="gift-grid">
        <GiftField label="模板名称" value={name} required maxLength={255} placeholder="请输入模板名称" onChange={setName} />
        <label className="gift-field">{translateAdmin("类型")}<select value={type} onChange={event => setType(Number(event.target.value) as GiftCardType)}>{Object.entries(typeNames).map(([value, label]) => <option key={value} value={value}>{translateAdmin(label)}</option>)}</select></label>
        <label className="gift-field gift-wide">{translateAdmin("描述")}<textarea value={description} maxLength={4096} placeholder={translateAdmin("请输入礼品卡描述")} onChange={event => setDescription(event.target.value)} /></label>
        <GiftField label="排序" type="number" min={0} value={sort} placeholder="0" onChange={setSort} />
        <GiftSwitch label="状态" description="禁用后，此模板将无法生成或兑换新的礼品卡。" checked={status} onChange={setStatus} />
      </div></GiftSection>
      <GiftSection title="奖励内容">
        {type === 1 && <div className="gift-grid">
          <GiftField label="奖励余额 (元)" value={balance} placeholder="请输入奖励的金额(元)" unit="¥" onChange={setBalance} />
          <GiftField label="奖励流量 (GB)" value={traffic} placeholder="请输入奖励的流量(GB)" unit="GB" onChange={setTraffic} />
          <GiftField label="延长有效期 (天)" type="number" min={0} value={expireDays} placeholder="请输入延长的天数" unit="天" onChange={setExpireDays} />
          <GiftField label="增加设备数" type="number" min={0} value={devices} placeholder="请输入增加的设备数量" onChange={setDevices} />
          <GiftSwitch label="重置当月流量" description="开启后，兑换时会将用户当前套餐的已用流量清零。" checked={reset} onChange={setReset} wide />
        </div>}
        {type === 2 && <div className="gift-grid"><label className="gift-field">{translateAdmin("指定套餐")}<select value={planID} required onChange={event => setPlanID(event.target.value)}><option value="">请选择一个套餐</option>{plans.map(plan => <option key={plan.id} value={plan.id}>{plan.name}</option>)}</select></label><GiftField label="套餐有效期 (天)" type="number" min={0} value={validity} placeholder="留空则保留用户当前到期时间" unit="天" onChange={setValidity} /></div>}
        {type === 3 && <RandomRewardEditor values={random} onChange={setRandom} />}
      </GiftSection>
      <GiftSection title="使用条件"><div className="gift-grid">
        <GiftField label="新用户注册天数限制" type="number" min={0} value={conditions.new_user_max_days ?? ""} placeholder="例如: 7 (仅限注册7天内的用户)" unit="天" wide onChange={value => setConditions({ ...conditions, new_user_max_days: value === "" ? null : numberValue(value) })} />
        <div className="gift-condition-switches gift-wide"><GiftSwitch label="仅限新用户" checked={conditions.new_user_only ?? false} onChange={value => setConditions({ ...conditions, new_user_only: value })} compact /><GiftSwitch label="仅限付费用户" checked={conditions.paid_user_only ?? false} onChange={value => setConditions({ ...conditions, paid_user_only: value })} compact /><GiftSwitch label="需要邀请关系" checked={conditions.require_invite ?? false} onChange={value => setConditions({ ...conditions, require_invite: value })} compact /></div>
        <PlanIDs plans={plans} label={translateAdmin("允许的套餐")} placeholder="选择允许兑换的套餐 (留空则不限制)" value={conditions.allowed_plans ?? []} onChange={value => setConditions({ ...conditions, allowed_plans: value })} />
        <PlanIDs plans={plans} label={translateAdmin("禁止的套餐")} placeholder="选择禁止兑换的套餐 (留空则不限制)" value={conditions.disallowed_plans ?? []} onChange={value => setConditions({ ...conditions, disallowed_plans: value })} />
      </div></GiftSection>
      <GiftSection title="使用限制"><div className="gift-grid">
        <GiftField label="单用户最大使用次数" type="number" min={0} value={limits.max_use_per_user ?? ""} placeholder="留空默认 1 次" onChange={value => setLimits({ ...limits, max_use_per_user: value === "" ? undefined : numberValue(value) })} />
        <GiftField label="同类卡冷却时间(小时)" type="number" min={0} value={limits.cooldown_hours ?? ""} placeholder="留空则不限制" unit="h" onChange={value => setLimits({ ...limits, cooldown_hours: value === "" ? undefined : numberValue(value) })} />
        <div className="gift-wide"><GiftField label="邀请人奖励比例" value={inviteRate} placeholder="例如: 0.2 (代表20%)" unit="%" onChange={setInviteRate} /><p className="gift-help">使用者有邀请人时，给邀请人的奖励 = 余额奖励 * 此比例</p></div>
      </div></GiftSection>
      <GiftSection title="特殊配置"><div className="gift-grid gift-responsive-grid">
        <DateTimeInput label={translateAdmin("活动开始时间")} value={startedAt} onChange={setStartedAt} placeholder={translateAdmin("请选择开始日期")} clearLabel="清空日期" />
        <DateTimeInput label={translateAdmin("活动结束时间")} value={endedAt} onChange={setEndedAt} placeholder={translateAdmin("请选择结束日期")} clearLabel="清空日期" />
        <GiftField label="节日奖励乘数" value={multiplier} placeholder="例如: 1.5 (代表1.5倍)" unit="x" onChange={setMultiplier} wide />
      </div></GiftSection>
      <GiftSection title="显示效果"><div className="gift-grid gift-responsive-grid"><GiftField label="图标" value={icon} maxLength={255} placeholder="请输入图标的URL" onChange={setIcon} /><GiftField label="背景图片" type="url" value={background} maxLength={255} placeholder="请输入背景图片的URL" onChange={setBackground} /></div></GiftSection>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
    </div><div className="form-actions"><button type="button" className="button ghost" disabled={saving} onClick={onClose}>{translateAdmin("取消")}</button><button type="submit" className="button primary" disabled={saving}>{saving ? "正在保存…" : translateAdmin("确认")}</button></div></form>
  </Modal>;

}

interface RandomRewardDraft {
  original: GiftCardReward;
  weight: string;
  balance: string;
  traffic: string;
  days: string;
}

function RandomRewardEditor({ values, onChange }: { values: RandomRewardDraft[]; onChange: (values: RandomRewardDraft[]) => void }) {
  const update = (index: number, key: "weight" | "balance" | "traffic" | "days", value: string) => onChange(values.map((item, position) => position === index ? { ...item, [key]: value } : item));
  const add = () => onChange([...values, { original: {}, weight: "10", balance: "", traffic: "", days: "" }]);
  return <div className="gift-random-pool"><div className="gift-reward-heading"><span>{translateAdmin("随机奖励池")}</span><button type="button" className="button secondary compact" onClick={add}><span aria-hidden="true">＋</span>{translateAdmin("添加随机奖励项")}</button></div>{values.map((item, index) => <div className="gift-random-item" key={index}><div className="gift-random-heading"><small>#{index + 1}</small><button type="button" aria-label={`删除奖励 ${index + 1}`} onClick={() => onChange(values.filter((_, position) => position !== index))}><svg aria-hidden="true" viewBox="0 0 24 24"><path d="M3 6h18M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" /></svg></button></div><div className="gift-random-fields">
    <GiftField label="权重" type="number" min={1} required value={item.weight} onChange={value => update(index, "weight", value)} />
    <GiftField label="奖励余额 (元)" unit="¥" value={item.balance} onChange={value => update(index, "balance", value)} />
    <GiftField label="奖励流量 (GB)" unit="GB" value={item.traffic} onChange={value => update(index, "traffic", value)} />
    <GiftField label="延长有效期 (天)" type="number" min={0} unit="d" value={item.days} onChange={value => update(index, "days", value)} />
  </div></div>)}</div>;

}

function CodeGenerator({ api, templates, onClose, onSaved }: { api: GiftCardManagementAPI; templates: GiftCardTemplate[]; onClose: () => void; onSaved: () => void }) {
  const [templateID, setTemplateID] = useState(String(templates.find((item) => item.status)?.id ?? "")); const [count, setCount] = useState("1"); const [prefix, setPrefix] = useState("GC"); const [expiresHours, setExpiresHours] = useState(""); const [maxUsage, setMaxUsage] = useState("1"); const [downloadCSV, setDownloadCSV] = useState(false); const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => { event.preventDefault(); setSaving(true); setError(""); try { const expiry = expiresHours === "" ? null : Math.floor(Date.now() / 1000) + numberValue(expiresHours) * 3600; const parameters = [numberValue(templateID), numberValue(count), prefix.trim().toUpperCase(), expiry, numberValue(maxUsage)] as const; if (downloadCSV) downloadBlob(await api.generateGiftCardCodesCSV(...parameters), "gift-cards.csv"); else await api.generateGiftCardCodes(...parameters); onSaved(); } catch (cause) { setError(cause instanceof Error ? cause.message : "生成失败"); } finally { setSaving(false); } };
  return <Modal title={translateAdmin("生成兑换码")} onClose={onClose}><div className="modal-header"><h2>{translateAdmin("生成兑换码")}</h2><button className="icon-button" aria-label="关闭兑换码生成" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>礼品卡模板<select value={templateID} required onChange={(event) => setTemplateID(event.target.value)}><option value="">请选择模板</option>{templates.filter((item) => item.status).map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><div className="form-grid"><label>{translateAdmin("生成数量")}<input type="number" min={1} max={10000} value={count} onChange={(event) => setCount(event.target.value)} /></label><label>兑换码前缀<input pattern="[A-Z0-9]*" maxLength={10} value={prefix} onChange={(event) => setPrefix(event.target.value.toUpperCase())} /></label><label>有效期（小时）<input type="number" min={1} value={expiresHours} placeholder="留空表示长期有效" onChange={(event) => setExpiresHours(event.target.value)} /></label><label>{translateAdmin("最大使用次数")}<input type="number" min={1} max={1000} value={maxUsage} onChange={(event) => setMaxUsage(event.target.value)} /></label><label className="switch-label"><input type="checkbox" checked={downloadCSV} onChange={(event) => setDownloadCSV(event.target.checked)} />{translateAdmin("导出CSV")}</label></div>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button type="button" className="button ghost" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" disabled={saving}>{saving ? "正在生成…" : translateAdmin("生成兑换码")}</button></div></form></Modal>;
}

function CodeEditor({ api, code, onClose, onSaved }: { api: GiftCardManagementAPI; code: GiftCardCode; onClose: () => void; onSaved: () => void }) {
  const [value, setValue] = useState(code.code); const [status, setStatus] = useState<GiftCardCodeStatus>(code.status);
  const [expiresAt, setExpiresAt] = useState(localDateTimeInput(code.expires_at)); const [maxUsage, setMaxUsage] = useState(String(code.max_usage));
  const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const submit = async (event: FormEvent) => { event.preventDefault(); setSaving(true); setError(""); try {
    await api.updateGiftCardCode(code.id, { code: value.trim().toUpperCase(), status, expires_at: expiresAt === "" ? null : Math.floor(new Date(expiresAt).getTime() / 1000), max_usage: numberValue(maxUsage) }); onSaved();
  } catch (cause) { setError(cause instanceof Error ? cause.message : "保存失败"); setSaving(false); } };
  return <Modal title="编辑兑换码" onClose={onClose}><div className="modal-header"><h2>编辑兑换码</h2><button className="icon-button" aria-label="关闭兑换码编辑" onClick={onClose}>×</button></div><form className="form-stack" onSubmit={(event) => void submit(event)}><label>{translateAdmin("兑换码")}<input required minLength={8} maxLength={32} pattern="[A-Z0-9]+" value={value} onChange={(event) => setValue(event.target.value.toUpperCase())} /></label><div className="form-grid"><label>兑换码状态<select value={status} onChange={(event) => setStatus(Number(event.target.value) as GiftCardCodeStatus)}>{codeStatusNames.map((label, itemStatus) => <option value={itemStatus} key={label}>{label}</option>)}</select></label><label>{translateAdmin("过期时间")}<input type="datetime-local" value={expiresAt} onChange={(event) => setExpiresAt(event.target.value)} /></label><label>{translateAdmin("最大使用次数")}<input type="number" min={Math.max(1, code.usage_count)} max={1000} value={maxUsage} onChange={(event) => setMaxUsage(event.target.value)} /></label></div>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存兑换码"}</button></div></form></Modal>;
}

function Pagination({ page, total, pageSize, onPage }: { page: number; total: number; pageSize: number; onPage: (page: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  if (pages <= 1) return null;
  return <div className="pagination-footer"><button className="button secondary compact" disabled={page <= 1} onClick={() => onPage(page - 1)}>{translateAdmin("上一页")}</button><span>{translateAdmin("第")}{page} / {pages} 页，共 {total} 条</span><button className="button secondary compact" disabled={page >= pages} onClick={() => onPage(page + 1)}>{translateAdmin("下一页")}</button></div>;
}

function PlanIDs({ plans, label, placeholder, value, onChange }: { plans: Plan[]; label: string; placeholder: string; value: number[]; onChange: (value: number[]) => void }) {
  const options = plans.map(plan => ({ value: String(plan.id), label: plan.name }));
  // Keep references to unavailable plans visible until the administrator removes them.
  for (const id of value) if (!plans.some(plan => plan.id === id)) options.push({ value: String(id), label: `套餐 #${id}` });
  return <div className="gift-field gift-wide gift-plan-select"><span>{label}</span><NodeFilter label={label} displayLabel={value.length ? options.filter(option => value.includes(Number(option.value))).map(option => option.label).join("、") : placeholder} options={options} value={value.map(String)} onChange={values => onChange(values.map(Number))} /></div>;
}
function rewardSummary(value: GiftCardReward) { const parts: string[] = []; if ((value.balance ?? 0) > 0) parts.push(`余额 ¥${centsToYuan(value.balance ?? 0)}`); if ((value.transfer_enable ?? 0) > 0) parts.push(`流量 ${bytesToGiB(value.transfer_enable ?? 0)} GB`); if (value.plan_id != null) parts.push(`套餐 #${value.plan_id}`); if ((value.expire_days ?? 0) > 0) parts.push(`${value.expire_days} 天`); if ((value.random_rewards?.length ?? 0) > 0) parts.push(`${value.random_rewards?.length} 项随机奖励`); return parts.join(" · ") || "流量重置"; }
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

function ratioBasisPoints(value: string, label: string, maximum = 100): number | undefined {
  if (value.trim() === "") return undefined;
  if (!/^\d+(?:\.\d{1,4})?$/.test(value.trim())) throw new Error(`${label}最多保留四位小数`);
  const ratio = Number(value);
  if (!Number.isFinite(ratio) || ratio < 0 || ratio > maximum) throw new Error(`${label}必须在 0 到 ${maximum} 之间`);
  return Math.round(ratio * 10_000);
}
