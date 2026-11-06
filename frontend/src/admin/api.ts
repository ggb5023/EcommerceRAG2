import { csrfHeaders } from '../auth'

export type AdminSession = {
  tenant_id: string
  user_id: string
  runtime_role: string
  shop_id: string
  allowed_shop_ids: string[]
  permission_revision: string
  admin_roles: string[]
  admin_role_catalog: AdminRoleCatalogEntry[]
  is_mock: boolean
}

export type AdminRoleCatalogEntry = {
  role_key: string
  storage_role: string | null
  display_name: string
  catalog_version: string
  enabled: boolean
  requestable: boolean
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

const messages: Record<number, string> = {
  401: '登录状态无效，请重新建立受限会话。',
  403: '当前账号没有该操作的授权范围。',
  404: '请求的记录不存在或已不在授权范围内。',
  409: '数据或权限版本已变化，请刷新后重新确认。',
  413: '上传文件超过允许大小。',
  503: '服务暂不可用，数据未确认成功。',
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  if (init.method && init.method !== 'GET') init = { ...init, headers: { ...csrfHeaders(), ...Object.fromEntries(new Headers(init.headers)) } }
  const response = await fetch(path, { credentials: 'same-origin', ...init })
  if (!response.ok) {
    let detail = ''
    try {
      const payload = await response.json() as { error?: { code?: string } }
      detail = payload.error?.code ? ` (${payload.error.code})` : ''
    } catch {
      detail = ''
    }
    throw new ApiError(response.status, `${messages[response.status] ?? '请求失败。'}${detail}`)
  }
  return response.json() as Promise<T>
}

export type ResolutionOutcome = {
  turns_total: number
  scored_total: number
  solved: number
  clarified: number
  abandoned: number
  failed: number
  cancelled: number
  pending: number
  unknown: number
  reasked_within_5m: number
  auto_rate: number | null
  assisted_rate: number | null
  truncated: boolean
  metric_gap: boolean
}

export type KnowledgeGap = {
  query: string
  normalized: string
  count: number
  last_seen: string
  reason: 'abandoned' | 'failed'
}

export type KnowledgeGapReport = {
  items: KnowledgeGap[]
  truncated: boolean
}

export type UsageRow = {
  slot?: string
  model?: string
  input_tokens: number
  output_tokens: number
  calls: number
  failed_calls: number
}

export type UsageReport = {
  input_tokens: number
  output_tokens: number
  calls: number
  failed_calls: number
  by_slot: UsageRow[]
  by_model: UsageRow[]
}

export type HandoffReport = {
  range: '7d' | '30d'
  window_start: string
  window_end: string
  handoff_conversations: number
  customer_conversations: number
  handoff_rate: number | null
}

export type InspectionItem = {
  id: number
  shop_id: string
  kind: 'expired_not_removed' | 'newer_version_conflict' | 'unanswered_high_frequency' | 'citation_invalid'
  severity: 'high' | 'medium' | 'low'
  status: 'open' | 'acknowledged' | 'resolved' | 'dismissed'
  subject_type: string
  subject_id: string
  evidence: Record<string, unknown>
  detected_at: string
  resolved_at: string | null
}

export type InspectionRunSummary = {
  run_id: string
  by_kind: Record<string, { inserted: number; updated: number; closed: number }>
}

const inspectionScope = (shopId = '') => shopId ? `shop_id=${encodeURIComponent(shopId)}` : ''

export type ReportSection = {
  status?: 'MEASURED' | 'NOT_MEASURED'
  reason?: string
  [key: string]: unknown
}

export type EvaluationReport = {
  input: ReportSection
  operations: ReportSection
  handoff: ReportSection
  quality: ReportSection
  tokens: ReportSection
}

export const getReport = () => get<EvaluationReport>('/admin/v1/report')

export type IntelObservation = {
  market: string
  category: string
  platform: string
  source_url: string
  title: string
  score: number
  published_date: string
  observed_at: string
  similarity: number
}

export type IntelWatchConfig = {
  id: number
  market: string
  category: string
  platform: string
  enabled: boolean
  created_at: string
}

export type IntelFilters = {
  market?: string
  category?: string
  platform?: string
  since?: string
  query?: string
}

export const getIntelObservations = (filters: IntelFilters = {}) => {
  const query = new URLSearchParams({ limit: '50' })
  for (const key of ['market', 'category', 'platform', 'since', 'query'] as const) {
    const value = filters[key]
    if (value) query.set(key, value)
  }
  return get<{ items: IntelObservation[]; query_degraded: boolean }>(
    `/admin/v1/intel/observations?${query.toString()}`,
  )
}
export const getIntelWatchConfigs = () =>
  get<{ items: IntelWatchConfig[] }>('/admin/v1/intel/watch-configs')
export const createIntelWatchConfig = (body: Omit<IntelWatchConfig, 'id' | 'created_at'>) =>
  post<IntelWatchConfig>('/admin/v1/intel/watch-configs', body)
export const updateIntelWatchConfig = (id: number, body: Partial<Pick<IntelWatchConfig, 'market' | 'category' | 'platform' | 'enabled'>>) =>
  request<IntelWatchConfig>(`/admin/v1/intel/watch-configs/${id}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  })

export const getInspections = (status = 'open', shopId = '') =>
  get<{ items: InspectionItem[] }>(`/admin/v1/inspections?status=${encodeURIComponent(status)}${inspectionScope(shopId) ? `&${inspectionScope(shopId)}` : ''}`)
export const getInspection = (id: number, shopId = '') =>
  get<InspectionItem>(`/admin/v1/inspections/${id}${inspectionScope(shopId) ? `?${inspectionScope(shopId)}` : ''}`)
export const runInspections = (shopId = '') =>
  post<InspectionRunSummary>(`/admin/v1/inspections/run${inspectionScope(shopId) ? `?${inspectionScope(shopId)}` : ''}`, {})
export const setInspectionStatus = (id: number, status: 'acknowledged' | 'resolved' | 'dismissed', shopId = '') =>
  post<InspectionItem>(`/admin/v1/inspections/${id}/status${inspectionScope(shopId) ? `?${inspectionScope(shopId)}` : ''}`, { status })

export const get = <T>(path: string) => request<T>(path)
export const getOutcomes = (range: '7d' | '30d' = '30d') =>
  get<ResolutionOutcome>(`/admin/v1/merchant/outcomes?range=${range}`)
export const getGaps = (range: '7d' | '30d' = '30d') =>
  get<KnowledgeGapReport>(`/admin/v1/merchant/gaps?range=${range}`)
export const getUsage = (range: '7d' | '30d' = '30d') =>
  get<UsageReport>(`/admin/v1/merchant/usage?range=${range}`)
export const getHandoff = (range: '7d' | '30d' = '30d') =>
  get<HandoffReport>(`/admin/v1/merchant/report/handoff?range=${range}`)

export async function downloadGaps(range: '7d' | '30d' = '30d'): Promise<{ blob: Blob; filename: string }> {
  const response = await fetch(`/admin/v1/merchant/gaps/export?range=${range}&download=1`, {
    method: 'POST', credentials: 'same-origin', headers: csrfHeaders(),
  })
  if (!response.ok) {
    let code = ''
    try { const payload = await response.json() as { error?: { code?: string } }; code = payload.error?.code ?? '' } catch { /* no body */ }
    throw new ApiError(response.status, `${messages[response.status] ?? '导出失败。'}${code ? ` (${code})` : ''}`)
  }
  const disposition = response.headers.get('Content-Disposition') ?? ''
  const match = /filename="?([^";]+)"?/i.exec(disposition)
  return { blob: await response.blob(), filename: match?.[1] ?? `gaps-${range}.jsonl` }
}

export function triggerDownload(blob: Blob, filename: string): void {
  const link = document.createElement('a')
  const url = URL.createObjectURL(blob)
  link.href = url
  link.download = filename
  link.click()
  URL.revokeObjectURL(url)
}
export const put = <T>(path: string, body: unknown) => request<T>(path, {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})
export const post = <T>(path: string, body: unknown) => request<T>(path, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify(body),
})
