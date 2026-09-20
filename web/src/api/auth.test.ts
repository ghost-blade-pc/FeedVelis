import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  fetchCurrentAccount,
  login,
  logoutSession,
  refreshSession,
  registerAccount,
  updateNickname,
} from './auth'

const account = {
  id: '3f2504e0-4f89-41d3-9a0c-0305e82c3301',
  username: 'alice',
  nickname: 'alice',
  role: 'user',
  status: 'active',
  created_at: '2026-09-19T12:00:00Z',
}

const session = { access_token: 'access-1', expires_at: '2026-09-19T12:15:00Z', account }

function stubFetch(body: unknown, status = 200) {
  const mock = vi.fn().mockResolvedValue(
    new Response(status === 204 ? null : JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', mock)
  return mock
}

function initOf(mock: ReturnType<typeof vi.fn>): RequestInit {
  return mock.mock.calls[0][1] as RequestInit
}

describe('认证请求', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('注册提交 JSON 且不带令牌', async () => {
    const mock = stubFetch(account, 201)
    await expect(registerAccount({ username: 'alice', password: 'Abcd123!' })).resolves.toEqual(account)
    expect(mock.mock.calls[0][0]).toBe('/api/v1/auth/register')
    expect(initOf(mock)).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ username: 'alice', password: 'Abcd123!' }),
      credentials: 'same-origin',
    })
  })

  it('登录返回访问令牌与账户', async () => {
    const mock = stubFetch(session)
    await expect(login('alice', 'Abcd123!')).resolves.toEqual(session)
    expect(initOf(mock).body).toBe(JSON.stringify({ username: 'alice', password: 'Abcd123!' }))
    expect((initOf(mock).headers as Record<string, string>).Authorization).toBeUndefined()
  })

  it('刷新与退出只带 CSRF 头', async () => {
    const refreshMock = stubFetch(session)
    await refreshSession('csrf-value')
    expect((initOf(refreshMock).headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-value')
    expect((initOf(refreshMock).headers as Record<string, string>).Authorization).toBeUndefined()

    const logoutMock = stubFetch(null, 204)
    await logoutSession('csrf-value')
    expect((initOf(logoutMock).headers as Record<string, string>)['X-CSRF-Token']).toBe('csrf-value')
  })

  it('本人资源请求带 Bearer 令牌', async () => {
    const getMock = stubFetch(account)
    await fetchCurrentAccount('access-1')
    expect((initOf(getMock).headers as Record<string, string>).Authorization).toBe('Bearer access-1')

    const patchMock = stubFetch(account)
    await updateNickname('access-1', '新昵称')
    expect(initOf(patchMock)).toMatchObject({ method: 'PATCH', body: JSON.stringify({ nickname: '新昵称' }) })
    expect((initOf(patchMock).headers as Record<string, string>).Authorization).toBe('Bearer access-1')
  })

  it('错误信封解析为带错误码的 ApiError', async () => {
    stubFetch({ error: { code: 'AUTH_RATE_LIMITED', message: '登录尝试过于频繁，请稍后重试' } }, 429)
    await expect(login('alice', 'wrong')).rejects.toEqual(
      expect.objectContaining({ name: 'ApiError', status: 429, code: 'AUTH_RATE_LIMITED' }),
    )
  })
})
