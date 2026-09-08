<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { listArticles } from '../../api/client'
import type { ArticleItem } from '../../types/article'
import { articleTime, formatTime, safeArticleURL } from './model'

const items = ref<ArticleItem[]>([])
const cursor = ref<string | null>(null)
const hasMore = ref(false)
const state = ref<'loading' | 'ready' | 'error'>('loading')
let request: AbortController | undefined

async function load(reset = false) {
  request?.abort()
  request = new AbortController()
  if (reset) {
    state.value = 'loading'
    items.value = []
    cursor.value = null
  }
  try {
    const page = await listArticles(reset ? undefined : (cursor.value ?? undefined), 20, request.signal)
    items.value = reset ? page.items : [...items.value, ...page.items]
    cursor.value = page.next_cursor
    hasMore.value = page.has_more
    state.value = 'ready'
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) state.value = 'error'
  }
}

onMounted(() => load(true))
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <section class="article-page" aria-labelledby="latest-title">
    <header class="page-heading">
      <p class="eyebrow">来自你登记的 Feed</p>
      <h1 id="latest-title">最新文章</h1>
    </header>

    <p v-if="state === 'loading'" class="state-card" role="status">正在加载文章…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>暂时无法加载文章。</p>
      <button type="button" @click="load(true)">重试</button>
    </div>
    <p v-else-if="items.length === 0" class="state-card">还没有文章。请先使用 velis-admin 添加并抓取一个 Feed。</p>

    <div v-else class="article-list">
      <article v-for="item in items" :key="item.id" class="article-card">
        <p class="article-meta">
          <span>{{ item.source.title }}</span>
          <span v-if="item.author_name"> · {{ item.author_name }}</span>
        </p>
        <h2>
          <RouterLink :to="`/articles/${item.id}`">{{ item.title }}</RouterLink>
        </h2>
        <p v-if="item.excerpt" class="article-excerpt">{{ item.excerpt }}</p>
        <p class="article-time">
          <span>{{ articleTime(item).label }} {{ formatTime(articleTime(item).value) }}</span>
          <a
            v-if="safeArticleURL(item.canonical_url)"
            class="origin-link"
            :href="safeArticleURL(item.canonical_url)"
            target="_blank"
            rel="noopener noreferrer"
          >原文 ↗</a>
        </p>
      </article>
      <button v-if="hasMore" class="load-more" type="button" @click="load(false)">加载更多</button>
    </div>
  </section>
</template>
