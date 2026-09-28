export interface ArticleFeedbackState {
  article_id: number
  favorited: boolean
  not_interested: boolean
  not_interested_expires_at: string | null
}

export interface ArticleFeedbackPage {
  items: ArticleFeedbackState[]
}
