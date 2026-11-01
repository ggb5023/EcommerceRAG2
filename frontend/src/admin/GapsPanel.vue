<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { exportGaps, getGaps, type KnowledgeGapReport } from './api'
import GapsView from './GapsView.vue'

type GapsRange = '7d' | '30d'
const range = ref<GapsRange>('30d')
const report = ref<KnowledgeGapReport | null>(null)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')

async function load() {
  loading.value = true
  error.value = ''
  notice.value = ''
  try {
    report.value = await getGaps(range.value)
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '内容缺口加载失败。'
  } finally {
    loading.value = false
  }
}
async function exportNow() {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const result = await exportGaps(range.value)
    notice.value = `已导出 ${result.count} 条到 ${result.path}`
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '导出失败。'
  } finally {
    busy.value = false
  }
}
onMounted(load)
</script>

<template>
  <section class="content-area gaps-panel">
    <div class="section-head">
      <div><h2>内容缺口</h2><p>窗口内未解决且没有后续澄清的问题，按归一化问句聚合。</p></div>
      <label>窗口<select v-model="range" @change="load"><option value="7d">近 7 天</option><option value="30d">近 30 天</option></select></label>
    </div>
    <section v-if="loading" class="state-panel" role="status"><strong>正在汇总内容缺口</strong><span>统计以服务端当前记录为准。</span></section>
    <GapsView v-else :report="report" :error="error" :notice="notice" :busy="busy" @export="exportNow" />
  </section>
</template>
