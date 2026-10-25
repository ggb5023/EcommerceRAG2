<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { browserAuth, checkBrowserAuth, csrfHeaders } from './auth'

const router = useRouter()
const current = ref('')
const next = ref('')
const busy = ref(false)
const error = ref('')

onMounted(async () => { await checkBrowserAuth().catch(() => undefined) })

async function submit() {
  busy.value = true; error.value = ''
  try {
    const response = await fetch('/v1/auth/password', {
      method: 'POST', credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', ...csrfHeaders() },
      body: JSON.stringify({ current_password: current.value, new_password: next.value }),
    })
    if (response.status === 401) { error.value = '当前密码不正确，或会话已过期。'; return }
    if (response.status === 400) { error.value = '新密码需要 12 到 256 个字节。'; return }
    if (response.status === 403) { error.value = '请求被拒绝，请重新登录后再试。'; return }
    if (!response.ok) { error.value = '修改失败，请稍后再试。'; return }
    await router.replace('/login?changed=1')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <main class="password-shell">
    <form class="password-card" @submit.prevent="submit">
      <h1>修改密码</h1>
      <p class="hint">修改成功后所有会话都会失效，需要用新密码重新登录。</p>
      <label>当前密码<input v-model="current" type="password" autocomplete="current-password" required></label>
      <label>新密码<input v-model="next" type="password" autocomplete="new-password" required minlength="12" maxlength="256"></label>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button type="submit" :disabled="busy || !browserAuth.authenticated">{{ busy ? '提交中…' : '修改密码' }}</button>
    </form>
  </main>
</template>

<style scoped>
.password-shell{min-height:100vh;display:grid;place-items:center;background:#f6f8f6;padding:16px}
.password-card{width:min(420px,100%);display:grid;gap:12px;padding:28px;background:#fff;border:1px solid #e2e9e4;border-radius:10px}
h1{margin:0;font-size:20px;color:#17231f}label{display:grid;gap:6px;font-size:12px;color:#52635b}
input{padding:10px;border:1px solid #d8e2dc;border-radius:6px;font:inherit}
button{padding:10px 14px;border:0;border-radius:6px;background:#1b654d;color:#fff;font-weight:700}
button:disabled{opacity:.6}.error{color:#a3453e;margin:0}.hint{color:#52635b;margin:0;font-size:12px}
</style>
