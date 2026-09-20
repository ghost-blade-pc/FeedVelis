<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { registerAccount } from '../api/auth'
import { ApiError } from '../api/client'
import type { RegisterInput } from '../types/account'

const router = useRouter()
const username = ref('')
const nickname = ref('')
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
    const input: RegisterInput = { username: username.value, password: password.value }
    if (nickname.value.trim() !== '') {
      input.nickname = nickname.value
    }
    await registerAccount(input)
    // 规格：注册成功跳转登录页并预填用户名，不自动建立会话。
    await router.push({ name: 'login', query: { username: username.value } })
  } catch (error) {
    errorMessage.value = error instanceof ApiError ? error.message : '注册失败，请稍后重试'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <section class="auth-page">
    <header class="page-heading">
      <p class="eyebrow">账户</p>
      <h1>注册</h1>
    </header>
    <form class="auth-card" @submit.prevent="submit">
      <label class="field">
        <span>用户名</span>
        <input v-model="username" name="username" autocomplete="username" required />
      </label>
      <p class="field-hint">3～32 位半角字符，首位必须是英文字母，之后只能是字母、数字或下划线。</p>

      <label class="field">
        <span>昵称（可选）</span>
        <input v-model="nickname" name="nickname" autocomplete="nickname" />
      </label>
      <p class="field-hint">1～32 个字符，可中文；省略时使用用户名。</p>

      <label class="field">
        <span>密码</span>
        <input v-model="password" type="password" name="password" autocomplete="new-password" required />
      </label>
      <p class="field-hint">8～20 位半角可打印字符（不含空格），需包含大写字母、小写字母、数字、标点中的至少三类。</p>

      <p v-if="errorMessage" class="form-error" role="alert">{{ errorMessage }}</p>
      <button type="submit" :disabled="submitting">{{ submitting ? '提交中…' : '注册' }}</button>
      <p class="field-hint">已有账户？<RouterLink class="link" to="/login">去登录</RouterLink></p>
    </form>
  </section>
</template>
