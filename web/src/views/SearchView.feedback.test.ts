// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { searchArticles } from '../api/client'
import { getArticleFeedback } from '../api/feedback'
import { appSession } from '../features/auth/browser'
import SearchView from './SearchView.vue'

vi.mock('../api/client', async () => ({ ...await vi.importActual('../api/client'), searchArticles: vi.fn() }))
vi.mock('../api/feedback', () => ({ getArticleFeedback: vi.fn(), recordArticleRead: vi.fn(), setArticleFavorite: vi.fn(), setArticleNotInterested: vi.fn() }))

afterEach(() => appSession.clear())

it('搜索结果复用文章反馈卡片并批量读取状态', async () => {
  vi.clearAllMocks()
  appSession.applyLogin({ access_token: 'token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } })
  vi.mocked(searchArticles).mockResolvedValue({ items: [
    { id: 5, title: '搜索文章 5', excerpt: '摘要', published_at: '2026-09-28T00:00:00Z', origin: { type: 'user', author: { id: 'a', nickname: '作者' } }, enhancement: null },
    { id: 6, title: '搜索文章 6', excerpt: '摘要', published_at: '2026-09-28T00:00:00Z', origin: { type: 'user', author: { id: 'a', nickname: '作者' } }, enhancement: null },
  ], next_cursor: null, has_more: false })
  vi.mocked(getArticleFeedback).mockResolvedValue({ items: [
    { article_id: 5, favorited: true, not_interested: false, not_interested_expires_at: null },
    { article_id: 6, favorited: false, not_interested: true, not_interested_expires_at: '2027-03-27T00:00:00Z' },
  ] })
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/search', component: SearchView },
    { path: '/articles/:id', component: { template: '<div />' } },
    { path: '/login', name: 'login', component: { template: '<div />' } },
  ] })
  await router.push('/search?q=go')
  await router.isReady()
  const wrapper = mount(SearchView, { global: { plugins: [router] } })
  await flushPromises()
  expect(wrapper.findAll('.feedback-actions')).toHaveLength(2)
  expect(getArticleFeedback).toHaveBeenCalledTimes(1)
  expect(vi.mocked(getArticleFeedback).mock.calls[0][0]).toEqual([5, 6])
  expect(wrapper.text()).toContain('取消收藏')
  expect(wrapper.text()).toContain('撤销不感兴趣')
  wrapper.unmount()
})
