export type PreviewChunk={chunk_index:number;content:string;chunk_id?:string;heading_path?:string;content_type?:string;split_reason?:string;source_position?:Record<string,number>|null;char_start?:number|null;char_end?:number|null;parse_hints?:{kind:string;code:string}[];table_body?:string[][];table_caption?:string}
export function sourceLocation(chunk:PreviewChunk):string{
 const p=chunk.source_position
 if(!p)return '来源位置不可用；需要完整位置请重新提交为新版本'
 if(p.block_index)return `文档块 ${p.block_index}${p.table?` · 表格 ${p.table}`:''}`
 if(p.line_start&&p.line_end)return `${p.record_number?`数据记录 ${p.record_number} · `:''}物理行 ${p.line_start}–${p.line_end}`
 return '来源位置不可用；需要完整位置请重新提交为新版本'
}
