<script setup lang="ts">
import type { HandoffReport, UsageReport } from './api'
import Table from '../components/Table.vue'
import Empty from '../components/Empty.vue'
defineProps<{ usage: UsageReport | null; handoff: HandoffReport | null; error?: string }>()
</script>

<template>
  <div class="report-panels" data-testid="report-panels">
    <p v-if="error" class="alert" role="alert" data-testid="report-error">{{ error }}</p>
    <section data-testid="report-usage">
      <h2>token 用量</h2>
      <template v-if="usage">
        <p>输入 {{ usage.input_tokens }} tokens · 输出 {{ usage.output_tokens }} tokens · 调用 {{ usage.calls }} 次 · 失败 {{ usage.failed_calls }} 次</p>
        <h3>按 slot</h3>
        <Table v-if="usage.by_slot.length" data-testid="report-usage-slots"><thead><tr><th>slot</th><th>输入</th><th>输出</th><th>调用</th><th>失败</th></tr></thead><tbody><tr v-for="row in usage.by_slot" :key="row.slot"><td>{{ row.slot }}</td><td>{{ row.input_tokens }}</td><td>{{ row.output_tokens }}</td><td>{{ row.calls }}</td><td>{{ row.failed_calls }}</td></tr></tbody></Table>
        <Empty v-else data-testid="report-usage-empty">当前窗口没有 token 用量。</Empty>
        <h3>按模型</h3>
        <Table v-if="usage.by_model.length" data-testid="report-usage-models"><thead><tr><th>model</th><th>输入</th><th>输出</th><th>调用</th><th>失败</th></tr></thead><tbody><tr v-for="row in usage.by_model" :key="row.model"><td>{{ row.model }}</td><td>{{ row.input_tokens }}</td><td>{{ row.output_tokens }}</td><td>{{ row.calls }}</td><td>{{ row.failed_calls }}</td></tr></tbody></Table>
        <Empty v-else data-testid="report-usage-models-empty">当前窗口没有按模型统计。</Empty>
      </template>
      <Empty v-else data-testid="report-usage-loading">用量数据未载入。</Empty>
    </section>
    <section data-testid="report-handoff">
      <h2>customer 转人工</h2>
      <template v-if="handoff">
        <p>分子 {{ handoff.handoff_conversations }} · 分母 {{ handoff.customer_conversations }} · 比率 {{ handoff.handoff_rate === null ? '无数据' : (handoff.handoff_rate * 100).toFixed(2) + '%' }}</p>
        <p v-if="handoff.handoff_rate === null" data-testid="report-handoff-none">无数据：窗口内没有 customer 消息。</p>
      </template>
      <Empty v-else data-testid="report-handoff-loading">转人工数据未载入。</Empty>
    </section>
  </div>
</template>
