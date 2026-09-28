import { onScopeDispose, ref, watch, type Ref } from 'vue'

import { getArticleFeedback, setArticleFavorite, setArticleNotInterested } from '../../api/feedback'
import { useSession } from '../auth/useSession'
import type { ArticleFeedbackState } from '../../types/feedback'

export interface FeedbackTransport {
  get(ids: number[], signal?: AbortSignal): Promise<{ items: ArticleFeedbackState[] }>
  favorite(id: number, enabled: boolean): Promise<void>
  notInterested(id: number, enabled: boolean): Promise<void>
}

const defaultTransport: FeedbackTransport = {
  get: getArticleFeedback, favorite: setArticleFavorite, notInterested: setArticleNotInterested,
}

/** 仅在当前账户的内存中保存已确认状态；分页按最多 50 篇批量读取。 */
export function useArticleFeedback(ids: Ref<number[]>, transport: FeedbackTransport = defaultTransport) {
  const { session } = useSession()
  const states = ref<Record<number, ArticleFeedbackState>>({})
  const busy = ref<Record<number, boolean>>({})
  const error = ref('')
  let generation = 0
  let abort: AbortController | undefined

  async function load(force = false) {
    const accountID = session.value.account?.id
    if (!accountID) return
    abort?.abort()
    abort = new AbortController()
    const current = ++generation
    const missing = [...new Set(ids.value)].filter((id) => force || !states.value[id])
    try {
      for (let index = 0; index < missing.length; index += 50) {
        const page = await transport.get(missing.slice(index, index + 50), abort.signal)
        if (current !== generation || session.value.account?.id !== accountID) return
        for (const item of page.items) states.value[item.article_id] = item
      }
      error.value = ''
    } catch (cause) {
      if (current === generation && !(cause instanceof DOMException && cause.name === 'AbortError')) {
        error.value = '反馈状态暂不可用，请稍后重试'
      }
    }
  }

  watch(() => session.value.account?.id, () => {
    generation++
    abort?.abort()
    states.value = {}
    busy.value = {}
    error.value = ''
    void load()
  }, { flush: 'sync' })
  watch(ids, () => { void load() }, { immediate: true })

  async function toggle(id: number, kind: 'favorite' | 'notInterested') {
    const accountID = session.value.account?.id
    const current = states.value[id]
    if (!accountID || !current || busy.value[id]) return
    busy.value[id] = true
    error.value = ''
    try {
      const enabled = kind === 'favorite' ? !current.favorited : !current.not_interested
      if (kind === 'favorite') await transport.favorite(id, enabled)
      else await transport.notInterested(id, enabled)
      if (session.value.account?.id !== accountID) return
      const page = await transport.get([id])
      if (session.value.account?.id === accountID) {
        const confirmed = page.items.find((item) => item.article_id === id)
        if (confirmed) states.value[id] = confirmed
      }
    } catch {
      if (session.value.account?.id === accountID) error.value = '反馈操作失败，状态未更改，请重试'
    } finally {
      if (session.value.account?.id === accountID) busy.value[id] = false
    }
  }

  onScopeDispose(() => { generation++; abort?.abort() })
  return { states, busy, error, toggle, load }
}
