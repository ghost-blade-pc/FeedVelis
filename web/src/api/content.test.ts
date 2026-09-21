import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { appSession } from '../features/auth/browser'
import { createMyArticle, updateAdminSource, updateMyArticle } from './content'

function applySession(token = 'old-token') {
  appSession.applyLogin({
    access_token: token,
    expires_at: new Date(Date.now() + 60_000).toISOString(),
    account: { id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z' },
  })
}

describe('内容 API 客户端', () => {
  beforeEach(() => applySession())
  afterEach(() => {
    appSession.clear()
    vi.unstubAllGlobals()
  })

  it('文章写入发送幂等键、强 If-Match 并读取 ETag', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 7, lock_version: 4 }), {
      headers: { 'Content-Type': 'application/json', ETag: '"4"' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(updateMyArticle(7, 3, { title: '标题', markdown: '正文' }, 'operation-1'))
      .resolves.toEqual({ body: { id: 7, lock_version: 4 }, etag: '"4"' })
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.method).toBe('PATCH')
    expect(init.headers).toMatchObject({
      Authorization: 'Bearer old-token',
      'Idempotency-Key': 'operation-1',
      'If-Match': '"3"',
    })
  })

  it('Source 修改使用同一并发与幂等协议', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 2, lock_version: 9 }), {
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    await updateAdminSource(2, 8, 900, 'source-operation')
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.headers).toMatchObject({ 'Idempotency-Key': 'source-operation', 'If-Match': '"8"' })
    expect(init.body).toBe(JSON.stringify({ fetch_interval_seconds: 900 }))
  })

  it('认证重试复用一次调用已经生成的幂等键', async () => {
    vi.stubGlobal('document', { cookie: 'velis_csrf=csrf-value' })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { code: 'AUTH_SESSION_INVALID' } }), {
        status: 401, headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({
        access_token: 'new-token', expires_at: new Date(Date.now() + 60_000).toISOString(),
        account: { id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z' },
      }), { headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 7 }), {
        status: 201, headers: { 'Content-Type': 'application/json' },
      }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(createMyArticle({ title: '标题', markdown: '正文', initial_status: 'draft' }, 'fixed-key'))
      .resolves.toEqual({ body: { id: 7 }, etag: null })
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).toMatchObject({ 'Idempotency-Key': 'fixed-key' })
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/auth/refresh')
    expect((fetchMock.mock.calls[2][1] as RequestInit).headers).toMatchObject({
      Authorization: 'Bearer new-token', 'Idempotency-Key': 'fixed-key',
    })
  })
})
