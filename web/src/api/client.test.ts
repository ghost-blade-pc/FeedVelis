import { afterEach, describe, expect, it, vi } from 'vitest'

import { ping } from './client'

describe('ping', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('返回 API 响应', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ message: 'pong' }))))
    await expect(ping()).resolves.toEqual({ message: 'pong' })
  })

  it('将非成功状态映射为 ApiError', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 503 })))
    await expect(ping()).rejects.toEqual(expect.objectContaining({ name: 'ApiError', status: 503 }))
  })
})
