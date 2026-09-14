// api.ts 后端接口封装。所有请求携带 Cookie（同源），并统一处理 401 → 跳登录。
import type {
  AccountProfile,
  AccountsResponse,
  ChatDelta,
  ChatMessage,
  ConfigResponse,
  Credits,
  ModelsResponse,
  OpResult,
  Overview,
  SessionInfo,
  StatsResponse,
  SystemInfo,
  TaskListResponse,
  TaskView,
  LoginSession,
} from './types'

/** ApiError 携带 HTTP 状态码，便于调用方区分 401 / 403 / 502。 */
export class ApiError extends Error {
  status: number
  code?: string
  constructor(status: number, message: string, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

/** onUnauthorized 由 App 注入：收到 401 时切回登录页。 */
let onUnauthorized: (() => void) | null = null
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: init?.body ? { 'Content-Type': 'application/json' } : undefined,
    ...init,
  })

  if (res.status === 401 && !path.startsWith('/api/session') && path !== '/api/login') {
    onUnauthorized?.()
  }

  const text = await res.text()
  let payload: unknown = null
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = { error: text }
    }
  }

  if (!res.ok) {
    // 错误体有两种形态：标准错误用 error，单账号操作结果（OpResult）用 message。
    // 两者都要读，否则导入凭证这类接口的具体原因会被通用的「请求失败」盖掉。
    const obj = (payload ?? {}) as { error?: string; message?: string; code?: string }
    const detail = obj.error || obj.message || `请求失败（HTTP ${res.status}）`
    throw new ApiError(res.status, detail, obj.code)
  }
  return payload as T
}

const get = <T,>(p: string) => request<T>(p)
const post = <T,>(p: string, body?: unknown, headers?: Record<string, string>) =>
  request<T>(p, {
    method: 'POST',
    body: body === undefined ? '{}' : JSON.stringify(body),
    headers: headers ? { 'Content-Type': 'application/json', ...headers } : undefined,
  })
const put = <T,>(p: string, body: unknown) =>
  request<T>(p, { method: 'PUT', body: JSON.stringify(body) })
const del = <T,>(p: string) => request<T>(p, { method: 'DELETE' })

export const api = {
  // 会话
  session: () => get<SessionInfo>('/api/session'),
  login: (username: string, password: string) =>
    post<{ token: string; username: string }>('/api/login', { username, password }),
  logout: () => post<{ ok: boolean }>('/api/logout'),
  changePassword: (current: string, next: string, newUsername?: string) =>
    post<{ ok: boolean; username: string; message: string; relogin?: boolean }>('/api/password', {
      current_password: current,
      new_password: next,
      new_username: newUsername,
    }),

  // 总览 / 账号
  overview: () => get<Overview>('/api/overview'),
  accounts: () => get<AccountsResponse>('/api/accounts'),
  account: (uid: string, silent = false) =>
    get<AccountProfile>(`/api/accounts/${encodeURIComponent(uid)}${silent ? '?silent=true' : ''}`),
  deleteAccount: (uid: string) =>
    del<{ ok: boolean; message: string }>(`/api/accounts/${encodeURIComponent(uid)}?confirm=${encodeURIComponent(uid)}`),
  importAccount: (payload: Record<string, unknown>) => post<OpResult>('/api/accounts/import', payload),

  accountCheckin: (uid: string) => post<OpResult>(`/api/accounts/${encodeURIComponent(uid)}/checkin`),
  accountRefresh: (uid: string) => post<OpResult>(`/api/accounts/${encodeURIComponent(uid)}/refresh`),
  accountTravel: (uid: string) => post<OpResult>(`/api/accounts/${encodeURIComponent(uid)}/travel`),
  accountCredits: (uid: string) => post<Credits>(`/api/accounts/${encodeURIComponent(uid)}/credits`),

  // 批量任务
  batchCheckin: (uids: string[]) => post<TaskView>('/api/tasks/checkin', { uids }),
  batchRefresh: (uids: string[]) => post<TaskView>('/api/tasks/refresh', { uids }),
  batchTravel: (uids: string[]) => post<TaskView>('/api/tasks/travel', { uids }),
  batchCredits: (uids: string[]) => post<TaskView>('/api/tasks/credits', { uids }),
  tasks: () => get<TaskListResponse>('/api/tasks'),
  task: (id: string) => get<TaskView>(`/api/tasks/${encodeURIComponent(id)}`),

  // 网页登录
  loginStart: (region: 'cn' | 'global') => post<LoginSession>('/api/login/start', { region }),
  loginPoll: (id: string) => post<LoginSession>(`/api/login/${encodeURIComponent(id)}/poll`),
  loginStatus: (id: string) => get<LoginSession>(`/api/login/${encodeURIComponent(id)}`),
  loginCancel: (id: string) => post<LoginSession>(`/api/login/${encodeURIComponent(id)}/cancel`),

  // 模型 / 聊天
  models: () => get<ModelsResponse>('/api/models'),
  // 请求统计（按模型聚合）+ 官方价换算
  stats: (mode?: 'peak' | 'offpeak') =>
    get<StatsResponse>(`/api/stats${mode ? `?mode=${mode}` : ''}`),
  resetStats: () => post<{ ok: boolean; message: string }>('/api/stats/reset'),
  // 官方价格表编辑
  savePrice: (p: {
    model: string
    cached_input: number
    miss_input: number
    output: number
    off_peak_ratio?: number
    note?: string
  }) => put<{ ok: boolean; message: string }>('/api/pricing', p),
  deletePrice: (model: string) =>
    del<{ ok: boolean; message: string }>(`/api/pricing/${encodeURIComponent(model)}`),
  chat: (payload: { model: string; messages: ChatMessage[]; extra?: Record<string, unknown>; conversationId?: string }) =>
    post<{ result: import('./types').ChatResult; elapsed_ms: number }>(
      '/api/chat',
      {
        model: payload.model,
        messages: payload.messages,
        stream: false,
        extra: payload.extra,
      },
      payload.conversationId ? { 'X-Conversation-Id': payload.conversationId } : undefined,
    ),

  // 配置
  config: () => get<ConfigResponse>('/api/config'),
  saveConfig: (doc: Record<string, unknown>) => put<{ ok: boolean; message: string }>('/api/config', doc),
  resetConfig: () => post<{ ok: boolean; message: string }>('/api/config/reset'),

  // 系统
  system: () => get<SystemInfo>('/api/system'),
  restart: () => post<{ ok: boolean; message: string }>('/api/system/restart'),
}

/** streamChat 流式聊天：逐帧回调，返回一个中止函数。 */
export function streamChat(
  payload: { model: string; messages: ChatMessage[]; extra?: Record<string, unknown>; conversationId?: string },
  onDelta: (d: ChatDelta) => void,
  onError: (msg: string) => void,
  onClose: () => void,
): () => void {
  const controller = new AbortController()

  fetch('/api/chat/stream', {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(payload.conversationId ? { 'X-Conversation-Id': payload.conversationId } : {}),
    },
    body: JSON.stringify({
      model: payload.model,
      messages: payload.messages,
      stream: true,
      extra: payload.extra,
    }),
    signal: controller.signal,
  })
    .then(async (res) => {
      if (!res.ok) {
        const text = await res.text()
        let msg = `请求失败（HTTP ${res.status}）`
        try {
          const obj = JSON.parse(text) as { error?: string }
          if (obj.error) msg = obj.error
        } catch {
          /* 非 JSON 响应：保留默认文案 */
        }
        if (res.status === 401) onUnauthorized?.()
        onError(msg)
        onClose()
        return
      }
      const reader = res.body?.getReader()
      if (!reader) {
        onError('服务器未返回流式响应')
        onClose()
        return
      }
      const decoder = new TextDecoder()
      let buffer = ''
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        // SSE 以空行分隔事件；逐事件解析，避免半截 JSON。
        const parts = buffer.split('\n\n')
        buffer = parts.pop() ?? ''
        for (const part of parts) {
          const line = part.split('\n').find((l) => l.startsWith('data: '))
          if (!line) continue
          try {
            const delta = JSON.parse(line.slice(6)) as ChatDelta
            onDelta(delta)
          } catch {
            /* 忽略无法解析的帧 */
          }
        }
      }
      onClose()
    })
    .catch((err: unknown) => {
      if ((err as { name?: string })?.name === 'AbortError') {
        onClose()
        return
      }
      onError(err instanceof Error ? err.message : String(err))
      onClose()
    })

  return () => controller.abort()
}
