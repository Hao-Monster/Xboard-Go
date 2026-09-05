import { useEffect, useState, type FormEvent } from "react";

import type { AdminAPI, NodeAgentSettings, NodeAgentSettingsInput } from "../../lib/api";

type NodeAgentSettingsAPI = Pick<AdminAPI, "getNodeAgentSettings" | "updateNodeAgentSettings">;
type Draft = Omit<NodeAgentSettingsInput, "revision" | "server_token" | "generate_server_token">;
type TokenAction = "preserve" | "replace" | "generate" | "clear";

export function NodeAgentSettingsPage({ api }: { api: NodeAgentSettingsAPI }) {
  const [current, setCurrent] = useState<NodeAgentSettings | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [tokenAction, setTokenAction] = useState<TokenAction>("preserve");
  const [manualToken, setManualToken] = useState("");
  const [issuedToken, setIssuedToken] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  const telemetry = current === null ? null : nodeAuthTelemetryOf(current);

  const apply = (settings: NodeAgentSettings, oneTimeToken = "") => {
    const safeSettings = withoutIssuedToken(settings);
    setCurrent(safeSettings);
    setDraft(toDraft(safeSettings));
    setTokenAction("preserve");
    setManualToken("");
    setIssuedToken(oneTimeToken);
  };

  const load = async () => {
    setLoading(true);
    setError("");
    setSaved(false);
    try {
      apply(await api.getNodeAgentSettings());
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let active = true;
    void api.getNodeAgentSettings().then((settings) => {
      if (active) apply(settings);
    }).catch((cause: unknown) => {
      if (active) setError(messageOf(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [api]);

  const updateDraft = <K extends keyof Draft,>(field: K, value: Draft[K]) => {
    if (draft === null) return;
    setDraft({ ...draft, [field]: value });
    setSaved(false);
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null) return;
    setSaving(true);
    setSaved(false);
    setError("");
    setIssuedToken("");
    const input: NodeAgentSettingsInput = { revision: current.revision, ...draft };
    if (tokenAction === "replace") input.server_token = manualToken;
    if (tokenAction === "generate") input.generate_server_token = true;
    if (tokenAction === "clear") input.server_token = "";
    try {
      const updated = await api.updateNodeAgentSettings(input);
      apply(updated, updated.issued_token ?? "");
      setSaved(true);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setSaving(false);
    }
  };

  const copyIssuedToken = async () => {
    try {
      await navigator.clipboard.writeText(issuedToken);
    } catch {
      setError("浏览器未允许复制，请立即手动保存通讯密钥");
    }
  };

  return <main className="page-shell node-agent-settings-page">
    <header className="page-header"><div><p className="eyebrow">Node compatibility</p><h1>节点配置</h1><p className="muted">管理旧单节点通讯兼容设置。新部署优先使用服务器机器凭据。</p></div></header>
    {loading && draft === null && <div className="empty-card" aria-live="polite">正在加载节点配置…</div>}
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {draft === null && !loading && <button className="button secondary" type="button" onClick={() => void load()}>重新加载节点配置</button>}
    {draft !== null && current !== null && <section className="site-settings-card" aria-labelledby="node-agent-settings-heading">
      <div className="section-heading"><div><h2 id="node-agent-settings-heading">节点通讯设置</h2><p className="muted">字段和旧 Xboard 节点配置保持同一业务含义。</p></div><span className="count-pill">Revision {current.revision}</span></div>
      <form className="form-stack node-agent-settings-form" onSubmit={(event) => void save(event)}>
        <fieldset className="settings-fieldset">
          <legend>通讯密钥</legend>
          <p className="small muted">{current.server_token_configured ? `已配置（前缀 ${current.server_token_prefix}…）` : "尚未配置"}。密钥仅在替换或生成成功后显示一次。</p>
          <label>密钥操作<select aria-label="通讯密钥操作" value={tokenAction} onChange={(event) => { setTokenAction(event.target.value as TokenAction); setManualToken(""); setSaved(false); }}>
            <option value="preserve">保持现有密钥</option><option value="replace">手动替换密钥</option><option value="generate">随机生成新密钥</option>
            {current.server_token_configured && <option value="clear">清除通讯密钥</option>}
          </select></label>
          {tokenAction === "replace" && <label>新通讯密钥<input type="password" autoComplete="new-password" required minLength={16} maxLength={256} value={manualToken} onChange={(event) => setManualToken(event.target.value)} /></label>}
          {tokenAction === "generate" && <p className="alert warning">保存后会生成 48 字符随机密钥，并立即断开所有旧单节点连接。</p>}
          {tokenAction === "clear" && <p className="alert warning">保存后旧单节点 HTTP 和 WebSocket 将立即失效；机器凭据不受影响。</p>}
        </fieldset>
        <div className="node-agent-settings-grid">
          <label>拉取间隔（秒）<input type="number" required min={1} max={3600} value={draft.server_pull_interval} onChange={(event) => updateDraft("server_pull_interval", Number(event.target.value))} /></label>
          <label>推送间隔（秒）<input type="number" required min={1} max={3600} value={draft.server_push_interval} onChange={(event) => updateDraft("server_push_interval", Number(event.target.value))} /></label>
        </div>
        <fieldset className="settings-fieldset">
          <legend>WebSocket</legend>
          <label className="switch-label"><input type="checkbox" checked={draft.server_ws_enable} disabled={!current.websocket_available && !draft.server_ws_enable} onChange={(event) => updateDraft("server_ws_enable", event.target.checked)} />启用节点 WebSocket</label>
          {!current.websocket_available && <p className="small muted">当前部署没有启用 WebSocket 服务能力，管理设置不能绕过部署约束。</p>}
          <label>WebSocket 地址<input type="text" inputMode="url" maxLength={2048} placeholder="wss://panel.example.com/ws（留空自动生成）" value={draft.server_ws_url} onChange={(event) => updateDraft("server_ws_url", event.target.value)} /></label>
        </fieldset>
        {telemetry !== null && <section className="settings-telemetry" aria-label="节点鉴权迁移遥测">
          <h3>节点鉴权迁移遥测</h3>
          <p className="small muted">自 {formatTime(telemetry.observed_since)} 起持久化聚合，仅用于评估 V1 全局令牌退役窗口。</p>
          <h4>V1 全局令牌</h4>
          <dl className="metrics"><div><dt>HTTP 认证</dt><dd>{telemetry.legacy_global_token.http_auth_success_count}</dd></div><div><dt>WebSocket 认证</dt><dd>{telemetry.legacy_global_token.websocket_auth_success_count}</dd></div><div><dt>最后使用</dt><dd>{formatTime(telemetry.legacy_global_token.last_used_at)}</dd></div></dl>
          <h4>V2 机器凭据</h4>
          <dl className="metrics"><div><dt>HTTP 认证</dt><dd>{telemetry.machine_credential.http_auth_success_count}</dd></div><div><dt>WebSocket 认证</dt><dd>{telemetry.machine_credential.websocket_auth_success_count}</dd></div><div><dt>最后使用</dt><dd>{formatTime(telemetry.machine_credential.last_used_at)}</dd></div></dl>
        </section>}
        {issuedToken !== "" && <div className="alert warning one-time-token" role="status"><strong>请立即保存通讯密钥，关闭后无法再次查看</strong><code>{issuedToken}</code><div className="row-actions"><button className="button secondary compact" type="button" onClick={() => void copyIssuedToken()}>复制通讯密钥</button><button className="button ghost compact" type="button" onClick={() => setIssuedToken("")}>我已保存</button></div></div>}
        {saved && issuedToken === "" && <div className="alert success" role="status">节点配置已保存</div>}
        <div className="form-actions">
          {error !== "" && <button className="button secondary" type="button" disabled={saving} onClick={() => void load()}>刷新最新设置</button>}
          <button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存节点配置"}</button>
        </div>
      </form>
    </section>}
  </main>;
}

function toDraft(settings: NodeAgentSettings): Draft {
  return {
    server_pull_interval: settings.server_pull_interval,
    server_push_interval: settings.server_push_interval,
    device_limit_mode: settings.device_limit_mode,
    server_ws_enable: settings.server_ws_enable,
    server_ws_url: settings.server_ws_url
  };
}

function withoutIssuedToken(settings: NodeAgentSettings): NodeAgentSettings {
  const safeSettings = { ...settings };
  delete safeSettings.issued_token;
  return safeSettings;
}

function nodeAuthTelemetryOf(settings: NodeAgentSettings): NonNullable<NodeAgentSettings["node_auth_telemetry"]> {
  return settings.node_auth_telemetry ?? {
    observed_since: settings.updated_at,
    legacy_global_token: {
      http_auth_success_count: settings.legacy_http_auth_success_count,
      websocket_auth_success_count: settings.legacy_websocket_auth_success_count,
      last_used_at: settings.legacy_last_used_at
    },
    machine_credential: { http_auth_success_count: 0, websocket_auth_success_count: 0, last_used_at: null }
  };
}

function formatTime(value: string | null): string {
  return value === null ? "尚无记录" : new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "medium" }).format(new Date(value));
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "节点配置请求失败";
}
