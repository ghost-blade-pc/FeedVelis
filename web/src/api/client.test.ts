import { afterEach, describe, expect, it, vi } from 'vitest'

import { getArticle, listArticles, ping } from './client'

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

describe('listArticles', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('发送不透明游标并读取分页响应', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [], next_cursor: null, has_more: false })))
    vi.stubGlobal('fetch', fetchMock)
    await expect(listArticles('opaque', 10)).resolves.toEqual({ items: [], next_cursor: null, has_more: false })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/articles?limit=10&cursor=opaque', expect.any(Object))
  })
})

describe('getArticle', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('读取文章详情并把失败映射为 ApiError', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 7, title: '标题', content_html: '<p>正文</p>' })))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getArticle(7)).resolves.toEqual({ id: 7, title: '标题', content_html: '<p>正文</p>' })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/articles/7', expect.any(Object))

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 404 })))
    await expect(getArticle(999)).rejects.toEqual(expect.objectContaining({ name: 'ApiError', status: 404 }))
  })
})
