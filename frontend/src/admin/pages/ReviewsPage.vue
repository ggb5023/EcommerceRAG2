<script setup lang="ts">
import Button from '../../components/Button.vue'
import Table from '../../components/Table.vue'
import KnowledgePreview from '../KnowledgePreview.vue'
import CustomerTrial from '../CustomerTrial.vue'
import {reasonSummary,suggestionEvidence,suggestionLabel} from '../review-suggestion'
import { useAdminContext } from '../admin-context'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
</script>

<template>
<section class="content-area review-layout"><details class="wide"><summary>历史版本（展开查看状态与发布时间）</summary><Button variant="secondary" @click="loadVersionHistory">加载历史版本</Button><Table><thead><tr><th>资料</th><th>版本</th><th>状态</th><th>审核</th><th>发布时间</th></tr></thead><tbody><tr v-for="row in versionHistory" :key="row.version_id"><td>{{row.title??row.document_id}}</td><td>{{row.version_id}}</td><td>{{row.status_label}}</td><td>{{row.review_label}}</td><td>{{row.published_at??'—'}}</td></tr></tbody></Table></details>
  <div class="review-list"><div class="section-head"><div><h2>资料版本</h2><p>审核基于版本哈希和权限版本；AI 建议仅供参考。</p></div></div>
    <div class="form-grid"><label>建议筛选<select v-model="suggestionFilter" @change="loadPage"><option value="">全部</option><option value="needs_revision">需修改</option><option value="approved">可审核</option></select></label><label>业务日期<input v-model="generateDate" type="date"></label><Button variant="secondary" :disabled="!selectedVersionIds.length" @click="generateSelected">批量生成建议</Button><Button variant="primary" :disabled="!selectedVersionIds.length" @click="openBatchConfirm">批量确认（{{selectedVersionIds.length}}）</Button></div>
    <p v-if="suggestionProgress" role="status">生成进度：{{suggestionProgress}}</p>
    <div v-for="row in data" :key="row.version_id" class="version-row" :class="{ selected: detail?.version_id === row.version_id }" role="button" tabindex="0" @click="loadReviewDetail(row)" @keydown.enter="loadReviewDetail(row)">
      <div class="version-title"><input type="checkbox" :checked="selectedVersionIds.includes(String(row.version_id))" @click.stop @change="toggleVersion(String(row.version_id), ($event.target as HTMLInputElement).checked)"><strong>{{ row.title }}</strong><Badge class="status" :tone="badgeTone(row.status)">{{ statusLabel(row.status) }}</Badge></div>
      <small>{{ row.document_id }} · {{ row.shop_id }} · {{ statusLabel(row.review_status) }}</small><small class="hash">{{ row.source_hash }}</small>
      <div class="version-meta"><span>{{ suggestionLabel(row.suggestion?.decision) }}</span><span v-if="row.suggestion">{{ reasonSummary(row.suggestion.reasons) }}</span><span>{{ row.chunk_count ?? 0 }} chunks</span></div>
      <details v-if="row.suggestion" @click.stop @toggle="loadSuggestionDetail(row)"><summary>查看 AI 建议详情</summary>
        <ul><li v-for="reason in row.suggestion.reasons" :key="reason.code">{{ reason.code }}：{{ reason.detail }}</li></ul>
        <div v-for="chunk in suggestionEvidence(suggestionDetails[row.version_id]?.content, row.suggestion.evidence_chunk_ids)" :key="chunk.chunk_id"><small>{{ chunk.chunk_id }}</small><p>{{ chunk.content }}</p></div>
      </details>
      <Button variant="text" @click.stop="generateSuggestion(row)">生成建议</Button>
    </div><p v-if="data.length === 0" class="empty">当前范围没有资料版本。</p></div>
  <div class="review-detail"><template v-if="detail"><div class="detail-head"><div><p class="eyebrow">VERSION {{ detail.version_id }}</p><h2>{{ detail.title }}</h2></div><Badge class="status" :tone="badgeTone(detail.review_status)">{{ statusLabel(detail.review_status) }}</Badge></div><dl class="detail-grid"><div><dt>租户</dt><dd>{{ session.tenant_id }}</dd></div><div><dt>店铺</dt><dd>{{ detail.shop_id }}</dd></div><div><dt>文档</dt><dd>{{ detail.document_id }}</dd></div><div><dt>状态</dt><dd>{{ statusLabel(detail.status) }}</dd></div><div><dt>披露分类</dt><dd>{{ detail.disclosure_class }}</dd></div><div><dt>Fencing epoch</dt><dd>{{ detail.fencing_epoch }}</dd></div><div class="wide"><dt>Source hash</dt><dd class="hash">{{ detail.source_hash }}</dd></div><div class="wide"><dt>Reviewed hash</dt><dd class="hash">{{ detail.reviewed_hash ?? '尚未审核' }}</dd></div></dl>
      <div v-if="detail.suggestion" class="suggestion-block"><h3>AI 建议</h3><p>{{ suggestionLabel(detail.suggestion.decision) }}</p><ul><li v-for="reason in detail.suggestion.reasons" :key="reason.code">{{ reason.code }}：{{ reason.detail }}</li></ul><div v-for="chunk in suggestionEvidence(detail.content, detail.suggestion.evidence_chunk_ids)" :key="chunk.chunk_id"><small>{{ chunk.chunk_id }}</small><p>{{ chunk.content }}</p></div></div>
      <KnowledgePreview :key="detail.version_id" :chunks="detail.content??[]"/>
      <CustomerTrial v-for="shop in trialShops.filter((s: any)=>s.shop_id===detail?.shop_id)" :key="shop.shop_id" :shop="shop"/>
      <div class="high-risk-actions"><Button v-if="canReview && detail.status === 'ready' && detail.review_status === 'pending'" variant="primary" @click="openDecision(detail, 'approved', 'review')">记录审核结论</Button><Button v-if="canReview && detail.status === 'ready' && detail.review_status === 'approved'" variant="primary" @click="openVersion(detail, 'publish')">发布此版本</Button><Button v-if="canReview && detail.status === 'superseded' && detail.review_status === 'approved'" variant="secondary" @click="openVersion(detail, 'rollback')">回退到此版本</Button><Button v-if="canReview && detail.status !== 'revoked'" variant="danger" @click="openVersion(detail, 'revoke')">撤销版本</Button></div>
      <div class="reviewer-line">审核账号：{{ detail.reviewed_by ?? '待审核' }} · 审计结果由服务端返回</div>
    </template><div v-else class="detail-placeholder"><span class="placeholder-mark">▤</span><strong>选择一个版本查看审核信息</strong><small>只有当前审核授权范围内的版本会出现在列表中。</small></div></div>
</section>

</template>
