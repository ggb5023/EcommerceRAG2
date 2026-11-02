<script setup lang="ts">
import CustomerTrial from '../CustomerTrial.vue'
import KnowledgeImport from '../KnowledgeImport.vue'
import { useAdminContext } from '../admin-context'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area">
  <div class="section-head"><div><h2>已登记资料来源</h2><p>只显示来源元数据，不在此页面展开资料正文。</p></div></div>
  <div class="table-wrap"><table><thead><tr><th>来源</th><th>店铺</th><th>类型</th><th>资料数</th><th>最近导入</th></tr></thead><tbody><tr v-for="row in data" :key="row.source_id"><td><strong>{{ row.source_id }}</strong><small>{{ row.uri ?? 'manifest package' }}</small></td><td>{{ row.shop_id }}</td><td>{{ row.type }}</td><td>{{ row.documents }}</td><td>{{ formatTime(row.latest_import) }}</td></tr><tr v-if="data.length === 0"><td colspan="5" class="empty">尚无管理台导入的资料来源。</td></tr></tbody></table></div>
  <div v-for="shop in trialShops" :key="shop.shop_id"><strong>{{shop.name}}</strong><CustomerTrial :shop="shop"/></div>
  <KnowledgeImport v-if="canManage" :shops="session?.allowed_shop_ids ?? []" @imported="notice='资料已提交；请重新登录后查看摄取任务并审核发布。'"/>
  <form v-if="canManage" class="import-form" @submit.prevent="importPackage"><div class="section-head"><div><h2>导入 YAML 资料包</h2><p>文件只进入当前授权店铺；新版本保持待审核，不会直接发布。</p></div><span class="env-mark">{{ session?.is_mock ? '合成环境' : '受限环境' }}</span></div>
    <div class="form-grid"><label>目标店铺<select v-model="selectedShop" required><option v-for="shop in session?.allowed_shop_ids ?? []" :key="shop" :value="shop">{{ shop }}</option></select></label><label>manifest.yaml<input type="file" accept=".yaml,.yml" required @change="onManifestChange"></label><label class="wide">资料文件<input type="file" multiple required @change="onFilesChange"><small>manifest 中的相对路径必须与文件名对应；单个文件上限由服务端执行。</small></label><label class="wide">YAML manifest<textarea v-model="manifestText" rows="10" spellcheck="false" required /></label></div>
    <div class="form-footer"><span>{{ selectedFiles.length }} 个文件已选择</span><button class="primary" :disabled="actionBusy || !manifestText || selectedFiles.length === 0">校验并排队</button></div>
  </form>
</section>

</template>
