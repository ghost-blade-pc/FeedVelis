import { describe, expect, it } from 'vitest'

import { ApiError } from '../../api/client'
import type { ArticleSearchPage, ArticleSearchQuery } from '../../types/article'
import { useArticleSearch } from './useArticleSearch'

const page = (id: number): ArticleSearchPage => ({
  items: [{ id, title: `文章${id}`, excerpt: '', published_at: '2026-09-25T00:00:00Z', origin: { type: 'user', author: { id: 'u', nickname: '作者' } }, enhancement: null }],
  next_cursor: null, has_more: false,
})

describe('搜索请求状态机', () => {
  it('条件变化会取消旧请求且过期结果不能覆盖新查询', async () => {
    const pending = new Map<string, (value: ArticleSearchPage) => void>()
    const aborted: string[] = []
    const request = (query: ArticleSearchQuery, signal?: AbortSignal) => new Promise<ArticleSearchPage>((resolve) => {
      pending.set(query.q, resolve)
      signal?.addEventListener('abort', () => aborted.push(query.q))
    })
    const search = useArticleSearch(request)
    const oldRequest = search.start({ q: 'old' })
    const newRequest = search.start({ q: 'new' })
    pending.get('new')?.(page(2))
    await newRequest
    pending.get('old')?.(page(1))
    await oldRequest
    expect(aborted).toContain('old')
    expect(search.items.value.map((item) => item.id)).toEqual([2])
  })

  it('区分 unavailable、empty，并在取消时清空旧结果', async () => {
    let mode: 'unavailable' | 'empty' = 'unavailable'
    const search = useArticleSearch(async () => {
      if (mode === 'unavailable') throw new ApiError('搜索暂不可用', 503, 'SEARCH_UNAVAILABLE')
      return { items: [], next_cursor: null, has_more: false }
    })
    await search.start({ q: 'go' })
    expect(search.state.value).toBe('unavailable')
    mode = 'empty'
    await search.retry()
    expect(search.state.value).toBe('empty')
    search.cancelAndClear()
    expect(search.state.value).toBe('idle')
    expect(search.items.value).toEqual([])
  })
})
