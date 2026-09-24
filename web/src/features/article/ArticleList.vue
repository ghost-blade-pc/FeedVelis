<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { listArticles } from '../../api/client'
import type { ArticleItem } from '../../types/article'
import { articleOriginLabel, articleOriginURL, articleSummary, articleTime, formatTime } from './model'

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
      <p class="eyebrow">RSS 与站内作者的统一内容池</p>
      <h1 id="latest-title">最新文章</h1>
    </header>

    <p v-if="state === 'loading'" class="state-card" role="status">正在加载文章…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>暂时无法加载文章。</p>
      <button type="button" @click="load(true)">重试</button>
    </div>
    <p v-else-if="items.length === 0" class="state-card">还没有公开文章。</p>

    <div v-else class="article-list">
      <article v-for="item in items" :key="item.id" class="article-card">
        <p class="article-meta">
          <span>{{ item.origin.type === 'rss' ? 'RSS' : '站内作者' }} · {{ articleOriginLabel(item) }}</span>
        </p>
        <h2>
          <RouterLink :to="`/articles/${item.id}`">{{ item.title }}</RouterLink>
        </h2>
        <div v-if="item.enhancement" class="ai-enhancement">
          <span class="ai-badge">AI 生成</span>
          <p class="article-excerpt">{{ articleSummary(item) }}</p>
          <div class="ai-labels" aria-label="AI 关键词与主题">
            <span v-for="keyword in item.enhancement.keywords" :key="`keyword-${keyword}`" class="ai-label keyword">关键词 · {{ keyword }}</span>
            <span v-for="topic in item.enhancement.topics" :key="`topic-${topic}`" class="ai-label topic">主题 · {{ topic }}</span>
          </div>
        </div>
        <p v-else-if="item.excerpt" class="article-excerpt">{{ articleSummary(item) }}</p>
        <p class="article-time">
          <span>{{ articleTime(item).label }} {{ formatTime(articleTime(item).value) }}</span>
          <a
            v-if="articleOriginURL(item)"
            class="origin-link"
            :href="articleOriginURL(item)"
            target="_blank"
            rel="noopener noreferrer"
          >原文 ↗</a>
        </p>
      </article>
      <button v-if="hasMore" class="load-more" type="button" @click="load(false)">加载更多</button>
    </div>
  </section>
</template>
