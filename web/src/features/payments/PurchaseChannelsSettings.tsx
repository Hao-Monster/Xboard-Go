import { useEffect, useState, type FormEvent } from "react";
import type { PurchaseChannels, PurchaseChannelsAdminAPI } from "../../lib/api";

export function PurchaseChannelsSettings({ api }: { api: PurchaseChannelsAdminAPI }) {
  const [draft, setDraft] = useState<PurchaseChannels | null>(null);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [saving, setSaving] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    void api.getAdminPurchaseChannels().then(value => { if (active) { setDraft(value); setError(""); } })
      .catch((cause: unknown) => { if (active) setError(cause instanceof Error ? cause.message : "购买渠道加载失败"); });
    return () => { active = false; };
  }, [api, attempt]);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (!draft || saving) return;
    setSaving(true); setSaved(false); setError("");
    try { setDraft(await api.updatePurchaseChannels(draft)); setSaved(true); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "购买渠道保存失败"); }
    finally { setSaving(false); }
  };
  const change = (field: keyof Omit<PurchaseChannels, "revision">, value: string) => { if (draft) setDraft({ ...draft, [field]: value }); setSaved(false); };
  return <section className="panel purchase-channel-settings" aria-label="兑换码购买渠道"><h2>兑换码购买渠道</h2>
    <p className="muted">没有可用在线支付方式时，向用户展示已配置的购买渠道。留空则关闭该渠道；不影响现有支付方式。</p>
    {error && <div role="alert" className="alert error">{error}<button type="button" className="button ghost compact" disabled={saving} onClick={() => { setSaved(false); setAttempt(value => value + 1); }}>重新加载配置</button></div>}
    {draft ? <form className="form-stack" onSubmit={event => void submit(event)}><fieldset className="settings-fieldset form-stack" disabled={saving}>
      <label>发卡网购买地址<input type="url" maxLength={2048} value={draft.card_store_url} placeholder="https://发卡网商品页或店铺地址" onChange={event => change("card_store_url", event.target.value)} /></label>
      <p className="muted">可填写店铺入口。用户需按套餐名称和周期选择对应兑换码；不会向发卡网自动传递订单或账号信息。</p>
      <div className="form-grid"><label>Chatwoot 服务地址<input type="url" maxLength={2048} value={draft.chatwoot_base_url} placeholder="https://chat.example.com" onChange={event => change("chatwoot_base_url", event.target.value)} /></label>
      <label>Chatwoot 网站标识<input autoComplete="off" maxLength={128} value={draft.chatwoot_website_token} placeholder="Website Token" onChange={event => change("chatwoot_website_token", event.target.value)} /></label></div>
      <p className="muted">填写网页收件箱的公开 Website Token，不是管理员 API Token。客服人工确认收款、发送兑换码，系统不会自动确认付款。</p>
      {saved && <div role="status" className="alert success">购买渠道已保存</div>}
      <div className="form-actions"><button type="submit" className="button primary" disabled={saving}>{saving ? "正在保存…" : "保存购买渠道"}</button></div>
    </fieldset></form> : !error && <p role="status">正在加载购买渠道…</p>}
  </section>;
}
