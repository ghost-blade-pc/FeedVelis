import { createRouter, createWebHistory } from 'vue-router'

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
  ],
})

export default router
