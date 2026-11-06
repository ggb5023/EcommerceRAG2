<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ApiError, getReport, type EvaluationReport } from '../api'
import ReportView from '../ReportView.vue'

const report = ref<EvaluationReport | null>(null)
const loading = ref(false)
const error = ref('')
const notFound = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  notFound.value = false
  try {
    report.value = await getReport()
  } catch (caught) {
    report.value = null
    if (caught instanceof ApiError && caught.status === 404) {
      notFound.value = true
    } else {
      error.value = caught instanceof Error ? caught.message : '评测报告加载失败。'
    }
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="content-area report-page">
    <div class="section-head">
      <div><h2>评测报告</h2><p>只读展示报告 CLI 生成的同一份 JSON，不重算任何指标。</p></div>
    </div>
    <ReportView :report="report" :loading="loading" :error="error" :not-found="notFound" />
  </section>
</template>
