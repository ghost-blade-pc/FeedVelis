<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ApiError, listArticles } from '../../api/client'
import { listRecommendedArticles } from '../../api/recommend'
import type { ArticleItem, RecommendedArticleItem, RecommendationReason } from '../../types/article'
import { appSession } from '../auth/browser'
import ArticleCard from './ArticleCard.vue'
import { useArticleFeedback } from './useArticleFeedback'

const items = ref<(ArticleItem | RecommendedArticleItem)[]>([])
const feedMode = ref<'latest' | 'recommend'>('latest')
const degraded = ref(false)
const cursorInvalid = ref(false)
const cursor = ref<string | null>(null)
const hasMore = ref(false)
const state = ref<'loading' | 'ready' | 'error'>('loading')
const feedback = useArticleFeedback(computed(() => items.value.map((item) => item.id)))
let request: AbortController | undefined
let generation = 0
let accountID = appSession.getSnapshot().account?.id ?? null
const unsubscribe = appSession.subscribe((snapshot) => {
  const nextID = snapshot.account?.id ?? null
  if (nextID !== accountID) {
    accountID = nextID
    void load(true)
  }
})

function switchMode(mode: 'latest' | 'recommend') {
  if (feedMode.value === mode) return
  feedMode.value = mode
  void load(true)
}

function reasonLabel(reason: RecommendationReason): string {
  switch (reason) {
    case 'keyword_match': return '与你关注的关键词相关'
    case 'topic_match': return '与你关注的主题相关'
    case 'similar_content': return '与你读过的内容相似'
    case 'recent': return '近期发布'
    case 'latest_fallback': return '按最新文章补充'
  }
}

function recommendationReason(item: ArticleItem | RecommendedArticleItem): RecommendationReason | null {
  return 'recommendation_reason' in item ? item.recommendation_reason : null
}

async function load(reset = false) {
  request?.abort()
  const controller = new AbortController()
  request = controller
  const currentGeneration = ++generation
  if (reset) {
    state.value = 'loading'
    items.value = []
    cursor.value = null
    hasMore.value = false
    degraded.value = false
    cursorInvalid.value = false
  }
  try {
    const page = feedMode.value === 'latest'
      ? await listArticles(reset ? undefined : (cursor.value ?? undefined), 20, controller.signal)
      : await listRecommendedArticles(reset ? undefined : (cursor.value ?? undefined), 20, controller.signal)
    if (currentGeneration !== generation) return
    items.value = reset ? page.items : [...items.value, ...page.items]
    cursor.value = page.next_cursor
    hasMore.value = page.has_more
    degraded.value = 'degraded' in page && page.degraded === true
    state.value = 'ready'
  } catch (error) {
    if (currentGeneration !== generation || error instanceof DOMException && error.name === 'AbortError') return
    cursorInvalid.value = error instanceof ApiError && error.code === 'INVALID_CURSOR'
    state.value = 'error'
  }
}

onMounted(() => load(true))
onBeforeUnmount(() => { request?.abort(); unsubscribe() })
</script>

<template>
  <section class="article-page" aria-labelledby="latest-title">
    <header class="page-heading">
      <p class="eyebrow">RSS 与站内作者的统一内容池</p>
      <h1 id="latest-title">{{ feedMode === 'latest' ? '最新文章' : '推荐文章' }}</h1>
    </header>

    <div class="feed-switch" aria-label="文章排序">
      <button type="button" :aria-pressed="feedMode === 'latest'" @click="switchMode('latest')">最新</button>
      <button type="button" :aria-pressed="feedMode === 'recommend'" @click="switchMode('recommend')">推荐</button>
    </div>
    <p v-if="feedMode === 'recommend' && degraded" class="state-card" role="status">推荐服务暂时降级，已显示可阅读的文章。</p>

    <p v-if="state === 'loading'" class="state-card" role="status">正在加载文章…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>{{ cursorInvalid ? '推荐页已过期，请从第一页重新加载。' : '暂时无法加载文章。' }}</p>
      <button type="button" @click="load(true)">{{ cursorInvalid ? '从第一页重试' : '重试' }}</button>
    </div>
    <div v-else-if="items.length === 0" class="state-card">
      <p>{{ hasMore ? '这一页暂无可显示的文章，可以继续加载。' : '还没有公开文章。' }}</p>
      <button v-if="hasMore" class="load-more" type="button" @click="load(false)">加载更多</button>
    </div>

    <div v-else class="article-list">
      <p v-if="feedback.error.value" role="alert">{{ feedback.error.value }} <button type="button" @click="feedback.load(true)">重试反馈状态</button></p>
      <div v-for="item in items" :key="item.id">
        <p v-if="feedMode === 'recommend' && recommendationReason(item)" class="recommend-reason">{{ reasonLabel(recommendationReason(item)!) }}</p>
        <ArticleCard :item="item" :feedback="feedback.states.value[item.id]" :feedback-busy="feedback.busy.value[item.id] || Boolean(feedback.error.value)" @feedback-toggle="feedback.toggle" />
      </div>
      <button v-if="hasMore" class="load-more" type="button" @click="load(false)">加载更多</button>
    </div>
  </section>
</template>
