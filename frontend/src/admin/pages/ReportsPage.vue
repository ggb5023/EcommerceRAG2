<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getHandoff, getUsage, type HandoffReport, type UsageReport } from '../api'
import ReportPanels from '../ReportPanels.vue'
type Range = '7d' | '30d'
const range = ref<Range>('30d')
const usage = ref<UsageReport | null>(null)
const handoff = ref<HandoffReport | null>(null)
const error = ref('')
const loading = ref(false)
async function load() {
  loading.value = true; error.value = ''
  try {
    const [usageResult, handoffResult] = await Promise.all([getUsage(range.value), getHandoff(range.value)])
    usage.value = usageResult; handoff.value = handoffResult
  } catch (caught) { error.value = caught instanceof Error ? caught.message : '报告数据加载失败。' }
  finally { loading.value = false }
}
onMounted(load)
</script>

<template>
  <section class="content-area reports-page" data-testid="reports-page">
    <div class="section-head"><div><h2>报告</h2><p>只读 token 用量与 customer 转人工计数；不展示货币金额。</p></div><label>窗口<select v-model="range" @change="load"><option value="7d">近 7 天</option><option value="30d">近 30 天</option></select></label></div>
    <p v-if="loading" role="status">正在加载报告…</p>
    <ReportPanels v-else :usage="usage" :handoff="handoff" :error="error" />
  </section>
</template>
