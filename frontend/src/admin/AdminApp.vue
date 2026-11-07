<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import KnowledgeImport from './KnowledgeImport.vue'
import OutcomesPanel from './OutcomesPanel.vue'
import GapsPanel from './GapsPanel.vue'
import ShopSettings from './ShopSettings.vue'
import AgentReviewSettings from './AgentReviewSettings.vue'
import {versionHistoryView} from './version-history'
import { groupedNavigation } from './navigation'
import {batchCounts,reasonSummary,suggestionEvidence,suggestionLabel,validBatchReason} from './review-suggestion'
import KnowledgePreview from './KnowledgePreview.vue'
import CustomerTrial from './CustomerTrial.vue'
import Badge from '../components/Badge.vue'
import Button from '../components/Button.vue'
import Table from '../components/Table.vue'
import type {PreviewChunk} from './knowledge-preview'
import { ApiError, get, post, request, type AdminSession } from './api'
import { adminPageFromPath, adminPathForPage, type AdminPage } from './routes'
import { browserAuth, checkBrowserAuth, login, logout } from '../auth'
import type { BatchItemResult } from './review-suggestion'
import type { VersionHistoryRow } from './version-history'
import { applyThemeChoice, loadThemeChoice, nextThemeChoice, themeLabel, type ThemeChoice } from './theme'
import { AdminContextKey, type AdminContext, type AdminDetail, type AdminRow } from './admin-context'
import AdminShell from './AdminShell.vue'
import AdminPageRouter from './AdminPageRouter.vue'
import AdminDialogs from './AdminDialogs.vue'

type Row = AdminRow
type ItemResponse = { items: VersionHistoryRow[] }
type Detail = AdminDetail

const route = useRoute()
const router = useRouter()
const session = ref<AdminSession | null>(null)
const data = ref<Row[]>([])
const detail = ref<Detail | null>(null)
const versionHistory = ref<ReturnType<typeof versionHistoryView>>([])
const suggestionFilter = ref('')
const generateDate = ref(new Date().toISOString().slice(0,10))
const selectedVersionIds = ref<string[]>([])
const batchDialog = ref(false)
const batchReason = ref('')
const batchBusy = ref(false)
const batchResults = ref<BatchItemResult[]>([])
const batchResultCounts = ref({success:0,stale:0,failed:0})
const suggestionProgress = ref('')
const suggestionDetails = ref<Record<string, Detail>>({})
const trialShops = ref<Row[]>([])
let contentEpoch=0
let permissionPoll:ReturnType<typeof setInterval>
const jobDetail = ref<Row | null>(null)
const loading = ref(true)
const unavailable = ref(false)
const error = ref('')
const identityStatus = ref(0)
const actionBusy = ref(false)
const notice = ref('')
const selectedShop = ref('')
const formReason = ref('')
const dialog = ref<'request' | 'review' | 'version' | 'grant_revoke' | 'job' | null>(null)
const dialogTarget = ref<Row | null>(null)
const reviewDecision = ref('approved')
const disclosure = ref('external_allowed')
const manifestText = ref('')
const selectedFiles = ref<File[]>([])
const manifestFile = ref<File | null>(null)
const requestRole = ref('merchant_admin')
const requestScope = ref('shops')
const requestShops = ref<string[]>([])
const statusFilter = ref('')
const confirmationChecked = ref(false)

const themeChoice = ref<ThemeChoice>(loadThemeChoice())
const page = computed(() => adminPageFromPath(route.path))
const title = computed(() => ({
  overview: '运行状态', outcomes: '结果度量', reports: '报告', report: '评测报告', intel: '情报观察', gaps: '内容缺口', inspections: '内容巡检', accounts: '账号与权限', requests: '权限申请', audit: '授权审计',
  shops: '店铺范围', sources: '资料来源', ingestion: '摄取任务', reviews: '版本审核', answers: '回答与引用',
  'not-found': '页面不可用',
}[page.value]))
const roles = computed(() => new Set(session.value?.admin_roles ?? []))
const roleCatalog = computed(() => session.value?.admin_role_catalog ?? [])
const canAccess = computed(() => roles.value.has('access_admin'))
const canManage = computed(() => roles.value.has('merchant_admin'))
const canReview = computed(() => roles.value.has('knowledge_reviewer'))
const canDecideRequests = computed(() => canAccess.value || canManage.value)
const canObserve = computed(() => roles.value.has('platform_observer'))
const canSeeOperations = computed(() => canManage.value || canReview.value || canObserve.value)
const canCreateStaff = computed(() => canManage.value)
const staffUsername = ref('')
const staffRole = ref('merchant_admin')
const staffReason = ref('')
const staffBusy = ref(false)
const staffError = ref('')
const staffPassword = ref('')
const staffCreated = ref('')
async function createStaff() {
  staffBusy.value = true; staffError.value = ''; staffPassword.value = ''; staffCreated.value = ''
  try {
    const body: Record<string, unknown> = { username: staffUsername.value.trim(), role: staffRole.value, reason: staffReason.value.trim() }
    if (selectedShop.value) body.shop_ids = [selectedShop.value]
    const created = await post('/admin/v1/staff', body) as { username: string; initial_password: string }
    staffCreated.value = created.username
    staffPassword.value = created.initial_password
    staffUsername.value = ''; staffReason.value = ''
    await loadPage()
  } catch (error) {
    staffError.value = error instanceof Error ? error.message : '创建失败，请稍后再试。'
  } finally {
    staffBusy.value = false
  }
}
async function copyStaffPassword() {
  try { await navigator.clipboard.writeText(staffPassword.value) } catch { /* copy is optional */ }
}
const shopQuery = computed(() => selectedShop.value ? `?shop_id=${encodeURIComponent(selectedShop.value)}` : '')

function roleLabel(role: string) {
  return roleCatalog.value.find(item => item.storage_role === role || item.role_key === role)?.display_name ?? role
}

const requestableRoles = computed(() => roleCatalog.value.filter(item => item.enabled && item.requestable && item.storage_role))

const navigationGroups = computed(() => groupedNavigation({
  access: canAccess.value, manage: canManage.value, review: canReview.value, observe: canObserve.value,
}))
const navigation = computed(() => navigationGroups.value.flatMap(group => group.items))

function endpointForPage() {
  const q = shopQuery.value
  switch (page.value) {
    case 'overview': return `/admin/v1/overview${q}`
    case 'accounts': return `/admin/v1/access/accounts${q}`
    case 'requests': return `/admin/v1/access/requests${statusFilter.value ? `${q ? '&' : '?'}status=${statusFilter.value}` : q}`
    case 'audit': return `/admin/v1/access/audit${q}`
    case 'shops': return `/admin/v1/merchant/shops${q}`
    case 'sources': return `/admin/v1/merchant/sources${q}`
    case 'ingestion': return `/admin/v1/merchant/ingestion${q}`
    case 'reviews': return `/admin/v1/merchant/reviews${q}${suggestionFilter.value?`${q?'&':'?'}suggestion=${encodeURIComponent(suggestionFilter.value)}`:''}`
    default: return `/admin/v1/overview${q}`
  }
}

function clearRestrictedContent(clearSession = true) {
  contentEpoch++
  trialShops.value = []
  data.value = []
  detail.value = null
  jobDetail.value = null
  manifestText.value = ''
  manifestFile.value = null
  selectedFiles.value = []
  formReason.value = ''
  requestShops.value = []
  dialog.value = null
  dialogTarget.value = null
  confirmationChecked.value = false
  if (clearSession) session.value = null
}

function handleFailure(caught: unknown, fallback: string) {
  const apiError = caught instanceof ApiError ? caught : new ApiError((caught as { status?: number })?.status ?? 0, caught instanceof Error ? caught.message : fallback)
  identityStatus.value = apiError.status
  error.value = apiError.message
  notice.value = ''
  if (apiError.status === 401 || apiError.status === 403) {
    clearRestrictedContent(true)
    unavailable.value = false
  } else if (apiError.status >= 500 || apiError.status === 0) {
    clearRestrictedContent(false)
    unavailable.value = true
  }
}

function toggleVersion(versionId: string, checked: boolean) {
  selectedVersionIds.value = checked ? [...new Set([...selectedVersionIds.value, versionId])] : selectedVersionIds.value.filter(id => id !== versionId)
}
function selectedNeedsRevision() {
  return data.value.filter(row => selectedVersionIds.value.includes(String(row.version_id)) && row.suggestion?.decision === 'needs_revision').length
}
async function generateSuggestion(row: Row) {
  try {
    await post(`/admin/v1/merchant/versions/${row.version_id}/suggestion`, { business_date: generateDate.value })
    await loadPage()
  } catch (error) {
    notice.value = error instanceof Error ? error.message : '建议生成失败。'
  }
}
async function generateSelected() {
  const rows = data.value.filter(row => selectedVersionIds.value.includes(String(row.version_id)))
  if (!rows.length) return
  suggestionProgress.value = `0/${rows.length}`
  for (let index = 0; index < rows.length; index += 1) {
    try { await post(`/admin/v1/merchant/versions/${rows[index].version_id}/suggestion`, { business_date: generateDate.value }) }
    catch { /* per-row failures stay visible via the refreshed list */ }
    suggestionProgress.value = `${index + 1}/${rows.length}`
  }
  await loadPage()
}
function openBatchConfirm() {
  if (!selectedVersionIds.value.length) return
  batchResults.value = []
  batchDialog.value = true
}
async function submitBatch() {
  if (!validBatchReason(batchReason.value)) { notice.value = '确认理由需为 10–2000 字。'; return }
  const items = data.value.filter(row => selectedVersionIds.value.includes(String(row.version_id)) && row.suggestion?.id)
    .map(row => ({ version_id: String(row.version_id), suggestion_id: String(row.suggestion?.id ?? '') }))
  if (!items.length) { notice.value = '选中的版本没有可用的建议。'; return }
  batchBusy.value = true
  try {
    const result = await post<{ results?: BatchItemResult[] }>('/admin/v1/merchant/versions/batch-approve', { items, confirm: true, reason: batchReason.value.trim() })
    batchResults.value = result.results ?? []
    batchResultCounts.value = batchCounts(batchResults.value)
    batchDialog.value = false
    batchReason.value = ''
    selectedVersionIds.value = []
    await loadPage()
  } catch (error) {
    notice.value = error instanceof Error ? error.message : '批量确认失败。'
  } finally { batchBusy.value = false }
}
async function loadSuggestionDetail(row: Row) {
  if (suggestionDetails.value[String(row.version_id)]) return
  try { suggestionDetails.value[String(row.version_id)] = await get(`/admin/v1/merchant/reviews/${row.version_id}`) }
  catch { /* detail remains collapsed */ }
}
async function regenerateStale(result: BatchItemResult) {
  const row = data.value.find(item => String(item.version_id) === String(result.version_id))
  if (row) await generateSuggestion(row)
}

async function loadVersionHistory() {
  try {
    const result = await get<ItemResponse>('/admin/v1/merchant/versions')
    versionHistory.value = versionHistoryView(result.items ?? [])
  } catch (error) {
    notice.value = error instanceof Error ? error.message : '版本历史加载失败。'
  }
}

async function loadPage() {
  contentEpoch++
  loading.value = true
  unavailable.value = false
  error.value = ''
  notice.value = ''
  detail.value = null
  try {
    await checkBrowserAuth()
    if (browserAuth.mustChangePassword) { void router.replace('/password'); return }
    if (browserAuth.enabled && !browserAuth.authenticated) { clearRestrictedContent(); identityStatus.value = 401; return }
    identityStatus.value = 0
    if (!session.value) session.value = await get<AdminSession>('/admin/v1/session')
    if (page.value === 'outcomes' || page.value === 'reports' || page.value === 'report' || page.value === 'intel' || page.value === 'gaps' || page.value === 'inspections') { data.value = []; return }
    // The account list needs the platform access role; merchant admins only use
    // this page to create staff (POST /admin/v1/staff) and must not 403 here.
    if (page.value === 'accounts' && !canAccess.value) { data.value = []; return }
    const response = await get<ItemResponse | Row>(endpointForPage())
    data.value = page.value === 'overview'
      ? [response as Row]
      : 'items' in response && Array.isArray(response.items) ? response.items : []
    if (['sources','reviews'].includes(page.value)) trialShops.value=(await get<ItemResponse>('/admin/v1/merchant/shops')).items
    if (page.value === 'shops') {
      const allowed = data.value.map(item => String(item.shop_id))
      if (!selectedShop.value || !allowed.includes(selectedShop.value)) selectedShop.value = session.value.shop_id || allowed[0] || ''
      requestShops.value = requestShops.value.filter(shop => allowed.includes(shop))
    }
  } catch (caught) {
    handleFailure(caught, '服务连接失败，请检查管理服务。')
  } finally {
    loading.value = false
  }
}

watch(() => [route.fullPath, selectedShop.value, statusFilter.value], () => { if (session.value || !loading.value) void loadPage() })
onMounted(() => { applyThemeChoice(themeChoice.value); void loadPage(); permissionPoll=setInterval(()=>void refreshPermission(),15000) })
function toggleTheme(){ themeChoice.value=nextThemeChoice(themeChoice.value); applyThemeChoice(themeChoice.value) }
onUnmounted(()=>clearInterval(permissionPoll))
async function refreshPermission(){
 if(!session.value||!detail.value)return
 try{const current=await get<AdminSession>('/admin/v1/session');if(current.permission_revision!==session.value.permission_revision||!current.admin_roles.includes('knowledge_reviewer')){clearRestrictedContent();await loadPage()}}
 catch(caught){handleFailure(caught,'审核权限无法确认，已清除预览。')}
}

function go(target: AdminPage) { void router.push(adminPathForPage(target)) }
function shopName(id: string) { return data.value.find(item => item.shop_id === id)?.name ?? id }
function formatTime(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
function statusLabel(value?: string) {
  const labels: Record<string, string> = { healthy: '正常', unavailable: '不可用', unknown: '未知', not_configured: '未配置',
    pending: '待处理', queued: '排队中', parsing: '解析中', building: '构建中', retry_wait: '等待重试',
    running: '运行中', awaiting_review: '待审核', done: '完成', failed: '失败', cancelled: '已取消', cancel_requested: '正在取消',
    ready: '待发布', active: '当前版本', superseded: '已替换', revoked: '已撤销', approved: '已批准', rejected: '已拒绝', needs_revision: '待补正' }
  return labels[value ?? ''] ?? value ?? '—'
}
function statusTone(value?: string) {
  if (['healthy', 'done', 'active', 'approved', 'success'].includes(value ?? '')) return 'good'
  if (['failed', 'unavailable', 'rejected', 'revoked', 'denied'].includes(value ?? '')) return 'bad'
  if (['unknown', 'not_configured'].includes(value ?? '')) return 'muted'
  return 'pending'
}
function badgeTone(value?: string) {
  const tone = statusTone(value)
  return tone === 'good' ? 'success' : tone === 'bad' ? 'danger' : tone === 'pending' ? 'warning' : 'muted'
}

async function refresh() { await loadPage() }
async function signout() {
  clearRestrictedContent(); identityStatus.value = 401; notice.value = ''; error.value = ''; unavailable.value = false
  try { await logout() } catch (caught) { error.value = caught instanceof Error ? caught.message : '注销未确认。' }
}
function openDecision(row: Row, decision = 'approved', kind: 'request' | 'review' = 'request') {
  dialogTarget.value = row
  reviewDecision.value = decision
  formReason.value = ''
  confirmationChecked.value = false
  dialog.value = kind
}
function openVersion(row: Row, operation: 'publish' | 'rollback' | 'revoke') {
  dialogTarget.value = { ...row, operation }
  formReason.value = ''
  confirmationChecked.value = false
  dialog.value = 'version'
}
function closeDialog() { dialog.value = null; dialogTarget.value = null; confirmationChecked.value = false }

async function decideRequest() {
  if (!dialogTarget.value || formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    await post(`/admin/v1/access/requests/${dialogTarget.value.id}/decision`, {
      confirm: true, decision: reviewDecision.value, reason: formReason.value.trim(),
    })
    notice.value = '权限申请已处理，审计结果已记录。'
    closeDialog(); await loadPage()
  } catch (caught) { handleFailure(caught, '权限申请处理失败。') }
  finally { actionBusy.value = false }
}

async function submitPermissionRequest() {
  if (formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    await post('/admin/v1/access/requests', { role: requestRole.value, scope_kind: requestScope.value,
      shop_ids: requestScope.value === 'tenant' ? [] : requestShops.value, reason: formReason.value.trim() })
    notice.value = '权限申请已提交。'
    formReason.value = ''; await loadPage()
  } catch (caught) { handleFailure(caught, '权限申请提交失败。') }
  finally { actionBusy.value = false }
}

async function revokeGrant(row: Row) {
  if (formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    await post(`/admin/v1/access/grants/${row.id}/revoke`, { confirm: true, reason: formReason.value.trim() })
    notice.value = '授权已撤销，审计结果已记录。'; closeDialog(); await loadPage()
  } catch (caught) { handleFailure(caught, '授权撤销失败。') }
  finally { actionBusy.value = false }
}

function submitDialogAction() {
  const target = dialogTarget.value
  if (!dialog.value || !target || !confirmationChecked.value) return
  if (dialog.value === 'request') void decideRequest()
  else if (dialog.value === 'review') void decideVersion()
  else if (dialog.value === 'grant_revoke') void revokeGrant(target)
  else if (dialog.value === 'job') void runJobOperation()
  else void runVersionOperation()
}

async function loadReviewDetail(row: Row) {
  error.value = ''
  detail.value = null
  const epoch=++contentEpoch
  try { const result=await get<Detail>(`/admin/v1/merchant/reviews/${row.version_id}${shopQuery.value}`); if(epoch===contentEpoch&&canReview.value)detail.value=result }
  catch (caught) { handleFailure(caught, '审核资料不可用。') }
}

async function loadJobDetail(row: Row) {
  jobDetail.value = null
  error.value = ''
  try { jobDetail.value = await get<Row>(`/admin/v1/merchant/ingestion/${row.job_id}${shopQuery.value}`) }
  catch (caught) { handleFailure(caught, '摄取任务明细不可用。') }
}

function openJobAction(row: Row, operation: 'cancel' | 'retry') {
  dialogTarget.value = { ...row, operation }
  formReason.value = ''
  confirmationChecked.value = false
  dialog.value = 'job'
}

async function runJobOperation() {
  const row = dialogTarget.value
  if (!row || formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    const body = { confirm: true, tenant_id: session.value?.tenant_id, shop_id: row.shop_id,
      job_id: row.job_id, permission_revision: session.value?.permission_revision,
      expected_fencing_epoch: row.fencing_epoch, reason: formReason.value.trim() }
    await post(`/admin/v1/merchant/ingestion/${row.job_id}/${row.operation}`, body)
    notice.value = row.operation === 'cancel' ? '取消请求已记录，worker 正在处理终态。' : '重试任务已重新排队。'
    closeDialog(); await loadPage()
    const refreshed = data.value.find(item => item.job_id === row.job_id)
    if (refreshed) await loadJobDetail(refreshed)
  } catch (caught) { handleFailure(caught, '任务操作失败，状态未确认。') }
  finally { actionBusy.value = false }
}

function openGrantRevoke(row: Row) {
  dialogTarget.value = row
  formReason.value = ''
  confirmationChecked.value = false
  dialog.value = 'grant_revoke'
}

async function decideVersion() {
  const row = dialogTarget.value
  if (!row || formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    await post(`/admin/v1/merchant/versions/${row.version_id}/review`, {
      confirm: true, decision: reviewDecision.value, disclosure_class: disclosure.value,
      external_allowed: disclosure.value === 'external_allowed', expected_hash: row.source_hash,
      expected_fencing_epoch: row.fencing_epoch, permission_revision: session.value?.permission_revision,
      reason: formReason.value.trim(),
    })
    notice.value = '审核结论已记录。'
    closeDialog(); detail.value = null; await loadPage()
  } catch (caught) { handleFailure(caught, '审核结果未提交。') }
  finally { actionBusy.value = false }
}

async function runVersionOperation() {
  const row = dialogTarget.value
  if (!row || formReason.value.trim().length < 10) return
  actionBusy.value = true
  try {
    const body = { confirm: true, tenant_id: session.value?.tenant_id, shop_id: row.shop_id,
      document_id: row.document_id, version_hash: row.source_hash,
      permission_revision: session.value?.permission_revision,
      expected_fencing_epoch: row.fencing_epoch, reason: formReason.value.trim() }
    await post(`/admin/v1/merchant/versions/${row.version_id}/${row.operation}`, body)
    notice.value = `版本${row.operation === 'publish' ? '已发布' : row.operation === 'rollback' ? '已回退' : '已撤销'}，索引和审计状态已刷新。`
    closeDialog(); detail.value = null; await loadPage()
  } catch (caught) { handleFailure(caught, '版本操作失败，状态未确认。') }
  finally { actionBusy.value = false }
}

async function importPackage() {
  if (!manifestText.value.trim() || !manifestFile.value || selectedFiles.value.length === 0 || !selectedShop.value) return
  const form = new FormData()
  form.set('shop_id', selectedShop.value)
  form.set('manifest', manifestText.value)
  for (const file of selectedFiles.value) {
    const relative = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
    form.append('paths', relative)
    form.append('files', file, file.name)
  }
  actionBusy.value = true
  try {
    const result = await request<Row>('/admin/v1/merchant/sources/import', { method: 'POST', body: form })
    notice.value = `摄取任务已排队：${result.item_count} 个资料项，状态 ${statusLabel(result.status)}。`
    manifestText.value = ''; manifestFile.value = null; selectedFiles.value = []
    await loadPage()
  } catch (caught) { handleFailure(caught, '资料导入失败。') }
  finally { actionBusy.value = false }
}

function onManifestChange(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0] ?? null
  manifestFile.value = file
  if (file) void file.text().then(value => { manifestText.value = value })
}
function onFilesChange(event: Event) { selectedFiles.value = Array.from((event.target as HTMLInputElement).files ?? []) }
const admin = {
  route,
  router,
  session,
  data,
  detail,
  versionHistory,
  suggestionFilter,
  generateDate,
  selectedVersionIds,
  batchDialog,
  batchReason,
  batchBusy,
  batchResults,
  batchResultCounts,
  validBatchReason,
  suggestionProgress,
  suggestionDetails,
  trialShops,
  contentEpoch,
  jobDetail,
  loading,
  unavailable,
  error,
  identityStatus,
  actionBusy,
  notice,
  selectedShop,
  formReason,
  dialog,
  dialogTarget,
  reviewDecision,
  disclosure,
  manifestText,
  selectedFiles,
  manifestFile,
  requestRole,
  requestScope,
  requestShops,
  statusFilter,
  confirmationChecked,
  page,
  title,
  roles,
  roleCatalog,
  canAccess,
  canManage,
  canReview,
  canDecideRequests,
  canObserve,
  canSeeOperations,
  canCreateStaff,
  staffUsername,
  staffRole,
  staffReason,
  staffBusy,
  staffError,
  staffPassword,
  staffCreated,
  createStaff,
  copyStaffPassword,
  shopQuery,
  roleLabel,
  requestableRoles,
  navigation,
  navigationGroups,
  endpointForPage,
  clearRestrictedContent,
  handleFailure,
  toggleVersion,
  selectedNeedsRevision,
  generateSuggestion,
  generateSelected,
  openBatchConfirm,
  submitBatch,
  loadSuggestionDetail,
  regenerateStale,
  loadVersionHistory,
  loadPage,
  refreshPermission,
  go,
  shopName,
  formatTime,
  statusLabel,
  statusTone,
  badgeTone,
  refresh,
  signout,
  openDecision,
  openVersion,
  closeDialog,
  decideRequest,
  submitPermissionRequest,
  revokeGrant,
  submitDialogAction,
  loadReviewDetail,
  loadJobDetail,
  openJobAction,
  runJobOperation,
  openGrantRevoke,
  decideVersion,
  runVersionOperation,
  importPackage,
  onManifestChange,
  onFilesChange,
  themeChoice,
  themeLabel,
  toggleTheme,
  browserAuth,
}
provide(AdminContextKey, admin as unknown as AdminContext)

</script>

<template>
  <AdminShell>
    <div v-if="error" class="alert" role="alert"><span>{{ error }}</span><button type="button" @click="refresh">重试</button></div>
    <div v-if="notice" class="notice" role="status">{{ notice }}</div>
    <section v-if="loading" class="state-panel" role="status"><strong>正在读取授权范围内的数据</strong><span>状态以服务端响应为准。</span></section>
    <section v-else-if="unavailable" class="state-panel unavailable"><strong>管理服务暂不可用</strong><span>没有使用缓存内容替代服务端状态。</span><button class="primary" @click="refresh">重试</button></section>
    <section v-else-if="!session" class="state-panel unavailable"><strong>{{ identityStatus === 403 ? '当前账号无访问权限' : '请登录管理台' }}</strong><span>{{ identityStatus === 403 ? '权限已变化或被撤销，请联系管理员。' : '登录后读取服务端授权范围。' }}</span><button v-if="browserAuth.enabled && identityStatus === 401" class="primary" @click="login('admin')">登录</button></section>
    <section v-else-if="navigation.length === 0" class="state-panel unavailable"><strong>当前账号没有管理权限</strong><span>页面访问范围由 Go 服务端授权决定。</span></section>
    <template v-else-if="!loading">
      <AdminPageRouter/>
      <div v-if="batchResults.length" class="content-area"><div class="section-head"><h2>批量确认结果</h2></div><p>成功 {{batchResultCounts.success}} · stale {{batchResultCounts.stale}} · 失败 {{batchResultCounts.failed}}</p><Table><thead><tr><th>版本</th><th>结果</th><th>代码 / 说明</th><th>操作</th></tr></thead><tbody><tr v-for="result in batchResults" :key="result.version_id"><td>{{result.version_id}}</td><td>{{result.ok?'成功':'失败'}}</td><td>{{result.code??'—'}} {{result.detail??''}}</td><td><Button v-if="result.code==='suggestion_stale'" variant="text" @click="regenerateStale(result)">重新生成建议</Button></td></tr></tbody></Table></div>
    </template>
  </AdminShell>
  <AdminDialogs/>

</template>
