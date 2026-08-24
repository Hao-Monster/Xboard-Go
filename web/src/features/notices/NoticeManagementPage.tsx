import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, Notice, NoticeInput } from "../../lib/api";

type NoticesAPI = Pick<AdminAPI,
  "listNotices" | "createNotice" | "updateNotice" | "setNoticeVisibility" | "reorderNotices" | "deleteNotice"
>;

export function NoticeManagementPage({ api }: { api: NoticesAPI }) {
  const [notices, setNotices] = useState<Notice[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Notice | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<Notice | null>(null);
  const [ordering, setOrdering] = useState<Notice[] | null>(null);
  const [togglingID, setTogglingID] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setNotices(await api.listNotices());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let live = true;
    void api.listNotices().then((result) => {
      if (live) setNotices(result);
    }).catch((cause: unknown) => {
      if (live) setError(errorMessage(cause));
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api]);

  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (query === "") return notices;
    return notices.filter((notice) => notice.title.toLocaleLowerCase().includes(query));
  }, [notices, search]);

  const toggleVisibility = async (notice: Notice) => {
    setTogglingID(notice.id);
    setError("");
    try {
      const saved = await api.setNoticeVisibility(notice.id, notice.revision, !notice.show);
      setNotices((current) => current.map((item) => item.id === saved.id ? saved : item));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setTogglingID(null);
    }
  };

  return <main className="page-shell resource-page">
    <header className="page-header">
      <div>
        <p className="eyebrow">Content</p>
        <h1>公告管理</h1>
        <p className="muted">创建、编辑、显隐和排序用户公告；正文支持安全的 Markdown 展示。</p>
      </div>
      <div className="action-group wrap">
        <button className="button secondary" disabled={notices.length === 0} onClick={() => setOrdering([...notices])}>编辑排序</button>
        <button className="button primary" onClick={() => setEditing(null)}>添加公告</button>
      </div>
    </header>

    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>刷新</button></div>}
    <div className="resource-toolbar">
      <label>搜索公告标题<input type="search" aria-label="搜索公告标题" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
    </div>

    {loading ? <div className="empty-card">正在加载公告…</div> : notices.length === 0 ? (
      <div className="empty-card">暂无公告。</div>
    ) : filtered.length === 0 ? (
      <div className="empty-card">没有匹配的公告。</div>
    ) : (
      <section className="resource-table-wrap" aria-label="公告列表">
        <table className="resource-table notice-table">
          <thead><tr><th>标题</th><th>显示状态</th><th>标签</th><th>排序</th><th>操作</th></tr></thead>
          <tbody>{filtered.map((notice) => <tr key={notice.id}>
            <td data-label="标题"><strong>{notice.title}</strong><small className="muted monospace">ID {notice.id}</small></td>
            <td data-label="显示状态"><button
              className={`button compact ${notice.show ? "secondary" : "ghost"}`}
              aria-label={`${notice.show ? "隐藏" : "显示"}公告：${notice.title}`}
              disabled={togglingID === notice.id}
              onClick={() => void toggleVisibility(notice)}
            >{togglingID === notice.id ? "正在更新…" : notice.show ? "已显示" : "已隐藏"}</button></td>
            <td data-label="标签"><div className="notice-tags">{notice.tags.length === 0 ? <span className="muted">—</span> : notice.tags.map((tag) => <span className="count-pill" key={tag}>{tag}</span>)}</div></td>
            <td data-label="排序"><span className="monospace">{notice.sort}</span></td>
            <td data-label="操作"><div className="row-actions">
              <button className="button secondary compact" aria-label={`编辑公告：${notice.title}`} onClick={() => setEditing(notice)}>编辑</button>
              <button className="button ghost compact danger-text" aria-label={`删除公告：${notice.title}`} onClick={() => setDeleting(notice)}>删除</button>
            </div></td>
          </tr>)}</tbody>
        </table>
      </section>
    )}

    {editing !== undefined && <NoticeEditor api={api} notice={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => {
      setNotices((current) => editing === null ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
      setEditing(undefined);
    }} />}
    {deleting !== null && <NoticeDelete api={api} notice={deleting} onClose={() => setDeleting(null)} onDeleted={() => {
      setNotices((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
    }} />}
    {ordering !== null && <NoticeOrderEditor api={api} notices={ordering} onClose={() => setOrdering(null)} onSaved={(saved) => {
      setNotices(saved);
      setOrdering(null);
    }} />}
  </main>;
}

function NoticeEditor({ api, notice, onClose, onSaved }: {
  api: NoticesAPI; notice: Notice | null; onClose: () => void; onSaved: (notice: Notice) => void;
}) {
  const title = notice === null ? "添加公告" : "编辑公告";
  const [headline, setHeadline] = useState(notice?.title ?? "");
  const [content, setContent] = useState(notice?.content ?? "");
  const [imageURL, setImageURL] = useState(notice?.image_url ?? "");
  const [tags, setTags] = useState(notice?.tags.join(", ") ?? "");
  const [show, setShow] = useState(notice?.show ?? false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setSaving(true);
    setError("");
    const input: NoticeInput = {
      title: headline,
      content,
      image_url: imageURL,
      tags: parseTags(tags),
      show
    };
    try {
      onSaved(notice === null ? await api.createNotice(input) : await api.updateNotice(notice.id, notice.revision, input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  };

  return <Modal title={title} onClose={onClose}>
    <ModalHeader title={title} onClose={onClose} />
    <p className="muted small">发布或编辑系统公告，支持 Markdown 格式；原始 HTML 不会执行。</p>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>标题<input value={headline} maxLength={255} required onChange={(event) => setHeadline(event.target.value)} /></label>
      <label>公告内容<textarea value={content} maxLength={262144} required onChange={(event) => setContent(event.target.value)} /></label>
      <label>公告背景图片 URL<input type="url" value={imageURL} maxLength={2048} placeholder="https://" onChange={(event) => setImageURL(event.target.value)} /></label>
      <label>节点标签<input value={tags} placeholder="多个标签使用逗号分隔" onChange={(event) => setTags(event.target.value)} /></label>
      <label className="switch-label"><input type="checkbox" checked={show} onChange={(event) => setShow(event.target.checked)} />显示给用户</label>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在保存…" : "保存"}</button></div>
    </form>
  </Modal>;
}

function NoticeOrderEditor({ api, notices, onClose, onSaved }: {
  api: NoticesAPI; notices: Notice[]; onClose: () => void; onSaved: (notices: Notice[]) => void;
}) {
  const [ordered, setOrdered] = useState(notices);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const move = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= ordered.length) return;
    setOrdered((current) => {
      const next = [...current];
      const sourceNotice = next[index];
      const targetNotice = next[target];
      if (sourceNotice === undefined || targetNotice === undefined) return current;
      next[index] = targetNotice;
      next[target] = sourceNotice;
      return next;
    });
  };
  const save = async () => {
    setSaving(true);
    setError("");
    try {
      onSaved(await api.reorderNotices(ordered.map((notice) => notice.id)));
    } catch (cause) {
      setError(errorMessage(cause));
      setSaving(false);
    }
  };
  return <Modal title="编辑公告排序" onClose={onClose}>
    <ModalHeader title="编辑公告排序" onClose={onClose} />
    <ol className="notice-order-list">{ordered.map((notice, index) => <li key={notice.id}>
      <span><strong>{notice.title}</strong><small className="muted monospace">ID {notice.id}</small></span>
      <span className="row-actions">
        <button className="button ghost compact" aria-label={`上移：${notice.title}`} disabled={index === 0} onClick={() => move(index, -1)}>↑</button>
        <button className="button ghost compact" aria-label={`下移：${notice.title}`} disabled={index === ordered.length - 1} onClick={() => move(index, 1)}>↓</button>
      </span>
    </li>)}</ol>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary" disabled={saving} onClick={() => void save()}>{saving ? "正在保存…" : "保存排序"}</button></div>
  </Modal>;
}

function NoticeDelete({ api, notice, onClose, onDeleted }: {
  api: NoticesAPI; notice: Notice; onClose: () => void; onDeleted: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const remove = async () => {
    setBusy(true);
    setError("");
    try {
      await api.deleteNotice(notice.id, notice.revision);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  };
  return <Modal title="删除公告" onClose={onClose}>
    <ModalHeader title="删除公告" onClose={onClose} />
    <p>确定删除“{notice.title}”吗？此操作不能撤销。</p>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}
    <div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : "确认删除"}</button></div>
  </Modal>;
}

function ModalHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>;
}

function parseTags(value: string): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const raw of value.split(/[,，]/u)) {
    const tag = raw.trim();
    if (tag !== "" && !seen.has(tag)) {
      seen.add(tag);
      result.push(tag);
    }
  }
  return result;
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : "请求失败，请稍后重试";
}
