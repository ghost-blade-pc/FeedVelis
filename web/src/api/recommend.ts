import { appSession } from '../features/auth/browser'
import type { ArticleRecommendPage } from '../types/article'
import { request } from './client'

export function listRecommendedArticles(cursor?: string, limit = 20, signal?: AbortSignal): Promise<ArticleRecommendPage> {
  const query = new URLSearchParams({ limit: String(limit) })
  if (cursor) query.set('cursor', cursor)
  const path = `/api/v1/articles/recommend?${query}`
  if (!appSession.getSnapshot().account) return request(path, { signal })
  return appSession.withAccessToken((accessToken) => request(path, { accessToken, signal }))
}
