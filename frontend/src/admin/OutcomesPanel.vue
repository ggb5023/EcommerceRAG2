<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { getOutcomes, type ResolutionOutcome } from './api'
import OutcomesSummary from './OutcomesSummary.vue'

type OutcomesRange = '7d' | '30d'
const range = ref<OutcomesRange>('30d')
const summary = ref<ResolutionOutcome | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    summary.value = await getOutcomes(range.value)
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '结果数据加载失败。'
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<template>
  <section class="content-area outcomes-panel">
    <div class="section-head">
      <div><h2>结果度量</h2><p>窗口内每轮提问归入互斥计数；比率的分母只含已确定结果的轮次。</p></div>
      <label>窗口<select v-model="range" @change="load"><option value="7d">近 7 天</option><option value="30d">近 30 天</option></select></label>
    </div>
    <div v-if="error" class="alert" role="alert"><span>{{ error }}</span><button type="button" @click="load">重试</button></div>
    <section v-if="loading" class="state-panel" role="status"><strong>正在汇总结果数据</strong><span>统计以服务端当前记录为准。</span></section>
    <OutcomesSummary v-else-if="summary" :summary="summary" />
  </section>
</template>
