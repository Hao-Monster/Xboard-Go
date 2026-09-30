import { translateAdmin } from "../../lib/adminLocale";
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { TagInput } from "../../components/TagInput";
import { MarkdownEditor } from "../../components/MarkdownEditor";
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

        <h1>{translateAdmin("公告管理")}</h1>
        <p className="muted">{translateAdmin("在这里可以配置公告，包括添加、删除、编辑等操作。")}</p>
      </div>
      <div className="action-group wrap">
      </div>
    </header>

    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>{translateAdmin("刷新")}</button></div>}
    <div className="resource-toolbar">
      <button className="button primary" onClick={() => setEditing(null)}>{translateAdmin("添加公告")}</button><input type="search" aria-label="搜索公告标题" placeholder={translateAdmin("搜索公告标题...")} value={search} onChange={(event) => setSearch(event.target.value)} />
    </div>

      <section className="resource-table-wrap" aria-label="公告列表">
        <table className="resource-table notice-table">
          <thead><tr><th>ID</th><th>{translateAdmin("状态")}</th><th>{translateAdmin("标题")}</th><th>{translateAdmin("操作")}</th></tr></thead>
          <tbody>{filtered.map((notice) => <tr key={notice.id}>
            <td data-label="ID">{notice.id}</td>
            <td data-label="显示状态"><button
              className={`button compact ${notice.show ? "secondary" : "ghost"}`}
              aria-label={`${notice.show ? "隐藏" : translateAdmin("显示")}公告：${notice.title}`}
              disabled={togglingID === notice.id}
              onClick={() => void toggleVisibility(notice)}
            >{togglingID === notice.id ? "正在更新…" : notice.show ? "已显示" : "已隐藏"}</button></td>
            <td data-label="标题"><strong>{notice.title}</strong></td>
            <td data-label="操作"><div className="row-actions">
              <button className="button secondary compact" aria-label={`编辑公告：${notice.title}`} onClick={() => setEditing(notice)}>{translateAdmin("编辑")}</button>
              <button className="button ghost compact danger-text" aria-label={`删除公告：${notice.title}`} onClick={() => setDeleting(notice)}>{translateAdmin("删除")}</button>
            </div></td>
          </tr>)}{filtered.length === 0 && <tr><td colSpan={4}><div className="empty-card">{loading ? "正在加载公告…" : notices.length === 0 ? "暂无公告。" : "没有匹配的公告。"}</div></td></tr>}</tbody>
        </table>
      </section>

    {editing !== undefined && <NoticeEditor api={api} notice={editing} onClose={() => setEditing(undefined)} onSaved={(saved) => {
      setNotices((current) => editing === null ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
      setEditing(undefined);
    }} />}
    {deleting !== null && <NoticeDelete api={api} notice={deleting} onClose={() => setDeleting(null)} onDeleted={() => {
      setNotices((current) => current.filter((item) => item.id !== deleting.id));
      setDeleting(null);
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
    <p className="muted small">发布或编辑系统公告，支持 Markdown 格式。</p>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>{translateAdmin("标题")}<input autoFocus placeholder={translateAdmin("请输入公告标题")} value={headline} maxLength={255} required onChange={(event) => setHeadline(event.target.value)} /></label>
      <div className="form-stack"><span>{translateAdmin("公告内容")}</span><MarkdownEditor label={translateAdmin("公告内容")} value={content} maxLength={262144} required onChange={setContent} /></div>
      <label>公告背景图片 URL<input type="url" value={imageURL} maxLength={2048} placeholder="https://" onChange={(event) => setImageURL(event.target.value)} /></label>
      <div className="form-stack"><span>{translateAdmin("节点标签")}</span><TagInput label={translateAdmin("节点标签")} value={parseTags(tags)} onChange={tags => setTags(tags.join(", "))} /></div>
      <label className="switch-label"><input type="checkbox" role="switch" checked={show} onChange={(event) => setShow(event.target.checked)} />{translateAdmin("显示")}</label>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在提交…" : translateAdmin("提交")}</button></div>
    </form>
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
    <div className="form-actions"><button className="button ghost" onClick={onClose}>{translateAdmin("取消")}</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : translateAdmin("确认删除")}</button></div>
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
