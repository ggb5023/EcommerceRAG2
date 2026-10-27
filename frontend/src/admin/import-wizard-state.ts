export function commandStateLabel(state: string | undefined, error?: string): string {
  if (state === 'done' || state === 'awaiting_review') return '已完成'
  if (state === 'failed' || state === 'cancelled') return `失败${error ? `：${error}` : ''}`
  if (state === 'running' || state === 'pending') return '处理中'
  return state ? `状态：${state}` : '待确认'
}

export function canRevokeImport(status: string | undefined, revokedAt: string | null | undefined): boolean {
  return !revokedAt && (status === 'staged' || status === 'confirmed')
}
