<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { getArticle } from '../api/client'
import { articleOriginLabel, articleOriginURL, articleTime, formatTime } from '../features/article/model'
import FeedbackButtons from '../features/article/FeedbackButtons.vue'
import { useArticleFeedback } from '../features/article/useArticleFeedback'
import { useArticleRead } from '../features/article/useArticleRead'
import type { ArticleDetail } from '../types/article'

const route = useRoute()
const detail = ref<ArticleDetail | null>(null)
const state = ref<'loading' | 'ready' | 'error'>('loading')
const feedback = useArticleFeedback(computed(() => detail.value ? [detail.value.id] : []))
const read = useArticleRead(computed(() => detail.value?.id ?? null), computed(() => state.value === 'ready'))
let request: AbortController | undefined

async function load() {
  request?.abort()
  request = new AbortController()
  state.value = 'loading'
  detail.value = null
  try {
    const articleId = Number(route.params.id)
    if (!Number.isInteger(articleId) || articleId <= 0) throw new Error('bad id')
    detail.value = await getArticle(articleId, request.signal)
    state.value = 'ready'
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) state.value = 'error'
  }
}

watch(() => route.params.id, () => { void load() })

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
        <span>{{ detail.origin.type === 'rss' ? 'RSS' : '站内作者' }} · {{ articleOriginLabel(detail) }}</span>
      </p>
      <h1 id="detail-title">{{ detail.title }}</h1>
      <p class="article-time">
        <span>{{ articleTime(detail).label }} {{ formatTime(articleTime(detail).value) }}</span>
        <a
          v-if="articleOriginURL(detail)"
          class="origin-link"
          :href="articleOriginURL(detail)"
          target="_blank"
          rel="noopener noreferrer"
        >查看原文 ↗</a>
      </p>
      <FeedbackButtons :article-id="detail.id" :state="feedback.states.value[detail.id]" :busy="feedback.busy.value[detail.id] || Boolean(feedback.error.value)" @toggle="feedback.toggle" />
      <p v-if="feedback.error.value" role="alert">{{ feedback.error.value }} <button type="button" @click="feedback.load(true)">重试反馈状态</button></p>
      <p v-if="read.error.value" role="status">{{ read.error.value }}</p>
      <aside v-if="detail.enhancement" class="ai-enhancement detail-enhancement" aria-label="内容摘要">
        <span class="ai-badge">{{ detail.enhancement.method === 'extractive' ? '原文摘录' : 'AI 生成' }}</span>
        <p class="article-excerpt">{{ detail.enhancement.summary }}</p>
        <div class="ai-labels">
          <span v-for="keyword in detail.enhancement.keywords" :key="`keyword-${keyword}`" class="ai-label keyword">关键词 · {{ keyword }}</span>
          <span v-for="topic in detail.enhancement.topics" :key="`topic-${topic}`" class="ai-label topic">主题 · {{ topic }}</span>
        </div>
      </aside>
      <!-- 后端已用白名单清洗 content_html（含图片），v-html 可直接渲染 -->
      <div v-if="detail.content_html" class="article-content" v-html="detail.content_html"></div>
      <p v-else class="article-content-fallback">{{ detail.excerpt }}</p>
    </article>
  </section>
</template>
