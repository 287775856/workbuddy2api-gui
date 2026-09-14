// types.ts 与 Go 后端 JSON 结构一一对应的类型定义。

/** 账号合并视图（磁盘凭证 + 网关运行态 + 积分缓存）。 */
export interface Account {
  uid: string
  nickname: string
  enterprise_id: string
  domain: string

  has_file: boolean
  file_name: string
  expires_at: number
  expired: boolean
  needs_refresh: boolean
  has_refresh_token: boolean

  in_gateway: boolean
  /** healthy | cooling | disabled | token_expired | gateway_unreachable | missing_credential | unknown */
  status: string

  cooling: boolean
  cool_kind?: string
  cool_remaining_sec?: number
  disabled: boolean
  reason?: string
  in_flight: number
  breaker_fails: number
  breaker_until?: string
  soft_streak?: number
  success_count: number
  err_total: number
  last_success?: string
  last_err?: string

  credits: number
  live_credits?: number
  credits_at?: string
}

export interface Credits {
  remain: number
  used: number
  size: number
  packages: number
}

export interface CreditsTotal {
  remain: number
  used: number
  size: number
  accounts: number
  ok: number
  failed: number
}

export interface Health {
  healthy: number
  total: number
  service: string
}

export interface Overview {
  gateway_url: string
  gateway_ok: boolean
  gateway_error?: string
  health?: Health

  total: number
  healthy: number
  cooling: number
  disabled: number
  in_flight_full: number
  in_flight: number
  sticky_sessions: number
  redis_mode: string

  file_count: number
  expired: number
  expiring: number
  warnings?: string[]
  file_issues?: string[]

  credits: CreditsTotal
  read_only: boolean
  dangerous_ops: boolean
  server_time: string
}

export interface GatewayStatus {
  total: number
  healthy: number
  cooling: number
  disabled: number
  in_flight_full: number
  sticky_sessions: number
  redis_mode: string
}

export interface AccountsResponse {
  accounts: Account[]
  file_issues?: string[]
  gateway_ok: boolean
  gateway_error?: string
  summary?: GatewayStatus
}

export interface OpResult {
  uid: string
  action: string
  ok: boolean
  message: string
  reward?: number
  data?: Record<string, unknown>
}

export interface TaskItem {
  uid: string
  nickname: string
  action: string
  ok: boolean
  message: string
  reward?: number
  started_at: string
  ended_at: string
}

export interface TaskView {
  id: string
  kind: string
  title: string
  running: boolean
  error?: string
  started_at: string
  finished_at?: string
  total: number
  done: number
  ok: number
  failed: number
  items?: TaskItem[]
}

export interface TaskListResponse {
  tasks: TaskView[] | null
  running: TaskView[] | null
}

export type LoginState = 'pending' | 'success' | 'error' | 'expired' | 'cancelled'

export interface LoginSession {
  id: string
  region: 'cn' | 'global'
  auth_url: string
  status: LoginState
  message?: string
  created_at: string
  updated_at: string
  uid?: string
  nickname?: string
  saved: boolean
  file?: string
  restart?: string
}

export interface Model {
  id: string
  object: string
  created: number
  owned_by: string
  context_length: number
  max_output_tokens?: number
}

export interface ModelsResponse {
  data: Model[] | null
  count: number
}

export interface SessionInfo {
  authenticated: boolean
  username: string
  read_only: boolean
  dangerous_ops: boolean
  using_default_password: boolean
  gateway_url: string
  /** 服务端是否支持网页改密码（配置了 credentials_file）。 */
  password_changeable?: boolean
}

export interface ConfigMeta {
  path: string
  exists: boolean
  size: number
  mod_time?: string
  backup_path?: string
  backup_at?: string
  parse_error?: string
  is_valid_json: boolean
  restart_note: string
}

export interface ConfigResponse {
  config: Record<string, unknown>
  meta: ConfigMeta
}

export interface ContainerInfo {
  name: string
  available: boolean
  exists: boolean
  running: boolean
  status: string
  health: string
  image: string
  started_at?: string
  error?: string
  disabled: boolean
}

export interface SystemInfo {
  version: string
  started_at: string
  uptime_sec: number
  read_only: boolean
  dangerous_ops: boolean
  auth_dir: string
  config_file: string
  gateway_url: string
  using_default_password: boolean
  docker_available: boolean
  container: ContainerInfo
  gateway_health?: Health
  gateway_health_error?: string
}

export interface ChatResult {
  content: string
  reasoning_content?: string
  model: string
  finish_reason?: string
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  raw?: string
}

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
}

export interface AccountProfile {
  account: Account
  credits?: Credits
  credits_error?: string
  buddy?: { id: number; name: string } | null
  buddy_error?: string
  travel?: { state: string; daily_limit_reached: boolean; record_id: number; reward_credit: number }
  travel_error?: string
  upstream_error?: string
}

/** SSE 流式聊天的增量帧。 */
export interface ChatDelta {
  content?: string
  reasoning?: string
  done?: boolean
  error?: string
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }
  elapsed_ms?: number
  ttfb_ms?: number
}

/** 单个模型的派生统计（对应网关 /v1/stats 的 models[]）。 */
export interface ModelStat {
  model: string
  requests: number
  success: number
  failed: number
  streaming: number
  /** 平均首字延迟（毫秒） */
  avg_ttfb_ms: number
  /** 平均端到端耗时（毫秒） */
  avg_latency_ms: number
  /** 生成吞吐（输出 token / 秒） */
  tokens_per_sec: number
  prompt_tokens: number
  completion_tokens: number
  total_tokens: number
  cache_hit_tokens: number
  cache_miss_tokens: number
  cache_write_tokens: number
  /** 缓存命中率 0~1 */
  cache_hit_rate: number
  credit: number
  credit_per_req: number
  last_seen?: string
}

/** 网关 /v1/stats 响应。 */
export interface Stats {
  enabled: boolean
  message?: string
  since: string
  now: string
  uptime_sec: number
  total: ModelStat
  models: ModelStat[] | null
}

/** 单个模型的官方单价（元/百万 token）。 */
export interface ModelPrice {
  /** 缓存命中输入单价 */
  cached_input: number
  /** 缓存未命中输入单价 */
  miss_input: number
  /** 输出单价 */
  output: number
  /** 空闲时段价倍数（如 0.5）；0/1 = 不区分时段 */
  off_peak_ratio?: number
  note?: string
}

/** 单模型的官方价换算结果。 */
export interface ModelCost {
  model: string
  priced: boolean
  note?: string
  cached_input_cost: number
  miss_input_cost: number
  output_cost: number
  total: number
  cached_input_tokens: number
  miss_input_tokens: number
  output_tokens: number
}

/** 价格表元信息。 */
export interface PricingTable {
  models: Record<string, ModelPrice> | null
  source?: string
  updated_at?: string
  /** 服务端是否可保存编辑（配置了 pricing_file） */
  editable: boolean
}

/** /api/stats 的完整响应（统计 + 官方价换算）。 */
export interface StatsResponse {
  stats: Stats
  mode: 'peak' | 'offpeak'
  costs: Record<string, ModelCost> | null
  total: ModelCost
  priced: string[] | null
  unpriced: string[] | null
  pricing: PricingTable
}
