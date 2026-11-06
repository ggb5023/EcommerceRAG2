<script setup lang="ts">
import { onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { shouldLandInAdmin } from './auth'
import SupportDesk from './SupportDesk.vue'

const route = useRoute()
const router = useRouter()

// Admin-role accounts belong to the management console; the staff workspace is
// for CS staff. ?stay=1 keeps it for an admin who deliberately wants it.
onMounted(async () => {
  if (route.query.tab === 'assist') {
    const conversation = typeof route.query.conversation === 'string' ? route.query.conversation : ''
    await router.replace({ path: '/assistant', query: conversation ? { conversation } : {} })
    return
  }
  if (route.query.tab === 'queue') await router.replace({ path: '/staff', query: {} })
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
    <SupportDesk />
  </div>
</template>

<style scoped>
.staff-workspace{min-height:100vh;background:var(--color-bg)}
</style>
