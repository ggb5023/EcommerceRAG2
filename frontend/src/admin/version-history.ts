export type VersionHistoryRow={version_id:string;document_id:string;title?:string;status:string;review_status:string;created_at?:string;activated_at?:string;effective_from?:string|null;effective_to?:string|null;validity_status?:string;days_until_expiry?:number}
export function validityLabel(status?:string){
 if(status==='expired')return '已过期'
 const match=status?.match(/^expiring_in_(\d+)$/)
 return match?`${match[1]} 天内失效`:''
}
export function validityTone(status?:string):'danger'|'warning'|'muted'{
 return status==='expired'?'danger':status?.startsWith('expiring_in_')?'warning':'muted'
}
export function validityRange(from?:string|null,to?:string|null){
 return `${from||'—'} 至 ${to||'不限'}`
}
export function versionHistoryView(rows:VersionHistoryRow[]){
 return rows.map(row=>({...row,status_label:row.status==='active'?'已发布':row.status==='revoked'?'已撤回':row.status==='superseded'?'已被替代':'待处理',review_label:row.review_status==='approved'?'已审核':row.review_status==='rejected'?'已驳回':'待审核',published_at:row.activated_at??null,validity_label:validityLabel(row.validity_status),validity_tone:validityTone(row.validity_status)}))
}
