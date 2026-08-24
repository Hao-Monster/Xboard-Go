export interface Machine {
  id: number;
  name: string;
  notes: string;
  is_active: boolean;
  last_seen_at: string | null;
  load_status: MachineLoadStatus | null;
  servers_count: number;
  created_at: string;
  updated_at: string;
}

export interface MachineLoadStatus {
  cpu: number;
  mem: { total: number; used: number };
  swap?: { total: number; used: number };
  disk?: { total: number; used: number };
  net?: { in_speed: number; out_speed: number };
  updated_at: number;
}

export interface LoadHistory {
  id: number;
  machine_id: number;
  cpu: number;
  mem_total: number;
  mem_used: number;
  disk_total: number;
  disk_used: number;
  net_in_speed: number;
  net_out_speed: number;
  recorded_at: string;
}

export interface MachineEnrollment extends Machine {
  token: string;
  token_type: "enrollment_code";
  expires_at: string;
  install_command: string;
}

export interface Node {
  id: number;
  name: string;
  type: string;
  host: string;
  port: string;
  show: boolean;
  enabled: boolean;
  sort: number;
  machine_id: number | null;
  created_at: string;
  updated_at: string;
}

export interface ActivationSchedule {
  server_id: number;
  schedule_type: "daily" | "once";
  timezone: string;
  enable_time: string;
  disable_time: string;
  enable_at?: string | null;
  disable_at?: string | null;
  revision: string;
  next_transition_at: string | null;
  next_target_enabled: boolean;
  phase: "active" | "inactive";
}

export interface DailyScheduleInput {
  schedule_type: "daily";
  timezone: "Asia/Singapore";
  enable_time: string;
  disable_time: string;
}

export interface UserSession {
  id: number;
  email: string;
  is_admin: boolean;
}

export interface AccountSession {
  id: number;
  is_current: boolean;
  created_at: string;
  last_used_at: string | null;
  expires_at: string;
}

export interface AccountSecurityAPI {
  listAccountSessions: () => Promise<AccountSession[]>;
  revokeAccountSession: (id: number) => Promise<void>;
  changePassword: (oldPassword: string, newPassword: string) => Promise<void>;
}

export interface ServerGroup {
  id: number;
  name: string;
  users_count: number;
  server_count: number;
  created_at: string;
  updated_at: string;
}

export type RoutingAction = "block" | "direct" | "dns" | "proxy";

export interface RoutingRule {
  id: number;
  remarks: string;
  match: string[];
  action: RoutingAction;
  action_value?: string;
  created_at: string;
  updated_at: string;
}

export interface RoutingRuleInput {
  remarks: string;
  match: string[];
  action: RoutingAction;
  action_value: string;
}

export interface Notice {
  id: number;
  sort: number;
  title: string;
  content: string;
  image_url: string | null;
  tags: string[];
  show: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface NoticeInput {
  title: string;
  content: string;
  image_url: string;
  tags: string[];
  show: boolean;
}

export interface NoticePage {
  items: Notice[];
  total: number;
  page: number;
  page_size: number;
}

export interface ClientCatalogActionLinks {
  direct: string;
  qr: string;
  cloud: string;
  tutorial: string;
}

export interface AdminClientCatalogPlatform {
  platform: string;
  links: ClientCatalogActionLinks;
}

export interface AdminClientCatalogClient {
  id: string;
  name: string;
  core: string;
  platforms: AdminClientCatalogPlatform[];
}

export interface AdminClientCatalog {
  revision: number;
  clients: AdminClientCatalogClient[];
}

export type ClientCatalogOverrideInput = Record<string, Record<string, ClientCatalogActionLinks>>;

export interface ClientCatalogDownload {
  platform: string;
  source: string;
  download_url: string;
  cloud_url: string | null;
  tutorial_url: string | null;
}

export interface ClientCatalogEntry {
  id: string;
  name: string;
  core: string;
  featured: boolean;
  hwid: boolean;
  description: string;
  downloads: ClientCatalogDownload[];
}

export interface ClientCatalogQR {
  download_url: string;
  qr_code: string;
}

export interface AdminUser {
  id: number;
  email: string;
  is_admin: boolean;
  banned: boolean;
  group_id: number | null;
  transfer_enable: number;
  traffic_upload: number;
  traffic_download: number;
  expired_at: string | null;
  speed_limit: number;
  device_limit: number;
  online_count: number;
  last_online_at: string | null;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface AdminUserPage {
  items: AdminUser[];
  next_cursor?: string;
}

export interface AdminUserQuery {
  limit?: number;
  cursor?: string;
  email_prefix?: string;
  banned?: boolean;
  group_id?: number;
}

export interface AdminUserCreateInput {
  email: string;
  password: string;
  group_id: number | null;
  transfer_enable: number;
  expired_at: string | null;
  speed_limit: number;
  device_limit: number;
  banned: boolean;
}

export interface AdminUserUpdateInput extends Omit<AdminUserCreateInput, "password"> {
  revision: number;
}

export interface AdminAPI {
  listMachines: () => Promise<Machine[]>;
  createMachine: (input: { name: string; notes: string; is_active: boolean }) => Promise<MachineEnrollment>;
  updateMachine: (id: number, input: { name: string; notes: string; is_active: boolean }) => Promise<Machine>;
  deleteMachine: (id: number) => Promise<void>;
  createEnrollment: (machineID: number, revokeExisting: boolean) => Promise<Pick<MachineEnrollment, "token" | "token_type" | "expires_at" | "install_command">>;
  listMachineNodes: (machineID: number) => Promise<Node[]>;
  listLoadHistory: (machineID: number, rangeHours?: number, limit?: number) => Promise<LoadHistory[]>;
  listUnassignedNodes: () => Promise<Node[]>;
  assignNode: (machineID: number, nodeID: number) => Promise<void>;
  unassignNode: (machineID: number, nodeID: number) => Promise<void>;
  setNodeEnabled: (machineID: number, nodeID: number, enabled: boolean) => Promise<void>;
  getActivationSchedule: (nodeID: number) => Promise<ActivationSchedule>;
  saveActivationSchedule: (nodeID: number, input: DailyScheduleInput) => Promise<ActivationSchedule>;
  deleteActivationSchedule: (nodeID: number) => Promise<void>;
  listServerGroups: () => Promise<ServerGroup[]>;
  createServerGroup: (name: string) => Promise<ServerGroup>;
  updateServerGroup: (id: number, name: string) => Promise<ServerGroup>;
  deleteServerGroup: (id: number) => Promise<void>;
  listRoutingRules: () => Promise<RoutingRule[]>;
  createRoutingRule: (input: RoutingRuleInput) => Promise<RoutingRule>;
  updateRoutingRule: (id: number, input: RoutingRuleInput) => Promise<RoutingRule>;
  deleteRoutingRule: (id: number) => Promise<void>;
  listAdminUsers: (query?: AdminUserQuery) => Promise<AdminUserPage>;
  getAdminUser: (id: number) => Promise<AdminUser>;
  createAdminUser: (input: AdminUserCreateInput) => Promise<AdminUser>;
  updateAdminUser: (id: number, input: AdminUserUpdateInput) => Promise<AdminUser>;
  resetAdminUserPassword: (id: number, revision: number, newPassword: string) => Promise<AdminUser>;
  listNotices: () => Promise<Notice[]>;
  createNotice: (input: NoticeInput) => Promise<Notice>;
  updateNotice: (id: number, revision: number, input: NoticeInput) => Promise<Notice>;
  setNoticeVisibility: (id: number, revision: number, show: boolean) => Promise<Notice>;
  reorderNotices: (ids: number[]) => Promise<Notice[]>;
  deleteNotice: (id: number, revision: number) => Promise<void>;
  listClientCatalogAdmin: () => Promise<AdminClientCatalog>;
  saveClientCatalog: (revision: number, links: ClientCatalogOverrideInput) => Promise<AdminClientCatalog>;
}

interface Envelope<T> {
  status: "success";
  data: T;
}

interface ErrorEnvelope {
  status: "fail";
  error: {
    code: string;
    message: string;
    fields?: Record<string, string>;
  };
}

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fields?: Record<string, string>;

  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
    this.fields = fields;
  }
}

export class APIClient implements AdminAPI {
  async session(): Promise<UserSession> {
    return this.request<UserSession>("/api/v1/auth/session");
  }

  async login(email: string, password: string): Promise<UserSession> {
    return this.request<UserSession>("/api/v1/auth/login", { method: "POST", body: { email, password } });
  }

  async logout(): Promise<void> {
    await this.request<void>("/api/v1/auth/logout", { method: "POST", body: {} });
  }

  async listAccountSessions(): Promise<AccountSession[]> {
    return this.request<AccountSession[]>("/api/v1/auth/sessions");
  }

  async revokeAccountSession(id: number): Promise<void> {
    await this.request<void>(`/api/v1/auth/sessions/${id}`, { method: "DELETE" });
  }

  async changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await this.request<void>("/api/v1/auth/password", {
      method: "PUT",
      body: { old_password: oldPassword, new_password: newPassword }
    });
  }

  async listMachines(): Promise<Machine[]> {
    return this.request<Machine[]>("/api/v1/admin/machines");
  }

  async createMachine(input: { name: string; notes: string; is_active: boolean }): Promise<MachineEnrollment> {
    return this.request<MachineEnrollment>("/api/v1/admin/machines", { method: "POST", body: input });
  }

  async updateMachine(id: number, input: { name: string; notes: string; is_active: boolean }): Promise<Machine> {
    return this.request<Machine>(`/api/v1/admin/machines/${id}`, { method: "PATCH", body: input });
  }

  async deleteMachine(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${id}`, { method: "DELETE" });
  }

  async createEnrollment(machineID: number, revokeExisting: boolean): Promise<MachineEnrollment> {
    return this.request<MachineEnrollment>(`/api/v1/admin/machines/${machineID}/enrollments`, {
      method: "POST",
      body: { revoke_existing: revokeExisting }
    });
  }

  async listMachineNodes(machineID: number): Promise<Node[]> {
    return this.request<Node[]>(`/api/v1/admin/machines/${machineID}/nodes`);
  }

  async listLoadHistory(machineID: number, rangeHours = 1, limit = 60): Promise<LoadHistory[]> {
    return this.request<LoadHistory[]>(`/api/v1/admin/machines/${machineID}/history?range_hours=${rangeHours}&limit=${limit}`);
  }

  async listUnassignedNodes(): Promise<Node[]> {
    return this.request<Node[]>("/api/v1/admin/nodes/unassigned");
  }

  async assignNode(machineID: number, nodeID: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}`, { method: "PUT", body: {} });
  }

  async unassignNode(machineID: number, nodeID: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}`, { method: "DELETE" });
  }

  async setNodeEnabled(machineID: number, nodeID: number, enabled: boolean): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}/enabled`, {
      method: "PATCH",
      body: { enabled }
    });
  }

  async getActivationSchedule(nodeID: number): Promise<ActivationSchedule> {
    return this.request<ActivationSchedule>(`/api/v1/admin/nodes/${nodeID}/activation-schedule`);
  }

  async saveActivationSchedule(nodeID: number, input: DailyScheduleInput): Promise<ActivationSchedule> {
    return this.request<ActivationSchedule>(`/api/v1/admin/nodes/${nodeID}/activation-schedule`, { method: "PUT", body: input });
  }

  async deleteActivationSchedule(nodeID: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/nodes/${nodeID}/activation-schedule`, { method: "DELETE" });
  }

  async listServerGroups(): Promise<ServerGroup[]> {
    return this.request<ServerGroup[]>("/api/v1/admin/server-groups");
  }

  async createServerGroup(name: string): Promise<ServerGroup> {
    return this.request<ServerGroup>("/api/v1/admin/server-groups", { method: "POST", body: { name } });
  }

  async updateServerGroup(id: number, name: string): Promise<ServerGroup> {
    return this.request<ServerGroup>(`/api/v1/admin/server-groups/${id}`, { method: "PATCH", body: { name } });
  }

  async deleteServerGroup(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/server-groups/${id}`, { method: "DELETE" });
  }

  async listRoutingRules(): Promise<RoutingRule[]> {
    return this.request<RoutingRule[]>("/api/v1/admin/routing-rules");
  }

  async createRoutingRule(input: RoutingRuleInput): Promise<RoutingRule> {
    return this.request<RoutingRule>("/api/v1/admin/routing-rules", { method: "POST", body: input });
  }

  async updateRoutingRule(id: number, input: RoutingRuleInput): Promise<RoutingRule> {
    return this.request<RoutingRule>(`/api/v1/admin/routing-rules/${id}`, { method: "PATCH", body: input });
  }

  async deleteRoutingRule(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/routing-rules/${id}`, { method: "DELETE" });
  }

  async listAdminUsers(query: AdminUserQuery = {}): Promise<AdminUserPage> {
    const params = new URLSearchParams();
    if (query.limit !== undefined) params.set("limit", String(query.limit));
    if (query.cursor !== undefined && query.cursor !== "") params.set("cursor", query.cursor);
    if (query.email_prefix !== undefined && query.email_prefix !== "") params.set("email_prefix", query.email_prefix);
    if (query.banned !== undefined) params.set("banned", String(query.banned));
    if (query.group_id !== undefined) params.set("group_id", String(query.group_id));
    const suffix = params.size === 0 ? "" : `?${params.toString()}`;
    return this.request<AdminUserPage>(`/api/v1/admin/users${suffix}`);
  }

  async getAdminUser(id: number): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}`);
  }

  async createAdminUser(input: AdminUserCreateInput): Promise<AdminUser> {
    return this.request<AdminUser>("/api/v1/admin/users", { method: "POST", body: input });
  }

  async updateAdminUser(id: number, input: AdminUserUpdateInput): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}`, { method: "PATCH", body: input });
  }

  async resetAdminUserPassword(id: number, revision: number, newPassword: string): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}/password`, {
      method: "PUT", body: { revision, new_password: newPassword }
    });
  }

  async listNotices(): Promise<Notice[]> {
    return this.request<Notice[]>("/api/v1/admin/notices");
  }

  async createNotice(input: NoticeInput): Promise<Notice> {
    return this.request<Notice>("/api/v1/admin/notices", { method: "POST", body: input });
  }

  async updateNotice(id: number, revision: number, input: NoticeInput): Promise<Notice> {
    return this.request<Notice>(`/api/v1/admin/notices/${id}`, {
      method: "PATCH", body: { revision, ...input }
    });
  }

  async setNoticeVisibility(id: number, revision: number, show: boolean): Promise<Notice> {
    return this.request<Notice>(`/api/v1/admin/notices/${id}/visibility`, {
      method: "PATCH", body: { revision, show }
    });
  }

  async reorderNotices(ids: number[]): Promise<Notice[]> {
    return this.request<Notice[]>("/api/v1/admin/notices/order", { method: "PUT", body: { ids } });
  }

  async deleteNotice(id: number, revision: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/notices/${id}?revision=${revision}`, { method: "DELETE" });
  }

  async listVisibleNotices(page = 1): Promise<NoticePage> {
    return this.request<NoticePage>(`/api/v1/notices?page=${page}`);
  }

  async listClientCatalogAdmin(): Promise<AdminClientCatalog> {
    return this.request<AdminClientCatalog>("/api/v1/admin/client-catalog");
  }

  async saveClientCatalog(revision: number, links: ClientCatalogOverrideInput): Promise<AdminClientCatalog> {
    return this.request<AdminClientCatalog>("/api/v1/admin/client-catalog", { method: "PUT", body: { revision, links } });
  }

  async listClientCatalog(): Promise<ClientCatalogEntry[]> {
    return this.request<ClientCatalogEntry[]>("/api/v1/client-catalog");
  }

  async clientCatalogQR(client: string, platform: string): Promise<ClientCatalogQR> {
    const query = new URLSearchParams({ client, platform });
    return this.request<ClientCatalogQR>(`/api/v1/client-catalog/qr?${query.toString()}`);
  }

  private async request<T>(path: string, options: { method?: string; body?: unknown } = {}): Promise<T> {
    const method = options.method ?? "GET";
    const headers = new Headers({ Accept: "application/json" });
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }
    if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
      const csrf = readCookie("xboard_csrf");
      if (csrf !== null) {
        headers.set("X-CSRF-Token", csrf);
      }
    }

    const response = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      body: options.body === undefined ? undefined : JSON.stringify(options.body)
    });
    if (response.status === 204) {
      return undefined as T;
    }
    const payload = (await response.json()) as Envelope<T> | ErrorEnvelope;
    if (!response.ok || payload.status === "fail") {
      const error = payload.status === "fail" ? payload.error : { code: "request_failed", message: "请求失败" };
      throw new APIError(response.status, error.code, error.message, error.fields);
    }
    return payload.data;
  }
}

function readCookie(name: string): string | null {
  const prefix = `${encodeURIComponent(name)}=`;
  const item = document.cookie.split("; ").find((cookie) => cookie.startsWith(prefix));
  return item === undefined ? null : decodeURIComponent(item.slice(prefix.length));
}
