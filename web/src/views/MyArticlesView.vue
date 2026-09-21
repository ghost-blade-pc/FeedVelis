<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { listMyArticles } from '../api/content'
import { formatTime } from '../features/article/model'
import type { MyArticleSummary } from '../types/content'

const items = ref<MyArticleSummary[]>([])
const state = ref<'loading' | 'ready' | 'error'>('loading')

async function load() {
  state.value = 'loading'
  try {
    items.value = (await listMyArticles()).items
    state.value = 'ready'
  } catch {
    state.value = 'error'
  }
}

onMounted(load)
</script>

<template>
  <section class="article-page" aria-labelledby="my-articles-title">
    <header class="page-heading heading-actions">
      <div><p class="eyebrow">创作中心</p><h1 id="my-articles-title">我的文章</h1></div>
      <RouterLink class="button-link" to="/me/articles/new">写文章</RouterLink>
    </header>
    <p v-if="state === 'loading'" class="state-card" role="status">正在加载文章…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert"><p>无法加载本人文章。</p><button @click="load">重试</button></div>
    <p v-else-if="items.length === 0" class="state-card">还没有文章，可以先保存一篇草稿。</p>
    <div v-else class="article-list">
      <article v-for="item in items" :key="item.id" class="article-card">
        <p class="article-meta">{{ item.status }} · 修订 {{ item.revision_no }} · 版本 {{ item.lock_version }}</p>
        <h2><RouterLink :to="`/me/articles/${item.id}/edit`">{{ item.title || '无标题草稿' }}</RouterLink></h2>
        <p class="article-time">更新于 {{ formatTime(item.updated_at) }}</p>
      </article>
    </div>
  </section>
</template>
