// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'

import { getArticle } from '../api/client'
import { getArticleFeedback, recordArticleRead, setArticleFavorite } from '../api/feedback'
import { appSession } from '../features/auth/browser'
import type { ArticleDetail } from '../types/article'
import ArticleView from './ArticleView.vue'

vi.mock('../api/client', async () => ({ ...await vi.importActual('../api/client'), getArticle: vi.fn() }))
vi.mock('../api/feedback', () => ({ getArticleFeedback: vi.fn(), recordArticleRead: vi.fn(), setArticleFavorite: vi.fn(), setArticleNotInterested: vi.fn() }))

const article: ArticleDetail = {
  id: 7, title: '正文标题', excerpt: '正文', content_html: '<p>已公开正文</p>', published_at: '2026-09-28T00:00:00Z',
  origin: { type: 'user', author: { id: 'author', nickname: '作者' } }, enhancement: null,
}

function login() {
  appSession.applyLogin({ access_token: 'token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
    id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
  } })
}

async function mountView() {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/articles/:id', component: ArticleView },
    { path: '/latest', component: { template: '<div />' } },
    { path: '/login', name: 'login', component: { template: '<div />' } },
  ] })
  await router.push('/articles/7')
  await router.isReady()
  const wrapper = mount(ArticleView, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('文章详情反馈', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    appSession.clear()
    vi.mocked(getArticle).mockResolvedValue(article)
    vi.mocked(getArticleFeedback).mockResolvedValue({ items: [{ article_id: 7, favorited: false, not_interested: false, not_interested_expires_at: null }] })
    vi.mocked(recordArticleRead).mockResolvedValue()
  })
  afterEach(() => appSession.clear())

  it('匿名展示正文且不调用本人反馈或阅读上报', async () => {
    const wrapper = await mountView()
    expect(wrapper.text()).toContain('已公开正文')
    expect(getArticleFeedback).not.toHaveBeenCalled()
    expect(recordArticleRead).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('摘录降级显示真实来源，不标记为 AI 生成', async () => {
    vi.mocked(getArticle).mockResolvedValue({ ...article, enhancement: {
      summary: '来自原文的摘录', keywords: ['正文标题'], topics: ['正文标题'], method: 'extractive', generated_at: '2026-09-28T00:01:00Z',
    } })
    const wrapper = await mountView()
    expect(wrapper.find('.ai-badge').text()).toBe('原文摘录')
    expect(wrapper.text()).toContain('来自原文的摘录')
    expect(wrapper.text()).not.toContain('AI 生成')
    wrapper.unmount()
  })

  it('阅读上报失败不阻塞正文，刷新后重新读取状态', async () => {
    login()
    vi.mocked(recordArticleRead).mockRejectedValue(new Error('网络失败'))
    const wrapper = await mountView()
    expect(wrapper.text()).toContain('已公开正文')
    expect(wrapper.text()).toContain('阅读反馈未能保存')
    expect(recordArticleRead).toHaveBeenCalledExactlyOnceWith(7)
    expect(getArticleFeedback).toHaveBeenCalledWith([7], expect.any(AbortSignal))
    wrapper.unmount()
    const refreshed = await mountView()
    expect(getArticleFeedback).toHaveBeenCalledTimes(2)
    refreshed.unmount()
  })

  it('连续点击等待写入确认，成功后从服务端更新按钮', async () => {
    login()
    let resolveWrite: (() => void) | undefined
    vi.mocked(setArticleFavorite).mockImplementation(() => new Promise<void>((resolve) => { resolveWrite = resolve }))
    vi.mocked(getArticleFeedback)
      .mockResolvedValueOnce({ items: [{ article_id: 7, favorited: false, not_interested: false, not_interested_expires_at: null }] })
      .mockResolvedValueOnce({ items: [{ article_id: 7, favorited: true, not_interested: false, not_interested_expires_at: null }] })
    const wrapper = await mountView()
    const favorite = wrapper.find('.feedback-actions button')
    await favorite.trigger('click')
    await favorite.trigger('click')
    expect(setArticleFavorite).toHaveBeenCalledTimes(1)
    resolveWrite?.()
    await flushPromises()
    expect(wrapper.text()).toContain('取消收藏')
    wrapper.unmount()
  })
})
