import { useState } from "react";

import type { AdminAPI } from "../../lib/api";
import { MailSettingsPage } from "./MailSettingsPage";
import { MailTemplateSettingsPage } from "./MailTemplateSettingsPage";

export function EmailSettingsPage({ api }: { api: AdminAPI }) {
  const [tab, setTab] = useState<"settings" | "templates">("settings");

  return <div className="email-settings-shell">
    <div className="email-settings-tabs" role="tablist" aria-label="邮件管理">
      <button type="button" role="tab" aria-selected={tab === "settings"} className={tab === "settings" ? "active" : ""} onClick={() => setTab("settings")}>邮件设置</button>
      <button type="button" role="tab" aria-selected={tab === "templates"} className={tab === "templates" ? "active" : ""} onClick={() => setTab("templates")}>邮件模板</button>
    </div>
    {tab === "settings" ? <MailSettingsPage api={api} /> : <MailTemplateSettingsPage api={api} />}
  </div>;
}
