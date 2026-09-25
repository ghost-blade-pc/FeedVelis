<script setup lang="ts">
import { onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ArticleCard from '../features/article/ArticleCard.vue'
import { normalizeSearchForm, searchRouteQuery } from '../features/article/search'
import { useArticleSearch } from '../features/article/useArticleSearch'
import type { ArticleSearchQuery } from '../types/article'

const route = useRoute()
const router = useRouter()
const form = reactive({
  q: typeof route.query.q === 'string' ? route.query.q : '',
  keyword: typeof route.query.keyword === 'string' ? route.query.keyword : '',
  topic: typeof route.query.topic === 'string' ? route.query.topic : '',
  sourceID: typeof route.query.source_id === 'string' ? route.query.source_id : '',
})
const { items, hasMore, state, start, loadMore, retry, cancelAndClear } = useArticleSearch()
const validationMessage = ref('')
let submitted: ArticleSearchQuery | undefined
let watching = false

watch(() => [form.q, form.keyword, form.topic, form.sourceID], () => {
  if (!watching || !submitted) return
  submitted = undefined
  cancelAndClear()
})

async function submit() {
  validationMessage.value = ''
  try {
    submitted = normalizeSearchForm(form)
  } catch (error) {
    cancelAndClear()
    validationMessage.value = error instanceof Error ? error.message : '搜索条件无效'
    return
  }
  await router.replace({ path: '/search', query: searchRouteQuery(submitted) })
  await start(submitted, false)
}

onMounted(async () => {
  watching = true
  if (form.q.trim()) await submit()
})
onBeforeUnmount(cancelAndClear)
</script>

<template>
  <section class="article-page search-page" aria-labelledby="search-title">
    <header class="page-heading">
      <p class="eyebrow">公开文章文本检索</p>
      <h1 id="search-title">搜索</h1>
    </header>
    <form class="search-form" role="search" @submit.prevent="submit">
      <label class="field search-query">搜索词<input v-model="form.q" name="q" maxlength="200" required autocomplete="off"></label>
      <div class="search-filters">
        <label class="field">关键词<input v-model="form.keyword" name="keyword" maxlength="64"></label>
        <label class="field">主题<input v-model="form.topic" name="topic" maxlength="64"></label>
        <label class="field">来源 ID<input v-model="form.sourceID" name="source_id" inputmode="numeric" pattern="[1-9][0-9]*"></label>
      </div>
      <p v-if="validationMessage" class="form-error" role="alert">{{ validationMessage }}</p>
      <button type="submit" :disabled="state === 'loading'">搜索文章</button>
    </form>

    <p v-if="state === 'idle'" class="state-card">输入搜索词，可按精确关键词、主题或来源筛选。</p>
    <p v-else-if="state === 'loading'" class="state-card" role="status">正在搜索…</p>
    <div v-else-if="state === 'unavailable'" class="state-card search-unavailable" role="alert">
      <p>搜索暂不可用，最新文章与正文阅读仍可使用。</p><button type="button" @click="retry">重试</button>
    </div>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>搜索请求失败，请稍后重试。</p><button type="button" @click="retry">重试</button>
    </div>
    <p v-else-if="state === 'empty'" class="state-card">没有匹配的公开文章，请调整搜索条件。</p>

    <div v-if="items.length" class="article-list" aria-live="polite">
      <ArticleCard v-for="item in items" :key="item.id" :item="item" />
      <button v-if="hasMore" class="load-more" type="button" :disabled="state === 'loading-more'" @click="loadMore">
        {{ state === 'loading-more' ? '正在加载…' : '加载更多' }}
      </button>
    </div>
  </section>
</template>
