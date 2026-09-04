<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
</script>

<template>
  <header class="app-header">
    <RouterLink class="brand" to="/">Velis</RouterLink>
    <nav aria-label="主导航">
      <RouterLink to="/">For You</RouterLink>
      <RouterLink to="/latest">最新</RouterLink>
      <RouterLink to="/following">关注</RouterLink>
      <RouterLink to="/hot">热门</RouterLink>
    </nav>
  </header>
  <main>
    <RouterView />
  </main>
</template>
