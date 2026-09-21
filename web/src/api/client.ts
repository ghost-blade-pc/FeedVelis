import type { ArticleDetail, ArticlePage } from '../types/article'

export interface PingResponse {
  message: 'pong'
}

/** 统一错误：携带 HTTP 状态与后端错误码，便于区分认证、限流与校验失败。 */
export class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code = '',
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE' | 'HEAD'
  body?: unknown
  signal?: AbortSignal
  accessToken?: string | null
  headers?: Record<string, string>
}

export interface ApiResponse<T> {
  body: T
  etag: string | null
}

interface ErrorEnvelope {
  error?: { code?: string; message?: string }
}

/** 统一请求入口：同源凭证、Bearer 令牌与错误信封解析都只在这里处理。 */
export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  return (await requestWithMeta<T>(path, options)).body
}

/** 需要乐观锁的资源同时返回响应 ETag；普通调用继续使用 request。 */
export async function requestWithMeta<T>(path: string, options: RequestOptions = {}): Promise<ApiResponse<T>> {
  const headers: Record<string, string> = { Accept: 'application/json', ...options.headers }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  if (options.accessToken) {
    headers.Authorization = `Bearer ${options.accessToken}`
  }
  const response = await fetch(path, {
    method: options.method ?? 'GET',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    credentials: 'same-origin',
    signal: options.signal,
  })
  if (!response.ok) {
    throw await toApiError(response)
  }
  if (response.status === 204) {
    return { body: undefined as T, etag: response.headers.get('ETag') }
  }
  return { body: (await response.json()) as T, etag: response.headers.get('ETag') }
}

async function toApiError(response: Response): Promise<ApiError> {
  const fallback = defaultMessage(response.status)
  try {
    const envelope = (await response.json()) as ErrorEnvelope
    const code = envelope.error?.code ?? ''
    const message = envelope.error?.message ?? fallback
    return new ApiError(message, response.status, code)
  } catch {
    return new ApiError(fallback, response.status)
  }
}

function defaultMessage(status: number): string {
  switch (status) {
    case 401:
      return '登录状态无效'
    case 403:
      return '请求被拒绝'
    case 429:
      return '操作过于频繁'
    default:
      return '服务暂不可用'
  }
}

export async function ping(signal?: AbortSignal): Promise<PingResponse> {
  return request<PingResponse>('/api/v1/ping', { signal })
}

export async function listArticles(cursor?: string, limit = 20, signal?: AbortSignal): Promise<ArticlePage> {
  const query = new URLSearchParams({ limit: String(limit) })
  if (cursor) query.set('cursor', cursor)
  return request<ArticlePage>(`/api/v1/articles?${query.toString()}`, { signal })
}

export async function getArticle(id: number, signal?: AbortSignal): Promise<ArticleDetail> {
  return request<ArticleDetail>(`/api/v1/articles/${id}`, { signal })
}
