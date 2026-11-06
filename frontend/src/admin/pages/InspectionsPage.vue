<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { getInspections, runInspections, setInspectionStatus, type InspectionItem, type InspectionRunSummary } from '../api'
import { useAdminContext } from '../admin-context'
import InspectionsView from '../InspectionsView.vue'
import Button from '../../components/Button.vue'

const { selectedShop } = useAdminContext()
const items = ref<InspectionItem[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const runSummary = ref<InspectionRunSummary | null>(null)
const shopId = computed(() => selectedShop.value || '')
const summaryText = computed(() => {
  if (!runSummary.value) return ''
  const totals = Object.values(runSummary.value.by_kind ?? {}).reduce(
    (sum, row) => ({ inserted: sum.inserted + row.inserted, updated: sum.updated + row.updated, closed: sum.closed + row.closed }),
    { inserted: 0, updated: 0, closed: 0 },
  )
  return `新增 ${totals.inserted}，更新 ${totals.updated}，关闭 ${totals.closed}`
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = (await getInspections('open', shopId.value)).items
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '巡检结果加载失败。'
  } finally {
    loading.value = false
  }
}

async function runNow() {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    runSummary.value = await runInspections(shopId.value)
    notice.value = `巡检完成：${summaryText.value}`
    await load()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '巡检执行失败。'
  } finally {
    busy.value = false
  }
}

async function act(item: InspectionItem, status: 'acknowledged' | 'resolved' | 'dismissed') {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await setInspectionStatus(item.id, status, shopId.value)
    notice.value = status === 'acknowledged' ? '已标记确认。' : status === 'resolved' ? '已标记处理完成。' : '已忽略该巡检项。'
    await load()
  } catch (caught) {
    error.value = caught instanceof Error ? caught.message : '巡检项状态更新失败。'
  } finally {
    busy.value = false
  }
}

watch(shopId, () => { void load() })
onMounted(load)
</script>

<template>
  <section class="content-area inspections-page">
    <div class="section-head">
      <div><h2>内容巡检</h2><p>巡检只发现与提示，不改变任何资料的发布状态。</p></div>
      <Button variant="primary" :disabled="busy" data-testid="inspection-run" @click="runNow">{{ busy ? '巡检中…' : '立即巡检' }}</Button>
    </div>
    <p v-if="error" class="alert" role="alert">{{ error }}</p>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <InspectionsView :items="items" :loading="loading" :busy="busy" @action="act" />
  </section>
</template>
