import {describe,expect,it} from 'vitest'
import {canRevokeImport,commandStateLabel,hasBlockingPrecheck,rowErrors,rowErrorsTruncated} from './import-wizard-state'
describe('import wizard state',()=>{
 it('labels command states including failures',()=>{
  expect(commandStateLabel('running')).toBe('处理中')
  expect(commandStateLabel('done')).toBe('已完成')
  expect(commandStateLabel('failed','embedding_failed')).toBe('失败：embedding_failed')
 })
 it('only revokes unpublished batches',()=>{
  expect(canRevokeImport('staged',null)).toBe(true)
  expect(canRevokeImport('confirmed',null)).toBe(true)
  expect(canRevokeImport('revoked','2026-10-27T00:00:00Z')).toBe(false)
  expect(canRevokeImport('expired',null)).toBe(false)
 })
 it('finds row errors and blocks confirmation only on block severity',()=>{
  const warn={precheck_result:[],precheck_detail:{row_errors:[{row:2,column:'price_cents',code:'unknown_column',severity:'warn',value:'',message:'',suggested_action:''}],truncated:true}}
  const block={precheck_result:[],precheck_detail:{row_errors:[{row:3,column:'sku_code',code:'duplicate_sku',severity:'block',value:'SKU-1',message:'',suggested_action:''}]}}
  expect(rowErrors(warn)).toHaveLength(1)
  expect(rowErrorsTruncated(warn)).toBe(true)
  expect(hasBlockingPrecheck(warn)).toBe(false)
  expect(hasBlockingPrecheck(block)).toBe(true)
  expect(hasBlockingPrecheck({precheck_result:[{severity:'block'}]})).toBe(true)
 })
})
