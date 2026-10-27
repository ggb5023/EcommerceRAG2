export type SuggestionReason={code:string;detail?:string}
export type BatchItemResult={version_id:string;suggestion_id?:string;ok:boolean;code?:string;detail?:string}
export function suggestionLabel(decision?: string|null): string {
  if (decision === 'approved') return 'AI 建议：可审核'
  if (decision === 'needs_revision') return 'AI 建议：需修改'
  return '无建议'
}
export function reasonSummary(reasons: SuggestionReason[]|undefined, limit=2): string {
  return (reasons??[]).slice(0,limit).map(r=>r.code+(r.detail?`：${r.detail}`:'')).join('；')
}
export function validBatchReason(value: string): boolean {
  const length=value.trim().length
  return length>=10&&length<=2000
}
export function batchCounts(results: BatchItemResult[]) {
  return {success:results.filter(r=>r.ok).length,stale:results.filter(r=>r.code==='suggestion_stale').length,
    failed:results.filter(r=>!r.ok&&r.code!=='suggestion_stale').length}
}
export function suggestionEvidence(content:{chunk_id?:string;content:string}[]|undefined, ids:string[]|undefined){
  const wanted=new Set(ids??[]);return (content??[]).filter(c=>c.chunk_id!==undefined&&wanted.has(c.chunk_id))
}
