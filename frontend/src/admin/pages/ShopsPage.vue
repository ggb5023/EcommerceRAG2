<script setup lang="ts">
import AgentReviewSettings from '../AgentReviewSettings.vue'
import CustomerTrial from '../CustomerTrial.vue'
import ShopSettings from '../ShopSettings.vue'
import { computed, ref } from 'vue'
import { useAdminContext, type Shop } from '../admin-context'
import DataTable from '../../components/DataTable.vue'
import Drawer from '../../components/Drawer.vue'
import Card from '../../components/Card.vue'
import Badge from '../../components/Badge.vue'
import Button from '../../components/Button.vue'
import Empty from '../../components/Empty.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
const search = ref('')
const settingsShop = ref<Shop | null>(null)
function onSettingsSaved() { settingsShop.value = null; loadPage() }
const rows = computed(() => data.value.filter((row) => {
  const q = search.value.trim().toLowerCase()
  return !q || [row.name, row.shop_id].join(' ').toLowerCase().includes(q)
}))
</script>

<template>
<section class="content-area">
  <DataTable v-model="search" title="当前授权店铺" placeholder="搜索店铺">
    <template #subtitle><p>列表内容来自服务端权限摘要和店铺接口。</p></template>
    <AgentReviewSettings v-if="canManage"/>
    <div class="shop-grid"><Card v-for="row in rows" :key="row.shop_id" class="shop-row"><span class="shop-symbol">⌂</span><div><strong>{{ row.name }}</strong><small>{{ row.shop_id }}</small></div><Badge :tone="badgeTone(row.status === 'active' ? 'healthy' : row.status)">{{ statusLabel(row.status) }}</Badge><CustomerTrial :shop="row"/><Button v-if="canManage && session" variant="text" @click="settingsShop = row">店铺设置</Button></Card></div>
    <Empty v-if="rows.length === 0" title="没有店铺">当前授权范围没有店铺。</Empty>
  </DataTable>
  <Drawer :open="!!settingsShop" title="店铺设置" @close="settingsShop = null">
    <ShopSettings v-if="settingsShop && session" :shop="settingsShop" :revision="session.permission_revision" @saved="onSettingsSaved"/>
  </Drawer>
</section>
</template>
