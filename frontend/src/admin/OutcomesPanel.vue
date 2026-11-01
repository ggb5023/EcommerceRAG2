<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { getOutcomes, type ResolutionOutcome } from './api'

type OutcomesRange = '7d' | '30d'
const range = ref<OutcomesRange>('30d')
const summary = ref<ResolutionOutcome | null>(null)
const loading = ref(false)
const error = ref('')

const resolved = computed(() => (summary.value?.solved ?? 0) + (summary.value?.clarified ?? 0) + (summary.value?.failed ?? 0))
const hasData = computed(() => resolved.value > 0)

function rate(value: number | null | undefined) {
  return value === null || value === undefined ? '暂无数据' : `${(value * 100).toFixed(1)}%`
}
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
    <template v-else-if="summary">
      <div class="health-grid">
        <article class="health-cell"><span>直接解决</span><strong>{{ summary.solved }}</strong><small>DONE 且 5 分钟内没有新轮次</small></article>
        <article class="health-cell"><span>澄清后解决</span><strong>{{ summary.clarified }}</strong><small>ASKING 且同会话之后出现 DONE</small></article>
        <article class="health-cell"><span>失败</span><strong>{{ summary.failed }}</strong><small>状态 FAILED</small></article>
        <article class="health-cell"><span>自动解决率</span><strong>{{ hasData ? rate(summary.auto_rate) : '暂无数据' }}</strong><small>{{ summary.solved }} / {{ resolved }} 轮</small></article>
        <article class="health-cell"><span>含澄清解决率</span><strong>{{ hasData ? rate(summary.assisted_rate) : '暂无数据' }}</strong><small>{{ summary.solved + summary.clarified }} / {{ resolved }} 轮</small></article>
      </div>
      <p class="scope-note">窗口内共 {{ summary.total }} 轮提问；{{ summary.unknown }} 轮缺少决策记录，不计入比率分母。参与计算 {{ resolved }} 轮。</p>
    </template>
  </section>
</template>
