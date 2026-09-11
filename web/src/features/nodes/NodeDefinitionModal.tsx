import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type {
  AdminNode, AdminNodeDefinition, AdminNodeDefinitionInput, AdminNodeParentOption, Machine, RoutingRule, ServerGroup
} from "../../lib/api";

export interface NodeDefinitionAPI {
  listAdminNodeParentOptions: (query: { type: string; q?: string; include_id?: number; exclude_id?: number }) => Promise<{ items: AdminNodeParentOption[]; has_more: boolean }>;
  getAdminNodeDefinition: (nodeID: number) => Promise<AdminNodeDefinition>;
  createAdminNodeDefinition: (input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
  replaceAdminNodeDefinition: (nodeID: number, input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
}

interface Props {
  api: NodeDefinitionAPI;
  node: AdminNode | null;
  machines: Machine[];
  groups: ServerGroup[];
  routes: RoutingRule[];
  onClose: () => void;
  onSaved: () => void;
}

export const protocols = [
  ["shadowsocks", "Shadowsocks"], ["vmess", "VMess"], ["trojan", "Trojan"], ["hysteria", "Hysteria"],
  ["vless", "VLess"], ["tuic", "TUIC"], ["socks", "SOCKS"], ["naive", "Naive"], ["http", "HTTP"],
  ["mieru", "Mieru"], ["anytls", "AnyTLS"]
] as const;

export const protocolConfigs: Record<string, { label: string; color: string }> = {
  shadowsocks: { label: "Shadowsocks", color: "#10b981" },
  vmess: { label: "VMess", color: "#d946ef" },
  trojan: { label: "Trojan", color: "#eab308" },
  hysteria: { label: "Hysteria", color: "#06b6d4" },
  vless: { label: "VLess", color: "#10b981" },
  tuic: { label: "TUIC", color: "#3b82f6" },
  socks: { label: "SOCKS", color: "#94a3b8" },
  naive: { label: "Naive", color: "#f97316" },
  http: { label: "HTTP", color: "#ef4444" },
  mieru: { label: "Mieru", color: "#eab308" },
  anytls: { label: "AnyTLS", color: "#8b5cf6" }
};

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

export function NodeDefinitionModal({ api, node, machines, groups, routes, onClose, onSaved }: Props) {
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
  const [parentQuery, setParentQuery] = useState("");
  const [parentOptions, setParentOptions] = useState<AdminNodeParentOption[]>([]);
  const [parentOptionsLoading, setParentOptionsLoading] = useState(false);
  const [parentOptionsError, setParentOptionsError] = useState("");
  const [parentOptionsHaveMore, setParentOptionsHaveMore] = useState(false);
  const [protocolDropdownOpen, setProtocolDropdownOpen] = useState(false);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const dropdownRef = useRef<HTMLDivElement>(null);

  const title = node === null ? "新建节点" : "编辑节点";
  const activeProtocol = protocolConfigs[input.type];

  // Handle outside click for custom protocol dropdown
  useEffect(() => {
    const handleOutsideClick = (e: MouseEvent) => {
      if (dropdownRef.current && !dropdownRef.current.contains(e.target as Node)) {
        setProtocolDropdownOpen(false);
      }
    };
    if (protocolDropdownOpen) {
      document.addEventListener("mousedown", handleOutsideClick);
    }
    return () => {
      document.removeEventListener("mousedown", handleOutsideClick);
    };
  }, [protocolDropdownOpen]);

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

  useEffect(() => {
    if (loading) return;
    let live = true;
    const timeout = window.setTimeout(() => {
      if (!live) return;
      setParentOptionsLoading(true);
      setParentOptionsError("");
      const query: { type: string; q?: string; include_id?: number; exclude_id?: number } = { type: input.type };
      const trimmed = parentQuery.trim();
      if (trimmed !== "") query.q = trimmed;
      if (input.parent_id !== null) query.include_id = input.parent_id;
      if (node !== null) query.exclude_id = node.id;
      void api.listAdminNodeParentOptions(query).then((result) => {
        if (!live) return;
        setParentOptions(result.items);
        setParentOptionsHaveMore(result.has_more);
      }).catch((cause: unknown) => {
        if (!live) return;
        setParentOptionsError(errorMessage(cause));
      }).finally(() => {
        if (live) setParentOptionsLoading(false);
      });
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(timeout);
    };
  }, [api, input.parent_id, input.type, loading, node, parentQuery]);

  const changeProtocol = (type: string) => {
    setInput((current) => ({ ...current, type, parent_id: null, protocol_settings: defaultProtocolSettings(type) }));
    setParentQuery("");
    setParentOptions([]);
    setParentOptionsHaveMore(false);
    setParentOptionsError("");
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

  return (
    <Modal title={title} className="node-definition-modal" onClose={onClose}>
      <div className="node-modal-container">
        {/* Header */}
        <div className="node-modal-header">
          <div className="node-modal-header-left">
            <div className="node-modal-title-line">
              <h2 className="node-modal-title">{title}</h2>
              {activeProtocol && (
                <span
                  className="node-protocol-pill"
                  style={{
                    color: activeProtocol.color,
                    backgroundColor: `${activeProtocol.color}15`,
                    borderColor: `${activeProtocol.color}44`
                  }}
                >
                  {activeProtocol.label}
                </span>
              )}
            </div>
            <p className="node-modal-subtitle">管理所有节点，包括添加、删除、编辑等操作。</p>
          </div>
          <div className="node-modal-header-right">
            {/* Styled Protocol Selector with accessible native select */}
            <div className="custom-protocol-selector" ref={dropdownRef}>
              <button
                type="button"
                className="custom-protocol-trigger"
                disabled={node !== null}
                onClick={() => setProtocolDropdownOpen(!protocolDropdownOpen)}
                aria-expanded={protocolDropdownOpen}
              >
                {activeProtocol ? (
                  <>
                    <span className="protocol-dot" style={{ backgroundColor: activeProtocol.color }} />
                    <span>{activeProtocol.label}</span>
                  </>
                ) : (
                  <span>选择协议类型</span>
                )}
                <svg className="chevron-icon" width="12" height="12" viewBox="0 0 12 12" fill="none">
                  <path d="M2.5 4.5L6 8L9.5 4.5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                </svg>
              </button>

              {/* Accessible select for test runners and screen readers */}
              <label htmlFor="node-protocol-select" className="accessible-select-label sr-only">协议类型</label>
              <select
                id="node-protocol-select"
                aria-label="协议类型"
                className="accessible-hidden-select"
                value={input.type}
                disabled={node !== null}
                onChange={(event) => changeProtocol(event.target.value)}
              >
                {protocols.map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </select>

              {/* Dropdown Menu */}
              {protocolDropdownOpen && (
                <div className="custom-protocol-menu" role="listbox">
                  {protocols.map(([value, label]) => {
                    const cfg = protocolConfigs[value];
                    const isSelected = input.type === value;
                    return (
                      <div
                        key={value}
                        className={`custom-protocol-item ${isSelected ? "selected" : ""}`}
                        role="option"
                        aria-selected={isSelected}
                        onClick={() => {
                          changeProtocol(value);
                          setProtocolDropdownOpen(false);
                        }}
                      >
                        <span className="protocol-dot" style={{ backgroundColor: cfg?.color ?? "#94a3b8" }} />
                        <span className="protocol-item-label">{label}</span>
                        {isSelected && (
                          <svg className="check-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                            <polyline points="20 6 9 17 4 12" />
                          </svg>
                        )}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Modal Close Button */}
            <button className="node-modal-close-btn" aria-label={`关闭${title}`} onClick={onClose}>
              ×
            </button>
          </div>
        </div>

        {/* Modal Body */}
        {loading ? (
          <div className="node-modal-loading" aria-live="polite">正在加载节点定义…</div>
        ) : (
          <form className="node-modal-form" onSubmit={(event) => void submit(event)}>
            <div className="node-modal-scrollable">
              {/* Row 1: Name & Base Rate */}
              <div className="form-row-duo">
                <div className="form-field flex-2">
                  <label htmlFor="node-name-input" className="field-label">节点名称</label>
                  <input
                    id="node-name-input"
                    required
                    maxLength={255}
                    placeholder="请输入节点名称"
                    value={input.name}
                    onChange={(event) => setInput({ ...input, name: event.target.value })}
                  />
                </div>
                <div className="form-field flex-1">
                  <label htmlFor="node-rate-input" className="field-label">基础倍率</label>
                  <div className="input-with-suffix">
                    <input
                      id="node-rate-input"
                      required
                      type="number"
                      min="0.000001"
                      max="1000"
                      step="any"
                      value={input.rate}
                      onChange={(event) => setInput({ ...input, rate: Number(event.target.value) })}
                    />
                    <span className="input-suffix">x</span>
                  </div>
                </div>
              </div>

              {/* Row 2: Dynamic Rate Switch Card */}
              <div className="dynamic-rate-toggle-box">
                <div className="dynamic-rate-left">
                  <div className="dynamic-rate-title-row">
                    <label htmlFor="node-dynamic-rate-toggle" className="field-label">启用动态倍率</label>
                    <input
                      id="node-dynamic-rate-toggle"
                      type="checkbox"
                      className="switch-input"
                      checked={input.rate_time_enabled}
                      onChange={(event) => setInput({ ...input, rate_time_enabled: event.target.checked })}
                    />
                  </div>
                  <span className="dynamic-rate-desc">根据时间段设置不同的倍率乘数</span>
                </div>
              </div>

              {/* Dynamic Rate Range List */}
              {input.rate_time_enabled && (
                <div className="rate-range-list">
                  {input.rate_time_ranges.map((range, index) => (
                    <div className="rate-range-row" key={`${index}-${range.start}`}>
                      <div className="rate-range-field">
                        <label htmlFor={`rate-start-${index}`} className="field-label">{`动态倍率 ${index + 1} 开始`}</label>
                        <input
                          id={`rate-start-${index}`}
                          type="time"
                          value={range.start}
                          onChange={(event) => updateRateRange(input, setInput, index, "start", event.target.value)}
                        />
                      </div>
                      <div className="rate-range-field">
                        <label htmlFor={`rate-end-${index}`} className="field-label">{`动态倍率 ${index + 1} 结束`}</label>
                        <input
                          id={`rate-end-${index}`}
                          type="time"
                          value={range.end}
                          onChange={(event) => updateRateRange(input, setInput, index, "end", event.target.value)}
                        />
                      </div>
                      <div className="rate-range-field">
                        <label htmlFor={`rate-value-${index}`} className="field-label">{`动态倍率 ${index + 1} 倍率`}</label>
                        <input
                          id={`rate-value-${index}`}
                          type="number"
                          min="0"
                          max="1000"
                          step="0.01"
                          value={range.rate}
                          onChange={(event) => updateRateRange(input, setInput, index, "rate", Number(event.target.value))}
                        />
                      </div>
                      <button
                        className="button compact ghost danger-text rate-remove-btn"
                        type="button"
                        onClick={() => setInput({ ...input, rate_time_ranges: input.rate_time_ranges.filter((_, pos) => pos !== index) })}
                      >
                        移除
                      </button>
                    </div>
                  ))}
                  <button
                    className="button compact secondary add-rate-range-btn"
                    type="button"
                    onClick={() => setInput({ ...input, rate_time_ranges: [...input.rate_time_ranges, { start: "00:00", end: "23:59", rate: 1 }] })}
                  >
                    添加时间段
                  </button>
                </div>
              )}

              {/* Row 3: Traffic Limit & Custom Node ID */}
              <div className="form-row-duo">
                <div className="form-field flex-1">
                  <label htmlFor="node-transfer-input" className="field-label">流量限制 (GiB)</label>
                  <input
                    id="node-transfer-input"
                    required
                    type="number"
                    min="0"
                    step="0.01"
                    placeholder="0 表示不限制"
                    value={transferGiB}
                    onChange={(event) => setTransferGiB(event.target.value)}
                  />
                </div>
                <div className="form-field flex-1">
                  <label htmlFor="node-custom-id-input" className="field-label">自定义 ID</label>
                  <input
                    id="node-custom-id-input"
                    maxLength={255}
                    placeholder="请输入自定义节点ID"
                    value={input.external_code ?? ""}
                    onChange={(event) => setInput({ ...input, external_code: event.target.value })}
                  />
                </div>
              </div>

              {/* Row 4: Tags */}
              <div className="form-field">
                <label htmlFor="node-tags-input" className="field-label">标签</label>
                <input
                  id="node-tags-input"
                  maxLength={4096}
                  placeholder="输入后回车添加标签"
                  value={tagsText}
                  onChange={(event) => setTagsText(event.target.value)}
                />
              </div>

              {/* Row 5: Permission Groups */}
              <div className="form-field">
                <div className="field-header-row">
                  <label htmlFor="node-groups-select" className="field-label">权限组</label>
                  <a href="#/server/group" className="field-action-link" target="_blank" rel="noreferrer">
                    添加权限组
                  </a>
                </div>
                <select
                  id="node-groups-select"
                  multiple
                  aria-label="权限组"
                  className="permission-groups-select"
                  value={input.group_ids.map(String)}
                  onChange={(event) => changeMultiple("group_ids", event)}
                >
                  {groups.map((group) => (
                    <option key={group.id} value={group.id}>{group.name}</option>
                  ))}
                </select>
              </div>

              {/* Row 6: Node Host Address */}
              <div className="form-field">
                <label htmlFor="node-host-input" className="field-label">节点地址</label>
                <input
                  id="node-host-input"
                  required
                  maxLength={255}
                  placeholder="请输入节点域名或者IP"
                  value={input.host}
                  onChange={(event) => setInput({ ...input, host: event.target.value })}
                />
              </div>

              {/* Row 7: Port Mapping Banner */}
              <div className="port-mapping-row">
                <div className="form-field flex-1">
                  <label htmlFor="node-port-input" className="field-label">连接端口</label>
                  <input
                    id="node-port-input"
                    required
                    inputMode="numeric"
                    pattern="[0-9]{1,5}(-[0-9]{1,5})?"
                    placeholder="用户连接端口"
                    value={input.port}
                    onChange={(event) => setInput({ ...input, port: event.target.value })}
                  />
                </div>
                <div className="port-arrow-indicator" aria-hidden="true">⇒</div>
                <div className="form-field flex-1">
                  <label htmlFor="node-server-port-input" className="field-label">服务端口</label>
                  <input
                    id="node-server-port-input"
                    required
                    type="number"
                    min="1"
                    max="65535"
                    placeholder="请输入服务端口"
                    value={input.server_port}
                    onChange={(event) => setInput({ ...input, server_port: Number(event.target.value) })}
                  />
                </div>
              </div>

              {/* Protocol-Specific Section */}
              <div className="protocol-specific-card">
                <ProtocolFields input={input} setInput={setInput} />
                {supportsNetwork(input.type) && (
                  <div className="network-settings-editor">
                    <div className="form-field">
                      <label htmlFor="node-network-settings-input" className="field-label">传输协议设置 (JSON)</label>
                      <textarea
                        id="node-network-settings-input"
                        rows={6}
                        value={networkSettingsText}
                        onChange={(event) => setNetworkSettingsText(event.target.value)}
                      />
                    </div>
                    <div className="network-template-actions" aria-label="传输协议模板">
                      {(networkTemplates[stringValue(input.protocol_settings.network, "tcp")] ?? []).map((template) => (
                        <button
                          className="button compact secondary"
                          type="button"
                          key={template.label}
                          onClick={() => setNetworkSettingsText(formatJSON(template.value))}
                        >
                          套用 {template.label} 模板
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>

              {/* Hierarchy and Machine Binding */}
              <div className="node-binding-group">
                <div className="parent-search-row">
                  <div className="form-field flex-1">
                    <label htmlFor="node-parent-search-input" className="field-label">搜索父节点</label>
                    <input
                      id="node-parent-search-input"
                      maxLength={255}
                      value={parentQuery}
                      onChange={(event) => setParentQuery(event.target.value)}
                      placeholder="名称或 #节点ID"
                    />
                  </div>
                  <div className="form-field flex-2">
                    <label htmlFor="node-parent-select" className="field-label">父节点</label>
                    <select
                      id="node-parent-select"
                      aria-label="父节点"
                      value={input.parent_id ?? ""}
                      onChange={(event) => setInput({ ...input, parent_id: event.target.value === "" ? null : Number(event.target.value) })}
                    >
                      <option value="">无父节点</option>
                      {parentOptions.map((candidate) => (
                        <option key={candidate.id} value={candidate.id}>
                          {candidate.name} (#{candidate.id})
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
                <small className="field-muted-notice" aria-live="polite">
                  {parentOptionsLoading
                    ? "正在搜索父节点…"
                    : parentOptionsError !== ""
                    ? `父节点加载失败：${parentOptionsError}`
                    : parentOptionsHaveMore
                    ? "仅显示前 50 项，请输入名称或 ID 缩小范围。"
                    : parentOptions.length === 0
                    ? "没有匹配的同协议父节点。"
                    : `找到 ${parentOptions.length} 项。`}
                </small>

                <div className="form-row-duo" style={{ marginTop: "12px" }}>
                  <div className="form-field flex-1">
                    <label htmlFor="node-routes-select" className="field-label">路由规则</label>
                    <select
                      id="node-routes-select"
                      multiple
                      aria-label="路由规则"
                      className="routing-select"
                      value={input.route_ids.map(String)}
                      onChange={(event) => changeMultiple("route_ids", event)}
                    >
                      {routes.map((route) => (
                        <option key={route.id} value={route.id}>{route.remarks}</option>
                      ))}
                    </select>
                  </div>
                  <div className="form-field flex-1">
                    <label htmlFor="node-machine-select" className="field-label">绑定服务器</label>
                    <select
                      id="node-machine-select"
                      value={input.machine_id ?? ""}
                      onChange={(event) => setInput({ ...input, machine_id: event.target.value === "" ? null : Number(event.target.value) })}
                    >
                      <option value="">独立部署</option>
                      {machines.map((machine) => (
                        <option key={machine.id} value={machine.id}>{machine.name}</option>
                      ))}
                    </select>
                  </div>
                </div>
              </div>

              {/* Advanced Settings Drawer/Collapsible */}
              {showAdvanced && (
                <div className="node-advanced-drawer">
                  <div className="advanced-drawer-title">高级设置选项</div>
                  <div className="form-row-duo">
                    <div className="form-field flex-2">
                      <label htmlFor="node-listen-address" className="field-label">监听地址</label>
                      <input
                        id="node-listen-address"
                        required
                        maxLength={45}
                        placeholder="0.0.0.0 或 ::"
                        value={input.listen_address}
                        onChange={(event) => setInput({ ...input, listen_address: event.target.value })}
                      />
                    </div>
                    <div className="form-field flex-1">
                      <label htmlFor="node-sort-order" className="field-label">排序</label>
                      <input
                        id="node-sort-order"
                        required
                        type="number"
                        min="0"
                        max="1000000000"
                        value={input.sort}
                        onChange={(event) => setInput({ ...input, sort: Number(event.target.value) })}
                      />
                    </div>
                  </div>

                  <div className="switch-row-bar">
                    <div className="switch-inline-control">
                      <label htmlFor="node-show-toggle" className="field-label">用户端显示</label>
                      <input
                        id="node-show-toggle"
                        type="checkbox"
                        className="switch-input"
                        checked={input.show}
                        onChange={(event) => setInput({ ...input, show: event.target.checked })}
                      />
                    </div>
                    <div className="switch-inline-control">
                      <label htmlFor="node-enabled-toggle" className="field-label">启用运行</label>
                      <input
                        id="node-enabled-toggle"
                        type="checkbox"
                        className="switch-input"
                        checked={input.enabled}
                        onChange={(event) => setInput({ ...input, enabled: event.target.checked })}
                      />
                    </div>
                  </div>

                  <fieldset className="advanced-fieldset">
                    <legend>证书配置</legend>
                    <CertificateFields
                      key={formatDNSEnv(asRecord(asRecord(input.certificate_config).dns_env))}
                      value={asRecord(input.certificate_config)}
                      onChange={updateCertificate}
                    />
                  </fieldset>

                  <div className="form-field">
                    <label htmlFor="node-outbounds-input" className="field-label">自定义出站 (JSON 数组)</label>
                    <textarea
                      id="node-outbounds-input"
                      rows={5}
                      value={customOutboundsText}
                      onChange={(event) => setCustomOutboundsText(event.target.value)}
                    />
                  </div>

                  <div className="form-field">
                    <label htmlFor="node-routes-input" className="field-label">自定义路由 (JSON 数组)</label>
                    <textarea
                      id="node-routes-input"
                      rows={5}
                      value={customRoutesText}
                      onChange={(event) => setCustomRoutesText(event.target.value)}
                    />
                  </div>

                  <details className="json-expert-details">
                    <summary>证书配置 JSON（专家）</summary>
                    <div className="form-field">
                      <label htmlFor="node-cert-json-input" className="field-label">证书配置 (JSON 对象)</label>
                      <textarea
                        id="node-cert-json-input"
                        rows={7}
                        value={certificateText}
                        onChange={(event) => setCertificateText(event.target.value)}
                        onBlur={() => {
                          try {
                            updateCertificate(parseJSONObject(certificateText, "证书配置"));
                            setError("");
                          } catch (cause) {
                            setError(errorMessage(cause));
                          }
                        }}
                      />
                    </div>
                  </details>
                </div>
              )}

              {/* Error banner */}
              {error !== "" && (
                <div className="alert error" role="alert">
                  {error}
                </div>
              )}
            </div>

            {/* Modal Footer */}
            <div className="node-modal-footer">
              <button
                type="button"
                className={`advanced-toggle-btn ${showAdvanced ? "active" : ""}`}
                onClick={() => setShowAdvanced((curr) => !curr)}
              >
                <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <line x1="4" y1="21" x2="4" y2="14" />
                  <line x1="4" y1="10" x2="4" y2="3" />
                  <line x1="12" y1="21" x2="12" y2="12" />
                  <line x1="12" y1="8" x2="12" y2="3" />
                  <line x1="20" y1="21" x2="20" y2="16" />
                  <line x1="20" y1="12" x2="20" y2="3" />
                  <line x1="1" y1="14" x2="7" y2="14" />
                  <line x1="9" y1="8" x2="15" y2="8" />
                  <line x1="17" y1="16" x2="23" y2="16" />
                </svg>
                <span>高级设置</span>
              </button>

              <div className="footer-action-buttons">
                <button className="button ghost modal-cancel-btn" type="button" disabled={saving} onClick={onClose}>
                  取消
                </button>
                <button className="button primary modal-submit-btn" type="submit" disabled={saving}>
                  {saving ? "正在保存…" : "提交"}
                </button>
              </div>
            </div>
          </form>
        )}
      </div>
    </Modal>
  );
}

function CertificateFields({ value, onChange }: { value: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void }) {
  const mode = stringValue(value.cert_mode, "none");
  const set = (field: string, fieldValue: unknown) => onChange({ ...value, [field]: fieldValue });
  const [dnsEnvText, setDNSEnvText] = useState(() => formatDNSEnv(asRecord(value.dns_env)));

  return (
    <div className="node-form-grid certificate-fields">
      <div className="form-field">
        <label htmlFor="cert-mode-select" className="field-label">证书模式</label>
        <select id="cert-mode-select" value={mode} onChange={(event) => set("cert_mode", event.target.value)}>
          <option value="none">none</option>
          <option value="http">http-01 (ACME)</option>
          <option value="dns">dns-01 (ACME)</option>
          <option value="self">self-signed</option>
          <option value="content">content (Cert Push)</option>
        </select>
      </div>

      {mode !== "none" && (
        <div className="form-field">
          <label htmlFor="cert-domain-input" className="field-label">证书域名</label>
          <input
            id="cert-domain-input"
            maxLength={4096}
            value={stringValue(value.domain)}
            onChange={(event) => set("domain", event.target.value)}
            placeholder="example.com"
          />
        </div>
      )}

      {["http", "dns"].includes(mode) && (
        <div className="form-field">
          <label htmlFor="cert-email-input" className="field-label">ACME 邮箱</label>
          <input
            id="cert-email-input"
            type="email"
            maxLength={4096}
            value={stringValue(value.email)}
            onChange={(event) => set("email", event.target.value)}
            placeholder="admin@example.com"
          />
        </div>
      )}

      {mode === "http" && (
        <div className="form-field">
          <label htmlFor="cert-port-input" className="field-label">HTTP 挑战端口</label>
          <input
            id="cert-port-input"
            type="number"
            min="1"
            max="65535"
            value={Number(value.http_port ?? 80)}
            onChange={(event) => set("http_port", Number(event.target.value))}
          />
        </div>
      )}

      {mode === "dns" && (
        <>
          <div className="form-field">
            <label htmlFor="cert-dns-provider-input" className="field-label">DNS Provider</label>
            <input
              id="cert-dns-provider-input"
              maxLength={4096}
              value={stringValue(value.dns_provider)}
              onChange={(event) => set("dns_provider", event.target.value)}
              placeholder="cloudflare / alidns / dnspod"
            />
          </div>
          <div className="form-field full-field">
            <label htmlFor="cert-dns-env-input" className="field-label">DNS 环境变量</label>
            <textarea
              id="cert-dns-env-input"
              rows={4}
              spellCheck={false}
              value={dnsEnvText}
              onChange={(event) => setDNSEnvText(event.target.value)}
              onBlur={() => set("dns_env", parseDNSEnv(dnsEnvText))}
              placeholder={"CF_API_TOKEN=xxxxxx\nALIDNS_ACCESS_KEY_ID=xxxx"}
            />
          </div>
        </>
      )}

      {mode === "content" && (
        <>
          <div className="form-field full-field">
            <label htmlFor="cert-content-input" className="field-label">证书内容</label>
            <textarea
              id="cert-content-input"
              rows={6}
              spellCheck={false}
              value={stringValue(value.cert_content)}
              onChange={(event) => set("cert_content", event.target.value)}
              placeholder={"-----BEGIN CERTIFICATE-----\n..."}
            />
          </div>
          <div className="form-field full-field">
            <label htmlFor="cert-key-input" className="field-label">私钥内容</label>
            <textarea
              id="cert-key-input"
              rows={6}
              spellCheck={false}
              value={stringValue(value.key_content)}
              onChange={(event) => set("key_content", event.target.value)}
              placeholder={"-----BEGIN PRIVATE KEY-----\n..."}
            />
          </div>
        </>
      )}
    </div>
  );
}

function ProtocolFields({ input, setInput }: { input: AdminNodeDefinitionInput; setInput: (input: AdminNodeDefinitionInput) => void }) {
  const settings = input.protocol_settings;
  const set = (key: string, value: unknown) => setInput({ ...input, protocol_settings: { ...settings, [key]: value } });
  const setNested = (key: string, field: string, value: unknown) => set(key, { ...asRecord(settings[key]), [field]: value });
  const tlsKey = ["hysteria", "tuic", "anytls"].includes(input.type) ? "tls" : "tls_settings";
  const tls = asRecord(settings[tlsKey]);
  const securityOptions = input.type === "vmess" ? [[0, "None"], [1, "TLS"]] : input.type === "trojan" ? [[1, "TLS"], [2, "Reality"]] : [[0, "None"], [1, "TLS"], [2, "Reality"]];

  return (
    <div className="protocol-fields-container">
      {/* Shadowsocks */}
      {input.type === "shadowsocks" && (
        <div className="protocol-fields-group">
          <div className="form-field">
            <label htmlFor="ss-cipher-input" className="field-label">加密算法</label>
            <input
              id="ss-cipher-input"
              list="shadowsocks-ciphers"
              value={stringValue(settings.cipher)}
              onChange={(event) => set("cipher", event.target.value)}
            />
            <datalist id="shadowsocks-ciphers">
              {["aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305", "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"].map((cipher) => (
                <option key={cipher} value={cipher} />
              ))}
            </datalist>
            <small className="field-input-hint">选择预设加密方式或输入自定义加密方式</small>
          </div>

          <div className="form-field">
            <label htmlFor="ss-plugin-select" className="field-label">插件</label>
            <select id="ss-plugin-select" value={stringValue(settings.plugin)} onChange={(event) => set("plugin", event.target.value)}>
              <option value="">None</option>
              <option value="obfs">Simple Obfs</option>
              <option value="v2ray-plugin">V2Ray Plugin</option>
              <option value="gost-plugin">Gost Plugin</option>
              <option value="shadow-tls">Shadow TLS</option>
              <option value="restls">ResTLS</option>
              <option value="kcptun">KCPTun</option>
            </select>
          </div>

          {stringValue(settings.plugin) !== "" && (
            <div className="form-field full-field">
              <label htmlFor="ss-plugin-opts-input" className="field-label">插件参数</label>
              <input
                id="ss-plugin-opts-input"
                maxLength={4096}
                value={stringValue(settings.plugin_opts)}
                onChange={(event) => set("plugin_opts", event.target.value)}
              />
            </div>
          )}
        </div>
      )}

      {/* VMess, Trojan, VLess */}
      {["vmess", "trojan", "vless"].includes(input.type) && (
        <div className="protocol-fields-group">
          <div className="form-field">
            <label htmlFor="protocol-security-select" className="field-label">安全性</label>
            <select
              id="protocol-security-select"
              value={Number(settings.tls ?? 0)}
              onChange={(event) => set("tls", Number(event.target.value))}
            >
              {securityOptions.map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </select>
          </div>

          <div className="form-field">
            <label htmlFor="protocol-network-select" className="field-label">传输协议</label>
            <select
              id="protocol-network-select"
              value={stringValue(settings.network, "tcp")}
              onChange={(event) => set("network", event.target.value)}
            >
              {[...networks, ...(input.type === "vless" ? [["kcp", "mKCP"]] as const : [])].map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </select>
          </div>

          {input.type === "vless" && (
            <div className="form-field">
              <label htmlFor="vless-flow-select" className="field-label">Flow</label>
              <select
                id="vless-flow-select"
                value={stringValue(settings.flow)}
                onChange={(event) => set("flow", event.target.value)}
              >
                <option value="">None</option>
                <option value="xtls-rprx-direct">xtls-rprx-direct</option>
                <option value="xtls-rprx-splice">xtls-rprx-splice</option>
                <option value="xtls-rprx-vision">xtls-rprx-vision</option>
              </select>
            </div>
          )}

          {Number(settings.tls) === 1 && <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />}

          {Number(settings.tls) > 0 && (
            <>
              <div className="dynamic-rate-toggle-box">
                <div className="dynamic-rate-left">
                  <div className="dynamic-rate-title-row">
                    <label htmlFor="utls-toggle" className="field-label">uTLS</label>
                    <input
                      id="utls-toggle"
                      type="checkbox"
                      className="switch-input"
                      checked={Boolean(asRecord(settings.utls).enabled)}
                      onChange={(event) => set("utls", { ...asRecord(settings.utls), enabled: event.target.checked })}
                    />
                  </div>
                  <span className="dynamic-rate-desc">客户端伪装指纹，用于降低被识别风险</span>
                </div>
              </div>

              {Boolean(asRecord(settings.utls).enabled) && (
                <div className="form-field">
                  <label htmlFor="utls-fingerprint-select" className="field-label">uTLS 指纹</label>
                  <select
                    id="utls-fingerprint-select"
                    value={stringValue(asRecord(settings.utls).fingerprint, "chrome")}
                    onChange={(event) => set("utls", { ...asRecord(settings.utls), fingerprint: event.target.value })}
                  >
                    {["chrome", "firefox", "safari", "ios", "edge", "random"].map((fingerprint) => (
                      <option key={fingerprint} value={fingerprint}>{fingerprint}</option>
                    ))}
                  </select>
                </div>
              )}
            </>
          )}

          <MultiplexFields settings={settings} set={set} />

          {Number(settings.tls) === 2 && <RealityFields settings={settings} set={set} />}

          {input.type === "vless" && (
            <>
              <div className="dynamic-rate-toggle-box">
                <div className="dynamic-rate-left">
                  <div className="dynamic-rate-title-row">
                    <label htmlFor="vless-enc-toggle" className="field-label">VLESS Encryption</label>
                    <input
                      id="vless-enc-toggle"
                      type="checkbox"
                      className="switch-input"
                      checked={Boolean(asRecord(settings.encryption).enabled)}
                      onChange={(event) => set("encryption", { ...asRecord(settings.encryption), enabled: event.target.checked })}
                    />
                  </div>
                  <span className="dynamic-rate-desc">启用 VLESS 加密</span>
                </div>
              </div>

              {Boolean(asRecord(settings.encryption).enabled) && (
                <div className="form-row-duo">
                  <div className="form-field flex-1">
                    <label htmlFor="vless-enc-key" className="field-label">客户端公钥</label>
                    <input
                      id="vless-enc-key"
                      maxLength={8192}
                      value={stringValue(asRecord(settings.encryption).encryption)}
                      onChange={(event) => set("encryption", { ...asRecord(settings.encryption), encryption: event.target.value })}
                    />
                  </div>
                  <div className="form-field flex-1">
                    <label htmlFor="vless-dec-key" className="field-label">服务端私钥</label>
                    <input
                      id="vless-dec-key"
                      maxLength={8192}
                      value={stringValue(asRecord(settings.encryption).decryption)}
                      onChange={(event) => set("encryption", { ...asRecord(settings.encryption), decryption: event.target.value })}
                    />
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* Hysteria */}
      {input.type === "hysteria" && (
        <div className="protocol-fields-group">
          <div className="form-row-duo">
            <div className="form-field flex-1">
              <label htmlFor="hysteria-version-select" className="field-label">版本</label>
              <select
                id="hysteria-version-select"
                value={Number(settings.version ?? 2)}
                onChange={(event) => set("version", Number(event.target.value))}
              >
                <option value={1}>V1</option>
                <option value={2}>V2</option>
              </select>
            </div>

            {Number(settings.version ?? 2) === 1 && (
              <div className="form-field flex-1">
                <label htmlFor="hysteria-alpn-select" className="field-label">ALPN</label>
                <select
                  id="hysteria-alpn-select"
                  value={stringValue(settings.alpn, "h2")}
                  onChange={(event) => set("alpn", event.target.value)}
                >
                  {["hysteria", "http/1.1", "h2", "h3"].map((value) => (
                    <option key={value} value={value}>{value}</option>
                  ))}
                </select>
              </div>
            )}
          </div>

          <div className="dynamic-rate-toggle-box">
            <div className="dynamic-rate-left">
              <div className="dynamic-rate-title-row">
                <label htmlFor="hysteria-obfs-toggle" className="field-label">混淆</label>
                <input
                  id="hysteria-obfs-toggle"
                  type="checkbox"
                  className="switch-input"
                  checked={Boolean(asRecord(settings.obfs).open)}
                  onChange={(event) => set("obfs", { ...asRecord(settings.obfs), open: event.target.checked })}
                />
              </div>
              <span className="dynamic-rate-desc">开启数据混淆以防止协议特征检测</span>
            </div>
          </div>

          {Boolean(asRecord(settings.obfs).open) && (
            <div className="form-field">
              <label htmlFor="hysteria-obfs-pwd" className="field-label">混淆密码</label>
              <input
                id="hysteria-obfs-pwd"
                maxLength={4096}
                value={stringValue(asRecord(settings.obfs).password)}
                onChange={(event) => set("obfs", { ...asRecord(settings.obfs), password: event.target.value })}
              />
            </div>
          )}

          <div className="form-row-duo">
            <div className="form-field flex-1">
              <label htmlFor="hysteria-up-bw" className="field-label">上行带宽 (Mbps)</label>
              <input
                id="hysteria-up-bw"
                type="number"
                min="0"
                max="1000000"
                value={Number(asRecord(settings.bandwidth).up ?? 0)}
                onChange={(event) => set("bandwidth", { ...asRecord(settings.bandwidth), up: Number(event.target.value) })}
              />
            </div>
            <div className="form-field flex-1">
              <label htmlFor="hysteria-down-bw" className="field-label">下行带宽 (Mbps)</label>
              <input
                id="hysteria-down-bw"
                type="number"
                min="0"
                max="1000000"
                value={Number(asRecord(settings.bandwidth).down ?? 0)}
                onChange={(event) => set("bandwidth", { ...asRecord(settings.bandwidth), down: Number(event.target.value) })}
              />
            </div>
          </div>

          <div className="form-field">
            <label htmlFor="hysteria-hop-int" className="field-label">端口跳跃间隔 (秒)</label>
            <input
              id="hysteria-hop-int"
              type="number"
              min="1"
              max="86400"
              placeholder="例如: 30"
              value={settings.hop_interval == null ? "" : Number(settings.hop_interval)}
              onChange={(event) => set("hop_interval", event.target.value === "" ? undefined : Number(event.target.value))}
            />
          </div>

          <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </div>
      )}

      {/* TUIC */}
      {input.type === "tuic" && (
        <div className="protocol-fields-group">
          <div className="form-row-duo">
            <div className="form-field flex-1">
              <label htmlFor="tuic-version-select" className="field-label">版本</label>
              <select
                id="tuic-version-select"
                value={Number(settings.version ?? 5)}
                onChange={(event) => set("version", Number(event.target.value))}
              >
                <option value={5}>V5</option>
                <option value={4}>V4</option>
              </select>
            </div>
            <div className="form-field flex-1">
              <label htmlFor="tuic-congestion-select" className="field-label">拥塞控制</label>
              <select
                id="tuic-congestion-select"
                value={stringValue(settings.congestion_control, "bbr")}
                onChange={(event) => set("congestion_control", event.target.value)}
              >
                <option value="bbr">BBR</option>
                <option value="cubic">CUBIC</option>
                <option value="new_reno">NEW_RENO</option>
              </select>
            </div>
          </div>

          <div className="form-row-duo">
            <div className="form-field flex-1">
              <label htmlFor="tuic-alpn-select" className="field-label">ALPN</label>
              <select
                id="tuic-alpn-select"
                multiple
                aria-label="ALPN"
                value={asStringArray(settings.alpn)}
                onChange={(event) => set("alpn", Array.from(event.currentTarget.selectedOptions, (opt) => opt.value))}
              >
                <option value="h3">h3</option>
                <option value="h2">HTTP/2</option>
                <option value="http/1.1">HTTP/1.1</option>
              </select>
            </div>
            <div className="form-field flex-1">
              <label htmlFor="tuic-udp-relay-select" className="field-label">UDP Relay</label>
              <select
                id="tuic-udp-relay-select"
                value={stringValue(settings.udp_relay_mode, "native")}
                onChange={(event) => set("udp_relay_mode", event.target.value)}
              >
                <option value="native">Native</option>
                <option value="quic">QUIC</option>
              </select>
            </div>
          </div>

          <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </div>
      )}

      {/* SOCKS, Naive, HTTP */}
      {["socks", "naive", "http"].includes(input.type) && (
        <div className="protocol-fields-group">
          <div className="form-field">
            <label htmlFor="std-tls-select" className="field-label">TLS</label>
            <select
              id="std-tls-select"
              value={Number(settings.tls ?? 0)}
              onChange={(event) => set("tls", Number(event.target.value))}
            >
              <option value={0}>不支持</option>
              <option value={1}>支持</option>
            </select>
          </div>
          <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </div>
      )}

      {/* Mieru */}
      {input.type === "mieru" && (
        <div className="protocol-fields-group">
          <div className="form-field">
            <label htmlFor="mieru-transport-select" className="field-label">传输协议</label>
            <select
              id="mieru-transport-select"
              value={stringValue(settings.transport, "TCP")}
              onChange={(event) => set("transport", event.target.value)}
            >
              <option value="TCP">TCP</option>
              <option value="UDP">UDP</option>
            </select>
          </div>
          <div className="form-field">
            <label htmlFor="mieru-pattern-input" className="field-label">Traffic Pattern</label>
            <input
              id="mieru-pattern-input"
              maxLength={4096}
              value={stringValue(settings.traffic_pattern)}
              onChange={(event) => set("traffic_pattern", event.target.value)}
            />
          </div>
          <MultiplexFields settings={settings} set={set} />
        </div>
      )}

      {/* AnyTLS */}
      {input.type === "anytls" && (
        <div className="protocol-fields-group">
          <div className="form-field">
            <label htmlFor="anytls-alpn-input" className="field-label">ALPN</label>
            <input
              id="anytls-alpn-input"
              maxLength={64}
              value={stringValue(settings.alpn)}
              onChange={(event) => set("alpn", event.target.value)}
            />
          </div>
          <div className="form-field full-field">
            <label htmlFor="anytls-padding-input" className="field-label">Padding Scheme</label>
            <textarea
              id="anytls-padding-input"
              rows={5}
              value={asStringArray(settings.padding_scheme).join("\n")}
              onChange={(event) => set("padding_scheme", event.target.value.split(/\r?\n/).filter(Boolean))}
            />
            <button
              className="button compact secondary"
              type="button"
              style={{ marginTop: "6px" }}
              onClick={() => set("padding_scheme", defaultAnyTLSPaddingScheme)}
            >
              使用默认方案
            </button>
          </div>
          <TLSFields tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </div>
      )}
    </div>
  );
}

function TLSFields({ tls, setTLS }: { tls: Record<string, unknown>; setTLS: (field: string, value: unknown) => void }) {
  const ech = asRecord(tls.ech);

  return (
    <div className="tls-fields-box">
      <div className="sni-row">
        <div className="form-field flex-2">
          <label htmlFor="tls-sni-input" className="field-label">SNI</label>
          <input
            id="tls-sni-input"
            maxLength={255}
            placeholder="当节点地址与证书不一致时用于证书验证"
            value={stringValue(tls.server_name)}
            onChange={(event) => setTLS("server_name", event.target.value)}
          />
        </div>
        <div className="switch-inline-control">
          <label htmlFor="tls-insecure-toggle" className="field-label">允许不安全连接</label>
          <input
            id="tls-insecure-toggle"
            type="checkbox"
            className="switch-input"
            checked={Boolean(tls.allow_insecure)}
            onChange={(event) => setTLS("allow_insecure", event.target.checked)}
          />
        </div>
      </div>

      <div className="dynamic-rate-toggle-box" style={{ marginTop: "10px" }}>
        <div className="dynamic-rate-left">
          <div className="dynamic-rate-title-row">
            <label htmlFor="ech-toggle" className="field-label">ECH</label>
            <input
              id="ech-toggle"
              type="checkbox"
              className="switch-input"
              checked={Boolean(ech.enabled)}
              onChange={(event) => setTLS("ech", { ...ech, enabled: event.target.checked })}
            />
          </div>
          <span className="dynamic-rate-desc">加密客户端 Hello (Encrypted Client Hello)</span>
        </div>
      </div>

      {Boolean(ech.enabled) && (
        <div className="ech-fields-block">
          <div className="form-field">
            <label htmlFor="ech-config-input" className="field-label">ECH Config</label>
            <textarea
              id="ech-config-input"
              required
              rows={3}
              value={stringValue(ech.config)}
              onChange={(event) => setTLS("ech", { ...ech, config: event.target.value })}
            />
          </div>
          <div className="form-field">
            <label htmlFor="ech-key-input" className="field-label">ECH Key</label>
            <textarea
              id="ech-key-input"
              required
              rows={2}
              value={stringValue(ech.key)}
              onChange={(event) => setTLS("ech", { ...ech, key: event.target.value })}
            />
          </div>
          <div className="form-field">
            <label htmlFor="ech-server-name-input" className="field-label">ECH Query Server Name</label>
            <input
              id="ech-server-name-input"
              maxLength={255}
              value={stringValue(ech.query_server_name)}
              onChange={(event) => setTLS("ech", { ...ech, query_server_name: event.target.value })}
            />
          </div>
        </div>
      )}
    </div>
  );
}

function RealityFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const reality = asRecord(settings.reality_settings);
  const update = (field: string, value: unknown) => set("reality_settings", { ...reality, [field]: value });

  return (
    <div className="reality-fields-box">
      <div className="reality-header">Reality 设置</div>
      <div className="form-row-duo">
        <div className="form-field flex-2">
          <label htmlFor="reality-sni-input" className="field-label">Reality SNI</label>
          <input
            id="reality-sni-input"
            required
            maxLength={255}
            value={stringValue(reality.server_name)}
            onChange={(event) => update("server_name", event.target.value)}
          />
        </div>
        <div className="form-field flex-1">
          <label htmlFor="reality-port-input" className="field-label">Reality 端口</label>
          <input
            id="reality-port-input"
            type="number"
            min="1"
            max="65535"
            value={Number(reality.server_port ?? 443)}
            onChange={(event) => update("server_port", Number(event.target.value))}
          />
        </div>
      </div>

      <div className="form-row-duo">
        <div className="form-field flex-1">
          <label htmlFor="reality-pub-key" className="field-label">Reality 公钥</label>
          <input
            id="reality-pub-key"
            required
            maxLength={4096}
            value={stringValue(reality.public_key)}
            onChange={(event) => update("public_key", event.target.value)}
          />
        </div>
        <div className="form-field flex-1">
          <label htmlFor="reality-priv-key" className="field-label">Reality 私钥</label>
          <input
            id="reality-priv-key"
            required
            maxLength={4096}
            value={stringValue(reality.private_key)}
            onChange={(event) => update("private_key", event.target.value)}
          />
        </div>
      </div>

      <div className="form-row-duo">
        <div className="form-field flex-1">
          <label htmlFor="reality-short-id" className="field-label">Reality Short ID</label>
          <input
            id="reality-short-id"
            maxLength={64}
            value={stringValue(reality.short_id)}
            onChange={(event) => update("short_id", event.target.value)}
          />
        </div>
        <div className="switch-inline-control flex-1" style={{ alignSelf: "center", marginTop: "14px" }}>
          <label htmlFor="reality-insecure-toggle" className="field-label">Reality 允许不安全连接</label>
          <input
            id="reality-insecure-toggle"
            type="checkbox"
            className="switch-input"
            checked={Boolean(reality.allow_insecure)}
            onChange={(event) => update("allow_insecure", event.target.checked)}
          />
        </div>
      </div>
    </div>
  );
}

function MultiplexFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const multiplex = asRecord(settings.multiplex);
  const update = (field: string, value: unknown) => set("multiplex", { ...multiplex, [field]: value });

  return (
    <div className="multiplex-fields-box">
      <div className="dynamic-rate-toggle-box">
        <div className="dynamic-rate-left">
          <div className="dynamic-rate-title-row">
            <label htmlFor="mux-toggle" className="field-label">多路复用</label>
            <input
              id="mux-toggle"
              type="checkbox"
              className="switch-input"
              checked={Boolean(multiplex.enabled)}
              onChange={(event) => update("enabled", event.target.checked)}
            />
          </div>
          <span className="dynamic-rate-desc">复用 TCP 连接以降低握手延迟</span>
        </div>
      </div>

      {Boolean(multiplex.enabled) && (
        <div className="multiplex-expanded-fields">
          <div className="form-row-duo">
            <div className="form-field flex-1">
              <label htmlFor="mux-protocol-select" className="field-label">复用协议</label>
              <select
                id="mux-protocol-select"
                value={stringValue(multiplex.protocol, "smux")}
                onChange={(event) => update("protocol", event.target.value)}
              >
                <option value="smux">smux</option>
                <option value="yamux">yamux</option>
                <option value="h2mux">h2mux</option>
              </select>
            </div>
            <div className="form-field flex-1">
              <label htmlFor="mux-max-conns" className="field-label">最大连接数</label>
              <input
                id="mux-max-conns"
                type="number"
                min="1"
                max="65535"
                value={Number(multiplex.max_connections ?? 4)}
                onChange={(event) => update("max_connections", Number(event.target.value))}
              />
            </div>
          </div>

          <div className="dynamic-rate-toggle-box" style={{ marginTop: "8px" }}>
            <div className="dynamic-rate-left">
              <div className="dynamic-rate-title-row">
                <label htmlFor="mux-padding-toggle" className="field-label">复用填充</label>
                <input
                  id="mux-padding-toggle"
                  type="checkbox"
                  className="switch-input"
                  checked={Boolean(multiplex.padding)}
                  onChange={(event) => update("padding", event.target.checked)}
                />
              </div>
              <span className="dynamic-rate-desc">添加随机填充以防御特征分析</span>
            </div>
          </div>

          <div className="dynamic-rate-toggle-box" style={{ marginTop: "8px" }}>
            <div className="dynamic-rate-left">
              <div className="dynamic-rate-title-row">
                <label htmlFor="mux-brutal-toggle" className="field-label">Brutal 加速</label>
                <input
                  id="mux-brutal-toggle"
                  type="checkbox"
                  className="switch-input"
                  checked={Boolean(asRecord(multiplex.brutal).enabled)}
                  onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), enabled: event.target.checked })}
                />
              </div>
              <span className="dynamic-rate-desc">TCP 暴力拥塞控制加速</span>
            </div>
          </div>

          {Boolean(asRecord(multiplex.brutal).enabled) && (
            <div className="form-row-duo" style={{ marginTop: "8px" }}>
              <div className="form-field flex-1">
                <label htmlFor="mux-brutal-up" className="field-label">Brutal 上行 (Mbps)</label>
                <input
                  id="mux-brutal-up"
                  type="number"
                  min="1"
                  max="1000000"
                  value={Number(asRecord(multiplex.brutal).up_mbps ?? 100)}
                  onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), up_mbps: Number(event.target.value) })}
                />
              </div>
              <div className="form-field flex-1">
                <label htmlFor="mux-brutal-down" className="field-label">Brutal 下行 (Mbps)</label>
                <input
                  id="mux-brutal-down"
                  type="number"
                  min="1"
                  max="1000000"
                  value={Number(asRecord(multiplex.brutal).down_mbps ?? 100)}
                  onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), down_mbps: Number(event.target.value) })}
                />
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
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

export const defaultAnyTLSPaddingScheme = [
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
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : "请求失败"; }
