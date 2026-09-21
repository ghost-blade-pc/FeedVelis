import { logoutSession, refreshSession } from '../../api/auth'
import { createSession, type RefreshLock, type SessionChannel, type SessionManager } from './session'

/** 刷新锁名；多标签用它串行化刷新。 */
export const REFRESH_LOCK_NAME = 'velis-auth-refresh'
const CHANNEL_NAME = 'velis-auth'

export const CSRF_COOKIE_NAMES = ['__Host-velis_csrf', 'velis_csrf'] as const

/** 读取 CSRF Cookie；生产为 __Host- 前缀，本地开发为无前缀名。 */
export function readCsrfToken(cookieString: string): string | null {
  for (const name of CSRF_COOKIE_NAMES) {
    const value = readCookie(cookieString, name)
    if (value) {
      return value
    }
  }
  return null
}

function readCookie(cookieString: string, name: string): string | null {
  for (const part of cookieString.split(';')) {
    const [key, ...rest] = part.trim().split('=')
    if (key === name) {
      return rest.join('=')
    }
  }
  return null
}

/**
 * Web Locks 不可用时返回 null。此时不做跨标签刷新协调：各标签各自刷新，
 * 服务端按单次轮换与重放撤销处理；无 Web Locks 下的协调能力留给后续提案单独设计。
 */
export function createBrowserLock(): RefreshLock | null {
  if (typeof navigator === 'undefined' || !navigator.locks) {
    return null
  }
  return {
    run: (fn) => navigator.locks.request(REFRESH_LOCK_NAME, fn),
  }
}

export function createBrowserChannel(): SessionChannel | null {
  if (typeof BroadcastChannel === 'undefined') {
    return null
  }
  const channel = new BroadcastChannel(CHANNEL_NAME)
  return {
    post: (message) => channel.postMessage(message),
    subscribe(handler) {
      const listener = (event: MessageEvent) => handler(event.data)
      channel.addEventListener('message', listener)
      return () => channel.removeEventListener('message', listener)
    },
  }
}

/** 组装浏览器会话：Web Locks 串行化多标签刷新，令牌始终只在内存与广播通道中流转。 */
export function createBrowserSession(onRequiresLogin?: () => void): SessionManager {
  return createSession({
    transport: { refresh: refreshSession, logout: logoutSession },
    csrfToken: () => (typeof document === 'undefined' ? null : readCsrfToken(document.cookie)),
    lock: createBrowserLock(),
    channel: createBrowserChannel(),
    onRequiresLogin,
  })
}

/** 应用级单例；登录、刷新、退出与路由守卫都以它为准。 */
export const appSession = createBrowserSession()
