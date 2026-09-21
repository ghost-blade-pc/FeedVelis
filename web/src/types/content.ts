export type ArticleStatus = 'draft' | 'published' | 'offline' | 'deleted'

export interface MyArticleSummary {
  id: number
  title: string
  status: ArticleStatus
  offline_reason: 'author' | 'admin' | null
  revision_no: number
  lock_version: number
  published_at: string | null
  updated_at: string
}

export interface MyArticleDetail extends MyArticleSummary {
  markdown: string
  content_html: string
  excerpt: string
  asset_ids: string[]
}

export interface MyArticlePage {
  items: MyArticleSummary[]
  next_cursor: string | null
  has_more: boolean
}

export interface ArticlePreview {
  content_html: string
  plain_text: string
  excerpt: string
  asset_ids: string[]
}

export interface ArticleAsset {
  id: string
  status: 'pending' | 'ready' | 'delete_pending' | 'deleted'
  content_type: string | null
  size_bytes: number | null
  width: number | null
  height: number | null
  checksum: string | null
  created_at: string
  confirmed_at: string | null
}

export interface AssetUpload {
  asset: ArticleAsset
  upload_url: string
  upload_method: string
  upload_headers: Record<string, string>
  expires_at: string
}
