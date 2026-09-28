// @vitest-environment jsdom
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import { createSSRApp } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { createMemoryHistory, createRouter } from 'vue-router'

import { appSession } from '../auth/browser'
import FeedbackButtons from './FeedbackButtons.vue'

async function render(favorited = false, negative = false) {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/articles/:id', component: { template: '<div />' } },
    { path: '/login', name: 'login', component: { template: '<div />' } },
  ] })
  await router.push('/articles/7')
  await router.isReady()
  const app = createSSRApp(FeedbackButtons, {
    articleId: 7, state: { article_id: 7, favorited, not_interested: negative, not_interested_expires_at: null },
  }).use(router)
  return renderToString(app)
}

describe('文章反馈按钮', () => {
  afterEach(() => appSession.clear())

  it('匿名仍显示可访问操作，登录态显示已确认状态及撤销文案', async () => {
    appSession.clear()
    const anonymous = await render()
    expect(anonymous).toContain('收藏')
    expect(anonymous).toContain('不感兴趣')
    expect(anonymous).toContain('aria-pressed="false"')
    appSession.applyLogin({ access_token: 'token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
      id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
    } })
    const confirmed = await render(true, true)
    expect(confirmed).toContain('取消收藏')
    expect(confirmed).toContain('撤销不感兴趣')
    expect(confirmed).toContain('aria-pressed="true"')
  })

  it('匿名点击跳转登录，登录用户操作按按钮状态发送并阻止忙碌时重复点击', async () => {
    appSession.clear()
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/articles/:id', component: { template: '<div />' } },
      { path: '/login', name: 'login', component: { template: '<div />' } },
    ] })
    await router.push('/articles/7')
    await router.isReady()
    const state = { article_id: 7, favorited: false, not_interested: false, not_interested_expires_at: null }
    const anonymous = mount(FeedbackButtons, { props: { articleId: 7, state }, global: { plugins: [router] } })
    await anonymous.findAll('button')[0].trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/articles/7')
    expect(anonymous.emitted('toggle')).toBeUndefined()
    anonymous.unmount()

    await router.push('/articles/7')
    appSession.applyLogin({ access_token: 'token', expires_at: new Date(Date.now() + 60_000).toISOString(), account: {
      id: 'u1', username: 'alice', nickname: 'Alice', role: 'user', status: 'active', created_at: '2026-09-21T00:00:00Z',
    } })
    const loggedIn = mount(FeedbackButtons, { props: { articleId: 7, state }, global: { plugins: [router] } })
    await loggedIn.findAll('button')[0].trigger('click')
    expect(loggedIn.emitted('toggle')).toEqual([[7, 'favorite']])
    await loggedIn.setProps({ busy: true })
    expect(loggedIn.findAll('button')[0].attributes('disabled')).toBeDefined()
    loggedIn.unmount()
  })

})
