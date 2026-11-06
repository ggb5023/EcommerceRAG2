<script setup lang="ts">
import { computed } from 'vue'
import type { InspectionItem } from './api'
import Badge from '../components/Badge.vue'
import Button from '../components/Button.vue'
import Empty from '../components/Empty.vue'

const props = defineProps<{ items: InspectionItem[]; loading?: boolean; busy?: boolean }>()
const emit = defineEmits<{ action: [item: InspectionItem, status: 'acknowledged' | 'resolved' | 'dismissed'] }>()

const groups = computed(() => [
  { key: 'high', label: '高优先级', items: props.items.filter(item => item.severity === 'high') },
  { key: 'medium', label: '中优先级', items: props.items.filter(item => item.severity === 'medium') },
])

const kindLabels: Record<string, string> = {
  expired_not_removed: '过期未下架',
  newer_version_conflict: '新版本冲突',
  unanswered_high_frequency: '高频未答',
  citation_invalid: '引用失效',
}
const statusLabels: Record<string, string> = {
  open: '待处理', acknowledged: '已确认', resolved: '已处理', dismissed: '已忽略',
}

function kindLabel(value: string) { return kindLabels[value] ?? value }
function statusLabel(value: string) { return statusLabels[value] ?? value }
function severityTone(value: string) { return value === 'high' ? 'danger' as const : 'warning' as const }
function formatTime(value: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
function subjectSummary(item: InspectionItem) {
  const evidence = item.evidence ?? {}
  const title = typeof evidence.title === 'string' ? evidence.title : ''
  const query = typeof evidence.query === 'string' ? evidence.query : typeof evidence.normalized === 'string' ? evidence.normalized : ''
  return title || query || item.subject_id
}
function evidenceText(item: InspectionItem) { return JSON.stringify(item.evidence ?? {}, null, 2) }
</script>

<template>
  <div>
    <section v-if="loading" class="state-panel" role="status"><strong>正在读取巡检结果</strong><span>数据以服务端记录为准。</span></section>
    <Empty v-else-if="!items.length" data-testid="inspections-empty">
      <template #title>暂无待办巡检项</template>
      点击「立即巡检」重新检查当前范围内已发布内容与引用。
    </Empty>
    <template v-else>
      <section v-for="group in groups" :key="group.key" :data-testid="`inspection-group-${group.key}`" class="inspection-group">
        <header class="inspection-group-head">
          <h3>{{ group.label }}</h3>
          <span>{{ group.items.length }} 项</span>
        </header>
        <p v-if="!group.items.length" class="inspection-group-empty">本组暂无待办。</p>
        <article v-for="item in group.items" :key="item.id" class="inspection-card" data-testid="inspection-card">
          <header class="inspection-card-head">
            <Badge :tone="severityTone(item.severity)">{{ item.severity === 'high' ? '高' : '中' }}</Badge>
            <strong>{{ kindLabel(item.kind) }}</strong>
            <span class="inspection-scope">{{ item.shop_id || '全店' }}</span>
            <Badge tone="muted">{{ statusLabel(item.status) }}</Badge>
          </header>
          <p class="inspection-subject">{{ subjectSummary(item) }}</p>
          <p class="inspection-meta">检测时间 {{ formatTime(item.detected_at) }} · 对象 {{ item.subject_type }} / {{ item.subject_id }}</p>
          <details class="inspection-evidence">
            <summary>证据面板</summary>
            <pre>{{ evidenceText(item) }}</pre>
          </details>
          <div v-if="item.status === 'open' || item.status === 'acknowledged'" class="inspection-actions">
            <Button :disabled="busy" data-testid="inspection-acknowledge" @click="emit('action', item, 'acknowledged')">确认</Button>
            <Button :disabled="busy" data-testid="inspection-resolve" @click="emit('action', item, 'resolved')">已处理</Button>
            <Button :disabled="busy" data-testid="inspection-dismiss" @click="emit('action', item, 'dismissed')">忽略</Button>
          </div>
        </article>
      </section>
    </template>
  </div>
</template>

<style scoped>
.inspection-group{margin-top:18px}
.inspection-group-head{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:9px}
.inspection-group-head h3{margin:0;font-size:13px}
.inspection-group-head span,.inspection-group-empty{color:var(--color-text-muted);font-size:10px}
.inspection-group-empty{padding:14px;border:1px solid var(--color-border);background:var(--color-surface)}
.inspection-card{padding:14px;margin-bottom:9px;border:1px solid var(--color-border);background:var(--color-surface)}
.inspection-card-head{display:flex;align-items:center;gap:9px;flex-wrap:wrap}
.inspection-card-head strong{font-size:12px}
.inspection-scope{margin-left:auto;color:var(--color-text-muted);font-size:10px}
.inspection-subject{margin:10px 0 4px;font-size:12px}
.inspection-meta{margin:0 0 10px;color:var(--color-text-muted);font-size:9px;overflow-wrap:anywhere}
.inspection-evidence{border-top:1px solid var(--color-border);padding-top:9px}
.inspection-evidence summary{cursor:pointer;color:var(--color-primary);font-size:10px}
.inspection-evidence pre{margin:8px 0 0;padding:9px;overflow:auto;background:var(--color-bg);color:var(--color-text);font-size:9px;line-height:1.55;max-height:240px}
.inspection-actions{display:flex;gap:7px;margin-top:11px}
</style>
