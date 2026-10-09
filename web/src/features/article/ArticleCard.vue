<script setup lang="ts">
import { RouterLink } from 'vue-router'

import type { ArticleItem } from '../../types/article'
import type { ArticleFeedbackState } from '../../types/feedback'
import { articleOriginLabel, articleOriginURL, articleSummary, articleTime, formatTime } from './model'
import FeedbackButtons from './FeedbackButtons.vue'

defineProps<{ item: ArticleItem; feedback?: ArticleFeedbackState; feedbackBusy?: boolean }>()
const emit = defineEmits<{ feedbackToggle: [id: number, kind: 'favorite' | 'notInterested'] }>()
</script>

<template>
  <article class="article-card">
    <p class="article-meta">
      <span>{{ item.origin.type === 'rss' ? 'RSS' : '站内作者' }} · {{ articleOriginLabel(item) }}</span>
    </p>
    <h2><RouterLink :to="`/articles/${item.id}`">{{ item.title }}</RouterLink></h2>
    <div v-if="item.enhancement" class="ai-enhancement">
      <span class="ai-badge">{{ item.enhancement.method === 'extractive' ? '原文摘录' : 'AI 生成' }}</span>
      <p class="article-excerpt">{{ articleSummary(item) }}</p>
      <div class="ai-labels" aria-label="AI 关键词与主题">
        <span v-for="keyword in item.enhancement.keywords" :key="`keyword-${keyword}`" class="ai-label keyword">关键词 · {{ keyword }}</span>
        <span v-for="topic in item.enhancement.topics" :key="`topic-${topic}`" class="ai-label topic">主题 · {{ topic }}</span>
      </div>
    </div>
    <p v-else-if="item.excerpt" class="article-excerpt">{{ articleSummary(item) }}</p>
    <p class="article-time">
      <span>{{ articleTime(item).label }} {{ formatTime(articleTime(item).value) }}</span>
      <a v-if="articleOriginURL(item)" class="origin-link" :href="articleOriginURL(item)" target="_blank" rel="noopener noreferrer">原文 ↗</a>
    </p>
    <FeedbackButtons :article-id="item.id" :state="feedback" :busy="feedbackBusy" @toggle="(id, kind) => emit('feedbackToggle', id, kind)" />
  </article>
</template>
