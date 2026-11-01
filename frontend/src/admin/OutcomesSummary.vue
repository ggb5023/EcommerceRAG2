<script setup lang="ts">
import type { ResolutionOutcome } from './api'

const props = defineProps<{ summary: ResolutionOutcome }>()

function rate(value: number | null | undefined) {
  return value === null || value === undefined ? '暂无数据' : `${(value * 100).toFixed(1)}%`
}
const directDenominator = () => props.summary.solved + props.summary.abandoned + props.summary.failed + props.summary.cancelled
</script>

<template>
  <div>
    <p v-if="summary.metric_gap" class="alert" role="alert">统计口径存在未归类轮次，数字仅供参考。</p>
    <p v-if="summary.truncated" class="notice" role="status">数据量过大，已按最近 5 万条统计。</p>
    <div class="health-grid">
      <article class="health-cell"><span>可判定</span><strong>{{ summary.scored_total }}</strong><small>已确定结果的轮次</small></article>
      <article class="health-cell"><span>直接解决</span><strong>{{ summary.solved }}</strong><small>状态 DONE</small></article>
      <article class="health-cell"><span>未解决（澄清未续 + 失败）</span><strong>{{ summary.abandoned + summary.failed }}</strong><small>澄清未续 {{ summary.abandoned }} · 失败 {{ summary.failed }}</small></article>
      <article class="health-cell"><span>已取消</span><strong>{{ summary.cancelled }}</strong><small>状态 CANCELLED</small></article>
      <article class="health-cell"><span>进行中</span><strong>{{ summary.pending }}</strong><small>ASKING 未超 10 分钟宽限</small></article>
      <article class="health-cell"><span>直接解决率</span><strong>{{ rate(summary.auto_rate) }}</strong><small>{{ summary.solved }} / {{ directDenominator() }} 轮</small></article>
      <article class="health-cell"><span>无需人工介入率</span><strong>{{ rate(summary.assisted_rate) }}</strong><small>{{ summary.solved + summary.clarified }} / {{ summary.scored_total }} 轮</small></article>
    </div>
    <p class="scope-note">表示未转人工的比例，不代表答案正确；正确率见离线评测。</p>
    <p class="scope-note">其中 {{ summary.reasked_within_5m }} 次答复后 5 分钟内顾客再次提问。</p>
    <p class="scope-note">{{ summary.turns_total }} 次提问中 {{ summary.scored_total }} 次可判定（{{ summary.unknown }} 次无决策记录，{{ summary.pending }} 次进行中）。</p>
  </div>
</template>
