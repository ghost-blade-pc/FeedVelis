import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../api/client'
import type { Account, SessionPayload } from '../../types/account'
import {
  createSession,
  type RefreshLock,
  type SessionChannel,
  type SessionMessage,
  type SessionSnapshot,
} from './session'

const account: Account = {
  id: '3f2504e0-4f89-41d3-9a0c-0305e82c3301',
  username: 'alice',
  nickname: 'alice',
  role: 'user',
  status: 'active',
  created_at: '2026-09-19T12:00:00Z',
}

function payload(expiresInMs: number, accessToken = 'access-1'): SessionPayload {
  return {
    access_token: accessToken,
    expires_at: new Date(START + expiresInMs).toISOString(),
    account,
  }
}

const START = Date.parse('2026-09-19T12:00:00Z')

/** 可手动推进的假时钟，避免测试依赖真实时间。 */
function fakeClock(start = START) {
  let current = start
  return {
    now: () => current,
    advance: (ms: number) => {
      current += ms
    },
  }
}

type Posted = SessionMessage[]

function fakeChannel(): SessionChannel & { posted: Posted; emit: (message: SessionMessage) => void } {
  const handlers = new Set<(message: SessionMessage) => void>()
  const posted: Posted = []
  return {
    posted,
    post: (message) => posted.push(message),
    subscribe(handler) {
      handlers.add(handler)
      return () => handlers.delete(handler)
    },
    emit: (message) => handlers.forEach((handler) => handler(message)),
  }
}

function harness(overrides: Partial<Parameters<typeof createSession>[0]> = {}) {
  const clock = fakeClock()
  const channel = fakeChannel()
  const refresh = vi.fn(async () => payload(15 * 60_000, 'refreshed'))
  const logout = vi.fn(async () => undefined)
  const snapshots: SessionSnapshot[] = []
  const requiresLogin = vi.fn()
  const manager = createSession({
    transport: { refresh, logout },
    csrfToken: () => 'csrf-value',
    channel,
    now: clock.now,
    onStateChange: (snapshot) => snapshots.push(snapshot),
    onRequiresLogin: requiresLogin,
    ...overrides,
  })
  return { manager, clock, channel, refresh, logout, snapshots, requiresLogin }
}

describe('会话内存驻留', () => {
  it('登录后令牌只在内存与广播通道中流转', () => {
    const { manager, channel } = harness()
    manager.applyLogin(payload(15 * 60_000))
    expect(manager.getSnapshot().accessToken).toBe('access-1')
    expect(channel.posted).toEqual([
      { type: 'token', accessToken: 'access-1', expiresAt: START + 15 * 60_000, account },
    ])
    expect(JSON.stringify(channel.posted)).not.toContain('velis_refresh')
  })

  it('令牌过期前 5 秒即视为需要刷新', async () => {
    const { manager, clock, refresh } = harness()
    manager.applyLogin(payload(15 * 60_000))
    clock.advance(15 * 60_000 - 4_000)
    await expect(manager.ensureAccessToken()).resolves.toBe('refreshed')
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('有效期内不重复刷新', async () => {
    const { manager, refresh } = harness()
    manager.applyLogin(payload(15 * 60_000))
    await expect(manager.ensureAccessToken()).resolves.toBe('access-1')
    expect(refresh).not.toHaveBeenCalled()
  })
})

describe('并发刷新协调', () => {
  it('同一标签内的并发请求只刷新一次', async () => {
    const { manager, refresh } = harness()
    const [first, second] = await Promise.all([manager.ensureAccessToken(), manager.ensureAccessToken()])
    expect(first).toBe('refreshed')
    expect(second).toBe('refreshed')
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('有 Web Locks 时先取锁，并在锁内复用其他标签的刷新结果', async () => {
    const clock = fakeClock()
    const channel = fakeChannel()
    let runs = 0
    const lock: RefreshLock = {
      run: async (fn) => {
        runs += 1
        return fn()
      },
    }
    const refresh = vi.fn(async () => payload(15 * 60_000, 'refreshed'))
    const manager = createSession({
      transport: { refresh, logout: async () => undefined },
      csrfToken: () => 'csrf-value',
      lock,
      channel,
      now: clock.now,
    })
    await manager.ensureAccessToken()
    expect(runs).toBe(1)
    expect(refresh).toHaveBeenCalledTimes(1)

    // 其他标签刷新后广播新令牌：下一次请求直接用广播结果，不再刷新。
    channel.emit({ type: 'token', accessToken: 'peer-token', expiresAt: clock.now() + 60_000, account })
    await expect(manager.ensureAccessToken()).resolves.toBe('peer-token')
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('无 Web Locks 时不做跨标签刷新协调，各标签各自刷新', async () => {
    // 规格已修订：无 Web Locks 时的租约降级移出 I1，留给后续提案单独设计（见 spec §4.3）。
    // 本用例钉住修订后的契约——没有 lock 就直接刷新，不再读写任何跨标签协调元数据；
    // 同时防止有人在没有设计的情况下重新引入一个做不到互斥的协调机制。
    const first = harness()
    const second = harness()
    await expect(first.manager.ensureAccessToken()).resolves.toBe('refreshed')
    await expect(second.manager.ensureAccessToken()).resolves.toBe('refreshed')
    expect(first.refresh).toHaveBeenCalledTimes(1)
    expect(second.refresh).toHaveBeenCalledTimes(1)
  })
})

describe('失败处理', () => {
  it('刷新失败时清空令牌、引导重新登录且不自动重放', async () => {
    const { manager, refresh, requiresLogin } = harness()
    refresh.mockRejectedValueOnce(new ApiError('登录状态无效', 401, 'AUTH_SESSION_INVALID'))
    await expect(manager.ensureAccessToken()).rejects.toBeInstanceOf(ApiError)
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(manager.getSnapshot().accessToken).toBeNull()
    expect(requiresLogin).toHaveBeenCalledTimes(1)
  })

  it('结果不明的网络失败同样不重放刷新请求', async () => {
    const { manager, refresh, requiresLogin } = harness()
    refresh.mockRejectedValueOnce(new TypeError('network error'))
    await expect(manager.ensureAccessToken()).rejects.toBeInstanceOf(TypeError)
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(requiresLogin).toHaveBeenCalled()
    expect(manager.getSnapshot().accessToken).toBeNull()
  })

  it('业务请求返回 401 时刷新一次并重试一次', async () => {
    const { manager, refresh } = harness()
    manager.applyLogin(payload(15 * 60_000))
    const exec = vi
      .fn<(token: string) => Promise<string>>()
      .mockRejectedValueOnce(new ApiError('登录状态无效', 401, 'AUTH_SESSION_INVALID'))
      .mockResolvedValueOnce('ok')
    await expect(manager.withAccessToken(exec)).resolves.toBe('ok')
    expect(exec).toHaveBeenNthCalledWith(1, 'access-1')
    expect(exec).toHaveBeenNthCalledWith(2, 'refreshed')
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('缺少 CSRF 值时按未登录处理', async () => {
    const { manager, refresh, requiresLogin } = harness({ csrfToken: () => null })
    await expect(manager.ensureAccessToken()).rejects.toEqual(
      expect.objectContaining({ code: 'CSRF_REJECTED' }),
    )
    expect(refresh).not.toHaveBeenCalled()
    expect(requiresLogin).toHaveBeenCalled()
  })
})

describe('退出与跨标签同步', () => {
  it('退出会调用服务端、清空内存并广播', async () => {
    const { manager, logout, channel } = harness()
    manager.applyLogin(payload(15 * 60_000))
    await manager.logout()
    expect(logout).toHaveBeenCalledWith('csrf-value')
    expect(manager.getSnapshot().accessToken).toBeNull()
    expect(channel.posted.at(-1)).toEqual({ type: 'logout' })
  })

  it('收到其他标签的退出广播后本地清空', () => {
    const { manager, channel } = harness()
    manager.applyLogin(payload(15 * 60_000))
    channel.emit({ type: 'logout' })
    expect(manager.getSnapshot().account).toBeNull()
  })

  it('恢复失败时静默返回未登录状态', async () => {
    const { manager, refresh } = harness()
    refresh.mockRejectedValueOnce(new ApiError('登录状态无效', 401, 'AUTH_SESSION_INVALID'))
    await expect(manager.restore()).resolves.toEqual({ accessToken: null, expiresAt: null, account: null })
  })
})
