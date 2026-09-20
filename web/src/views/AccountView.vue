<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { fetchCurrentAccount, updateNickname } from '../api/auth'
import { ApiError } from '../api/client'
import { useSession } from '../features/auth/useSession'

const router = useRouter()
const { session, manager } = useSession()
const state = ref<'loading' | 'ready' | 'error'>('loading')
const nickname = ref('')
const saving = ref(false)
const notice = ref('')
const errorMessage = ref('')

onMounted(async () => {
  try {
    const account = await manager.withAccessToken((token) => fetchCurrentAccount(token))
    nickname.value = account.nickname
    state.value = 'ready'
  } catch {
    state.value = 'error'
  }
})

async function save() {
  if (saving.value) {
    return
  }
  saving.value = true
  notice.value = ''
  errorMessage.value = ''
  try {
    const updated = await manager.withAccessToken((token) => updateNickname(token, nickname.value))
    nickname.value = updated.nickname
    notice.value = '昵称已更新'
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '保存失败，请稍后重试'
  } finally {
    saving.value = false
  }
}

async function signOut() {
  await manager.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <section class="auth-page">
    <header class="page-heading">
      <p class="eyebrow">账户</p>
      <h1>我的账户</h1>
    </header>

    <p v-if="state === 'loading'" class="state-card" role="status">正在读取账户…</p>
    <div v-else-if="state === 'error'" class="state-card" role="alert">
      <p>暂时无法读取账户信息。</p>
      <button type="button" @click="router.go(0)">重试</button>
    </div>

    <template v-else>
      <div class="auth-card">
        <dl class="account-facts">
          <dt>用户名</dt>
          <dd>{{ session.account?.username }}</dd>
          <dt>角色</dt>
          <dd>{{ session.account?.role === 'admin' ? '管理员' : '普通用户' }}</dd>
          <dt>状态</dt>
          <dd>{{ session.account?.status === 'active' ? '正常' : '已禁用' }}</dd>
          <dt>注册时间</dt>
          <dd>{{ session.account ? new Date(session.account.created_at).toLocaleString() : '' }}</dd>
        </dl>
      </div>

      <form class="auth-card" @submit.prevent="save">
        <label class="field">
          <span>昵称</span>
          <input v-model="nickname" name="nickname" maxlength="32" required />
        </label>
        <p class="field-hint">1～32 个字符，可中文，允许重名。</p>
        <p v-if="notice" class="form-notice" role="status">{{ notice }}</p>
        <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
        <button type="submit" :disabled="saving">{{ saving ? '保存中…' : '保存昵称' }}</button>
      </form>

      <div class="auth-card">
        <button type="button" class="secondary" @click="signOut">退出登录</button>
      </div>
    </template>
  </section>
</template>
