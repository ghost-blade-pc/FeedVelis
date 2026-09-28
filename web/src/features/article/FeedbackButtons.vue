<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'

import { useSession } from '../auth/useSession'
import type { ArticleFeedbackState } from '../../types/feedback'

const props = defineProps<{ articleId: number; state?: ArticleFeedbackState; busy?: boolean }>()
const emit = defineEmits<{ toggle: [id: number, kind: 'favorite' | 'notInterested'] }>()
const { session } = useSession()
const route = useRoute()
const router = useRouter()

function act(kind: 'favorite' | 'notInterested') {
  if (!session.value.account) {
    void router.push({ name: 'login', query: { redirect: route.fullPath } })
    return
  }
  if (props.state && !props.busy) emit('toggle', props.articleId, kind)
}
</script>

<template>
  <div class="feedback-actions" :aria-label="`文章 ${articleId} 反馈`">
    <button type="button" :aria-pressed="state?.favorited ?? false" :disabled="Boolean(session.account) && (!state || busy)" @click="act('favorite')">
      {{ state?.favorited ? '取消收藏' : '收藏' }}
    </button>
    <button type="button" :aria-pressed="state?.not_interested ?? false" :disabled="Boolean(session.account) && (!state || busy)" @click="act('notInterested')">
      {{ state?.not_interested ? '撤销不感兴趣' : '不感兴趣' }}
    </button>
  </div>
</template>
