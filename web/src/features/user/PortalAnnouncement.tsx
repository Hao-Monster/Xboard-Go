import { useEffect, useState } from "react";
import type { Notice, NoticePage } from "../../lib/api";

export function PortalAnnouncement({ api, onOpen }: { api: { listVisibleNotices: (page?: number) => Promise<NoticePage> }; onOpen: () => void }) {
  const [notice, setNotice] = useState<Notice | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    void api.listVisibleNotices(1).then(result => {
      if (active) setNotice(result.items[0] ?? null);
    }).catch((cause: unknown) => {
      if (active) setError(cause instanceof Error ? cause.message : "公告加载失败");
    });
    return () => { active = false; };
  }, [api]);
  if (error) return <div className="alert error" role="alert">{error}<button className="button ghost" onClick={onOpen}>查看公告</button></div>;
  if (!notice) return null;
  return <button className="portal-announcement" onClick={onOpen} aria-label={`查看公告：${notice.title}`}>
    {notice.image_url && <img src={notice.image_url} alt="" referrerPolicy="no-referrer" />}
    <span className="portal-announcement-label">公告</span>
    <strong>{notice.title}</strong>
    <time dateTime={notice.updated_at}>{notice.updated_at.slice(0, 10)}</time>
  </button>;
}
