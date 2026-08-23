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
