import { useEffect, useMemo, useState, type FormEvent } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { KnowledgeArticle, KnowledgeLanguage } from "../../lib/api";

interface UserKnowledgeAPI {
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
}

export function UserKnowledgePage({ api }: { api: UserKnowledgeAPI }) {
  const [language, setLanguage] = useState<KnowledgeLanguage>("zh-CN");
  const [keyword, setKeyword] = useState("");
  const [appliedKeyword, setAppliedKeyword] = useState("");
  const [requestKey, setRequestKey] = useState(0);
  const [articles, setArticles] = useState<KnowledgeArticle[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<KnowledgeArticle | null>(null);
  const [reading, setReading] = useState<number | null>(null);

  useEffect(() => {
    let live = true;
    void api.listKnowledge(language, appliedKeyword).then((result) => { if (live) setArticles(result); }).catch((cause: unknown) => { if (live) setError(messageOf(cause)); }).finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api, language, appliedKeyword, requestKey]);

  const grouped = useMemo(() => {
    const result = new Map<string, KnowledgeArticle[]>();
    for (const article of articles) result.set(article.category, [...(result.get(article.category) ?? []), article]);
    return Array.from(result.entries());
  }, [articles]);
  const search = (event: FormEvent) => { event.preventDefault(); setLoading(true); setError(""); setAppliedKeyword(keyword.trim()); setRequestKey((current) => current + 1); };
  const read = async (article: KnowledgeArticle) => {
    setReading(article.id); setError("");
    try { setSelected(await api.getKnowledge(article.id)); } catch (cause) { setError(messageOf(cause)); } finally { setReading(null); }
  };

  return <main className="page-shell knowledge-feed-page"><header className="page-header"><div><p className="eyebrow">Guides</p><h1>知识库</h1><p className="muted">查找客户端安装、订阅与使用指南。</p></div></header>
    <form className="resource-toolbar knowledge-toolbar" onSubmit={search}><label>语言<select value={language} onChange={(event) => { setLoading(true); setError(""); setLanguage(event.target.value as KnowledgeLanguage); }}><option value="zh-CN">简体中文</option><option value="zh-TW">繁體中文</option><option value="en-US">English</option><option value="ja-JP">日本語</option><option value="ko-KR">한국어</option><option value="vi-VN">Tiếng Việt</option><option value="ru-RU">Русский</option></select></label><label>搜索知识<input type="search" value={keyword} onChange={(event) => setKeyword(event.target.value)} /></label><button className="button secondary" type="submit">搜索</button></form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">正在加载知识库…</div> : grouped.length === 0 ? <div className="empty-card">没有匹配的知识文章。</div> : <div className="knowledge-categories">{grouped.map(([category, items]) => <section className="knowledge-category-card" key={category}><h2>{category}</h2><div className="knowledge-links">{items.map((article) => <button key={article.id} className="knowledge-link" aria-label={`阅读：${article.title}`} disabled={reading === article.id} onClick={() => void read(article)}><span>{article.title}</span><time dateTime={article.updated_at}>{formatDate(article.updated_at)}</time></button>)}</div></section>)}</div>}
    {selected !== null && <KnowledgeReader article={selected} onClose={() => setSelected(null)} />}
  </main>;
}

function KnowledgeReader({ article, onClose }: { article: KnowledgeArticle; onClose: () => void }) {
  const body = (article.body ?? "").replace(/<div class="v2board-no-access">(.*?)<\/div>/gs, "> $1");
  return <Modal title={article.title} onClose={onClose}><div className="modal-header"><div><p className="eyebrow">{article.category}</p><h2>{article.title}</h2></div><button className="icon-button" aria-label={`关闭${article.title}`} onClick={onClose}>×</button></div><div className="knowledge-reader-meta"><time dateTime={article.updated_at}>更新于 {formatDate(article.updated_at)}</time><a className="button ghost compact" href={article.share_url} target="_blank" rel="noopener noreferrer">公开分享</a></div>
    <div className="markdown-body"><Markdown components={{ a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; }, img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; } }}>{body}</Markdown></div></Modal>;
}

function formatDate(value: string) { return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeZone: "Asia/Singapore" }).format(new Date(value)); }
function messageOf(cause: unknown) { return cause instanceof Error ? cause.message : "知识库加载失败"; }
