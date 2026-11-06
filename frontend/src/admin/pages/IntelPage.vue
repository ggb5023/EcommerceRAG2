<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import {
  ApiError,
  createIntelWatchConfig,
  getIntelObservations,
  getIntelWatchConfigs,
  updateIntelWatchConfig,
  type IntelObservation,
  type IntelWatchConfig,
} from '../api'
import Badge from '../../components/Badge.vue'
import Button from '../../components/Button.vue'
import Empty from '../../components/Empty.vue'
import Table from '../../components/Table.vue'

const observations = ref<IntelObservation[]>([])
const watchConfigs = ref<IntelWatchConfig[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const queryDegraded = ref(false)
const filters = reactive({ market: '', category: '', platform: '', since: '', query: '' })
const form = reactive({ market: '', category: '', platform: '', enabled: true })

const sinceRFC = computed(() => filters.since ? new Date(filters.since).toISOString() : '')

function formatTime(value: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'
}
function failure(caught: unknown, fallback: string) {
  if (caught instanceof ApiError && caught.status === 503) return '情报服务未启动。'
  return caught instanceof Error ? caught.message : fallback
}

async function loadObservations() {
  loading.value = true
  error.value = ''
  queryDegraded.value = false
  try {
    const result = await getIntelObservations({
      market: filters.market.trim(), category: filters.category.trim(),
      platform: filters.platform.trim(), since: sinceRFC.value, query: filters.query.trim(),
    })
    observations.value = result.items
    queryDegraded.value = result.query_degraded
  } catch (caught) {
    error.value = failure(caught, '观测加载失败。')
  } finally {
    loading.value = false
  }
}
async function loadWatchConfigs() {
  try {
    watchConfigs.value = (await getIntelWatchConfigs()).items
  } catch (caught) {
    error.value = failure(caught, '监控配置加载失败。')
  }
}
async function createWatch() {
  busy.value = true
  error.value = ''
  try {
    await createIntelWatchConfig({
      market: form.market.trim(), category: form.category.trim(),
      platform: form.platform.trim(), enabled: form.enabled,
    })
    form.market = ''; form.category = ''; form.platform = ''; form.enabled = true
    await loadWatchConfigs()
  } catch (caught) {
    error.value = failure(caught, '监控配置创建失败。')
  } finally {
    busy.value = false
  }
}
async function toggleWatch(item: IntelWatchConfig) {
  busy.value = true
  error.value = ''
  try {
    await updateIntelWatchConfig(item.id, { enabled: !item.enabled })
    await loadWatchConfigs()
  } catch (caught) {
    error.value = failure(caught, '监控配置更新失败。')
  } finally {
    busy.value = false
  }
}
onMounted(() => { void loadObservations(); void loadWatchConfigs() })
</script>

<template>
  <section class="content-area intel-page">
    <div class="section-head">
      <div><h2>情报观察</h2><p>可按语义查询或 observed_at 排序；页面只读，不触发抓取。</p></div>
    </div>
    <p v-if="error" class="alert" role="alert">{{ error }}</p>
    <p v-if="queryDegraded" class="alert" role="status">语义查询暂不可用，已按时间排序。</p>

    <section class="intel-block" data-testid="intel-observations">
      <header class="intel-block-head"><h3>观测列表</h3><span>截至 observed_at</span></header>
      <div class="intel-filters">
        <label>市场<input v-model="filters.market" placeholder="market"></label>
        <label>类目<input v-model="filters.category" placeholder="category"></label>
        <label>平台<input v-model="filters.platform" placeholder="platform"></label>
        <label>截止时间<input v-model="filters.since" type="datetime-local"></label>
        <label>语义搜索<input v-model="filters.query" maxlength="500" placeholder="例如：保温杯 竞品 定价" data-testid="intel-query"></label>
        <Button variant="primary" :disabled="loading" data-testid="intel-filter" @click="loadObservations">搜索 / 筛选</Button>
      </div>
      <Table v-if="observations.length" data-testid="intel-observation-table">
        <thead><tr><th>市场</th><th>类目</th><th>平台</th><th>标题</th><th>分数</th><th>相似度</th><th>发布日期</th><th>截至 observed_at</th><th>来源</th></tr></thead>
        <tbody>
          <tr v-for="item in observations" :key="`${item.market}/${item.category}/${item.platform}/${item.source_url}`" data-testid="intel-observation-row">
            <td>{{ item.market }}</td><td>{{ item.category }}</td><td>{{ item.platform }}</td>
            <td><strong>{{ item.title || '—' }}</strong></td>
            <td>{{ item.score.toFixed(2) }}</td>
            <td>{{ filters.query ? item.similarity.toFixed(3) : '—' }}</td>
            <td>{{ item.published_date || '日期未知' }}</td>
            <td>{{ formatTime(item.observed_at) }}</td>
            <td class="intel-url">{{ item.source_url }}</td>
          </tr>
        </tbody>
      </Table>
      <Empty v-else data-testid="intel-empty">
        <template #title>暂无观测</template>
        当前筛选范围没有共享观测。
      </Empty>
    </section>

    <section class="intel-block" data-testid="intel-watch-configs">
      <header class="intel-block-head"><h3>监控配置</h3><span>只保存商家想监控的目标</span></header>
      <form class="intel-form" @submit.prevent="createWatch">
        <label>市场<input v-model="form.market" required maxlength="200"></label>
        <label>类目<input v-model="form.category" required maxlength="200"></label>
        <label>平台<input v-model="form.platform" required maxlength="200"></label>
        <label class="intel-check"><input v-model="form.enabled" type="checkbox">启用</label>
        <Button type="submit" variant="primary" :disabled="busy" data-testid="intel-create">添加监控</Button>
      </form>
      <Table v-if="watchConfigs.length" data-testid="intel-watch-table">
        <thead><tr><th>市场</th><th>类目</th><th>平台</th><th>状态</th><th>创建时间</th><th></th></tr></thead>
        <tbody>
          <tr v-for="item in watchConfigs" :key="item.id" data-testid="intel-watch-row">
            <td>{{ item.market }}</td><td>{{ item.category }}</td><td>{{ item.platform }}</td>
            <td><Badge :tone="item.enabled ? 'success' : 'muted'">{{ item.enabled ? '启用' : '停用' }}</Badge></td>
            <td>{{ formatTime(item.created_at) }}</td>
            <td><Button :disabled="busy" data-testid="intel-toggle" @click="toggleWatch(item)">{{ item.enabled ? '停用' : '启用' }}</Button></td>
          </tr>
        </tbody>
      </Table>
      <Empty v-else><template #title>暂无监控配置</template>添加一个市场/类目/平台目标。</Empty>
    </section>
  </section>
</template>

<style scoped>
.intel-block{margin-top:18px;border:1px solid var(--color-border);background:var(--color-surface);padding:16px}
.intel-block-head{display:flex;align-items:baseline;justify-content:space-between;gap:12px;margin-bottom:12px}
.intel-block-head h3{margin:0;font-size:14px}
.intel-block-head span{color:var(--color-text-muted);font-size:10px}
.intel-filters,.intel-form{display:flex;flex-wrap:wrap;gap:10px;align-items:end;margin-bottom:12px}
.intel-filters label,.intel-form label{display:grid;gap:5px;color:var(--color-text-muted);font-size:10px}
.intel-filters input,.intel-form input{min-height:33px;border:1px solid var(--color-border);border-radius:4px;padding:7px 9px;background:var(--color-surface);color:var(--color-text);font-size:11px}
.intel-check{display:flex!important;align-items:center;gap:6px}
.intel-check input{min-height:0}
.intel-url{max-width:280px;overflow-wrap:anywhere}
</style>
