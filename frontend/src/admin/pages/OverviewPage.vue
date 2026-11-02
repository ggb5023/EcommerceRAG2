<script setup lang="ts">
import { useAdminContext } from '../admin-context'
import Card from '../../components/Card.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area">
  <div class="overview-meta"><span>只读运行信息</span><span>15 分钟窗口 · {{ formatTime(new Date().toISOString()) }}</span></div>
  <div class="health-grid">
    <Card v-for="(component, name) in (data[0]?.components ?? {})" :key="name" class="health-cell">
      <span>{{ ({ go: 'Go 网关', python: 'Python RAG', postgresql: 'PostgreSQL', worker: '摄取 Worker' } as Record<string, string>)[name] ?? name }}</span>
      <strong :class="`tone-${statusTone(component.status)}`">{{ statusLabel(component.status) }}</strong>
      <small>{{ component.version ?? component.role ?? component.detail ?? '运行状态' }}</small>
    </Card>
  </div>
  <div class="summary-grid">
    <Card><span>迁移版本</span><strong>{{ data[0]?.migration?.current_version ?? '—' }}</strong><small>只读检查</small></Card>
    <Card><span>摄取任务</span><strong>{{ data[0]?.ingestion?.jobs ?? 0 }}</strong><small>待处理 {{ data[0]?.ingestion?.awaiting_or_running ?? 0 }} · 失败 {{ data[0]?.ingestion?.failed ?? 0 }}</small></Card>
    <Card><span>请求错误率</span><strong>{{ ((data[0]?.error_rate?.rate ?? 0) * 100).toFixed(1) }}%</strong><small>{{ data[0]?.error_rate?.errors ?? 0 }} / {{ data[0]?.error_rate?.requests ?? 0 }} 次请求</small></Card>
    <Card><span>应用版本</span><strong>{{ data[0]?.version?.application ?? '—' }}</strong><small>{{ data[0]?.version?.revision ?? 'revision unknown' }}</small></Card>
  </div>
  <p class="scope-note">授权范围：{{ data[0]?.scope?.tenant_id }} / {{ data[0]?.scope?.shop_id }}。历史迁移摘要未验证：{{ data[0]?.migration?.legacy_unverified ?? '—' }}。</p>
</section>

</template>
