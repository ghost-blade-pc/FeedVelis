// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { listArticles } from '../../api/client'
import { getArticleFeedback, setArticleFavorite } from '../../api/feedback'
import { appSession } from '../auth/browser'
import type { ArticleItem } from '../../types/article'
import ArticleList from './ArticleList.vue'

vi.mock('../../api/client', async () => ({ ...await vi.importActual('../../api/client'), listArticles: vi.fn() }))
vi.mock('../../api/feedback', () => ({ getArticleFeedback: vi.fn(), recordArticleRead: vi.fn(), setArticleFavorite: vi.fn(), setArticleNotInterested: vi.fn() }))

function articles(start: number, count: number): ArticleItem[] {
  return Array.from({ length: count }, (_, index) => ({
    id: start + index, title: `文章 ${start + index}`, excerpt: '摘要', published_at: '2026-09-28T00:00:00Z',
    origin: { type: 'user' as const, author: { id: 'author', nickname: '作者' } }, enhancement: null,
  }))
}

function login(id: string) {
  appSession.applyLogin({ access_token: `token-${id}`, expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id, username: id, nickname: id, role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } })
}

async function mountList() {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/latest', component: ArticleList },
    { path: '/articles/:id', component: { template: '<div />' } },
    { path: '/login', name: 'login', component: { template: '<div />' } },
  ] })
  await router.push('/latest')
  await router.isReady()
  const wrapper = mount(ArticleList, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('latest 文章反馈卡片', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    appSession.clear()
    vi.mocked(listArticles).mockResolvedValue({ items: articles(1, 20), next_cursor: null, has_more: false })
    vi.mocked(getArticleFeedback).mockImplementation(async (ids) => ({ items: ids.map((id) => ({ article_id: id, favorited: false, not_interested: false, not_interested_expires_at: null })) }))
  })
  afterEach(() => appSession.clear())

  it('匿名卡片不读状态，登录后按页批量读取且分页只读新增文章', async () => {
    vi.mocked(listArticles)
      .mockResolvedValueOnce({ items: articles(1, 20), next_cursor: 'next', has_more: true })
      .mockResolvedValueOnce({ items: articles(21, 20), next_cursor: null, has_more: false })
    const wrapper = await mountList()
    expect(wrapper.findAll('.feedback-actions')).toHaveLength(20)
    expect(getArticleFeedback).not.toHaveBeenCalled()
    login('A')
    await flushPromises()
    expect(getArticleFeedback).toHaveBeenCalledTimes(1)
    expect(vi.mocked(getArticleFeedback).mock.calls[0][0]).toEqual(Array.from({ length: 20 }, (_, index) => index + 1))
    await wrapper.find('.load-more').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.feedback-actions')).toHaveLength(40)
    expect(vi.mocked(getArticleFeedback).mock.calls[1][0]).toEqual(Array.from({ length: 20 }, (_, index) => index + 21))
    wrapper.unmount()
  })

  it('写失败维持服务端状态，账户切换立即清空旧状态', async () => {
    login('A')
    vi.mocked(getArticleFeedback).mockImplementation(async (ids) => ({ items: ids.map((id) => ({ article_id: id, favorited: true, not_interested: false, not_interested_expires_at: null })) }))
    vi.mocked(setArticleFavorite).mockRejectedValue(new Error('网络失败'))
    const wrapper = await mountList()
    expect(wrapper.find('.feedback-actions button').text()).toContain('取消收藏')
    await wrapper.find('.feedback-actions button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('反馈操作失败')
    expect(wrapper.find('.feedback-actions button').text()).toContain('取消收藏')
    vi.mocked(getArticleFeedback).mockImplementation(() => new Promise(() => {}))
    login('B')
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.feedback-actions button').text()).toBe('收藏')
    expect(wrapper.find('.feedback-actions button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})
