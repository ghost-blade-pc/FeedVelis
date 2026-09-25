import type { ArticleSearchQuery } from '../../types/article'

export interface SearchFormValues {
  q: string
  keyword: string
  topic: string
  sourceID: string
}

export function normalizeSearchForm(form: SearchFormValues): ArticleSearchQuery {
  const q = form.q.trim()
  const keyword = form.keyword.trim()
  const topic = form.topic.trim()
  if (q.length === 0 || [...q].length > 200) throw new Error('搜索词必须为 1 到 200 个字符')
  if (keyword && [...keyword].length > 64) throw new Error('关键词最多 64 个字符')
  if (topic && [...topic].length > 64) throw new Error('主题最多 64 个字符')
  let sourceID: number | undefined
  if (form.sourceID.trim()) {
    sourceID = Number(form.sourceID)
    if (!Number.isSafeInteger(sourceID) || sourceID <= 0) throw new Error('来源 ID 必须是正整数')
  }
  return { q, keyword: keyword || undefined, topic: topic || undefined, source_id: sourceID, limit: 20 }
}

export function searchRouteQuery(query: ArticleSearchQuery): Record<string, string> {
  const route: Record<string, string> = { q: query.q }
  if (query.keyword) route.keyword = query.keyword
  if (query.topic) route.topic = query.topic
  if (query.source_id !== undefined) route.source_id = String(query.source_id)
  return route
}
