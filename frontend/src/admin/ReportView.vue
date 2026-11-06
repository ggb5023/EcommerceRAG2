<script setup lang="ts">
import { computed } from 'vue'
import type { EvaluationReport, ReportSection } from './api'
import Empty from '../components/Empty.vue'

const props = defineProps<{
  report: EvaluationReport | null
  loading?: boolean
  error?: string
  notFound?: boolean
}>()

const input = computed(() => props.report?.input ?? {})
const operations = computed(() => props.report?.operations ?? {})
const handoff = computed(() => props.report?.handoff ?? {})
const quality = computed(() => props.report?.quality ?? {})
const tokens = computed(() => props.report?.tokens ?? {})

function sectionStatus(section: ReportSection): string {
  return section.status === 'MEASURED' ? '已测量' : '未测量'
}
function field(section: ReportSection, key: string): string {
  const value = section[key]
  if (value === null || value === undefined || value === '') return '未测量'
  if (typeof value === 'boolean') return value ? '是' : '否'
  return String(value)
}
function metric(section: ReportSection, key: string): string {
  if (section.status !== 'MEASURED') return '未测量'
  return field(section, key)
}
function rate(section: ReportSection, key: string): string {
  if (section.status !== 'MEASURED' || section[key] === null || section[key] === undefined) return '未测量'
  const value = section[key]
  return typeof value === 'number' ? `${(value * 100).toFixed(1)}%` : String(value)
}
function perThousand(section: ReportSection, key: string): string {
  if (section.status !== 'MEASURED' || section[key] === null || section[key] === undefined) return '未测量'
  const value = section[key]
  return typeof value === 'number' ? value.toFixed(2) : String(value)
}
function rows(section: ReportSection, key: string): Record<string, unknown>[] {
  const value = section[key]
  return Array.isArray(value) ? value.filter((row): row is Record<string, unknown> => typeof row === 'object' && row !== null) : []
}
function tokenRow(row: Record<string, unknown>, key: string): string {
  if (tokens.value.status !== 'MEASURED') return '未测量'
  const value = row[key]
  return value === null || value === undefined ? '未测量' : String(value)
}
function tokenRowPerThousand(row: Record<string, unknown>, key: string): string {
  if (tokens.value.status !== 'MEASURED') return '未测量'
  const value = row[key]
  return typeof value === 'number' ? value.toFixed(2) : '未测量'
}
function title(value: unknown): string {
  return typeof value === 'string' && value ? value : '—'
}
function list(value: unknown): string {
  return Array.isArray(value) && value.length ? value.map(String).join(', ') : '未测量'
}
function record(value: unknown): string {
  return value && typeof value === 'object' ? JSON.stringify(value) : '未测量'
}
</script>

<template>
  <section v-if="loading" class="state-panel" role="status"><strong>正在读取评测报告</strong><span>页面只展示报告 CLI 已算好的值。</span></section>
  <section v-else-if="notFound" class="state-panel" data-testid="report-missing">
    <strong>未生成报告</strong>
    <span>请先运行报告 CLI（scripts/dev/demo-report.py，用法见 docs/workpackages/36-m9-report.md）。</span>
  </section>
  <p v-else-if="error" class="alert" role="alert">{{ error }}</p>
  <div v-else-if="report" class="report-view">
    <section class="report-section" data-testid="report-input">
      <header class="report-section-head"><h2>输入</h2><span>{{ sectionStatus(input) }}</span></header>
      <dl class="report-grid">
        <div><dt>git commit</dt><dd>{{ field(input, 'git_commit') }}</dd></div>
        <div><dt>演示包 sha256</dt><dd class="report-hash">{{ field(input, 'package_sha256') }}</dd></div>
        <div><dt>manifest sha256</dt><dd class="report-hash">{{ field(input, 'manifest_sha256') }}</dd></div>
        <div><dt>问题集 sha256</dt><dd class="report-hash">{{ field(input, 'questions_sha256') }}</dd></div>
        <div><dt>business_date</dt><dd>{{ field(input, 'business_date') }}</dd></div>
        <div><dt>人群</dt><dd>{{ list(input.cohorts) }}</dd></div>
        <div><dt>窗口</dt><dd>{{ field(input, 'window_start') }} 至 {{ field(input, 'window_end') }}</dd></div>
        <div><dt>冻结评测报告 sha256</dt><dd class="report-hash">{{ field(input, 'quality_report_sha256') }}</dd></div>
        <div><dt>active 版本</dt><dd class="report-hash">{{ record(input.version_ids) }}</dd></div>
      </dl>
      <p v-if="input.reason" class="report-reason">{{ input.reason }}</p>
    </section>

    <section class="report-section" data-testid="report-operations">
      <header class="report-section-head"><h2>运营结果</h2><span>{{ sectionStatus(operations) }}</span></header>
      <div class="report-grid">
        <div><span>总轮次</span><strong>{{ metric(operations, 'turns_total') }}</strong></div>
        <div><span>计分轮次</span><strong>{{ metric(operations, 'scored_total') }}</strong></div>
        <div><span>已解决</span><strong>{{ metric(operations, 'solved') }}</strong></div>
        <div><span>澄清</span><strong>{{ metric(operations, 'clarified') }}</strong></div>
        <div><span>放弃</span><strong>{{ metric(operations, 'abandoned') }}</strong></div>
        <div><span>失败</span><strong>{{ metric(operations, 'failed') }}</strong></div>
        <div><span>取消</span><strong>{{ metric(operations, 'cancelled') }}</strong></div>
        <div><span>进行中</span><strong>{{ metric(operations, 'pending') }}</strong></div>
        <div><span>未知</span><strong>{{ metric(operations, 'unknown') }}</strong></div>
        <div><span>直接解决率</span><strong>{{ rate(operations, 'auto_rate') }}</strong></div>
        <div><span>无需人工介入率</span><strong>{{ rate(operations, 'assisted_rate') }}</strong></div>
      </div>
      <p v-if="operations.reason" class="report-reason">{{ operations.reason }}</p>
    </section>

    <section class="report-section" data-testid="report-quality">
      <header class="report-section-head"><h2>质量</h2><span>{{ sectionStatus(quality) }}</span></header>
      <div class="report-grid">
        <div><span>错答率</span><strong>{{ rate(quality, 'wrong_answer_rate') }}</strong></div>
        <div><span>用例数</span><strong>{{ metric(quality, 'case_count') }}</strong></div>
        <div><span>评审者</span><strong>{{ field(quality, 'reviewer_kind') }}</strong></div>
        <div><span>人工复核完成</span><strong>{{ field(quality, 'human_review_completed') }}</strong></div>
        <div><span>来源报告 sha256</span><strong class="report-hash">{{ field(quality, 'source_sha256') }}</strong></div>
      </div>
      <p v-if="quality.reason" class="report-reason">{{ quality.reason }}</p>
    </section>

    <section class="report-section" data-testid="report-handoff">
      <header class="report-section-head"><h2>转人工</h2><span>{{ sectionStatus(handoff) }}</span></header>
      <div class="report-grid">
        <div><span>转人工会话</span><strong>{{ metric(handoff, 'handoff_conversations') }}</strong></div>
        <div><span>顾客会话分母</span><strong>{{ metric(handoff, 'customer_conversations') }}</strong></div>
        <div><span>转人工率</span><strong>{{ rate(handoff, 'handoff_rate') }}</strong></div>
      </div>
      <p v-if="handoff.reason" class="report-reason">{{ handoff.reason }}</p>
    </section>

    <section class="report-section" data-testid="report-tokens">
      <header class="report-section-head"><h2>token</h2><span>{{ sectionStatus(tokens) }}</span></header>
      <div class="report-grid">
        <div><span>问题数</span><strong>{{ metric(tokens, 'question_count') }}</strong></div>
        <div><span>输入 tokens</span><strong>{{ metric(tokens, 'input_tokens') }}</strong></div>
        <div><span>输出 tokens</span><strong>{{ metric(tokens, 'output_tokens') }}</strong></div>
        <div><span>调用次数</span><strong>{{ metric(tokens, 'calls') }}</strong></div>
        <div><span>失败次数</span><strong>{{ metric(tokens, 'failed_calls') }}</strong></div>
        <div><span>输入 tokens / 千次</span><strong>{{ perThousand(tokens, 'input_tokens_per_1000') }}</strong></div>
        <div><span>输出 tokens / 千次</span><strong>{{ perThousand(tokens, 'output_tokens_per_1000') }}</strong></div>
        <div><span>调用 / 千次</span><strong>{{ perThousand(tokens, 'calls_per_1000') }}</strong></div>
        <div><span>失败 / 千次</span><strong>{{ perThousand(tokens, 'failed_calls_per_1000') }}</strong></div>
      </div>
      <div v-for="key in ['by_slot', 'by_model']" :key="key" class="report-breakdown">
        <h3>{{ key === 'by_slot' ? '按环节' : '按模型' }}</h3>
        <table v-if="rows(tokens, key).length" class="report-table">
          <thead><tr><th>{{ key === 'by_slot' ? '环节' : '模型' }}</th><th>输入</th><th>输出</th><th>调用</th><th>失败</th><th>输入/千次</th><th>输出/千次</th><th>调用/千次</th><th>失败/千次</th></tr></thead>
          <tbody>
            <tr v-for="(row, index) in rows(tokens, key)" :key="index">
              <td>{{ title(row[key === 'by_slot' ? 'slot' : 'model']) }}</td>
              <td>{{ tokenRow(row, 'input_tokens') }}</td>
              <td>{{ tokenRow(row, 'output_tokens') }}</td>
              <td>{{ tokenRow(row, 'calls') }}</td>
              <td>{{ tokenRow(row, 'failed_calls') }}</td>
              <td>{{ tokenRowPerThousand(row, 'input_tokens_per_1000') }}</td>
              <td>{{ tokenRowPerThousand(row, 'output_tokens_per_1000') }}</td>
              <td>{{ tokenRowPerThousand(row, 'calls_per_1000') }}</td>
              <td>{{ tokenRowPerThousand(row, 'failed_calls_per_1000') }}</td>
            </tr>
          </tbody>
        </table>
        <Empty v-else><template #title>暂无明细</template>报告没有返回 {{ key }} 行。</Empty>
      </div>
      <p v-if="tokens.reason" class="report-reason">{{ tokens.reason }}</p>
    </section>
  </div>
</template>

<style scoped>
.report-view{display:grid;gap:16px}
.report-section{border:1px solid var(--color-border);background:var(--color-surface);padding:16px}
.report-section-head{display:flex;align-items:baseline;justify-content:space-between;gap:12px;margin-bottom:12px}
.report-section-head h2{margin:0;font-size:14px}
.report-section-head span{color:var(--color-text-muted);font-size:10px}
.report-grid{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:1px;border:1px solid var(--color-border);background:var(--color-border)}
.report-grid>div{min-width:0;padding:10px;background:var(--color-bg);display:grid;gap:5px}
.report-grid dt,.report-grid span{color:var(--color-text-muted);font-size:9px}
.report-grid dd{margin:0;font-size:11px;overflow-wrap:anywhere}
.report-grid strong{font-size:12px}
.report-hash{font-family:ui-monospace,monospace;font-size:9px!important;overflow-wrap:anywhere}
.report-reason{margin:10px 0 0;color:var(--color-text-muted);font-size:10px}
.report-breakdown{margin-top:14px}
.report-breakdown h3{margin:0 0 7px;font-size:11px}
.report-table{width:100%;border-collapse:collapse;font-size:9px;text-align:left}
.report-table th,.report-table td{padding:7px 8px;border-top:1px solid var(--color-border);white-space:nowrap}
.report-table th{color:var(--color-text-muted);font-weight:600}
</style>
