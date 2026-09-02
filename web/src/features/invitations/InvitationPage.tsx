import { type FormEvent, useEffect, useRef, useState } from "react";

import type { CommissionLogPage, CommissionTransferResult, CommissionWithdrawal, CommissionWithdrawalPage, CommissionWithdrawalPolicy, InvitationCode, InvitationSummary } from "../../lib/api";
import { secureRandomUUID } from "../../lib/random";

type Locale = "zh-CN" | "en-US";

interface InvitationPageAPI {
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
  listCommissionLogs: (page?: number, pageSize?: number) => Promise<CommissionLogPage>;
  transferCommission: (amount: number, idempotencyKey: string) => Promise<CommissionTransferResult>;
  getCommissionWithdrawalPolicy: () => Promise<CommissionWithdrawalPolicy>;
  listCommissionWithdrawals: (page?: number, pageSize?: number) => Promise<CommissionWithdrawalPage>;
  createCommissionWithdrawal: (idempotencyKey: string, method: string, account: string) => Promise<CommissionWithdrawal>;
}

const emptySummary: InvitationSummary = {
  codes: [], invited_count: 0, valid_commission: 0, pending_commission: 0,
  commission_rate: 0, commission_distribution_enabled: false, commission_distribution_rates: [], available_commission: 0
};
const emptyHistory: CommissionLogPage = { items: [], total: 0, page: 1, page_size: 50 };
const emptyWithdrawals: CommissionWithdrawalPage = { items: [], total: 0, page: 1, page_size: 20 };

const copy = {
  "zh-CN": {
    title: "我的邀请", subtitle: "生成邀请码并查看邀请关系与佣金。", inviteUsers: "已邀请用户",
    validCommission: "有效佣金", pendingCommission: "确认中佣金", rate: "佣金比例",
    availableCommission: "可用佣金", codeHeading: "邀请码", generateCode: "生成邀请码",
    noCode: "暂无可用邀请码", loadingCodes: "正在加载邀请码…", views: "访问次数",
    created: "创建时间", actions: "操作", copyLink: "复制邀请链接", generated: "邀请码已生成",
    copied: "邀请链接已复制", copyFailed: "复制邀请链接失败", transfer: "佣金划转余额",
    transferAmount: "划转金额", transferHint: "划转后佣金余额会立即转入账户余额。",
    success: "操作成功", invalidAmount: "请输入最多两位小数且大于 0 的金额",
    history: "佣金记录", orderNo: "订单号", orderAmount: "订单金额", empty: "暂无数据",
    retry: "重新加载", requestFailed: "邀请码请求失败", withdrawal: "佣金提现",
    withdrawalHint: "提交时会冻结全部可用佣金；当前不收手续费，管理员批准并确认支付后完成。",
    method: "提现方式", account: "收款账户", submitWithdrawal: "提交提现申请",
    minimum: "最低提现", frozen: "已冻结", withdrawalHistory: "提现记录", status: "状态",
    withdrawSubmitted: "提现申请已提交", activeWithdrawal: "已有待处理申请", accountMasked: "收款账户"
  },
  "en-US": {
    title: "My Invitations", subtitle: "Create invitation codes and review referral commissions.", inviteUsers: "Invited users",
    validCommission: "Valid commission", pendingCommission: "Pending commission", rate: "Commission rate",
    availableCommission: "Available commission", codeHeading: "Invite code", generateCode: "Generate code",
    noCode: "No invite code", loadingCodes: "Loading invite codes…", views: "Views",
    created: "Created", actions: "Actions", copyLink: "Copy invite link", generated: "Invite code created",
    copied: "Invite link copied", copyFailed: "Could not copy invite link", transfer: "Transfer commission",
    transferAmount: "Transfer amount", transferHint: "Transferred commission is added to the account balance immediately.",
    success: "Success", invalidAmount: "Enter an amount greater than zero with at most two decimal places",
    history: "Commission history", orderNo: "Order", orderAmount: "Amount", empty: "No data",
    retry: "Retry", requestFailed: "Invitation request failed", withdrawal: "Commission withdrawal",
    withdrawalHint: "Submitting freezes all available commission with no fee; an administrator must pay or reject it.",
    method: "Method", account: "Payout account", submitWithdrawal: "Submit withdrawal",
    minimum: "Minimum", frozen: "Frozen", withdrawalHistory: "Withdrawal history", status: "Status",
    withdrawSubmitted: "Withdrawal submitted", activeWithdrawal: "An active withdrawal already exists", accountMasked: "Account"
  }
} as const;

export function InvitationPage({ api, locale = "zh-CN" }: { api: InvitationPageAPI; locale?: Locale }) {
  const labels = copy[locale];
  const [summary, setSummary] = useState(emptySummary);
  const [history, setHistory] = useState(emptyHistory);
  const [withdrawalPolicy, setWithdrawalPolicy] = useState<CommissionWithdrawalPolicy | null>(null);
  const [withdrawals, setWithdrawals] = useState(emptyWithdrawals);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"" | "generate" | "transfer" | "withdraw">("");
  const transferRequest = useRef<{ amount: number; idempotencyKey: string } | null>(null);
  const [transferAmount, setTransferAmount] = useState("");
  const [withdrawalMethod, setWithdrawalMethod] = useState("");
  const [withdrawalAccount, setWithdrawalAccount] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const currency = withdrawalPolicy?.currency ?? "CNY";

  const refresh = async () => {
    const [nextSummary, nextHistory, nextPolicy, nextWithdrawals] = await Promise.all([
      api.getInvitations(), api.listCommissionLogs(1, 50), api.getCommissionWithdrawalPolicy(), api.listCommissionWithdrawals(1, 20)
    ]);
    setSummary(nextSummary);
    setHistory(nextHistory);
    setWithdrawalPolicy(nextPolicy);
    setWithdrawals(nextWithdrawals);
    setWithdrawalMethod((current) => current || nextPolicy.methods[0] || "");
  };

  useEffect(() => {
    let active = true;
    Promise.all([api.getInvitations(), api.listCommissionLogs(1, 50), api.getCommissionWithdrawalPolicy(), api.listCommissionWithdrawals(1, 20)]).then(([nextSummary, nextHistory, nextPolicy, nextWithdrawals]) => {
      if (!active) return;
      setSummary(nextSummary);
      setHistory(nextHistory);
      setWithdrawalPolicy(nextPolicy);
      setWithdrawals(nextWithdrawals);
      setWithdrawalMethod(nextPolicy.methods[0] || "");
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

  const withdraw = async (event: FormEvent) => {
    event.preventDefault();
    setError("");
    setMessage("");
    if (withdrawalPolicy === null || withdrawalMethod === "" || withdrawalAccount.trim() === "") {
      setError(labels.requestFailed);
      return;
    }
    setBusy("withdraw");
    try {
      await api.createCommissionWithdrawal(secureRandomUUID(), withdrawalMethod, withdrawalAccount.trim());
      setWithdrawalAccount("");
      await refresh();
      setMessage(labels.withdrawSubmitted);
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
      if (transferRequest.current?.amount !== amount) {
        transferRequest.current = { amount, idempotencyKey: secureRandomUUID() };
      }
      await api.transferCommission(amount, transferRequest.current.idempotencyKey);
      setTransferAmount("");
      await refresh();
      transferRequest.current = null;
      setMessage(labels.success);
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
      <Metric label={labels.validCommission} value={formatCurrency(summary.valid_commission, currency, locale)} />
      <Metric label={labels.pendingCommission} value={formatCurrency(summary.pending_commission, currency, locale)} kind="warning" />
      <Metric label={labels.rate} value={summary.commission_distribution_enabled
        ? summary.commission_distribution_rates.map((rate) => `${rate}%`).join(" / ")
        : `${summary.commission_rate}%`} />
      <Metric label={labels.availableCommission} value={formatCurrency(summary.available_commission, currency, locale)} kind="good" />
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
          <label>{labels.transferAmount}（{currency}）<input inputMode="decimal" autoComplete="off" placeholder="0.00" value={transferAmount} onChange={(event) => setTransferAmount(event.target.value)} /></label>
          <button className="button primary" type="submit" disabled={busy !== ""}>{busy === "transfer" ? "…" : labels.transfer}</button>
        </form>
      </section>
    </div>
    {withdrawalPolicy !== null && <section className="system-section" aria-labelledby="commission-withdrawal-heading">
      <div className="section-heading"><div><h2 id="commission-withdrawal-heading">{labels.withdrawal}</h2><p className="muted">{labels.withdrawalHint}</p></div><span className="count-pill">{withdrawalPolicy.currency}</span></div>
      <div className="system-overview-grid invitation-overview">
        <Metric label={labels.availableCommission} value={formatCurrency(withdrawalPolicy.available_commission, withdrawalPolicy.currency, locale)} kind="good" />
        <Metric label={labels.frozen} value={formatCurrency(withdrawalPolicy.frozen_commission, withdrawalPolicy.currency, locale)} kind="warning" />
        <Metric label={labels.minimum} value={formatCurrency(withdrawalPolicy.minimum_amount, withdrawalPolicy.currency, locale)} />
      </div>
      {withdrawalPolicy.active !== null && <div className="alert warning" role="status">{labels.activeWithdrawal}：#{withdrawalPolicy.active.id} · {withdrawalStatusLabel(withdrawalPolicy.active.status, locale)}</div>}
      <form className="invitation-transfer-form" onSubmit={(event) => void withdraw(event)}>
        <label>{labels.method}<select value={withdrawalMethod} onChange={(event) => setWithdrawalMethod(event.target.value)}>{withdrawalPolicy.methods.map((method) => <option key={method} value={method}>{method}</option>)}</select></label>
        <label>{labels.account}<input autoComplete="off" maxLength={320} value={withdrawalAccount} onChange={(event) => setWithdrawalAccount(event.target.value)} /></label>
        <button className="button primary" type="submit" disabled={busy !== "" || withdrawalPolicy.active !== null || withdrawalPolicy.methods.length === 0 || withdrawalPolicy.available_commission < withdrawalPolicy.minimum_amount}>{busy === "withdraw" ? "…" : labels.submitWithdrawal}</button>
      </form>
    </section>}
    <section className="system-section" aria-labelledby="withdrawal-history-heading">
      <div className="section-heading"><h2 id="withdrawal-history-heading">{labels.withdrawalHistory}</h2></div>
      <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>ID</th><th>{labels.method}</th><th>{labels.accountMasked}</th><th>{labels.orderAmount}</th><th>{labels.status}</th><th>{labels.created}</th></tr></thead><tbody>
        {withdrawals.items.map((item) => <tr key={item.id}><td>#{item.id}</td><td>{item.method}</td><td><code>{item.account_masked}</code></td><td>{formatCurrency(item.amount, item.currency, locale)}</td><td>{withdrawalStatusLabel(item.status, locale)}</td><td>{formatDate(item.created_at, locale)}</td></tr>)}
        {!loading && withdrawals.items.length === 0 && <tr className="empty-table-row"><td colSpan={6}>{labels.empty}</td></tr>}
      </tbody></table></div>
    </section>
    <section className="system-section" aria-labelledby="commission-history-heading">
      <div className="section-heading"><h2 id="commission-history-heading">{labels.history}</h2></div>
      <div className="resource-table-wrap"><table className="resource-table"><thead><tr><th>{labels.orderNo}</th><th>{labels.orderAmount}</th><th>{labels.validCommission}</th><th>{labels.created}</th></tr></thead><tbody>
        {history.items.map((item) => <tr key={item.id}><td data-label={labels.orderNo}><strong className="monospace">{item.trade_no}</strong></td><td data-label={labels.orderAmount}>{formatCurrency(item.order_amount, currency, locale)}</td><td data-label={labels.validCommission}>{formatCurrency(item.get_amount, currency, locale)}</td><td data-label={labels.created}>{formatDate(item.created_at, locale)}</td></tr>)}
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

function formatDate(value: string, locale: Locale) {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function formatCurrency(value: number, currency: string, locale: Locale) {
  return new Intl.NumberFormat(locale, { style: "currency", currency }).format(value / 100);
}

function withdrawalStatusLabel(status: CommissionWithdrawal["status"], locale: Locale) {
  const labels = locale === "zh-CN"
    ? { pending: "待审核", approved: "已批准待支付", paid: "已支付", rejected: "已拒绝" }
    : { pending: "Pending", approved: "Approved", paid: "Paid", rejected: "Rejected" };
  return labels[status];
}

function messageOf(cause: unknown, fallback: string) {
  return cause instanceof Error ? cause.message : fallback;
}
