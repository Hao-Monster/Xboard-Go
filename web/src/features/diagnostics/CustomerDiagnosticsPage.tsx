import React, { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import type {
  AdminDiagnosticsSettingsResponse,
  ConnectionSummary,
  DiagnosticsAdminAPI,
  DiagnosticsConnectionEvent,
  DiagnosticsConnectionResponse,
  DiagnosticsOrderLookupResponse,
  DiagnosticsQueryFilters,
  DiagnosticsQueryResponse,
  DiagnosticsStatusResponse,
  UpdateDiagnosticsSettingsInput,
} from "../../lib/api";
import { Modal } from "../../components/Overlay";
import "./CustomerDiagnosticsPage.css";

export interface CustomerDiagnosticsPageProps {
  api: DiagnosticsAdminAPI;
  initialOrderID?: number;
  initialTradeNo?: string;
}

type QueryMode = "order" | "id";
type QuickRange = "15m" | "1h" | "6h" | "24h" | "72h" | "custom";
type DrawerTab = "timeline" | "dns" | "stack";

function parseInitialOrderTarget(initialOrderID?: number, initialTradeNo?: string) {
  let targetOrderID = initialOrderID;
  let targetTradeNo = initialTradeNo;

  if (!targetOrderID && !targetTradeNo && typeof window !== "undefined") {
    const hashPart = window.location.hash.split("?")[1];
    if (hashPart) {
      const params = new URLSearchParams(hashPart);
      const parsedOid = Number(params.get("order_id"));
      if (!isNaN(parsedOid) && parsedOid > 0) targetOrderID = parsedOid;
      const parsedTn = params.get("trade_no");
      if (parsedTn && parsedTn.trim() !== "") targetTradeNo = parsedTn.trim();
    }
  }

  return { targetOrderID, targetTradeNo };
}

export function CustomerDiagnosticsPage({ api, initialOrderID, initialTradeNo }: CustomerDiagnosticsPageProps) {
  // Query mode: order lookup vs direct 24-hex diagnostic ID
  const [mode, setMode] = useState<QueryMode>("order");

  const initialTarget = useMemo(() => parseInitialOrderTarget(initialOrderID, initialTradeNo), [initialOrderID, initialTradeNo]);

  // Inputs
  const [orderInput, setOrderInput] = useState<string>(() => {
    const t = parseInitialOrderTarget(initialOrderID, initialTradeNo);
    return t.targetOrderID ? String(t.targetOrderID) : t.targetTradeNo || "";
  });
  const [diagnosticIDInput, setDiagnosticIDInput] = useState<string>("");

  // Customer identity & session state
  const [identity, setIdentity] = useState<DiagnosticsOrderLookupResponse | null>(null);
  const [sessionStatus, setSessionStatus] = useState<DiagnosticsStatusResponse | null>(null);
  const [scopeSettings, setScopeSettings] = useState<AdminDiagnosticsSettingsResponse | null>(null);

  // Filter parameters
  const [quickRange, setQuickRange] = useState<QuickRange>("15m");
  const [fromTime, setFromTime] = useState<string>("");
  const [toTime, setToTime] = useState<string>("");
  const [nodeIDFilter, setNodeIDFilter] = useState<string>("");
  const [domainFilter, setDomainFilter] = useState<string>("");
  const [domainMatch, setDomainMatch] = useState<"contains" | "exact" | "suffix">("contains");
  const [onlyErrors, setOnlyErrors] = useState<boolean>(false);
  const [traceIDFilter, setTraceIDFilter] = useState<string>("");

  // Results & pagination
  const [queryResult, setQueryResult] = useState<DiagnosticsQueryResponse | null>(null);
  const [selectedConnection, setSelectedConnection] = useState<ConnectionSummary | null>(null);
  const [connectionDetail, setConnectionDetail] = useState<DiagnosticsConnectionResponse | null>(null);

  // Drawer & modal states
  const [drawerTab, setDrawerTab] = useState<DrawerTab>("timeline");
  const [settingsOpen, setSettingsOpen] = useState<boolean>(false);
  const [settingsDraft, setSettingsDraft] = useState<UpdateDiagnosticsSettingsInput>({
    query_endpoint: "",
    query_username: "",
    query_password: "",
    storage_generation: 1,
  });

  // Busy & error states
  const [loadingScope, setLoadingScope] = useState<boolean>(true);
  const [loadingLookup, setLoadingLookup] = useState<boolean>(() => Boolean(initialTarget.targetOrderID || initialTarget.targetTradeNo));
  const [loadingQuery, setLoadingQuery] = useState<boolean>(false);
  const [loadingDetail, setLoadingDetail] = useState<boolean>(false);
  const [leaseBusy, setLeaseBusy] = useState<boolean>(false);
  const [settingsSaving, setSettingsSaving] = useState<boolean>(false);

  const [generalError, setGeneralError] = useState<string>("");
  const [lookupError, setLookupError] = useState<string>("");
  const [queryError, setQueryError] = useState<string>("");
  const [leaseError, setLeaseError] = useState<string>("");
  const [detailError, setDetailError] = useState<string>("");
  const [currentTime, setCurrentTime] = useState<number>(() => Date.now());

  // Concurrency & cancellation controls
  const queryAbortController = useRef<AbortController | null>(null);
  const detailAbortController = useRef<AbortController | null>(null);
  const requestGeneration = useRef<number>(0);
  const pollTimerRef = useRef<number | null>(null);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setCurrentTime(Date.now());
    }, 10000);
    return () => window.clearInterval(timer);
  }, []);

  // Perform order lookup
  const executeOrderLookup = useCallback(async (params: { trade_no?: string; order_id?: number }) => {
    setLoadingLookup(true);
    setLookupError("");
    setIdentity(null);
    setSessionStatus(null);
    setQueryResult(null);
    setSelectedConnection(null);
    setConnectionDetail(null);

    try {
      const ident = await api.lookupDiagnosticsOrder(params);
      setIdentity(ident);
      // Fetch initial session status
      const status = await api.getDiagnosticsSessionStatus({
        order_id: ident.order_id,
        trade_no: ident.trade_no,
      });
      setSessionStatus(status);
    } catch (err) {
      setLookupError(err instanceof Error ? err.message : "订单客户身份解析失败");
    } finally {
      setLoadingLookup(false);
    }
  }, [api]);

  // Read URL search params on mount if props not explicitly passed
  useEffect(() => {
    let active = true;
    if (!initialTarget.targetOrderID && !initialTarget.targetTradeNo) return;

    const params = initialTarget.targetOrderID
      ? { order_id: initialTarget.targetOrderID }
      : { trade_no: initialTarget.targetTradeNo! };

    void api.lookupDiagnosticsOrder(params).then(async (ident) => {
      if (!active) return;
      setIdentity(ident);
      setLoadingLookup(false);
      try {
        const status = await api.getDiagnosticsSessionStatus({
          order_id: ident.order_id,
          trade_no: ident.trade_no,
        });
        if (active) setSessionStatus(status);
      } catch {
        // Status error handled gracefully
      }
    }).catch((err: unknown) => {
      if (!active) return;
      setLookupError(err instanceof Error ? err.message : "订单客户身份解析失败");
      setLoadingLookup(false);
    });

    return () => { active = false; };
  }, [initialTarget, api]);

  // Load scope and server settings on mount
  const refreshScopeSettings = useCallback(async () => {
    setLoadingScope(true);
    try {
      const cfg = await api.getDiagnosticsSettings();
      setScopeSettings(cfg);
      setSettingsDraft({
        query_endpoint: cfg.query_endpoint,
        query_username: cfg.query_username,
        query_password: "",
        storage_generation: cfg.storage_generation || 1,
      });
    } catch (err) {
      setGeneralError(err instanceof Error ? err.message : "获取诊断配置失败");
    } finally {
      setLoadingScope(false);
    }
  }, [api]);

  useEffect(() => {
    let active = true;
    void api.getDiagnosticsSettings().then((cfg) => {
      if (!active) return;
      setScopeSettings(cfg);
      setSettingsDraft({
        query_endpoint: cfg.query_endpoint,
        query_username: cfg.query_username,
        query_password: "",
        storage_generation: cfg.storage_generation || 1,
      });
      setLoadingScope(false);
    }).catch((err: unknown) => {
      if (!active) return;
      setGeneralError(err instanceof Error ? err.message : "获取诊断配置失败");
      setLoadingScope(false);
    });
    return () => { active = false; };
  }, [api]);

  // Handle explicit scope initialization
  const initializeScope = async () => {
    setLoadingScope(true);
    setGeneralError("");
    try {
      await api.initDiagnosticsScope();
      await refreshScopeSettings();
    } catch (err) {
      setGeneralError(err instanceof Error ? err.message : "初始化诊断 scope 失败");
    } finally {
      setLoadingScope(false);
    }
  };



  const handleOrderLookupSubmit = (e: FormEvent) => {
    e.preventDefault();
    const trimmed = orderInput.trim();
    if (!trimmed) {
      setLookupError("请输入订单号 (trade_no) 或数字订单 ID");
      return;
    }
    const isPureInt = /^\d+$/.test(trimmed) && trimmed.length < 10;
    if (isPureInt) {
      void executeOrderLookup({ order_id: Number(trimmed) });
    } else {
      void executeOrderLookup({ trade_no: trimmed });
    }
  };

  // Compute time filters
  const computedTimeRange = useMemo((): { from?: string; to?: string } => {
    if (quickRange === "custom") {
      return {
        from: fromTime ? new Date(fromTime).toISOString() : undefined,
        to: toTime ? new Date(toTime).toISOString() : undefined,
      };
    }

    const now = new Date();
    const to = now.toISOString();
    let fromDate: Date;
    switch (quickRange) {
      case "15m":
        fromDate = new Date(now.getTime() - 15 * 60 * 1000);
        break;
      case "1h":
        fromDate = new Date(now.getTime() - 60 * 60 * 1000);
        break;
      case "6h":
        fromDate = new Date(now.getTime() - 6 * 60 * 60 * 1000);
        break;
      case "24h":
        fromDate = new Date(now.getTime() - 24 * 60 * 60 * 1000);
        break;
      case "72h":
        fromDate = new Date(now.getTime() - 72 * 60 * 60 * 1000);
        break;
      default:
        fromDate = new Date(now.getTime() - 15 * 60 * 1000);
    }
    return { from: fromDate.toISOString(), to };
  }, [quickRange, fromTime, toTime]);

  // Execute connection query
  const executeQuery = useCallback(async (cursor?: string) => {
    if (mode === "order" && !identity) {
      setQueryError("请先定位有效订单客户");
      return;
    }
    if (mode === "id" && (!diagnosticIDInput || !/^[0-9a-f]{24}$/.test(diagnosticIDInput.trim()))) {
      setQueryError("请输入有效的 24 位小写十六进制客户日志 ID");
      return;
    }

    // Cancel in-flight query
    if (queryAbortController.current) {
      queryAbortController.current.abort();
    }
    const controller = new AbortController();
    queryAbortController.current = controller;
    const currentGen = ++requestGeneration.current;

    setLoadingQuery(true);
    setQueryError("");

    try {
      const times = computedTimeRange;
      const parsedNodeID = nodeIDFilter.trim() ? Number(nodeIDFilter.trim()) : undefined;

      const filters: DiagnosticsQueryFilters = {
        from: times.from,
        to: times.to,
        node_id: parsedNodeID && !isNaN(parsedNodeID) ? parsedNodeID : undefined,
        domain: domainFilter.trim() ? domainFilter.trim() : undefined,
        domain_match: domainFilter.trim() ? domainMatch : undefined,
        has_error: onlyErrors ? true : undefined,
        trace_id: traceIDFilter.trim() ? traceIDFilter.trim() : undefined,
        cursor,
      };

      const res = await api.queryDiagnosticsOrder({
        order_id: mode === "order" ? identity?.order_id : undefined,
        trade_no: mode === "order" ? identity?.trade_no : undefined,
        diagnostic_id: mode === "id" ? diagnosticIDInput.trim().toLowerCase() : undefined,
        from: filters.from,
        to: filters.to,
        node_id: filters.node_id,
        domain: filters.domain,
        domain_match: filters.domain_match,
        has_error: filters.has_error,
        trace_id: filters.trace_id,
        cursor: filters.cursor,
        signal: controller.signal,
      });

      if (currentGen !== requestGeneration.current) return;

      if (cursor && queryResult) {
        setQueryResult({
          ...res,
          items: [...queryResult.items, ...res.items],
        });
      } else {
        setQueryResult(res);
      }
    } catch (err) {
      if (currentGen !== requestGeneration.current) return;
      if (err instanceof DOMException && err.name === "AbortError") return;
      setQueryError(err instanceof Error ? err.message : "连接诊断查询失败");
    } finally {
      if (currentGen === requestGeneration.current) {
        setLoadingQuery(false);
      }
    }
  }, [mode, identity, diagnosticIDInput, computedTimeRange, nodeIDFilter, domainFilter, domainMatch, onlyErrors, traceIDFilter, api, queryResult]);

  // Load connection detail
  const loadConnectionDetail = useCallback(async (conn: ConnectionSummary) => {
    setSelectedConnection(conn);
    setConnectionDetail(null);
    setDetailError("");
    setDrawerTab("timeline");

    if (detailAbortController.current) {
      detailAbortController.current.abort();
    }
    const controller = new AbortController();
    detailAbortController.current = controller;

    setLoadingDetail(true);
    try {
      const res = await api.connectionDiagnosticsOrder({
        order_id: mode === "order" ? identity?.order_id : undefined,
        trade_no: mode === "order" ? identity?.trade_no : undefined,
        diagnostic_id: mode === "id" ? diagnosticIDInput.trim().toLowerCase() : undefined,
        node_id: conn.node_id,
        runtime_id: conn.runtime_id,
        connection_id: conn.connection_id,
        signal: controller.signal,
      });
      setConnectionDetail(res);
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      setDetailError(err instanceof Error ? err.message : "获取连接详情失败");
    } finally {
      setLoadingDetail(false);
    }
  }, [api, mode, identity, diagnosticIDInput]);

  // Start 15-minute diagnostic session
  const startSession = async () => {
    if (!identity) return;
    setLeaseBusy(true);
    setLeaseError("");
    try {
      const idempotencyKey = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
      await api.startDiagnosticsSession({
        order_id: identity.order_id,
        trade_no: identity.trade_no,
        idempotency_key: idempotencyKey,
      });
      const nextStatus = await api.getDiagnosticsSessionStatus({
        order_id: identity.order_id,
        trade_no: identity.trade_no,
      });
      setSessionStatus(nextStatus);
    } catch (err) {
      setLeaseError(err instanceof Error ? err.message : "开启详细诊断会话失败");
    } finally {
      setLeaseBusy(false);
    }
  };

  // Stop diagnostic session
  const stopSession = async () => {
    if (!identity || !sessionStatus?.session) return;
    setLeaseBusy(true);
    setLeaseError("");
    try {
      const idempotencyKey = typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
      await api.stopDiagnosticsSession({
        order_id: identity.order_id,
        trade_no: identity.trade_no,
        session_id: sessionStatus.session.session_id,
        idempotency_key: idempotencyKey,
      });
      const nextStatus = await api.getDiagnosticsSessionStatus({
        order_id: identity.order_id,
        trade_no: identity.trade_no,
      });
      setSessionStatus(nextStatus);
    } catch (err) {
      setLeaseError(err instanceof Error ? err.message : "停止诊断会话失败");
    } finally {
      setLeaseBusy(false);
    }
  };

  // Status Polling: poll every 5 seconds if session is active/pending and tab is visible
  useEffect(() => {
    if (pollTimerRef.current) {
      window.clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }

    if (mode !== "order" || !identity) return;

    const hasActiveSession = sessionStatus?.has_session &&
      sessionStatus.session?.enabled &&
      new Date(sessionStatus.session.expires_at).getTime() > Date.now();

    if (!hasActiveSession) return;

    pollTimerRef.current = window.setInterval(() => {
      if (document.visibilityState === "hidden") return;
      void api.getDiagnosticsSessionStatus({
        order_id: identity.order_id,
        trade_no: identity.trade_no,
      }).then((fresh) => {
        setSessionStatus(fresh);
      }).catch(() => undefined);
    }, 5000);

    return () => {
      if (pollTimerRef.current) {
        window.clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    };
  }, [mode, identity, sessionStatus?.has_session, sessionStatus?.session?.enabled, sessionStatus?.session?.expires_at, api]);

  // Clean up all controllers on unmount
  useEffect(() => {
    return () => {
      queryAbortController.current?.abort();
      detailAbortController.current?.abort();
    };
  }, []);

  // Save diagnostic settings
  const handleSaveSettings = async (e: FormEvent) => {
    e.preventDefault();
    setSettingsSaving(true);
    setGeneralError("");
    try {
      const updated = await api.updateDiagnosticsSettings(settingsDraft);
      setScopeSettings(updated);
      setSettingsOpen(false);
    } catch (err) {
      setGeneralError(err instanceof Error ? err.message : "保存诊断存储设置失败");
    } finally {
      setSettingsSaving(false);
    }
  };

  // Helper formatting functions
  const formatTime = (timeStr?: string) => {
    if (!timeStr) return "—";
    try {
      const d = new Date(timeStr);
      return d.toLocaleTimeString([], { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" }) +
        "." + String(d.getMilliseconds()).padStart(3, "0");
    } catch {
      return timeStr;
    }
  };

  const formatDateTime = (timeStr?: string) => {
    if (!timeStr) return "—";
    try {
      const d = new Date(timeStr);
      return d.toLocaleString([], { hour12: false });
    } catch {
      return timeStr;
    }
  };

  const formatBytes = (bytes?: number) => {
    if (bytes === undefined || bytes === null || isNaN(bytes)) return "—";
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MiB`;
  };

  const getRemainingMinutes = (expiresAt?: string) => {
    if (!expiresAt) return 0;
    const diff = new Date(expiresAt).getTime() - currentTime;
    return Math.max(0, Math.ceil(diff / 60000));
  };

  return (
    <div className="diagnostics-shell">
      {/* Header */}
      <header className="diagnostics-header">
        <div className="diagnostics-header-titles">
          <p className="eyebrow">Customer Connection Diagnostics</p>
          <h1>客户连接诊断与日志中心</h1>
          <p className="muted">
            输入订单或稳定客户日志 ID，排查节点真实连接、DNS 状态、拨号错误与 15 分钟实时详细诊断
          </p>
        </div>

        <div className="diagnostics-header-actions">
          {loadingScope ? (
            <span className="scope-badge">加载中…</span>
          ) : scopeSettings?.initialized ? (
            <span className="scope-badge initialized" title={`Panel Scope: ${scopeSettings.panel_scope}`}>
              <span className="badge-dot" />
              Scope 已就绪 ({scopeSettings.panel_scope.slice(0, 16)}…)
            </span>
          ) : (
            <button className="button danger compact" onClick={() => void initializeScope()} title="初始化安装级稳定 Scope">
              初始化诊断 Scope
            </button>
          )}

          <button className="button secondary compact" onClick={() => setSettingsOpen(true)} title="配置存储与端点">
            存储配置
          </button>
        </div>
      </header>

      {/* General Alert */}
      {generalError && (
        <div className="alert error" role="alert">
          {generalError}
        </div>
      )}

      {/* Mode Tabs */}
      <div className="mode-tabs" role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={mode === "order"}
          className={`mode-tab-button ${mode === "order" ? "active" : ""}`}
          onClick={() => {
            setMode("order");
            setQueryResult(null);
            setSelectedConnection(null);
            setConnectionDetail(null);
          }}
        >
          订单定位模式 (推荐)
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={mode === "id"}
          className={`mode-tab-button ${mode === "id" ? "active" : ""}`}
          onClick={() => {
            setMode("id");
            setQueryResult(null);
            setSelectedConnection(null);
            setConnectionDetail(null);
          }}
        >
          客户日志 ID 模式 (只读)
        </button>
      </div>

      {/* Lookup Card */}
      <section className="diagnostics-card">
        {mode === "order" ? (
          <>
            <form className="search-form" onSubmit={handleOrderLookupSubmit}>
              <div className="search-field-group">
                <label htmlFor="order-input">订单号 (trade_no) 或数字订单 ID</label>
                <input
                  id="order-input"
                  type="text"
                  className="search-input"
                  placeholder="例如: 2026102512345678 或 1024"
                  value={orderInput}
                  onChange={(e) => setOrderInput(e.target.value)}
                  disabled={loadingLookup}
                />
              </div>
              <button type="submit" className="button primary" disabled={loadingLookup || !orderInput.trim()}>
                {loadingLookup ? "解析中…" : "定位客户"}
              </button>
            </form>

            {lookupError && (
              <div className="alert error" role="alert" style={{ marginTop: "1rem" }}>
                {lookupError}
              </div>
            )}

            {/* Identity Card */}
            {identity && (
              <>
                <div className="identity-card" aria-label="客户诊断身份卡">
                  <div className="identity-item">
                    <span className="label">订单号</span>
                    <span className="value monospace">{identity.trade_no}</span>
                  </div>
                  <div className="identity-item">
                    <span className="label">订单 ID</span>
                    <span className="value">#{identity.order_id}</span>
                  </div>
                  <div className="identity-item">
                    <span className="label">订单类型 / 状态</span>
                    <span className="value">{identity.order_type} · 状态 {identity.order_status}</span>
                  </div>
                  <div className="identity-item">
                    <span className="label">客户名称</span>
                    <span className="value">{identity.customer_display_name || "—"}</span>
                  </div>
                  <div className="identity-item">
                    <span className="label">订阅套餐</span>
                    <span className="value">{identity.subscription_name || "—"}</span>
                  </div>
                  <div className="identity-item">
                    <span className="label">客户诊断 ID (稳定只读标识)</span>
                    <span className="value monospace">{identity.diagnostic_id}</span>
                  </div>
                </div>

                {/* 15-Minute Detailed Diagnostic Lease Control */}
                <div className="lease-control-panel">
                  <div className="lease-status-banner">
                    <div>
                      <strong>15 分钟详细诊断状态：</strong>
                      {sessionStatus?.has_session && sessionStatus.session?.enabled ? (
                        <span className={`badge badge-${sessionStatus.session.state}`}>
                          {sessionStatus.session.state === "active" ? "已生效 (Active)" :
                           sessionStatus.session.state === "partial" ? "部分节点确认 (Partial)" :
                           sessionStatus.session.state === "stopping" ? "正在停止 (Stopping)" : "等待确认 (Pending)"}
                          {" · 剩余约 "}{getRemainingMinutes(sessionStatus.session.expires_at)}{" 分钟"}
                        </span>
                      ) : (
                        <span className="badge badge-expired">未开启 (Baseline 模式)</span>
                      )}
                    </div>

                    <div>
                      {sessionStatus?.has_session && sessionStatus.session?.enabled ? (
                        <button
                          type="button"
                          className="button danger compact"
                          onClick={() => void stopSession()}
                          disabled={leaseBusy}
                        >
                          {leaseBusy ? "正在停止…" : "停止详细诊断"}
                        </button>
                      ) : (
                        <button
                          type="button"
                          className="button primary compact"
                          onClick={() => void startSession()}
                          disabled={leaseBusy}
                        >
                          {leaseBusy ? "正在开启…" : "开启详细诊断 (15分钟)"}
                        </button>
                      )}
                    </div>
                  </div>

                  {leaseError && (
                    <div className="alert error" role="alert">
                      {leaseError}
                    </div>
                  )}

                  {/* Node ACKs Breakdown */}
                  {sessionStatus?.session && Object.keys(sessionStatus.session.node_acks || {}).length > 0 && (
                    <div className="node-acks-list" aria-label="节点确认列表">
                      {Object.values(sessionStatus.session.node_acks).map((ack) => (
                        <span
                          key={ack.node_id}
                          className={`node-ack-pill ${ack.effective ? "confirmed" : "unconfirmed"}`}
                          title={`修订版本: r${ack.effective_revision}，接收时间: ${ack.ack_received_at}`}
                        >
                          节点 #{ack.node_id}: {ack.effective ? "已确认" : `未确认 (${ack.rejection_reason || "等待ACK"})`}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </>
            )}
          </>
        ) : (
          <form
            className="search-form"
            onSubmit={(e) => {
              e.preventDefault();
              void executeQuery();
            }}
          >
            <div className="search-field-group">
              <label htmlFor="diag-id-input">客户日志 ID (24 位十六进制散列)</label>
              <input
                id="diag-id-input"
                type="text"
                className="search-input monospace"
                placeholder="例如: 848d531a78dc1fcbb6ac065b"
                value={diagnosticIDInput}
                maxLength={24}
                onChange={(e) => setDiagnosticIDInput(e.target.value.toLowerCase())}
              />
            </div>
            <button
              type="submit"
              className="button primary"
              disabled={loadingQuery || !/^[0-9a-f]{24}$/.test(diagnosticIDInput.trim())}
            >
              {loadingQuery ? "查询中…" : "只读查询"}
            </button>
            <p className="muted" style={{ width: "100%", margin: "0.25rem 0 0 0", fontSize: "0.75rem" }}>
              * 纯日志 ID 模式受限于安全沙箱，仅支持只读连接查询，禁止反查客户资料或开启租约。
            </p>
          </form>
        )}
      </section>

      {/* Filters Bar */}
      <section className="filters-bar" aria-label="诊断过滤条件">
        {/* Quick Range */}
        <div className="filter-item">
          <label>时间范围</label>
          <div className="quick-ranges">
            {(["15m", "1h", "6h", "24h", "72h"] as QuickRange[]).map((r) => (
              <button
                key={r}
                type="button"
                className={`quick-range-btn ${quickRange === r ? "active" : ""}`}
                onClick={() => setQuickRange(r)}
              >
                {r === "15m" ? "15分钟" : r === "1h" ? "1小时" : r === "6h" ? "6小时" : r === "24h" ? "24小时" : "72小时"}
              </button>
            ))}
            <button
              type="button"
              className={`quick-range-btn ${quickRange === "custom" ? "active" : ""}`}
              onClick={() => setQuickRange("custom")}
            >
              自定义
            </button>
          </div>
        </div>

        {quickRange === "custom" && (
          <>
            <div className="filter-item">
              <label htmlFor="filter-from">起始时间 (From)</label>
              <input
                id="filter-from"
                type="datetime-local"
                className="filter-input"
                value={fromTime}
                onChange={(e) => setFromTime(e.target.value)}
              />
            </div>
            <div className="filter-item">
              <label htmlFor="filter-to">截止时间 (To)</label>
              <input
                id="filter-to"
                type="datetime-local"
                className="filter-input"
                value={toTime}
                onChange={(e) => setToTime(e.target.value)}
              />
            </div>
          </>
        )}

        {/* Node ID */}
        <div className="filter-item">
          <label htmlFor="filter-node">节点 ID</label>
          <input
            id="filter-node"
            type="number"
            className="filter-input"
            placeholder="全部节点"
            style={{ width: "90px" }}
            value={nodeIDFilter}
            onChange={(e) => setNodeIDFilter(e.target.value)}
          />
        </div>

        {/* Domain Filter */}
        <div className="filter-item" style={{ flex: 1, minWidth: "200px" }}>
          <label htmlFor="filter-domain">目标域名</label>
          <div style={{ display: "flex", gap: "0.25rem" }}>
            <select
              className="filter-select"
              value={domainMatch}
              onChange={(e) => setDomainMatch(e.target.value as "contains" | "exact" | "suffix")}
              aria-label="域名匹配模式"
            >
              <option value="contains">包含</option>
              <option value="exact">精确</option>
              <option value="suffix">后缀</option>
            </select>
            <input
              id="filter-domain"
              type="text"
              className="filter-input"
              placeholder="例如: google.com"
              style={{ flex: 1 }}
              value={domainFilter}
              onChange={(e) => setDomainFilter(e.target.value)}
            />
          </div>
        </div>

        {/* Trace ID */}
        <div className="filter-item">
          <label htmlFor="filter-trace">精确 Trace ID (32位十六进制)</label>
          <input
            id="filter-trace"
            type="text"
            className="filter-input monospace"
            placeholder="可选 32 位 Hex"
            style={{ width: "160px" }}
            value={traceIDFilter}
            onChange={(e) => setTraceIDFilter(e.target.value.toLowerCase())}
          />
        </div>

        {/* Error Only */}
        <div className="filter-item" style={{ justifyContent: "center" }}>
          <label style={{ display: "flex", alignItems: "center", gap: "0.35rem", cursor: "pointer", marginTop: "1rem" }}>
            <input
              type="checkbox"
              checked={onlyErrors}
              onChange={(e) => setOnlyErrors(e.target.checked)}
            />
            仅查看错误 (Has Error)
          </label>
        </div>

        {/* Query Actions */}
        <div style={{ display: "flex", gap: "0.5rem" }}>
          <button
            type="button"
            className="button primary"
            onClick={() => void executeQuery()}
            disabled={loadingQuery || (mode === "order" && !identity) || (mode === "id" && !diagnosticIDInput)}
          >
            {loadingQuery ? "查询中…" : "执行查询"}
          </button>
          <button
            type="button"
            className="button secondary"
            onClick={() => void executeQuery()}
            disabled={loadingQuery || (mode === "order" && !identity) || (mode === "id" && !diagnosticIDInput)}
            title="刷新建立新快照"
          >
            刷新
          </button>
        </div>
      </section>

      {/* Query Error */}
      {queryError && (
        <div className="alert error" role="alert">
          {queryError}
        </div>
      )}

      {/* Coverage & Quota Status Bar */}
      {queryResult && (
        <>
          <div className="coverage-bar">
            <div className="coverage-status">
              <span>观测覆盖状态：</span>
              <span className={`badge badge-${queryResult.coverage.state}`}>
                {queryResult.coverage.state === "full" ? "完整覆盖 (Full Coverage)" :
                 queryResult.coverage.state === "partial" ? "部分节点覆盖 (Partial)" :
                 queryResult.coverage.state === "delayed" ? "日志延迟缓冲 (Delayed)" : "覆盖范围未知 (Unknown)"}
              </span>
              {queryResult.coverage.reasons.length > 0 && (
                <span className="muted">({queryResult.coverage.reasons.join(", ")})</span>
              )}
            </div>

            <div className="muted">
              快照建立时间：{formatDateTime(queryResult.as_of)} · 当前展示 {queryResult.items.length} 条记录
            </div>
          </div>

          {/* Truncation warnings */}
          {queryResult.scan_truncated && (
            <div className="alert-truncation" role="alert">
              ⚠️ 读取达到安全上限 (10,000 行 / 16MiB)，仅展示先到范围的聚合结果。建议缩小时间范围或指定目标域名。
            </div>
          )}

          {queryResult.truncated && (
            <div className="alert-truncation" role="alert">
              ⚠️ 聚合连接条目达到当前快照上限 (1,000 条)。
            </div>
          )}
        </>
      )}

      {/* Connection Summary Table */}
      <section className="diagnostics-card" style={{ padding: 0 }}>
        <div className="table-responsive">
          <table className="diagnostics-table" aria-label="连接诊断记录列表">
            <thead>
              <tr>
                <th>开始时间 / 事件数</th>
                <th>节点 / 协议</th>
                <th>目标域名 / 来源</th>
                <th>目标地址 / Socket 对端</th>
                <th>传输状态</th>
                <th>阶段 / 错误原因</th>
                <th>异常栈</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {loadingQuery && (!queryResult || queryResult.items.length === 0) ? (
                <tr>
                  <td colSpan={8} className="empty-state">
                    正在查询连接事件…
                  </td>
                </tr>
              ) : !queryResult || queryResult.items.length === 0 ? (
                <tr>
                  <td colSpan={8} className="empty-state">
                    {queryResult ? "所选范围没有收到匹配记录" : "请输入订单或客户日志 ID 并点击查询"}
                  </td>
                </tr>
              ) : (
                queryResult.items.map((conn) => {
                  const isSelected = selectedConnection?.connection_id === conn.connection_id;
                  return (
                    <tr
                      key={`${conn.node_id}-${conn.runtime_id}-${conn.connection_id}-${conn.target_id}`}
                      className={isSelected ? "table-row-selected" : ""}
                      onClick={() => void loadConnectionDetail(conn)}
                    >
                      <td>
                        <div>{formatTime(conn.start_time)}</div>
                        <div className="muted" style={{ fontSize: "0.75rem" }}>
                          {conn.event_count} 个事件 · {formatBytes(conn.bytes_in + conn.bytes_out)}
                        </div>
                      </td>
                      <td>
                        <div>节点 #{conn.node_id}</div>
                        <span className="badge">{conn.observed_protocol || "TCP"}</span>
                      </td>
                      <td>
                        <div style={{ fontWeight: 600 }}>{conn.observed_domain || "—"}</div>
                        <div className="muted" style={{ fontSize: "0.75rem" }}>
                          来源: {conn.domain_source || "unknown"}
                        </div>
                      </td>
                      <td>
                        <div className="monospace" style={{ fontSize: "0.75rem" }}>
                          {conn.selected_target_address}:{conn.selected_target_port}
                        </div>
                        {conn.socket_peer && conn.socket_peer !== conn.selected_target_address && (
                          <div className="muted monospace" style={{ fontSize: "0.75rem" }}>
                            对端: {conn.socket_peer}
                          </div>
                        )}
                      </td>
                      <td>
                        <span className={`badge ${conn.has_error ? "badge-stopping" : "badge-active"}`}>
                          {conn.transport_state}
                        </span>
                      </td>
                      <td>
                        {conn.has_error ? (
                          <div>
                            <span style={{ color: "#f87171", fontWeight: 600 }}>
                              {conn.last_error_stage || "error"}
                            </span>
                            <div className="muted" style={{ fontSize: "0.75rem" }}>
                              {conn.last_reason_code}
                            </div>
                          </div>
                        ) : (
                          <span style={{ color: "#34d399" }}>正常</span>
                        )}
                      </td>
                      <td>
                        {conn.has_stack ? (
                          <span className="badge badge-partial" title={`捕获类型: ${conn.stack_capture_kind}`}>
                            捕获栈
                          </span>
                        ) : (
                          <span className="muted">—</span>
                        )}
                      </td>
                      <td>
                        <button
                          type="button"
                          className="button secondary compact"
                          onClick={(e) => {
                            e.stopPropagation();
                            void loadConnectionDetail(conn);
                          }}
                        >
                          详情
                        </button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination Footer */}
        {queryResult?.next_cursor && (
          <div className="pagination-footer" style={{ padding: "1rem" }}>
            <span className="muted">还有更多记录…</span>
            <button
              type="button"
              className="button secondary"
              onClick={() => void executeQuery(queryResult.next_cursor || undefined)}
              disabled={loadingQuery}
            >
              {loadingQuery ? "加载中…" : "加载下一页"}
            </button>
          </div>
        )}
      </section>

      {/* Connection Detail Drawer */}
      {selectedConnection && (
        <div
          className="drawer-overlay"
          onClick={() => setSelectedConnection(null)}
          onKeyDown={(e) => e.key === "Escape" && setSelectedConnection(null)}
        >
          <div className="drawer-panel" aria-label="连接详情与事件流" onClick={(e) => e.stopPropagation()}>
            <header className="drawer-header">
              <div>
                <h3 style={{ margin: "0 0 0.25rem 0", fontSize: "1.125rem" }}>连接详情与事件流</h3>
                <span className="monospace muted" style={{ fontSize: "0.8125rem" }}>
                  连接 ID: {selectedConnection.connection_id} (节点 #{selectedConnection.node_id})
                </span>
              </div>
              <button
                type="button"
                className="icon-button"
                aria-label="关闭详情抽屉"
                onClick={() => setSelectedConnection(null)}
              >
                ×
              </button>
            </header>

            {/* Drawer Tabs */}
            <div className="drawer-tabs">
              <button
                type="button"
                className={`drawer-tab ${drawerTab === "timeline" ? "active" : ""}`}
                onClick={() => setDrawerTab("timeline")}
              >
                事件时间线 ({connectionDetail?.events?.length || 0})
              </button>
              <button
                type="button"
                className={`drawer-tab ${drawerTab === "dns" ? "active" : ""}`}
                onClick={() => setDrawerTab("dns")}
              >
                DNS 解析操作 ({connectionDetail?.dns_exchange?.length || 0})
              </button>
              <button
                type="button"
                className={`drawer-tab ${drawerTab === "stack" ? "active" : ""}`}
                onClick={() => setDrawerTab("stack")}
              >
                异常调用栈 {selectedConnection.has_stack ? "●" : ""}
              </button>
            </div>

            {/* Drawer Content */}
            <div className="drawer-content">
              {detailError && (
                <div className="alert error" role="alert">
                  {detailError}
                </div>
              )}

              {loadingDetail ? (
                <div className="empty-state">正在加载连接详细事件…</div>
              ) : drawerTab === "timeline" ? (
                <div className="timeline" aria-label="事件时间线">
                  {(!connectionDetail?.events || connectionDetail.events.length === 0) ? (
                    <div className="empty-state">未获取到连接事件流</div>
                  ) : (
                    connectionDetail.events.map((evt: DiagnosticsConnectionEvent, idx: number) => {
                      const isError = evt.span_status === "error" || evt.has_error;
                      return (
                        <div key={evt.event_id || idx} className="timeline-item">
                          <span className={`timeline-dot ${isError ? "error" : "ok"}`} />
                          <div className="timeline-header">
                            <span className="event-name">{evt.event}</span>
                            <span className="event-time">{formatTime(evt.event_time)}</span>
                          </div>
                          {evt.span_name && (
                            <div style={{ fontSize: "0.75rem", marginBottom: "0.25rem" }}>
                              <span className="muted">Span: </span>
                              <strong>{evt.span_name}</strong>
                              {" · 状态: "}
                              <span style={{ color: isError ? "#f87171" : "#34d399" }}>
                                {evt.span_status || "ok"}
                              </span>
                              {evt.span_duration_ms !== undefined && ` (${evt.span_duration_ms}ms)`}
                            </div>
                          )}
                          <div className="event-details">
                            {evt.observed_domain && (
                              <div><span>域名:</span>{evt.observed_domain}</div>
                            )}
                            {evt.selected_target_address && (
                              <div><span>目标:</span>{evt.selected_target_address}:{evt.selected_target_port}</div>
                            )}
                            {evt.last_reason_code && (
                              <div><span style={{ color: "#f87171" }}>原因:</span>{evt.last_reason_code}</div>
                            )}
                            {evt.reason_code && !evt.last_reason_code && (
                              <div><span style={{ color: "#f87171" }}>原因:</span>{evt.reason_code}</div>
                            )}
                            {evt.error_message && (
                              <div><span style={{ color: "#f87171" }}>详情:</span>{evt.error_message}</div>
                            )}
                            {evt.transport_state && (
                              <div><span>传输:</span>{evt.transport_state}</div>
                            )}
                          </div>
                        </div>
                      );
                    })
                  )}
                </div>
              ) : drawerTab === "dns" ? (
                <div>
                  {(!connectionDetail?.dns_exchange || connectionDetail.dns_exchange.length === 0) ? (
                    <div className="empty-state">该连接没有关联的独立 DNS 解析操作</div>
                  ) : (
                    <div style={{ display: "flex", flexDirection: "column", gap: "1rem" }}>
                      {connectionDetail.dns_exchange.map((dns: DiagnosticsConnectionEvent, idx: number) => (
                        <div key={dns.event_id || idx} className="diagnostics-card" style={{ background: "rgba(255,255,255,0.02)" }}>
                          <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "0.5rem" }}>
                            <strong>操作 ID: {dns.resolver_operation_id}</strong>
                            <span className="muted">{formatTime(dns.event_time)}</span>
                          </div>
                          <div className="event-details">
                            <div><span>服务器:</span>{dns.dns_server || "节点内置"}</div>
                            <div><span>协议:</span>{dns.dns_transport || "UDP"}</div>
                            <div><span>响应码:</span>{dns.dns_rcode || "NOERROR"}</div>
                            <div><span>解析结论:</span>{dns.node_resolution || "resolved"}</div>
                          </div>
                          {dns.dns_answers && dns.dns_answers.length > 0 && (
                            <div style={{ marginTop: "0.5rem" }}>
                              <span className="muted" style={{ fontSize: "0.75rem" }}>解析结果答案:</span>
                              <div className="monospace" style={{ fontSize: "0.75rem", background: "#0b0d12", padding: "0.5rem", borderRadius: "4px" }}>
                                {dns.dns_answers.join("\n")}
                              </div>
                            </div>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ) : (
                <div>
                  {(() => {
                    const stackEvt = connectionDetail?.events?.find((e: DiagnosticsConnectionEvent) => e.exception_stack && e.exception_stack.length > 0);
                    if (!stackEvt || !stackEvt.exception_stack) {
                      return <div className="empty-state">该连接未触发或未记录异常调用栈</div>;
                    }
                    return (
                      <div>
                        <div className="stack-capture-label">
                          捕获来源: <strong>{stackEvt.stack_capture_kind || "creation_site"}</strong>
                          {stackEvt.payload_truncated && <span style={{ color: "#fbbf24", marginLeft: "0.5rem" }}>(栈帧已按安全预算截断)</span>}
                        </div>
                        <pre className="stack-container">
                          {stackEvt.exception_stack.join("\n")}
                        </pre>
                      </div>
                    );
                  })()}
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Storage Settings Modal */}
      {settingsOpen && (
        <Modal title="诊断存储与端点配置" onClose={() => setSettingsOpen(false)}>
          <div className="modal-header">
            <h2>诊断存储与端点配置</h2>
            <button
              type="button"
              className="icon-button"
              aria-label="关闭配置窗口"
              onClick={() => setSettingsOpen(false)}
            >
              ×
            </button>
          </div>
          <form className="form-stack" onSubmit={(e) => void handleSaveSettings(e)}>
            <label>
              VictoriaLogs 查询端点 (Query Endpoint)
              <input
                type="url"
                required
                placeholder="例如: http://127.0.0.1:9428"
                value={settingsDraft.query_endpoint}
                onChange={(e) => setSettingsDraft({ ...settingsDraft, query_endpoint: e.target.value })}
              />
            </label>

            <label>
              Basic Auth 用户名 (可选)
              <input
                type="text"
                placeholder="留空表示无鉴权"
                value={settingsDraft.query_username}
                onChange={(e) => setSettingsDraft({ ...settingsDraft, query_username: e.target.value })}
              />
            </label>

            <label>
              Basic Auth 密码 (可选，留空保持不变)
              <input
                type="password"
                placeholder={scopeSettings?.has_query_password ? "•••••••• (已配置)" : "输入新密码"}
                value={settingsDraft.query_password || ""}
                onChange={(e) => setSettingsDraft({ ...settingsDraft, query_password: e.target.value })}
              />
            </label>

            <label>
              存储代数 (Storage Generation，用于失效旧快照与游标)
              <input
                type="number"
                min={1}
                required
                value={settingsDraft.storage_generation || 1}
                onChange={(e) => setSettingsDraft({ ...settingsDraft, storage_generation: Number(e.target.value) })}
              />
            </label>

            <div className="form-actions">
              <button type="button" className="button ghost" onClick={() => setSettingsOpen(false)}>
                取消
              </button>
              <button type="submit" className="button primary" disabled={settingsSaving}>
                {settingsSaving ? "保存中…" : "保存配置"}
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
