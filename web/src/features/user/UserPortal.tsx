import { useState } from "react";

import type { ClientCatalogEntry, ClientCatalogQR, KnowledgeArticle, KnowledgeLanguage, NoticePage, Ticket, TicketInput, TicketPage, UserSession } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { UserNoticesPage } from "../notices/UserNoticesPage";
import { UserTicketsPage } from "../tickets/UserTicketsPage";

interface UserPortalAPI {
  listVisibleNotices: (page?: number) => Promise<NoticePage>;
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
  listKnowledge: (language: KnowledgeLanguage, keyword?: string) => Promise<KnowledgeArticle[]>;
  getKnowledge: (id: number) => Promise<KnowledgeArticle>;
  listTickets: (page?: number, pageSize?: number) => Promise<TicketPage>;
  createTicket: (input: TicketInput) => Promise<Ticket>;
  getTicket: (id: number) => Promise<Ticket>;
  replyTicket: (id: number, message: string) => Promise<Ticket>;
  closeTicket: (id: number) => Promise<Ticket>;
  logout: () => Promise<void>;
}

export function UserPortal({ api, session, onSignedOut }: {
  api: UserPortalAPI;
  session: UserSession;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<"notices" | "knowledge" | "tickets" | "clients">("notices");
  const [logoutError, setLogoutError] = useState("");

  const logout = async () => {
    setLogoutError("");
    try {
      await api.logout();
      onSignedOut();
    } catch (cause) {
      setLogoutError(cause instanceof Error ? cause.message : "退出失败");
    }
  };

  return <div className="app-frame">
    <nav className="topbar" aria-label="用户导航">
      <div className="brand"><span className="brand-mark">X</span><span>Xboard-Go</span></div>
      <div className="admin-nav">
        <button className="nav-link" aria-current={page === "notices" ? "page" : undefined} onClick={() => setPage("notices")}>公告</button>
        <button className="nav-link" aria-current={page === "knowledge" ? "page" : undefined} onClick={() => setPage("knowledge")}>知识库</button>
        <button className="nav-link" aria-current={page === "tickets" ? "page" : undefined} onClick={() => setPage("tickets")}>我的工单</button>
        <button className="nav-link" aria-current={page === "clients" ? "page" : undefined} onClick={() => setPage("clients")}>客户端下载</button>
      </div>
      <div className="account"><span>{session.email}</span><button className="button ghost compact" onClick={() => void logout()}>退出</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "notices" && <UserNoticesPage api={api} />}
    {page === "knowledge" && <UserKnowledgePage api={api} />}
    {page === "tickets" && <UserTicketsPage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
  </div>;
}
