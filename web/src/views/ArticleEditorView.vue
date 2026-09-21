<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'

import {
  changeMyArticleState, confirmAsset, createAsset, createMyArticle, deleteMyArticle, getMyArticle,
  newOperationKey, previewMyArticle, updateMyArticle, uploadAssetObject,
} from '../api/content'
import { contentErrorMessage, insertAssetReference, isArticleVersionConflict } from '../features/content/editor'
import type { ArticlePreview, AssetUpload, MyArticleDetail } from '../types/content'

const route = useRoute()
const router = useRouter()
const articleID = computed(() => Number(route.params.id || 0))
const isNew = computed(() => !articleID.value)
const title = ref('')
const markdown = ref('')
const article = ref<MyArticleDetail | null>(null)
const saved = ref({ title: '', markdown: '' })
const preview = ref<ArticlePreview | null>(null)
const loading = ref(!isNew.value)
const busy = ref(false)
const errorMessage = ref('')
const notice = ref('')
const conflict = ref(false)
const uploadProgress = ref<number | null>(null)
const markdownInput = ref<HTMLTextAreaElement | null>(null)
let pendingUpload: { file: File; upload: AssetUpload; confirmKey: string } | null = null

const dirty = computed(() => title.value !== saved.value.title || markdown.value !== saved.value.markdown)

function accept(detail: MyArticleDetail) {
  article.value = detail
  title.value = detail.title
  markdown.value = detail.markdown
  saved.value = { title: detail.title, markdown: detail.markdown }
  conflict.value = false
}

async function load() {
  if (isNew.value) return
  loading.value = true
  errorMessage.value = ''
  try {
    accept((await getMyArticle(articleID.value)).body)
  } catch (error) {
    errorMessage.value = contentErrorMessage(error)
  } finally {
    loading.value = false
  }
}

async function create(initialStatus: 'draft' | 'published') {
  busy.value = true
  errorMessage.value = ''
  notice.value = ''
  const key = newOperationKey()
  try {
    const result = await createMyArticle({ title: title.value, markdown: markdown.value, initial_status: initialStatus }, key)
    accept(result.body)
    notice.value = initialStatus === 'published' ? '文章已直接发布。' : '草稿已保存。'
    await router.replace(`/me/articles/${result.body.id}/edit`)
  } catch (error) {
    errorMessage.value = contentErrorMessage(error)
  } finally {
    busy.value = false
  }
}

async function save() {
  if (!article.value) return
  busy.value = true
  errorMessage.value = ''
  notice.value = ''
  const localMarkdown = markdown.value
  try {
    const result = await updateMyArticle(article.value.id, article.value.lock_version, { title: title.value, markdown: localMarkdown }, newOperationKey())
    accept(result.body)
    notice.value = result.body.status === 'published' ? '修改已保存并立即公开。' : '修改已保存。'
  } catch (error) {
    if (isArticleVersionConflict(error)) {
      conflict.value = true
      markdown.value = localMarkdown
      errorMessage.value = '远端版本已经变化。本地 Markdown 已保留；请先复制，再主动刷新远端版本。'
    } else {
      errorMessage.value = contentErrorMessage(error)
    }
  } finally {
    busy.value = false
  }
}

async function changeState(action: 'publish' | 'offline') {
  if (!article.value) return
  busy.value = true
  errorMessage.value = ''
  try {
    accept((await changeMyArticleState(article.value.id, action, article.value.lock_version, newOperationKey())).body)
    notice.value = action === 'publish' ? '文章已公开。' : '文章已下架。'
  } catch (error) {
    if (isArticleVersionConflict(error)) conflict.value = true
    errorMessage.value = contentErrorMessage(error)
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!article.value || !window.confirm('删除后不可恢复，确定继续吗？')) return
  busy.value = true
  try {
    await deleteMyArticle(article.value.id, article.value.lock_version, newOperationKey())
    saved.value = { title: title.value, markdown: markdown.value }
    await router.replace('/me/articles')
  } catch (error) {
    errorMessage.value = contentErrorMessage(error)
  } finally {
    busy.value = false
  }
}

async function renderPreview() {
  busy.value = true
  errorMessage.value = ''
  try {
    preview.value = await previewMyArticle({ title: title.value, markdown: markdown.value })
  } catch (error) {
    errorMessage.value = contentErrorMessage(error)
  } finally {
    busy.value = false
  }
}

async function copyMarkdown() {
  await navigator.clipboard.writeText(markdown.value)
  notice.value = '本地 Markdown 已复制。'
}

async function chooseImage(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file) return
  busy.value = true
  uploadProgress.value = 0
  errorMessage.value = ''
  try {
    const upload = await createAsset(file, newOperationKey())
    pendingUpload = { file, upload, confirmKey: newOperationKey() }
    await finishUpload()
  } catch (error) {
    errorMessage.value = contentErrorMessage(error)
    busy.value = false
  }
}

async function finishUpload() {
  if (!pendingUpload) return
  busy.value = true
  try {
    await uploadAssetObject(pendingUpload.upload, pendingUpload.file, (value) => { uploadProgress.value = value })
    const asset = await confirmAsset(pendingUpload.upload.asset.id, pendingUpload.confirmKey)
    const input = markdownInput.value
    const start = input?.selectionStart ?? markdown.value.length
    const end = input?.selectionEnd ?? start
    const result = insertAssetReference(markdown.value, asset.id, start, end)
    markdown.value = result.value
    pendingUpload = null
    uploadProgress.value = null
    notice.value = '图片已确认并插入 Markdown。'
    await nextTick()
    input?.focus()
    input?.setSelectionRange(result.cursor, result.cursor)
  } catch (error) {
    errorMessage.value = `${contentErrorMessage(error)} 可使用“重试图片上传”继续同一次操作。`
  } finally {
    busy.value = false
  }
}

function beforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) event.preventDefault()
}

onBeforeRouteLeave(() => dirty.value && !window.confirm('有未保存的修改，确定离开吗？') ? false : true)
onMounted(() => { window.addEventListener('beforeunload', beforeUnload); void load() })
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>

<template>
  <section class="editor-page" aria-labelledby="editor-title">
    <header class="page-heading"><p class="eyebrow">创作中心</p><h1 id="editor-title">{{ isNew ? '新建文章' : '编辑文章' }}</h1></header>
    <p v-if="loading" class="state-card">正在加载文章…</p>
    <form v-else class="editor-card" @submit.prevent="isNew ? create('draft') : save()">
      <p v-if="article?.status === 'published'" class="warning">这篇文章已公开，保存修改会立即对匿名读者生效。</p>
      <p v-if="article?.offline_reason === 'admin'" class="warning">文章由管理员下架；可继续保存内容，但不能自行恢复公开。</p>
      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
      <p v-if="notice" class="form-notice" role="status">{{ notice }}</p>
      <div v-if="conflict" class="conflict-actions">
        <button type="button" class="secondary" @click="copyMarkdown">复制本地 Markdown</button>
        <button type="button" class="secondary" @click="load">主动刷新远端版本</button>
      </div>
      <label class="field">标题<input v-model="title" maxlength="200" /></label>
      <label class="field">Markdown<textarea ref="markdownInput" v-model="markdown" rows="18" maxlength="262144"></textarea></label>
      <div class="upload-row">
        <label class="button-link secondary-link">选择图片<input class="visually-hidden" type="file" accept="image/jpeg,image/png,image/webp" @change="chooseImage" /></label>
        <span v-if="uploadProgress !== null">上传 {{ uploadProgress }}%</span>
        <button v-if="pendingUpload" type="button" class="secondary" :disabled="busy" @click="finishUpload">重试图片上传</button>
      </div>
      <p class="field-hint">图片只支持 JPEG、PNG、WebP。I2 保留原始 EXIF 等元数据；上传前请自行确认其中不含位置等隐私。</p>
      <div class="editor-actions">
        <button type="submit" :disabled="busy">{{ isNew ? '保存草稿' : '手动保存' }}</button>
        <button v-if="isNew" type="button" :disabled="busy" @click="create('published')">直接发布</button>
        <button type="button" class="secondary" :disabled="busy" @click="renderPreview">服务端预览</button>
        <button v-if="article?.status === 'draft' || article?.offline_reason === 'author'" type="button" :disabled="busy" @click="changeState('publish')">发布</button>
        <button v-if="article?.status === 'published'" type="button" class="secondary" :disabled="busy" @click="changeState('offline')">下架</button>
        <button v-if="article" type="button" class="danger" :disabled="busy" @click="remove">删除</button>
      </div>
      <section v-if="preview" class="preview-card" aria-label="服务端预览">
        <h2>服务端预览</h2><div class="article-content" v-html="preview.content_html"></div>
      </section>
    </form>
  </section>
</template>
