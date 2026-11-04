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
