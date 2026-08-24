import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";

import { Modal } from "../../components/Overlay";
import type { AdminAPI, KnowledgeArticle, KnowledgeInput, KnowledgeLanguage } from "../../lib/api";

type KnowledgeAdminAPI = Pick<AdminAPI,
  "listKnowledgeAdmin" | "getKnowledgeAdmin" | "listKnowledgeCategories" | "createKnowledge" | "updateKnowledge" |
  "setKnowledgeVisibility" | "reorderKnowledge" | "deleteKnowledge"
>;

const languages: Array<{ value: KnowledgeLanguage; label: string }> = [
  { value: "en-US", label: "English" }, { value: "ja-JP", label: "日本語" }, { value: "ko-KR", label: "한국어" },
  { value: "vi-VN", label: "Tiếng Việt" }, { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" }, { value: "ru-RU", label: "Русский" }
];

export function KnowledgeManagementPage({ api }: { api: KnowledgeAdminAPI }) {
  const [articles, setArticles] = useState<KnowledgeArticle[]>([]);
  const [categories, setCategories] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<KnowledgeArticle | null | undefined>(undefined);
  const [deleting, setDeleting] = useState<KnowledgeArticle | null>(null);
  const [ordering, setOrdering] = useState<KnowledgeArticle[] | null>(null);
  const [busyID, setBusyID] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [nextArticles, nextCategories] = await Promise.all([api.listKnowledgeAdmin(), api.listKnowledgeCategories()]);
      setArticles(nextArticles);
      setCategories(nextCategories);
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setLoading(false);
    }
  }, [api]);

  useEffect(() => {
    let live = true;
    void Promise.all([api.listKnowledgeAdmin(), api.listKnowledgeCategories()]).then(([nextArticles, nextCategories]) => {
      if (live) { setArticles(nextArticles); setCategories(nextCategories); }
    }).catch((cause: unknown) => { if (live) setError(messageOf(cause)); }).finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api]);

  const filtered = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    if (query === "") return articles;
    return articles.filter((article) => article.title.toLocaleLowerCase().includes(query) || article.category.toLocaleLowerCase().includes(query));
  }, [articles, search]);

  const edit = async (article: KnowledgeArticle) => {
    setBusyID(article.id);
    setError("");
    try {
      setEditing(await api.getKnowledgeAdmin(article.id));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusyID(null);
    }
  };

  const toggle = async (article: KnowledgeArticle) => {
    setBusyID(article.id);
    setError("");
    try {
      const saved = await api.setKnowledgeVisibility(article.id, article.revision, !article.show);
      setArticles((current) => current.map((item) => item.id === saved.id ? saved : item));
    } catch (cause) {
      setError(messageOf(cause));
    } finally {
      setBusyID(null);
    }
  };

  return <main className="page-shell resource-page">
    <header className="page-header"><div><p className="eyebrow">Content</p><h1>知识库管理</h1><p className="muted">创建、分类、多语言发布、订阅区块和公开分享知识文章。</p></div>
      <div className="action-group wrap"><button className="button secondary" disabled={articles.length === 0} onClick={() => setOrdering([...articles])}>编辑排序</button><button className="button primary" onClick={() => setEditing(null)}>添加知识</button></div>
    </header>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}<button className="button ghost compact" onClick={() => void refresh()}>刷新</button></div>}
    <div className="resource-toolbar"><label>搜索知识<input type="search" aria-label="搜索知识" value={search} onChange={(event) => setSearch(event.target.value)} /></label></div>
    {loading ? <div className="empty-card">正在加载知识库…</div> : articles.length === 0 ? <div className="empty-card">暂无知识文章。</div> : filtered.length === 0 ? <div className="empty-card">没有匹配的知识文章。</div> :
      <section className="resource-table-wrap" aria-label="知识文章列表"><table className="resource-table"><thead><tr><th>标题</th><th>状态</th><th>分类</th><th>语言</th><th>排序</th><th>操作</th></tr></thead>
        <tbody>{filtered.map((article) => <tr key={article.id}>
          <td data-label="标题"><strong>{article.title}</strong><small className="muted monospace">ID {article.id}</small></td>
          <td data-label="状态"><button className={`button compact ${article.show ? "secondary" : "ghost"}`} aria-label={`${article.show ? "隐藏" : "显示"}知识：${article.title}`} disabled={busyID === article.id} onClick={() => void toggle(article)}>{article.show ? "已显示" : "已隐藏"}</button></td>
          <td data-label="分类">{article.category}</td><td data-label="语言">{languageLabel(article.language)}</td><td data-label="排序" className="monospace">{article.sort}</td>
          <td data-label="操作"><div className="row-actions"><button className="button secondary compact" aria-label={`编辑知识：${article.title}`} disabled={busyID === article.id} onClick={() => void edit(article)}>{busyID === article.id ? "加载中…" : "编辑"}</button>
            {article.show && <a className="button ghost compact" href={article.share_url} target="_blank" rel="noopener noreferrer">分享</a>}
            <button className="button ghost compact danger-text" aria-label={`删除知识：${article.title}`} onClick={() => setDeleting(article)}>删除</button></div></td>
        </tr>)}</tbody></table></section>}

    {editing !== undefined && <KnowledgeEditor api={api} article={editing} categories={categories} onClose={() => setEditing(undefined)} onSaved={(saved) => {
      setArticles((current) => editing === null ? [saved, ...current] : current.map((item) => item.id === saved.id ? saved : item));
      setCategories((current) => current.includes(saved.category) ? current : [...current, saved.category].sort());
      setEditing(undefined);
    }} />}
    {ordering !== null && <KnowledgeOrderEditor api={api} articles={ordering} onClose={() => setOrdering(null)} onSaved={(saved) => { setArticles(saved); setOrdering(null); }} />}
    {deleting !== null && <KnowledgeDelete api={api} article={deleting} onClose={() => setDeleting(null)} onDeleted={() => { setArticles((current) => current.filter((item) => item.id !== deleting.id)); setDeleting(null); }} />}
  </main>;
}

function KnowledgeEditor({ api, article, categories, onClose, onSaved }: { api: KnowledgeAdminAPI; article: KnowledgeArticle | null; categories: string[]; onClose: () => void; onSaved: (saved: KnowledgeArticle) => void }) {
  const dialogTitle = article === null ? "添加知识" : "编辑知识";
  const [title, setTitle] = useState(article?.title ?? "");
  const [category, setCategory] = useState(article?.category ?? "");
  const [language, setLanguage] = useState<KnowledgeLanguage>(article?.language ?? "zh-CN");
  const [body, setBody] = useState(article?.body ?? "");
  const [show, setShow] = useState(article?.show ?? false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: FormEvent) => {
    event.preventDefault(); setSaving(true); setError("");
    const input: KnowledgeInput = { title, category, language, body, show };
    try { onSaved(article === null ? await api.createKnowledge(input) : await api.updateKnowledge(article.id, article.revision, input)); }
    catch (cause) { setError(messageOf(cause)); setSaving(false); }
  };
  const insertProtectedRegion = () => setBody((current) => `${current}${current.trim() === "" ? "" : "\n\n"}<!--access start-->\n订阅用户专属内容\n<!--access end-->`);

  return <Modal title={dialogTitle} onClose={onClose}><DialogHeader title={dialogTitle} onClose={onClose} /><p className="muted small">发布或编辑知识库文章，支持多语言和 Markdown 格式。</p>
    <form className="form-stack" onSubmit={(event) => void submit(event)}>
      <label>标题<input name="title" required maxLength={255} value={title} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>分类<input name="category" required maxLength={255} list="knowledge-categories" value={category} onChange={(event) => setCategory(event.target.value)} /><datalist id="knowledge-categories">{categories.map((item) => <option key={item} value={item} />)}</datalist></label>
      <label>语言<select value={language} onChange={(event) => setLanguage(event.target.value as KnowledgeLanguage)}>{languages.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
      <label className="switch-label"><input type="checkbox" checked={show} onChange={(event) => setShow(event.target.checked)} />显示</label>
      <label>内容<textarea name="body" required maxLength={1_048_576} value={body} onChange={(event) => setBody(event.target.value)} /></label>
      <div className="editor-toolbar"><button className="button secondary compact" type="button" onClick={insertProtectedRegion}>插入订阅专属区块</button><span className="muted small">区块仅对有效订阅用户显示。</span></div>
      {error !== "" && <div className="alert error" role="alert">{error}</div>}
      <div className="form-actions"><button className="button ghost" type="button" onClick={onClose}>取消</button><button className="button primary" type="submit" disabled={saving}>{saving ? "正在提交…" : "提交"}</button></div>
    </form></Modal>;
}

function KnowledgeOrderEditor({ api, articles, onClose, onSaved }: { api: KnowledgeAdminAPI; articles: KnowledgeArticle[]; onClose: () => void; onSaved: (saved: KnowledgeArticle[]) => void }) {
  const [ordered, setOrdered] = useState(articles); const [saving, setSaving] = useState(false); const [error, setError] = useState("");
  const move = (index: number, offset: -1 | 1) => setOrdered((current) => {
    const target = index + offset; if (target < 0 || target >= current.length) return current;
    const next = [...current]; const source = next[index]; const destination = next[target]; if (source === undefined || destination === undefined) return current;
    next[index] = destination; next[target] = source; return next;
  });
  const save = async () => { setSaving(true); setError(""); try { onSaved(await api.reorderKnowledge(ordered.map((item) => item.id))); } catch (cause) { setError(messageOf(cause)); setSaving(false); } };
  return <Modal title="编辑知识排序" onClose={onClose}><DialogHeader title="编辑知识排序" onClose={onClose} /><ol className="notice-order-list">{ordered.map((article, index) => <li key={article.id}><span><strong>{article.title}</strong><small className="muted">{article.category}</small></span><span className="row-actions"><button className="button ghost compact" aria-label={`上移：${article.title}`} disabled={index === 0} onClick={() => move(index, -1)}>↑</button><button className="button ghost compact" aria-label={`下移：${article.title}`} disabled={index === ordered.length - 1} onClick={() => move(index, 1)}>↓</button></span></li>)}</ol>
    {error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary" disabled={saving} onClick={() => void save()}>{saving ? "正在保存…" : "保存排序"}</button></div></Modal>;
}

function KnowledgeDelete({ api, article, onClose, onDeleted }: { api: KnowledgeAdminAPI; article: KnowledgeArticle; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const remove = async () => { setBusy(true); setError(""); try { await api.deleteKnowledge(article.id, article.revision); onDeleted(); } catch (cause) { setError(messageOf(cause)); setBusy(false); } };
  return <Modal title="删除知识" onClose={onClose}><DialogHeader title="删除知识" onClose={onClose} /><p>确定永久删除“{article.title}”吗？此操作不能撤销。</p>{error !== "" && <div className="alert error" role="alert">{error}</div>}<div className="form-actions"><button className="button ghost" onClick={onClose}>取消</button><button className="button primary destructive" disabled={busy} onClick={() => void remove()}>{busy ? "正在删除…" : "确认删除"}</button></div></Modal>;
}

function DialogHeader({ title, onClose }: { title: string; onClose: () => void }) { return <div className="modal-header"><h2>{title}</h2><button className="icon-button" aria-label={`关闭${title}`} onClick={onClose}>×</button></div>; }
function languageLabel(language: KnowledgeLanguage) { return languages.find((item) => item.value === language)?.label ?? language; }
function messageOf(cause: unknown) { return cause instanceof Error ? cause.message : "请求失败，请稍后重试"; }
