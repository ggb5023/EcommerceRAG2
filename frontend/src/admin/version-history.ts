export type VersionHistoryRow={version_id:string;document_id:string;title?:string;status:string;review_status:string;created_at?:string;activated_at?:string}
export function versionHistoryView(rows:VersionHistoryRow[]){
 return rows.map(row=>({...row,status_label:row.status==='active'?'已发布':row.status==='revoked'?'已撤回':row.status==='superseded'?'已被替代':'待处理',review_label:row.review_status==='approved'?'已审核':row.review_status==='rejected'?'已驳回':'待审核',published_at:row.activated_at??null}))
}
