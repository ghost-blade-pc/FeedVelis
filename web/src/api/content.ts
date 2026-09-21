import { appSession } from '../features/auth/browser'
import type { AssetUpload, ArticleAsset, ArticlePreview, MyArticleDetail, MyArticlePage } from '../types/content'
import type { AdminSource, SourceFetchRun, SourceFetchRunPage, SourcePage } from '../types/source'
import { request, requestWithMeta, type ApiResponse } from './client'

export const newOperationKey = (): string => crypto.randomUUID()
const etag = (version: number): string => `"${version}"`

function authenticated<T>(run: (token: string) => Promise<T>): Promise<T> {
  return appSession.withAccessToken(run)
}

function writeHeaders(operationKey: string, version?: number): Record<string, string> {
  const headers: Record<string, string> = { 'Idempotency-Key': operationKey }
  if (version !== undefined) headers['If-Match'] = etag(version)
  return headers
}

export function listMyArticles(cursor?: string, limit = 20, signal?: AbortSignal): Promise<MyArticlePage> {
  const query = new URLSearchParams({ limit: String(limit) })
  if (cursor) query.set('cursor', cursor)
  return authenticated((accessToken) => request(`/api/v1/me/articles?${query}`, { accessToken, signal }))
}

export function getMyArticle(id: number, signal?: AbortSignal): Promise<ApiResponse<MyArticleDetail>> {
  return authenticated((accessToken) => requestWithMeta(`/api/v1/me/articles/${id}`, { accessToken, signal }))
}

export function createMyArticle(
  input: { title: string; markdown: string; initial_status: 'draft' | 'published' },
  operationKey = newOperationKey(),
): Promise<ApiResponse<MyArticleDetail>> {
  return authenticated((accessToken) => requestWithMeta('/api/v1/me/articles', {
    method: 'POST', body: input, accessToken, headers: writeHeaders(operationKey),
  }))
}

export function previewMyArticle(input: { title: string; markdown: string }): Promise<ArticlePreview> {
  return authenticated((accessToken) => request('/api/v1/me/articles/preview', {
    method: 'POST', body: input, accessToken,
  }))
}

export function updateMyArticle(
  id: number, version: number, input: { title: string; markdown: string }, operationKey = newOperationKey(),
): Promise<ApiResponse<MyArticleDetail>> {
  return authenticated((accessToken) => requestWithMeta(`/api/v1/me/articles/${id}`, {
    method: 'PATCH', body: input, accessToken, headers: writeHeaders(operationKey, version),
  }))
}

export function changeMyArticleState(
  id: number, action: 'publish' | 'offline', version: number, operationKey = newOperationKey(),
): Promise<ApiResponse<MyArticleDetail>> {
  return authenticated((accessToken) => requestWithMeta(`/api/v1/me/articles/${id}/${action}`, {
    method: 'POST', body: {}, accessToken, headers: writeHeaders(operationKey, version),
  }))
}

export function deleteMyArticle(id: number, version: number, operationKey = newOperationKey()): Promise<void> {
  return authenticated((accessToken) => request(`/api/v1/me/articles/${id}`, {
    method: 'DELETE', accessToken, headers: writeHeaders(operationKey, version),
  }))
}

export function createAsset(file: Pick<File, 'type' | 'size'>, operationKey = newOperationKey()): Promise<AssetUpload> {
  return authenticated((accessToken) => request('/api/v1/me/assets', {
    method: 'POST', body: { content_type: file.type, size_bytes: file.size }, accessToken,
    headers: writeHeaders(operationKey),
  }))
}

export function confirmAsset(assetID: string, operationKey = newOperationKey()): Promise<ArticleAsset> {
  return authenticated((accessToken) => request(`/api/v1/me/assets/${assetID}/confirm`, {
    method: 'POST', body: {}, accessToken, headers: writeHeaders(operationKey),
  }))
}

export function uploadAssetObject(
  upload: AssetUpload, file: File, onProgress?: (percent: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(upload.upload_method, upload.upload_url)
    for (const [name, value] of Object.entries(upload.upload_headers)) xhr.setRequestHeader(name, value)
    xhr.upload.addEventListener('progress', (event) => {
      if (event.lengthComputable) onProgress?.(Math.round((event.loaded / event.total) * 100))
    })
    xhr.addEventListener('load', () => xhr.status >= 200 && xhr.status < 300
      ? resolve()
      : reject(new Error(`对象上传失败（HTTP ${xhr.status}）`)))
    xhr.addEventListener('error', () => reject(new Error('对象上传网络失败，结果可能未知')))
    xhr.send(file)
  })
}

export function listAdminSources(): Promise<SourcePage> {
  return authenticated((accessToken) => request('/api/v1/admin/sources', { accessToken }))
}

export function createAdminSource(
  input: { feed_url: string; fetch_interval_seconds?: number }, operationKey = newOperationKey(),
): Promise<ApiResponse<AdminSource>> {
  return authenticated((accessToken) => requestWithMeta('/api/v1/admin/sources', {
    method: 'POST', body: input, accessToken, headers: writeHeaders(operationKey),
  }))
}

export function updateAdminSource(
  id: number, version: number, fetchIntervalSeconds: number, operationKey = newOperationKey(),
): Promise<ApiResponse<AdminSource>> {
  return authenticated((accessToken) => requestWithMeta(`/api/v1/admin/sources/${id}`, {
    method: 'PATCH', body: { fetch_interval_seconds: fetchIntervalSeconds }, accessToken,
    headers: writeHeaders(operationKey, version),
  }))
}

export function changeAdminSourceState(
  id: number, action: 'pause' | 'resume', version: number, operationKey = newOperationKey(),
): Promise<ApiResponse<AdminSource>> {
  return authenticated((accessToken) => requestWithMeta(`/api/v1/admin/sources/${id}/${action}`, {
    method: 'POST', body: {}, accessToken, headers: writeHeaders(operationKey, version),
  }))
}

export function fetchAdminSource(id: number, operationKey = newOperationKey()): Promise<SourceFetchRun> {
  return authenticated((accessToken) => request(`/api/v1/admin/sources/${id}/fetches`, {
    method: 'POST', body: {}, accessToken, headers: writeHeaders(operationKey),
  }))
}

export function listAdminSourceRuns(id: number, cursor?: string, limit = 50): Promise<SourceFetchRunPage> {
  const query = new URLSearchParams({ limit: String(limit) })
  if (cursor) query.set('cursor', cursor)
  return authenticated((accessToken) => request(`/api/v1/admin/sources/${id}/fetches?${query}`, { accessToken }))
}

export function changeAdminArticleState(
  id: number, action: 'offline' | 'restore', version: number, operationKey = newOperationKey(),
): Promise<unknown> {
  return authenticated((accessToken) => request(`/api/v1/admin/articles/${id}/${action}`, {
    method: 'POST', body: {}, accessToken, headers: writeHeaders(operationKey, version),
  }))
}
