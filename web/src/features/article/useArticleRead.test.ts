import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

import { appSession } from '../auth/browser'
import { useArticleRead } from './useArticleRead'

function login() {
  appSession.applyLogin({ access_token: 'token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } })
}

describe('详情阅读上报', () => {
  afterEach(() => appSession.clear())

  it('匿名和正文加载失败不请求，成功展示后只上报一次', async () => {
    appSession.clear()
    const id = ref<number | null>(7)
    const ready = ref(false)
    const report = vi.fn().mockResolvedValue(undefined)
    const scope = effectScope()
    scope.run(() => useArticleRead(id, ready, report))
    ready.value = true
    await nextTick()
    expect(report).not.toHaveBeenCalled()
    ready.value = false
    login()
    await nextTick()
    expect(report).not.toHaveBeenCalled()
    ready.value = true
    await nextTick()
    expect(report).toHaveBeenCalledExactlyOnceWith(7)
    ready.value = false
    ready.value = true
    await nextTick()
    expect(report).toHaveBeenCalledTimes(1)
    scope.stop()
  })

  it('上报失败只提示，不改变正文展示条件', async () => {
    login()
    const id = ref<number | null>(7)
    const ready = ref(true)
    const report = vi.fn().mockRejectedValue(new Error('网络失败'))
    const scope = effectScope()
    const read = scope.run(() => useArticleRead(id, ready, report))!
    id.value = 8
    await vi.waitFor(() => expect(read.error.value).toContain('阅读反馈未能保存'))
    expect(ready.value).toBe(true)
    scope.stop()
  })
})
