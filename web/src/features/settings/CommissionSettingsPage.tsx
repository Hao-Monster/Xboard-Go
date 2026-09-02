import { useEffect, useState, type FormEvent } from "react";

import type { AdminAPI, CommissionSettings, CommissionSettingsInput, CommissionWithdrawal, CommissionWithdrawalPage } from "../../lib/api";

type CommissionSettingsAPI = Pick<AdminAPI, "getCommissionSettings" | "updateCommissionSettings" | "listAdminCommissionWithdrawals" |
  "getAdminCommissionWithdrawalAccount" | "approveCommissionWithdrawal" | "rejectCommissionWithdrawal" | "payCommissionWithdrawal">;
type Draft = Omit<CommissionSettingsInput, "revision">;

export function CommissionSettingsPage({ api }: { api: CommissionSettingsAPI }) {
  const [current, setCurrent] = useState<CommissionSettings | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const [withdrawals, setWithdrawals] = useState<CommissionWithdrawalPage>({ items: [], total: 0, page: 1, page_size: 50 });
  const [withdrawalsLoading, setWithdrawalsLoading] = useState(true);
  const [withdrawalAction, setWithdrawalAction] = useState<number | null>(null);
  const [revealedAccount, setRevealedAccount] = useState<{ id: number; account: string } | null>(null);

  const apply = (settings: CommissionSettings) => {
    setCurrent(settings);
    setDraft(toDraft(settings));
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      apply(await api.getCommissionSettings());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.getCommissionSettings().then((settings) => {
      if (active) apply(settings);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const loadWithdrawals = async () => {
    setWithdrawalsLoading(true);
    try {
      setWithdrawals(await api.listAdminCommissionWithdrawals(undefined, 1, 50));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setWithdrawalsLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.listAdminCommissionWithdrawals(undefined, 1, 50).then((page) => {
      if (active) setWithdrawals(page);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setWithdrawalsLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  useEffect(() => {
    if (revealedAccount === null) return undefined;
    const timer = window.setTimeout(() => setRevealedAccount(null), 30_000);
    return () => window.clearTimeout(timer);
  }, [revealedAccount]);

  const transitionWithdrawal = async (withdrawal: CommissionWithdrawal, action: "approve" | "reject" | "pay") => {
    let detail = "";
    if (action === "reject") {
      detail = window.prompt("请输入拒绝原因")?.trim() ?? "";
      if (detail === "") return;
    } else if (action === "pay") {
      detail = window.prompt("请输入支付流水号（将用于防重复支付）")?.trim() ?? "";
      if (detail === "") return;
    } else if (!window.confirm(`确认批准提现 #${withdrawal.id}？`)) return;
    setWithdrawalAction(withdrawal.id);
    setError("");
    try {
      if (action === "approve") await api.approveCommissionWithdrawal(withdrawal.id, withdrawal.revision);
      else if (action === "reject") await api.rejectCommissionWithdrawal(withdrawal.id, withdrawal.revision, detail);
      else await api.payCommissionWithdrawal(withdrawal.id, withdrawal.revision, detail);
      setRevealedAccount(null);
      await loadWithdrawals();
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setWithdrawalAction(null);
    }
  };

  const revealWithdrawalAccount = async (withdrawal: CommissionWithdrawal) => {
    setWithdrawalAction(withdrawal.id);
    setError("");
    try {
      setRevealedAccount({ id: withdrawal.id, account: await api.getAdminCommissionWithdrawalAccount(withdrawal.id) });
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setWithdrawalAction(null);
    }
  };

  const updateDraft = <K extends keyof Draft,>(field: K, value: Draft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [field]: value });
    setSaved(false);
    setError("");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    setSaved(false);
    setError("");
    const percentages = [draft.invite_commission, draft.commission_distribution_l1,
      draft.commission_distribution_l2, draft.commission_distribution_l3];
    if (percentages.some((value) => !Number.isInteger(value) || value < 0 || value > 100)) {
      setError("佣金比例必须是 0 到 100 的整数");
      return;
    }
    const withdrawLimitCents = draft.commission_withdraw_limit * 100;
    if (!Number.isFinite(withdrawLimitCents) || draft.commission_withdraw_limit < 0 || draft.commission_withdraw_limit > 90_000_000_000_000 ||
      Math.abs(withdrawLimitCents - Math.round(withdrawLimitCents)) > 1e-6) {
      setError("最低提现金额必须是非负数且最多保留两位小数");
      return;
    }
    const withdrawMethods = draft.commission_withdraw_method.map((method) => method.trim()).filter((method) => method !== "");
    if (withdrawMethods.length > 32 || withdrawMethods.some((method) => new TextEncoder().encode(method).length > 64)) {
      setError("提现方式最多 32 项，每项不得为空且不能超过 64 字节");
      return;
    }
    const total = draft.commission_distribution_l1 + draft.commission_distribution_l2 + draft.commission_distribution_l3;
    if (total > 100) {
      setError("三级分佣比例合计不能超过 100%");
      return;
    }
    setSaving(true);
    try {
      apply(await api.updateCommissionSettings({ revision: current.revision, ...draft, commission_withdraw_method: withdrawMethods }));
      setSaved(true);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };

  const rateValue = (field: "commission_distribution_l1" | "commission_distribution_l2" | "commission_distribution_l3") =>
    draft === null ? 0 : draft[field];
  const effectiveRates = draft === null ? [] : [
    effectiveRate(draft.invite_commission, draft.commission_distribution_l1),
    effectiveRate(draft.invite_commission, draft.commission_distribution_l2),
    effectiveRate(draft.invite_commission, draft.commission_distribution_l3)
  ];

  return <main className="page-shell commission-settings-page">
    <header className="page-header"><div><p className="eyebrow">Referral commission</p><h1>佣金设置</h1><p className="muted">配置邀请订单返佣、确认方式与三级分佣规则。</p></div></header>
    {loading && draft === null && <div className="empty-card" aria-live="polite">正在加载佣金设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {draft === null && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载佣金设置</button>}
    {draft !== null && current !== null && <section className="site-settings-card" aria-labelledby="commission-settings-heading">
      <div className="section-heading"><div><h2 id="commission-settings-heading">邀请佣金设置</h2><p className="muted">所有比例使用整数百分比；保存后仅影响后续订单和待处理佣金。</p></div><span className="count-pill">Revision {current.revision}</span></div>
      <form className="form-stack commission-settings-form" onSubmit={(event) => void save(event)}>
        <fieldset className="settings-fieldset">
          <legend>基础返佣</legend>
          <div className="commission-settings-grid">
            <label>全局邀请佣金比例（%）<input type="number" required min={0} max={100} step={1} value={draft.invite_commission} onChange={(event) => updateDraft("invite_commission", numberValue(event.currentTarget))} /></label>
            <div className="commission-switches">
              <label className="switch-label"><input type="checkbox" checked={draft.commission_first_time_enable} onChange={(event) => updateDraft("commission_first_time_enable", event.target.checked)} />仅首次有效订单返佣</label>
              <label className="switch-label"><input type="checkbox" checked={draft.commission_auto_check_enable} onChange={(event) => updateDraft("commission_auto_check_enable", event.target.checked)} />自动确认到期佣金</label>
              <label className="switch-label"><input type="checkbox" checked={draft.withdraw_close_enable} onChange={(event) => updateDraft("withdraw_close_enable", event.target.checked)} />佣金直接计入账户余额</label>
            </div>
          </div>
          {draft.withdraw_close_enable && <p className="alert warning">启用后，已确认佣金会直接计入账户余额，不再进入可划转的佣金余额。</p>}
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>提现兼容设置</legend>
          <div className="commission-settings-grid">
            <label>最低提现金额<input type="number" required min={0} max={90_000_000_000_000} step={0.01} value={draft.commission_withdraw_limit} onChange={(event) => updateDraft("commission_withdraw_limit", numberValue(event.currentTarget))} /></label>
            <label>允许的提现方式（每行一个）<textarea rows={4} value={draft.commission_withdraw_method.join("\n")} onChange={(event) => updateDraft("commission_withdraw_method", event.target.value.split("\n"))} /></label>
          </div>
          <p className="small muted">该设置直接控制原子提现账本；M1 手续费率固定为 0，未来收费必须另行决策。</p>
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>三级分佣</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.commission_distribution_enable} onChange={(event) => updateDraft("commission_distribution_enable", event.target.checked)} />启用三级分佣</label>
          <div className="commission-level-grid">
            {(["commission_distribution_l1", "commission_distribution_l2", "commission_distribution_l3"] as const).map((field, index) => <label key={field}>{["一级", "二级", "三级"][index]}分佣比例（%）<input type="number" required min={0} max={100} step={1} disabled={!draft.commission_distribution_enable} value={rateValue(field)} onChange={(event) => updateDraft(field, numberValue(event.currentTarget))} /></label>)}
          </div>
          <p className="small muted">三级合计 {draft.commission_distribution_l1 + draft.commission_distribution_l2 + draft.commission_distribution_l3}%（不得超过 100%）。</p>
          {draft.commission_distribution_enable && <p className="commission-rate-preview">当前用户侧有效比例：{effectiveRates.map((rate) => `${rate}%`).join(" / ")}</p>}
        </fieldset>
        {saved && <div className="alert success" role="status">佣金设置已保存</div>}
        <div className="form-actions">
          {error !== "" && <button className="button secondary" type="button" disabled={saving} onClick={() => void load()}>刷新最新设置</button>}
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存佣金设置"}</button>
        </div>
      </form>
    </section>}
    <section className="system-section" aria-labelledby="commission-withdrawals-heading">
      <div className="section-heading"><div><h2 id="commission-withdrawals-heading">提现审核</h2><p className="muted">批准只确认申请，支付必须再提交唯一流水号；拒绝会原子退回冻结佣金。</p></div><button className="button secondary compact" type="button" disabled={withdrawalsLoading} onClick={() => void loadWithdrawals()}>刷新</button></div>
      {revealedAccount !== null && <div className="alert warning" role="status">申请 #{revealedAccount.id} 收款账户：<code>{revealedAccount.account}</code>（30 秒后自动隐藏）</div>}
      <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>ID</th><th>用户</th><th>金额</th><th>方式 / 账户</th><th>状态</th><th>操作</th></tr></thead><tbody>
        {withdrawals.items.map((item) => <tr key={item.id}><td>#{item.id}</td><td>{item.user_email || `#${item.user_id}`}</td><td>{formatWithdrawalAmount(item.amount, item.currency)}</td><td>{item.method}<small className="muted">{item.account_masked}</small></td><td>{withdrawalStatusLabel(item.status)}<small className="muted">Revision {item.revision}</small></td><td><div className="row-actions">
          <button className="button ghost compact" type="button" disabled={withdrawalAction !== null} onClick={() => void revealWithdrawalAccount(item)}>查看账户</button>
          {item.status === "pending" && <button className="button primary compact" type="button" disabled={withdrawalAction !== null} onClick={() => void transitionWithdrawal(item, "approve")}>批准</button>}
          {(item.status === "pending" || item.status === "approved") && <button className="button danger compact" type="button" disabled={withdrawalAction !== null} onClick={() => void transitionWithdrawal(item, "reject")}>拒绝</button>}
          {item.status === "approved" && <button className="button primary compact" type="button" disabled={withdrawalAction !== null} onClick={() => void transitionWithdrawal(item, "pay")}>确认支付</button>}
        </div></td></tr>)}
        {!withdrawalsLoading && withdrawals.items.length === 0 && <tr className="empty-table-row"><td colSpan={6}>暂无提现申请。</td></tr>}
        {withdrawalsLoading && withdrawals.items.length === 0 && <tr className="empty-table-row"><td colSpan={6}>正在读取提现申请…</td></tr>}
      </tbody></table></div>
    </section>
  </main>;
}

function toDraft(settings: CommissionSettings): Draft {
  return {
    invite_commission: settings.invite_commission,
    commission_first_time_enable: settings.commission_first_time_enable,
    commission_auto_check_enable: settings.commission_auto_check_enable,
    commission_withdraw_limit: settings.commission_withdraw_limit,
    commission_withdraw_method: [...settings.commission_withdraw_method],
    withdraw_close_enable: settings.withdraw_close_enable,
    commission_distribution_enable: settings.commission_distribution_enable,
    commission_distribution_l1: settings.commission_distribution_l1,
    commission_distribution_l2: settings.commission_distribution_l2,
    commission_distribution_l3: settings.commission_distribution_l3
  };
}

function effectiveRate(base: number, level: number) {
  return Math.floor(base * level / 100);
}

function numberValue(input: HTMLInputElement) {
  return Number.isNaN(input.valueAsNumber) ? 0 : input.valueAsNumber;
}

function messageOf(cause: unknown) {
  return cause instanceof Error ? cause.message : "佣金设置请求失败";
}

function formatWithdrawalAmount(amount: number, currency: string) {
  return new Intl.NumberFormat("zh-CN", { style: "currency", currency }).format(amount / 100);
}

function withdrawalStatusLabel(status: CommissionWithdrawal["status"]) {
  return { pending: "待审核", approved: "已批准待支付", paid: "已支付", rejected: "已拒绝" }[status];
}
