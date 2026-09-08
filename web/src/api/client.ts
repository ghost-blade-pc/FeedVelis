export interface PingResponse {
  message: 'pong'
}

export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export async function ping(signal?: AbortSignal): Promise<PingResponse> {
  const response = await fetch('/api/v1/ping', {
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!response.ok) {
    throw new ApiError('API 暂不可用', response.status)
  }
  return (await response.json()) as PingResponse
}

export async function listArticles(cursor?: string, limit = 20, signal?: AbortSignal): Promise<ArticlePage> {
  const query = new URLSearchParams({ limit: String(limit) })
  if (cursor) query.set('cursor', cursor)
  const response = await fetch(`/api/v1/articles?${query.toString()}`, {
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!response.ok) {
    throw new ApiError('文章列表暂不可用', response.status)
  }
  return (await response.json()) as ArticlePage
}
import type { ArticlePage } from '../types/article'
