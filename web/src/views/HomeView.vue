<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { ping } from '../api/client'

defineProps<{ title: string }>()

const apiStatus = ref<'checking' | 'ready' | 'unavailable'>('checking')

onMounted(async () => {
  try {
    await ping()
    apiStatus.value = 'ready'
  } catch {
    apiStatus.value = 'unavailable'
  }
})
</script>

<template>
  <section class="welcome-card">
    <p class="eyebrow">个人内容聚合与智能阅读平台</p>
    <h1>{{ title }}</h1>
    <p>项目框架已就绪，业务功能将按照开发文档逐步接入。</p>
    <p class="status" :data-status="apiStatus">
      API：{{ apiStatus === 'ready' ? '可用' : apiStatus === 'checking' ? '检查中' : '暂不可用' }}
    </p>
  </section>
</template>
