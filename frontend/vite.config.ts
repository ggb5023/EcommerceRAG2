import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.')
  const target = env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080'
  return {
    plugins: [vue()],
    // The merged app is served from one origin, so every gateway prefix goes to
    // the same target; there is no second admin proxy variable any more.
    server: {
      proxy: {
        '/v1': target,
        '/admin/v1': target,
      },
    },
    test: {
      environment: 'node',
    },
  }
})
