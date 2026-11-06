<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { shouldLandInAdmin } from './auth'
import App from './App.vue'
import SupportDesk from './SupportDesk.vue'

const route = useRoute()
const router = useRouter()
const tab = computed(() => (route.query.tab === 'assist' ? 'assist' : 'queue'))

function go(next: 'queue' | 'assist') {
  void router.replace({ path: '/staff', query: { tab: next } })
}

// Admin-role accounts belong to the management console; the staff workspace is
// for CS staff. ?stay=1 keeps it for an admin who deliberately wants it.
onMounted(async () => {
  try {
    const response = await fetch('/admin/v1/session', { credentials: 'same-origin' })
    if (!response.ok) return
    const state = await response.json() as { admin_roles?: string[] }
    if (shouldLandInAdmin(state.admin_roles, route.query.stay === '1')) await router.replace('/admin/overview')
  } catch { /* the workspace still renders and surfaces its own errors */ }
})
</script>

<template>
  <div class="staff-workspace">
    <nav class="staff-tabs" aria-label="员工工作台">
      <button type="button" :class="{ active: tab === 'queue' }" @click="go('queue')">人工队列</button>
      <button type="button" :class="{ active: tab === 'assist' }" @click="go('assist')">AI 辅助</button>
    </nav>
    <SupportDesk v-if="tab === 'queue'" />
    <App v-else />
  </div>
</template>

<style scoped>
.staff-workspace{min-height:100vh;background:var(--color-bg)}
.staff-tabs{display:flex;gap:4px;padding:8px 16px;background:var(--color-surface);border-bottom:1px solid var(--color-border)}
.staff-tabs button{border:1px solid transparent;background:transparent;padding:8px 16px;border-radius:6px;font:inherit;color:var(--color-text-muted);cursor:pointer}
.staff-tabs button.active{background:var(--color-surface-raised);border-color:var(--color-border);color:var(--color-text);font-weight:600}
</style>
