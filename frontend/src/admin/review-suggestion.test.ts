import {describe,expect,it} from 'vitest'
import {batchCounts,reasonSummary,suggestionEvidence,suggestionLabel,validBatchReason} from './review-suggestion'
describe('review suggestion ui',()=>{
 it('renders decisions without claiming approval',()=>{
  expect(suggestionLabel('approved')).toBe('AI 建议：可审核')
  expect(suggestionLabel('needs_revision')).toBe('AI 建议：需修改')
  expect(suggestionLabel()).toBe('无建议')
  expect(suggestionLabel('approved')).not.toContain('已批准')
 })
 it('summarizes first two reasons and validates reason length',()=>{
  expect(reasonSummary([{code:'a',detail:'x'},{code:'b'},{code:'c'}])).toBe('a：x；b')
  expect(validBatchReason(' too short ')).toBe(false)
  expect(validBatchReason('1234567890')).toBe(true)
 })
 it('classifies partial batch results',()=>{
  expect(batchCounts([{version_id:'1',ok:true},{version_id:'2',ok:false,code:'suggestion_stale'},{version_id:'3',ok:false,code:'review_command_failed'}])).toEqual({success:1,stale:1,failed:1})
 })
 it('maps evidence ids to preview snippets',()=>{
  expect(suggestionEvidence([{chunk_id:'a',content:'A'},{chunk_id:'b',content:'B'}],['b'])).toEqual([{chunk_id:'b',content:'B'}])
 })
})
