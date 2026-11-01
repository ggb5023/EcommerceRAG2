<script setup lang="ts">
import type { KnowledgeGapReport } from './api'

defineProps<{ report: KnowledgeGapReport | null; error?: string; notice?: string; busy?: boolean }>()
const emit = defineEmits<{ export: [] }>()

function formatTime(value: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
function reasonLabel(value: string) {
  return value === 'abandoned' ? '澄清未续' : value === 'failed' ? '失败' : value
}
</script>

<template>
  <div>
    <div class="section-head">
      <p>共 {{ report?.items.length ?? 0 }} 个归一化问题</p>
      <button class="primary" type="button" data-testid="gaps-export" :disabled="busy" @click="emit('export')">{{ busy ? '导出中…' : '导出评测用例片段' }}</button>
    </div>
    <p v-if="error" class="alert" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="report?.truncated" class="notice" role="status">数据量过大，已按最近 5 万条统计。</p>
    <div v-if="report && report.items.length" class="table-wrap" data-testid="gaps-table">
      <table>
        <thead><tr><th>原句</th><th>归一化</th><th>次数</th><th>最近出现</th><th>原因</th></tr></thead>
        <tbody>
          <tr v-for="item in report.items" :key="item.normalized" data-testid="gap-row">
            <td><strong>{{ item.query }}</strong></td>
            <td>{{ item.normalized }}</td>
            <td>{{ item.count }}</td>
            <td>{{ formatTime(item.last_seen) }}</td>
            <td><span class="status" :class="`tone-${item.reason === 'failed' ? 'bad' : 'pending'}`">{{ reasonLabel(item.reason) }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <section v-else-if="report" class="state-panel" data-testid="gaps-empty">
      <strong>暂无内容缺口</strong>
      <span>当前窗口没有 abandoned 或 failed 的问题。</span>
    </section>
  </div>
</template>
