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
  revision: number;
  name: string;
  type: string;
  host: string;
  port: string;
  show: boolean;
  enabled: boolean;
  sort: number;
  rate: number;
  traffic_upload: number;
  traffic_download: number;
  runtime_configured: boolean;
  last_check_at: string | null;
  last_push_at: string | null;
  machine_id: number | null;
  created_at: string;
  updated_at: string;
}

export interface AdminNode extends Node {
  machine_name: string | null;
  group_ids: number[];
  online_count: number;
}

export interface AdminNodePage {
  items: AdminNode[];
  total: number;
  page: number;
  page_size: number;
}

export interface AdminNodeQuery {
  page?: number;
  page_size?: number;
  q?: string;
  type?: string;
  show?: boolean;
  enabled?: boolean;
  machine_id?: number;
  unassigned?: boolean;
}

export interface AdminNodeUpdateInput {
  revision: number;
  name: string;
  host: string;
  port: string;
  show: boolean;
  enabled: boolean;
  sort: number;
  machine_id: number | null;
}

export interface AdminNodeRevision {
  id: number;
  revision: number;
}

export interface AdminNodeStateInput {
  targets: AdminNodeRevision[];
  show?: boolean;
  enabled?: boolean;
  machine_id?: number | null;
}

export interface AdminNodeMutation {
  node_ids: number[];
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
  is_staff?: boolean;
  is_distributor?: boolean;
  distributor_name?: string | null;
}

export type LoginLinkRedirect = "dashboard" | "invite" | "knowledge" | "ticket" | "subscribe";

export interface LoginLinkExchange extends UserSession {
  redirect: LoginLinkRedirect;
}

export interface AccountSession {
  id: number;
  is_current: boolean;
  created_at: string;
  last_used_at: string | null;
  expires_at: string;
}

export interface AccountAccessToken {
  id: number;
  name: string;
  is_current: boolean;
  created_at: string;
  updated_at: string;
  last_used_at: string | null;
  expires_at: string | null;
}

export interface IssuedAccessToken extends Pick<AccountAccessToken, "id" | "name" | "created_at" | "expires_at"> {
  token: string;
  token_type: "Bearer";
}

export interface AccountSecurityAPI {
  listAccountSessions: () => Promise<AccountSession[]>;
  revokeAccountSession: (id: number) => Promise<void>;
  listAccessTokens: () => Promise<AccountAccessToken[]>;
  createAccessToken: (name: string, expiresAt: string | null) => Promise<IssuedAccessToken>;
  revokeAccessToken: (id: number) => Promise<void>;
  revokeAllAccessTokens: () => Promise<void>;
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

export type PlanPeriod = "monthly" | "quarterly" | "half_yearly" | "yearly" | "two_yearly" | "three_yearly" | "onetime" | "reset_traffic";
export type PlanPrices = Partial<Record<PlanPeriod, number>>;

export interface PlanDetails {
  id: number;
  group_id: number | null;
  transfer_enable: number;
  name: string;
  speed_limit: number | null;
  show: boolean;
  sort: number;
  renew: boolean;
  content: string;
  reset_traffic_method: number | null;
  capacity_limit: number | null;
  prices: PlanPrices;
  sell: boolean;
  device_limit: number | null;
  tags: string[];
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface Plan extends PlanDetails {
  users_count: number;
  active_users_count: number;
  capacity_users_count: number;
}

export interface PlanInput {
  group_id: number | null;
  transfer_enable: number;
  name: string;
  speed_limit: number | null;
  content: string;
  reset_traffic_method: number | null;
  capacity_limit: number | null;
  prices: PlanPrices;
  device_limit: number | null;
  tags: string[];
}

export interface PlanOffer extends PlanDetails {
  capacity_remaining: number | null;
  can_purchase: boolean;
  can_renew: boolean;
}

export type OrderStatus = 0 | 1 | 2 | 3 | 4;
export type OrderType = 1 | 2 | 3 | 4;

export interface Order {
  id: number;
  user_id: number;
  plan_id: number;
  payment_id: number | null;
  period: PlanPeriod;
  trade_no: string;
  original_amount: number;
  total_amount: number;
  handling_amount: number | null;
  balance_amount: number;
  surplus_credit: number;
  surplus_amount: number;
  type: OrderType;
  status: OrderStatus;
  surplus_order_ids: number[];
  coupon_id: number | null;
  commission_status: number | null;
  invite_user_id: number | null;
  actual_commission_balance: number | null;
  commission_rate: number | null;
  commission_auto_check: boolean | null;
  commission_balance: number;
  discount_amount: number;
  paid_at: string | null;
  callback_no: string | null;
  entitlement_expired_at_before: string | null;
  entitlement_expired_at_after: string | null;
  created_at: string;
  updated_at: string;
  plan?: PlanDetails;
}

export interface AdminOrder extends Order {
  user_email: string;
  plan_name: string;
}

export interface AdminOrderDetail extends AdminOrder {
	invite_user: { id: number; email: string } | null;
	commission_log: CommissionLog[];
	subscribe_url: string | null;
}

export interface AdminOrderPage {
  items: AdminOrder[];
  total: number;
  page: number;
  page_size: number;
}

export type DistributorDeliveryStatus = 0 | 1 | 2;
export type DistributorSettlementStatus = 0 | 1;

export interface DistributorSubscription {
	id: number;
	original_order_id: number;
	trade_no: string;
	distributor_user_id: number;
	customer_name: string | null;
	remark: string | null;
	delivery_status: DistributorDeliveryStatus;
	settlement_status: DistributorSettlementStatus;
	config_issued_at: string | null;
	connected_at: string | null;
	connected_node_id: number | null;
	connected_node_name: string | null;
	claimed_at: string | null;
	closed_at: string | null;
	hwid_enabled: boolean;
	hwid_limit: number;
	revision: number;
	created_at: string;
	updated_at: string;
}

export interface DistributorEntitlement {
	plan_id: number;
	plan_name: string;
	transfer_enable: number;
	used_traffic: number;
	remaining_traffic: number;
	expired_at: string | null;
	speed_limit: number;
	device_limit: number;
}

export interface DistributorOrder {
	order: Order;
	plan_name: string;
	distributor_email?: string;
	distributor_name?: string;
	subscription: DistributorSubscription;
	settlement_status: DistributorSettlementStatus;
	subscription_entitlement: DistributorEntitlement;
	bound_devices: string[];
	is_subscription_origin: boolean;
	can_view_subscription_qr: boolean;
	can_renew: boolean;
}

export interface DistributorOrderPage {
	items: DistributorOrder[];
	total: number;
	page: number;
	page_size: number;
}

export interface DistributorOrderQuery {
	page?: number;
	page_size?: number;
	search?: string;
	settlement_status?: DistributorSettlementStatus;
	distributor_user_id?: number;
}

export interface DistributorQR {
	trade_no: string;
	customer_name: string | null;
	qr_code: string;
	hwid_enabled: boolean;
	hwid_devices: string[];
}

export interface DistributorHWIDSettings {
	enabled: boolean;
	limit: number;
	registered_count: number;
}

export interface DistributorHWIDDevice {
	id: number;
	hwid: string;
	device_os: string | null;
	os_version: string | null;
	device_model: string | null;
	user_agent: string | null;
	ip_address: string | null;
	first_seen_at: string;
	last_seen_at: string;
}

export interface AdminDistributorOrderDetail {
	order: DistributorOrder;
	hwid: DistributorHWIDSettings;
	subscribe_url: string;
}

export interface DistributorSettlementSummary {
	count: number;
	total_amount: number;
	settled_at: string | null;
}

export interface AdminOrderQuery {
  page?: number;
  page_size?: number;
  status?: OrderStatus;
  type?: OrderType;
  period?: PlanPeriod;
	statuses?: OrderStatus[];
	types?: OrderType[];
	periods?: PlanPeriod[];
	commission_statuses?: Array<0 | 1 | 2 | 3>;
  query?: string;
	sort_by?: "created_at" | "total_amount" | "status" | "commission_balance" | "commission_status";
	sort_desc?: boolean;
}

export interface AssignOrderInput {
  email: string;
  plan_id: number;
  period: PlanPeriod;
  total_amount: number;
}

export type PaymentProvider = "AlipayF2F" | "BTCPay" | "CoinPayments" | "Coinbase" | "EPay" | "MGate";

export interface PaymentConfigField {
  key: string;
  label: string;
  type: "text" | "url" | "password" | "textarea";
  description?: string;
  required: boolean;
  secret: boolean;
  options?: string[];
}

export interface PaymentProviderDefinition {
  provider: PaymentProvider;
  label: string;
  fields: PaymentConfigField[];
}

export interface PaymentMethod {
  id: number;
  uuid: string;
  payment: PaymentProvider;
  name: string;
  icon?: string;
  notify_domain?: string;
  handling_fee_fixed: number;
  handling_fee_basis_points: number;
  enable: boolean;
  sort: number;
  revision: number;
  created_at: string;
  updated_at: string;
  config: Record<string, string>;
  configured_fields: string[];
  notify_url: string;
}

export interface UserPaymentMethod {
  id: number;
  name: string;
  payment: PaymentProvider;
  icon?: string;
  handling_fee_fixed: number;
  handling_fee_basis_points: number;
}

export interface PaymentMethodInput {
  revision?: number;
  payment: PaymentProvider;
  name: string;
  icon: string;
  notify_domain: string;
  handling_fee_fixed: number;
  handling_fee_basis_points: number;
  enable: boolean;
  config: Record<string, string>;
  clear_config_fields?: string[];
}

export interface PaymentPage {
  items: PaymentMethod[];
  total: number;
  page: number;
  page_size: number;
}

export interface PaymentCheckout {
  type: 0 | 1;
  data: string;
  qr_code?: string;
  payment_id: number;
  handling_amount: number;
  total_amount: number;
}

export type CouponType = 1 | 2;

export interface Coupon {
  id: number;
  code: string;
  name: string;
  type: CouponType;
  value: number;
  show: boolean;
  limit_use: number | null;
  limit_use_with_user: number | null;
  limit_plan_ids: number[];
  limit_period: PlanPeriod[];
  started_at: string;
  ended_at: string;
  created_at: string;
  updated_at: string;
}

export interface CouponQuote {
  coupon: Coupon;
  original_amount: number;
  coupon_discount_amount: number;
  total_after_coupon: number;
}

export interface CouponPage {
  items: Coupon[];
  total: number;
  page: number;
  page_size: number;
}

export interface CouponQuery {
  page?: number;
  page_size?: number;
  query?: string;
  type?: CouponType;
  show?: boolean;
  sort?: "id" | "name" | "type" | "code" | "limit_use" | "started_at" | "ended_at" | "created_at";
  desc?: boolean;
}

export interface CouponInput {
  code: string;
  name: string;
  type: CouponType;
  value: number;
  show: boolean;
  limit_use: number | null;
  limit_use_with_user: number | null;
  limit_plan_ids: number[];
  limit_period: PlanPeriod[];
  started_at: number;
  ended_at: number;
}

export type GiftCardType = 1 | 2 | 3;
export type GiftCardCodeStatus = 0 | 1 | 2 | 3;

export interface GiftCardReward {
  balance?: number;
  transfer_enable?: number;
  expire_days?: number;
  device_limit?: number;
  reset_package?: boolean;
  plan_id?: number | null;
  plan_validity_days?: number;
  random_rewards?: Array<{ weight: number; rewards: Omit<GiftCardReward, "random_rewards" | "plan_id" | "plan_validity_days"> }>;
}

export interface GiftCardConditions {
  new_user_max_days?: number | null;
  new_user_only?: boolean;
  paid_user_only?: boolean;
  require_invite?: boolean;
  allowed_plans?: number[];
  disallowed_plans?: number[];
}

export interface GiftCardLimits {
  max_use_per_user?: number;
  cooldown_hours?: number;
  invite_reward_basis_points?: number;
}

export interface GiftCardSpecialConfig {
  started_at?: string | null;
  ended_at?: string | null;
  festival_multiplier_basis_points?: number;
}

export interface GiftCardTemplate {
  id: number;
  name: string;
  description: string;
  type: GiftCardType;
  status: boolean;
  conditions: GiftCardConditions;
  rewards: GiftCardReward;
  limits: GiftCardLimits;
  special_config: GiftCardSpecialConfig;
  icon: string;
  background_image: string;
  theme: string;
  sort: number;
  admin_id: number;
  revision: number;
  created_at: string;
  updated_at: string;
}

export type GiftCardTemplateInput = Omit<GiftCardTemplate, "id" | "admin_id" | "revision" | "created_at" | "updated_at"> & { revision?: number };

export interface GiftCardCode {
  id: number;
  template_id: number;
  template_name?: string;
  code: string;
  batch_no: string;
  status: GiftCardCodeStatus;
  user_id: number | null;
  used_at: string | null;
  expires_at: string | null;
  actual_rewards?: GiftCardReward;
  usage_count: number;
  max_usage: number;
  created_at: string;
  updated_at: string;
}

export interface GiftCardUsage {
  id: number;
  code_id: number;
  code?: string;
  template_id: number;
  template_name?: string;
  template_type?: GiftCardType;
  user_id: number;
  user_email?: string;
  inviter_id: number | null;
  inviter_email?: string;
  rewards: GiftCardReward;
  inviter_rewards: GiftCardReward;
  user_level_at_use?: number | null;
  user_plan_id: number | null;
  multiplier_basis_points: number;
  used_at: string;
}

export interface GiftCardPage<T> { items: T[]; total: number; page: number; page_size: number }
export type GiftCardTemplatePage = GiftCardPage<GiftCardTemplate>;
export type GiftCardCodePage = GiftCardPage<GiftCardCode>;
export type GiftCardUsagePage = GiftCardPage<GiftCardUsage>;
export interface GiftCardStatistics {
  template_total: number;
  active_templates: number;
  code_total: number;
  used_codes: number;
  usage_total: number;
  daily_usages: Array<{ date: string; count: number }>;
  type_stats: Array<{ type: GiftCardType; count: number }>;
}
export interface GiftCardPreview { template: GiftCardTemplate; code_info: GiftCardCode; reward_preview: GiftCardReward; can_redeem: boolean; reason: string }
export interface GiftCardRedeemResult { message: string; rewards: GiftCardReward; invite_rewards: GiftCardReward; template_name: string; usage: GiftCardUsage }
export interface GiftCardCodeInput { code?: string; status?: GiftCardCodeStatus; expires_at?: number | null; max_usage?: number }

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

export type KnowledgeLanguage = "en-US" | "ja-JP" | "ko-KR" | "vi-VN" | "zh-CN" | "zh-TW" | "ru-RU";

export interface KnowledgeArticle {
  id: number;
  language: KnowledgeLanguage;
  category: string;
  title: string;
  body?: string;
  sort: number;
  show: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
  share_url: string;
}

export interface KnowledgeInput {
  language: KnowledgeLanguage;
  category: string;
  title: string;
  body: string;
  show: boolean;
  draft_token?: string;
}

export interface KnowledgeAttachmentUpload {
  upload_uuid: string;
  original_name: string;
  declared_size: number;
  chunk_size: number;
  total_chunks: number;
  received_chunks: number;
  uploaded_chunks: number[];
  status: "initialized" | "uploading" | "completing" | "completed" | "failed" | "expired";
  expires_at: number;
}

export interface KnowledgeAttachment {
  uuid: string;
  knowledge_id: number | null;
  original_name: string;
  mime_type: string;
  extension: string | null;
  size: number;
  sha256: string;
  status: "ready";
  disposition: "inline" | "attachment";
  url: string;
  placeholder: string;
  created_at: number;
}

export interface KnowledgeAttachmentPage {
  items: KnowledgeAttachment[];
  total: number;
  page: number;
  per_page: number;
}

export interface KnowledgeAttachmentChunkResult {
  accepted_index: number;
  idempotent: boolean;
  received_chunks: number;
  ready_to_complete: boolean;
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
  is_staff?: boolean;
  is_distributor?: boolean;
  distributor_name?: string | null;
  banned: boolean;
  group_id: number | null;
  group_name: string | null;
  plan_id: number | null;
  plan_name: string | null;
  invite_user_id: number | null;
  invite_user_email: string | null;
  transfer_enable: number;
  traffic_upload: number;
  traffic_download: number;
  traffic_used: number;
  expired_at: string | null;
  speed_limit: number;
  device_limit: number;
  online_count: number;
  last_online_at: string | null;
  last_login_at: string | null;
  balance: number;
  commission_type: number;
  commission_rate: number | null;
  commission_balance: number;
  discount: number | null;
  next_reset_at: string | null;
  last_reset_at: string | null;
  reset_count: number;
  telegram_id: number | null;
  remind_expire: boolean;
  remind_traffic: boolean;
  remarks: string | null;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface AdminUserPage {
  items: AdminUser[];
  next_cursor?: string;
  total?: number;
  page?: number;
  page_size?: number;
}

export type AdminUserBulkScopeName = "selected" | "filtered" | "all";
export type AdminUserBulkKind = "mail" | "csv" | "ban";
export type AdminUserBulkStatus = "queued" | "running" | "cancelling" | "cancelled" | "succeeded" | "failed";

export interface AdminUserBulkScopeInput {
  scope: AdminUserBulkScopeName;
  user_ids?: number[];
  email_prefix?: string;
  banned?: boolean;
  group_id?: number;
  filters?: AdminUserFilter[];
}

export interface AdminUserBulkJob {
  id: string;
  kind: AdminUserBulkKind;
  scope: AdminUserBulkScopeName;
  administrator_id: number | null;
  administrator_email: string;
  status: AdminUserBulkStatus;
  subject?: string;
  app_name?: string;
  app_url?: string;
  total_count: number;
  processed_count: number;
  success_count: number;
  failure_count: number;
  skipped_count: number;
  cancelled_count: number;
  output_filename?: string;
  output_size?: number;
  output_sha256?: string;
  output_expires_at?: string;
  last_error?: string;
  started_at?: string;
  completed_at?: string;
  cancelled_at?: string;
  created_at: string;
  updated_at: string;
}

export interface AdminUserBulkJobPage {
  items: AdminUserBulkJob[];
  total: number;
  page: number;
  page_size: number;
}

export interface AdminUserSubscriptionURL {
	subscribe_url: string;
}

export interface AdminUserTrafficReset {
	user_id: number;
	email: string;
	upload_before: number;
	download_before: number;
	upload_after: number;
	download_after: number;
	reset_count: number;
	reset_at: string;
	next_reset_at: string | null;
	reason: string;
	idempotent: boolean;
}

export interface AdminUserTrafficResetLog {
	id: number;
	user_id: number;
	plan_id: number | null;
	scheduled_for: string | null;
	reset_at: string;
	upload_before: number;
	download_before: number;
	upload_after: number;
	download_after: number;
	reset_count: number;
	trigger_source: "scheduled" | "manual";
	reason: string;
	administrator_id: number | null;
	administrator_email: string | null;
}

export interface AdminUserTrafficResetPage {
	items: AdminUserTrafficResetLog[];
	total: number;
	page: number;
	page_size: number;
}

export interface AdminUserTrafficStat {
	rate_micros: number;
	record_at: string;
	record_type: "d" | "m";
	upload: number;
	download: number;
}

export interface AdminUserTrafficStatPage {
	items: AdminUserTrafficStat[];
	total: number;
	page: number;
	page_size: number;
}

export type AssignAdminUserOrderInput = Omit<AssignOrderInput, "email">;

export type TicketLevel = 0 | 1 | 2;
export type TicketStatus = 0 | 1;
export type TicketReplyStatus = 0 | 1;

export interface TicketMessage {
  id: number;
  ticket_id: number;
  is_me: boolean;
  message: string;
  created_at: string;
  updated_at: string;
}

export interface Ticket {
  id: number;
  user_id: number;
  user_email?: string;
  subject: string;
  level: TicketLevel;
  status: TicketStatus;
  reply_status: TicketReplyStatus;
  messages?: TicketMessage[];
  created_at: string;
  updated_at: string;
}

export interface TicketPage {
  items: Ticket[];
  total: number;
  page: number;
  page_size: number;
}

export interface TicketInput {
  subject: string;
  level: TicketLevel;
  message: string;
}

export type SMTPEncryption = "starttls" | "tls" | "none";

export interface TicketSettings {
  revision: number;
  app_name: string;
  app_url: string;
  ticket_must_wait_reply: boolean;
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_password_set: boolean;
  smtp_encryption: SMTPEncryption;
  smtp_from_address: string;
  updated_at: string;
}

export interface TicketSettingsInput {
  revision: number;
  app_name: string;
  app_url: string;
  ticket_must_wait_reply: boolean;
  smtp_enabled: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_username: string;
  smtp_password?: string;
  smtp_encryption: SMTPEncryption;
  smtp_from_address: string;
}

export interface SiteSettings {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  tos_url: string;
  logo: string;
  stop_register: boolean;
  email_verify: boolean;
  email_whitelist_enable: boolean;
  email_whitelist_suffix: string[];
  email_gmail_limit_enable: boolean;
  register_limit_by_ip_enable: boolean;
  register_limit_count: number;
  register_limit_expire: number;
  password_limit_enable: boolean;
  password_limit_count: number;
  password_limit_expire: number;
  invite_force: boolean;
  invite_gen_limit: number;
  invite_never_expire: boolean;
  login_with_mail_link_enable: boolean;
  traffic_reset_method: number;
  coupon_enabled: boolean;
  captcha_enable: boolean;
  captcha_type: CaptchaProvider;
  recaptcha_site_key: string;
  recaptcha_secret_configured: boolean;
  recaptcha_v3_site_key: string;
  recaptcha_v3_score_threshold: number;
  recaptcha_v3_secret_configured: boolean;
  turnstile_site_key: string;
  turnstile_secret_configured: boolean;
  updated_at: string;
}

export type CaptchaProvider = "recaptcha" | "recaptcha-v3" | "turnstile";

export interface CaptchaToken {
  recaptcha_data?: string;
  recaptcha_v3_token?: string;
  turnstile_token?: string;
}

export interface SiteSettingsInput {
  revision: number;
  app_name: string;
  app_description: string;
  app_url: string;
  tos_url: string;
  logo: string;
  stop_register: boolean;
  email_verify: boolean;
  email_whitelist_enable: boolean;
  email_whitelist_suffix: string[];
  email_gmail_limit_enable: boolean;
  register_limit_by_ip_enable: boolean;
  register_limit_count: number;
  register_limit_expire: number;
  password_limit_enable: boolean;
  password_limit_count: number;
  password_limit_expire: number;
  invite_force: boolean;
  invite_gen_limit: number;
  invite_never_expire: boolean;
  login_with_mail_link_enable: boolean;
  traffic_reset_method: number;
  coupon_enabled: boolean;
  captcha_enable: boolean;
  captcha_type: CaptchaProvider;
  recaptcha_site_key: string;
  recaptcha_secret?: string;
  clear_recaptcha_secret?: boolean;
  recaptcha_v3_site_key: string;
  recaptcha_v3_score_threshold: number;
  recaptcha_v3_secret?: string;
  clear_recaptcha_v3_secret?: boolean;
  turnstile_site_key: string;
  turnstile_secret?: string;
  clear_turnstile_secret?: boolean;
}

export type SubscriptionTemplateName = "singbox" | "clash" | "clashmeta" | "stash" | "surge" | "surfboard";

export interface SubscriptionSettings {
  revision: number;
  path: string;
  show_info: boolean;
  show_protocol: boolean;
  templates: Record<SubscriptionTemplateName, string>;
  updated_at: string;
}

export type SubscriptionSettingsInput = Omit<SubscriptionSettings, "updated_at">;

export interface UserSubscription {
  plan_id: number | null;
  token: string;
  expired_at: string | null;
  u: number;
  d: number;
  transfer_enable: number;
  email: string;
  uuid: string;
  device_limit: number;
  speed_limit: number;
  next_reset_at: string | null;
  plan: { name: string; renew: boolean } | null;
  subscribe_url: string;
  reset_day: number | null;
  subscription_valid: boolean;
}

export interface SubscriptionQR {
  subscribe_url: string;
  qr_code: string;
}

export interface GuestConfig {
  app_name: string;
  app_description: string | null;
  app_url: string | null;
  tos_url: string | null;
  logo: string | null;
  is_email_verify: number;
  is_invite_force: number;
  enable_coupon_system: number;
  email_whitelist_suffix: number | string[];
  is_captcha: number;
  captcha_type: string;
  recaptcha_site_key: string | null;
  recaptcha_v3_site_key: string | null;
  recaptcha_v3_score_threshold: number;
  turnstile_site_key: string | null;
  is_recaptcha: number;
}

export interface InvitationCode {
  code: string;
  pv: number;
  created_at: string;
}

export interface InvitationSummary {
  codes: InvitationCode[];
  invited_count: number;
  valid_commission: number;
  pending_commission: number;
  commission_rate: number;
  available_commission: number;
}

export interface CommissionLog {
  id: number;
  trade_no: string;
  order_amount: number;
  get_amount: number;
  created_at: string;
}

export interface CommissionLogPage {
  items: CommissionLog[];
  total: number;
  page: number;
  page_size: number;
}

export interface CommissionTransferResult {
  commission_balance: number;
  balance: number;
}

export interface WorkerStatus {
  healthy: boolean;
  last_run_at: string | null;
}

export interface SystemQueueStats {
  pending: number;
  claimed: number;
  sent: number;
  failed: number;
  oldest_pending_at: string | null;
}

export interface SystemStatus {
  started_at: string;
  uptime_seconds: number;
  schema_version: number;
  scheduler: WorkerStatus;
  mail_worker: WorkerStatus;
  mail_queue: SystemQueueStats;
}

export type AuditMethod = "POST" | "PUT" | "PATCH" | "DELETE";

export interface AdminAuditLog {
  id: number;
  administrator_id: number | null;
  administrator_email: string;
  method: AuditMethod;
  route: string;
  status_code: number;
  created_at: string;
}

export interface AdminAuditPage {
  items: AdminAuditLog[];
  total: number;
  page: number;
  page_size: number;
}

export interface TicketMailFailure {
  id: number;
  kind: "ticket" | "password_reset" | "registration_email_verification" | "login_link";
  recipient: string;
  ticket_subject: string;
  attempt_count: number;
  last_error: string;
  created_at: string;
  failed_at: string;
}

export interface TicketMailFailurePage {
  items: TicketMailFailure[];
  total: number;
  page: number;
  page_size: number;
}

export interface SystemOperationsAPI {
  getSystemStatus: () => Promise<SystemStatus>;
  listAdminAudit: (page?: number, pageSize?: number, method?: AuditMethod | "", query?: string) => Promise<AdminAuditPage>;
  listTicketMailFailures: (page?: number, pageSize?: number) => Promise<TicketMailFailurePage>;
}

export interface AdminTicketQuery {
  page?: number;
  page_size?: number;
  status?: TicketStatus;
  reply_status?: TicketReplyStatus;
  level?: TicketLevel;
  query?: string;
}

export interface AdminUserQuery {
  limit?: number;
  cursor?: string;
  email_prefix?: string;
  banned?: boolean;
  group_id?: number;
  page?: number;
  page_size?: number;
  sort_by?: AdminUserSort;
  sort_desc?: boolean;
  filters?: AdminUserFilter[];
}

export type AdminUserSort = "id" | "online_count" | "banned" | "traffic_used" | "transfer_enable" | "expired_at" | "balance" | "commission_balance" | "created_at";
export type AdminUserFilterOperator = "eq" | "neq" | "contains" | "gt" | "gte" | "lt" | "lte" | "in" | "is_null" | "not_null";
export interface AdminUserFilter {
  field: string;
  operator: AdminUserFilterOperator;
  value?: string | number | boolean | Array<string | number | boolean>;
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
  is_admin?: boolean;
  is_staff?: boolean;
  is_distributor?: boolean;
  distributor_name?: string | null;
}

export type AdminUserGenerateMode = "single" | "random_batch" | "prefixed_batch";

export interface AdminUserGenerateInput {
  mode: AdminUserGenerateMode;
  email?: string;
  email_prefix?: string;
  email_domain?: string;
  count?: number;
  password?: string;
  plan_id: number | null;
  expired_at: string | null;
  is_distributor: boolean;
  distributor_name: string | null;
}

export interface AdminUserGeneratedCredential {
  id: number;
  email: string;
  password: string;
  expired_at: string | null;
  uuid: string;
  created_at: string;
  subscribe_url: string;
}

export interface AdminUserGenerationResult {
  items: AdminUserGeneratedCredential[];
}

export interface AdminUserUpdateInput extends Omit<AdminUserCreateInput, "password"> {
  revision: number;
	password?: string;
	plan_id?: number | null;
	invite_user_email?: string | null;
	traffic_upload?: number;
	traffic_download?: number;
	balance?: number;
	commission_type?: number;
	commission_rate?: number | null;
	commission_balance?: number;
	discount?: number | null;
	telegram_id?: number | null;
	remind_expire?: boolean;
	remind_traffic?: boolean;
	remarks?: string | null;
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
  assignNode: (machineID: number, nodeID: number, revision: number) => Promise<void>;
  unassignNode: (machineID: number, nodeID: number, revision: number) => Promise<void>;
  setNodeEnabled: (machineID: number, nodeID: number, revision: number, enabled: boolean) => Promise<void>;
  listAdminNodes: (query?: AdminNodeQuery) => Promise<AdminNodePage>;
  updateAdminNode: (nodeID: number, input: AdminNodeUpdateInput) => Promise<Node>;
  copyAdminNode: (nodeID: number, revision: number) => Promise<Node>;
  reorderAdminNodes: (targets: AdminNodeRevision[]) => Promise<AdminNodeMutation>;
  updateAdminNodeStates: (input: AdminNodeStateInput) => Promise<AdminNodeMutation>;
  resetAdminNodeTraffic: (targets: AdminNodeRevision[]) => Promise<AdminNodeMutation>;
  deleteAdminNodes: (targets: AdminNodeRevision[]) => Promise<void>;
  getActivationSchedule: (nodeID: number) => Promise<ActivationSchedule>;
  saveActivationSchedule: (nodeID: number, input: DailyScheduleInput) => Promise<ActivationSchedule>;
  deleteActivationSchedule: (nodeID: number) => Promise<void>;
  listServerGroups: () => Promise<ServerGroup[]>;
  createServerGroup: (name: string) => Promise<ServerGroup>;
  updateServerGroup: (id: number, name: string) => Promise<ServerGroup>;
  deleteServerGroup: (id: number) => Promise<void>;
  listPlans: () => Promise<Plan[]>;
  createPlan: (input: PlanInput) => Promise<Plan>;
  updatePlan: (id: number, revision: number, input: PlanInput, forceUpdate: boolean) => Promise<Plan>;
  setPlanState: (id: number, revision: number, show: boolean, sell: boolean, renew: boolean) => Promise<Plan>;
  reorderPlans: (ids: number[]) => Promise<Plan[]>;
  deletePlan: (id: number) => Promise<void>;
  listPaymentProviders: () => Promise<PaymentProviderDefinition[]>;
  listAdminPayments: (page?: number, pageSize?: number, query?: string) => Promise<PaymentPage>;
  createPayment: (input: PaymentMethodInput) => Promise<PaymentMethod>;
  updatePayment: (id: number, input: PaymentMethodInput) => Promise<PaymentMethod>;
  setPaymentEnabled: (id: number, enable: boolean) => Promise<PaymentMethod>;
  reorderPayments: (ids: number[]) => Promise<void>;
  deletePayment: (id: number) => Promise<void>;
  listAdminOrders: (query?: AdminOrderQuery) => Promise<AdminOrderPage>;
  getAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
  assignOrder: (input: AssignOrderInput) => Promise<Order>;
  paidAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
  cancelAdminOrder: (tradeNo: string) => Promise<AdminOrderDetail>;
	updateAdminOrderCommissionStatus: (tradeNo: string, status: 0 | 1 | 3) => Promise<AdminOrderDetail>;
	listAdminDistributorOptions: () => Promise<AdminUser[]>;
	listAdminDistributorOrders: (query?: DistributorOrderQuery) => Promise<DistributorOrderPage>;
	getAdminDistributorOrder: (orderID: number) => Promise<AdminDistributorOrderDetail>;
	updateAdminDistributorRemark: (orderID: number, remark: string | null) => Promise<{ order_id: number; remark: string | null }>;
	updateAdminDistributorEntitlement: (orderID: number, input: Omit<DistributorEntitlement, "plan_id" | "plan_name" | "used_traffic" | "remaining_traffic">) => Promise<DistributorEntitlement>;
	updateAdminDistributorHWID: (orderID: number, enabled: boolean, limit: number) => Promise<DistributorHWIDSettings>;
	listAdminDistributorHWIDDevices: (orderID: number, search?: string) => Promise<DistributorHWIDDevice[]>;
	deleteAdminDistributorHWIDDevice: (orderID: number, deviceID: number) => Promise<void>;
	previewAdminDistributorSettlement: (userID: number) => Promise<DistributorSettlementSummary>;
	settleAdminDistributorOrders: (userID: number) => Promise<DistributorSettlementSummary>;
	exportAdminDistributorOrders: (query?: DistributorOrderQuery) => Promise<Blob>;
  listCoupons: (query?: CouponQuery) => Promise<CouponPage>;
  createCoupon: (input: CouponInput) => Promise<Coupon>;
  updateCoupon: (id: number, input: CouponInput) => Promise<Coupon>;
  setCouponVisibility: (id: number, show: boolean) => Promise<Coupon>;
  deleteCoupon: (id: number) => Promise<void>;
  createCouponBatch: (input: CouponInput, count: number) => Promise<Blob>;
  listRoutingRules: () => Promise<RoutingRule[]>;
  createRoutingRule: (input: RoutingRuleInput) => Promise<RoutingRule>;
  updateRoutingRule: (id: number, input: RoutingRuleInput) => Promise<RoutingRule>;
  deleteRoutingRule: (id: number) => Promise<void>;
  listAdminUsers: (query?: AdminUserQuery) => Promise<AdminUserPage>;
  getAdminUser: (id: number) => Promise<AdminUser>;
  createAdminUser: (input: AdminUserCreateInput) => Promise<AdminUser>;
  generateAdminUsers: (input: AdminUserGenerateInput) => Promise<AdminUserGenerationResult>;
  updateAdminUser: (id: number, input: AdminUserUpdateInput) => Promise<AdminUser>;
  resetAdminUserPassword: (id: number, revision: number, newPassword: string) => Promise<AdminUser>;
	getAdminUserSubscriptionURL: (id: number) => Promise<AdminUserSubscriptionURL>;
	listAdminUserOrders: (id: number, page?: number, pageSize?: number) => Promise<AdminOrderPage>;
	assignAdminUserOrder: (id: number, input: AssignAdminUserOrderInput) => Promise<Order>;
	listAdminUserInvitations: (id: number, page?: number, pageSize?: number) => Promise<AdminUserPage>;
	listAdminUserTraffic: (id: number, page?: number, pageSize?: number) => Promise<AdminUserTrafficStatPage>;
	listAdminUserTrafficResets: (id: number, page?: number, pageSize?: number) => Promise<AdminUserTrafficResetPage>;
  resetAdminUserTraffic: (id: number, reason: string, idempotencyKey: string) => Promise<AdminUserTrafficReset>;
  createAdminUserBulkMail: (scope: AdminUserBulkScopeInput, subject: string, content: string) => Promise<AdminUserBulkJob>;
  createAdminUserBulkCSV: (scope: AdminUserBulkScopeInput) => Promise<AdminUserBulkJob>;
  banAdminUsers: (scope: AdminUserBulkScopeInput, idempotencyKey: string) => Promise<AdminUserBulkJob>;
  listAdminUserBulkJobs: (page?: number, pageSize?: number) => Promise<AdminUserBulkJobPage>;
  getAdminUserBulkJob: (id: string) => Promise<AdminUserBulkJob>;
  cancelAdminUserBulkJob: (id: string) => Promise<AdminUserBulkJob>;
  downloadAdminUserBulkCSV: (id: string) => Promise<Blob>;
  listAdminTickets: (query?: AdminTicketQuery) => Promise<TicketPage>;
  getAdminTicket: (id: number) => Promise<Ticket>;
  replyAdminTicket: (id: number, message: string) => Promise<Ticket>;
  closeAdminTicket: (id: number) => Promise<Ticket>;
  getTicketSettings: () => Promise<TicketSettings>;
  updateTicketSettings: (input: TicketSettingsInput) => Promise<TicketSettings>;
  getSiteSettings: () => Promise<SiteSettings>;
  updateSiteSettings: (input: SiteSettingsInput) => Promise<SiteSettings>;
  listNotices: () => Promise<Notice[]>;
  createNotice: (input: NoticeInput) => Promise<Notice>;
  updateNotice: (id: number, revision: number, input: NoticeInput) => Promise<Notice>;
  setNoticeVisibility: (id: number, revision: number, show: boolean) => Promise<Notice>;
  reorderNotices: (ids: number[]) => Promise<Notice[]>;
  deleteNotice: (id: number, revision: number) => Promise<void>;
  listKnowledgeAdmin: () => Promise<KnowledgeArticle[]>;
  getKnowledgeAdmin: (id: number) => Promise<KnowledgeArticle>;
  listKnowledgeCategories: () => Promise<string[]>;
  createKnowledge: (input: KnowledgeInput) => Promise<KnowledgeArticle>;
  updateKnowledge: (id: number, revision: number, input: KnowledgeInput) => Promise<KnowledgeArticle>;
  setKnowledgeVisibility: (id: number, revision: number, show: boolean) => Promise<KnowledgeArticle>;
  reorderKnowledge: (ids: number[]) => Promise<KnowledgeArticle[]>;
  deleteKnowledge: (id: number, revision: number) => Promise<void>;
  initializeKnowledgeAttachment: (file: File, draftToken: string) => Promise<KnowledgeAttachmentUpload>;
  uploadKnowledgeAttachmentChunk: (uploadUUID: string, index: number, digest: string, chunk: Blob, signal?: AbortSignal) => Promise<KnowledgeAttachmentChunkResult>;
  getKnowledgeAttachmentUpload: (uploadUUID: string) => Promise<KnowledgeAttachmentUpload>;
  completeKnowledgeAttachmentUpload: (uploadUUID: string) => Promise<KnowledgeAttachment>;
  cancelKnowledgeAttachmentUpload: (uploadUUID: string, draftToken: string) => Promise<void>;
  listKnowledgeAttachments: (filter: { knowledgeID?: number; draftToken?: string; page?: number; perPage?: number }) => Promise<KnowledgeAttachmentPage>;
  dropKnowledgeAttachment: (uuid: string, draftToken: string) => Promise<void>;
  cloneKnowledgeAttachments: (sourceKnowledgeID: number, sourceUUIDs: string[], draftToken: string) => Promise<Array<{ source_uuid: string; attachment: KnowledgeAttachment }>>;
  generateKnowledgeAttachmentQRCode: (url: string) => Promise<{ svg: string }>;
  listClientCatalogAdmin: () => Promise<AdminClientCatalog>;
  saveClientCatalog: (revision: number, links: ClientCatalogOverrideInput) => Promise<AdminClientCatalog>;
  getSystemStatus: () => Promise<SystemStatus>;
  listAdminAudit: (page?: number, pageSize?: number, method?: AuditMethod | "", query?: string) => Promise<AdminAuditPage>;
  listTicketMailFailures: (page?: number, pageSize?: number) => Promise<TicketMailFailurePage>;
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
  async guestConfig(): Promise<GuestConfig> {
    return this.request<GuestConfig>("/api/v1/guest/comm/config");
  }

  async session(): Promise<UserSession> {
    return this.request<UserSession>("/api/v1/auth/session");
  }

  async login(email: string, password: string): Promise<UserSession> {
    return this.request<UserSession>("/api/v1/auth/login", { method: "POST", body: { email, password } });
  }

  async exchangeLoginLink(token: string): Promise<LoginLinkExchange> {
    return this.request<LoginLinkExchange>("/api/v1/auth/login-link/exchange", { method: "POST", body: { token } });
  }

  async register(email: string, password: string, passwordConfirmation: string, emailCode = "", invitationCode = "", captchaToken: CaptchaToken = {}): Promise<UserSession> {
    const body: Record<string, string> = { email, password, password_confirmation: passwordConfirmation };
    if (emailCode !== "") body.email_code = emailCode;
    if (invitationCode !== "") body.invite_code = invitationCode;
    Object.assign(body, captchaToken);
    return this.request<UserSession>("/api/v1/auth/register", {
      method: "POST", body
    });
  }

  async requestRegistrationEmailVerification(email: string, captchaToken: CaptchaToken = {}): Promise<void> {
    await this.request<boolean>("/api/v1/auth/registration-email/request", { method: "POST", body: { email, ...captchaToken } });
  }

  async requestPasswordReset(email: string, captchaToken: CaptchaToken = {}): Promise<void> {
    await this.request<boolean>("/api/v1/auth/password-reset/request", { method: "POST", body: { email, ...captchaToken } });
  }

  async resetPassword(email: string, emailCode: string, password: string): Promise<void> {
    await this.request<boolean>("/api/v1/auth/password-reset/confirm", {
      method: "POST", body: { email, email_code: emailCode, password }
    });
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

  async listAccessTokens(): Promise<AccountAccessToken[]> {
    return this.request<AccountAccessToken[]>("/api/v1/auth/access-tokens");
  }

  async createAccessToken(name: string, expiresAt: string | null): Promise<IssuedAccessToken> {
    const body: Record<string, string> = { name };
    if (expiresAt !== null) body.expires_at = expiresAt;
    return this.request<IssuedAccessToken>("/api/v1/auth/access-tokens", { method: "POST", body });
  }

  async revokeAccessToken(id: number): Promise<void> {
    await this.request<void>(`/api/v1/auth/access-tokens/${id}`, { method: "DELETE" });
  }

  async revokeAllAccessTokens(): Promise<void> {
    await this.request<void>("/api/v1/auth/access-tokens", { method: "DELETE" });
  }

  async changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await this.request<void>("/api/v1/auth/password", {
      method: "PUT",
      body: { old_password: oldPassword, new_password: newPassword }
    });
  }

  async getInvitations(): Promise<InvitationSummary> {
    return this.request<InvitationSummary>("/api/v1/invitations");
  }

  async createInvitation(): Promise<InvitationCode> {
    return this.request<InvitationCode>("/api/v1/invitations", { method: "POST", body: {} });
  }

  async listCommissionLogs(page = 1, pageSize = 50): Promise<CommissionLogPage> {
    return this.request<CommissionLogPage>(`/api/v1/invitations/commissions?page=${page}&page_size=${pageSize}`);
  }

  async transferCommission(amount: number): Promise<CommissionTransferResult> {
    return this.request<CommissionTransferResult>("/api/v1/invitations/transfer", { method: "POST", body: { amount } });
  }

  async recordInvitationView(invitationCode: string): Promise<void> {
    await this.request<boolean>("/api/v1/invitations/view", { method: "POST", body: { invite_code: invitationCode } });
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

  async assignNode(machineID: number, nodeID: number, revision: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}`, { method: "PUT", body: { revision } });
  }

  async unassignNode(machineID: number, nodeID: number, revision: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}`, { method: "DELETE", body: { revision } });
  }

  async setNodeEnabled(machineID: number, nodeID: number, revision: number, enabled: boolean): Promise<void> {
    await this.request<void>(`/api/v1/admin/machines/${machineID}/nodes/${nodeID}/enabled`, {
      method: "PATCH",
      body: { revision, enabled }
    });
  }

  async listAdminNodes(query: AdminNodeQuery = {}): Promise<AdminNodePage> {
    const parameters = new URLSearchParams();
    if (query.page !== undefined) parameters.set("page", String(query.page));
    if (query.page_size !== undefined) parameters.set("page_size", String(query.page_size));
    if (query.q !== undefined && query.q.trim() !== "") parameters.set("q", query.q.trim());
    if (query.type !== undefined && query.type !== "") parameters.set("type", query.type);
    if (query.show !== undefined) parameters.set("show", String(query.show));
    if (query.enabled !== undefined) parameters.set("enabled", String(query.enabled));
    if (query.machine_id !== undefined) parameters.set("machine_id", String(query.machine_id));
    if (query.unassigned !== undefined) parameters.set("unassigned", String(query.unassigned));
    return this.request<AdminNodePage>(`/api/v1/admin/nodes${parameters.size === 0 ? "" : `?${parameters.toString()}`}`);
  }

  async updateAdminNode(nodeID: number, input: AdminNodeUpdateInput): Promise<Node> {
    return this.request<Node>(`/api/v1/admin/nodes/${nodeID}`, { method: "PATCH", body: input });
  }

  async copyAdminNode(nodeID: number, revision: number): Promise<Node> {
    return this.request<Node>(`/api/v1/admin/nodes/${nodeID}/copy`, { method: "POST", body: { revision } });
  }

  async reorderAdminNodes(targets: AdminNodeRevision[]): Promise<AdminNodeMutation> {
    return this.request<AdminNodeMutation>("/api/v1/admin/nodes/order", { method: "PUT", body: { targets } });
  }

  async updateAdminNodeStates(input: AdminNodeStateInput): Promise<AdminNodeMutation> {
    return this.request<AdminNodeMutation>("/api/v1/admin/nodes/bulk-state", { method: "POST", body: input });
  }

  async resetAdminNodeTraffic(targets: AdminNodeRevision[]): Promise<AdminNodeMutation> {
    return this.request<AdminNodeMutation>("/api/v1/admin/nodes/bulk-reset-traffic", { method: "POST", body: { targets } });
  }

  async deleteAdminNodes(targets: AdminNodeRevision[]): Promise<void> {
    await this.request<void>("/api/v1/admin/nodes/bulk-delete", { method: "POST", body: { targets } });
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

  async listPlans(): Promise<Plan[]> {
    return this.request<Plan[]>("/api/v1/admin/plans");
  }

  async createPlan(input: PlanInput): Promise<Plan> {
    return this.request<Plan>("/api/v1/admin/plans", { method: "POST", body: input });
  }

  async updatePlan(id: number, revision: number, input: PlanInput, forceUpdate: boolean): Promise<Plan> {
    return this.request<Plan>(`/api/v1/admin/plans/${id}`, { method: "PATCH", body: { revision, ...input, force_update: forceUpdate } });
  }

  async setPlanState(id: number, revision: number, show: boolean, sell: boolean, renew: boolean): Promise<Plan> {
    return this.request<Plan>(`/api/v1/admin/plans/${id}/state`, { method: "PATCH", body: { revision, show, sell, renew } });
  }

  async reorderPlans(ids: number[]): Promise<Plan[]> {
    return this.request<Plan[]>("/api/v1/admin/plans/order", { method: "PUT", body: { ids } });
  }

  async deletePlan(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/plans/${id}`, { method: "DELETE" });
  }

  async listPaymentProviders(): Promise<PaymentProviderDefinition[]> {
    return this.request<PaymentProviderDefinition[]>("/api/v1/admin/payment-providers");
  }

  async listAdminPayments(page = 1, pageSize = 100, query = ""): Promise<PaymentPage> {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (query !== "") params.set("query", query);
    return this.request<PaymentPage>(`/api/v1/admin/payments?${params.toString()}`);
  }

  async createPayment(input: PaymentMethodInput): Promise<PaymentMethod> {
    return this.request<PaymentMethod>("/api/v1/admin/payments", { method: "POST", body: input });
  }

  async updatePayment(id: number, input: PaymentMethodInput): Promise<PaymentMethod> {
    return this.request<PaymentMethod>(`/api/v1/admin/payments/${id}`, { method: "PUT", body: input });
  }

  async setPaymentEnabled(id: number, enable: boolean): Promise<PaymentMethod> {
    return this.request<PaymentMethod>(`/api/v1/admin/payments/${id}/enabled`, { method: "PATCH", body: { enable } });
  }

  async reorderPayments(ids: number[]): Promise<void> {
    await this.request<void>("/api/v1/admin/payments/order", { method: "PUT", body: { ids } });
  }

  async deletePayment(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/payments/${id}`, { method: "DELETE" });
  }

  async listPlanOffers(): Promise<PlanOffer[]> {
    return this.request<PlanOffer[]>("/api/v1/plans");
  }

	async listDistributorOrders(query: DistributorOrderQuery = {}): Promise<DistributorOrderPage> {
		return this.request<DistributorOrderPage>(`/api/v1/distributor/orders${distributorOrderQuery(query)}`);
	}

	async createDistributorOrder(planID: number, period: PlanPeriod): Promise<DistributorOrder> {
		return this.request<DistributorOrder>("/api/v1/distributor/orders", { method: "POST", body: { plan_id: planID, period } });
	}

	async getDistributorOrder(tradeNo: string): Promise<DistributorOrder> {
		return this.request<DistributorOrder>(`/api/v1/distributor/orders/${encodeURIComponent(tradeNo)}`);
	}

	async renewDistributorOrder(tradeNo: string, period: PlanPeriod, idempotencyKey: string): Promise<DistributorOrder> {
		return this.request<DistributorOrder>(`/api/v1/distributor/orders/${encodeURIComponent(tradeNo)}/renew`, {
			method: "POST", body: { period, idempotency_key: idempotencyKey }
		});
	}

	async getDistributorOrderQR(tradeNo: string): Promise<DistributorQR> {
		return this.request<DistributorQR>(`/api/v1/distributor/orders/${encodeURIComponent(tradeNo)}/qr`);
	}

	async exportDistributorOrders(query: DistributorOrderQuery = {}): Promise<Blob> {
		return this.download(`/api/v1/distributor/orders/export${distributorOrderQuery(query)}`);
	}

  async listOrders(status?: OrderStatus, limit = 100): Promise<Order[]> {
    const params = new URLSearchParams({ limit: String(limit) });
    if (status !== undefined) params.set("status", String(status));
    return this.request<Order[]>(`/api/v1/orders?${params.toString()}`);
  }

  async checkCoupon(code: string, planID: number, period: PlanPeriod): Promise<CouponQuote> {
    return this.request<CouponQuote>("/api/v1/user/coupons/check", { method: "POST", body: { code, plan_id: planID, period } });
  }

  async createOrder(planID: number, period: PlanPeriod, couponCode?: string): Promise<Order> {
    const body: { plan_id: number; period: PlanPeriod; coupon_code?: string } = { plan_id: planID, period };
    if (couponCode !== undefined && couponCode !== "") body.coupon_code = couponCode;
    return this.request<Order>("/api/v1/orders", { method: "POST", body });
  }

  async getOrder(tradeNo: string): Promise<Order> {
    return this.request<Order>(`/api/v1/orders/${encodeURIComponent(tradeNo)}`);
  }

  async listPaymentMethods(): Promise<UserPaymentMethod[]> {
    return this.request<UserPaymentMethod[]>("/api/v1/payments");
  }

  async checkoutOrder(tradeNo: string, paymentID?: number): Promise<Order | PaymentCheckout> {
    return this.request<Order | PaymentCheckout>(`/api/v1/orders/${encodeURIComponent(tradeNo)}/checkout`, {
      method: "POST", body: paymentID === undefined ? {} : { payment_id: paymentID }
    });
  }

  async cancelOrder(tradeNo: string): Promise<Order> {
    return this.request<Order>(`/api/v1/orders/${encodeURIComponent(tradeNo)}/cancel`, { method: "POST", body: {} });
  }

  async listAdminOrders(query: AdminOrderQuery = {}): Promise<AdminOrderPage> {
    const params = new URLSearchParams();
    if (query.page !== undefined) params.set("page", String(query.page));
    if (query.page_size !== undefined) params.set("page_size", String(query.page_size));
		for (const status of query.statuses ?? (query.status === undefined ? [] : [query.status])) params.append("status", String(status));
		for (const type of query.types ?? (query.type === undefined ? [] : [query.type])) params.append("type", String(type));
		for (const period of query.periods ?? (query.period === undefined ? [] : [query.period])) params.append("period", period);
		for (const status of query.commission_statuses ?? []) params.append("commission_status", String(status));
    if (query.query !== undefined && query.query !== "") params.set("query", query.query);
		if (query.sort_by !== undefined) params.set("sort_by", query.sort_by);
		if (query.sort_desc !== undefined) params.set("sort_desc", String(query.sort_desc));
    const suffix = params.size === 0 ? "" : `?${params.toString()}`;
    return this.request<AdminOrderPage>(`/api/v1/admin/orders${suffix}`);
  }

  async getAdminOrder(tradeNo: string): Promise<AdminOrderDetail> {
		return this.request<AdminOrderDetail>(`/api/v1/admin/orders/${encodeURIComponent(tradeNo)}`);
  }

  async assignOrder(input: AssignOrderInput): Promise<Order> {
    return this.request<Order>("/api/v1/admin/orders", { method: "POST", body: input });
  }

  async paidAdminOrder(tradeNo: string): Promise<AdminOrderDetail> {
    return this.request<AdminOrderDetail>(`/api/v1/admin/orders/${encodeURIComponent(tradeNo)}/paid`, { method: "POST", body: {} });
  }

  async cancelAdminOrder(tradeNo: string): Promise<AdminOrderDetail> {
    return this.request<AdminOrderDetail>(`/api/v1/admin/orders/${encodeURIComponent(tradeNo)}/cancel`, { method: "POST", body: {} });
  }

	async updateAdminOrderCommissionStatus(tradeNo: string, status: 0 | 1 | 3): Promise<AdminOrderDetail> {
		return this.request<AdminOrderDetail>(`/api/v1/admin/orders/${encodeURIComponent(tradeNo)}/commission`, {
			method: "PATCH", body: { commission_status: status }
		});
	}

	async listAdminDistributorOptions(): Promise<AdminUser[]> {
		return this.request<AdminUser[]>("/api/v1/admin/distributors/options");
	}

	async listAdminDistributorOrders(query: DistributorOrderQuery = {}): Promise<DistributorOrderPage> {
		return this.request<DistributorOrderPage>(`/api/v1/admin/distributor-orders${distributorOrderQuery(query)}`);
	}

	async getAdminDistributorOrder(orderID: number): Promise<AdminDistributorOrderDetail> {
		return this.request<AdminDistributorOrderDetail>(`/api/v1/admin/distributor-orders/${orderID}`);
	}

	async updateAdminDistributorRemark(orderID: number, remark: string | null): Promise<{ order_id: number; remark: string | null }> {
		return this.request(`/api/v1/admin/distributor-orders/${orderID}/remark`, { method: "PATCH", body: { remark } });
	}

	async updateAdminDistributorEntitlement(orderID: number, input: Omit<DistributorEntitlement, "plan_id" | "plan_name" | "used_traffic" | "remaining_traffic">): Promise<DistributorEntitlement> {
		return this.request<DistributorEntitlement>(`/api/v1/admin/distributor-orders/${orderID}/entitlement`, { method: "PATCH", body: input });
	}

	async updateAdminDistributorHWID(orderID: number, enabled: boolean, limit: number): Promise<DistributorHWIDSettings> {
		return this.request<DistributorHWIDSettings>(`/api/v1/admin/distributor-orders/${orderID}/hwid`, { method: "PATCH", body: { enabled, limit } });
	}

	async listAdminDistributorHWIDDevices(orderID: number, search = ""): Promise<DistributorHWIDDevice[]> {
		const suffix = search.trim() === "" ? "" : `?search=${encodeURIComponent(search.trim())}`;
		return this.request<DistributorHWIDDevice[]>(`/api/v1/admin/distributor-orders/${orderID}/hwid/devices${suffix}`);
	}

	async deleteAdminDistributorHWIDDevice(orderID: number, deviceID: number): Promise<void> {
		await this.request<boolean>(`/api/v1/admin/distributor-orders/${orderID}/hwid/devices/${deviceID}`, { method: "DELETE" });
	}

	async previewAdminDistributorSettlement(userID: number): Promise<DistributorSettlementSummary> {
		return this.request<DistributorSettlementSummary>(`/api/v1/admin/distributors/${userID}/settlement`);
	}

	async settleAdminDistributorOrders(userID: number): Promise<DistributorSettlementSummary> {
		return this.request<DistributorSettlementSummary>(`/api/v1/admin/distributors/${userID}/settlement`, { method: "POST", body: {} });
	}

	async exportAdminDistributorOrders(query: DistributorOrderQuery = {}): Promise<Blob> {
		return this.download(`/api/v1/admin/distributor-orders/export${distributorOrderQuery(query)}`);
	}

  async listCoupons(query: CouponQuery = {}): Promise<CouponPage> {
    const params = new URLSearchParams();
    if (query.page !== undefined) params.set("page", String(query.page));
    if (query.page_size !== undefined) params.set("page_size", String(query.page_size));
    if (query.query !== undefined && query.query !== "") params.set("query", query.query);
    if (query.type !== undefined) params.set("type", String(query.type));
    if (query.show !== undefined) params.set("show", String(query.show));
    if (query.sort !== undefined) params.set("sort", query.sort);
    if (query.desc !== undefined) params.set("desc", String(query.desc));
    const suffix = params.size === 0 ? "" : `?${params.toString()}`;
    return this.request<CouponPage>(`/api/v1/admin/coupons${suffix}`);
  }

  async createCoupon(input: CouponInput): Promise<Coupon> {
    return this.request<Coupon>("/api/v1/admin/coupons", { method: "POST", body: input });
  }

  async updateCoupon(id: number, input: CouponInput): Promise<Coupon> {
    return this.request<Coupon>(`/api/v1/admin/coupons/${id}`, { method: "PUT", body: input });
  }

  async setCouponVisibility(id: number, show: boolean): Promise<Coupon> {
    return this.request<Coupon>(`/api/v1/admin/coupons/${id}/visibility`, { method: "PATCH", body: { show } });
  }

  async deleteCoupon(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/coupons/${id}`, { method: "DELETE" });
  }

  async createCouponBatch(input: CouponInput, count: number): Promise<Blob> {
    return this.download("/api/v1/admin/coupons/batch", { ...input, code: "", count });
  }

  async listGiftCardTemplates(page = 1, pageSize = 20, type?: GiftCardType, status?: boolean): Promise<GiftCardTemplatePage> {
    const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (type !== undefined) query.set("type", String(type));
    if (status !== undefined) query.set("status", String(status));
    return this.request<GiftCardTemplatePage>(`/api/v1/admin/gift-card/templates?${query.toString()}`);
  }

  async createGiftCardTemplate(input: GiftCardTemplateInput): Promise<GiftCardTemplate> {
    return this.request<GiftCardTemplate>("/api/v1/admin/gift-card/templates", { method: "POST", body: input });
  }

  async updateGiftCardTemplate(id: number, input: GiftCardTemplateInput): Promise<GiftCardTemplate> {
    return this.request<GiftCardTemplate>(`/api/v1/admin/gift-card/templates/${id}`, { method: "PUT", body: input });
  }

  async deleteGiftCardTemplate(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/gift-card/templates/${id}`, { method: "DELETE" });
  }

  async generateGiftCardCodes(templateID: number, count: number, prefix: string, expiresAt: number | null, maxUsage: number): Promise<GiftCardCode[]> {
    return this.request<GiftCardCode[]>("/api/v1/admin/gift-card/codes/generate", { method: "POST", body: { template_id: templateID, count, prefix, expires_at: expiresAt, max_usage: maxUsage } });
  }

  async generateGiftCardCodesCSV(templateID: number, count: number, prefix: string, expiresAt: number | null, maxUsage: number): Promise<Blob> {
    return this.download("/api/v1/admin/gift-card/codes/generate", {
      template_id: templateID, count, prefix, expires_at: expiresAt, max_usage: maxUsage, download_csv: true
    });
  }

  async listGiftCardCodes(page = 1, pageSize = 20, search = "", templateID?: number, status?: GiftCardCodeStatus, batchNo = ""): Promise<GiftCardCodePage> {
    const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (search.trim() !== "") query.set("query", search.trim());
    if (templateID !== undefined) query.set("template_id", String(templateID));
    if (status !== undefined) query.set("status", String(status));
    if (batchNo.trim() !== "") query.set("batch_no", batchNo.trim());
    return this.request<GiftCardCodePage>(`/api/v1/admin/gift-card/codes?${query.toString()}`);
  }

  async updateGiftCardCode(id: number, input: GiftCardCodeInput): Promise<GiftCardCode> {
    return this.request<GiftCardCode>(`/api/v1/admin/gift-card/codes/${id}`, { method: "PATCH", body: input });
  }

  async exportGiftCardCodes(batchNo = ""): Promise<Blob> {
    const query = new URLSearchParams();
    if (batchNo.trim() !== "") query.set("batch_no", batchNo.trim());
    const suffix = query.size === 0 ? "" : `?${query.toString()}`;
    return this.download(`/api/v1/admin/gift-card/codes/export${suffix}`);
  }

  async toggleGiftCardCode(id: number): Promise<GiftCardCode> {
    return this.request<GiftCardCode>(`/api/v1/admin/gift-card/codes/${id}/toggle`, { method: "POST", body: {} });
  }

  async deleteGiftCardCode(id: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/gift-card/codes/${id}`, { method: "DELETE" });
  }

  async listGiftCardUsages(page = 1, pageSize = 20, userID?: number, templateID?: number, codeID?: number): Promise<GiftCardUsagePage> {
    const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (userID !== undefined) query.set("user_id", String(userID));
    if (templateID !== undefined) query.set("template_id", String(templateID));
    if (codeID !== undefined) query.set("code_id", String(codeID));
    return this.request<GiftCardUsagePage>(`/api/v1/admin/gift-card/usages?${query.toString()}`);
  }

  async getGiftCardStatistics(): Promise<GiftCardStatistics> {
    return this.request<GiftCardStatistics>("/api/v1/admin/gift-card/statistics");
  }

  async checkGiftCard(code: string): Promise<GiftCardPreview> {
    return this.request<GiftCardPreview>("/api/v1/user/gift-card/check", { method: "POST", body: { code } });
  }

  async redeemGiftCard(code: string): Promise<GiftCardRedeemResult> {
    return this.request<GiftCardRedeemResult>("/api/v1/user/gift-card/redeem", { method: "POST", body: { code } });
  }

  async listMyGiftCardUsages(page = 1, pageSize = 15): Promise<GiftCardUsagePage> {
    return this.request<GiftCardUsagePage>(`/api/v1/user/gift-card/history?page=${page}&page_size=${pageSize}`);
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
    if ((query.filters?.length ?? 0) > 0) {
      return this.request<AdminUserPage>("/api/v1/admin/users/query", {
        method: "POST",
        body: {
          page: query.page ?? 1,
          page_size: query.page_size ?? 50,
          sort_by: query.sort_by ?? "id",
          sort_desc: query.sort_desc ?? true,
          email_prefix: query.email_prefix ?? "",
          banned: query.banned,
          group_id: query.group_id,
          filters: query.filters
        }
      });
    }
    const params = new URLSearchParams();
    if (query.limit !== undefined) params.set("limit", String(query.limit));
    if (query.cursor !== undefined && query.cursor !== "") params.set("cursor", query.cursor);
    if (query.email_prefix !== undefined && query.email_prefix !== "") params.set("email_prefix", query.email_prefix);
    if (query.banned !== undefined) params.set("banned", String(query.banned));
    if (query.group_id !== undefined) params.set("group_id", String(query.group_id));
    if (query.page !== undefined) params.set("page", String(query.page));
    if (query.page_size !== undefined) params.set("page_size", String(query.page_size));
    if (query.sort_by !== undefined) params.set("sort_by", query.sort_by);
    if (query.sort_desc !== undefined) params.set("sort_desc", String(query.sort_desc));
    if (query.filters !== undefined && query.filters.length > 0) params.set("filters", JSON.stringify(query.filters));
    const suffix = params.size === 0 ? "" : `?${params.toString()}`;
    return this.request<AdminUserPage>(`/api/v1/admin/users${suffix}`);
  }

  async getAdminUser(id: number): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}`);
  }

  async createAdminUser(input: AdminUserCreateInput): Promise<AdminUser> {
    return this.request<AdminUser>("/api/v1/admin/users", { method: "POST", body: input });
  }

  async generateAdminUsers(input: AdminUserGenerateInput): Promise<AdminUserGenerationResult> {
    return this.request<AdminUserGenerationResult>("/api/v1/admin/users/generate", { method: "POST", body: input });
  }

  async updateAdminUser(id: number, input: AdminUserUpdateInput): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}`, { method: "PATCH", body: input });
  }

  async resetAdminUserPassword(id: number, revision: number, newPassword: string): Promise<AdminUser> {
    return this.request<AdminUser>(`/api/v1/admin/users/${id}/password`, {
      method: "PUT", body: { revision, new_password: newPassword }
    });
  }

	async getAdminUserSubscriptionURL(id: number): Promise<AdminUserSubscriptionURL> {
		return this.request<AdminUserSubscriptionURL>(`/api/v1/admin/users/${id}/subscription-url`);
	}

	async listAdminUserOrders(id: number, page = 1, pageSize = 20): Promise<AdminOrderPage> {
		return this.request<AdminOrderPage>(`/api/v1/admin/users/${id}/orders?page=${page}&page_size=${pageSize}`);
	}

	async assignAdminUserOrder(id: number, input: AssignAdminUserOrderInput): Promise<Order> {
		return this.request<Order>(`/api/v1/admin/users/${id}/orders`, { method: "POST", body: input });
	}

	async listAdminUserInvitations(id: number, page = 1, pageSize = 20): Promise<AdminUserPage> {
		return this.request<AdminUserPage>(`/api/v1/admin/users/${id}/invitations?page=${page}&page_size=${pageSize}`);
	}

	async listAdminUserTraffic(id: number, page = 1, pageSize = 20): Promise<AdminUserTrafficStatPage> {
		return this.request<AdminUserTrafficStatPage>(`/api/v1/admin/users/${id}/traffic?page=${page}&page_size=${pageSize}`);
	}

	async listAdminUserTrafficResets(id: number, page = 1, pageSize = 20): Promise<AdminUserTrafficResetPage> {
		return this.request<AdminUserTrafficResetPage>(`/api/v1/admin/users/${id}/traffic-resets?page=${page}&page_size=${pageSize}`);
	}

	async resetAdminUserTraffic(id: number, reason: string, idempotencyKey: string): Promise<AdminUserTrafficReset> {
		return this.request<AdminUserTrafficReset>(`/api/v1/admin/users/${id}/traffic-reset`, {
			method: "POST", body: { reason }, headers: { "Idempotency-Key": idempotencyKey }
		});
	}

  async createAdminUserBulkMail(scope: AdminUserBulkScopeInput, subject: string, content: string): Promise<AdminUserBulkJob> {
    return this.request<AdminUserBulkJob>("/api/v1/admin/users/bulk/mail", { method: "POST", body: { ...scope, subject, content } });
  }

  async createAdminUserBulkCSV(scope: AdminUserBulkScopeInput): Promise<AdminUserBulkJob> {
    return this.request<AdminUserBulkJob>("/api/v1/admin/users/bulk/csv", { method: "POST", body: scope });
  }

  async banAdminUsers(scope: AdminUserBulkScopeInput, idempotencyKey: string): Promise<AdminUserBulkJob> {
    return this.request<AdminUserBulkJob>("/api/v1/admin/users/bulk/ban", {
      method: "POST", body: { ...scope, idempotency_key: idempotencyKey }
    });
  }

  async listAdminUserBulkJobs(page = 1, pageSize = 20): Promise<AdminUserBulkJobPage> {
    return this.request<AdminUserBulkJobPage>(`/api/v1/admin/user-bulk-jobs?page=${page}&page_size=${pageSize}`);
  }

  async getAdminUserBulkJob(id: string): Promise<AdminUserBulkJob> {
    return this.request<AdminUserBulkJob>(`/api/v1/admin/user-bulk-jobs/${encodeURIComponent(id)}`);
  }

  async cancelAdminUserBulkJob(id: string): Promise<AdminUserBulkJob> {
    return this.request<AdminUserBulkJob>(`/api/v1/admin/user-bulk-jobs/${encodeURIComponent(id)}/cancel`, { method: "POST", body: {} });
  }

  async downloadAdminUserBulkCSV(id: string): Promise<Blob> {
    return this.download(`/api/v1/admin/user-bulk-jobs/${encodeURIComponent(id)}/download`);
  }

  async listTickets(page = 1, pageSize = 20): Promise<TicketPage> {
    return this.request<TicketPage>(`/api/v1/tickets?page=${page}&page_size=${pageSize}`);
  }

  async createTicket(input: TicketInput): Promise<Ticket> {
    return this.request<Ticket>("/api/v1/tickets", { method: "POST", body: input });
  }

  async getTicket(id: number): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/tickets/${id}`);
  }

  async replyTicket(id: number, message: string): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/tickets/${id}/messages`, { method: "POST", body: { message } });
  }

  async closeTicket(id: number): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/tickets/${id}/close`, { method: "POST", body: {} });
  }

  async listAdminTickets(query: AdminTicketQuery = {}): Promise<TicketPage> {
    const params = new URLSearchParams();
    params.set("page", String(query.page ?? 1));
    params.set("page_size", String(query.page_size ?? 20));
    if (query.status !== undefined) params.set("status", String(query.status));
    if (query.reply_status !== undefined) params.set("reply_status", String(query.reply_status));
    if (query.level !== undefined) params.set("level", String(query.level));
    if (query.query !== undefined && query.query.trim() !== "") params.set("query", query.query.trim());
    return this.request<TicketPage>(`/api/v1/admin/tickets?${params.toString()}`);
  }

  async getAdminTicket(id: number): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/admin/tickets/${id}`);
  }

  async replyAdminTicket(id: number, message: string): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/admin/tickets/${id}/messages`, { method: "POST", body: { message } });
  }

  async closeAdminTicket(id: number): Promise<Ticket> {
    return this.request<Ticket>(`/api/v1/admin/tickets/${id}/close`, { method: "POST", body: {} });
  }

  async getTicketSettings(): Promise<TicketSettings> {
    return this.request<TicketSettings>("/api/v1/admin/ticket-settings");
  }

  async updateTicketSettings(input: TicketSettingsInput): Promise<TicketSettings> {
    return this.request<TicketSettings>("/api/v1/admin/ticket-settings", { method: "PUT", body: input });
  }

  async getSiteSettings(): Promise<SiteSettings> {
    return this.request<SiteSettings>("/api/v1/admin/site-settings");
  }

  async updateSiteSettings(input: SiteSettingsInput): Promise<SiteSettings> {
    return this.request<SiteSettings>("/api/v1/admin/site-settings", { method: "PUT", body: input });
  }

  async getSubscriptionSettings(): Promise<SubscriptionSettings> {
    return this.request<SubscriptionSettings>("/api/v1/admin/subscription-settings");
  }

  async updateSubscriptionSettings(input: SubscriptionSettingsInput): Promise<SubscriptionSettings> {
    return this.request<SubscriptionSettings>("/api/v1/admin/subscription-settings", { method: "PUT", body: input });
  }

  async getSubscription(): Promise<UserSubscription> {
    return this.request<UserSubscription>("/api/v1/subscription");
  }

  async getSubscriptionQR(): Promise<SubscriptionQR> {
    return this.request<SubscriptionQR>("/api/v1/subscription/qr");
  }

  async resetSubscriptionSecurity(): Promise<UserSubscription> {
    return this.request<UserSubscription>("/api/v1/subscription/security/reset", { method: "POST", body: {} });
  }

  async getSystemStatus(): Promise<SystemStatus> {
    return this.request<SystemStatus>("/api/v1/admin/system/status");
  }

  async listAdminAudit(page = 1, pageSize = 20, method: AuditMethod | "" = "", query = ""): Promise<AdminAuditPage> {
    const params = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (method !== "") params.set("method", method);
    if (query.trim() !== "") params.set("query", query.trim());
    return this.request<AdminAuditPage>(`/api/v1/admin/system/audit?${params.toString()}`);
  }

  async listTicketMailFailures(page = 1, pageSize = 20): Promise<TicketMailFailurePage> {
    return this.request<TicketMailFailurePage>(`/api/v1/admin/system/mail-failures?page=${page}&page_size=${pageSize}`);
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

  async listKnowledgeAdmin(): Promise<KnowledgeArticle[]> {
    return this.request<KnowledgeArticle[]>("/api/v1/admin/knowledge");
  }

  async getKnowledgeAdmin(id: number): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>(`/api/v1/admin/knowledge/${id}`);
  }

  async listKnowledgeCategories(): Promise<string[]> {
    return this.request<string[]>("/api/v1/admin/knowledge/categories");
  }

  async createKnowledge(input: KnowledgeInput): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>("/api/v1/admin/knowledge", { method: "POST", body: input });
  }

  async updateKnowledge(id: number, revision: number, input: KnowledgeInput): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>(`/api/v1/admin/knowledge/${id}`, { method: "PATCH", body: { revision, ...input } });
  }

  async setKnowledgeVisibility(id: number, revision: number, show: boolean): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>(`/api/v1/admin/knowledge/${id}/visibility`, { method: "PATCH", body: { revision, show } });
  }

  async reorderKnowledge(ids: number[]): Promise<KnowledgeArticle[]> {
    return this.request<KnowledgeArticle[]>("/api/v1/admin/knowledge/order", { method: "PUT", body: { ids } });
  }

  async deleteKnowledge(id: number, revision: number): Promise<void> {
    await this.request<void>(`/api/v1/admin/knowledge/${id}?revision=${revision}`, { method: "DELETE" });
  }

  async initializeKnowledgeAttachment(file: File, draftToken: string): Promise<KnowledgeAttachmentUpload> {
    return this.request<KnowledgeAttachmentUpload>("/api/v1/admin/knowledge-attachments/uploads", {
      method: "POST", body: { original_name: file.name, size: file.size, draft_token: draftToken }
    });
  }

  async uploadKnowledgeAttachmentChunk(uploadUUID: string, index: number, digest: string, chunk: Blob, signal?: AbortSignal): Promise<KnowledgeAttachmentChunkResult> {
    const body = new FormData();
    body.set("index", String(index));
    body.set("sha256", digest);
    body.set("file", chunk, `${index}.part`);
    return this.requestForm<KnowledgeAttachmentChunkResult>(`/api/v1/admin/knowledge-attachments/uploads/${encodeURIComponent(uploadUUID)}/chunks`, body, signal);
  }

  async getKnowledgeAttachmentUpload(uploadUUID: string): Promise<KnowledgeAttachmentUpload> {
    return this.request<KnowledgeAttachmentUpload>(`/api/v1/admin/knowledge-attachments/uploads/${encodeURIComponent(uploadUUID)}`);
  }

  async completeKnowledgeAttachmentUpload(uploadUUID: string): Promise<KnowledgeAttachment> {
    return this.request<KnowledgeAttachment>(`/api/v1/admin/knowledge-attachments/uploads/${encodeURIComponent(uploadUUID)}/complete`, { method: "POST", body: {} });
  }

  async cancelKnowledgeAttachmentUpload(uploadUUID: string, draftToken: string): Promise<void> {
    await this.request<boolean>(`/api/v1/admin/knowledge-attachments/uploads/${encodeURIComponent(uploadUUID)}/cancel`, { method: "POST", body: { draft_token: draftToken } });
  }

  async listKnowledgeAttachments(filter: { knowledgeID?: number; draftToken?: string; page?: number; perPage?: number }): Promise<KnowledgeAttachmentPage> {
    const query = new URLSearchParams();
    if (filter.knowledgeID !== undefined) query.set("knowledge_id", String(filter.knowledgeID));
    if (filter.draftToken !== undefined) query.set("draft_token", filter.draftToken);
    query.set("page", String(filter.page ?? 1));
    query.set("per_page", String(filter.perPage ?? 100));
    return this.request<KnowledgeAttachmentPage>(`/api/v1/admin/knowledge-attachments?${query.toString()}`);
  }

  async dropKnowledgeAttachment(uuid: string, draftToken: string): Promise<void> {
    await this.request<boolean>(`/api/v1/admin/knowledge-attachments/${encodeURIComponent(uuid)}/drop`, { method: "POST", body: { draft_token: draftToken } });
  }

  async cloneKnowledgeAttachments(sourceKnowledgeID: number, sourceUUIDs: string[], draftToken: string): Promise<Array<{ source_uuid: string; attachment: KnowledgeAttachment }>> {
    const result = await this.request<{ items: Array<{ source_uuid: string; attachment: KnowledgeAttachment }> }>("/api/v1/admin/knowledge-attachments/clone", {
      method: "POST", body: { source_knowledge_id: sourceKnowledgeID, source_uuids: sourceUUIDs, draft_token: draftToken }
    });
    return result.items;
  }

  async generateKnowledgeAttachmentQRCode(url: string): Promise<{ svg: string }> {
    return this.request<{ svg: string }>("/api/v1/admin/knowledge-attachments/qr-code", { method: "POST", body: { url } });
  }

  async listKnowledge(language: KnowledgeLanguage, keyword = ""): Promise<KnowledgeArticle[]> {
    const query = new URLSearchParams({ language });
    if (keyword.trim() !== "") query.set("keyword", keyword.trim());
    return this.request<KnowledgeArticle[]>(`/api/v1/knowledge?${query.toString()}`);
  }

  async getKnowledge(id: number): Promise<KnowledgeArticle> {
    return this.request<KnowledgeArticle>(`/api/v1/knowledge/${id}`);
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

  private async request<T>(path: string, options: { method?: string; body?: unknown; headers?: Record<string, string> } = {}): Promise<T> {
    const method = options.method ?? "GET";
    const headers = new Headers({ Accept: "application/json" });
    if (options.body !== undefined) {
      headers.set("Content-Type", "application/json");
    }
		for (const [name, value] of Object.entries(options.headers ?? {})) headers.set(name, value);
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

  private async requestForm<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T> {
    const headers = new Headers({ Accept: "application/json" });
    const csrf = readCookie("xboard_csrf");
    if (csrf !== null) headers.set("X-CSRF-Token", csrf);
    const response = await fetch(path, { method: "POST", headers, credentials: "same-origin", body, signal });
    const payload = (await response.json()) as Envelope<T> | ErrorEnvelope;
    if (!response.ok || payload.status === "fail") {
      const error = payload.status === "fail" ? payload.error : { code: "request_failed", message: "请求失败" };
      throw new APIError(response.status, error.code, error.message, error.fields);
    }
    return payload.data;
  }

  private async download(path: string, body?: unknown): Promise<Blob> {
    const method = body === undefined ? "GET" : "POST";
    const headers = new Headers({ Accept: "text/csv" });
    if (body !== undefined) headers.set("Content-Type", "application/json");
    if (method !== "GET") {
      const csrf = readCookie("xboard_csrf");
      if (csrf !== null) headers.set("X-CSRF-Token", csrf);
    }
    const response = await fetch(path, { method, headers, credentials: "same-origin", body: body === undefined ? undefined : JSON.stringify(body) });
    if (!response.ok) {
      const payload = (await response.json()) as ErrorEnvelope;
      const error = payload.status === "fail" ? payload.error : { code: "request_failed", message: "请求失败" };
      throw new APIError(response.status, error.code, error.message, error.fields);
    }
    return response.blob();
  }
}

function readCookie(name: string): string | null {
  const prefix = `${encodeURIComponent(name)}=`;
  const item = document.cookie.split("; ").find((cookie) => cookie.startsWith(prefix));
  return item === undefined ? null : decodeURIComponent(item.slice(prefix.length));
}

function distributorOrderQuery(query: DistributorOrderQuery): string {
	const params = new URLSearchParams();
	if (query.page !== undefined) params.set("page", String(query.page));
	if (query.page_size !== undefined) params.set("page_size", String(query.page_size));
	if (query.search !== undefined && query.search.trim() !== "") params.set("search", query.search.trim());
	if (query.settlement_status !== undefined) params.set("settlement_status", String(query.settlement_status));
	if (query.distributor_user_id !== undefined) params.set("distributor_user_id", String(query.distributor_user_id));
	return params.size === 0 ? "" : `?${params.toString()}`;
}
