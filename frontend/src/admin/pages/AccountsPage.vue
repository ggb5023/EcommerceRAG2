<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAdminContext } from '../admin-context'
import DataTable from '../../components/DataTable.vue'
import Drawer from '../../components/Drawer.vue'
import Table from '../../components/Table.vue'
import Button from '../../components/Button.vue'
import Input from '../../components/Input.vue'
import Badge from '../../components/Badge.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
const search = ref('')
const createOpen = ref(false)
const rows = computed(() => data.value.filter((row) => {
  const q = search.value.trim().toLowerCase()
  return !q || [row.display_name, row.external_id, row.runtime_role].join(' ').toLowerCase().includes(q)
}))
</script>

<template>
<section class="content-area">
  <DataTable v-model="search" title="授权范围内的账号" placeholder="搜索账号 / 角色">
    <template #subtitle><p>撤权状态和权限版本由服务端当前记录决定。</p></template>
    <template #actions><Button v-if="canCreateStaff" variant="primary" @click="createOpen=true">新增员工</Button></template>
    <Table>
      <thead><tr><th>账号</th><th>运行角色</th><th>店铺范围</th><th>管理角色</th><th>权限版本</th><th>状态</th></tr></thead>
      <tbody><tr v-for="row in rows" :key="row.user_id"><td><strong>{{ row.display_name }}</strong><small>{{ row.external_id }} · {{ row.user_id }}</small></td><td>{{ row.runtime_role }}</td><td>{{ (row.shop_ids ?? []).join(', ') || '租户级' }}</td><td><div v-for="grant in row.grants ?? []" :key="grant.id" class="grant-line"><span>{{ roleLabel(grant.role) }} <small>({{ grant.role }})</small> · {{ grant.scope_kind === 'tenant' ? '租户' : grant.shop_ids?.join(', ') }}</span><Button v-if="canAccess && grant.status === 'active'" variant="text" class="danger-text" @click="openGrantRevoke({ ...grant, external_id: row.external_id, display_name: row.display_name })">撤销</Button></div><span v-if="!row.grants?.length">—</span></td><td>{{ row.permission_revision }}</td><td><Badge :tone="badgeTone(row.revoked ? 'failed' : 'active')">{{ row.revoked ? '已撤权' : '有效' }}</Badge></td></tr>
        <tr v-if="rows.length === 0"><td colspan="6" class="empty">当前授权范围没有账号记录。</td></tr></tbody>
    </Table>
  </DataTable>
  <Drawer :open="createOpen" title="新增员工" @close="createOpen=false">
    <form v-if="canCreateStaff" class="side-form" @submit.prevent="createStaff">
      <p>初始密码只在创建成功时显示一次，员工首次登录必须修改密码。</p>
      <label>用户名<Input v-model="staffUsername" required pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,63}" maxlength="64"/></label>
      <label>管理角色<select v-model="staffRole"><option value="merchant_admin">商家管理员</option><option value="knowledge_reviewer">客服权限审核员</option></select></label>
      <label>授权理由<Input v-model="staffReason" required minlength="10" maxlength="2000"/></label>
      <p v-if="staffError" role="alert" class="error">{{ staffError }}</p>
      <Button variant="primary" type="submit" :disabled="staffBusy">{{ staffBusy ? '创建中…' : '创建并生成一次性密码' }}</Button>
      <div v-if="staffPassword" class="notice" role="status"><strong>初始密码（只显示一次）：</strong><code>{{ staffPassword }}</code>
        <Button variant="secondary" @click="copyStaffPassword">复制</Button>
        <small>{{ staffCreated }} 首次登录必须修改密码；关闭后无法再次查看。</small></div>
    </form>
  </Drawer>
</section>
</template>
