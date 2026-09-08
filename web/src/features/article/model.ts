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
  const sourceTime = item.source_published_at
  return {
    label: sourceTime ? '发布于' : '收录于',
    value: sourceTime ?? item.discovered_at,
  }
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(date)
}
