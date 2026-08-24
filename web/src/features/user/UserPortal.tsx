import { useState } from "react";

import type { ClientCatalogEntry, ClientCatalogQR, InvitationCode, InvitationSummary, KnowledgeArticle, KnowledgeLanguage, LoginLinkRedirect, NoticePage, Ticket, TicketInput, TicketPage, UserSession } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { UserKnowledgePage } from "../knowledge/UserKnowledgePage";
import { UserNoticesPage } from "../notices/UserNoticesPage";
import { UserTicketsPage } from "../tickets/UserTicketsPage";
import { BrandMark } from "../../components/BrandMark";
import { InvitationPage } from "../invitations/InvitationPage";

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
  getInvitations: () => Promise<InvitationSummary>;
  createInvitation: () => Promise<InvitationCode>;
  logout: () => Promise<void>;
}

export function UserPortal({ api, session, siteName, siteLogo, initialPage = "dashboard", onSignedOut }: {
  api: UserPortalAPI;
  session: UserSession;
  siteName: string;
  siteLogo: string | null;
  initialPage?: LoginLinkRedirect;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<"notices" | "knowledge" | "tickets" | "clients" | "invitations">(() => portalPage(initialPage));
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
      <div className="brand"><BrandMark appName={siteName} logo={siteLogo} /><span>{siteName}</span></div>
      <div className="admin-nav">
        <button className="nav-link" aria-current={page === "notices" ? "page" : undefined} onClick={() => setPage("notices")}>公告</button>
        <button className="nav-link" aria-current={page === "knowledge" ? "page" : undefined} onClick={() => setPage("knowledge")}>知识库</button>
        <button className="nav-link" aria-current={page === "tickets" ? "page" : undefined} onClick={() => setPage("tickets")}>我的工单</button>
        <button className="nav-link" aria-current={page === "clients" ? "page" : undefined} onClick={() => setPage("clients")}>客户端下载</button>
        <button className="nav-link" aria-current={page === "invitations" ? "page" : undefined} onClick={() => setPage("invitations")}>我的邀请</button>
      </div>
      <div className="account"><span>{session.email}</span><button className="button ghost compact" onClick={() => void logout()}>退出</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "notices" && <UserNoticesPage api={api} />}
    {page === "knowledge" && <UserKnowledgePage api={api} />}
    {page === "tickets" && <UserTicketsPage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
    {page === "invitations" && <InvitationPage api={api} />}
  </div>;
}

function portalPage(redirect: LoginLinkRedirect): "notices" | "knowledge" | "tickets" | "clients" | "invitations" {
  switch (redirect) {
    case "invite": return "invitations";
    case "knowledge": return "knowledge";
    case "ticket": return "tickets";
    case "subscribe": return "clients";
    default: return "notices";
  }
}
