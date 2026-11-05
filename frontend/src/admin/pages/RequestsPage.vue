<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAdminContext } from '../admin-context'
import DataTable from '../../components/DataTable.vue'
import Drawer from '../../components/Drawer.vue'
import Table from '../../components/Table.vue'
import Button from '../../components/Button.vue'
import Badge from '../../components/Badge.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
const search = ref('')
const requestOpen = ref(false)
const rows = computed(() => data.value.filter((row) => {
  const q = search.value.trim().toLowerCase()
  return !q || [row.requester_name, row.role, row.reason, row.status].join(' ').toLowerCase().includes(q)
}))
</script>

<template>
<section class="content-area split-layout">
  <DataTable v-model="search" title="权限申请队列" placeholder="搜索申请人 / 理由">
    <template #subtitle><p>审批决定不会自动扩大当前授权的 tenant/shop 范围。</p></template>
    <template #actions><Button v-if="session" variant="primary" @click="requestOpen=true">提交权限申请</Button><select v-model="statusFilter" aria-label="申请状态"><option value="">全部状态</option><option value="pending">待处理</option><option value="approved">已批准</option><option value="needs_revision">待补正</option><option value="rejected">已拒绝</option></select></template>
    <Table><thead><tr><th>申请人 / 角色</th><th>范围</th><th>理由</th><th>状态</th><th>操作</th></tr></thead><tbody>
      <tr v-for="row in rows" :key="row.id"><td><strong>{{ row.requester_name }}</strong><small>{{ row.role }} · {{ row.id }}</small></td><td>{{ row.scope_kind === 'tenant' ? '租户' : (row.shop_ids ?? []).join(', ') }}</td><td class="reason-cell">{{ row.reason }}</td><td><Badge :tone="badgeTone(row.status)">{{ statusLabel(row.status) }}</Badge></td><td><Button v-if="canDecideRequests && row.status === 'pending'" variant="text" @click="openDecision(row, 'approved')">处理</Button></td></tr>
      <tr v-if="rows.length === 0"><td colspan="5" class="empty">没有符合筛选条件的申请。</td></tr></tbody></Table>
  </DataTable>
  <Drawer :open="requestOpen" title="提交权限申请" @close="requestOpen=false">
    <form v-if="session" class="side-form" @submit.prevent="submitPermissionRequest">
      <label>申请角色<select v-model="requestRole"><option v-for="role in requestableRoles" :key="role.role_key" :value="role.storage_role ?? ''">{{ role.display_name }}（{{ role.role_key }}）</option></select></label>
      <small v-if="requestableRoles.length === 0" class="form-warning">当前角色目录没有开放可申请的权限。</small>
      <label>授权范围<select v-model="requestScope"><option value="shops">指定店铺</option><option value="tenant">当前租户</option></select></label>
      <fieldset v-if="requestScope === 'shops'"><legend>选择店铺</legend><label v-for="shop in session.allowed_shop_ids" :key="shop" class="check-row"><input v-model="requestShops" type="checkbox" :value="shop">{{ shop }}</label></fieldset>
      <label>申请理由<textarea v-model="formReason" minlength="10" maxlength="2000" rows="4" required /></label>
      <Button variant="primary" type="submit" :disabled="actionBusy || requestableRoles.length === 0">提交申请</Button>
    </form>
  </Drawer>
</section>
</template>
