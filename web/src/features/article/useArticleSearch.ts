import { ref } from 'vue'

import { ApiError, searchArticles } from '../../api/client'
import type { ArticleItem, ArticleSearchPage, ArticleSearchQuery } from '../../types/article'

export type SearchState = 'idle' | 'loading' | 'loading-more' | 'ready' | 'empty' | 'unavailable' | 'error'
export type SearchRequest = (query: ArticleSearchQuery, signal?: AbortSignal) => Promise<ArticleSearchPage>

export function useArticleSearch(requestPage: SearchRequest = searchArticles) {
  const items = ref<ArticleItem[]>([])
  const cursor = ref<string | null>(null)
  const hasMore = ref(false)
  const state = ref<SearchState>('idle')
  let query: ArticleSearchQuery | undefined
  let controller: AbortController | undefined
  let generation = 0
  let failedLoadMore = false

  async function start(nextQuery: ArticleSearchQuery, loadMore = false) {
    query = nextQuery
    failedLoadMore = loadMore
    const current = ++generation
    controller?.abort()
    controller = new AbortController()
    state.value = loadMore ? 'loading-more' : 'loading'
    if (!loadMore) {
      items.value = []
      cursor.value = null
      hasMore.value = false
    }
    try {
      const page = await requestPage({ ...nextQuery, cursor: loadMore ? (cursor.value ?? undefined) : undefined }, controller.signal)
      if (current !== generation) return
      items.value = loadMore ? [...items.value, ...page.items] : page.items
      cursor.value = page.next_cursor
      hasMore.value = page.has_more
      state.value = items.value.length === 0 ? 'empty' : 'ready'
    } catch (error) {
      if (current !== generation || (error instanceof Error && error.name === 'AbortError')) return
      state.value = error instanceof ApiError && error.code === 'SEARCH_UNAVAILABLE' ? 'unavailable' : 'error'
    }
  }

  function cancelAndClear() {
    generation++
    controller?.abort()
    query = undefined
    items.value = []
    cursor.value = null
    hasMore.value = false
    state.value = 'idle'
  }

  async function loadMore() {
    if (query && hasMore.value && state.value !== 'loading-more') await start(query, true)
  }

  async function retry() {
    if (query) await start(query, failedLoadMore && items.value.length > 0)
  }

  return { items, cursor, hasMore, state, start, loadMore, retry, cancelAndClear }
}
