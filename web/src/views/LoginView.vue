<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { login } from '../api/auth'
import { ApiError } from '../api/client'
import { appSession } from '../features/auth/browser'

const route = useRoute()
const router = useRouter()
const username = ref(typeof route.query.username === 'string' ? route.query.username : '')
const password = ref('')
const submitting = ref(false)
const errorMessage = ref('')

async function submit() {
  if (submitting.value) {
    return
  }
  submitting.value = true
  errorMessage.value = ''
  try {
    // 登录非幂等：每次提交都会建立新会话，失败后由用户决定是否重试。
    appSession.applyLogin(await login(username.value, password.value))
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/account'
    await router.replace(redirect)
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '登录失败，请稍后重试'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="auth-page">
    <header class="page-heading">
      <p class="eyebrow">账户</p>
      <h1>登录</h1>
    </header>
    <form class="auth-card" @submit.prevent="submit">
      <label class="field">
        <span>用户名</span>
        <input v-model="username" name="username" autocomplete="username" required />
      </label>
      <label class="field">
        <span>密码</span>
        <input v-model="password" type="password" name="password" autocomplete="current-password" required />
      </label>
      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
      <button type="submit" :disabled="submitting">{{ submitting ? '登录中…' : '登录' }}</button>
      <p class="field-hint">还没有账户？<RouterLink class="link" to="/register">去注册</RouterLink></p>
    </form>
  </section>
</template>
