export interface ArticleSource {
  id: number
  title: string
  site_url: string | null
}

export interface ArticleAuthor {
  id: string
  nickname: string
}

export interface RSSArticleOrigin {
  type: 'rss'
  source: ArticleSource
  canonical_url: string
  source_published_at: string | null
}

export interface UserArticleOrigin {
  type: 'user'
  author: ArticleAuthor
}

export type ArticleOrigin = RSSArticleOrigin | UserArticleOrigin

export interface ArticleEnhancement {
  summary: string
  keywords: string[]
  topics: string[]
  generated_at: string
}

export interface ArticleItem {
  id: number
  title: string
  excerpt: string
  published_at: string
  origin: ArticleOrigin
  enhancement: ArticleEnhancement | null
}

export interface ArticlePage {
  items: ArticleItem[]
  next_cursor: string | null
  has_more: boolean
}

export type ArticleSearchPage = ArticlePage

export interface ArticleSearchQuery {
  q: string
  keyword?: string
  topic?: string
  source_id?: number
  limit?: number
  cursor?: string
}

export interface ArticleDetail extends ArticleItem {
  content_html: string
}
