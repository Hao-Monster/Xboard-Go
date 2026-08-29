import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";

import type { MailSettingsTestResult, MailTemplate, MailTemplateName, MailTemplatePreview, MailTemplateSummary } from "../../lib/api";

interface MailTemplateAPI {
  listMailTemplates: () => Promise<MailTemplateSummary[]>;
  getMailTemplate: (name: MailTemplateName) => Promise<MailTemplate>;
  updateMailTemplate: (name: MailTemplateName, revision: number, subject: string, content: string) => Promise<MailTemplate>;
  resetMailTemplate: (name: MailTemplateName, revision: number) => Promise<MailTemplate>;
  previewMailTemplate: (name: MailTemplateName, subject: string, content: string) => Promise<MailTemplatePreview>;
  testMailTemplate: (name: MailTemplateName, email: string) => Promise<MailSettingsTestResult>;
}

interface Draft {
  subject: string;
  content: string;
}

export function MailTemplateSettingsPage({ api }: { api: MailTemplateAPI }) {
  const [templates, setTemplates] = useState<MailTemplateSummary[]>([]);
  const [current, setCurrent] = useState<MailTemplate | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [recipient, setRecipient] = useState("");
  const [preview, setPreview] = useState<MailTemplatePreview | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState<"" | "save" | "reset" | "preview" | "test">("");
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");
  const contentRef = useRef<HTMLTextAreaElement>(null);
  const loadSequence = useRef(0);

  const dirty = current !== null && draft !== null && (current.subject !== draft.subject || current.content !== draft.content);

  const apply = useCallback((template: MailTemplate) => {
    setCurrent(template);
    setDraft({ subject: template.subject, content: template.content });
    setTemplates((items) => items.map((item) => item.name === template.name ? {
      name: template.name, label: template.label, customized: template.customized,
      revision: template.revision, updated_at: template.updated_at
    } : item));
    setPreview(null);
  }, []);

  const loadTemplate = useCallback(async (name: MailTemplateName) => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      const template = await api.getMailTemplate(name);
      if (sequence === loadSequence.current) apply(template);
    } catch (cause) {
      if (sequence === loadSequence.current) setError(message(cause));
    } finally {
      if (sequence === loadSequence.current) setLoading(false);
    }
  }, [api, apply]);

  const loadCatalog = useCallback(async () => {
    const sequence = ++loadSequence.current;
    setLoading(true);
    setError("");
    setSuccess("");
    try {
      const items = await api.listMailTemplates();
      if (sequence !== loadSequence.current) return;
      setTemplates(items);
      const first = items[0];
      if (first === undefined) {
        setCurrent(null);
        setDraft(null);
        return;
      }
      const template = await api.getMailTemplate(first.name);
      if (sequence === loadSequence.current) apply(template);
    } catch (cause) {
      if (sequence === loadSequence.current) setError(message(cause));
    } finally {
      if (sequence === loadSequence.current) setLoading(false);
    }
  }, [api, apply]);

  useEffect(() => {
    let live = true;
    const sequence = ++loadSequence.current;
    void api.listMailTemplates().then(async (items) => {
      if (!live) return;
      setTemplates(items);
      const first = items[0];
      if (first === undefined) {
        setCurrent(null);
        setDraft(null);
        setLoading(false);
        return;
      }
      const template = await api.getMailTemplate(first.name);
      if (live && sequence === loadSequence.current) apply(template);
    }).catch((cause: unknown) => {
      if (live && sequence === loadSequence.current) setError(message(cause));
    }).finally(() => {
      if (live && sequence === loadSequence.current) setLoading(false);
    });
    return () => {
      live = false;
      loadSequence.current += 1;
    };
  }, [api, apply]);

  useEffect(() => {
    const warn = (event: BeforeUnloadEvent) => {
      if (!dirty) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const select = (name: MailTemplateName) => {
    if (name === current?.name || busy !== "") return;
    if (dirty && !window.confirm("当前模板有未保存的修改，确认切换并放弃这些修改吗？")) return;
    void loadTemplate(name);
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (current === null || draft === null || busy !== "") return;
    setBusy("save"); setError(""); setSuccess("");
    try {
      apply(await api.updateMailTemplate(current.name, current.revision, draft.subject, draft.content));
      setSuccess("邮件模板已保存");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const reset = async () => {
    if (current === null || busy !== "" || !window.confirm(`确认将“${current.label}”恢复为默认模板吗？`)) return;
    setBusy("reset"); setError(""); setSuccess("");
    try {
      apply(await api.resetMailTemplate(current.name, current.revision));
      setSuccess("已恢复默认模板");
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const renderPreview = async () => {
    if (current === null || draft === null || busy !== "") return;
    setBusy("preview"); setError(""); setSuccess("");
    try {
      setPreview(await api.previewMailTemplate(current.name, draft.subject, draft.content));
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const sendTest = async () => {
    if (current === null || busy !== "") return;
    setBusy("test"); setError(""); setSuccess("");
    try {
      const result = await api.testMailTemplate(current.name, recipient);
      setSuccess(`测试邮件已发送至 ${result.recipient}`);
    } catch (cause) {
      setError(message(cause));
    } finally {
      setBusy("");
    }
  };

  const insertVariable = (variable: string) => {
    if (draft === null) return;
    const textarea = contentRef.current;
    const start = textarea?.selectionStart ?? draft.content.length;
    const end = textarea?.selectionEnd ?? start;
    const token = `{{${variable}}}`;
    setDraft({ ...draft, content: draft.content.slice(0, start) + token + draft.content.slice(end) });
    setPreview(null); setError(""); setSuccess("");
    window.requestAnimationFrame(() => {
      textarea?.focus();
      textarea?.setSelectionRange(start + token.length, start + token.length);
    });
  };

  return <main className="page-shell mail-template-page">
    <header className="page-header"><div><p className="eyebrow">Email templates</p><h1>邮件模板</h1><p className="muted">管理与旧版 Xboard 一致的五类系统邮件；预览和真实投递共用服务端安全渲染器。</p></div></header>
    {error !== "" && <div className="alert error global-alert" role="alert">{error}</div>}
    {success !== "" && <div className="alert success global-alert" role="status">{success}</div>}
    <div className="mail-template-layout">
      <aside className="site-settings-card mail-template-list" aria-label="邮件模板列表">
        {templates.map((template) => <button key={template.name} type="button" className={`mail-template-item ${current?.name === template.name ? "active" : ""}`} aria-current={current?.name === template.name ? "true" : undefined} onClick={() => select(template.name)}>
          <span>{template.label}</span><small>{template.customized ? "已自定义" : "默认"}</small>
        </button>)}
        {!loading && templates.length === 0 && <p className="muted">没有可用模板。</p>}
      </aside>
      <section className="site-settings-card mail-template-editor">
        {loading && current === null ? <div className="empty-card">正在加载邮件模板…</div> : current === null || draft === null ? <button className="button secondary" type="button" onClick={() => void loadCatalog()}>重新加载</button> : <form className="form-stack" onSubmit={(event) => void save(event)}>
          <div className="section-heading"><div><h2>{current.label}</h2><p className="muted">{current.customized ? "当前使用自定义模板" : "当前使用系统默认模板"}</p></div><span className="count-pill">Revision {current.revision}</span></div>
          <label>邮件主题<input maxLength={255} required value={draft.subject} onChange={(event) => { setDraft({ ...draft, subject: event.target.value }); setPreview(null); setSuccess(""); }} /></label>
          <label>HTML 内容<textarea ref={contentRef} rows={18} maxLength={262144} required value={draft.content} onChange={(event) => { setDraft({ ...draft, content: event.target.value }); setPreview(null); setSuccess(""); }} /></label>
          <div className="mail-template-variables" aria-label="可用变量">
            <strong>插入变量</strong>
            {[...current.required_variables, ...current.optional_variables].map((variable) => <button key={variable} className="button ghost compact monospace" type="button" onClick={() => insertVariable(variable)}>{`{{${variable}}}`}{current.required_variables.includes(variable) ? " *" : ""}</button>)}
          </div>
          <p className="muted small">仅支持上列占位符；变量会自动转义，脚本、事件属性和危险链接会在服务端移除。标 * 的变量必须保留。</p>
          <div className="form-actions split"><div>{current.customized && <button className="button ghost" type="button" disabled={busy !== ""} onClick={() => void reset()}>{busy === "reset" ? "正在恢复…" : "恢复默认"}</button>}<button className="button secondary" type="button" disabled={busy !== ""} onClick={() => void renderPreview()}>{busy === "preview" ? "正在生成…" : "预览"}</button></div><button className="button primary" type="submit" disabled={busy !== "" || !dirty}>{busy === "save" ? "正在保存…" : "保存模板"}</button></div>
          <div className="mail-template-test-row"><label>测试收件人<input type="email" maxLength={320} placeholder="留空发送给当前管理员" value={recipient} onChange={(event) => setRecipient(event.target.value)} /></label><button className="button secondary" type="button" title={dirty ? "请先保存当前修改" : undefined} disabled={busy !== "" || dirty} onClick={() => void sendTest()}>{busy === "test" ? "正在发送…" : "发送测试邮件"}</button></div>
          {preview !== null && <section className="mail-template-preview" aria-label="邮件模板预览"><h3>{preview.subject}</h3><iframe title="邮件 HTML 预览" sandbox="" srcDoc={preview.html} /><details><summary>纯文本备用内容</summary><pre>{preview.text}</pre></details></section>}
        </form>}
      </section>
    </div>
  </main>;
}

function message(cause: unknown): string {
  return cause instanceof Error ? cause.message : "邮件模板请求失败";
}
