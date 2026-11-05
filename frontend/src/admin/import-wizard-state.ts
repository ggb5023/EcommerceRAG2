export function commandStateLabel(state: string | undefined, error?: string): string {
  if (state === 'done' || state === 'awaiting_review') return '已完成'
  if (state === 'failed' || state === 'cancelled') return `失败${error ? `：${error}` : ''}`
  if (state === 'running' || state === 'pending') return '处理中'
  return state ? `状态：${state}` : '待确认'
}

export function canRevokeImport(status: string | undefined, revokedAt: string | null | undefined): boolean {
  return !revokedAt && (status === 'staged' || status === 'confirmed')
}

export type ImportRowError={row:number;column:string;code:string;severity:string;value:string;message:string;suggested_action:string}
export type PrecheckIssue={issue_code?:string;severity:string;snippet?:string;suggested_action?:string}
export type PrecheckItem={item_id?:string|number;precheck_detail?:{title?:string;row_errors?:ImportRowError[];truncated?:boolean;error_count?:number};precheck_result?:PrecheckIssue[]}
export type ImportPreview={items?:PrecheckItem[];status?:string}

export function rowErrors(item:PrecheckItem):ImportRowError[]{
  const rows=item?.precheck_detail?.row_errors
  return Array.isArray(rows)?rows:[]
}

export function hasBlockingPrecheck(item:PrecheckItem):boolean{
  return (item?.precheck_result??[]).some((issue)=>issue?.severity==='block')||rowErrors(item).some(issue=>issue.severity==='block')
}

export function rowErrorsTruncated(item:PrecheckItem):boolean{
  return item?.precheck_detail?.truncated===true
}
