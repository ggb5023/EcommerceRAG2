import { describe, expect, it } from 'vitest'
import { messageCopyState } from './customer-api'

describe('customer message copy state', () => {
  it('uses the server copyable flag and reason', () => {
    expect(messageCopyState({ source: 'assistant', can_copy: true, blocked_reason: '' })).toEqual({ canCopy: true, reason: '' })
    expect(messageCopyState({ source: 'assistant', can_copy: false, blocked_reason: 'no_citable_evidence' }))
      .toEqual({ canCopy: false, reason: '没有可引用的资料依据' })
    expect(messageCopyState({ source: 'assistant', can_copy: false, blocked_reason: 'needs_clarification' }))
      .toEqual({ canCopy: false, reason: '需要追问' })
  })

  it('does not infer copyability when the server field is missing', () => {
    expect(messageCopyState({ source: 'assistant' })).toEqual({ canCopy: false, reason: '当前回答不可复制' })
    expect(messageCopyState({ source: 'agent', can_copy: true })).toEqual({ canCopy: false, reason: '仅 AI 回答可复制' })
  })
})
