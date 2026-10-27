import {describe,expect,it} from 'vitest'
import {canRevokeImport,commandStateLabel} from './import-wizard-state'
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
})
