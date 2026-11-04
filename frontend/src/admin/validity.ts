import { ApiError, request } from './api'

export type ValidityDetail = {
  version_id: string
  effective_from?: string | null
  effective_to?: string | null
  fencing_epoch?: number
}

export type ValiditySession = { permission_revision: string }

export function validityBody(detail: ValidityDetail, session: ValiditySession, from: string, to: string, reason: string) {
  return {
    effective_from: from.trim() || null,
    effective_to: to.trim() || null,
    reason: reason.trim(),
    confirm: true,
    expected_fencing_epoch: Number(detail.fencing_epoch ?? 0),
    permission_revision: session.permission_revision,
  }
}

export async function submitValidity(detail: ValidityDetail, session: ValiditySession, from: string, to: string, reason: string) {
  const key = `validity-${detail.version_id}-${Date.now()}`
  return request<Record<string, unknown>>(`/admin/v1/merchant/versions/${detail.version_id}/validity`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
    body: JSON.stringify(validityBody(detail, session, from, to, reason)),
  })
}

export function validityError(error: unknown): string {
  if (error instanceof ApiError && error.status === 409) return '版本状态或 fencing epoch 已变化，请重新加载后再试。'
  return error instanceof Error ? error.message : '有效期保存失败。'
}
