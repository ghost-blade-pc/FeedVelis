import { ref, watch, type Ref } from 'vue'

import { recordArticleRead } from '../../api/feedback'
import { useSession } from '../auth/useSession'

/** 仅在正文成功展示且有登录身份时异步上报；错误不会改变正文状态。 */
export function useArticleRead(articleID: Ref<number | null>, ready: Ref<boolean>, report = recordArticleRead) {
  const { session } = useSession()
  const error = ref('')
  let lastKey = ''
  watch([articleID, ready, () => session.value.account?.id], ([id, displayed, userID]) => {
    if (!id || !userID) { lastKey = ''; error.value = ''; return }
    if (!displayed) return
    const key = `${userID}:${id}`
    if (lastKey === key) return
    lastKey = key
    error.value = ''
    void report(id).catch(() => { if (lastKey === key) error.value = '阅读反馈未能保存，正文仍可正常阅读' })
  })
  return { error }
}
