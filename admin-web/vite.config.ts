import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.')
  return {
    plugins: [vue()],
    server: {
      proxy: {
        '/v1/auth': env.VITE_ADMIN_API_PROXY_TARGET ?? 'http://127.0.0.1:8080',
        '/admin/v1': env.VITE_ADMIN_API_PROXY_TARGET ?? 'http://127.0.0.1:8082',
      },
    },
    test: {
      environment: 'node',
    },
  }
})
