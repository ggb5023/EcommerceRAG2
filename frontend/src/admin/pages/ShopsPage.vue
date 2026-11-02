<script setup lang="ts">
import AgentReviewSettings from '../AgentReviewSettings.vue'
import CustomerTrial from '../CustomerTrial.vue'
import ShopSettings from '../ShopSettings.vue'
import { useAdminContext } from '../admin-context'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area"><AgentReviewSettings v-if="canManage"/><div class="section-head"><div><h2>当前授权店铺</h2><p>列表内容来自服务端权限摘要和店铺接口。</p></div></div><div class="shop-grid"><article v-for="row in data" :key="row.shop_id" class="shop-row"><span class="shop-symbol">⌂</span><div><strong>{{ row.name }}</strong><small>{{ row.shop_id }}</small></div><span class="status" :class="`tone-${statusTone(row.status === 'active' ? 'healthy' : row.status)}`">{{ statusLabel(row.status) }}</span><CustomerTrial :shop="row"/><ShopSettings v-if="canManage && session" :shop="row" :revision="session.permission_revision" @saved="loadPage"/></article></div></section>

</template>
