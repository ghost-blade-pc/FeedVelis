// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { ApiError, listArticles } from '../../api/client'
import { listRecommendedArticles } from '../../api/recommend'
import { getArticleFeedback, setArticleFavorite } from '../../api/feedback'
import { appSession } from '../auth/browser'
import type { ArticleItem } from '../../types/article'
import ArticleList from './ArticleList.vue'

vi.mock('../../api/client', async () => ({ ...await vi.importActual('../../api/client'), listArticles: vi.fn() }))
vi.mock('../../api/recommend', () => ({ listRecommendedArticles: vi.fn() }))
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
    vi.mocked(listRecommendedArticles).mockResolvedValue({ items: articles(31, 1).map((item) => ({ ...item, recommendation_reason: 'recent' })), next_cursor: null, has_more: false, mode: 'cold_start', degraded: false })
    vi.mocked(getArticleFeedback).mockImplementation(async (ids) => ({ items: ids.map((id) => ({ article_id: id, favorited: false, not_interested: false, not_interested_expires_at: null })) }))
  })
  afterEach(() => appSession.clear())

  it('匿名卡片不读状态，登录后按页批量读取且分页只读新增文章', async () => {
    vi.mocked(listArticles)
      .mockResolvedValueOnce({ items: articles(1, 20), next_cursor: 'next', has_more: true })
      .mockResolvedValueOnce({ items: articles(1, 20), next_cursor: 'next', has_more: true })
      .mockResolvedValueOnce({ items: articles(21, 20), next_cursor: null, has_more: false })
    const wrapper = await mountList()
    expect(wrapper.findAll('.feedback-actions')).toHaveLength(20)
    expect(getArticleFeedback).not.toHaveBeenCalled()
    login('A')
    await flushPromises()
    expect(getArticleFeedback).toHaveBeenCalledTimes(2)
    expect(vi.mocked(getArticleFeedback).mock.calls[1][0]).toEqual(Array.from({ length: 20 }, (_, index) => index + 1))
    await wrapper.find('.load-more').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.feedback-actions')).toHaveLength(40)
    expect(vi.mocked(getArticleFeedback).mock.calls[2][0]).toEqual(Array.from({ length: 20 }, (_, index) => index + 21))
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
    expect(wrapper.findAll('.feedback-actions button')).toHaveLength(0)
    await flushPromises()
    expect(wrapper.find('.feedback-actions button').text()).toBe('收藏')
    expect(wrapper.find('.feedback-actions button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('推荐切换、翻页、降级和空内容状态', async () => {
    vi.mocked(listRecommendedArticles)
      .mockResolvedValueOnce({ items: articles(31, 1).map((item) => ({ ...item, recommendation_reason: 'keyword_match' })), next_cursor: 'next', has_more: true, mode: 'personalized', degraded: false })
      .mockResolvedValueOnce({ items: articles(32, 1).map((item) => ({ ...item, recommendation_reason: 'latest_fallback' })), next_cursor: null, has_more: false, mode: 'personalized', degraded: true })
      .mockResolvedValueOnce({ items: [], next_cursor: null, has_more: false, mode: 'cold_start', degraded: false })
    const wrapper = await mountList()
    await wrapper.findAll('.feed-switch button')[1].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('与你关注的关键词相关')
    await wrapper.find('.load-more').trigger('click')
    await flushPromises()
    expect(vi.mocked(listRecommendedArticles).mock.calls[1][0]).toBe('next')
    expect(wrapper.text()).toContain('推荐服务暂时降级')
    expect(wrapper.text()).toContain('按最新文章补充')
    await wrapper.findAll('.feed-switch button')[0].trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('按最新文章补充')
    await wrapper.findAll('.feed-switch button')[1].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('还没有公开文章')
    wrapper.unmount()
  })

  it('账户变化取消旧请求，游标失效可从第一页恢复，请求失败与空页区分', async () => {
    let finishOld!: (value: Awaited<ReturnType<typeof listRecommendedArticles>>) => void
    vi.mocked(listRecommendedArticles)
      .mockImplementationOnce(() => new Promise((resolve) => { finishOld = resolve }))
      .mockResolvedValueOnce({ items: articles(41, 1).map((item) => ({ ...item, recommendation_reason: 'recent' })), next_cursor: 'next', has_more: true, mode: 'cold_start', degraded: false })
      .mockRejectedValueOnce(new ApiError('游标过期', 400, 'INVALID_CURSOR'))
      .mockResolvedValueOnce({ items: [], next_cursor: null, has_more: false, mode: 'cold_start', degraded: false })
    const wrapper = await mountList()
    await wrapper.findAll('.feed-switch button')[1].trigger('click')
    login('B')
    await flushPromises()
    finishOld({ items: articles(99, 1).map((item) => ({ ...item, recommendation_reason: 'recent' })), next_cursor: null, has_more: false, mode: 'cold_start', degraded: false })
    await flushPromises()
    expect(wrapper.text()).toContain('文章 41')
    expect(wrapper.text()).not.toContain('文章 99')
    await wrapper.find('.load-more').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('从第一页重新加载')
    await wrapper.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('还没有公开文章')
    wrapper.unmount()
  })

  it('推荐请求失败显示错误且可重试', async () => {
    vi.mocked(listRecommendedArticles)
      .mockRejectedValueOnce(new Error('网络失败'))
      .mockResolvedValueOnce({ items: articles(55, 1).map((item) => ({ ...item, recommendation_reason: 'recent' })), next_cursor: null, has_more: false, mode: 'cold_start', degraded: false })
    const wrapper = await mountList()
    await wrapper.findAll('.feed-switch button')[1].trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('暂时无法加载文章')
    await wrapper.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('文章 55')
    wrapper.unmount()
  })
})
