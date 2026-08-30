import { useEffect, useState, type FormEvent } from "react";

import type { AdminAPI, Plan, SiteSettings, SiteSettingsInput } from "../../lib/api";

type SiteSettingsAPI = Pick<AdminAPI, "getSiteSettings" | "updateSiteSettings" | "listPlans">;
type SiteDraft = Omit<SiteSettingsInput, "revision">;

export function SiteSettingsPage({ api, onIdentityChanged }: {
  api: SiteSettingsAPI;
  onIdentityChanged: (settings: SiteSettings) => void;
}) {
  const [current, setCurrent] = useState<SiteSettings | null>(null);
  const [draft, setDraft] = useState<SiteDraft | null>(null);
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  const applySettings = (settings: SiteSettings) => {
    setCurrent(settings);
    setDraft(toDraft(settings));
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      const [settings, availablePlans] = await Promise.all([api.getSiteSettings(), api.listPlans()]);
      applySettings(settings);
      setPlans(availablePlans);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void Promise.all([api.getSiteSettings(), api.listPlans()]).then(([settings, availablePlans]) => {
      if (active) {
        applySettings(settings);
        setPlans(availablePlans);
      }
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    setSaving(true);
    setError("");
    setSaved(false);
    try {
      const updated = await api.updateSiteSettings({ revision: current.revision, ...draft });
      applySettings(updated);
      onIdentityChanged(updated);
      setSaved(true);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };

  const updateDraft = <K extends keyof SiteDraft,>(field: K, value: SiteDraft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [field]: value });
    setSaved(false);
  };

  return <main className="page-shell site-settings-page">
    <header className="page-header"><div><p className="eyebrow">Configuration</p><h1>系统设置</h1><p className="muted">配置站点身份、订阅公开地址和注册安全策略。</p></div></header>
    {loading && draft === null && <div className="empty-card">正在加载站点设置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {draft === null && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载站点设置</button>}
    {draft !== null && current !== null && <section className="site-settings-card" aria-labelledby="site-settings-heading">
      <div className="section-heading"><div><h2 id="site-settings-heading">站点设置</h2><p className="muted">字段与旧 Xboard 站点设置保持同一业务含义。</p></div><span className="count-pill">Revision {current.revision}</span></div>
      <form className="form-stack site-settings-form" onSubmit={(event) => void save(event)}>
        <label>站点名称<input required value={draft.app_name} onChange={(event) => updateDraft("app_name", event.target.value)} /></label>
        <label>站点描述<textarea value={draft.app_description} onChange={(event) => updateDraft("app_description", event.target.value)} /></label>
        <div className="site-settings-url-grid">
          <label>站点网址<input type="url" placeholder="https://panel.example.com" value={draft.app_url} onChange={(event) => updateDraft("app_url", event.target.value)} /></label>
          <label>用户条款(TOS)URL<input type="url" placeholder="https://panel.example.com/terms" value={draft.tos_url} onChange={(event) => updateDraft("tos_url", event.target.value)} /></label>
          <label>LOGO<input type="url" placeholder="请输入LOGO URL，末尾不要/" value={draft.logo} onChange={(event) => updateDraft("logo", event.target.value)} /></label>
        </div>
        <fieldset className="settings-fieldset">
          <legend>访问安全</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.safe_mode_enable} onChange={(event) => updateDraft("safe_mode_enable", event.target.checked)} />安全模式（仅允许站点网址的域名访问前端）</label>
          <label>后台路径<input required minLength={1} maxLength={64} pattern="[A-Za-z0-9_-]+" value={draft.secure_path} onChange={(event) => updateDraft("secure_path", event.target.value)} /></label>
          <p className="small muted">新路径至少 8 位，仅限字母、数字、下划线和连字符。修改后旧 V2 管理接口路径立即失效；后台路径不能替代登录和权限校验。</p>
          {draft.safe_mode_enable && draft.app_url.trim() === "" && <p className="alert warning">启用安全模式前必须配置站点网址。</p>}
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>订阅公开地址</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.force_https} onChange={(event) => updateDraft("force_https", event.target.checked)} />强制使用 HTTPS 生成公开地址</label>
          <label>订阅公开地址<textarea aria-describedby="subscribe-url-help" placeholder={"https://subscribe-a.example.com\nhttps://subscribe-b.example.com"} value={draft.subscribe_url.split(",").join("\n")} onChange={(event) => updateDraft("subscribe_url", event.target.value)} /></label>
          <p className="small muted" id="subscribe-url-help">每行一个地址，最多 32 个。外网地址必须使用 HTTPS，不能包含账号、查询参数或片段；留空时使用站点网址。</p>
        </fieldset>
        <div className="site-settings-url-grid">
          <label>货币代码<input required minLength={3} maxLength={3} pattern="[A-Za-z]{3}" value={draft.currency} onChange={(event) => updateDraft("currency", event.target.value.toUpperCase())} /></label>
          <label>货币符号<input maxLength={16} value={draft.currency_symbol} onChange={(event) => updateDraft("currency_symbol", event.target.value)} /></label>
        </div>
        <label className="switch-label"><input type="checkbox" checked={draft.stop_register} onChange={(event) => updateDraft("stop_register", event.target.checked)} />停止新用户注册</label>
        <p className="small muted">网址可留空；非空时必须是完整的 HTTP 或 HTTPS 地址。LOGO 用于显示需要品牌标识的地方。站点描述最多 500 个字符。</p>
        <fieldset className="settings-fieldset">
          <legend>流量重置策略</legend>
          <label>系统默认重置方式<select value={draft.traffic_reset_method} onChange={(event) => updateDraft("traffic_reset_method", Number(event.target.value))}>
            <option value={0}>每月 1 日</option><option value={1}>按用户到期日每月重置</option><option value={2}>永不重置</option><option value={3}>每年 1 月 1 日</option><option value={4}>按用户到期月日每年重置</option>
          </select></label>
          <p className="small muted">套餐选择“跟随系统”时使用此规则；永久有效用户不安排自动重置。计算时区与旧 Xboard 一致，固定为 Asia/Shanghai。</p>
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>优惠券系统</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.coupon_enabled} onChange={(event) => updateDraft("coupon_enabled", event.target.checked)} />启用优惠券</label>
          <p className="small muted">关闭后用户不能验证或使用优惠券，已有订单及优惠券数据保持不变。</p>
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>注册安全策略</legend>
          <label>注册试用<select aria-describedby="registration-trial-plan-help" value={draft.try_out_plan_id} onChange={(event) => updateDraft("try_out_plan_id", Number(event.target.value))}>
            <option value={0}>关闭</option>
            {plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name}</option>)}
          </select></label>
          <p className="small muted" id="registration-trial-plan-help">选择需要试用的订阅，如果没有选项请先前往订阅管理添加。</p>
          {draft.try_out_plan_id !== 0 && <label>注册试用时长<input aria-describedby="registration-trial-duration-help" type="number" required min={1} max={8760} step={1} value={draft.try_out_hour} onChange={(event) => updateDraft("try_out_hour", Number(event.target.value))} /></label>}
          {draft.try_out_plan_id !== 0 && <p className="small muted" id="registration-trial-duration-help">注册试用时长，单位为小时。</p>}
          <label className="switch-label"><input type="checkbox" checked={draft.captcha_enable} onChange={(event) => updateDraft("captcha_enable", event.target.checked)} />验证码</label>
          <p className="small muted">保护直接注册、注册邮箱验证码和找回密码验证码请求；密码登录不使用此策略。</p>
          {draft.captcha_enable && <label>验证码类型<select value={draft.captcha_type} onChange={(event) => updateDraft("captcha_type", event.target.value as SiteDraft["captcha_type"])}>
            <option value="recaptcha">Google reCAPTCHA v2</option><option value="recaptcha-v3">Google reCAPTCHA v3</option><option value="turnstile">Cloudflare Turnstile</option>
          </select></label>}
          {draft.captcha_enable && draft.captcha_type === "recaptcha" && <div className="captcha-settings-grid">
            <label>reCAPTCHA v2 站点密钥<input required value={draft.recaptcha_site_key} onChange={(event) => updateDraft("recaptcha_site_key", event.target.value)} /></label>
            <label>reCAPTCHA v2 服务端密钥<input type="password" autoComplete="off" placeholder={current.recaptcha_secret_configured ? "已配置，留空保持不变" : "请输入服务端密钥"} value={draft.recaptcha_secret ?? ""} onChange={(event) => setDraft({ ...draft, recaptcha_secret: event.target.value, clear_recaptcha_secret: false })} /></label>
            {current.recaptcha_secret_configured && <label className="switch-label"><input type="checkbox" checked={draft.clear_recaptcha_secret === true} onChange={(event) => updateDraft("clear_recaptcha_secret", event.target.checked)} />清除 reCAPTCHA v2 服务端密钥</label>}
          </div>}
          {draft.captcha_enable && draft.captcha_type === "recaptcha-v3" && <div className="captcha-settings-grid">
            <label>reCAPTCHA v3 站点密钥<input required value={draft.recaptcha_v3_site_key} onChange={(event) => updateDraft("recaptcha_v3_site_key", event.target.value)} /></label>
            <label>reCAPTCHA v3 服务端密钥<input type="password" autoComplete="off" placeholder={current.recaptcha_v3_secret_configured ? "已配置，留空保持不变" : "请输入服务端密钥"} value={draft.recaptcha_v3_secret ?? ""} onChange={(event) => setDraft({ ...draft, recaptcha_v3_secret: event.target.value, clear_recaptcha_v3_secret: false })} /></label>
            <label>reCAPTCHA v3 分数阈值<input type="number" required min={0.01} max={1} step={0.01} value={draft.recaptcha_v3_score_threshold} onChange={(event) => updateDraft("recaptcha_v3_score_threshold", Number(event.target.value))} /></label>
            {current.recaptcha_v3_secret_configured && <label className="switch-label"><input type="checkbox" checked={draft.clear_recaptcha_v3_secret === true} onChange={(event) => updateDraft("clear_recaptcha_v3_secret", event.target.checked)} />清除 reCAPTCHA v3 服务端密钥</label>}
          </div>}
          {draft.captcha_enable && draft.captcha_type === "turnstile" && <div className="captcha-settings-grid">
            <label>Turnstile 站点密钥<input required value={draft.turnstile_site_key} onChange={(event) => updateDraft("turnstile_site_key", event.target.value)} /></label>
            <label>Turnstile 服务端密钥<input type="password" autoComplete="off" placeholder={current.turnstile_secret_configured ? "已配置，留空保持不变" : "请输入服务端密钥"} value={draft.turnstile_secret ?? ""} onChange={(event) => setDraft({ ...draft, turnstile_secret: event.target.value, clear_turnstile_secret: false })} /></label>
            {current.turnstile_secret_configured && <label className="switch-label"><input type="checkbox" checked={draft.clear_turnstile_secret === true} onChange={(event) => updateDraft("clear_turnstile_secret", event.target.checked)} />清除 Turnstile 服务端密钥</label>}
          </div>}
          {draft.captcha_enable && <p className="small muted">服务端密钥加密保存且不会回显；留空保存会保留现有密钥。只有明确勾选清除后才会删除。</p>}
          <label className="switch-label"><input type="checkbox" checked={draft.email_verify} onChange={(event) => updateDraft("email_verify", event.target.checked)} />邮箱验证</label>
          <p className="small muted">启用后，新用户必须通过 6 位一次性邮箱验证码；请先在邮件设置中启用 SMTP。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.email_whitelist_enable} onChange={(event) => updateDraft("email_whitelist_enable", event.target.checked)} />邮箱后缀白名单</label>
          {draft.email_whitelist_enable && <label>邮箱后缀<textarea required aria-describedby="email-whitelist-help" value={draft.email_whitelist_suffix.join("\n")} onChange={(event) => updateDraft("email_whitelist_suffix", suffixesFromText(event.target.value))} /></label>}
          {draft.email_whitelist_enable && <p className="small muted" id="email-whitelist-help">每行一个完整域名，不支持通配符；域名匹配不区分大小写。</p>}
          <label className="switch-label"><input type="checkbox" checked={draft.email_gmail_limit_enable} onChange={(event) => updateDraft("email_gmail_limit_enable", event.target.checked)} />禁止使用Gmail多别名</label>
          <p className="small muted">启用后拒绝 Gmail 与 Googlemail 地址中包含点号或加号的别名，不影响其他邮箱域名。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.register_limit_by_ip_enable} onChange={(event) => updateDraft("register_limit_by_ip_enable", event.target.checked)} />IP注册限制</label>
          {draft.register_limit_by_ip_enable && <div className="registration-policy-grid">
            <label>注册次数<input type="number" required min={1} max={100} value={draft.register_limit_count} onChange={(event) => updateDraft("register_limit_count", Number(event.target.value))} /></label>
            <label>限制时长（分钟）<input type="number" required min={1} max={10080} value={draft.register_limit_expire} onChange={(event) => updateDraft("register_limit_expire", Number(event.target.value))} /></label>
          </div>}
          <p className="small muted">只统计成功注册；达到次数后，在滑动窗口结束前拒绝同一来源 IP 的新注册。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.invite_force} onChange={(event) => updateDraft("invite_force", event.target.checked)} />强制邀请码</label>
          <div className="registration-policy-grid">
            <label>邀请码生成上限<input type="number" required min={0} max={100} value={draft.invite_gen_limit} onChange={(event) => updateDraft("invite_gen_limit", Number(event.target.value))} /></label>
          </div>
          <label className="switch-label"><input type="checkbox" checked={draft.invite_never_expire} onChange={(event) => updateDraft("invite_never_expire", event.target.checked)} />邀请码永不过期</label>
          <p className="small muted">生成上限为 0 时禁止创建新邀请码；“永不过期”开启后，同一码可关联多个新用户。</p>
        </fieldset>
        <fieldset className="settings-fieldset">
          <legend>登录安全策略</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.password_limit_enable} onChange={(event) => updateDraft("password_limit_enable", event.target.checked)} />密码错误次数限制</label>
          {draft.password_limit_enable && <div className="registration-policy-grid">
            <label>密码错误次数<input type="number" required min={1} max={20} value={draft.password_limit_count} onChange={(event) => updateDraft("password_limit_count", Number(event.target.value))} /></label>
            <label>登录锁定时长（分钟）<input type="number" required min={1} max={1440} value={draft.password_limit_expire} onChange={(event) => updateDraft("password_limit_expire", Number(event.target.value))} /></label>
          </div>}
          <p className="small muted">达到错误次数后，从下一次登录开始锁定该邮箱；成功登录不会清空当前计数。邮箱大小写和首尾空格按同一账号统计，未知账号使用相同失败提示，并始终保留独立的来源 IP 防护。</p>
          <label className="switch-label"><input type="checkbox" checked={draft.login_with_mail_link_enable} onChange={(event) => updateDraft("login_with_mail_link_enable", event.target.checked)} />邮件链接登录</label>
          <p className="small muted">启用后，已有用户可通过 5 分钟有效、仅能使用一次的邮件链接登录；请先在邮件设置中启用 SMTP。</p>
        </fieldset>
        {saved && <div className="alert success" role="status">站点设置已保存</div>}
        <div className="form-actions">
          {error !== "" && draft !== null && <button className="button secondary" type="button" disabled={saving} onClick={() => void load()}>刷新最新设置</button>}
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存站点设置"}</button>
        </div>
      </form>
    </section>}
  </main>;
}

function toDraft(settings: SiteSettings): SiteDraft {
  return {
    app_name: settings.app_name,
    app_description: settings.app_description,
    app_url: settings.app_url,
    safe_mode_enable: settings.safe_mode_enable,
    secure_path: settings.secure_path,
    force_https: settings.force_https,
    subscribe_url: settings.subscribe_url,
    tos_url: settings.tos_url,
    logo: settings.logo,
    currency: settings.currency,
    currency_symbol: settings.currency_symbol,
    stop_register: settings.stop_register,
    email_verify: settings.email_verify,
    email_whitelist_enable: settings.email_whitelist_enable,
    email_whitelist_suffix: settings.email_whitelist_suffix,
    email_gmail_limit_enable: settings.email_gmail_limit_enable,
    register_limit_by_ip_enable: settings.register_limit_by_ip_enable,
    register_limit_count: settings.register_limit_count,
    register_limit_expire: settings.register_limit_expire,
    password_limit_enable: settings.password_limit_enable,
    password_limit_count: settings.password_limit_count,
    password_limit_expire: settings.password_limit_expire,
    invite_force: settings.invite_force,
    invite_gen_limit: settings.invite_gen_limit,
    invite_never_expire: settings.invite_never_expire,
    login_with_mail_link_enable: settings.login_with_mail_link_enable,
    try_out_plan_id: settings.try_out_plan_id,
    try_out_hour: settings.try_out_hour,
    traffic_reset_method: settings.traffic_reset_method,
    coupon_enabled: settings.coupon_enabled,
    captcha_enable: settings.captcha_enable,
    captcha_type: settings.captcha_type,
    recaptcha_site_key: settings.recaptcha_site_key,
    recaptcha_secret: "",
    clear_recaptcha_secret: false,
    recaptcha_v3_site_key: settings.recaptcha_v3_site_key,
    recaptcha_v3_score_threshold: settings.recaptcha_v3_score_threshold,
    recaptcha_v3_secret: "",
    clear_recaptcha_v3_secret: false,
    turnstile_site_key: settings.turnstile_site_key,
    turnstile_secret: "",
    clear_turnstile_secret: false
  };
}

function suffixesFromText(value: string): string[] {
  return value.split(/\r?\n/).map((suffix) => suffix.trim()).filter((suffix) => suffix !== "");
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "站点设置请求失败";
}
