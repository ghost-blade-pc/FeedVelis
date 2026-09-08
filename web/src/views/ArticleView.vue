<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { getArticle } from '../api/client'
import { articleTime, formatTime, safeArticleURL } from '../features/article/model'
import type { ArticleDetail } from '../types/article'

const route = useRoute()
const detail = ref<ArticleDetail | null>(null)
const state = ref<'loading' | 'ready' | 'error'>('loading')
let request: AbortController | undefined

async function load() {
  request?.abort()
  request = new AbortController()
  state.value = 'loading'
  try {
    const articleId = Number(route.params.id)
    if (!Number.isInteger(articleId) || articleId <= 0) throw new Error('bad id')
    detail.value = await getArticle(articleId, request.signal)
    state.value = 'ready'
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) state.value = 'error'
  }
}

onMounted(load)
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <section class="article-detail" aria-labelledby="detail-title">
    <p class="eyebrow"><RouterLink to="/latest">← 最新文章</RouterLink></p>

    <p v-if="state === 'loading'" class="state-card" role="status">正在加载文章…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>暂时无法加载这篇文章。</p>
      <button type="button" @click="load">重试</button>
    </div>
    <article v-else-if="detail" class="article-card detail-card">
      <p class="article-meta">
        <span>{{ detail.source.title }}</span>
        <span v-if="detail.author_name"> · {{ detail.author_name }}</span>
      </p>
      <h1 id="detail-title">{{ detail.title }}</h1>
      <p class="article-time">
        <span>{{ articleTime(detail).label }} {{ formatTime(articleTime(detail).value) }}</span>
        <a
          v-if="safeArticleURL(detail.canonical_url)"
          class="origin-link"
          :href="safeArticleURL(detail.canonical_url)"
          target="_blank"
          rel="noopener noreferrer"
        >查看原文 ↗</a>
      </p>
      <!-- 后端已用白名单清洗 content_html（含图片），v-html 可直接渲染 -->
      <div v-if="detail.content_html" class="article-content" v-html="detail.content_html"></div>
      <p v-else class="article-content-fallback">{{ detail.excerpt }}</p>
    </article>
  </section>
</template>
