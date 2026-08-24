import { useState } from "react";

import type { ClientCatalogEntry, ClientCatalogQR, NoticePage, UserSession } from "../../lib/api";
import { ClientCatalogPage } from "../clients/ClientCatalogPage";
import { UserNoticesPage } from "../notices/UserNoticesPage";

interface UserPortalAPI {
  listVisibleNotices: (page?: number) => Promise<NoticePage>;
  listClientCatalog: () => Promise<ClientCatalogEntry[]>;
  clientCatalogQR: (client: string, platform: string) => Promise<ClientCatalogQR>;
  logout: () => Promise<void>;
}

export function UserPortal({ api, session, onSignedOut }: {
  api: UserPortalAPI;
  session: UserSession;
  onSignedOut: () => void;
}) {
  const [page, setPage] = useState<"notices" | "clients">("notices");
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
        <button className="nav-link" aria-current={page === "clients" ? "page" : undefined} onClick={() => setPage("clients")}>客户端下载</button>
      </div>
      <div className="account"><span>{session.email}</span><button className="button ghost compact" onClick={() => void logout()}>退出</button></div>
    </nav>
    {logoutError !== "" && <div className="alert error global-alert" role="alert">{logoutError}</div>}
    {page === "notices" && <UserNoticesPage api={api} />}
    {page === "clients" && <ClientCatalogPage api={api} />}
  </div>;
}
