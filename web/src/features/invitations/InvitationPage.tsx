import { type FormEvent, useEffect, useState } from "react";

import type { CommissionLogPage, CommissionTransferResult, InvitationCode, InvitationSummary, Ticket } from "../../lib/api";

type Locale = "zh-CN" | "en-US";

interface InvitationPageAPI {
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
  listCommissionLogs: (page?: number, pageSize?: number) => Promise<CommissionLogPage>;
  transferCommission: (amount: number) => Promise<CommissionTransferResult>;
  requestCommissionWithdrawal: (withdrawMethod: string, withdrawAccount: string) => Promise<Ticket>;
}

const emptySummary: InvitationSummary = {
  codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0,
  commission_rate: 0, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0,
  withdraw_enabled: false, withdraw_limit: 0, withdraw_methods: []
};
const emptyHistory: CommissionLogPage = { items: [], total: 0, page: 1, page_size: 50 };

const copy = {
  "zh-CN": {
    title: "我的邀请", subtitle: "生成邀请码并查看邀请关系与佣金。", inviteUsers: "已邀请用户",
    validCommission: "有效佣金", pendingCommission: "确认中佣金", rate: "佣金比例",
    availableCommission: "可用佣金", codeHeading: "邀请码", generateCode: "生成邀请码",
    noCode: "暂无可用邀请码", loadingCodes: "正在加载邀请码…", views: "访问次数",
    created: "创建时间", actions: "操作", copyLink: "复制邀请链接", generated: "邀请码已生成",
    copied: "邀请链接已复制", copyFailed: "复制邀请链接失败", transfer: "佣金划转余额",
    transferAmount: "划转金额（CNY）", transferHint: "划转后佣金余额会立即转入账户余额。",
    success: "操作成功", invalidAmount: "请输入最多两位小数且大于 0 的金额",
    history: "佣金记录", orderNo: "订单号", orderAmount: "订单金额", empty: "暂无数据",
    withdraw: "佣金提现", withdrawMethod: "提现方式", withdrawAccount: "提现账号",
    withdrawHint: "提交后系统会创建高优先级提现工单，佣金余额由管理员处理后结算。",
    withdrawDisabled: "管理员当前未开放佣金提现。", withdrawUnavailable: "当前没有可用的提现方式。",
    withdrawCreated: "提现工单已创建，可在“我的工单”查看。", invalidAccount: "请输入有效的提现账号",
    retry: "重新加载", requestFailed: "邀请码请求失败"
  },
  "en-US": {
    title: "My Invitations", subtitle: "Create invitation codes and review referral commissions.", inviteUsers: "Invited users",
    validCommission: "Valid commission", pendingCommission: "Pending commission", rate: "Commission rate",
    availableCommission: "Available commission", codeHeading: "Invite code", generateCode: "Generate code",
    noCode: "No invite code", loadingCodes: "Loading invite codes…", views: "Views",
    created: "Created", actions: "Actions", copyLink: "Copy invite link", generated: "Invite code created",
    copied: "Invite link copied", copyFailed: "Could not copy invite link", transfer: "Transfer commission",
    transferAmount: "Transfer amount (CNY)", transferHint: "Transferred commission is added to the account balance immediately.",
    success: "Success", invalidAmount: "Enter an amount greater than zero with at most two decimal places",
    history: "Commission history", orderNo: "Order", orderAmount: "Amount", empty: "No data",
    withdraw: "Withdraw commission", withdrawMethod: "Withdrawal method", withdrawAccount: "Withdrawal account",
    withdrawHint: "Submitting creates a high-priority support ticket. An administrator settles the balance after review.",
    withdrawDisabled: "Commission withdrawal is currently disabled.", withdrawUnavailable: "No withdrawal method is available.",
    withdrawCreated: "Withdrawal ticket created. You can review it under My Tickets.", invalidAccount: "Enter a valid withdrawal account",
    retry: "Retry", requestFailed: "Invitation request failed"
  }
} as const;

export function InvitationPage({ api, locale = "zh-CN", allowWithdrawal = true }: { api: InvitationPageAPI; locale?: Locale; allowWithdrawal?: boolean }) {
  const labels = copy[locale];
  const [summary, setSummary] = useState(emptySummary);
  const [history, setHistory] = useState(emptyHistory);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"" | "generate" | "transfer" | "withdraw">("");
  const [transferAmount, setTransferAmount] = useState("");
  const [withdrawMethod, setWithdrawMethod] = useState("");
  const [withdrawAccount, setWithdrawAccount] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  const refresh = async () => {
    const [nextSummary, nextHistory] = await Promise.all([
      api.getInvitations(), api.listCommissionLogs(1, 50)
    ]);
    setSummary(nextSummary);
    setHistory(nextHistory);
  };

  useEffect(() => {
    let active = true;
    Promise.all([api.getInvitations(), api.listCommissionLogs(1, 50)]).then(([nextSummary, nextHistory]) => {
      if (!active) return;
      setSummary(nextSummary);
      setHistory(nextHistory);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause, labels.requestFailed));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api, labels.requestFailed]);

  const generate = async () => {
    setBusy("generate");
    setError("");
    setMessage("");
    try {
      await api.createInvitation();
      await refresh();
      setMessage(labels.generated);
    } catch (cause) {
      setError(messageOf(cause, labels.requestFailed));
    } finally {
      setBusy("");
    }
  };

  const transfer = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setMessage("");
    const amount = parseCents(transferAmount);
    if (amount === null) {
      setError(labels.invalidAmount);
      return;
    }
    setBusy("transfer");
    try {
      await api.transferCommission(amount);
      setTransferAmount("");
      await refresh();
      setMessage(labels.success);
    } catch (cause) {
      setError(messageOf(cause, labels.requestFailed));
    } finally {
      setBusy("");
    }
  };

  const withdraw = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setMessage("");
    const account = withdrawAccount.trim();
    const method = summary.withdraw_methods.includes(withdrawMethod) ? withdrawMethod : (summary.withdraw_methods[0] ?? "");
    if (method === "" || account === "" || new TextEncoder().encode(account).length > 512 || containsUnsafeControl(account)) {
      setError(labels.invalidAccount);
      return;
    }
    setBusy("withdraw");
    try {
      await api.requestCommissionWithdrawal(method, account);
      setWithdrawAccount("");
      setMessage(labels.withdrawCreated);
    } catch (cause) {
      setError(messageOf(cause, labels.requestFailed));
    } finally {
      setBusy("");
    }
  };

  const copyLink = async (code: string) => {
    setError("");
    setMessage("");
    try {
      await navigator.clipboard.writeText(`${window.location.origin}/#/register?code=${encodeURIComponent(code)}`);
      setMessage(labels.copied);
    } catch {
      setError(labels.copyFailed);
    }
  };

  return <main className="page-shell invitation-page">
    <header className="page-header"><div><p className="eyebrow">Referral</p><h1>{labels.title}</h1><p className="muted">{labels.subtitle}</p></div></header>
    <div className="system-overview-grid invitation-overview">
      <Metric label={labels.inviteUsers} value={String(summary.invited_count)} kind="good" />
      <Metric label={labels.validCommission} value={formatCents(summary.valid_commission)} />
      <Metric label={labels.pendingCommission} value={formatCents(summary.pending_commission)} kind="warning" />
      <Metric label={labels.rate} value={summary.commission_distribution_enabled
        ? summary.commission_distribution_rates.map((rate) => `${rate}%`).join(" / ")
        : `${summary.commission_rate}%`} />
      <Metric label={labels.availableCommission} value={formatCents(summary.available_commission)} kind="good" />
    </div>
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {message !== "" && <div className="alert success global-alert" role="status">{message}</div>}
    <div className="invitation-action-grid">
      <section className="system-section invitation-panel" aria-labelledby="invitation-codes-heading">
        <div className="section-heading"><div><h2 id="invitation-codes-heading">{labels.codeHeading}</h2><p className="muted">PV · {labels.views}</p></div><button className="button primary" type="button" disabled={busy !== ""} onClick={() => void generate()}>{busy === "generate" ? labels.loadingCodes : labels.generateCode}</button></div>
        {loading && summary.codes.length === 0 ? <div className="empty-card compact-empty">{labels.loadingCodes}</div> : summary.codes.length === 0 ? <div className="empty-card compact-empty">{labels.noCode}</div> : <div className="resource-table-wrap">
          <table className="resource-table"><thead><tr><th>{labels.codeHeading}</th><th>{labels.views}</th><th>{labels.created}</th><th>{labels.actions}</th></tr></thead><tbody>
            {summary.codes.map((code) => <tr key={code.code}><td data-label={labels.codeHeading}><code className="monospace">{code.code}</code></td><td data-label={labels.views}>{code.pv}</td><td data-label={labels.created}>{formatDate(code.created_at, locale)}</td><td data-label={labels.actions}><button className="button secondary compact" type="button" onClick={() => void copyLink(code.code)}>{labels.copyLink}</button></td></tr>)}
          </tbody></table>
        </div>}
      </section>
      <section className="system-section invitation-panel" aria-labelledby="commission-transfer-heading">
        <div className="section-heading"><div><h2 id="commission-transfer-heading">{labels.transfer}</h2><p className="muted">{labels.transferHint}</p></div></div>
        <form className="invitation-transfer-form" onSubmit={(event) => void transfer(event)}>
          <label>{labels.transferAmount}<input inputMode="decimal" autoComplete="off" placeholder="0.00" value={transferAmount} onChange={(event) => setTransferAmount(event.target.value)} /></label>
          <button className="button primary" type="submit" disabled={busy !== ""}>{busy === "transfer" ? "…" : labels.transfer}</button>
        </form>
      </section>
      {allowWithdrawal && <section className="system-section invitation-panel" aria-labelledby="commission-withdraw-heading">
        <div className="section-heading"><div><h2 id="commission-withdraw-heading">{labels.withdraw}</h2><p className="muted">{labels.withdrawHint}</p></div></div>
        {loading ? <div className="empty-card compact-empty">{labels.loadingCodes}</div> : !summary.withdraw_enabled ? <p className="alert warning">{labels.withdrawDisabled}</p> : summary.withdraw_methods.length === 0 ? <p className="alert warning">{labels.withdrawUnavailable}</p> : <form className="invitation-transfer-form" onSubmit={(event) => void withdraw(event)}>
          <p className="small muted">{labels.availableCommission}: {formatCents(summary.available_commission)} · {labels.withdraw}: ≥ {formatMajorAmount(summary.withdraw_limit)}</p>
          <label>{labels.withdrawMethod}<select value={summary.withdraw_methods.includes(withdrawMethod) ? withdrawMethod : summary.withdraw_methods[0]} onChange={(event) => setWithdrawMethod(event.target.value)}>{summary.withdraw_methods.map((method) => <option key={method} value={method}>{method}</option>)}</select></label>
          <label>{labels.withdrawAccount}<input autoComplete="off" maxLength={512} value={withdrawAccount} onChange={(event) => setWithdrawAccount(event.target.value)} /></label>
          <button className="button primary" type="submit" disabled={busy !== ""}>{busy === "withdraw" ? "…" : labels.withdraw}</button>
        </form>}
      </section>}
    </div>
    <section className="system-section" aria-labelledby="commission-history-heading">
      <div className="section-heading"><h2 id="commission-history-heading">{labels.history}</h2></div>
      <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>{labels.orderNo}</th><th>{labels.orderAmount}</th><th>{labels.validCommission}</th><th>{labels.created}</th></tr></thead><tbody>
        {history.items.map((item) => <tr key={item.id}><td data-label={labels.orderNo}><strong className="monospace">{item.trade_no}</strong></td><td data-label={labels.orderAmount}>{formatCents(item.order_amount)}</td><td data-label={labels.validCommission}>{formatCents(item.get_amount)}</td><td data-label={labels.created}>{formatDate(item.created_at, locale)}</td></tr>)}
        {!loading && history.items.length === 0 && <tr className="empty-table-row"><td colSpan={4}>{labels.empty}</td></tr>}
        {loading && history.items.length === 0 && <tr className="empty-table-row"><td colSpan={4}>{labels.loadingCodes}</td></tr>}
      </tbody></table></div>
      {!loading && error !== "" && summary.codes.length === 0 && history.items.length === 0 && <button className="button secondary invitation-retry" type="button" onClick={() => { setLoading(true); setError(""); void refresh().catch((cause) => setError(messageOf(cause, labels.requestFailed))).finally(() => setLoading(false)); }}>{labels.retry}</button>}
    </section>
  </main>;
}

function Metric({ label, value, kind = "" }: { label: string; value: string; kind?: string }) {
  return <article className={`overview-metric ${kind}`.trim()}><span>{label}</span><strong>{value}</strong></article>;
}

function parseCents(value: string): number | null {
  const normalized = value.trim();
  if (!/^(?:0|[1-9]\d*)(?:\.\d{1,2})?$/.test(normalized)) return null;
  const [yuan = "", fraction = ""] = normalized.split(".");
  const cents = BigInt(yuan) * 100n + BigInt(fraction.padEnd(2, "0"));
  if (cents < 1n || cents > 9_000_000_000_000_000n) return null;
  return Number(cents);
}

function formatCents(value: number) {
  return `¥${(value / 100).toFixed(2)}`;
}

function formatMajorAmount(value: number) {
  return `¥${value.toFixed(2)}`;
}

function containsUnsafeControl(value: string) {
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code < 0x20 || code === 0x7f) return true;
  }
  return false;
}

function formatDate(value: string, locale: Locale) {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function messageOf(cause: unknown, fallback: string) {
  return cause instanceof Error ? cause.message : fallback;
}
