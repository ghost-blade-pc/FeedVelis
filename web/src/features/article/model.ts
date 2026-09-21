import type { ArticleItem } from '../../types/article'

export function safeArticleURL(value: string): string | undefined {
  try {
    const parsed = new URL(value)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && !parsed.username && !parsed.password
      ? parsed.href
      : undefined
  } catch {
    return undefined
  }
}

export function articleTime(item: ArticleItem): { label: string; value: string } {
  const sourceTime = item.origin.type === 'rss' ? item.origin.source_published_at : null
  return {
    label: sourceTime ? '原站发布于' : '发布于',
    value: sourceTime ?? item.published_at,
  }
}

export function articleOriginLabel(item: ArticleItem): string {
  return item.origin.type === 'rss' ? item.origin.source.title : item.origin.author.nickname
}

export function articleOriginURL(item: ArticleItem): string | undefined {
  return item.origin.type === 'rss' ? safeArticleURL(item.origin.canonical_url) : undefined
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
