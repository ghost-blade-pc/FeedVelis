export interface ArticleSource {
  id: number
  title: string
  site_url: string | null
}

export interface ArticleItem {
  id: number
  title: string
  canonical_url: string
  source: ArticleSource
  author_name: string | null
  excerpt: string
  source_published_at: string | null
  discovered_at: string
}

export interface ArticlePage {
  items: ArticleItem[]
  next_cursor: string | null
  has_more: boolean
}
