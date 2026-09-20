import { readonly, ref, type DeepReadonly, type Ref } from 'vue'

import { appSession } from './browser'
import type { SessionSnapshot } from './session'

const snapshot = ref<SessionSnapshot>(appSession.getSnapshot())
appSession.subscribe((next) => {
  snapshot.value = next
})

/** 组件读取的会话状态；令牌只在内存中，不写入任何持久存储。 */
export function useSession(): { session: DeepReadonly<Ref<SessionSnapshot>>; manager: typeof appSession } {
  return { session: readonly(snapshot), manager: appSession }
}
