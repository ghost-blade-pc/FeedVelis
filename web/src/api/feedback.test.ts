import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { appSession } from '../features/auth/browser'
import { getArticleFeedback, recordArticleRead, setArticleFavorite, setArticleNotInterested } from './feedback'

describe('文章反馈 API', () => {
  beforeEach(() => appSession.applyLogin({ access_token: 'feedback-token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } }))
  afterEach(() => { appSession.clear(); vi.unstubAllGlobals() })

  it('批量读取和写入使用本人路径与 Bearer 令牌', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ items: [] }), { headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    await getArticleFeedback([1, 2])
    await recordArticleRead(1)
    await setArticleFavorite(1, true)
    await setArticleFavorite(1, false)
    await setArticleNotInterested(1, true)
    await setArticleNotInterested(1, false)
    expect(fetchMock.mock.calls.map((call) => [call[0], (call[1] as RequestInit).method])).toEqual([
      ['/api/v1/me/article-feedback?article_ids=1%2C2', 'GET'],
      ['/api/v1/me/articles/1/reads', 'POST'],
      ['/api/v1/me/articles/1/favorite', 'PUT'],
      ['/api/v1/me/articles/1/favorite', 'DELETE'],
      ['/api/v1/me/articles/1/not-interested', 'PUT'],
      ['/api/v1/me/articles/1/not-interested', 'DELETE'],
    ])
    for (const [, init] of fetchMock.mock.calls) expect(init.headers).toMatchObject({ Authorization: 'Bearer feedback-token' })
  })

  it('失败时传播 API 错误', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'ARTICLE_NOT_FOUND', message: '文章不可见' } }), { status: 404 })))
    await expect(setArticleFavorite(7, true)).rejects.toMatchObject({ status: 404, code: 'ARTICLE_NOT_FOUND' })
  })
})
