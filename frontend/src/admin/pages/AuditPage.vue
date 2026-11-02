<script setup lang="ts">
import { useAdminContext } from '../admin-context'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area"><div class="section-head"><div><h2>追加式授权审计</h2><p>按当前权限范围显示的近期管理动作。</p></div></div><div class="table-wrap"><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>资源</th><th>版本 / Hash</th><th>原因与结果</th></tr></thead><tbody>
  <tr v-for="row in data" :key="row.id"><td>{{ formatTime(row.created_at) }}</td><td>{{ row.actor_user_id }}</td><td>{{ row.action }}</td><td>{{ row.resource_type }} · {{ row.resource_id }}</td><td><span>{{ row.resource_version ?? '—' }}</span><small class="hash">{{ row.content_hash ?? '' }}</small></td><td>{{ row.reason ?? '—' }}<small><span class="status" :class="`tone-${statusTone(row.result)}`">{{ row.result }}</span></small></td></tr>
  <tr v-if="data.length === 0"><td colspan="6" class="empty">暂无审计记录。</td></tr></tbody></table></div></section>

</template>
