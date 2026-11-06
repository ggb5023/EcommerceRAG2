import { inject, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import type { RouteLocationNormalizedLoaded, Router } from 'vue-router'
import type { AdminRoleCatalogEntry, AdminSession } from './api'
import type { PreviewChunk } from './knowledge-preview'
import type { BatchItemResult } from './review-suggestion'
import type { VersionHistoryRow } from './version-history'
import type { AdminPage } from './routes'
import type { ThemeChoice } from './theme'

export type AdminRow = {
  [key: string]: unknown
  version_id?: string
  item_id?: string | number
  payload_hash?: string
  retry_count?: number
  fencing_epoch?: number
  version_status?: string
  source_id?: string
  document_id?: string
  title?: string
  shop_id?: string
  status?: string
  review_status?: string
  source_hash?: string
  chunk_count?: number
  item_count?: number
  error_code?: string | null
  error?: string | null
  suggestion?: Suggestion
  grants?: Grant[]
  shop_ids?: string[]
  user_id?: string | number
  external_id?: string
  display_name?: string
  runtime_role?: string
  permission_revision?: string
  revoked?: boolean
  id?: string | number
  created_at?: string
  actor_user_id?: string | number
  action?: string
  resource_type?: string
  resource_id?: string
  resource_version?: string | number
  content_hash?: string
  reason?: string
  result?: string
  job_id?: string | number
  stage?: string
  pending?: number
  done?: number
  failed?: number
  manifest_sha256?: string
  dataset_sha256?: string | null
  requester_name?: string
  role?: string
  scope_kind?: string
  effective_from?: string | null
  effective_to?: string | null
  reviewed_by?: string | number
  documents?: number
  latest_import?: string
  uri?: string
  type?: string
  status_label?: string
  review_label?: string
  published_at?: string | null
  validity_status?: string
  components?: Record<string, { status?: string; version?: string; role?: string; detail?: string }>
  migration?: { current_version?: string | number; legacy_unverified?: string }
  ingestion?: { jobs?: number; awaiting_or_running?: number; failed?: number }
  error_rate?: { rate?: number; errors?: number; requests?: number }
  version?: { application?: string; revision?: string }
  scope?: { tenant_id?: string; shop_id?: string }
}

export type Suggestion = {
  id?: string
  decision?: string
  reasons?: { code: string; detail?: string }[]
  evidence_chunk_ids?: string[]
  rules_version?: string
}

export type Grant = {
  id?: string
  role: string
  scope_kind: string
  shop_ids?: string[]
  status?: string
  external_id?: string
  display_name?: string
}

export type Shop = AdminRow & {
  name?: string
  public_slug?: string
  welcome_message?: string
  customer_chat_enabled?: boolean
  timezone?: string
  customer_chat_url?: string
  customer_published_knowledge_available?: boolean
}

export type AdminDetail = AdminRow & { version_id: string; content?: PreviewChunk[] }

export type DialogTarget = {
  operation?: string
  shop_id?: string
  document_id?: string
  version_id?: string
  item_id?: string | number
  payload_hash?: string
  retry_count?: number
  version_status?: string
  source_id?: string
  source_hash?: string
  disclosure_class?: string
  external_allowed?: boolean
  fencing_epoch?: number
  display_name?: string
  external_id?: string
  role?: string
  scope_kind?: string
  shop_ids?: string[]
  requester_name?: string
  id?: string
  job_id?: string
  status?: string
  reason?: string
}

export type AdminContext = {
  [key: string]: unknown
  route: RouteLocationNormalizedLoaded
  router: Router
  session: Ref<AdminSession | null>
  data: Ref<AdminRow[]>
  detail: Ref<AdminDetail | null>
  versionHistory: Ref<VersionHistoryRow[]>
  suggestionFilter: Ref<string>
  generateDate: Ref<string>
  selectedVersionIds: Ref<string[]>
  batchDialog: Ref<boolean>
  batchReason: Ref<string>
  batchBusy: Ref<boolean>
  batchResults: Ref<BatchItemResult[]>
  batchResultCounts: Ref<{ success: number; stale: number; failed: number }>
  suggestionProgress: Ref<string>
  suggestionDetails: Ref<Record<string, AdminDetail>>
  trialShops: Ref<AdminRow[]>
  jobDetail: Ref<(AdminRow & { items?: AdminRow[] }) | null>
  loading: Ref<boolean>
  unavailable: Ref<boolean>
  error: Ref<string>
  identityStatus: Ref<number>
  actionBusy: Ref<boolean>
  notice: Ref<string>
  selectedShop: Ref<string>
  formReason: Ref<string>
  dialog: Ref<'request' | 'review' | 'version' | 'grant_revoke' | 'job' | null>
  dialogTarget: Ref<DialogTarget | null>
  reviewDecision: Ref<string>
  disclosure: Ref<string>
  manifestText: Ref<string>
  selectedFiles: Ref<File[]>
  manifestFile: Ref<File | null>
  requestRole: Ref<string>
  requestScope: Ref<string>
  requestShops: Ref<string[]>
  statusFilter: Ref<string>
  confirmationChecked: Ref<boolean>
  page: ComputedRef<AdminPage>
  title: ComputedRef<string>
  roles: ComputedRef<Set<string>>
  roleCatalog: ComputedRef<AdminRoleCatalogEntry[]>
  canAccess: ComputedRef<boolean>
  canManage: ComputedRef<boolean>
  canReview: ComputedRef<boolean>
  canDecideRequests: ComputedRef<boolean>
  canObserve: ComputedRef<boolean>
  canSeeOperations: ComputedRef<boolean>
  canCreateStaff: ComputedRef<boolean>
  staffUsername: Ref<string>
  staffRole: Ref<string>
  staffReason: Ref<string>
  staffBusy: Ref<boolean>
  staffError: Ref<string>
  staffPassword: Ref<string>
  staffCreated: Ref<string>
  shopQuery: ComputedRef<string>
  requestableRoles: ComputedRef<AdminRoleCatalogEntry[]>
  navigation: ComputedRef<{ label: string; page: AdminPage; visible: boolean }[]>
  themeChoice: Ref<ThemeChoice>
  themeLabel: (choice: ThemeChoice) => string
  browserAuth: { enabled: boolean; mustChangePassword: boolean }
  validBatchReason: (value: string) => boolean
  selectedNeedsRevision: () => number
  statusLabel: (value?: string) => string
  statusTone: (value?: string) => 'danger' | 'success' | 'warning' | 'muted'
  badgeTone: (value?: string) => 'danger' | 'success' | 'warning' | 'muted'
  roleLabel: (role: string) => string
  formatTime: (value?: string | null) => string
  createStaff: () => Promise<void>
  copyStaffPassword: () => Promise<void>
  clearRestrictedContent: (clearSession?: boolean) => void
  handleFailure: (error: unknown, fallback: string) => void
  toggleVersion: (versionId: string, checked: boolean) => void
  generateSuggestion: (row: AdminRow) => Promise<void>
  generateSelected: () => Promise<void>
  openBatchConfirm: () => void
  submitBatch: () => Promise<void>
  loadSuggestionDetail: (row: AdminRow) => Promise<void>
  regenerateStale: (result: BatchItemResult) => Promise<void>
  loadVersionHistory: () => Promise<void>
  loadPage: () => Promise<void>
  refreshPermission: () => Promise<void>
  go: (page: AdminPage) => void
  shopName: (value?: string) => string
  refresh: () => Promise<void>
  signout: () => Promise<void>
  openDecision: (target: AdminRow, decision: string, kind?: string) => void
  openVersion: (target: AdminRow, operation: string) => void
  closeDialog: () => void
  decideRequest: () => Promise<void>
  submitPermissionRequest: () => Promise<void>
  revokeGrant: () => Promise<void>
  submitDialogAction: () => Promise<void>
  loadReviewDetail: (row: AdminRow) => Promise<void>
  loadJobDetail: (row: AdminRow) => Promise<void>
  openJobAction: (row: AdminRow, operation: string) => void
  runJobOperation: () => Promise<void>
  openGrantRevoke: (row: AdminRow) => void
  decideVersion: () => Promise<void>
  runVersionOperation: () => Promise<void>
  importPackage: () => Promise<void>
  onManifestChange: (event: Event) => void
  onFilesChange: (event: Event) => void
  toggleTheme: () => void
  endpointForPage: () => string
}

export const AdminContextKey: InjectionKey<AdminContext> = Symbol('admin-context')

export function useAdminContext(): AdminContext {
  const context = inject(AdminContextKey)
  if (!context) throw new Error('admin context is missing')
  return context
}
