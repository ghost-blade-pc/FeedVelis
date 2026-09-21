<script setup lang="ts">
import { onMounted, ref } from 'vue'

import {
  changeAdminSourceState, createAdminSource, fetchAdminSource, listAdminSourceRuns, listAdminSources,
  newOperationKey, updateAdminSource,
} from '../api/content'
import { formatTime } from '../features/article/model'
import { fetchRunSummary, sourceErrorMessage, sourceStatusLabel } from '../features/source/model'
import type { AdminSource, SourceFetchRun } from '../types/source'

const sources = ref<AdminSource[]>([])
const runs = ref<Record<number, SourceFetchRun[]>>({})
const feedURL = ref('')
const intervalSeconds = ref(1800)
const state = ref<'loading' | 'ready' | 'error'>('loading')
const busyID = ref<number | 'create' | null>(null)
const errorMessage = ref('')

async function load() {
  state.value = 'loading'
  errorMessage.value = ''
  try {
    sources.value = (await listAdminSources()).items
    state.value = 'ready'
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
    state.value = 'error'
  }
}

async function create() {
  busyID.value = 'create'
  errorMessage.value = ''
  try {
    sources.value = [...sources.value, (await createAdminSource({
      feed_url: feedURL.value, fetch_interval_seconds: intervalSeconds.value,
    }, newOperationKey())).body]
    feedURL.value = ''
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
  } finally {
    busyID.value = null
  }
}

function replaceSource(updated: AdminSource) {
  sources.value = sources.value.map((item) => item.id === updated.id ? updated : item)
}

async function updateInterval(source: AdminSource, event: Event) {
  const value = Number((event.target as HTMLInputElement).value)
  busyID.value = source.id
  try {
    replaceSource((await updateAdminSource(source.id, source.lock_version, value, newOperationKey())).body)
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
  } finally {
    busyID.value = null
  }
}

async function toggle(source: AdminSource) {
  busyID.value = source.id
  try {
    const action = source.status === 'paused' ? 'resume' : 'pause'
    replaceSource((await changeAdminSourceState(source.id, action, source.lock_version, newOperationKey())).body)
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
  } finally {
    busyID.value = null
  }
}

async function fetchNow(source: AdminSource) {
  busyID.value = source.id
  try {
    const run = await fetchAdminSource(source.id, newOperationKey())
    runs.value[source.id] = [run, ...(runs.value[source.id] ?? [])]
    await load()
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
  } finally {
    busyID.value = null
  }
}

async function showHistory(source: AdminSource) {
  busyID.value = source.id
  try {
    runs.value[source.id] = (await listAdminSourceRuns(source.id)).items
  } catch (error) {
    errorMessage.value = sourceErrorMessage(error)
  } finally {
    busyID.value = null
  }
}

onMounted(load)
</script>

<template>
  <section class="source-page" aria-labelledby="sources-title">
    <header class="page-heading"><p class="eyebrow">管理员</p><h1 id="sources-title">内容来源</h1></header>
    <form class="source-create" @submit.prevent="create">
      <label class="field">Feed URL<input v-model="feedURL" type="url" required placeholder="https://example.com/feed.xml" /></label>
      <label class="field">抓取周期（秒）<input v-model.number="intervalSeconds" type="number" min="300" max="86400" required /></label>
      <button :disabled="busyID !== null">新增 Source</button>
    </form>
    <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
    <p v-if="state === 'loading'" class="state-card">正在加载 Source…</p>
    <div v-else-if="state === 'error'" class="state-card"><button @click="load">重试</button></div>
    <p v-else-if="sources.length === 0" class="state-card">尚未登记 Source。</p>
    <div v-else class="source-list">
      <article v-for="source in sources" :key="source.id" class="source-card">
        <header><div><p class="article-meta">{{ sourceStatusLabel(source) }} · 版本 {{ source.lock_version }}</p><h2>{{ source.title || source.feed_url }}</h2></div></header>
        <p class="source-url">{{ source.feed_url }}</p>
        <dl class="source-facts">
          <dt>下次抓取</dt><dd>{{ source.next_fetch_at ? formatTime(source.next_fetch_at) : '未安排' }}</dd>
          <dt>最近成功</dt><dd>{{ source.last_success_at ? formatTime(source.last_success_at) : '暂无' }}</dd>
          <dt>连续失败</dt><dd>{{ source.consecutive_failures }}</dd>
          <dt>最近错误</dt><dd>{{ source.last_error_code || '无' }}</dd>
        </dl>
        <div class="source-actions">
          <label class="field compact-field">周期（秒）<input :value="source.fetch_interval_seconds" type="number" min="300" max="86400" :disabled="busyID !== null" @change="updateInterval(source, $event)" /></label>
          <button class="secondary" :disabled="busyID !== null" @click="toggle(source)">{{ source.status === 'paused' ? '恢复' : '暂停' }}</button>
          <button :disabled="busyID !== null" @click="fetchNow(source)">立即抓取</button>
          <button class="secondary" :disabled="busyID !== null" @click="showHistory(source)">最近历史</button>
        </div>
        <ul v-if="runs[source.id]" class="run-list">
          <li v-for="run in runs[source.id]" :key="run.id"><time>{{ formatTime(run.started_at) }}</time> · {{ fetchRunSummary(run) }}</li>
          <li v-if="runs[source.id].length === 0">暂无抓取历史。</li>
        </ul>
      </article>
    </div>
  </section>
</template>
