import {describe,expect,it} from 'vitest'
import {versionHistoryView} from './version-history'
describe('version history',()=>{
 it('labels status and keeps publication time',()=>{
  const rows=versionHistoryView([{version_id:'1',document_id:'doc',status:'active',review_status:'approved',activated_at:'2026-10-27T00:00:00Z'},{version_id:'2',document_id:'doc',status:'revoked',review_status:'pending'}])
  expect(rows[0].status_label).toBe('已发布')
  expect(rows[0].published_at).toBe('2026-10-27T00:00:00Z')
  expect(rows[1].status_label).toBe('已撤回')
 })
})
