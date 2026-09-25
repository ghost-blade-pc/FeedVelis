import { afterEach, describe, expect, it, vi } from 'vitest'

import { getArticle, listArticles, ping, searchArticles } from './client'

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
    const fixture = { items: [{ id: 1, title: '标题', excerpt: '摘要', published_at: '2026-09-23T00:00:00Z', origin: { type: 'user', author: { id: 'u', nickname: '作者' } }, enhancement: null }], next_cursor: null, has_more: false }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(fixture)))
    vi.stubGlobal('fetch', fetchMock)
    await expect(listArticles('opaque', 10)).resolves.toEqual(fixture)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/articles?limit=10&cursor=opaque', expect.any(Object))
  })
})

describe('getArticle', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('读取文章详情并把失败映射为 ApiError', async () => {
    const fixture = { id: 7, title: '标题', excerpt: '摘要', published_at: '2026-09-23T00:00:00Z', origin: { type: 'user', author: { id: 'u', nickname: '作者' } }, enhancement: { summary: 'AI 摘要', keywords: ['词'], topics: ['主题'], generated_at: '2026-09-23T00:01:00Z' }, content_html: '<p>正文</p>' }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(fixture)))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getArticle(7)).resolves.toEqual(fixture)
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/articles/7', expect.any(Object))

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 404 })))
    await expect(getArticle(999)).rejects.toEqual(expect.objectContaining({ name: 'ApiError', status: 404 }))
  })
})

describe('searchArticles', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('编码 Unicode、空白、可选过滤与续页游标，并传递 AbortSignal', async () => {
    const fixture = { items: [], next_cursor: null, has_more: false }
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(fixture)))
    vi.stubGlobal('fetch', fetchMock)
    const controller = new AbortController()
    await expect(searchArticles({
      q: '中文 Go', keyword: 'C++ 空格', topic: '主题/URL', source_id: 9, limit: 12, cursor: 'opaque+/=',
    }, controller.signal)).resolves.toEqual(fixture)
    const [url, options] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/search/articles?q=%E4%B8%AD%E6%96%87+Go&limit=12&keyword=C%2B%2B+%E7%A9%BA%E6%A0%BC&topic=%E4%B8%BB%E9%A2%98%2FURL&source_id=9&cursor=opaque%2B%2F%3D')
    expect(options.signal).toBe(controller.signal)
  })

  it('保留 SEARCH_UNAVAILABLE 错误码', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: 'SEARCH_UNAVAILABLE', message: '搜索暂不可用' },
    }), { status: 503 })))
    await expect(searchArticles({ q: 'go' })).rejects.toEqual(expect.objectContaining({
      name: 'ApiError', status: 503, code: 'SEARCH_UNAVAILABLE', message: '搜索暂不可用',
    }))
  })
})
