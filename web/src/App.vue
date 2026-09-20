<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'

import { useSession } from './features/auth/useSession'

const { session } = useSession()
</script>

<template>
  <header class="app-header">
    <RouterLink class="brand" to="/">Velis</RouterLink>
    <nav aria-label="主导航">
      <RouterLink to="/">For You</RouterLink>
      <RouterLink to="/latest">最新</RouterLink>
      <RouterLink to="/following">关注</RouterLink>
      <RouterLink to="/hot">热门</RouterLink>
      <RouterLink v-if="session.account" to="/account">{{ session.account.nickname }}</RouterLink>
      <template v-else>
        <RouterLink to="/login">登录</RouterLink>
        <RouterLink to="/register">注册</RouterLink>
      </template>
    </nav>
  </header>
  <main>
    <RouterView />
  </main>
</template>
