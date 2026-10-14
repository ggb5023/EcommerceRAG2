import {expect,it} from 'vitest'
import {sourceLocation} from './knowledge-preview'
it('distinguishes DOCX blocks, CSV records and missing legacy locations',()=>{
 const base={chunk_index:0,content:'text'}
 expect(sourceLocation({...base,source_position:{block_index:4,table:2,page_no:99}})).toBe('文档块 4 · 表格 2')
 expect(sourceLocation({...base,source_position:{record_number:2,line_start:3,line_end:5}})).toBe('数据记录 2 · 物理行 3–5')
 expect(sourceLocation({...base,char_start:0,char_end:4})).toContain('来源位置不可用')
 expect(sourceLocation({...base,source_position:{char_start:0,char_end:4}})).toContain('重新提交为新版本')
})
