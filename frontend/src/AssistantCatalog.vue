<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { searchCatalog, type CatalogItem } from './catalog-api'

const filters = reactive({ q: '', category: '', sku: '', attributeKey: '', attributeValue: '' })
const items = ref<CatalogItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 24
const notice = ref('')
const loading = ref(false)
const error = ref('')

function attributeSummary(item: CatalogItem): string {
  const entries = Object.entries(item.attributes ?? {}).slice(0, 4)
  return entries.map(([key, value]) => `${key}=${String(value)}`).join(' · ')
}

async function load(nextPage = page.value) {
  loading.value = true
  error.value = ''
  try {
    const result = await searchCatalog({ ...filters, page: nextPage, pageSize })
    items.value = result.items
    total.value = result.total
    page.value = result.page
    notice.value = result.notice
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '商品目录暂不可用，请稍后重试。'
  } finally {
    loading.value = false
  }
}

function submit() {
  void load(1)
}
function previous() {
  if (page.value > 1) void load(page.value - 1)
}
function next() {
  if (page.value * pageSize < total.value) void load(page.value + 1)
}

onMounted(() => void load(1))
</script>

<template>
  <main class="catalog-shell">
    <header class="catalog-head">
      <div>
        <p class="eyebrow">AI 查询台</p>
        <h1>商品目录</h1>
        <p class="subtitle">来自已发布的商品表；价格、库存、订单与配送以下方说明为准。</p>
      </div>
      <nav class="catalog-nav">
        <a href="/assistant">知识问答</a>
        <a href="/staff">客服工作台</a>
      </nav>
    </header>

    <form class="catalog-filters" @submit.prevent="submit">
      <label>关键词<input v-model="filters.q" placeholder="标题 / SKU / 品牌" maxlength="120" /></label>
      <label>品类<input v-model="filters.category" maxlength="128" /></label>
      <label>SKU<input v-model="filters.sku" maxlength="128" /></label>
      <label>属性名<input v-model="filters.attributeKey" placeholder="如 capacity_ml" maxlength="64" /></label>
      <label>属性值<input v-model="filters.attributeValue" maxlength="200" /></label>
      <button type="submit" :disabled="loading">{{ loading ? '查询中…' : '查询' }}</button>
    </form>

    <p v-if="notice" class="catalog-notice" role="status">{{ notice }}</p>
    <p v-if="error" class="catalog-error" role="alert">{{ error }}</p>

    <section class="catalog-table" :aria-busy="loading">
      <table>
        <thead><tr><th>SKU</th><th>标题</th><th>品类</th><th>品牌</th><th>颜色</th><th>尺码</th><th>属性</th></tr></thead>
        <tbody>
          <tr v-for="item in items" :key="item.sku_code + item.shop_id">
            <td><strong>{{ item.sku_code }}</strong></td>
            <td>{{ item.title }}</td>
            <td>{{ item.category || '—' }}</td>
            <td>{{ item.brand || '—' }}</td>
            <td>{{ item.color || '—' }}</td>
            <td>{{ item.size_label || '—' }}</td>
            <td class="attributes">{{ attributeSummary(item) || '—' }}</td>
          </tr>
          <tr v-if="!loading && items.length === 0"><td colspan="7" class="empty">没有匹配的商品。</td></tr>
        </tbody>
      </table>
    </section>

    <footer class="catalog-footer">
      <span>共 {{ total }} 条 · 第 {{ page }} 页</span>
      <div>
        <button type="button" :disabled="loading || page <= 1" @click="previous">上一页</button>
        <button type="button" :disabled="loading || page * pageSize >= total" @click="next">下一页</button>
      </div>
    </footer>
  </main>
</template>

<style scoped>
.catalog-shell{max-width:1100px;margin:0 auto;padding:var(--space-6);color:var(--color-text)}
.catalog-head{display:flex;justify-content:space-between;align-items:flex-start;gap:var(--space-4);padding-bottom:var(--space-4);border-bottom:1px solid var(--color-border)}
.catalog-head h1{margin:0;font-size:var(--text-2xl)}
.eyebrow{margin:0 0 var(--space-1);color:var(--color-text-muted);font-size:var(--text-xs);font-weight:700;letter-spacing:.08em;text-transform:uppercase}
.subtitle{margin:var(--space-2) 0 0;color:var(--color-text-muted);font-size:var(--text-sm)}
.catalog-nav{display:flex;gap:var(--space-3)}
.catalog-nav a{color:var(--color-primary);font-size:var(--text-sm)}
.catalog-filters{display:flex;flex-wrap:wrap;gap:var(--space-3);align-items:end;margin:var(--space-5) 0}
.catalog-filters label{display:grid;gap:var(--space-1);color:var(--color-text-muted);font-size:var(--text-sm)}
.catalog-filters input{min-height:var(--control-height);padding:0 var(--space-2);border:1px solid var(--color-border);border-radius:var(--radius-md);background:var(--color-surface);color:var(--color-text);font:inherit}
.catalog-filters button,.catalog-footer button{min-height:var(--control-height);padding:0 var(--space-4);border:1px solid var(--color-border-strong);border-radius:var(--radius-md);background:var(--color-primary);color:var(--color-on-primary);font:inherit;cursor:pointer}
.catalog-filters button:disabled,.catalog-footer button:disabled{opacity:.5;cursor:not-allowed}
.catalog-notice,.catalog-error{margin:0 0 var(--space-3);font-size:var(--text-sm)}
.catalog-notice{color:var(--color-text-muted)}
.catalog-error{color:var(--color-danger)}
.catalog-table{border:1px solid var(--color-border);border-radius:var(--radius-lg);overflow:auto;background:var(--color-surface)}
.catalog-table table{width:100%;border-collapse:collapse;font-size:var(--text-sm)}
.catalog-table th{height:38px;padding:var(--space-2);background:var(--color-bg-subtle);color:var(--color-text-muted);text-align:left;font-size:var(--text-xs);white-space:nowrap}
.catalog-table td{padding:var(--space-2);border-top:1px solid var(--color-border);vertical-align:top}
.catalog-table td.attributes{color:var(--color-text-muted);overflow-wrap:anywhere}
.catalog-table td.empty{height:90px;text-align:center;color:var(--color-text-muted)}
.catalog-footer{display:flex;justify-content:space-between;align-items:center;margin-top:var(--space-4);color:var(--color-text-muted);font-size:var(--text-sm)}
.catalog-footer div{display:flex;gap:var(--space-2)}
@media (max-width:700px){.catalog-shell{padding:var(--space-3)}.catalog-head{flex-direction:column}.catalog-filters label{width:100%}}
</style>
