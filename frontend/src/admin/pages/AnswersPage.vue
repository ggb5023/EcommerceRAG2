<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import Badge from '../../components/Badge.vue'
import Button from '../../components/Button.vue'
import Drawer from '../../components/Drawer.vue'
import PageHeader from '../../components/PageHeader.vue'
import StatePanel from '../../components/StatePanel.vue'
import Table from '../../components/Table.vue'
import {
  answerDetailPath, answerListPath, request,
  type AnswerDetail, type AnswerList, type AnswerRow,
} from '../api'

const kind = ref<'all' | 'staff' | 'customer'>('all')
const mock = ref<'' | 'true' | 'false'>('')
const items = ref<AnswerRow[]>([])
const next = ref('')
const loading = ref(false)
const loadingMore = ref(false)
const error = ref('')
const detail = ref<AnswerDetail | null>(null)
const detailOpen = ref(false)
const detailLoading = ref(false)

const kindLabels: Record<string, string> = { staff: '员工轮次', customer: '顾客消息' }
const behaviorLabels: Record<string, string> = {
  ANSWER: '回答', CLARIFY: '追问', HANDOFF: '转人工', '': '—',
}
const stateLabels: Record<string, string> = {
  DONE: '完成', ASKING: '追问中', FAILED: '失败', PENDING: '待处理', EXECUTING: '执行中', CANCELLED: '已取消',
}

function timeText(value: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
function kindLabel(value: string) { return kindLabels[value] ?? value }
function behaviorLabel(value: string) { return behaviorLabels[value] ?? value }
function stateLabel(value: string) { return stateLabels[value] ?? (value || '—') }

async function load(reset = true) {
  if (reset) { loading.value = true; error.value = '' } else { loadingMore.value = true }
  try {
    const page = await request<AnswerList>(answerListPath(kind.value, mock.value, reset ? undefined : next.value))
    items.value = reset ? page.items : [...items.value, ...page.items]
    next.value = page.next_before ?? ''
  } catch (cause) {
    error.value = (cause as Error).message
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

async function openDetail(row: AnswerRow) {
  detailOpen.value = true
  detailLoading.value = true
  detail.value = null
  try {
    detail.value = await request<AnswerDetail>(answerDetailPath(row.kind, row.id))
  } catch (cause) {
    error.value = (cause as Error).message
    detailOpen.value = false
  } finally {
    detailLoading.value = false
  }
}

watch([kind, mock], () => void load(true))
onMounted(() => void load(true))

const hasMore = computed(() => next.value !== '')
</script>

<template>
  <section class="answers-page">
    <PageHeader eyebrow="知识" title="回答与引用"
      subtitle="审查员工与顾客 AI 回答的全文、引用与事实快照。列表截断问题与回答，打开详情会记录审计。" />

    <section class="answers-filters" aria-label="筛选回答">
      <label>来源
        <select v-model="kind" data-testid="answer-kind">
          <option value="all">全部</option>
          <option value="staff">员工轮次</option>
          <option value="customer">顾客消息</option>
        </select>
      </label>
      <label>结果来源
        <select v-model="mock" data-testid="answer-mock">
          <option value="">全部</option>
          <option value="false">真实</option>
          <option value="true">Mock</option>
        </select>
      </label>
      <Button :disabled="loading" data-testid="answer-refresh" @click="load(true)">刷新</Button>
    </section>

    <StatePanel v-if="loading" tone="loading" title="正在读取回答" detail="列表只包含当前店铺范围内的记录。" />
    <StatePanel v-else-if="error" tone="error" title="回答列表暂不可用" :detail="error">
      <Button @click="load(true)">重试</Button>
    </StatePanel>
    <StatePanel v-else-if="!items.length" tone="empty" title="当前筛选下没有回答"
      detail="员工轮次与顾客 AI 消息完成回答后会出现在这里。" />

    <template v-else>
      <Table data-testid="answer-table">
        <thead><tr><th>时间</th><th>来源</th><th>店铺</th><th>问题</th><th>引用/事实</th><th>状态</th><th></th></tr></thead>
        <tbody>
          <tr v-for="row in items" :key="`${row.kind}:${row.id}`" :data-testid="`answer-row-${row.kind}`">
            <td class="answers-time">{{ timeText(row.created_at) }}</td>
            <td><span class="answers-badges"><Badge :tone="row.kind === 'customer' ? 'muted' : 'success'">{{ kindLabel(row.kind) }}</Badge>
              <Badge v-if="row.is_mock" tone="warning">mock</Badge></span></td>
            <td>{{ row.shop_id || '全店' }}</td>
            <td class="answers-question">{{ row.question }}</td>
            <td>{{ row.citation_count }} / {{ row.fact_count }}</td>
            <td>{{ stateLabel(row.state) }} · {{ behaviorLabel(row.behavior) }}</td>
            <td><Button variant="text" :data-testid="`answer-open-${row.kind}`" @click="openDetail(row)">查看</Button></td>
          </tr>
        </tbody>
      </Table>
      <div v-if="hasMore" class="answers-more">
        <Button :disabled="loadingMore" @click="load(false)">{{ loadingMore ? '正在读取…' : '加载更多' }}</Button>
      </div>
    </template>

    <Drawer :open="detailOpen" title="回答与引用详情" @close="detailOpen = false">
      <StatePanel v-if="detailLoading" tone="loading" title="正在读取详情" />
      <template v-else-if="detail">
        <dl class="answer-meta">
          <div><dt>来源</dt><dd>{{ kindLabel(detail.kind) }}</dd></div>
          <div><dt>时间</dt><dd>{{ timeText(detail.created_at) }}</dd></div>
          <div><dt>店铺</dt><dd>{{ detail.shop_id || '全店' }}</dd></div>
          <div><dt>状态</dt><dd>{{ stateLabel(detail.state) }} · {{ behaviorLabel(detail.behavior) }}</dd></div>
          <div><dt>结果</dt><dd>{{ detail.is_mock ? 'Mock' : '真实' }}</dd></div>
          <div><dt>引用 / 事实</dt><dd>{{ detail.citation_count }} / {{ detail.fact_count }}</dd></div>
        </dl>
        <section class="answer-block"><h3>问题</h3><p>{{ detail.question || '—' }}</p></section>
        <section class="answer-block"><h3>回答</h3><p class="answer-text">{{ detail.answer }}</p></section>
        <section class="answer-block"><h3>引用</h3>
          <p v-if="!detail.citations.length" class="answer-empty">没有引用。</p>
          <ul v-else class="answer-citations">
            <li v-for="citation in detail.citations" :key="citation.evidence_id">
              <header>
                <Badge :tone="citation.valid ? 'success' : 'danger'">{{ citation.valid ? '有效' : '已失效' }}</Badge>
                <strong>[{{ citation.citation_index }}] {{ citation.title || citation.document_id || citation.evidence_id }}</strong>
              </header>
              <p>{{ citation.snippet }}</p>
              <small>{{ citation.document_id }} · {{ citation.version_id }} · {{ citation.disclosure_class }}</small>
            </li>
          </ul>
        </section>
        <section class="answer-block"><h3>事实快照</h3>
          <p v-if="!detail.facts.length" class="answer-empty">没有事实快照。</p>
          <ul v-else class="answer-facts">
            <li v-for="(fact, index) in detail.facts" :key="`${fact.tool_name}-${index}`">
              <header><Badge :tone="fact.is_mock ? 'warning' : 'success'">{{ fact.is_mock ? 'mock' : '真实' }}</Badge>
                <strong>{{ fact.tool_name }}</strong><small>{{ fact.resource_id }}</small></header>
              <p>观测时间 {{ timeText(fact.observed_at) }}</p>
              <pre>{{ fact.value_json }}</pre>
            </li>
          </ul>
        </section>
      </template>
    </Drawer>
  </section>
</template>

<style scoped>
.answers-page{display:grid;gap:var(--space-4)}
.answers-filters{display:flex;align-items:flex-end;gap:var(--space-3);flex-wrap:wrap}
.answers-filters label{display:grid;gap:var(--space-1);font-size:var(--text-xs);color:var(--color-text-muted);font-weight:650}
.answers-filters select{min-height:var(--control-height);padding:var(--space-2);border:1px solid var(--color-border-strong);border-radius:var(--radius-sm);background:var(--color-surface);color:var(--color-text);font:inherit;font-size:var(--text-sm)}
.answers-badges{display:inline-flex;align-items:center;gap:var(--space-2)}
.answers-time{white-space:nowrap;color:var(--color-text-muted);font-size:var(--text-xs)}
.answers-question{max-width:360px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.answers-more{display:flex;justify-content:center}
.answer-meta{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:var(--space-3);margin:0}
.answer-meta dt{color:var(--color-text-muted);font-size:var(--text-xs);font-weight:650}
.answer-meta dd{margin:var(--space-1) 0 0;font-size:var(--text-sm)}
.answer-block h3{margin:0 0 var(--space-2);font-size:var(--text-sm)}
.answer-block p{margin:0;font-size:var(--text-sm);line-height:1.6;white-space:pre-wrap;overflow-wrap:anywhere}
.answer-empty{color:var(--color-text-muted)}
.answer-citations,.answer-facts{display:grid;gap:var(--space-3);margin:0;padding:0;list-style:none}
.answer-citations li,.answer-facts li{padding:var(--space-3);border:1px solid var(--color-border);border-radius:var(--radius-sm);background:var(--color-bg-subtle)}
.answer-citations header,.answer-facts header{display:flex;align-items:center;gap:var(--space-2);flex-wrap:wrap}
.answer-citations p,.answer-facts p{margin:var(--space-2) 0;font-size:var(--text-sm);white-space:pre-wrap}
.answer-citations small,.answer-facts small{color:var(--color-text-muted);font-size:var(--text-xs)}
.answer-facts pre{margin:var(--space-2) 0 0;padding:var(--space-2);border-radius:var(--radius-sm);background:var(--color-surface);font-family:var(--font-mono);font-size:var(--text-xs);white-space:pre-wrap;overflow-wrap:anywhere}
</style>
