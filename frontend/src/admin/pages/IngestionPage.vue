<script setup lang="ts">
import { computed, ref } from 'vue'
import { useAdminContext } from '../admin-context'
import DataTable from '../../components/DataTable.vue'
import Table from '../../components/Table.vue'
import Button from '../../components/Button.vue'
import Badge from '../../components/Badge.vue'
const {
  route, router, session, data, detail, versionHistory, suggestionFilter, generateDate, selectedVersionIds, batchDialog, batchReason, batchBusy, batchResults, batchResultCounts, suggestionProgress, suggestionDetails, trialShops, contentEpoch, jobDetail, loading, unavailable, error, identityStatus, actionBusy, notice, selectedShop, formReason, dialog, dialogTarget, reviewDecision, disclosure, manifestText, selectedFiles, manifestFile, requestRole, requestScope, requestShops, statusFilter, confirmationChecked, page, title, roles, roleCatalog, canAccess, canManage, canReview, canDecideRequests, canObserve, canSeeOperations, canCreateStaff, staffUsername, staffRole, staffReason, staffBusy, staffError, staffPassword, staffCreated, createStaff, copyStaffPassword, shopQuery, roleLabel, requestableRoles, navigation, endpointForPage, clearRestrictedContent, handleFailure, toggleVersion, selectedNeedsRevision, generateSuggestion, generateSelected, openBatchConfirm, submitBatch, loadSuggestionDetail, regenerateStale, loadVersionHistory, loadPage, refreshPermission, go, shopName, formatTime, statusLabel, statusTone, badgeTone, refresh, signout, openDecision, openVersion, closeDialog, decideRequest, submitPermissionRequest, revokeGrant, submitDialogAction, loadReviewDetail, loadJobDetail, openJobAction, runJobOperation, openGrantRevoke, decideVersion, runVersionOperation, importPackage, onManifestChange, onFilesChange, themeChoice, themeLabel
} = useAdminContext()
const search = ref('')
const rows = computed(() => data.value.filter((row) => {
  const q = search.value.trim().toLowerCase()
  return !q || [row.job_id, row.stage, row.status].join(' ').toLowerCase().includes(q)
}))
</script>

<template>
<section class="content-area">
  <DataTable v-model="search" title="资料摄取任务" placeholder="搜索任务 / 状态">
    <template #subtitle><p>任务持久化在 PostgreSQL；worker 重启后依据租约和 fencing epoch 接管。</p></template>
    <template #actions><Button variant="secondary" @click="refresh">刷新</Button></template>
    <Table><thead><tr><th>任务</th><th>阶段</th><th>状态</th><th>资料项</th><th>清单 / 数据摘要</th><th>创建时间</th><th>操作</th></tr></thead><tbody>
      <tr v-for="row in rows" :key="row.job_id" class="click-row" tabindex="0" @click="loadJobDetail(row)" @keydown.enter="loadJobDetail(row)">
        <td>{{ row.job_id }}</td><td>{{ row.stage }}</td><td><Badge :tone="badgeTone(row.status)">{{ statusLabel(row.status) }}</Badge><small v-if="row.error_code || row.error">{{ row.error_code ?? '' }}{{ row.error ? ' · ' + row.error : '' }}</small></td>
        <td>{{ row.item_count }}<small>待 {{ row.pending }} · 完成 {{ row.done }} · 失败 {{ row.failed }}</small></td>
        <td><small class="hash">M {{ row.manifest_sha256 }}</small><small class="hash">D {{ row.dataset_sha256 ?? '解析后生成' }}</small></td>
        <td>{{ formatTime(row.created_at) }}</td><td class="job-actions">
          <Button v-if="canManage && ['pending','running'].includes(row.status ?? '')" variant="text" class="danger-text" @click.stop="openJobAction(row, 'cancel')">取消</Button>
          <Button v-if="canManage && row.status === 'failed'" variant="text" @click.stop="openJobAction(row, 'retry')">重试</Button>
        </td>
      </tr><tr v-if="rows.length === 0"><td colspan="7" class="empty">暂无摄取任务。</td></tr>
    </tbody></Table>
  </DataTable>
  <div v-if="jobDetail" class="job-items">
    <div class="section-head"><div><h2>任务 {{ jobDetail.job_id }} 的资料项</h2><p>{{ statusLabel(jobDetail.status) }} · {{ jobDetail.shop_id }} · fence {{ jobDetail.fencing_epoch }} · {{ jobDetail.cancel_requested ? '取消请求已记录' : '无取消请求' }}</p><p v-if="jobDetail.error_code || jobDetail.error">错误：{{ jobDetail.error_code ?? '—' }} · {{ jobDetail.error ?? '—' }}</p></div>
      <Button v-if="canManage && ['pending','running'].includes(jobDetail.status ?? '')" variant="text" class="danger-text" @click="openJobAction(jobDetail, 'cancel')">取消任务</Button>
      <Button v-if="canManage && jobDetail.status === 'failed'" variant="text" @click="openJobAction(jobDetail, 'retry')">重试任务</Button>
    </div>
    <Table><thead><tr><th>资料</th><th>状态 / 阶段</th><th>版本状态</th><th>Payload hash</th><th>重试 / Fence</th><th>错误代码 / 原因</th></tr></thead><tbody>
      <tr v-for="item in jobDetail.items" :key="item.item_id"><td>{{ item.document_id }}<small>{{ item.item_id }}</small></td><td>{{ statusLabel(item.status) }} / {{ item.stage }}</td><td>{{ statusLabel(item.version_status) }} · {{ statusLabel(item.review_status) }}</td><td class="hash">{{ item.payload_hash }}</td><td>{{ item.retry_count }} / {{ item.fencing_epoch }}</td><td>{{ item.error_code ?? '—' }}<small v-if="item.error">{{ item.error }}</small></td></tr>
      <tr v-if="!jobDetail.items?.length"><td colspan="6" class="empty">任务没有资料项记录。</td></tr>
    </tbody></Table>
  </div>
</section>
</template>
