import { appSession } from '../features/auth/browser'
import type { ArticleFeedbackPage } from '../types/feedback'
import { request } from './client'

export function getArticleFeedback(ids: number[], signal?: AbortSignal): Promise<ArticleFeedbackPage> {
  const query = new URLSearchParams({ article_ids: ids.join(',') })
  return appSession.withAccessToken((accessToken) => request(`/api/v1/me/article-feedback?${query}`, { accessToken, signal }))
}

export function recordArticleRead(id: number): Promise<void> {
  return appSession.withAccessToken((accessToken) => request(`/api/v1/me/articles/${id}/reads`, { method: 'POST', accessToken }))
}

export function setArticleFavorite(id: number, enabled: boolean): Promise<void> {
  return appSession.withAccessToken((accessToken) => request(`/api/v1/me/articles/${id}/favorite`, {
    method: enabled ? 'PUT' : 'DELETE', accessToken,
  }))
}

export function setArticleNotInterested(id: number, enabled: boolean): Promise<void> {
  return appSession.withAccessToken((accessToken) => request(`/api/v1/me/articles/${id}/not-interested`, {
    method: enabled ? 'PUT' : 'DELETE', accessToken,
  }))
}
