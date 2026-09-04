import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/livez': 'http://localhost:8080',
      '/readyz': 'http://localhost:8080',
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
  },
})
