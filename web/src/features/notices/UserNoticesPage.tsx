import { useEffect, useState } from "react";
import Markdown from "react-markdown";

import type { NoticePage } from "../../lib/api";

interface UserNoticeAPI {
  listVisibleNotices: (page?: number) => Promise<NoticePage>;
}

export function UserNoticesPage({ api }: { api: UserNoticeAPI }) {
  const [page, setPage] = useState(1);
  const [result, setResult] = useState<NoticePage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    void api.listVisibleNotices(page).then((data) => {
      if (live) setResult(data);
    }).catch((cause: unknown) => {
      if (live) setError(cause instanceof Error ? cause.message : "公告加载失败");
    }).finally(() => {
      if (live) setLoading(false);
    });
    return () => { live = false; };
  }, [api, page]);

  const totalPages = Math.max(1, Math.ceil((result?.total ?? 0) / (result?.page_size ?? 5)));
  const changePage = (nextPage: number) => {
    setLoading(true);
    setError("");
    setPage(nextPage);
  };

  return <main className="page-shell notice-feed-page">
      <header className="page-header"><div><p className="eyebrow">Updates</p><h1>公告</h1><p className="muted">查看服务更新与重要通知。</p></div></header>
      {error !== "" && <div className="alert error resource-alert" role="alert">{error}</div>}
      {loading ? <div className="empty-card">正在加载公告…</div> : result === null || result.items.length === 0 ? <div className="empty-card">本页没有公告。</div> : (
        <section className="notice-feed" aria-label="公告列表">{result.items.map((notice) => <article className="notice-card" key={notice.id}>
          {notice.image_url !== null && <img className="notice-cover" src={notice.image_url} alt="" loading="lazy" referrerPolicy="no-referrer" />}
          <div className="notice-card-body">
            <div className="notice-card-heading"><div><h2>{notice.title}</h2><time dateTime={notice.updated_at}>{formatDate(notice.updated_at)}</time></div><div className="notice-tags">{notice.tags.map((tag) => <span className="count-pill" key={tag}>{tag}</span>)}</div></div>
            <div className="markdown-body"><Markdown components={{
              a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; },
              img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; }
            }}>{notice.content}</Markdown></div>
          </div>
        </article>)}</section>
      )}
      {result !== null && result.total > result.page_size && <nav className="notice-pagination" aria-label="公告分页">
        <button className="button secondary" disabled={page <= 1 || loading} onClick={() => changePage(page - 1)}>上一页</button>
        <span>第 {page} / {totalPages} 页</span>
        <button className="button secondary" disabled={page >= totalPages || loading} onClick={() => changePage(page + 1)}>下一页</button>
      </nav>}
  </main>;
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeZone: "Asia/Singapore" }).format(new Date(value));
}
