<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAdminContext } from '../admin-context'
import DataTable from '../../components/DataTable.vue'
import Table from '../../components/Table.vue'
import Badge from '../../components/Badge.vue'
import Button from '../../components/Button.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
const search = ref('')
const rows = computed(() => data.value.filter((row) => {
  const q = search.value.trim().toLowerCase()
  return !q || [row.action, row.resource_type, row.resource_id, row.actor_user_id, row.result].join(' ').toLowerCase().includes(q)
}))
</script>

<template>
<section class="content-area">
  <DataTable v-model="search" title="追加式授权审计" placeholder="搜索动作 / 资源">
    <template #subtitle><p>按当前权限范围显示的近期管理动作。</p></template>
    <template #actions><Button variant="secondary" @click="refresh">刷新</Button></template>
    <Table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>资源</th><th>版本 / Hash</th><th>原因与结果</th></tr></thead><tbody>
      <tr v-for="row in rows" :key="row.id"><td>{{ formatTime(row.created_at) }}</td><td>{{ row.actor_user_id }}</td><td>{{ row.action }}</td><td>{{ row.resource_type }} · {{ row.resource_id }}</td><td><span>{{ row.resource_version ?? '—' }}</span><small class="hash">{{ row.content_hash ?? '' }}</small></td><td>{{ row.reason ?? '—' }}<small><Badge :tone="badgeTone(row.result)">{{ row.result }}</Badge></small></td></tr>
      <tr v-if="rows.length === 0"><td colspan="6" class="empty">暂无审计记录。</td></tr></tbody></Table>
  </DataTable>
</section>
</template>
