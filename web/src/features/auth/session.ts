import { ApiError } from '../../api/client'
import type { Account, SessionPayload } from '../../types/account'

/** 访问令牌提前视为过期的安全边界，避免刚好在到期瞬间发出请求。 */
const EXPIRY_SKEW_MS = 5_000

export interface SessionSnapshot {
  accessToken: string | null
  expiresAt: number | null
  account: Account | null
}

export const emptySnapshot: SessionSnapshot = { accessToken: null, expiresAt: null, account: null }

export interface AuthTransport {
  refresh(csrfToken: string, signal?: AbortSignal): Promise<SessionPayload>
  logout(csrfToken: string): Promise<void>
}

export type SessionMessage =
  | { type: 'token'; accessToken: string; expiresAt: number; account: Account }
  | { type: 'logout' }

/** 跨标签串行化刷新；浏览器实现基于 Web Locks。 */
export interface RefreshLock {
  run<T>(fn: () => Promise<T>): Promise<T>
}

export interface SessionChannel {
  post(message: SessionMessage): void
  subscribe(handler: (message: SessionMessage) => void): () => void
}

export interface SessionOptions {
  transport: AuthTransport
  csrfToken: () => string | null
  lock?: RefreshLock | null
  channel?: SessionChannel | null
  now?: () => number
  onStateChange?: (snapshot: SessionSnapshot) => void
  onRequiresLogin?: () => void
}

export interface SessionManager {
  getSnapshot(): SessionSnapshot
  subscribe(listener: (snapshot: SessionSnapshot) => void): () => void
  /** 登录成功后写入内存令牌并广播给其他标签。 */
  applyLogin(payload: SessionPayload): SessionSnapshot
  /** 仅清空本地状态；不发起退出请求。 */
  clear(): void
  /** 页面重载后尝试用刷新 Cookie 恢复会话；失败时静默返回未登录。 */
  restore(): Promise<SessionSnapshot>
  /** 返回可用的访问令牌；不可恢复时抛错。 */
  ensureAccessToken(): Promise<string>
  /** 带令牌执行请求；仅在服务端拒绝令牌时刷新并重试一次。 */
  withAccessToken<T>(exec: (accessToken: string) => Promise<T>): Promise<T>
  logout(): Promise<void>
}

export function createSession(options: SessionOptions): SessionManager {
  const now = options.now ?? (() => Date.now())
  const listeners = new Set<(snapshot: SessionSnapshot) => void>()
  let snapshot: SessionSnapshot = emptySnapshot
  let inFlight: Promise<SessionSnapshot> | null = null

  options.channel?.subscribe((message) => {
    if (message.type === 'token') {
      setSnapshot({ accessToken: message.accessToken, expiresAt: message.expiresAt, account: message.account })
    } else {
      setSnapshot(emptySnapshot)
    }
  })

  function setSnapshot(next: SessionSnapshot): void {
    snapshot = next
    options.onStateChange?.(next)
    for (const listener of listeners) {
      listener(next)
    }
  }

  function applyLogin(payload: SessionPayload): SessionSnapshot {
    const next = snapshotOf(payload)
    setSnapshot(next)
    options.channel?.post({
      type: 'token',
      accessToken: payload.access_token,
      expiresAt: next.expiresAt ?? 0,
      account: payload.account,
    })
    return next
  }

  function clear(): void {
    setSnapshot(emptySnapshot)
  }

  function usable(current: SessionSnapshot): boolean {
    return Boolean(current.accessToken) && (current.expiresAt ?? 0) - EXPIRY_SKEW_MS > now()
  }

  async function restore(): Promise<SessionSnapshot> {
    if (usable(snapshot)) {
      return snapshot
    }
    try {
      await ensureAccessToken()
    } catch {
      // 没有可恢复的会话是正常状态，不向调用方抛错。
    }
    return snapshot
  }

  async function ensureAccessToken(): Promise<string> {
    if (usable(snapshot)) {
      return snapshot.accessToken as string
    }
    const refreshed = await refreshOnce()
    if (!refreshed.accessToken) {
      throw new ApiError('需要重新登录', 401, 'AUTH_SESSION_INVALID')
    }
    return refreshed.accessToken
  }

  /** 同一标签内的并发刷新合并为一次请求。 */
  function refreshOnce(): Promise<SessionSnapshot> {
    if (!inFlight) {
      inFlight = performRefresh().finally(() => {
        inFlight = null
      })
    }
    return inFlight
  }

  async function performRefresh(): Promise<SessionSnapshot> {
    try {
      if (options.lock) {
        return await options.lock.run(async () => {
          // 拿到锁后其他标签可能已经刷新成功，避免重复请求。
          return usable(snapshot) ? snapshot : callRefresh()
        })
      }
      return await callRefresh()
    } catch (error) {
      // 规格要求：刷新失败不自动重放；清空内存令牌并引导重新登录。
      setSnapshot(emptySnapshot)
      options.onRequiresLogin?.()
      throw error
    }
  }

  async function callRefresh(): Promise<SessionSnapshot> {
    const csrf = options.csrfToken()
    if (!csrf) {
      throw new ApiError('缺少 CSRF 值，请重新登录', 401, 'CSRF_REJECTED')
    }
    const next = applyLogin(await options.transport.refresh(csrf))
    return next
  }

  async function withAccessToken<T>(exec: (accessToken: string) => Promise<T>): Promise<T> {
    const accessToken = await ensureAccessToken()
    try {
      return await exec(accessToken)
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 401) {
        throw error
      }
      setSnapshot(emptySnapshot)
      const refreshed = await refreshOnce()
      if (!refreshed.accessToken) {
        throw error
      }
      return await exec(refreshed.accessToken)
    }
  }

  async function logout(): Promise<void> {
    const csrf = options.csrfToken()
    try {
      if (csrf) {
        await options.transport.logout(csrf)
      }
    } finally {
      setSnapshot(emptySnapshot)
      options.channel?.post({ type: 'logout' })
    }
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    applyLogin,
    clear,
    restore,
    ensureAccessToken,
    withAccessToken,
    logout,
  }
}

function snapshotOf(payload: SessionPayload): SessionSnapshot {
  return {
    accessToken: payload.access_token,
    expiresAt: Date.parse(payload.expires_at),
    account: payload.account,
  }
}
