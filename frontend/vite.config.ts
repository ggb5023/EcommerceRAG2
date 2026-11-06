import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.')
  const target = env.VITE_API_PROXY_TARGET ?? 'http://127.0.0.1:8080'
  // The selfhost deployment mounts Keycloak under /identity on the same origin
  // (docker/selfhost/compose.yml). Mirror that in the dev server so OIDC works
  // through the single frontend origin instead of a second exposed port.
  const identity = env.VITE_IDENTITY_PROXY_TARGET ?? 'http://127.0.0.1:26080'
  return {
    plugins: [vue()],
    // The merged app is served from one origin, so every gateway prefix goes to
    // the same target; there is no second admin proxy variable any more.
    server: {
      proxy: {
        '/v1': target,
        '/admin/v1': target,
        '/setup': target,
        // Keycloak signs tokens with the request host, so keep the original
        // Host header (changeOrigin stays false) for the /identity proxy.
        '/identity': { target: identity, changeOrigin: false },
      },
    },
    test: {
      environment: 'node',
    },
  }
})
