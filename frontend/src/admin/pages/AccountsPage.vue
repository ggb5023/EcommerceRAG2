<script setup lang="ts">
import { useAdminContext } from '../admin-context'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area">
  <div class="section-head"><div><h2>授权范围内的账号</h2><p>撤权状态和权限版本由服务端当前记录决定。</p></div></div>
  <form v-if="canCreateStaff" class="side-form" @submit.prevent="createStaff">
    <h2>新增员工</h2>
    <p>初始密码只在创建成功时显示一次，员工首次登录必须修改密码。</p>
    <label>用户名<input v-model="staffUsername" required pattern="[A-Za-z0-9][A-Za-z0-9._-]{2,63}" maxlength="64"></label>
    <label>管理角色<select v-model="staffRole"><option value="merchant_admin">商家管理员</option><option value="knowledge_reviewer">客服权限审核员</option></select></label>
    <label>授权理由<input v-model="staffReason" required minlength="10" maxlength="2000"></label>
    <p v-if="staffError" role="alert" class="error">{{ staffError }}</p>
    <button class="primary" type="submit" :disabled="staffBusy">{{ staffBusy ? '创建中…' : '创建并生成一次性密码' }}</button>
    <div v-if="staffPassword" class="notice" role="status"><strong>初始密码（只显示一次）：</strong><code>{{ staffPassword }}</code>
      <button type="button" class="secondary" @click="copyStaffPassword">复制</button>
      <small>{{ staffCreated }} 首次登录必须修改密码；关闭后无法再次查看。</small></div>
  </form>
  <div class="table-wrap"><table><thead><tr><th>账号</th><th>运行角色</th><th>店铺范围</th><th>管理角色</th><th>权限版本</th><th>状态</th></tr></thead>
    <tbody><tr v-for="row in data" :key="row.user_id"><td><strong>{{ row.display_name }}</strong><small>{{ row.external_id }} · {{ row.user_id }}</small></td><td>{{ row.runtime_role }}</td><td>{{ (row.shop_ids ?? []).join(', ') || '租户级' }}</td><td><div v-for="grant in row.grants ?? []" :key="grant.id" class="grant-line"><span>{{ roleLabel(grant.role) }} <small>({{ grant.role }})</small> · {{ grant.scope_kind === 'tenant' ? '租户' : grant.shop_ids.join(', ') }}</span><button v-if="canAccess && grant.status === 'active'" class="text-action danger-text" @click="openGrantRevoke({ ...grant, external_id: row.external_id, display_name: row.display_name })">撤销</button></div><span v-if="!row.grants?.length">—</span></td><td>{{ row.permission_revision }}</td><td><span class="status" :class="`tone-${row.revoked ? 'bad' : 'good'}`">{{ row.revoked ? '已撤权' : '有效' }}</span></td></tr>
      <tr v-if="data.length === 0"><td colspan="6" class="empty">当前授权范围没有账号记录。</td></tr></tbody></table></div>
</section>

</template>
