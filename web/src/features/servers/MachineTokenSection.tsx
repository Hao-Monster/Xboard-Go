import { useState } from "react";
import { Modal } from "../../components/Overlay";
import type { AdminAPI } from "../../lib/api";

export function MachineTokenSection({ api, machineID, onReset, onDialogChange }: {
  api: AdminAPI; machineID: number; onReset: () => Promise<void>; onDialogChange: (open: boolean) => void;
}) {
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [copied, setCopied] = useState(false);
  const close = () => { setConfirming(false); onDialogChange(false); };
  const view = async () => {
    if (token) { setToken(""); return; }
    setBusy(true); setError(""); setCopied(false);
    try {
      const result = await api.getMachineToken(machineID);
      if (result.available) setToken(result.token);
      else setError("尚无可查看的 Token（未接入或旧版本仅保存哈希）。重新接入或明确重置后即可查看；现有凭据不会被自动更改。");
    } catch { setError("读取 Token 失败，请重试。"); } finally { setBusy(false); }
  };
  const reset = async () => {
    if (busy) return;
    setBusy(true); setError(""); setCopied(false);
    try {
      const result = await api.resetMachineToken(machineID);
      setToken(result.token); close(); await onReset();
    } catch { setError("重置 Token 或刷新安装命令失败，请检查当前状态后重试。"); } finally { setBusy(false); }
  };
  return <>
    <section className="detail-card machine-token-card">
      <h3>服务器 Token</h3>
      <p className="muted">此 Token 用于 xboard-node 向面板认证，请妥善保管。</p>
      <div className="action-group">
        <button className="button secondary compact" disabled={busy} onClick={() => void view()}>{token ? "隐藏 Token" : "查看 Token"}</button>
        <button className="button secondary compact" disabled={busy} onClick={() => { setConfirming(true); onDialogChange(true); }}>重置 Token</button>
      </div>
      {token && <div className="machine-token-value"><code>{token}</code><button className="button secondary compact" onClick={() => { void navigator.clipboard.writeText(token).then(() => setCopied(true)).catch(() => setError("复制失败，请手动复制。")); }}>{copied ? "已复制" : "复制 Token"}</button></div>}
      {error && !confirming && <p className="alert error" role="alert">{error}</p>}
    </section>
    {confirming && <Modal title="重置 Token" role="alertdialog" onClose={() => { if (!busy) close(); }}>
      <h2>重置 Token</h2>
      <p>重置后旧 Token 和未使用的安装命令立即失效，已连接的服务器会断开。需要更新服务器凭据或使用新的安装命令重新接入。</p>
      {error && <p className="alert error" role="alert">{error}</p>}
      <div className="form-actions"><button className="button ghost" disabled={busy} onClick={close}>取消</button><button className="button primary" disabled={busy} onClick={() => void reset()}>{busy ? "正在重置…" : "确认重置"}</button></div>
    </Modal>}
  </>;
}
