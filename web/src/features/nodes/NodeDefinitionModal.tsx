import { useEffect, useMemo, useState, type ChangeEvent, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type {
  AdminNode, AdminNodeDefinition, AdminNodeDefinitionInput, Machine, RoutingRule, ServerGroup
} from "../../lib/api";

export interface NodeDefinitionAPI {
  getAdminNodeDefinition: (nodeID: number) => Promise<AdminNodeDefinition>;
  createAdminNodeDefinition: (input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
  replaceAdminNodeDefinition: (nodeID: number, input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
}

interface Props {
  api: NodeDefinitionAPI;
  node: AdminNode | null;
  nodes: AdminNode[];
  machines: Machine[];
  groups: ServerGroup[];
  routes: RoutingRule[];
  onClose: () => void;
  onSaved: () => void;
}

const protocols = [
  ["shadowsocks", "Shadowsocks"], ["vmess", "VMess"], ["trojan", "Trojan"], ["hysteria", "Hysteria"],
  ["vless", "VLess"], ["tuic", "TUIC"], ["socks", "SOCKS"], ["naive", "Naive"], ["http", "HTTP"],
  ["mieru", "Mieru"], ["anytls", "AnyTLS"]
] as const;

const networks = [
  ["tcp", "TCP"], ["ws", "Websocket"], ["grpc", "gRPC"], ["h2", "HTTP/2"],
  ["httpupgrade", "HttpUpgrade"], ["xhttp", "XHTTP"]
] as const;

const networkTemplates: Record<string, Array<{ label: string; value: Record<string, unknown> }>> = {
  tcp: [
    { label: "TCP", value: { acceptProxyProtocol: false, header: { type: "none" } } },
    { label: "TCP + HTTP", value: { acceptProxyProtocol: false, header: { type: "http", request: { version: "1.1", method: "GET", path: ["/"], headers: { Host: ["www.example.com"] } }, response: { version: "1.1", status: "200", reason: "OK" } } } }
  ],
  grpc: [{ label: "gRPC", value: { serviceName: "GunService" } }],
  ws: [{ label: "WebSocket", value: { path: "/", headers: { Host: "v2ray.com" } } }],
  h2: [{ label: "HTTP/2", value: { path: "/", host: ["www.google.com"] } }],
  httpupgrade: [{ label: "HttpUpgrade", value: { acceptProxyProtocol: false, path: "/", host: "xray.com", headers: { key: "value" } } }],
  xhttp: [{ label: "XHTTP", value: {
    host: "example.com", path: "/yourpath", mode: "auto", extra: {
      headers: {}, xPaddingBytes: "100-1000", noGRPCHeader: false, noSSEHeader: false,
      scMaxEachPostBytes: 1_000_000, scMinPostsIntervalMs: 30, scMaxBufferedPosts: 30,
      xmux: { maxConcurrency: "16-32", maxConnections: 0, cMaxReuseTimes: "64-128", cMaxLifetimeMs: 0, hMaxRequestTimes: "800-900", hKeepAlivePeriod: 0 },
      downloadSettings: { address: "", port: 443, network: "xhttp", security: "tls", tlsSettings: {}, xhttpSettings: { path: "/yourpath" }, sockopt: {} }
    }
  } }]
};

export function NodeDefinitionModal({ api, node, nodes, machines, groups, routes, onClose, onSaved }: Props) {
  const [input, setInput] = useState<AdminNodeDefinitionInput>(() => newNodeInput());
  const [loading, setLoading] = useState(node !== null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [tagsText, setTagsText] = useState("");
  const [transferGiB, setTransferGiB] = useState("0");
  const [networkSettingsText, setNetworkSettingsText] = useState("{}");
  const [customOutboundsText, setCustomOutboundsText] = useState("[]");
  const [customRoutesText, setCustomRoutesText] = useState("[]");
  const [certificateText, setCertificateText] = useState('{"cert_mode":"none"}');
  const title = node === null ? "新建节点" : "编辑节点";

  useEffect(() => {
    if (node === null) return;
    let live = true;
    void api.getAdminNodeDefinition(node.id).then((detail) => {
      if (!live) return;
      const next = definitionInput(detail);
      setInput(next);
      setTagsText(next.tags.join(", "));
      setTransferGiB(String(next.transfer_enable / 1024 ** 3));
      setNetworkSettingsText(formatJSON(asRecord(next.protocol_settings.network_settings)));
      setCustomOutboundsText(formatJSON(next.custom_outbounds));
      setCustomRoutesText(formatJSON(next.custom_routes));
      setCertificateText(formatJSON(next.certificate_config));
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, node]);

  const parentOptions = useMemo(
    () => nodes.filter((candidate) => candidate.type === input.type && candidate.id !== node?.id),
    [input.type, node?.id, nodes]
  );

  const changeProtocol = (type: string) => {
    setInput((current) => ({ ...current, type, parent_id: null, protocol_settings: defaultProtocolSettings(type) }));
    setNetworkSettingsText("{}");
  };
  const changeMultiple = (field: "group_ids" | "route_ids", event: ChangeEvent<HTMLSelectElement>) => {
    const values = Array.from(event.currentTarget.selectedOptions, (option) => Number(option.value));
    setInput((current) => ({ ...current, [field]: values }));
  };
  const updateCertificate = (certificate: Record<string, unknown>) => {
    setInput((current) => ({ ...current, certificate_config: certificate }));
    setCertificateText(formatJSON(certificate));
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    try {
      const networkSettings = parseJSONObject(networkSettingsText, "传输协议设置");
      const customOutbounds = parseJSONArray(customOutboundsText, "自定义出站");
      const customRoutes = parseJSONArray(customRoutesText, "自定义路由");
      const certificate = parseJSONObject(certificateText, "证书配置");
      const parsedTransfer = Number(transferGiB);
      if (!Number.isFinite(parsedTransfer) || parsedTransfer < 0 || !Number.isInteger(parsedTransfer * 1024 ** 3)) {
        throw new Error("流量限制必须是有效的非负 GiB 数值");
      }
      const payload: AdminNodeDefinitionInput = {
        ...input,
        external_code: emptyToNull(input.external_code),
        tags: splitTags(tagsText),
        transfer_enable: parsedTransfer * 1024 ** 3,
        protocol_settings: { ...input.protocol_settings, ...(supportsNetwork(input.type) ? { network_settings: networkSettings } : {}) },
        custom_outbounds: customOutbounds,
        custom_routes: customRoutes,
        certificate_config: certificate
      };
      if (node === null) await api.createAdminNodeDefinition(payload);
      else await api.replaceAdminNodeDefinition(node.id, payload);
      onSaved();
    } catch (cause) {
      setError(errorMessage(cause));
      setSaving(false);
    }
  };

  return <Modal title={title} className="node-definition-modal" onClose={onClose}>
    <div className="modal-header"><div><p className="eyebrow">节点协议</p><h2>{title}</h2></div><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>
    {loading ? <div className="empty-card" aria-live="polite">正在加载节点定义…</div> : <form className="form-stack node-definition-form" onSubmit={(event) => void submit(event)}>
      <fieldset><legend>基础设置</legend><div className="node-form-grid">
        <label>协议类型<select aria-label="协议类型" value={input.type} disabled={node !== null} onChange={(event) => changeProtocol(event.target.value)}>{protocols.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <label>节点名称<input required maxLength={255} value={input.name} onChange={(event) => setInput({ ...input, name: event.target.value })} /></label>
        <label>基础倍率<input required type="number" min="0.000001" max="1000" step="any" value={input.rate} onChange={(event) => setInput({ ...input, rate: Number(event.target.value) })} /></label>
        <label>流量限制 (GiB)<input required type="number" min="0" step="0.01" value={transferGiB} onChange={(event) => setTransferGiB(event.target.value)} /></label>
        <label>自定义 ID<input maxLength={255} value={input.external_code ?? ""} onChange={(event) => setInput({ ...input, external_code: event.target.value })} /></label>
        <label>标签<input maxLength={4096} placeholder="以逗号分隔" value={tagsText} onChange={(event) => setTagsText(event.target.value)} /></label>
        <label>节点地址<input required maxLength={255} value={input.host} onChange={(event) => setInput({ ...input, host: event.target.value })} /></label>
        <label>连接端口<input required inputMode="numeric" pattern="[0-9]{1,5}(-[0-9]{1,5})?" value={input.port} onChange={(event) => setInput({ ...input, port: event.target.value })} /></label>
        <label>服务端口<input required type="number" min="1" max="65535" value={input.server_port} onChange={(event) => setInput({ ...input, server_port: Number(event.target.value) })} /></label>
        <label>监听地址<input required maxLength={45} placeholder="0.0.0.0 或 ::" value={input.listen_address} onChange={(event) => setInput({ ...input, listen_address: event.target.value })} /></label>
        <label>父节点<select value={input.parent_id ?? ""} onChange={(event) => setInput({ ...input, parent_id: event.target.value === "" ? null : Number(event.target.value) })}><option value="">无父节点</option>{parentOptions.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name}</option>)}</select></label>
        <label>绑定服务器<select value={input.machine_id ?? ""} onChange={(event) => setInput({ ...input, machine_id: event.target.value === "" ? null : Number(event.target.value) })}><option value="">独立部署</option>{machines.map((machine) => <option key={machine.id} value={machine.id}>{machine.name}</option>)}</select></label>
        <label>排序<input required type="number" min="0" max="1000000000" value={input.sort} onChange={(event) => setInput({ ...input, sort: Number(event.target.value) })} /></label>
        <label>权限组<select multiple aria-label="权限组" value={input.group_ids.map(String)} onChange={(event) => changeMultiple("group_ids", event)}>{groups.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}</select></label>
        <label>路由规则<select multiple aria-label="路由规则" value={input.route_ids.map(String)} onChange={(event) => changeMultiple("route_ids", event)}>{routes.map((route) => <option key={route.id} value={route.id}>{route.remarks}</option>)}</select></label>
      </div><div className="switch-row">
        <label className="switch-label"><input type="checkbox" checked={input.show} onChange={(event) => setInput({ ...input, show: event.target.checked })} />用户端显示</label>
        <label className="switch-label"><input type="checkbox" checked={input.enabled} onChange={(event) => setInput({ ...input, enabled: event.target.checked })} />启用运行</label>
      </div></fieldset>

      <fieldset><legend>{protocolLabel(input.type)} 协议设置</legend>
        <ProtocolFields input={input} setInput={setInput} />
        {supportsNetwork(input.type) && <div className="form-stack network-settings-editor">
          <label>传输协议设置 (JSON)<textarea rows={7} value={networkSettingsText} onChange={(event) => setNetworkSettingsText(event.target.value)} /></label>
          <div className="inline-actions" aria-label="传输协议模板">{(networkTemplates[stringValue(input.protocol_settings.network, "tcp")] ?? []).map((template) =>
            <button className="button compact secondary" type="button" key={template.label} onClick={() => setNetworkSettingsText(formatJSON(template.value))}>套用 {template.label} 模板</button>
          )}</div>
        </div>}
      </fieldset>

      <fieldset><legend>动态倍率</legend>
        <label className="switch-label"><input type="checkbox" checked={input.rate_time_enabled} onChange={(event) => setInput({ ...input, rate_time_enabled: event.target.checked })} />启用动态倍率</label>
        {input.rate_time_enabled && <div className="rate-range-list">{input.rate_time_ranges.map((range, index) => <div className="rate-range-row" key={`${index}-${range.start}`}>
          <label>开始<input aria-label={`动态倍率 ${index + 1} 开始`} type="time" value={range.start} onChange={(event) => updateRateRange(input, setInput, index, "start", event.target.value)} /></label>
          <label>结束<input aria-label={`动态倍率 ${index + 1} 结束`} type="time" value={range.end} onChange={(event) => updateRateRange(input, setInput, index, "end", event.target.value)} /></label>
          <label>倍率<input aria-label={`动态倍率 ${index + 1} 倍率`} type="number" min="0" max="1000" step="0.01" value={range.rate} onChange={(event) => updateRateRange(input, setInput, index, "rate", Number(event.target.value))} /></label>
          <button className="button compact ghost danger-text" type="button" onClick={() => setInput({ ...input, rate_time_ranges: input.rate_time_ranges.filter((_, position) => position !== index) })}>移除</button>
        </div>)}<button className="button compact secondary" type="button" onClick={() => setInput({ ...input, rate_time_ranges: [...input.rate_time_ranges, { start: "00:00", end: "23:59", rate: 1 }] })}>添加时间段</button></div>}
      </fieldset>

      <details className="node-advanced-settings"><summary>高级设置</summary><div className="form-stack">
        <fieldset><legend>证书配置</legend><CertificateFields key={formatDNSEnv(asRecord(asRecord(input.certificate_config).dns_env))} value={asRecord(input.certificate_config)} onChange={updateCertificate} /></fieldset>
        <label>自定义出站 (JSON 数组)<textarea rows={7} value={customOutboundsText} onChange={(event) => setCustomOutboundsText(event.target.value)} /></label>
        <label>自定义路由 (JSON 数组)<textarea rows={7} value={customRoutesText} onChange={(event) => setCustomRoutesText(event.target.value)} /></label>
        <details><summary>证书配置 JSON（专家）</summary><label>证书配置 (JSON 对象)<textarea rows={9} value={certificateText} onChange={(event) => setCertificateText(event.target.value)} onBlur={() => {
          try { updateCertificate(parseJSONObject(certificateText, "证书配置")); setError(""); }
          catch (cause) { setError(errorMessage(cause)); }
        }} /></label></details>
      </div></details>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" disabled={saving} onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "提交"}</button></div>
    </form>}
  </Modal>;
}

function CertificateFields({ value, onChange }: { value: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void }) {
  const mode = stringValue(value.cert_mode, "none");
  const set = (field: string, fieldValue: unknown) => onChange({ ...value, [field]: fieldValue });
  const [dnsEnvText, setDNSEnvText] = useState(() => formatDNSEnv(asRecord(value.dns_env)));
  return <div className="node-form-grid certificate-fields">
    <label>证书模式<select value={mode} onChange={(event) => set("cert_mode", event.target.value)}>
      <option value="none">none</option><option value="http">http-01 (ACME)</option><option value="dns">dns-01 (ACME)</option>
      <option value="self">self-signed</option><option value="content">content (Cert Push)</option>
    </select></label>
    {mode !== "none" && <label>证书域名<input maxLength={4096} value={stringValue(value.domain)} onChange={(event) => set("domain", event.target.value)} placeholder="example.com" /></label>}
    {["http", "dns"].includes(mode) && <label>ACME 邮箱<input type="email" maxLength={4096} value={stringValue(value.email)} onChange={(event) => set("email", event.target.value)} placeholder="admin@example.com" /></label>}
    {mode === "http" && <label>HTTP 挑战端口<input type="number" min="1" max="65535" value={Number(value.http_port ?? 80)} onChange={(event) => set("http_port", Number(event.target.value))} /></label>}
    {mode === "dns" && <>
      <label>DNS Provider<input maxLength={4096} value={stringValue(value.dns_provider)} onChange={(event) => set("dns_provider", event.target.value)} placeholder="cloudflare / alidns / dnspod" /></label>
      <label className="full-field">DNS 环境变量<textarea rows={5} spellCheck={false} value={dnsEnvText} onChange={(event) => setDNSEnvText(event.target.value)} onBlur={() => set("dns_env", parseDNSEnv(dnsEnvText))} placeholder={"CF_API_TOKEN=xxxxxx\nALIDNS_ACCESS_KEY_ID=xxxx"} /></label>
    </>}
    {mode === "content" && <>
      <label className="full-field">证书内容<textarea rows={7} spellCheck={false} value={stringValue(value.cert_content)} onChange={(event) => set("cert_content", event.target.value)} placeholder={"-----BEGIN CERTIFICATE-----\n..."} /></label>
      <label className="full-field">私钥内容<textarea rows={7} spellCheck={false} value={stringValue(value.key_content)} onChange={(event) => set("key_content", event.target.value)} placeholder={"-----BEGIN PRIVATE KEY-----\n..."} /></label>
    </>}
  </div>;
}

function ProtocolFields({ input, setInput }: { input: AdminNodeDefinitionInput; setInput: (input: AdminNodeDefinitionInput) => void }) {
  const settings = input.protocol_settings;
  const set = (key: string, value: unknown) => setInput({ ...input, protocol_settings: { ...settings, [key]: value } });
  const setNested = (key: string, field: string, value: unknown) => set(key, { ...asRecord(settings[key]), [field]: value });
  const tlsKey = ["hysteria", "tuic", "anytls"].includes(input.type) ? "tls" : "tls_settings";
  const tls = asRecord(settings[tlsKey]);
  const securityOptions = input.type === "vmess" ? [[0, "None"], [1, "TLS"]] : input.type === "trojan" ? [[1, "TLS"], [2, "Reality"]] : [[0, "None"], [1, "TLS"], [2, "Reality"]];

  return <div className="node-form-grid protocol-fields">
    {input.type === "shadowsocks" && <>
      <label>加密算法<input list="shadowsocks-ciphers" value={stringValue(settings.cipher)} onChange={(event) => set("cipher", event.target.value)} /><datalist id="shadowsocks-ciphers">{["aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"].map((cipher) => <option key={cipher} value={cipher} />)}</datalist></label>
      <label>插件<select value={stringValue(settings.plugin)} onChange={(event) => set("plugin", event.target.value)}><option value="">None</option><option value="obfs">Simple Obfs</option><option value="v2ray-plugin">V2Ray Plugin</option><option value="gost-plugin">Gost Plugin</option><option value="shadow-tls">Shadow TLS</option><option value="restls">ResTLS</option><option value="kcptun">KCPTun</option></select></label>
      <label className="full-field">插件参数<input maxLength={4096} value={stringValue(settings.plugin_opts)} onChange={(event) => set("plugin_opts", event.target.value)} /></label>
    </>}
    {["vmess", "trojan", "vless"].includes(input.type) && <>
      <label>安全性<select value={Number(settings.tls ?? 0)} onChange={(event) => set("tls", Number(event.target.value))}>{securityOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label>传输协议<select value={stringValue(settings.network, "tcp")} onChange={(event) => set("network", event.target.value)}>{[...networks, ...(input.type === "vless" ? [["kcp", "mKCP"]] as const : [])].map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      {input.type === "vless" && <label>Flow<select value={stringValue(settings.flow)} onChange={(event) => set("flow", event.target.value)}><option value="">None</option><option value="xtls-rprx-direct">xtls-rprx-direct</option><option value="xtls-rprx-splice">xtls-rprx-splice</option><option value="xtls-rprx-vision">xtls-rprx-vision</option></select></label>}
      {Number(settings.tls) === 1 && <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />}
      {Number(settings.tls) > 0 && <><label className="switch-label"><input type="checkbox" checked={Boolean(asRecord(settings.utls).enabled)} onChange={(event) => set("utls", { ...asRecord(settings.utls), enabled: event.target.checked })} />uTLS</label>
      {Boolean(asRecord(settings.utls).enabled) && <label>uTLS 指纹<select value={stringValue(asRecord(settings.utls).fingerprint, "chrome")} onChange={(event) => set("utls", { ...asRecord(settings.utls), fingerprint: event.target.value })}>{[
        "chrome", "firefox", "safari", "ios", "edge", "random"
      ].map((fingerprint) => <option key={fingerprint} value={fingerprint}>{fingerprint}</option>)}</select></label>}</>}
      <MultiplexFields settings={settings} set={set} />
      {Number(settings.tls) === 2 && <RealityFields settings={settings} set={set} />}
      {input.type === "vless" && <>
        <label className="switch-label"><input type="checkbox" checked={Boolean(asRecord(settings.encryption).enabled)} onChange={(event) => set("encryption", { ...asRecord(settings.encryption), enabled: event.target.checked })} />VLESS Encryption</label>
        <label>客户端公钥<input maxLength={8192} value={stringValue(asRecord(settings.encryption).encryption)} onChange={(event) => set("encryption", { ...asRecord(settings.encryption), encryption: event.target.value })} /></label>
        <label>服务端私钥<input maxLength={8192} value={stringValue(asRecord(settings.encryption).decryption)} onChange={(event) => set("encryption", { ...asRecord(settings.encryption), decryption: event.target.value })} /></label>
      </>}
    </>}
    {input.type === "hysteria" && <>
      <label>版本<select value={Number(settings.version ?? 2)} onChange={(event) => set("version", Number(event.target.value))}><option value={1}>V1</option><option value={2}>V2</option></select></label>
      {Number(settings.version ?? 2) === 1 && <label>ALPN<select value={stringValue(settings.alpn, "h2")} onChange={(event) => set("alpn", event.target.value)}>{["hysteria", "http/1.1", "h2", "h3"].map((value) => <option key={value} value={value}>{value}</option>)}</select></label>}
      <label className="switch-label"><input type="checkbox" checked={Boolean(asRecord(settings.obfs).open)} onChange={(event) => set("obfs", { ...asRecord(settings.obfs), open: event.target.checked })} />混淆</label>
      <label>混淆密码<input maxLength={4096} value={stringValue(asRecord(settings.obfs).password)} onChange={(event) => set("obfs", { ...asRecord(settings.obfs), password: event.target.value })} /></label>
      <label>上行带宽 (Mbps)<input type="number" min="0" max="1000000" value={Number(asRecord(settings.bandwidth).up ?? 0)} onChange={(event) => set("bandwidth", { ...asRecord(settings.bandwidth), up: Number(event.target.value) })} /></label>
      <label>下行带宽 (Mbps)<input type="number" min="0" max="1000000" value={Number(asRecord(settings.bandwidth).down ?? 0)} onChange={(event) => set("bandwidth", { ...asRecord(settings.bandwidth), down: Number(event.target.value) })} /></label>
      <label>端口跳跃间隔 (秒)<input type="number" min="1" max="86400" placeholder="例如: 30" value={settings.hop_interval == null ? "" : Number(settings.hop_interval)} onChange={(event) => set("hop_interval", event.target.value === "" ? undefined : Number(event.target.value))} /></label>
      <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
    </>}
    {input.type === "tuic" && <>
      <label>版本<select value={Number(settings.version ?? 5)} onChange={(event) => set("version", Number(event.target.value))}><option value={5}>V5</option><option value={4}>V4</option></select></label>
      <label>拥塞控制<select value={stringValue(settings.congestion_control, "bbr")} onChange={(event) => set("congestion_control", event.target.value)}><option value="bbr">BBR</option><option value="cubic">CUBIC</option><option value="new_reno">NEW_RENO</option></select></label>
      <label>ALPN<select multiple aria-label="ALPN" value={asStringArray(settings.alpn)} onChange={(event) => set("alpn", Array.from(event.currentTarget.selectedOptions, (option) => option.value))}><option value="h3">h3</option><option value="h2">HTTP/2</option><option value="http/1.1">HTTP/1.1</option></select></label>
      <label>UDP Relay<select value={stringValue(settings.udp_relay_mode, "native")} onChange={(event) => set("udp_relay_mode", event.target.value)}><option value="native">Native</option><option value="quic">QUIC</option></select></label>
      <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
    </>}
    {["socks", "naive", "http"].includes(input.type) && <>
      <label>TLS<select value={Number(settings.tls ?? 0)} onChange={(event) => set("tls", Number(event.target.value))}><option value={0}>不支持</option><option value={1}>支持</option></select></label>
      <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
    </>}
    {input.type === "mieru" && <>
      <label>传输协议<select value={stringValue(settings.transport, "TCP")} onChange={(event) => set("transport", event.target.value)}><option value="TCP">TCP</option><option value="UDP">UDP</option></select></label>
      <label>Traffic Pattern<input maxLength={4096} value={stringValue(settings.traffic_pattern)} onChange={(event) => set("traffic_pattern", event.target.value)} /></label>
      <MultiplexFields settings={settings} set={set} />
    </>}
    {input.type === "anytls" && <>
      <label>ALPN<input maxLength={64} value={stringValue(settings.alpn)} onChange={(event) => set("alpn", event.target.value)} /></label>
      <label className="full-field">Padding Scheme<textarea rows={6} value={asStringArray(settings.padding_scheme).join("\n")} onChange={(event) => set("padding_scheme", event.target.value.split(/\r?\n/).filter(Boolean))} /><button className="button compact secondary" type="button" onClick={() => set("padding_scheme", defaultAnyTLSPaddingScheme)}>使用默认方案</button></label>
      <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
    </>}
  </div>;
}

function TLSFields({ tls, setTLS }: { tls: Record<string, unknown>; setTLS: (field: string, value: unknown) => void }) {
  const ech = asRecord(tls.ech);
  return <>
    <label>SNI<input maxLength={255} value={stringValue(tls.server_name)} onChange={(event) => setTLS("server_name", event.target.value)} /></label>
    <label className="switch-label"><input type="checkbox" checked={Boolean(tls.allow_insecure)} onChange={(event) => setTLS("allow_insecure", event.target.checked)} />允许不安全连接</label>
    <label className="switch-label"><input type="checkbox" checked={Boolean(ech.enabled)} onChange={(event) => setTLS("ech", { ...ech, enabled: event.target.checked })} />ECH</label>
    {Boolean(ech.enabled) && <><label>ECH Config<textarea required rows={4} value={stringValue(ech.config)} onChange={(event) => setTLS("ech", { ...ech, config: event.target.value })} /></label><label>ECH Key<textarea required rows={3} value={stringValue(ech.key)} onChange={(event) => setTLS("ech", { ...ech, key: event.target.value })} /></label><label>ECH Query Server Name<input maxLength={255} value={stringValue(ech.query_server_name)} onChange={(event) => setTLS("ech", { ...ech, query_server_name: event.target.value })} /></label></>}
  </>;
}

function RealityFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const reality = asRecord(settings.reality_settings);
  const update = (field: string, value: unknown) => set("reality_settings", { ...reality, [field]: value });
  return <>
    <label>Reality SNI<input required maxLength={255} value={stringValue(reality.server_name)} onChange={(event) => update("server_name", event.target.value)} /></label>
    <label>Reality 端口<input type="number" min="1" max="65535" value={Number(reality.server_port ?? 443)} onChange={(event) => update("server_port", Number(event.target.value))} /></label>
    <label>Reality 公钥<input required maxLength={4096} value={stringValue(reality.public_key)} onChange={(event) => update("public_key", event.target.value)} /></label>
    <label>Reality 私钥<input required maxLength={4096} value={stringValue(reality.private_key)} onChange={(event) => update("private_key", event.target.value)} /></label>
    <label>Reality Short ID<input maxLength={64} value={stringValue(reality.short_id)} onChange={(event) => update("short_id", event.target.value)} /></label>
    <label className="switch-label"><input type="checkbox" checked={Boolean(reality.allow_insecure)} onChange={(event) => update("allow_insecure", event.target.checked)} />Reality 允许不安全连接</label>
  </>;
}

function MultiplexFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const multiplex = asRecord(settings.multiplex);
  const update = (field: string, value: unknown) => set("multiplex", { ...multiplex, [field]: value });
  return <>
    <label className="switch-label"><input type="checkbox" checked={Boolean(multiplex.enabled)} onChange={(event) => update("enabled", event.target.checked)} />多路复用</label>
    {Boolean(multiplex.enabled) && <>
      <label>复用协议<select value={stringValue(multiplex.protocol, "smux")} onChange={(event) => update("protocol", event.target.value)}><option value="smux">smux</option><option value="yamux">yamux</option><option value="h2mux">h2mux</option></select></label>
      <label>最大连接数<input type="number" min="1" max="65535" value={Number(multiplex.max_connections ?? 4)} onChange={(event) => update("max_connections", Number(event.target.value))} /></label>
      <label className="switch-label"><input type="checkbox" checked={Boolean(multiplex.padding)} onChange={(event) => update("padding", event.target.checked)} />复用填充</label>
      <label className="switch-label"><input type="checkbox" checked={Boolean(asRecord(multiplex.brutal).enabled)} onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), enabled: event.target.checked })} />Brutal 加速</label>
      {Boolean(asRecord(multiplex.brutal).enabled) && <><label>Brutal 上行 (Mbps)<input type="number" min="1" max="1000000" value={Number(asRecord(multiplex.brutal).up_mbps ?? 100)} onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), up_mbps: Number(event.target.value) })} /></label><label>Brutal 下行 (Mbps)<input type="number" min="1" max="1000000" value={Number(asRecord(multiplex.brutal).down_mbps ?? 100)} onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), down_mbps: Number(event.target.value) })} /></label></>}
    </>}
  </>;
}

function newNodeInput(): AdminNodeDefinitionInput {
  return {
    type: "shadowsocks", external_code: null, parent_id: null, name: "", rate: 1, tags: [], host: "",
    port: "443", server_port: 443, listen_address: "0.0.0.0", protocol_settings: defaultProtocolSettings("shadowsocks"),
    show: false, enabled: true, sort: 0, machine_id: null, group_ids: [], route_ids: [], rate_time_enabled: false,
    rate_time_ranges: [], custom_outbounds: [], custom_routes: [], certificate_config: { cert_mode: "none" }, transfer_enable: 0
  };
}

function definitionInput(detail: AdminNodeDefinition): AdminNodeDefinitionInput {
  return {
    revision: detail.revision, type: detail.type, external_code: detail.external_code || null, parent_id: detail.parent_id,
    name: detail.name, rate: detail.rate, tags: detail.tags, host: detail.host, port: detail.port,
    server_port: detail.server_port, listen_address: detail.listen_address, protocol_settings: detail.protocol_settings,
    show: detail.show, enabled: detail.enabled, sort: detail.sort, machine_id: detail.machine_id,
    group_ids: detail.group_ids, route_ids: detail.route_ids, rate_time_enabled: detail.rate_time_enabled,
    rate_time_ranges: detail.rate_time_ranges, custom_outbounds: detail.custom_outbounds, custom_routes: detail.custom_routes,
    certificate_config: detail.certificate_config, transfer_enable: detail.transfer_enable
  };
}

export function defaultProtocolSettings(type: string): Record<string, unknown> {
  const tls = () => ({ server_name: "", allow_insecure: false, ech: { enabled: false, config: "", query_server_name: "", key: "" } });
  const multiplex = () => ({ enabled: false, protocol: "smux", max_connections: 4, padding: false, brutal: { enabled: false, up_mbps: 100, down_mbps: 100 } });
  const reality = () => ({ server_name: "", server_port: 443, public_key: "", private_key: "", short_id: "", allow_insecure: false });
  switch (type) {
    case "shadowsocks": return { cipher: "aes-128-gcm", plugin: "", plugin_opts: "" };
    case "vmess": return { tls: 0, network: "tcp", network_settings: {}, tls_settings: tls(), utls: { enabled: false, fingerprint: "chrome" }, multiplex: multiplex() };
    case "trojan": return { tls: 1, network: "tcp", network_settings: {}, tls_settings: tls(), reality_settings: reality(), utls: { enabled: false, fingerprint: "chrome" }, multiplex: multiplex() };
    case "hysteria": return { version: 2, alpn: "h2", obfs: { open: false, type: "salamander", password: "" }, tls: tls(), bandwidth: { up: 0, down: 0 } };
    case "vless": return { tls: 0, network: "tcp", network_settings: {}, flow: "", encryption: { enabled: false, encryption: "", decryption: "" }, tls_settings: tls(), reality_settings: reality(), utls: { enabled: false, fingerprint: "chrome" }, multiplex: multiplex() };
    case "tuic": return { version: 5, congestion_control: "bbr", alpn: ["h3"], udp_relay_mode: "native", tls: tls() };
    case "socks": case "naive": case "http": return { tls: 0, tls_settings: tls() };
    case "mieru": return { transport: "TCP", traffic_pattern: "", multiplex: multiplex() };
    case "anytls": return { alpn: "", padding_scheme: [], tls: tls() };
    default: return {};
  }
}

function updateRateRange(input: AdminNodeDefinitionInput, setInput: (input: AdminNodeDefinitionInput) => void, index: number, field: "start" | "end" | "rate", value: string | number) {
  setInput({ ...input, rate_time_ranges: input.rate_time_ranges.map((range, position) => position === index ? { ...range, [field]: value } : range) });
}

const defaultAnyTLSPaddingScheme = [
  "stop=8", "0=30-30", "1=100-400", "2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000",
  "3=9-9,500-1000", "4=500-1000", "5=500-1000", "6=500-1000", "7=500-1000"
];

function supportsNetwork(type: string): boolean { return ["vmess", "trojan", "vless"].includes(type); }
function asRecord(value: unknown): Record<string, unknown> { return typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {}; }
function stringValue(value: unknown, fallback = ""): string { return typeof value === "string" ? value : fallback; }
function asStringArray(value: unknown): string[] { return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string") : []; }
function formatJSON(value: unknown): string { return JSON.stringify(value, null, 2); }
function parseJSONObject(value: string, label: string): Record<string, unknown> { const parsed: unknown = JSON.parse(value); if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 对象`); return parsed as Record<string, unknown>; }
function parseJSONArray(value: string, label: string): unknown[] { const parsed: unknown = JSON.parse(value); if (!Array.isArray(parsed)) throw new Error(`${label}必须是 JSON 数组`); return parsed; }
function parseDNSEnv(value: string): Record<string, string> { const result: Record<string, string> = {}; for (const line of value.split(/\r?\n/)) { const index = line.indexOf("="); if (index > 0) result[line.slice(0, index).trim()] = line.slice(index + 1).trim(); } return result; }
function formatDNSEnv(value: Record<string, unknown>): string { return Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === "string").map(([key, entry]) => `${key}=${entry}`).join("\n"); }
function splitTags(value: string): string[] { return [...new Set(value.split(/[,，]/).map((entry) => entry.trim()).filter(Boolean))]; }
function emptyToNull(value: string | null): string | null { const normalized = value?.trim() ?? ""; return normalized === "" ? null : normalized; }
function protocolLabel(type: string): string { return protocols.find(([value]) => value === type)?.[1] ?? type; }
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : "请求失败"; }
