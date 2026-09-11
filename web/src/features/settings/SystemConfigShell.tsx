import { useEffect, useState, type ReactNode } from "react";

import type { AdminAPI, SiteSettings } from "../../lib/api";
import { SiteSettingsPage } from "./SiteSettingsPage";
import { SubscriptionSettingsPage } from "./SubscriptionSettingsPage";
import { CommissionSettingsPage } from "./CommissionSettingsPage";
import { NodeAgentSettingsPage } from "./NodeAgentSettingsPage";
import { EmailSettingsPage } from "./EmailSettingsPage";
import { TelegramSettingsPage } from "./TelegramSettingsPage";
import { ClientAppSettingsPage } from "./ClientAppSettingsPage";

export type SystemConfigTab =
  | "site"
  | "security"
  | "subscriptions"
  | "commissions"
  | "node-settings"
  | "mail"
  | "telegram"
  | "client-app"
  | "templates";

interface TabItem {
  key: SystemConfigTab;
  label: string;
  buttonName: string; // for accessible name compatibility with tests
  icon: () => ReactNode;
}

function IconSite() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="18" height="18" x="3" y="3" rx="2" /><path d="M3 9h18" /><path d="M9 21V9" />
    </svg>
  );
}

function IconSecurity() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="18" height="11" x="3" y="11" rx="2" ry="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" />
    </svg>
  );
}

function IconSubscription() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M21 12a9 9 0 0 0-9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" /><path d="M3 3v5h5" /><path d="M3 12a9 9 0 0 0 9 9 9.75 9.75 0 0 0 6.74-2.74L21 16" /><path d="M16 21h5v-5" />
    </svg>
  );
}

function IconCommission() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="8" cy="8" r="6" /><path d="M18.09 10.37A6 6 0 1 1 10.34 18" /><path d="m7 6 2 4" /><path d="m17 14 2 4" />
    </svg>
  );
}

function IconNodeConfig() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="20" height="8" x="2" y="2" rx="2" ry="2" /><rect width="20" height="8" x="2" y="14" rx="2" ry="2" /><line x1="6" x2="6.01" y1="6" y2="6" /><line x1="6" x2="6.01" y1="18" y2="18" />
    </svg>
  );
}

function IconMail() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="20" height="16" x="2" y="4" rx="2" /><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7" />
    </svg>
  );
}

function IconTelegram() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="m22 2-7 20-4-9-9-4Z" /><path d="M22 2 11 13" />
    </svg>
  );
}

function IconApp() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect width="14" height="20" x="5" y="2" rx="2" ry="2" /><path d="M12 18h.01" />
    </svg>
  );
}

function IconTemplate() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M14.5 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7.5L14.5 2z" /><polyline points="14 2 14 8 20 8" /><path d="m10 13-2 2 2 2" /><path d="m14 17 2-2-2-2" />
    </svg>
  );
}

const configTabs: TabItem[] = [
  { key: "site", label: "站点设置", buttonName: "站点设置", icon: IconSite },
  { key: "security", label: "安全设置", buttonName: "安全设置", icon: IconSecurity },
  { key: "subscriptions", label: "订阅设置", buttonName: "订阅设置", icon: IconSubscription },
  { key: "commissions", label: "邀请&佣金设置", buttonName: "佣金设置", icon: IconCommission },
  { key: "node-settings", label: "节点配置", buttonName: "节点配置", icon: IconNodeConfig },
  { key: "mail", label: "邮件设置", buttonName: "邮件设置", icon: IconMail },
  { key: "telegram", label: "Telegram设置", buttonName: "Telegram 设置", icon: IconTelegram },
  { key: "client-app", label: "APP设置", buttonName: "客户端版本", icon: IconApp },
  { key: "templates", label: "订阅模板", buttonName: "订阅模板", icon: IconTemplate }
];

export function SystemConfigShell({
  api,
  activeTab = "site",
  onTabChange,
  onBeforeTabChange,
  onIdentityChanged,
  onSecurePathChanged,
  onClientAppDirtyChange
}: {
  api: AdminAPI;
  activeTab?: SystemConfigTab;
  onTabChange?: (tab: SystemConfigTab) => void;
  onBeforeTabChange?: () => boolean;
  onIdentityChanged?: (settings: SiteSettings) => void;
  onSecurePathChanged?: (nextPath: string) => void;
  onClientAppDirtyChange?: (dirty: boolean) => void;
}) {
  const [currentTab, setCurrentTab] = useState<SystemConfigTab>(activeTab);

  useEffect(() => {
    setCurrentTab(activeTab);
  }, [activeTab]);

  const handleSelectTab = (nextTab: SystemConfigTab) => {
    if (nextTab === currentTab || onBeforeTabChange?.() === false) return;
    setCurrentTab(nextTab);
    onTabChange?.(nextTab);
  };

  const tab = currentTab;

  return (
    <div className="system-config-page">
      <header className="page-header system-page-header">
        <div>
          <h1 className="system-page-title">系统设置</h1>
          <p className="system-page-desc">管理系统核心配置，包括站点、安全、订阅、邀请佣金、节点、邮件和通知等设置</p>
        </div>
      </header>

      <div className="system-config-container">
        {/* Left 200px vertical sub-tabs */}
        <nav className="system-tabs-nav" aria-label="系统配置子导航">
          {configTabs.map((item) => {
            const Icon = item.icon;
            const isActive = tab === item.key;
            return (
              <button
                key={item.key}
                type="button"
                className={`system-tab-button ${isActive ? "active" : ""}`}
                aria-current={isActive ? "page" : undefined}
                aria-label={item.buttonName}
                onClick={() => handleSelectTab(item.key)}
              >
                <span className="system-tab-icon">
                  <Icon />
                </span>
                <span className="system-tab-label">{item.label}</span>
              </button>
            );
          })}
        </nav>

        {/* Right content card */}
        <div className="system-content-area">
          {(tab === "site" || tab === "security") && (
            <SiteSettingsPage
              api={api}
              activeSubTab={tab}
              onIdentityChanged={onIdentityChanged ?? (() => undefined)}
              onSecurePathChanged={onSecurePathChanged}
            />
          )}
          {tab === "subscriptions" && <SubscriptionSettingsPage api={api} displayMode="settings" />}
          {tab === "commissions" && <CommissionSettingsPage api={api} />}
          {tab === "node-settings" && <NodeAgentSettingsPage api={api} />}
          {tab === "mail" && <EmailSettingsPage api={api} />}
          {tab === "telegram" && <TelegramSettingsPage api={api} />}
          {tab === "client-app" && <ClientAppSettingsPage api={api} onDirtyChange={onClientAppDirtyChange} />}
          {tab === "templates" && <SubscriptionSettingsPage api={api} displayMode="templates" />}
        </div>
      </div>
    </div>
  );
}
