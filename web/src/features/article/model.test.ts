import { describe, expect, it } from 'vitest'

import type { ArticleItem } from '../../types/article'
import { articleTime, safeArticleURL } from './model'

const base: ArticleItem = {
  id: 1,
  title: '标题',
  canonical_url: 'https://example.com/a',
  source: { id: 1, title: '来源', site_url: null },
  author_name: null,
  excerpt: '摘要',
  source_published_at: null,
  discovered_at: '2026-09-07T00:00:00Z',
}

describe('article model', () => {
  it('只允许 HTTP(S) 原文链接', () => {
    expect(safeArticleURL('https://example.com/a')).toBe('https://example.com/a')
    expect(safeArticleURL('javascript:alert(1)')).toBeUndefined()
    expect(safeArticleURL('data:text/html,x')).toBeUndefined()
    expect(safeArticleURL('https://user:secret@example.com/a')).toBeUndefined()
  })

  it('缺失上游发布时间时显示收录时间', () => {
    expect(articleTime(base)).toEqual({ label: '收录于', value: base.discovered_at })
    expect(articleTime({ ...base, source_published_at: '2026-09-01T00:00:00Z' }).label).toBe('发布于')
  })
})
