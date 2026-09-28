import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'

import { appSession } from '../auth/browser'
import type { ArticleFeedbackState } from '../../types/feedback'
import { useArticleFeedback, type FeedbackTransport } from './useArticleFeedback'

function login(id: string) {
  appSession.applyLogin({ access_token: `token-${id}`, expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id, username: id, nickname: id, role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } })
}
function state(id: number, favorited = false): ArticleFeedbackState {
  return { article_id: id, favorited, not_interested: false, not_interested_expires_at: null }
}

describe('文章反馈状态', () => {
  afterEach(() => { appSession.clear(); vi.restoreAllMocks() })

  it('匿名不请求；登录和分页按 50 篇批量读取，切换账户清空旧状态', async () => {
    appSession.clear()
    const ids = ref(Array.from({ length: 50 }, (_, index) => index + 1))
    const get = vi.fn(async (batch: number[]) => ({ items: batch.map((id) => state(id)) }))
    const transport: FeedbackTransport = { get, favorite: vi.fn(), notInterested: vi.fn() }
    const scope = effectScope()
    const feedback = scope.run(() => useArticleFeedback(ids, transport))!
    await nextTick()
    expect(get).not.toHaveBeenCalled()
    login('A')
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(Object.keys(feedback.states.value)).toHaveLength(50))
    ids.value = Array.from({ length: 60 }, (_, index) => index + 1)
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2))
    expect(get.mock.calls[1][0]).toEqual(Array.from({ length: 10 }, (_, index) => index + 51))
    login('B')
    expect(Object.keys(feedback.states.value)).toHaveLength(0)
    await vi.waitFor(() => expect(Object.keys(feedback.states.value)).toHaveLength(60))
    expect(get.mock.calls.slice(2).map(([batch]) => batch.length)).toEqual([50, 10])
    scope.stop()
  })

  it('写入失败保留已确认状态，重复点击串行，成功后读取服务端状态', async () => {
    login('A')
    const ids = ref([1])
    let saved = false
    let resolveWrite: (() => void) | undefined
    const favorite = vi.fn(() => new Promise<void>((resolve) => { resolveWrite = () => { saved = true; resolve() } }))
    const get = vi.fn(async () => ({ items: [state(1, saved)] }))
    const scope = effectScope()
    const feedback = scope.run(() => useArticleFeedback(ids, { get, favorite, notInterested: vi.fn() }))!
    await vi.waitFor(() => expect(feedback.states.value[1]).toBeDefined())
    const write = feedback.toggle(1, 'favorite')
    await feedback.toggle(1, 'favorite')
    expect(favorite).toHaveBeenCalledTimes(1)
    resolveWrite?.()
    await write
    expect(feedback.states.value[1].favorited).toBe(true)
    scope.stop()

    const failed = effectScope()
    const failing = failed.run(() => useArticleFeedback(ids, { get, favorite: vi.fn().mockRejectedValue(new Error('失败')), notInterested: vi.fn() }))!
    await vi.waitFor(() => expect(failing.states.value[1]).toBeDefined())
    await failing.toggle(1, 'favorite')
    expect(failing.states.value[1].favorited).toBe(true)
    expect(failing.error.value).toContain('失败')
    failed.stop()
  })
})
