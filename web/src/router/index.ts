import { createRouter, createWebHistory } from 'vue-router'

import { appSession } from '../features/auth/browser'
import HomeView from '../views/HomeView.vue'
import LatestView from '../views/LatestView.vue'
import ArticleView from '../views/ArticleView.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', name: 'for-you', component: HomeView, props: { title: 'For You' } },
    { path: '/latest', name: 'latest', component: LatestView },
    { path: '/articles/:id', name: 'article-detail', component: ArticleView },
    { path: '/following', name: 'following', component: HomeView, props: { title: '关注内容' } },
    { path: '/hot', name: 'hot', component: HomeView, props: { title: '热门内容' } },
    { path: '/register', name: 'register', component: () => import('../views/RegisterView.vue') },
    { path: '/login', name: 'login', component: () => import('../views/LoginView.vue') },
    {
      path: '/account',
      name: 'account',
      component: () => import('../views/AccountView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/me/articles',
      name: 'my-articles',
      component: () => import('../views/MyArticlesView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/me/articles/new',
      name: 'new-article',
      component: () => import('../views/ArticleEditorView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/me/articles/:id/edit',
      name: 'edit-article',
      component: () => import('../views/ArticleEditorView.vue'),
      meta: { requiresAuth: true },
    },
    {
      path: '/admin/sources',
      name: 'admin-sources',
      component: () => import('../views/AdminSourcesView.vue'),
      meta: { requiresAuth: true, requiresAdmin: true },
    },
  ],
})

// 受保护页面先尝试用刷新 Cookie 恢复会话，恢复失败再引导登录。
router.beforeEach(async (to) => {
  if (!to.meta.requiresAuth) {
    return true
  }
  const snapshot = await appSession.restore()
  if (snapshot.accessToken && (!to.meta.requiresAdmin || snapshot.account?.role === 'admin')) {
    return true
  }
  if (snapshot.accessToken && to.meta.requiresAdmin) return { name: 'latest' }
  return { name: 'login', query: { redirect: to.fullPath } }
})

export default router
