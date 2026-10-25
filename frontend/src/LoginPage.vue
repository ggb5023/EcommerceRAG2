<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { browserAuth, checkBrowserAuth } from './auth'

const route = useRoute()
const router = useRouter()
const username = ref('')
const password = ref('')
const busy = ref(false)
const error = ref('')
const localMode = ref(false)

const destination = (): string => {
  if (route.query.app === 'admin' && typeof route.query.return_to !== 'string') return '/admin'
  const raw = typeof route.query.return_to === 'string' ? route.query.return_to : '/chat'
  if (['/chat', '/support', '/admin', '/shop'].some(prefix => raw === prefix || raw.startsWith(prefix + '/') || raw.startsWith(prefix + '?'))) return raw
  return route.query.app === 'admin' ? '/admin' : '/chat'
}

onMounted(async () => {
  await checkBrowserAuth().catch(() => undefined)
  localMode.value = browserAuth.mode === 'local'
})

async function submit() {
  busy.value = true; error.value = ''
  try {
    const response = await fetch('/v1/auth/login', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    if (response.status === 401) { error.value = '账号或密码不正确。'; return }
    if (response.status === 423) { error.value = '尝试次数过多，账号已临时锁定，请稍后再试。'; return }
    if (response.status === 429) { error.value = '请求过于频繁，请稍后再试。'; return }
    if (response.status === 403) { error.value = '该账号已停用，请联系店主。'; return }
    if (!response.ok) { error.value = '登录暂不可用，请稍后再试。'; return }
    const state = await response.json() as { must_change_password?: boolean }
    await checkBrowserAuth()
    await router.replace(state.must_change_password || browserAuth.mustChangePassword ? '/password' : destination())
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <form class="login-card" @submit.prevent="submit">
      <h1>登录客服知识引擎</h1>
      <p v-if="!localMode" class="notice">当前部署未启用内置账号，请通过身份服务登录。</p>
      <template v-else>
        <label>账号<input v-model="username" autocomplete="username" required></label>
        <label>密码<input v-model="password" type="password" autocomplete="current-password" required></label>
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <button type="submit" :disabled="busy">{{ busy ? '登录中…' : '登录' }}</button>
      </template>
    </form>
  </main>
</template>

<style scoped>
.login-shell{min-height:100vh;display:grid;place-items:center;background:#f6f8f6;padding:16px}
.login-card{width:min(420px,100%);display:grid;gap:12px;padding:28px;background:#fff;border:1px solid #e2e9e4;border-radius:10px}
h1{margin:0;font-size:20px;color:#17231f}label{display:grid;gap:6px;font-size:12px;color:#52635b}
input{padding:10px;border:1px solid #d8e2dc;border-radius:6px;font:inherit}
button{padding:10px 14px;border:0;border-radius:6px;background:#1b654d;color:#fff;font-weight:700}
button:disabled{opacity:.6}.error{color:#a3453e;margin:0}.notice{color:#795b19;margin:0}
</style>
