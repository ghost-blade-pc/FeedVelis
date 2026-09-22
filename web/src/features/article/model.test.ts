import { describe, expect, it } from 'vitest'

import type { ArticleItem } from '../../types/article'
import { articleOriginLabel, articleOriginURL, articleTime, safeArticleURL } from './model'

const rss: ArticleItem = {
  id: 1,
  title: '标题',
  excerpt: '摘要',
  published_at: '2026-09-02T00:00:00Z',
  origin: {
    type: 'rss',
    source: { id: 1, title: '示例 Feed', site_url: null },
    canonical_url: 'https://example.com/a',
    source_published_at: '2026-09-01T00:00:00Z',
  },
}

const user: ArticleItem = {
  id: 2,
  title: '投稿',
  excerpt: '摘要',
  published_at: '2026-09-02T00:00:00Z',
  origin: { type: 'user', author: { id: 'u1', nickname: '小唯' } },
}

const rssWithoutSourceTime: ArticleItem = {
  id: 3,
  title: '缺失原站时间',
  excerpt: '摘要',
  published_at: '2026-09-02T00:00:00Z',
  origin: {
    type: 'rss',
    source: { id: 1, title: '示例 Feed', site_url: null },
    canonical_url: 'https://example.com/no-source-time',
    source_published_at: null,
  },
}

describe('article model', () => {
  it('只允许无凭据 HTTP(S) 原文链接', () => {
    expect(safeArticleURL('https://example.com/a')).toBe('https://example.com/a')
    expect(safeArticleURL('javascript:alert(1)')).toBeUndefined()
    expect(safeArticleURL('data:text/html,x')).toBeUndefined()
    expect(safeArticleURL('https://user:secret@example.com/a')).toBeUndefined()
  })

  it('RSS 展示 Source、原站时间和原文链接', () => {
    expect(articleOriginLabel(rss)).toBe('示例 Feed')
    expect(articleOriginURL(rss)).toBe('https://example.com/a')
    expect(articleTime(rss)).toEqual({ label: '原站发布于', value: '2026-09-01T00:00:00Z' })
  })

  it('RSS 缺失原站时间时回退到站内发布时间', () => {
    expect(articleTime(rssWithoutSourceTime)).toEqual({ label: '发布于', value: '2026-09-02T00:00:00Z' })
  })

  it('站内投稿只展示作者和固定站内发布时间', () => {
    expect(articleOriginLabel(user)).toBe('小唯')
    expect(articleOriginURL(user)).toBeUndefined()
    expect(articleTime(user)).toEqual({ label: '发布于', value: '2026-09-02T00:00:00Z' })
  })
})
