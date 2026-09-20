import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";

import { Drawer, Modal } from "../../components/Overlay";
import type {
  AdminNode, AdminNodeDefinition, AdminNodeDefinitionInput, AdminNodeParentOption, Machine, RoutingRule, ServerGroup
} from "../../lib/api";
import "./node-definition.css";
import { NodeGroupCreator } from "./NodeGroupCreator";

export interface NodeDefinitionAPI {
  createServerGroup?: (name: string) => Promise<ServerGroup>;
  generateNodeECH?: (publicName: string) => Promise<{key: string; config: string}>;
  listAdminNodeParentOptions: (query: { type: string; q?: string; include_id?: number; exclude_id?: number }) => Promise<{ items: AdminNodeParentOption[]; has_more: boolean }>;
  getAdminNodeDefinition: (nodeID: number) => Promise<AdminNodeDefinition>;
  createAdminNodeDefinition: (input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
  replaceAdminNodeDefinition: (nodeID: number, input: AdminNodeDefinitionInput) => Promise<AdminNodeDefinition>;
}

interface Props {
  api: NodeDefinitionAPI;
  initialMachineID?: number;
  node: AdminNode | null;
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

export function NodeDefinitionModal({ api, node, machines, groups, routes, onClose, onSaved, initialMachineID }: Props) {
  const [addedGroups, setAddedGroups] = useState<ServerGroup[]>([]);
  const availableGroups = [...groups, ...addedGroups.filter(group => !groups.some(existing => existing.id === group.id))];
  const [rawDefinition, setRawDefinition] = useState<AdminNodeDefinition | null>(null);
  const [input, setInput] = useState<AdminNodeDefinitionInput>(() => newNodeInput(initialMachineID));
  const [loading, setLoading] = useState(node !== null);
  const [loadError, setLoadError] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [tagInput, setTagInput] = useState("");
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
  const [advanced, setAdvanced] = useState<{ input: AdminNodeDefinitionInput; outbounds: string; routes: string; certificate: string } | null>(null);
  const [transportEditing, setTransportEditing] = useState(false);
  const [advancedTab, setAdvancedTab] = useState("TLS");
  const cancelAdvanced = () => {
    if (advanced) { setInput(advanced.input); setCustomOutboundsText(advanced.outbounds); setCustomRoutesText(advanced.routes); setCertificateText(advanced.certificate); }
    setAdvanced(null); setError("");
  };
  const title = node === null ? "新建节点" : "编辑节点";

  const [loadAttempt, setLoadAttempt] = useState(0);
  useEffect(() => {
    if (node === null) return () => {};
    let live = true;
    api.getAdminNodeDefinition(node.id)
      .then((detail) => {
        if (!live) return;
        setRawDefinition(detail);
        const next = definitionInput(detail);
        setInput(next);
        setTransferGiB(String(next.transfer_enable / (1024 ** 3)));
        setNetworkSettingsText(formatJSON(asRecord(next.protocol_settings.network_settings)));
        setCustomOutboundsText(formatJSON(next.custom_outbounds));
        setCustomRoutesText(formatJSON(next.custom_routes));
        setCertificateText(formatJSON(next.certificate_config));
        setLoading(false);
      })
      .catch((cause: unknown) => {
        if (!live) return;
        setLoadError(errorMessage(cause));
        setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [api, node, loadAttempt]);

  useEffect(() => {
    if (loading || !input.type) return;
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
    if (!type) {
      setInput((current) => ({ ...current, type: "", protocol_settings: {} }));
      return;
    }
    setInput((current) => ({
      ...current,
      type,
      parent_id: null,
      protocol_settings: defaultProtocolSettings(type)
    }));
    setParentQuery("");
    setParentOptions([]);
    setParentOptionsHaveMore(false);
    setParentOptionsError("");
    setNetworkSettingsText("{}");
  };

  const handleClose = () => {
    if (saving) return;
    onClose();
  };

  const addTag = (text: string) => {
    const clean = text.trim();
    if (!clean) return;
    const parts = clean.split(/[,，]/).map((t) => t.trim()).filter(Boolean);
    setInput((current) => {
      const nextTags = [...current.tags];
      for (const part of parts) {
        if (!nextTags.includes(part)) {
          nextTags.push(part);
        }
      }
      return { ...current, tags: nextTags };
    });
    setTagInput("");
  };

  const removeTag = (tagToRemove: string) => {
    setInput((current) => ({
      ...current,
      tags: current.tags.filter((t) => t !== tagToRemove)
    }));
  };

  const handleTagKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter" || event.key === ",") {
      event.preventDefault();
      addTag(tagInput);
    } else if (event.key === "Backspace" && tagInput === "" && input.tags.length > 0) {
      removeTag(input.tags[input.tags.length - 1]!);
    }
  };

  const updateCertificate = (certificate: Record<string, unknown>) => {
    setInput((current) => ({ ...current, certificate_config: certificate }));
    setCertificateText(formatJSON(certificate));
  };

  const updateRateRange = (index: number, field: "start" | "end" | "rate", value: string | number) => {
    setInput((current) => ({
      ...current,
      rate_time_ranges: current.rate_time_ranges.map((range, position) =>
        position === index ? { ...range, [field]: value } : range
      )
    }));
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving) return;

    const effectiveType = input.type;
    if (!effectiveType) {
      setError("请先选择协议类型");
      return;
    }

    setSaving(true);
    setError("");
    try {
      const networkSettings = supportsNetwork(effectiveType)
        ? parseJSONObject(networkSettingsText, "传输协议设置")
        : {};
      const customOutbounds = parseJSONArray(customOutboundsText, "自定义出站");
      const customRoutes = parseJSONArray(customRoutesText, "自定义路由");
      const certificate = parseJSONObject(certificateText, "证书配置");
      const parsedTransfer = Number(transferGiB);
      if (!Number.isFinite(parsedTransfer) || parsedTransfer < 0 || !Number.isSafeInteger(Math.round(parsedTransfer * (1024 ** 3)))) {
        throw new Error("流量限制必须是有效的非负 GiB 数值");
      }

      const payload: AdminNodeDefinitionInput = {
        ...input,
        type: effectiveType,
        external_code: emptyToNull(input.external_code),
        tags: input.tags,
        transfer_enable: Math.round(parsedTransfer * (1024 ** 3)),
        protocol_settings: {
          ...input.protocol_settings,
          ...(supportsNetwork(effectiveType) ? { network_settings: networkSettings } : {})
        },
        custom_outbounds: customOutbounds,
        custom_routes: customRoutes,
        certificate_config: certificate
      };

      if (rawDefinition?.revision !== undefined) {
        payload.revision = rawDefinition.revision;
      }

      if (node === null) {
        await api.createAdminNodeDefinition(payload);
      } else {
        await api.replaceAdminNodeDefinition(node.id, payload);
      }
      onSaved();
    } catch (cause) {
      setError(errorMessage(cause));
      setSaving(false);
    }
  };

  const content = (
    <>
      <div className="node-definition-header">
        <div className="node-header-top-row">
          <div className="node-title-group">
            <h2 className="node-modal-title">{title}</h2>
            <p className="node-modal-subtitle">管理所有节点，包括添加、删除、编辑等操作。</p>
          </div>
        <div className="node-protocol-top-bar">
          <label className="node-protocol-label">

            <select
              aria-label="协议类型"
              value={input.type}
              disabled={saving}
              onChange={(event) => changeProtocol(event.target.value)}
            >
              {node === null && <option value="">选择协议类型</option>}
              {protocols.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
        </div>
          <button
            className="node-modal-close"
            aria-label={`关闭${title}`}
            disabled={saving}
            onClick={handleClose}
            type="button"
          >
            ×
          </button>
        </div>

      </div>

      {loading ? (
        <div className="node-loading-state" aria-live="polite">
          正在加载节点定义…
        </div>
      ) : loadError !== "" ? (
        <div className="node-error-state" role="alert">
          <p className="node-error-message">加载节点定义失败：{loadError}</p>
          <button className="button secondary compact" type="button" onClick={() => { setLoading(true); setLoadError(""); setLoadAttempt(current => current + 1); }}>
            重试
          </button>
        </div>
      ) : (
        <form className="node-definition-form" onSubmit={(event) => void submit(event)}>
          <div className="node-definition-scroll">
            <div className="node-form-compact">
              <div className="node-field-pair node-name-rate-pair">
              {/* 1. 节点名称 */}
              <label className="field-label">
                <span>节点名称</span>
                <input
                  required
                  maxLength={255}
                  value={input.name}
                  onChange={(event) => setInput({ ...input, name: event.target.value })}
                  placeholder="请输入节点名称"
                />
              </label>

              {/* 2. 基础倍率 x (disabled parent chosen with 子节点倍率继承自父节点) */}
              <div className="field-block">
                <label className="field-label">
                  <span>基础倍率</span>
                  <div className="rate-input-wrap">
                    <input
                      required
                      type="number"
                      min="0.000001"
                      max="1000"
                      step="any"
                      disabled={input.parent_id !== null}
                      value={input.rate}
                      onChange={(event) => setInput({ ...input, rate: Number(event.target.value) })}
                      placeholder={input.parent_id !== null ? "子节点倍率继承自父节点" : "1"}
                    />
                    <span className="rate-suffix">x</span>
                  </div>
                </label>
                {input.parent_id !== null && (
                  <small className="field-hint parent-rate-notice">子节点倍率继承自父节点</small>
                )}
              </div>

              </div>
              {/* 3. dynamicrate switch/helper 根据时间段设置不同的倍率乘数 with conditional ranges */}
              <div className="field-block">
                <div className="switch-row-item">
                  <label className="switch-label">
                    <input
                      type="checkbox"
                      checked={input.rate_time_enabled}
                      onChange={(event) => setInput({ ...input, rate_time_enabled: event.target.checked })}
                    />
                    <span>启用动态倍率</span>
                  </label>
                  <small className="field-hint">根据时间段设置不同的倍率乘数</small>
                </div>
                {input.rate_time_enabled && (
                  <div className="rate-range-list">
                    {input.rate_time_ranges.map((range, index) => (
                      <div className="rate-range-row" key={`${index}-${range.start}`}>
                        <label className="range-sublabel">
                          <span>开始</span>
                          <input
                            type="time"
                            aria-label={`动态倍率 ${index + 1} 开始`}
                            value={range.start}
                            onChange={(event) => updateRateRange(index, "start", event.target.value)}
                          />
                        </label>
                        <label className="range-sublabel">
                          <span>结束</span>
                          <input
                            type="time"
                            aria-label={`动态倍率 ${index + 1} 结束`}
                            value={range.end}
                            onChange={(event) => updateRateRange(index, "end", event.target.value)}
                          />
                        </label>
                        <label className="range-sublabel">
                          <span>倍率</span>
                          <input
                            type="number"
                            min="0"
                            max="1000"
                            step="0.01"
                            aria-label={`动态倍率 ${index + 1} 倍率`}
                            value={range.rate}
                            onChange={(event) => updateRateRange(index, "rate", Number(event.target.value))}
                          />
                        </label>
                        <button
                          className="button compact ghost danger-text"
                          type="button"
                          onClick={() =>
                            setInput({
                              ...input,
                              rate_time_ranges: input.rate_time_ranges.filter((_, position) => position !== index)
                            })
                          }
                        >
                          移除
                        </button>
                      </div>
                    ))}
                    <button
                      className="button compact secondary add-range-btn"
                      type="button"
                      onClick={() =>
                        setInput({
                          ...input,
                          rate_time_ranges: [
                            ...input.rate_time_ranges,
                            { start: "00:00", end: "23:59", rate: 1 }
                          ]
                        })
                      }
                    >
                      添加时间段
                    </button>
                  </div>
                )}
              </div>

              <div className="node-field-pair">
              {/* 4. 流量限制(GB) */}
              <label className="field-label">
                <span>流量限制(GB)</span>
                <input
                  required
                  type="number"
                  min="0"
                  step="0.01"
                  value={transferGiB}
                  onChange={(event) => setTransferGiB(event.target.value)}
                  placeholder="0 为不限制"
                />
              </label>

              {/* 5. 自定义节点ID ((选填)) */}
              <label className="field-label">
                <span>
                  自定义节点ID <small className="muted">(选填)</small>
                </span>
                <input
                  maxLength={255}
                  value={input.external_code ?? ""}
                  onChange={(event) => setInput({ ...input, external_code: event.target.value })}
                  placeholder="选填，自定义外部编码"
                />
              </label>

              </div>
              {/* 6. 节点标签 enter-to-add chips/remove */}
              <div className="field-block">
                <label className="field-label">
                  <span>节点标签</span>
                </label>
                <div className="chip-input-container">
                  {input.tags.map((tag) => (
                    <span key={tag} className="chip tag-chip">
                      <span className="chip-text">{tag}</span>
                      <button
                        type="button"
                        className="chip-remove"
                        aria-label={`移除标签 ${tag}`}
                        onClick={() => removeTag(tag)}
                      >
                        ×
                      </button>
                    </span>
                  ))}
                  <input
                    type="text"
                    className="chip-input"
                    aria-label="节点标签"
                    placeholder={input.tags.length === 0 ? "输入标签后回车添加" : "添加标签..."}
                    value={tagInput}
                    onChange={(e) => setTagInput(e.target.value)}
                    onKeyDown={handleTagKeyDown}
                    onBlur={() => {
                      if (tagInput.trim()) addTag(tagInput);
                    }}
                  />
                </div>
              </div>

              {api.createServerGroup && <NodeGroupCreator create={name => api.createServerGroup!(name)} onCreated={group => { setAddedGroups(current => [...current, group]); setInput(current => ({...current, group_ids: [...current.group_ids, group.id]})); }} />}
              <div className="field-block">
                <label className="field-label">
                  <span>权限组</span>
                </label>
                <div className="chips-picker-wrapper">
                  <div className="chips-list">
                    {input.group_ids.length === 0 ? (
                      <span className="chips-empty-hint">请选择权限组</span>
                    ) : (
                      input.group_ids.map((id) => {
                        const group = availableGroups.find((g) => g.id === id);
                        return (
                          <span key={id} className="chip group-chip">
                            <span className="chip-text">{group?.name ?? `#${id}`}</span>
                            <button
                              type="button"
                              className="chip-remove"
                              aria-label={`移除权限组 ${group?.name ?? id}`}
                              onClick={() =>
                                setInput({ ...input, group_ids: input.group_ids.filter((gId) => gId !== id) })
                              }
                            >
                              ×
                            </button>
                          </span>
                        );
                      })
                    )}
                  </div>
                  {availableGroups.filter((g) => !input.group_ids.includes(g.id)).length > 0 && (
                    <select
                      className="chip-add-select"
                      value=""
                      aria-label="添加权限组"
                      onChange={(e) => {
                        const id = Number(e.target.value);
                        if (id && !input.group_ids.includes(id)) {
                          setInput({ ...input, group_ids: [...input.group_ids, id] });
                        }
                      }}
                    >
                      <option value="">+ 添加权限组...</option>
                      {availableGroups
                        .filter((g) => !input.group_ids.includes(g.id))
                        .map((g) => (
                          <option key={g.id} value={g.id}>
                            {g.name}
                          </option>
                        ))}
                    </select>
                  )}
                </div>
              </div>

              {/* 8. 节点地址 */}
              <label className="field-label">
                <span>节点地址</span>
                <input
                  required
                  maxLength={255}
                  value={input.host}
                  onChange={(event) => setInput({ ...input, host: event.target.value })}
                  placeholder="例如: node.example.com 或 1.2.3.4"
                />
              </label>

              <div className="node-field-pair">
              {/* 9. 连接端口 with copy-to-server-port button */}
              <label className="field-label">
                <span>连接端口</span>
                <div className="input-with-button">
                  <input
                    required
                    aria-label="连接端口"
                    inputMode="numeric"
                    pattern="[0-9]{1,5}(-[0-9]{1,5})?"
                    value={input.port}
                    onChange={(event) => setInput({ ...input, port: event.target.value })}
                    placeholder="443 或 10000-20000"
                  />
                  <button
                    className="button compact secondary copy-port-button"
                    type="button"
                    title="复制连接端口至服务端口"
                    aria-label="复制到服务端口"
                    onClick={() => {
                      const singlePort = parseInt((input.port.split("-")[0] ?? "").trim(), 10);
                      if (!isNaN(singlePort) && singlePort >= 1 && singlePort <= 65535) {
                        setInput((current) => ({ ...current, server_port: singlePort }));
                      }
                    }}
                  >
                    →
                  </button>
                </div>
              </label>

              {/* 10. 服务端口 */}
              <label className="field-label">
                <span>服务端口</span>
                <input
                  required
                  type="number"
                  min="1"
                  max="65535"
                  value={input.server_port}
                  onChange={(event) => setInput({ ...input, server_port: Number(event.target.value) })}
                  placeholder="例如: 443"
                />
              </label>

              </div>
              {!input.type && <p>请先选择协议类型</p>}
              <ProtocolFields key={input.type} onDialogChange={setTransportEditing}
                generateECH={api.generateNodeECH ? name => api.generateNodeECH!(name) : undefined}
                input={input}
                setInput={setInput}
                networkSettingsText={networkSettingsText}
                setNetworkSettingsText={setNetworkSettingsText}
              />

              {/* 12. 父级节点 searchable dropdown */}
              <div className="field-block parent-node-block">
                <label className="field-label">
                  <span>父级节点</span>
                </label>
                <div className="parent-search-row">
                  <input
                    type="text"
                    className="parent-query-input"
                    aria-label="搜索父节点"
                    maxLength={255}
                    value={parentQuery}
                    onChange={(event) => setParentQuery(event.target.value)}
                    placeholder="搜索父节点 (名称或 #ID)"
                  />
                  <select
                    className="parent-select"
                    aria-label="父级节点"
                    value={input.parent_id ?? ""}
                    onChange={(event) =>
                      setInput({
                        ...input,
                        parent_id: event.target.value === "" ? null : Number(event.target.value)
                      })
                    }
                  >
                    <option value="">无父节点</option>
                    {parentOptions.map((candidate) => (
                      <option key={candidate.id} value={candidate.id}>
                        {candidate.name} (#{candidate.id})
                      </option>
                    ))}
                  </select>
                </div>
                <small className="field-hint" aria-live="polite">
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
              </div>

              {/* 13. 路由组 chips */}
              <div className="field-block">
                <label className="field-label">
                  <span>路由组</span>
                </label>
                <div className="chips-picker-wrapper">
                  <div className="chips-list">
                    {input.route_ids.length === 0 ? (
                      <span className="chips-empty-hint">无路由规则</span>
                    ) : (
                      input.route_ids.map((id) => {
                        const route = routes.find((r) => r.id === id);
                        return (
                          <span key={id} className="chip route-chip">
                            <span className="chip-text">{route?.remarks ?? `#${id}`}</span>
                            <button
                              type="button"
                              className="chip-remove"
                              aria-label={`移除路由规则 ${route?.remarks ?? id}`}
                              onClick={() =>
                                setInput({ ...input, route_ids: input.route_ids.filter((rId) => rId !== id) })
                              }
                            >
                              ×
                            </button>
                          </span>
                        );
                      })
                    )}
                  </div>
                  {routes.filter((r) => !input.route_ids.includes(r.id)).length > 0 && (
                    <select
                      className="chip-add-select"
                      value=""
                      aria-label="添加路由规则"
                      onChange={(e) => {
                        const id = Number(e.target.value);
                        if (id && !input.route_ids.includes(id)) {
                          setInput({ ...input, route_ids: [...input.route_ids, id] });
                        }
                      }}
                    >
                      <option value="">+ 添加路由规则...</option>
                      {routes
                        .filter((r) => !input.route_ids.includes(r.id))
                        .map((r) => (
                          <option key={r.id} value={r.id}>
                            {r.remarks}
                          </option>
                        ))}
                    </select>
                  )}
                </div>
              </div>

              {/* 14. 绑定服务器 (独立部署 or name SID:id) and enabled switch for selected machine */}
              <div className="field-block machine-binding-block">
                <label className="field-label">
                  <span>绑定服务器</span>
                  <select
                    value={input.machine_id ?? ""}
                    onChange={(event) =>
                      setInput({
                        ...input,
                        machine_id: event.target.value === "" ? null : Number(event.target.value)
                      })
                    }
                  >
                    <option value="">独立部署</option>
                    {machines.map((machine) => (
                      <option key={machine.id} value={machine.id}>
                        {machine.name} SID:{machine.id}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="switch-row-item machine-enabled-switch">
                  <label className="switch-label">
                    <input
                      type="checkbox"
                      checked={input.enabled}
                      onChange={(event) => setInput({ ...input, enabled: event.target.checked })}
                    />
                    <span>启用运行</span>
                  </label>
                  <small className="field-hint">控制此节点是否在绑定的服务器上启用</small>
                </div>
              </div>

            </div>
          </div>

          {/* Pinned Footer with 取消/提交 */}
          <div className="node-footer">
            {error !== "" && (
              <div className="alert error node-submit-error" role="alert">
                {error}
              </div>
            )}
            <div className="node-footer-actions">
              {input.type && <button type="button" className="button secondary" disabled={saving} onClick={() => { setAdvanced({input, outbounds: customOutboundsText, routes: customRoutesText, certificate: certificateText}); setAdvancedTab("TLS"); }}>高级设置</button>}
              <button
                className="button ghost"
                type="button"
                disabled={saving}
                onClick={handleClose}
              >
                取消
              </button>
              <button
                className="button primary"
                type="submit"
                disabled={saving || (node === null && !input.type)}
              >
                {saving ? "正在保存…" : "提交"}
              </button>
            </div>
          </div>
        </form>
      )}
      {advanced && <Modal title="高级协议配置" className="node-definition-modal node-advanced-modal" onClose={cancelAdvanced}>
        <div className="node-definition-header">
          <div className="node-header-top-row">
            <div className="node-title-group">
              <h2 className="node-modal-title node-advanced-title">高级协议配置</h2>
            </div>
          </div>
          <div role="tablist" aria-label="高级协议设置" className="node-advanced-tabs">
            {["TLS", "多路复用", "自定义 Outbounds", "自定义 Routes"].map(tab => (
              <button key={tab} type="button" role="tab" aria-selected={advancedTab === tab} onClick={() => setAdvancedTab(tab)}>{tab}</button>
            ))}
          </div>
        </div>
        <form className="node-definition-form" onSubmit={event => { event.preventDefault(); event.stopPropagation(); try { parseJSONArray(customOutboundsText, "自定义出站"); parseJSONArray(customRoutesText, "自定义路由"); updateCertificate(parseJSONObject(certificateText, "证书配置")); setAdvanced(null); setError(""); } catch(cause) { setError(errorMessage(cause)); } }}>
          <div className="node-definition-scroll node-form-compact node-advanced-scroll" role="tabpanel" aria-label={advancedTab}>
            {advancedTab === "TLS" && <>
              <CertificateFields value={asRecord(input.certificate_config)} onChange={updateCertificate}/>
              <details className="cert-expert-details">
                <summary>其他设置</summary>
                <div className="cert-expert-inner">
                  <label className="field-label"><span>监听地址</span><input required value={input.listen_address} onChange={e => setInput({...input, listen_address:e.target.value})}/></label>
                  <label className="field-label"><span>排序</span><input type="number" min="0" value={input.sort} onChange={e => setInput({...input,sort:Number(e.target.value)})}/></label>
                  <label className="field-label switch-label"><input type="checkbox" checked={input.show} onChange={e => setInput({...input,show:e.target.checked})}/><span>用户端显示</span></label>
                  <label className="field-label"><span>证书配置 (JSON 对象)</span><textarea spellCheck={false} value={certificateText} onChange={e => setCertificateText(e.target.value)}/></label>
                </div>
              </details>
            </>}
            {advancedTab === "多路复用" && (
              ["vmess","trojan","vless","mieru"].includes(input.type)
                ? <MultiplexFields settings={input.protocol_settings} set={(key,value) => setInput(current => ({...current,protocol_settings:{...current.protocol_settings,[key]:value}}))}/>
                : <p className="node-advanced-unsupported">当前协议不支持多路复用。</p>
            )}
            {advancedTab === "自定义 Outbounds" && (
              <label className="field-label">
                <span>自定义出站 (JSON 数组)</span>
                <textarea className="node-advanced-json-editor" rows={14} spellCheck={false} value={customOutboundsText} onChange={e=>setCustomOutboundsText(e.target.value)}/>
              </label>
            )}
            {advancedTab === "自定义 Routes" && (
              <label className="field-label">
                <span>自定义路由 (JSON 数组)</span>
                <textarea className="node-advanced-json-editor" rows={14} spellCheck={false} value={customRoutesText} onChange={e=>setCustomRoutesText(e.target.value)}/>
              </label>
            )}
          </div>
          <div className="node-footer">
            {error && <div role="alert" className="alert error">{error}</div>}
            <div className="node-footer-actions">
              <button type="button" className="button ghost" onClick={cancelAdvanced}>取消</button>
              <button type="submit" className="button primary">Save</button>
            </div>
          </div>
        </form>
      </Modal>}
    </>
  );

  if (node === null) {
    return (
      <Modal title={title} className="node-definition-modal" suspended={advanced !== null || transportEditing} onClose={handleClose}>
        {content}
      </Modal>
    );
  }

  return (
    <Drawer title={title} className="node-definition-drawer" suspended={advanced !== null || transportEditing} onClose={handleClose}>
      {content}
    </Drawer>
  );
}

function CertificateFields({ value, onChange }: { value: Record<string, unknown>; onChange: (value: Record<string, unknown>) => void }) {
  const mode = stringValue(value.cert_mode, "none");
  const set = (field: string, fieldValue: unknown) => onChange({ ...value, [field]: fieldValue });
  const [dnsEnvText, setDNSEnvText] = useState(() => formatDNSEnv(asRecord(value.dns_env)));
  return (
    <div className="certificate-fields-stack">
      <label className="field-label">
        <span>证书模式</span>
        <select value={mode} onChange={(event) => set("cert_mode", event.target.value)}>
          <option value="none">none</option>
          <option value="http">http-01 (ACME)</option>
          <option value="dns">dns-01 (ACME)</option>
          <option value="self">self-signed</option>
          <option value="content">content (Cert Push)</option>
        </select>
      </label>
      {mode !== "none" && (
        <label className="field-label">
          <span>证书域名</span>
          <input
            maxLength={4096}
            value={stringValue(value.domain)}
            onChange={(event) => set("domain", event.target.value)}
            placeholder="example.com"
          />
        </label>
      )}
      {["http", "dns"].includes(mode) && (
        <label className="field-label">
          <span>ACME 邮箱</span>
          <input
            type="email"
            maxLength={4096}
            value={stringValue(value.email)}
            onChange={(event) => set("email", event.target.value)}
            placeholder="admin@example.com"
          />
        </label>
      )}
      {mode === "http" && (
        <label className="field-label">
          <span>HTTP 挑战端口</span>
          <input
            type="number"
            min="1"
            max="65535"
            value={Number(value.http_port ?? 80)}
            onChange={(event) => set("http_port", Number(event.target.value))}
          />
        </label>
      )}
      {mode === "dns" && (
        <>
          <label className="field-label">
            <span>DNS Provider</span>
            <input
              maxLength={4096}
              value={stringValue(value.dns_provider)}
              onChange={(event) => set("dns_provider", event.target.value)}
              placeholder="cloudflare / alidns / dnspod"
            />
          </label>
          <label className="field-label">
            <span>DNS 环境变量</span>
            <textarea
              rows={4}
              spellCheck={false}
              value={dnsEnvText}
              onChange={(event) => setDNSEnvText(event.target.value)}
              onBlur={() => set("dns_env", parseDNSEnv(dnsEnvText))}
              placeholder={"CF_API_TOKEN=xxxxxx\nALIDNS_ACCESS_KEY_ID=xxxx"}
            />
          </label>
        </>
      )}
      {mode === "content" && (
        <>
          <label className="field-label">
            <span>证书内容</span>
            <textarea
              rows={5}
              spellCheck={false}
              value={stringValue(value.cert_content)}
              onChange={(event) => set("cert_content", event.target.value)}
              placeholder={"-----BEGIN CERTIFICATE-----\n..."}
            />
          </label>
          <label className="field-label">
            <span>私钥内容</span>
            <textarea
              rows={5}
              spellCheck={false}
              value={stringValue(value.key_content)}
              onChange={(event) => set("key_content", event.target.value)}
              placeholder={"-----BEGIN PRIVATE KEY-----\n..."}
            />
          </label>
        </>
      )}
    </div>
  );
}

function ProtocolFields({
  onDialogChange,
  generateECH,
  input,
  setInput,
  networkSettingsText,
  setNetworkSettingsText
}: {
  onDialogChange: (open: boolean) => void;
  generateECH?: (name: string) => Promise<{key: string; config: string}>;
  input: AdminNodeDefinitionInput;
  setInput: React.Dispatch<React.SetStateAction<AdminNodeDefinitionInput>>;
  networkSettingsText: string;
  setNetworkSettingsText: (value: string) => void;
}) {
  const [showNetworkSettings, setShowNetworkSettings] = useState(false);
  const [networkDraft, setNetworkDraft] = useState("");
  const [networkError, setNetworkError] = useState("");
  const closeNetwork = () => { setShowNetworkSettings(false); onDialogChange(false); };

  const settings = input.protocol_settings;
  const set = (key: string, value: unknown) =>
    setInput((current) => ({
      ...current,
      protocol_settings: { ...current.protocol_settings, [key]: value }
    }));
  const setNested = (key: string, field: string, value: unknown) =>
    set(key, { ...asRecord(settings[key]), [field]: value });
  const tlsKey = ["hysteria", "tuic", "anytls"].includes(input.type) ? "tls" : "tls_settings";
  const tls = asRecord(settings[tlsKey]);
  const securityOptions =
    input.type === "vmess"
      ? [[0, "None"], [1, "TLS"]]
      : input.type === "trojan"
      ? [[1, "TLS"], [2, "Reality"]]
      : [[0, "None"], [1, "TLS"], [2, "Reality"]];

  return (
    <div className="protocol-fields-stack">
      {input.type === "shadowsocks" && (
        <>
          <label className="field-label">
            <span>加密算法</span>
            <input
              list="shadowsocks-ciphers"
              value={stringValue(settings.cipher)}
              onChange={(event) => set("cipher", event.target.value)}
            />
            <datalist id="shadowsocks-ciphers">
              {[
                "aes-128-gcm", "aes-192-gcm", "aes-256-gcm", "chacha20-ietf-poly1305",
                "2022-blake3-aes-128-gcm", "2022-blake3-aes-256-gcm", "2022-blake3-chacha20-poly1305"
              ].map((cipher) => (
                <option key={cipher} value={cipher} />
              ))}
            </datalist>
          </label>
          <label className="field-label">
            <span>插件</span>
            <select
              value={stringValue(settings.plugin)}
              onChange={(event) => set("plugin", event.target.value)}
            >
              <option value="">None</option>
              <option value="obfs">Simple Obfs</option>
              <option value="v2ray-plugin">V2Ray Plugin</option>
              <option value="gost-plugin">Gost Plugin</option>
              <option value="shadow-tls">Shadow TLS</option>
              <option value="restls">ResTLS</option>
              <option value="kcptun">KCPTun</option>
            </select>
          </label>
          <label className="field-label">
            <span>插件参数</span>
            <input
              maxLength={4096}
              value={stringValue(settings.plugin_opts)}
              onChange={(event) => set("plugin_opts", event.target.value)}
            />
          </label>
        </>
      )}

      {["vmess", "trojan", "vless"].includes(input.type) && (
        <>
          <label className="field-label">
            <span>安全性</span>
            <select
              value={Number(settings.tls ?? 0)}
              onChange={(event) => set("tls", Number(event.target.value))}
            >
              {securityOptions.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>

          <div className="network-protocol-header-row">
            <label className="field-label network-select-label">
              <span>传输协议</span>
              <select
                value={stringValue(settings.network, "tcp")}
                onChange={(event) => set("network", event.target.value)}
              >
                {[...networks, ...(input.type === "vless" ? [["kcp", "mKCP"]] as const : [])].map(
                  ([value, label]) => (
                    <option key={value} value={value}>
                      {label}
                    </option>
                  )
                )}
              </select>
            </label>
            <button
              type="button"
              className={`button compact ${showNetworkSettings ? "primary" : "secondary"} network-settings-toggle-btn`}
              onClick={() => {setNetworkDraft(networkSettingsText);setNetworkError("");setShowNetworkSettings(true);onDialogChange(true);}}
            >
              {showNetworkSettings ? "收起协议配置" : "编辑协议"}
            </button>
          </div>

          {showNetworkSettings && <Modal title="编辑传输协议" className="node-definition-modal node-advanced-modal" onClose={closeNetwork}>
            <div className="node-definition-header"><h2>编辑传输协议</h2></div>
            <div className="node-definition-scroll node-form-compact">
              <label>传输协议设置 (JSON)<textarea rows={14} value={networkDraft} onChange={event => setNetworkDraft(event.target.value)}/></label>
              <div className="network-template-actions">{(networkTemplates[stringValue(settings.network,"tcp")] ?? []).map(template => <button className="button compact secondary" type="button" key={template.label} onClick={()=>setNetworkDraft(formatJSON(template.value))}>套用 {template.label} 模板</button>)}</div>
            </div>
            <div className="node-footer">{networkError && <div role="alert" className="alert error">{networkError}</div>}<div className="node-footer-actions">
              <button className="button ghost" type="button" onClick={closeNetwork}>取消</button>
              <button className="button primary" type="button" onClick={() => {try {parseJSONObject(networkDraft,"传输协议设置");setNetworkSettingsText(networkDraft);closeNetwork();}catch(cause){setNetworkError(errorMessage(cause));}}}>保存</button>
            </div></div>
          </Modal>}

          {input.type === "vless" && (
            <label className="field-label">
              <span>流控</span>
              <select
                value={stringValue(settings.flow)}
                onChange={(event) => set("flow", event.target.value)}
              >
                <option value="">None</option>
                <option value="xtls-rprx-direct">xtls-rprx-direct</option>
                <option value="xtls-rprx-splice">xtls-rprx-splice</option>
                <option value="xtls-rprx-vision">xtls-rprx-vision</option>
              </select>
            </label>
          )}

          {Number(settings.tls) === 1 && (
            <TLSFields generateECH={generateECH} tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
          )}

          {Number(settings.tls) > 0 && (
            <div className="utls-section">
              <div className="switch-row-item">
                <label className="switch-label">
                  <input
                    type="checkbox"
                    checked={Boolean(asRecord(settings.utls).enabled)}
                    onChange={(event) =>
                      set("utls", { ...asRecord(settings.utls), enabled: event.target.checked })
                    }
                  />
                  <span>uTLS</span>
                </label>
              </div>
              {Boolean(asRecord(settings.utls).enabled) && (
                <label className="field-label">
                  <span>客户端指纹 (uTLS)</span>
                  <select
                    value={stringValue(asRecord(settings.utls).fingerprint, "chrome")}
                    onChange={(event) =>
                      set("utls", { ...asRecord(settings.utls), fingerprint: event.target.value })
                    }
                  >
                    {["chrome", "firefox", "safari", "ios", "edge", "random"].map((fingerprint) => (
                      <option key={fingerprint} value={fingerprint}>
                        {fingerprint}
                      </option>
                    ))}
                  </select>
                </label>
              )}
            </div>
          )}



          {Number(settings.tls) === 2 && <RealityFields settings={settings} set={set} />}

          {input.type === "vless" && (
            <div className="vless-encryption-section">
              <div className="switch-row-item">
                <label className="switch-label">
                  <input
                    type="checkbox"
                    checked={Boolean(asRecord(settings.encryption).enabled)}
                    onChange={(event) =>
                      set("encryption", { ...asRecord(settings.encryption), enabled: event.target.checked })}
                  />
                  <span>VLESS Encryption</span>
                </label>
              </div>
              {Boolean(asRecord(settings.encryption).enabled) && (
                <div className="encryption-keys-stack">
                  <label className="field-label">
                    <span>客户端公钥</span>
                    <input
                      maxLength={8192}
                      value={stringValue(asRecord(settings.encryption).encryption)}
                      onChange={(event) =>
                        set("encryption", { ...asRecord(settings.encryption), encryption: event.target.value })}
                      placeholder="客户端公钥"
                    />
                  </label>
                  <label className="field-label">
                    <span>服务端私钥</span>
                    <input
                      maxLength={8192}
                      value={stringValue(asRecord(settings.encryption).decryption)}
                      onChange={(event) =>
                        set("encryption", { ...asRecord(settings.encryption), decryption: event.target.value })}
                      placeholder="服务端私钥"
                    />
                  </label>
                </div>
              )}
            </div>
          )}
        </>
      )}

      {input.type === "hysteria" && (
        <>
          <label className="field-label">
            <span>版本</span>
            <select
              value={Number(settings.version ?? 2)}
              onChange={(event) => set("version", Number(event.target.value))}
            >
              <option value={1}>V1</option>
              <option value={2}>V2</option>
            </select>
          </label>

          {Number(settings.version ?? 2) === 1 && (
            <label className="field-label">
              <span>ALPN</span>
              <select
                value={stringValue(settings.alpn, "h2")}
                onChange={(event) => set("alpn", event.target.value)}
              >
                {["hysteria", "http/1.1", "h2", "h3"].map((value) => (
                  <option key={value} value={value}>
                    {value}
                  </option>
                ))}
              </select>
            </label>
          )}

          <div className="switch-row-item">
            <label className="switch-label">
              <input
                type="checkbox"
                checked={Boolean(asRecord(settings.obfs).open)}
                onChange={(event) =>
                  set("obfs", { ...asRecord(settings.obfs), open: event.target.checked })}
              />
              <span>混淆</span>
            </label>
          </div>

          {Boolean(asRecord(settings.obfs).open) && (
            <label className="field-label">
              <span>混淆密码</span>
              <input
                maxLength={4096}
                value={stringValue(asRecord(settings.obfs).password)}
                onChange={(event) =>
                  set("obfs", { ...asRecord(settings.obfs), password: event.target.value })}
                placeholder="混淆密码"
              />
            </label>
          )}

          <label className="field-label">
            <span>上行带宽 (Mbps)</span>
            <input
              type="number"
              min="0"
              max="1000000"
              value={Number(asRecord(settings.bandwidth).up ?? 0)}
              onChange={(event) =>
                set("bandwidth", { ...asRecord(settings.bandwidth), up: Number(event.target.value) })}
            />
          </label>

          <label className="field-label">
            <span>下行带宽 (Mbps)</span>
            <input
              type="number"
              min="0"
              max="1000000"
              value={Number(asRecord(settings.bandwidth).down ?? 0)}
              onChange={(event) =>
                set("bandwidth", { ...asRecord(settings.bandwidth), down: Number(event.target.value) })}
            />
          </label>

          <label className="field-label">
            <span>端口跳跃间隔 (秒)</span>
            <input
              type="number"
              min="1"
              max="86400"
              placeholder="例如: 30"
              value={settings.hop_interval == null ? "" : Number(settings.hop_interval)}
              onChange={(event) =>
                set("hop_interval", event.target.value === "" ? undefined : Number(event.target.value))}
            />
          </label>

          <TLSFields generateECH={generateECH} tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </>
      )}

      {input.type === "tuic" && (
        <>
          <label className="field-label">
            <span>版本</span>
            <select
              value={Number(settings.version ?? 5)}
              onChange={(event) => set("version", Number(event.target.value))}
            >
              <option value={5}>V5</option>
              <option value={4}>V4</option>
            </select>
          </label>

          <label className="field-label">
            <span>拥塞控制</span>
            <select
              value={stringValue(settings.congestion_control, "bbr")}
              onChange={(event) => set("congestion_control", event.target.value)}
            >
              <option value="bbr">BBR</option>
              <option value="cubic">CUBIC</option>
              <option value="new_reno">NEW_RENO</option>
            </select>
          </label>

          <label className="field-label">
            <span>ALPN</span>
            <select
              multiple
              aria-label="ALPN"
              value={asStringArray(settings.alpn)}
              onChange={(event) =>
                set("alpn", Array.from(event.currentTarget.selectedOptions, (option) => option.value))}
            >
              <option value="h3">h3</option>
              <option value="h2">HTTP/2</option>
              <option value="http/1.1">HTTP/1.1</option>
            </select>
          </label>

          <label className="field-label">
            <span>UDP Relay</span>
            <select
              value={stringValue(settings.udp_relay_mode, "native")}
              onChange={(event) => set("udp_relay_mode", event.target.value)}
            >
              <option value="native">Native</option>
              <option value="quic">QUIC</option>
            </select>
          </label>

          <TLSFields generateECH={generateECH} tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </>
      )}

      {["socks", "naive", "http"].includes(input.type) && (
        <>
          <label className="field-label">
            <span>TLS</span>
            <select
              value={Number(settings.tls ?? 0)}
              onChange={(event) => set("tls", Number(event.target.value))}
            >
              <option value={0}>不支持</option>
              <option value={1}>支持</option>
            </select>
          </label>
          <TLSFields generateECH={generateECH} tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </>
      )}

      {input.type === "mieru" && (
        <>
          <label className="field-label">
            <span>传输协议</span>
            <select
              value={stringValue(settings.transport, "TCP")}
              onChange={(event) => set("transport", event.target.value)}
            >
              <option value="TCP">TCP</option>
              <option value="UDP">UDP</option>
            </select>
          </label>
          <label className="field-label">
            <span>Traffic Pattern</span>
            <input
              maxLength={4096}
              value={stringValue(settings.traffic_pattern)}
              onChange={(event) => set("traffic_pattern", event.target.value)}
            />
          </label>

        </>
      )}

      {input.type === "anytls" && (
        <>
          <label className="field-label">
            <span>ALPN</span>
            <input
              maxLength={64}
              value={stringValue(settings.alpn)}
              onChange={(event) => set("alpn", event.target.value)}
            />
          </label>
          <label className="field-label">
            <span>Padding Scheme</span>
            <textarea
              rows={5}
              value={asStringArray(settings.padding_scheme).join("\n")}
              onChange={(event) =>
                set("padding_scheme", event.target.value.split(/\r?\n/).filter(Boolean))}
            />
            <button
              className="button compact secondary"
              type="button"
              style={{ marginTop: "4px", alignSelf: "flex-start" }}
              onClick={() => set("padding_scheme", defaultAnyTLSPaddingScheme)}
            >
              使用默认方案
            </button>
          </label>
          <TLSFields generateECH={generateECH} tls={tls} setTLS={(field, value) => setNested(tlsKey, field, value)} />
        </>
      )}
    </div>
  );
}

function TLSFields({ tls, setTLS, generateECH }: { generateECH?: (name: string) => Promise<{key: string; config: string}>; tls: Record<string, unknown>; setTLS: (field: string, value: unknown) => void }) {
  const ech = asRecord(tls.ech);
  const [generating, setGenerating] = useState(false);
  const [keyError, setKeyError] = useState("");
  const tlsRef = useRef<Record<string, unknown> | null>(tls);
  useEffect(() => { tlsRef.current = tls; return () => { tlsRef.current = null; }; }, [tls]);
  const generate = async () => {
    if (!generateECH || generating) return;
    setGenerating(true); setKeyError("");
    try { const pair = await generateECH(stringValue(ech.query_server_name) || stringValue(tls.server_name)); if (tlsRef.current === tls) setTLS("ech", {...ech, ...pair}); else if (tlsRef.current) setKeyError("配置已变更，请重新生成密钥。"); }
    catch (cause) { setKeyError(errorMessage(cause)); }
    finally { setGenerating(false); }
  };
  return (
    <div className="tls-fields-group">
      <label className="field-label">
        <span>服务器名称指示(SNI)</span>
        <input
          maxLength={255}
          value={stringValue(tls.server_name)}
          onChange={(event) => setTLS("server_name", event.target.value)}
          placeholder="例如: example.com"
        />
      </label>
      <div className="switch-row-item">
        <label className="switch-label">
          <input
            type="checkbox"
            checked={Boolean(tls.allow_insecure)}
            onChange={(event) => setTLS("allow_insecure", event.target.checked)}
          />
          <span>允许不安全?</span>
        </label>
      </div>
      <div className="switch-row-item">
        <label className="switch-label">
          <input
            type="checkbox"
            checked={Boolean(ech.enabled)}
            onChange={(event) => setTLS("ech", { ...ech, enabled: event.target.checked })}
          />
          <span>ECH</span>
        </label>
      </div>
      {Boolean(ech.enabled) && (
        <div className="ech-fields-stack">
          {generateECH && <button type="button" className="button secondary compact" disabled={generating} onClick={() => void generate()}>{generating ? "正在生成…" : "自动生成 ECH 密钥对"}</button>}
          {keyError && <div role="alert" className="alert error">{keyError}</div>}
          <label className="field-label">
            <span>ECH 配置 (PEM)</span>
            <textarea
              rows={3}
              value={stringValue(ech.config)}
              onChange={(event) => setTLS("ech", { ...ech, config: event.target.value })}
              placeholder="留空则通过 DNS 查询"
            />
          </label>
          <label className="field-label">
            <span>ECH Key</span>
            <textarea
              rows={2}
              value={stringValue(ech.key)}
              onChange={(event) => setTLS("ech", { ...ech, key: event.target.value })}
              placeholder="ECH 私钥（选填）"
            />
          </label>
          <label className="field-label">
            <span>ECH 查询域名</span>
            <input
              maxLength={255}
              value={stringValue(ech.query_server_name)}
              onChange={(event) => setTLS("ech", { ...ech, query_server_name: event.target.value })}
              placeholder="例如: example.com"
            />
          </label>
        </div>
      )}
    </div>
  );
}

function RealityFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const reality = asRecord(settings.reality_settings);
  const update = (field: string, value: unknown) => set("reality_settings", { ...reality, [field]: value });
  return (
    <div className="reality-fields-stack">
      <label className="field-label">
        <span>Reality SNI</span>
        <input
          required
          maxLength={255}
          value={stringValue(reality.server_name)}
          onChange={(event) => update("server_name", event.target.value)}
        />
      </label>
      <label className="field-label">
        <span>Reality 端口</span>
        <input
          type="number"
          min="1"
          max="65535"
          value={Number(reality.server_port ?? 443)}
          onChange={(event) => update("server_port", Number(event.target.value))}
        />
      </label>
      <label className="field-label">
        <span>Reality 公钥</span>
        <input
          required
          maxLength={4096}
          value={stringValue(reality.public_key)}
          onChange={(event) => update("public_key", event.target.value)}
        />
      </label>
      <label className="field-label">
        <span>Reality 私钥</span>
        <input
          required
          maxLength={4096}
          value={stringValue(reality.private_key)}
          onChange={(event) => update("private_key", event.target.value)}
        />
      </label>
      <label className="field-label">
        <span>Reality Short ID</span>
        <input
          maxLength={64}
          value={stringValue(reality.short_id)}
          onChange={(event) => update("short_id", event.target.value)}
        />
      </label>
      <div className="switch-row-item">
        <label className="switch-label">
          <input
            type="checkbox"
            checked={Boolean(reality.allow_insecure)}
            onChange={(event) => update("allow_insecure", event.target.checked)}
          />
          <span>Reality 允许不安全连接</span>
        </label>
      </div>
    </div>
  );
}

function MultiplexFields({ settings, set }: { settings: Record<string, unknown>; set: (key: string, value: unknown) => void }) {
  const multiplex = asRecord(settings.multiplex);
  const update = (field: string, value: unknown) => set("multiplex", { ...multiplex, [field]: value });
  return (
    <div className="multiplex-fields-stack">
      <div className="switch-row-item">
        <label className="switch-label">
          <input
            type="checkbox"
            checked={Boolean(multiplex.enabled)}
            onChange={(event) => update("enabled", event.target.checked)}
          />
          <span>多路复用</span>
        </label>
      </div>
      {Boolean(multiplex.enabled) && (
        <div className="multiplex-inner-fields">
          <label className="field-label">
            <span>复用协议</span>
            <select
              value={stringValue(multiplex.protocol, "smux")}
              onChange={(event) => update("protocol", event.target.value)}
            >
              <option value="smux">smux</option>
              <option value="yamux">yamux</option>
              <option value="h2mux">h2mux</option>
            </select>
          </label>
          <label className="field-label">
            <span>最大连接数</span>
            <input
              type="number"
              min="1"
              max="65535"
              value={Number(multiplex.max_connections ?? 4)}
              onChange={(event) => update("max_connections", Number(event.target.value))}
            />
          </label>
          <div className="switch-row-item">
            <label className="switch-label">
              <input
                type="checkbox"
                checked={Boolean(multiplex.padding)}
                onChange={(event) => update("padding", event.target.checked)}
              />
              <span>复用填充</span>
            </label>
          </div>
          <div className="switch-row-item">
            <label className="switch-label">
              <input
                type="checkbox"
                checked={Boolean(asRecord(multiplex.brutal).enabled)}
                onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), enabled: event.target.checked })}
              />
              <span>Brutal 加速</span>
            </label>
          </div>
          {Boolean(asRecord(multiplex.brutal).enabled) && (
            <div className="brutal-speed-fields">
              <label className="field-label">
                <span>Brutal 上行 (Mbps)</span>
                <input
                  type="number"
                  min="1"
                  max="1000000"
                  value={Number(asRecord(multiplex.brutal).up_mbps ?? 100)}
                  onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), up_mbps: Number(event.target.value) })}
                />
              </label>
              <label className="field-label">
                <span>Brutal 下行 (Mbps)</span>
                <input
                  type="number"
                  min="1"
                  max="1000000"
                  value={Number(asRecord(multiplex.brutal).down_mbps ?? 100)}
                  onChange={(event) => update("brutal", { ...asRecord(multiplex.brutal), down_mbps: Number(event.target.value) })}
                />
              </label>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function newNodeInput(initialMachineID?: number): AdminNodeDefinitionInput {
  return {
    type: "",
    external_code: null,
    parent_id: null,
    name: "",
    rate: 1,
    tags: [],
    host: "",
    port: "443",
    server_port: 443,
    listen_address: "0.0.0.0",
    protocol_settings: {},
    show: false,
    enabled: true,
    sort: 0,
    machine_id: initialMachineID ?? null,
    group_ids: [],
    route_ids: [],
    rate_time_enabled: false,
    rate_time_ranges: [],
    custom_outbounds: [],
    custom_routes: [],
    certificate_config: { cert_mode: "none" },
    transfer_enable: 0
  };
}

function definitionInput(detail: AdminNodeDefinition): AdminNodeDefinitionInput {
  return {
    revision: detail.revision,
    type: detail.type,
    external_code: detail.external_code || null,
    parent_id: detail.parent_id,
    name: detail.name,
    rate: detail.rate,
    tags: detail.tags ?? [],
    host: detail.host,
    port: detail.port,
    server_port: detail.server_port,
    listen_address: detail.listen_address,
    protocol_settings: detail.protocol_settings ?? {},
    show: detail.show,
    enabled: detail.enabled,
    sort: detail.sort,
    machine_id: detail.machine_id,
    group_ids: detail.group_ids ?? [],
    route_ids: detail.route_ids ?? [],
    rate_time_enabled: detail.rate_time_enabled ?? false,
    rate_time_ranges: detail.rate_time_ranges ?? [],
    custom_outbounds: detail.custom_outbounds ?? [],
    custom_routes: detail.custom_routes ?? [],
    certificate_config: detail.certificate_config ?? { cert_mode: "none" },
    transfer_enable: detail.transfer_enable ?? 0
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
function emptyToNull(value: string | null): string | null { const normalized = value?.trim() ?? ""; return normalized === "" ? null : normalized; }
function errorMessage(cause: unknown): string { return cause instanceof Error ? cause.message : "请求失败"; }
