import { useEffect, useMemo, useState, type FormEvent } from "react";
import Markdown from "react-markdown";

import { Modal } from "../../components/Overlay";
import type { KnowledgeArticle, KnowledgeLanguage } from "../../lib/api";
import { distributorCloseLabel, distributorCopy, type DistributorLocale } from "../distributor/locale";

interface UserKnowledgeAPI {
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
}

export function UserKnowledgePage({ api, locale = "zh-CN", fixedLocale = false }: { api: UserKnowledgeAPI; locale?: KnowledgeLanguage; fixedLocale?: boolean }) {
  const [language, setLanguage] = useState<KnowledgeLanguage>(locale);
  const [keyword, setKeyword] = useState("");
  const [appliedKeyword, setAppliedKeyword] = useState("");
  const [requestKey, setRequestKey] = useState(0);
  const [articles, setArticles] = useState<KnowledgeArticle[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selected, setSelected] = useState<KnowledgeArticle | null>(null);
  const [reading, setReading] = useState<number | null>(null);
  const activeLanguage = fixedLocale ? locale : language;
  const uiLocale: DistributorLocale = activeLanguage === "en-US" ? "en-US" : "zh-CN";
  const copy = distributorCopy[uiLocale];
  const labels = fixedLocale ? {
    title: copy.knowledgeTitle, subtitle: copy.knowledgeSubtitle, search: copy.knowledgeSearch,
    loading: copy.loadingKnowledge, empty: copy.noArticles, read: copy.read, updated: copy.updatedAt
  } : uiLocale === "en-US" ? {
    title: "Knowledge base", subtitle: "Find client installation, subscription, and usage guides.", search: "Search knowledge",
    loading: "Loading knowledge base…", empty: "No matching knowledge article.", read: "Read", updated: "Updated"
  } : {
    title: "知识库", subtitle: "查找客户端安装、订阅与使用指南。", search: "搜索知识",
    loading: "正在加载知识库…", empty: "没有匹配的知识文章。", read: "阅读", updated: "更新于"
  };

  useEffect(() => {
    let live = true;
    void api.listKnowledge(activeLanguage, appliedKeyword).then((result) => { if (live) setArticles(result); }).catch((cause: unknown) => { if (live) setError(messageOf(cause, uiLocale)); }).finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [api, activeLanguage, appliedKeyword, requestKey, uiLocale]);

  const grouped = useMemo(() => {
    const result = new Map<string, KnowledgeArticle[]>();
    for (const article of articles) result.set(article.category, [...(result.get(article.category) ?? []), article]);
    return Array.from(result.entries());
  }, [articles]);
  const search = (event: FormEvent) => { event.preventDefault(); setLoading(true); setError(""); setAppliedKeyword(keyword.trim()); setRequestKey((current) => current + 1); };
  const read = async (article: KnowledgeArticle) => {
    setReading(article.id); setError("");
    try { setSelected(await api.getKnowledge(article.id)); } catch (cause) { setError(messageOf(cause, uiLocale)); } finally { setReading(null); }
  };

  return <main className="page-shell knowledge-feed-page"><header className="page-header"><div><p className="eyebrow">Guides</p><h1>{labels.title}</h1><p className="muted">{labels.subtitle}</p></div></header>
    <form className="resource-toolbar knowledge-toolbar" onSubmit={search}>{!fixedLocale && <label>{uiLocale === "en-US" ? "Language" : "语言"}<select value={language} onChange={(event) => { setLoading(true); setError(""); setLanguage(event.target.value as KnowledgeLanguage); }}><option value="zh-CN">简体中文</option><option value="zh-TW">繁體中文</option><option value="en-US">English</option><option value="ja-JP">日本語</option><option value="ko-KR">한국어</option><option value="vi-VN">Tiếng Việt</option><option value="ru-RU">Русский</option></select></label>}<label>{labels.search}<input type="search" aria-label={labels.search} value={keyword} onChange={(event) => setKeyword(event.target.value)} /></label><button className="button secondary" type="submit">{copy.search}</button></form>
    {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
    {loading ? <div className="empty-card">{labels.loading}</div> : grouped.length === 0 ? <div className="empty-card">{labels.empty}</div> : <div className="knowledge-categories">{grouped.map(([category, items]) => <section className="knowledge-category-card" key={category}><h2>{category}</h2><div className="knowledge-links">{items.map((article) => <button key={article.id} className="knowledge-link" aria-label={`${labels.read}${uiLocale === "zh-CN" ? "：" : ": "}${article.title}`} disabled={reading === article.id} onClick={() => void read(article)}><span>{article.title}</span><time dateTime={article.updated_at}>{labels.updated}: {formatDate(article.updated_at, uiLocale)}</time></button>)}</div></section>)}</div>}
    {selected !== null && <KnowledgeReader article={selected} locale={uiLocale} onClose={() => setSelected(null)} />}
  </main>;
}

function KnowledgeReader({ article, locale, onClose }: { article: KnowledgeArticle; locale: DistributorLocale; onClose: () => void }) {
  const body = (article.body ?? "").replace(/<div class="v2board-no-access">(.*?)<\/div>/gs, "> $1");
  const copy = distributorCopy[locale];
  return <Modal title={article.title} onClose={onClose}><div className="modal-header"><div><p className="eyebrow">{article.category}</p><h2>{article.title}</h2></div><button className="icon-button" aria-label={distributorCloseLabel(locale, article.title)} onClick={onClose}>×</button></div><div className="knowledge-reader-meta"><time dateTime={article.updated_at}>{copy.updatedAt}: {formatDate(article.updated_at, locale)}</time><a className="button ghost compact" href={article.share_url} target="_blank" rel="noopener noreferrer">{copy.publicShare}</a></div>
    <div className="markdown-body"><SafeKnowledgeMarkdown body={body} /></div></Modal>;
}

export function SafeKnowledgeMarkdown({ body }: { body: string }) {
  const videos = /<video controls preload="metadata" src="([^"<>]+)"><\/video>/g;
  const parts: Array<{ markdown?: string; video?: string }> = [];
  let cursor = 0;
  for (const match of body.matchAll(videos)) {
    const index = match.index;
    const source = match[1];
    if (index === undefined || source === undefined || !safeAttachmentVideoURL(source)) continue;
    if (index > cursor) parts.push({ markdown: body.slice(cursor, index) });
    parts.push({ video: source });
    cursor = index + match[0].length;
  }
  if (cursor < body.length) parts.push({ markdown: body.slice(cursor) });
  return <>{parts.map((part, index) => part.video === undefined
    ? <Markdown key={`markdown-${index}`} components={{ a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; }, img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; } }}>{part.markdown ?? ""}</Markdown>
    : <video key={`video-${index}`} src={part.video} controls preload="metadata" />)}</>;
}

function safeAttachmentVideoURL(value: string): boolean {
  try {
    const parsed = new URL(value, window.location.origin);
    if (parsed.origin !== window.location.origin || !/^\/(?:knowledge|guide)-attachments\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(parsed.pathname)) return false;
    if (parsed.pathname.startsWith("/knowledge-attachments/")) {
      return /^\d+$/.test(parsed.searchParams.get("expires") ?? "") && parsed.searchParams.get("disposition") === "inline" && /^[0-9a-f]{64}$/.test(parsed.searchParams.get("signature") ?? "");
    }
    return parsed.search === "";
  } catch {
    return false;
  }
}

function formatDate(value: string, locale: DistributorLocale) { return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone: "Asia/Singapore" }).format(new Date(value)); }
function messageOf(cause: unknown, locale: DistributorLocale) { return cause instanceof Error ? cause.message : locale === "en-US" ? "Knowledge base request failed" : "知识库加载失败"; }
